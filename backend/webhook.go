package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// webhookClient is shared across deliveries: a per-message http.Transport never
// expires its idle connections and is never collected, so sockets and their
// readLoop goroutines accumulate on busy channels. Certificate verification is
// left at the default (on) — the payload carries the channel's verify token.
var webhookClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	},
}

type WebhookPayload struct {
	Action      string    `json:"action"`
	Message     Message   `json:"message"`
	Timestamp   time.Time `json:"timestamp"`
	VerifyToken string    `json:"verifyToken"`
}

func SendWebhook(ctx context.Context, slug string, action string, message *Message) {
	// Load per-channel config for webhook settings
	chCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Operator-level kill switch: the super admin can withdraw webhooks from a
	// tenant regardless of what the owner has configured.
	if ch, err := dbGetChannel(chCtx, slug); err == nil && !ch.Features.Webhook {
		return
	}

	cfg := getChannelConfig(chCtx, slug)
	if cfg.WebhookURL == "" {
		return
	}

	payload := WebhookPayload{
		Action:      action,
		Message:     *message,
		Timestamp:   time.Now(),
		VerifyToken: cfg.VerifyToken,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error converting webhook data to JSON: %v\n", err)
		return
	}

	httpCtx, httpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer httpCancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", cfg.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error creating webhook request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TheChannel-Webhook")

	resp, err := webhookClient.Do(req)
	if err != nil {
		log.Printf("Error sending webhook: %v\n", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Sent webhook for action '%s' on message %d. Response code: %d\n",
		action, message.ID, resp.StatusCode)
}
