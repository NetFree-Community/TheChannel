package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// PublicFeatures is the subset of ChannelFeatures the UI needs to know about:
// exactly the toggles requireFeature gates a user-visible action on. Without
// them the client offers a button the backend then answers with 403.
type PublicFeatures struct {
	Reactions         bool `json:"reactions"`
	FileUploads       bool `json:"fileUploads"`
	Reports           bool `json:"reports"`
	ScheduledMessages bool `json:"scheduledMessages"`
}

type Channel struct {
	Id                      string         `json:"id"`
	Name                    string         `json:"name"`
	Description             string         `json:"description"`
	CreatedAt               time.Time      `json:"created_at"`
	LogoUrl                 string         `json:"logoUrl"`
	Views                   int64          `json:"views"`
	RequireAuthForViewFiles bool           `json:"require_auth_for_view_files"`
	ContactUs               string         `json:"contact_us"`
	Features                PublicFeatures `json:"features"`
}

func getChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	ch := channelFromCtx(r)

	amount, err := dbGetUsersAmount(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	var channel Channel
	if ch != nil {
		channel.Id = ch.Slug
		channel.Name = ch.Name
		channel.Description = ch.Description
		channel.CreatedAt = ch.CreatedAt
		channel.LogoUrl = ch.LogoUrl
		channel.RequireAuthForViewFiles = ch.Features.RequireAuthFiles
		channel.ContactUs = ch.ContactUs
		channel.Features = PublicFeatures{
			Reactions:         ch.Features.Reactions,
			FileUploads:       ch.Features.FileUploads,
			Reports:           ch.Features.Reports,
			ScheduledMessages: ch.Features.ScheduledMessages,
		}
	}
	channel.Views = amount

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

func editChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	// ContactUs is a pointer so an absent field (the shape the current client
	// posts) can be told apart from a deliberately cleared one. Only the latter
	// may overwrite the stored value.
	type Request struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		LogoUrl     string  `json:"logoUrl"`
		ContactUs   *string `json:"contactUs"`
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	hashKey := "channel:" + slug
	args := []any{"name", req.Name, "description", req.Description, "logoUrl", req.LogoUrl}
	if req.ContactUs != nil {
		args = append(args, "contactUs", *req.ContactUs)
	}
	if _, err := rdb.HSet(ctx, hashKey, args...).Result(); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	res := Response{Success: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
