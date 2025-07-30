package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"
)

type Reactions map[string]int

func (r Reactions) MarshalBinary() ([]byte, error) {
	return json.Marshal(r)
}

var emojis []string = []string{}

func init() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e, err := dbGetEmojisList(ctx)
	if err != nil {
		panic("Failed to load emojis list: " + err.Error())
	}

	emojis = e
}

func isAllowedEmoji(emoji string) bool {
	return slices.Contains(emojis, emoji)
}

func setReactions(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, cookieName)
	userId := session.Values["user"].(Session).ID

	var req struct {
		MessageID int    `json:"messageId"`
		Emoji     string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if req.MessageID <= 0 || !isAllowedEmoji(req.Emoji) {
		http.Error(w, "Invalid message ID or reactions", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := setReaction(ctx, req.MessageID, req.Emoji, userId); err != nil {
		http.Error(w, "Failed to set reactions", http.StatusInternalServerError)
		return
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getEmojisList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(emojis)
}

func setEmojis(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := struct {
		Emojis []string `json:"emojis"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// if len(req.Emojis) == 0 {
	// 	http.Error(w, "No emojis provided", http.StatusBadRequest)
	// 	return
	// }

	if err := dbSetEmojisList(ctx, req.Emojis); err != nil {
		http.Error(w, "Failed to set emojis", http.StatusInternalServerError)
		return
	}

	emojis = req.Emojis

	var res Response
	res.Success = true

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
