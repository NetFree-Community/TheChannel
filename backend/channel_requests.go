package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

const (
	// channelRequestTTL bounds how long a request record (and its index entry)
	// survives.
	channelRequestTTL = 30 * 24 * time.Hour
	// maxChannelRequestsListed caps the admin queue read, which does one GET per
	// entry.
	maxChannelRequestsListed = 500
)

type RequestStatus string

const (
	RequestStatusPending  RequestStatus = "pending"
	RequestStatusApproved RequestStatus = "approved"
	RequestStatusRejected RequestStatus = "rejected"
)

type ChannelRequest struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Email        string        `json:"email"`
	DesiredSlug  string        `json:"desiredSlug"`
	Description  string        `json:"description"`
	Status       RequestStatus `json:"status"`
	Notes        string        `json:"notes"`        // admin notes / rejection reason
	ApprovedSlug string        `json:"approvedSlug"` // final slug after approval
	CreatedAt    time.Time     `json:"createdAt"`
}

func dbSaveChannelRequest(ctx context.Context, req *ChannelRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	// TTL so the audit trail cannot accumulate records forever.
	if err := rdb.Set(ctx, "channel_request:"+req.ID, data, channelRequestTTL).Err(); err != nil {
		return err
	}
	return rdb.ZAdd(ctx, "channel_requests:list", redis.Z{
		Score:  float64(req.CreatedAt.Unix()),
		Member: req.ID,
	}).Err()
}

func dbGetChannelRequest(ctx context.Context, id string) (*ChannelRequest, error) {
	data, err := rdb.Get(ctx, "channel_request:"+id).Result()
	if err != nil {
		return nil, err
	}
	var req ChannelRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func dbListChannelRequests(ctx context.Context) ([]*ChannelRequest, error) {
	// Drop index entries whose bodies have already expired, then read a bounded
	// page: the list does one GET per entry.
	cutoff := strconv.FormatInt(time.Now().Add(-channelRequestTTL).Unix(), 10)
	rdb.ZRemRangeByScore(ctx, "channel_requests:list", "-inf", cutoff)

	ids, err := rdb.ZRevRange(ctx, "channel_requests:list", 0, maxChannelRequestsListed-1).Result()
	if err != nil {
		return nil, err
	}
	requests := make([]*ChannelRequest, 0, len(ids))
	for _, id := range ids {
		req, err := dbGetChannelRequest(ctx, id)
		if err != nil {
			continue
		}
		requests = append(requests, req)
	}
	return requests, nil
}

// GET /api/super-admin/channel-requests
func listChannelRequests(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requests, err := dbListChannelRequests(ctx)
	if err != nil {
		// An empty 200 rendered "אין בקשות" over a Redis outage, and the
		// screen's error toast never ran; the sibling handlers answer 500.
		log.Printf("listChannelRequests: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(requests)
}

// POST /api/super-admin/channel-requests/{id}/approve
func approveChannelRequest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id := chi.URLParam(r, "id")

	var body struct {
		Slug  string `json:"slug"`
		Notes string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req, err := dbGetChannelRequest(ctx, id)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}

	// Without this, replaying approve with a different slug creates a second
	// channel and a second owner grant for the same applicant.
	if req.Status != RequestStatusPending {
		http.Error(w, "request already processed", http.StatusConflict)
		return
	}

	finalSlug := body.Slug
	if finalSlug == "" {
		finalSlug = req.DesiredSlug
	}

	if !slugRegex.MatchString(finalSlug) {
		http.Error(w, "invalid slug format", http.StatusBadRequest)
		return
	}
	// Same reserved-slug guard as the other creation paths: approving a request
	// onto "admin"/"api"/"login"/... would shadow a real route.
	if isReservedSlug(finalSlug) {
		http.Error(w, "slug is reserved", http.StatusBadRequest)
		return
	}

	exists, err := dbChannelExists(ctx, finalSlug)
	if err != nil {
		http.Error(w, "error checking slug", http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "slug already taken", http.StatusConflict)
		return
	}

	// The stored request is the one write path that never had the name
	// checked; the same trim/empty/length rules as the other creation paths.
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "Channel name is required", http.StatusBadRequest)
		return
	}
	if channelFieldTooLong(name, maxChannelNameLen) {
		http.Error(w, "Channel name is too long", http.StatusBadRequest)
		return
	}

	ownerEmail := normEmail(req.Email)
	channel := &ChannelData{
		Slug:       finalSlug,
		Name:       name,
		OwnerEmail: ownerEmail,
		CreatedAt:  time.Now(),
		Features:   defaultChannelFeatures(),
	}

	if err := dbCreateChannel(ctx, channel); err != nil {
		if errors.Is(err, errChannelExists) {
			http.Error(w, "slug already taken", http.StatusConflict)
			return
		}
		http.Error(w, "error creating channel", http.StatusInternalServerError)
		return
	}

	if err := dbAssignChannelRole(ctx, ownerEmail, finalSlug, RoleOwner); err != nil {
		log.Printf("approveChannelRequest: %s created but owner role for %s not assigned: %v\n", finalSlug, ownerEmail, err)
	}
	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after approveChannelRequest(%s): %v\n", finalSlug, err)
	}

	req.Status = RequestStatusApproved
	req.ApprovedSlug = finalSlug
	req.Notes = body.Notes
	dbSaveChannelRequest(ctx, req)

	// Same welcome mail as self-service creation. No greeting name: on these
	// requests Name is what became the channel name, not the person's.
	created := *channel
	dispatchMagnetMail(func(context.Context) (*magnetMailEvent, bool) {
		return channelCreatedMail(&created, ownerEmail, "")
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "approved",
		"channelSlug": finalSlug,
		"channelName": channel.Name,
		"ownerEmail":  req.Email,
	})
}

// POST /api/super-admin/channel-requests/{id}/reject
func rejectChannelRequest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := chi.URLParam(r, "id")

	var body struct {
		Notes string `json:"notes"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	req, err := dbGetChannelRequest(ctx, id)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}

	if req.Status != RequestStatusPending {
		http.Error(w, "request already processed", http.StatusConflict)
		return
	}

	req.Status = RequestStatusRejected
	req.Notes = body.Notes
	dbSaveChannelRequest(ctx, req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rejected"})
}
