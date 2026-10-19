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

func isAllowedEmoji(ctx context.Context, slug, emoji string) bool {
	list, err := dbGetEmojisList(ctx, slug)
	if err != nil {
		return false
	}
	return slices.Contains(list, emoji)
}

func setReactions(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, cookieName)
	userId := session.Values["user"].(Session).ID

	slug := channelSlugFromCtx(r)

	var req struct {
		MessageID int    `json:"messageId"`
		Emoji     string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if req.MessageID <= 0 || !isAllowedEmoji(ctx, slug, req.Emoji) {
		http.Error(w, "Invalid message ID or reactions", http.StatusBadRequest)
		return
	}

	if err := setReaction(ctx, slug, req.MessageID, req.Emoji, userId); err != nil {
		http.Error(w, "Failed to set reactions", http.StatusInternalServerError)
		return
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getEmojisList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	list, err := dbGetEmojisList(ctx, slug)
	if err != nil {
		http.Error(w, "Failed to get emojis list", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func setEmojis(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	req := struct {
		Emojis []string `json:"emojis"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if err := dbSetEmojisList(ctx, slug, req.Emojis); err != nil {
		http.Error(w, "Failed to set emojis", http.StatusInternalServerError)
		return
	}

	var res Response
	res.Success = true

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
