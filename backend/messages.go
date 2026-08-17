package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

// maxMessagesPerRequest caps the client-supplied page size for /messages.
const maxMessagesPerRequest = 100

// maxStreamReadFailures bounds how long an SSE connection keeps retrying a
// stream read that consistently fails before the handler gives up.
const maxStreamReadFailures = 5

// streamIDRegex matches a Redis stream entry ID ("<ms>" or "<ms>-<seq>").
var streamIDRegex = regexp.MustCompile(`^\d+(-\d+)?$`)

func getMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	ch := channelFromCtx(r)

	offsetFromClient := r.URL.Query().Get("offset")
	limitFromClient := r.URL.Query().Get("limit")
	direction := r.URL.Query().Get("direction")

	offset, err := strconv.Atoi(offsetFromClient)
	if err != nil {
		offset = 0
	}

	// The limit drives a blocking Lua scan (ZREVRANGE + one HGETALL per member)
	// on a shared Redis, so an unbounded client value must not reach it.
	limit, err := strconv.Atoi(limitFromClient)
	if err != nil || limit <= 0 || limit > maxMessagesPerRequest {
		limit = 20
	}

	isAdmin := hasChannelRole(r, slug, RoleWriter)
	countViews := ch != nil && ch.Features.CountViews

	// ETag: skip expensive query if content hasn't changed since client's copy.
	// The body varies by viewer (admins see real authors and soft-deleted
	// posts), so the validator has to carry those inputs and the response must
	// never be reused across identities.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Cookie")
	if lm := getLastModified(ctx, slug); lm != "" {
		etag := `"` + lm + "-" + strconv.FormatBool(isAdmin) + "-" + strconv.FormatBool(countViews) + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	messages, err := funcGetMessageRange(ctx, slug, int64(offset), int64(limit), isAdmin, countViews, direction)
	if err != nil {
		log.Printf("Failed to get messages: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)

	addViewsToMessages(ctx, slug, countViews, messages)
}

func addMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var message Message
	var err error
	defer r.Body.Close()

	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	if message.ID, err = getMessageNextId(ctx, slug); err != nil {
		log.Printf("Failed to allocate message id: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	message.Type = body.Type
	message.Author = user.PublicName
	message.AuthorId = user.ID
	message.Timestamp = time.Now()
	message.Text = body.Text
	message.File = body.File
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, slug, &message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), slug, "create", &message)
	go pushFcmMessage(slug, &message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}

// canModifyMessage reports whether the current session may edit or delete a
// message written by authorId. Writers may only touch their own posts;
// moderators (and above, including super admins) may touch any message.
func canModifyMessage(r *http.Request, slug, authorId string) bool {
	if hasChannelRole(r, slug, RoleModerator) {
		return true
	}
	// System posts have no real author: the scheduler stamps "0" and the API
	// import path never sets the field at all. They are channel content rather
	// than anyone's personal post, so any writer may manage them — the caller is
	// already behind protectedWithChannelRole(RoleWriter, ...).
	if authorId == "" || authorId == "0" {
		return true
	}
	session, _ := store.Get(r, cookieName)
	user, ok := session.Values["user"].(Session)
	if !ok || user.ID == "" {
		return false
	}
	return authorId == user.ID
}

func updateMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var err error
	defer r.Body.Close()

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	// The message must already exist: setMessage would otherwise happily create
	// an unindexed hash from whatever ID the client sent.
	stored, err := dbGetMessageFields(ctx, slug, strconv.Itoa(body.ID))
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if !canModifyMessage(r, slug, stored["authorId"]) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Only the body of the message is editable — identity, ordering and counters
	// stay as stored, so they cannot be forged through the request payload.
	// Deleted is deliberately NOT restored: re-saving a deleted message is how
	// the composer republishes it, which the edit form promises to the user.
	body.Author = stored["author"]
	body.AuthorId = stored["authorId"]
	body.Views, _ = strconv.Atoi(stored["views"])
	if ts, perr := time.Parse(time.RFC3339Nano, stored["timestamp"]); perr == nil {
		body.Timestamp = ts
	}
	body.Reactions = nil
	if raw := stored["reactions"]; raw != "" {
		var reactions Reactions
		if json.Unmarshal([]byte(raw), &reactions) == nil {
			body.Reactions = reactions
		}
	}

	body.LastEdit = time.Now()

	// A storage failure must not read as success: the client replaces the
	// composer contents on a 2xx, so the user's edit would be lost silently.
	if err := setMessage(ctx, slug, &body, true); err != nil {
		log.Printf("Failed to update message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), slug, "update", &body)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func deleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	id := chi.URLParam(r, "id")

	stored, err := dbGetMessageFields(ctx, slug, id)
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if !canModifyMessage(r, slug, stored["authorId"]) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	idInt, _ := strconv.Atoi(id)
	message := Message{ID: idInt, Deleted: true}

	if err := funcDeleteMessage(ctx, slug, id); err != nil {
		log.Printf("Failed to delete message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), slug, "delete", &message)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

// getEvents serves a Server-Sent Events stream backed by a Redis Stream.
//
// Using Redis Streams instead of pub/sub enables:
//   - Horizontal scaling: multiple backend instances can all serve SSE
//   - Reconnection: clients send Last-Event-ID to resume without missing events
//   - Durability: stream retains the last ~1000 events (configurable in publishEvent)
func getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	slug := channelSlugFromCtx(r)
	streamKey := fmt.Sprintf("channel:%s:events", slug)

	// Support SSE reconnection: browser sends Last-Event-ID with the stream entry ID
	// from the last event it received. On fresh connect, start from "now".
	// A malformed value would make every XREAD fail, turning the loop below into
	// a busy retry against Redis, so anything that is not a stream ID is ignored.
	lastID := r.Header.Get("Last-Event-ID")
	if !streamIDRegex.MatchString(lastID) {
		lastID = "$"
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if _, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n"); err != nil {
		return
	}
	flusher.Flush()

	// Increment synchronously: the deferred decrement runs as soon as the loop
	// notices a dead client, which can happen before a spawned goroutine is even
	// scheduled, leaving the counter one below where it started.
	increaseCounterSSE(slug)
	defer decreaseCounterSSE(slug)

	clientCtx := r.Context()
	lastHeartbeat := time.Now()
	failures := 0
	const blockDuration = 5 * time.Second
	const heartbeatInterval = 25 * time.Second

	for {
		if clientCtx.Err() != nil {
			return
		}

		// Send heartbeat if it's been too long
		if time.Since(lastHeartbeat) >= heartbeatInterval {
			if _, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n"); err != nil {
				return
			}
			flusher.Flush()
			lastHeartbeat = time.Now()
		}

		// XREAD blocks until new messages arrive or timeout expires.
		// A short block duration lets us send periodic heartbeats and check for disconnect.
		streams, err := rdb.XRead(clientCtx, &redis.XReadArgs{
			Streams: []string{streamKey, lastID},
			Count:   50,
			Block:   blockDuration,
		}).Result()

		if clientCtx.Err() != nil {
			return
		}
		if err != nil {
			// redis.Nil = block timeout with no messages; other errors: brief pause
			// then retry, but a stream that keeps failing must not be retried
			// forever at 2 Hz.
			if err != redis.Nil {
				failures++
				if failures > maxStreamReadFailures {
					log.Printf("SSE stream %s: giving up after %d failed reads: %v\n", streamKey, failures, err)
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		failures = 0

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				data, _ := msg.Values["data"].(string)
				if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", lastID, data); err != nil {
					return
				}
			}
		}
		flusher.Flush()
	}
}
