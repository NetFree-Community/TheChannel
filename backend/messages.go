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
	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

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

	messages, err := GetMessageRange(ctx, int64(offset), int64(limit), user.IsAdmin)
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

	AddViewsToMessages(ctx, messages)
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

	message.ID = GetMessageNextId(ctx)
	message.Type = body.Type
	message.Author = user.Username
	message.Timestamp = time.Now()
	message.Text = body.Text
	message.File = body.File
	message.Views = 0

	if err = SetMessage(ctx, message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), "create", message)

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

	if err := SetMessage(ctx, body, true); err != nil {
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

	if err := DeleteMessage(ctx, id); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	go SendWebhook(context.Background(), "delete", message)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func getEvents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientCtx := r.Context()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	pubsub := rdb.Subscribe(ctx, "events")
	defer pubsub.Close()

	for {
		select {
		case <-clientCtx.Done():
			return
		case msg, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			w.(http.Flusher).Flush()
		}
	}
}
