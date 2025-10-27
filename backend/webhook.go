package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// var webhookURL string = os.Getenv("WEBHOOK_URL")
// var verifyToken string = os.Getenv("WEBHOOK_VERIFY_TOKEN")

type WebhookPayload struct {
	Action      string    `json:"action"`
	Message     Message   `json:"message"`
	Timestamp   time.Time `json:"timestamp"`
	VerifyToken string    `json:"verifyToken"`
}

func SendWebhook(ctx context.Context, action string, message *Message) {
	if settingConfig.WebhookURL == "" {
		return
	}

	payload := WebhookPayload{
		Action:      action,
		Message:     *message,
		Timestamp:   time.Now(),
		VerifyToken: settingConfig.VerifyToken,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error converting webhook data to JSON: %v\n", err)
		return
	}

	httpCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", settingConfig.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error creating webhook request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TheChannel-Webhook")

	// Warning! Default is not secure
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending webhook: %v\n", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Sent webhook for action '%s' on message %d. Response code: %d\n",
		action, message.ID, resp.StatusCode)
}
