package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi"
)

func getMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	offsetFromClient := r.URL.Query().Get("offset")
	limitFromClient := r.URL.Query().Get("limit")

	offset, err := strconv.Atoi(offsetFromClient)
	if err != nil {
		offset = 0
	}

	limit, err := strconv.Atoi(limitFromClient)
	if err != nil {
		limit = 20
	}

	isAuthenticated := false
	session, err := store.Get(r, cookieName)
	if err == nil {
		if _, ok := session.Values["user"].(Session); ok {
			isAuthenticated = true
		}
	}

	messages, err := funcGetMessageRange(ctx, int64(offset), int64(limit), checkPrivilege(r, Writer), settingConfig.CountViews, isAuthenticated)
	if err != nil {
		log.Printf("Failed to get messages: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	res := struct {
		Messages []Message `json:"messages"`
		HasMore  bool      `json:"hasMore"`
	}{
		Messages: messages,
		HasMore:  len(messages) >= limit,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)

	addViewsToMessages(ctx, messages)
}

func addMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

	for _, regex := range settingConfig.RegexReplace {
		t := regex.Pattern.ReplaceAllString(body.Text, regex.Replace)
		body.Text = t
	}

	message.ID = getMessageNextId(ctx)
	message.Type = body.Type
	message.Author = user.PublicName
	message.Timestamp = time.Now()
	message.Text = body.Text
	message.File = body.File
	message.Views = 0
	message.ReplyTo = body.ReplyTo
	message.IsThread = body.IsThread

	if err = setMessage(ctx, message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), "create", message)
	go pushFcmMessage(message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}

func updateMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var err error
	defer r.Body.Close()

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	body.LastEdit = time.Now()

	if err := setMessage(ctx, body, true); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	go SendWebhook(context.Background(), "update", body)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func deleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := chi.URLParam(r, "id")

	idInt, _ := strconv.Atoi(id)
	message := Message{ID: idInt, Deleted: true}

	if err := funcDeleteMessage(ctx, id); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	go SendWebhook(context.Background(), "delete", message)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	clientCtx := r.Context()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	pubsub := rdb.Subscribe(r.Context(), "events")
	defer pubsub.Close()

	if _, err := pubsub.Receive(clientCtx); err != nil {
		http.Error(w, "Failed to subscribe to events", http.StatusInternalServerError)
		return
	}

	for {
		select {
		case <-clientCtx.Done():
			return

		case <-heartbeat.C:
			_, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n")
			if err != nil {
				return
			}
			flusher.Flush()

		case msg, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
