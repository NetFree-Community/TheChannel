package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctxGet, cancelGet := context.WithTimeout(context.Background(), 5*time.Second)
			list, err := dbGetScheduledMessages(ctxGet)
			cancelGet()
			if err != nil {
				continue
			}

			now := time.Now()
			newList := make([]Message, 0)
			for _, msg := range *list {
				if msg.Timestamp.Before(now) {
					go func(m *Message) {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()

						m.ID = getMessageNextId(ctx)
						m.Timestamp = time.Now()
						m.Author = "Scheduled"
						m.AuthorId = "0"
						setMessage(ctx, m, false)
						go SendWebhook(context.Background(), "create", m)
						go pushFcmMessage(m)
					}(&msg)
					//*list = slices.Delete(*list, i, i+1)
				} else {
					newList = append(newList, msg)
				}
			}

			ctxSave, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
			dbSaveScheduledMessages(ctxSave, &newList)
			cancelSave()
		}
	}()
}

func getScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	messages, err := dbGetScheduledMessages(ctx)
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

	var messages []Message
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, "error decoding messages", http.StatusBadRequest)
		return
	}

	if err := dbSaveScheduledMessages(ctx, &messages); err != nil {
		http.Error(w, "error saving messages", http.StatusInternalServerError)
		return
	}

	var res = Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
