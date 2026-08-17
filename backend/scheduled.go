package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			runScheduledMessages()
		}
	}()
}

// runScheduledMessages only processes channels that have at least one message
// due before now, using the "scheduled:due_channels" sorted set (score = earliest
// due timestamp). This avoids querying every channel every minute.
func runScheduledMessages() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := float64(time.Now().Unix())

	// Get all channels with at least one message due by now
	slugs, err := rdb.ZRangeByScore(ctx, "scheduled:due_channels", &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", now),
	}).Result()
	if err != nil || len(slugs) == 0 {
		return
	}

	for _, slug := range slugs {
		slug := slug

		// Claim the channel for this tick. Every instance runs this ticker, and
		// dispatch is a read-then-write over the pending list with no atomicity,
		// so without a claim two replicas post the same message twice.
		lockKey := "scheduled:lock:" + slug
		ok, lerr := rdb.SetNX(ctx, lockKey, 1, 55*time.Second).Result()
		if lerr != nil || !ok {
			continue
		}

		ctxGet, cancelGet := context.WithTimeout(context.Background(), 5*time.Second)
		list, err := dbGetScheduledMessages(ctxGet, slug)
		cancelGet()
		if err != nil {
			rdb.Del(ctx, lockKey)
			continue
		}

		nowTime := time.Now()
		newList := make([]Message, 0)
		for _, msg := range *list {
			if !msg.Timestamp.Before(nowTime) {
				newList = append(newList, msg)
				continue
			}

			// Posted synchronously: the save below erases everything not in
			// newList, so a message that failed to post must stay pending
			// instead of vanishing with only a log line.
			postCtx, postCancel := context.WithTimeout(context.Background(), 5*time.Second)
			id, ierr := getMessageNextId(postCtx, slug)
			if ierr != nil {
				log.Printf("Failed to allocate message id for scheduled post on %s: %v\n", slug, ierr)
				postCancel()
				newList = append(newList, msg)
				continue
			}
			m := msg
			m.ID = id
			m.Timestamp = time.Now()
			m.Author = "Scheduled"
			m.AuthorId = "0"
			if serr := setMessage(postCtx, slug, &m, false); serr != nil {
				log.Printf("Failed to post scheduled message on %s: %v\n", slug, serr)
				postCancel()
				newList = append(newList, msg)
				continue
			}
			postCancel()

			go SendWebhook(context.Background(), slug, "create", &m)
			go pushFcmMessage(slug, &m)
		}

		ctxSave, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
		dbSaveScheduledMessages(ctxSave, slug, &newList) // also updates the sorted set
		cancelSave()

		rdb.Del(ctx, lockKey)
	}
}

func getScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	messages, err := dbGetScheduledMessages(ctx, slug)
	if err != nil {
		http.Error(w, "error getting messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func updateScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()

	slug := channelSlugFromCtx(r)

	var messages []Message
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, "error decoding messages", http.StatusBadRequest)
		return
	}

	// A zero timestamp would be stored as a permanently-overdue entry and take
	// the channel out of the scheduler's due set (see dbSaveScheduledMessages).
	for _, m := range messages {
		if m.Timestamp.IsZero() {
			http.Error(w, "scheduled message requires a timestamp", http.StatusBadRequest)
			return
		}
	}

	if err := dbSaveScheduledMessages(ctx, slug, &messages); err != nil {
		http.Error(w, "error saving messages", http.StatusInternalServerError)
		return
	}

	var res = Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
