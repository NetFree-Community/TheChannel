package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi"
)

func addNewPost(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !slugRegex.MatchString(slug) {
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := getChannelConfig(ctx, slug)
	// Unauthenticated route: an unset key must fail closed, and the comparison
	// against the configured secret must not vary with how much of it matched.
	key := r.Header.Get("X-API-Key")
	if cfg.ApiSecretKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(cfg.ApiSecretKey)) != 1 {
		http.Error(w, "error", http.StatusUnauthorized)
		return
	}

	var message Message
	var err error
	defer r.Body.Close()

	// The decoded text is stored verbatim and broadcast to every SSE client, so
	// an unbounded body must not reach it (same cap style as submitChannelRequest).
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	if len(body.Text) > 100_000 {
		http.Error(w, "text too long", http.StatusBadRequest)
		return
	}

	if message.ID, err = getMessageNextId(ctx, slug); err != nil {
		log.Printf("Failed to allocate message id: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	message.Type = "md" //body.Type
	message.Author = body.Author
	// The timestamp is the sort score. A missing one decodes to the zero time
	// and buries the post at the bottom of every listing; a far-future one pins
	// it to the top forever. Backdated imports stay legitimate.
	if body.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	} else if body.Timestamp.After(time.Now().Add(time.Hour)) {
		http.Error(w, "timestamp out of range", http.StatusBadRequest)
		return
	} else {
		message.Timestamp = body.Timestamp
	}
	message.Text = body.Text
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, slug, &message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}
