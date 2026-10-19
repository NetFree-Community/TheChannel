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

	// Wrong keys are rationed per client and channel; a client that has used
	// up its failures is refused before the key is even looked at, so the
	// key cannot be guessed at network speed. Correct keys cost nothing.
	authLimiter := importAuthLimiter(clientKey(r) + ":" + slug)
	if authLimiter.Tokens() < 1 {
		http.Error(w, "too many failed attempts — please wait", http.StatusTooManyRequests)
		return
	}

	// This route is mounted at the router root, outside the /api/channel/{slug}
	// group, so channelMiddleware never runs for it — and channelMiddleware is
	// where the super admin's kill switch lives. Without this check, disabling a
	// channel stopped every browser-facing route while the owner's API key kept
	// ingesting posts, publishing to SSE and firing the channel's webhook. The
	// check is repeated here rather than by remounting the route because the
	// API key, not a session, is what authenticates it.
	//
	// It runs before the key check: a mistyped slug has no configured key, so
	// it used to answer 401 and burn the failure budget of an integrator whose
	// key was fine. A channel's existence is public (/info), not a secret.
	channel, err := dbGetChannel(ctx, slug)
	if err != nil {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}
	if channel.Features.Disabled {
		http.Error(w, "channel_disabled", http.StatusForbidden)
		return
	}

	cfg := getChannelConfig(ctx, slug)
	// Unauthenticated route: an unset key must fail closed, and the comparison
	// against the configured secret must not vary with how much of it matched.
	key := r.Header.Get("X-API-Key")
	if cfg.ApiSecretKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(cfg.ApiSecretKey)) != 1 {
		authLimiter.Allow()
		http.Error(w, "error", http.StatusUnauthorized)
		return
	}

	var message Message
	defer r.Body.Close()

	// The decoded text is stored verbatim and broadcast to every SSE client, so
	// an unbounded body must not reach it.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	if len(body.Text) > maxMessageTextLen {
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

	// Same fan-out as a post from the composer or the scheduler: the docs
	// promise the webhook on every creation, and readers who subscribed to
	// push expect one for content that appears in the feed. A backdated import
	// is an archive being copied in, not news, so it fires the webhook but
	// not a push.
	go SendWebhook(context.Background(), slug, "create", &message)
	if time.Since(message.Timestamp) < time.Hour {
		go pushFcmMessage(slug, &message)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}
