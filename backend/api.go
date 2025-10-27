package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func addNewPost(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("X-API-Key")
	if key != settingConfig.ApiSecretKey {
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	message := &Message{}
	var err error
	defer r.Body.Close()

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	message.ID = getMessageNextId(ctx)
	message.Type = "md" //body.Type
	message.Author = body.Author
	message.Timestamp = body.Timestamp
	message.Text = body.Text
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}
