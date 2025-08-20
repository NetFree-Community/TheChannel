package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/icza/dyno"
)

type Channel struct {
	Id                      int       `json:"id"`
	Name                    string    `json:"name"`
	Description             string    `json:"description"`
	CreatedAt               time.Time `json:"created_at"`
	LogoUrl                 string    `json:"logoUrl"`
	Views                   int64     `json:"views"`
	RequireAuthForViewFiles bool      `json:"require_auth_for_view_files"`
	ContactUs               string    `json:"contact_us"`
}

func getChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	amount, err := dbGetUsersAmount(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	amount, _ = dyno.GetInteger(amount)

	c, err := getChannelDetails(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	var channel Channel
	channel.Id, _ = strconv.Atoi(c["id"])
	channel.Name = c["name"]
	channel.Description = c["description"]
	channel.CreatedAt, _ = time.Parse(time.RFC3339, c["created_at"])
	channel.Views = amount //strconv.Atoi(c["views"])
	channel.LogoUrl = c["logoUrl"]
	channel.RequireAuthForViewFiles = settingConfig.RequireAuthForViewFiles
	channel.ContactUs = settingConfig.ContactUs

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

func editChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type Request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		LogoUrl     string `json:"logoUrl"`
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	if _, err := rdb.HSet(ctx, "channel:1", "name", req.Name, "description", req.Description, "logoUrl", req.LogoUrl).Result(); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	res := Response{Success: true}
	json.NewEncoder(w).Encode(res)
}

func registeringEmail(email string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb.SAdd(ctx, "registered_emails", email)
}
