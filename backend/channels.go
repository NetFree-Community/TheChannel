package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

type ChannelFeatures struct {
	Reactions           bool `json:"reactions"`
	FileUploads         bool `json:"fileUploads"`
	Reports             bool `json:"reports"`
	Ads                 bool `json:"ads"`
	Notifications       bool `json:"notifications"`
	RequireAuth         bool `json:"requireAuth"`
	RequireAuthFiles    bool `json:"requireAuthFiles"`
	CountViews          bool `json:"countViews"`
	ScheduledMessages   bool `json:"scheduledMessages"`
	Webhook             bool `json:"webhook"`
	MagnetLockedByAdmin bool `json:"magnetLockedByAdmin"` // set by super admin, owner cannot change
	AdsLockedByAdmin    bool `json:"adsLockedByAdmin"`    // set by super admin, owner cannot change
}

type ChannelData struct {
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	LogoUrl     string          `json:"logoUrl"`
	OwnerEmail  string          `json:"ownerEmail"`
	CreatedAt   time.Time       `json:"createdAt"`
	Features    ChannelFeatures `json:"features"`
	ContactUs   string          `json:"contactUs"`
}

type ctxKey string

const channelCtxKey ctxKey = "channel"

var slugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{1,48}[a-z0-9]$`)

func channelMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		ctx := r.Context()

		channel, err := dbGetChannel(ctx, slug)
		if err != nil {
			if err == redis.Nil {
				http.Error(w, "Channel not found", http.StatusNotFound)
			} else {
				http.Error(w, "error", http.StatusInternalServerError)
			}
			return
		}

		ctx = context.WithValue(ctx, channelCtxKey, channel)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func channelFromCtx(r *http.Request) *ChannelData {
	v := r.Context().Value(channelCtxKey)
	if v == nil {
		return nil
	}
	return v.(*ChannelData)
}

func channelSlugFromCtx(r *http.Request) string {
	ch := channelFromCtx(r)
	if ch == nil {
		return ""
	}
	return ch.Slug
}

// requireFeature rejects a request when the channel's operator-controlled
// toggle for that feature is off. Only applied to toggles both channel
// creation paths default to true, so it cannot silently disable a live tenant.
func requireFeature(pick func(*ChannelFeatures) bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch := channelFromCtx(r)
		if ch == nil || !pick(&ch.Features) {
			http.Error(w, "Feature disabled for this channel", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// channelIfRequireAuth middleware
func channelIfRequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch := channelFromCtx(r)
		if ch != nil && ch.Features.RequireAuth {
			checkLogin(next).ServeHTTP(w, r)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// channelIfRequireAuthFiles middleware
func channelIfRequireAuthFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch := channelFromCtx(r)
		if ch != nil && ch.Features.RequireAuthFiles {
			checkLogin(next).ServeHTTP(w, r)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// Super admin: list all channels
func listChannels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	channels, err := dbListChannels(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channels)
}

// Super admin: create channel
func createChannel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req struct {
		Slug       string `json:"slug"`
		Name       string `json:"name"`
		OwnerEmail string `json:"ownerEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if !slugRegex.MatchString(req.Slug) {
		http.Error(w, "Invalid slug: use lowercase letters, numbers, hyphens (min 3 chars)", http.StatusBadRequest)
		return
	}

	exists, err := dbChannelExists(ctx, req.Slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "Channel already exists", http.StatusConflict)
		return
	}

	channel := &ChannelData{
		Slug:       req.Slug,
		Name:       req.Name,
		OwnerEmail: req.OwnerEmail,
		CreatedAt:  time.Now(),
		Features: ChannelFeatures{
			Reactions:         true,
			FileUploads:       true,
			Reports:           true,
			ScheduledMessages: true,
			// Permission to use the feature, not a request to use it: each one
			// still needs the owner to configure it in channel settings. Left
			// false these would be unusable and undiagnosable for a new tenant.
			Ads:           true,
			Notifications: true,
			Webhook:       true,
		},
	}

	if err := dbCreateChannel(ctx, channel); err != nil {
		if errors.Is(err, errChannelExists) {
			http.Error(w, "Channel already exists", http.StatusConflict)
			return
		}
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	if req.OwnerEmail != "" {
		dbAssignChannelRole(ctx, req.OwnerEmail, req.Slug, RoleOwner)
		if err := initializePrivilegeUsers(); err != nil {
			log.Printf("initializePrivilegeUsers after createChannel: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

// Super admin: delete channel
func deleteChannel(w http.ResponseWriter, r *http.Request) {
	// Deletion also releases every uploaded blob (R2/disk), so it needs more
	// headroom than the usual 5s handler budget.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")

	// Without these checks a typo deletes nothing and still reports success, so
	// the operator records a channel as gone while it is still live.
	if !slugRegex.MatchString(slug) {
		http.Error(w, "Invalid slug", http.StatusBadRequest)
		return
	}
	exists, err := dbChannelExists(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	if err := dbDeleteChannel(ctx, slug); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// Super admin: update channel features
func updateChannelFeatures(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")

	var features ChannelFeatures
	if err := json.NewDecoder(r.Body).Decode(&features); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if err := dbSetChannelFeatures(ctx, slug, &features); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// Super admin: get channel (including features)
func getSuperAdminChannel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")
	channel, err := dbGetChannel(ctx, slug)
	if err != nil {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

// Super admin: set channel users
func superAdminSetChannelUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")

	var req struct {
		Users []struct {
			Email string      `json:"email"`
			Role  ChannelRole `json:"role"`
		} `json:"users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Read-modify-write under a WATCH so a concurrent role edit elsewhere is not
	// silently overwritten by this one.
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		userMap := make(map[string]int)
		for i, u := range users {
			userMap[u.Email] = i
		}

		for _, ru := range req.Users {
			if i, exists := userMap[ru.Email]; exists {
				if users[i].ChannelRoles == nil {
					users[i].ChannelRoles = make(map[string]ChannelRole)
				}
				if ru.Role == "" {
					delete(users[i].ChannelRoles, slug)
				} else {
					users[i].ChannelRoles[slug] = ru.Role
				}
			} else if ru.Role != "" {
				userMap[ru.Email] = len(users)
				users = append(users, User{
					Email:        ru.Email,
					ChannelRoles: map[string]ChannelRole{slug: ru.Role},
				})
			}
		}
		return users
	}); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after superAdminSetChannelUsers: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// Super admin: get channel users
func superAdminGetChannelUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	type ChannelUser struct {
		Email string      `json:"email"`
		Role  ChannelRole `json:"role"`
	}

	// Encoded straight to JSON: a nil slice would serialise as null, which the
	// user management screens do not accept.
	channelUsers := make([]ChannelUser, 0)
	for _, u := range users {
		if u.ChannelRoles != nil {
			if role, exists := u.ChannelRoles[slug]; exists {
				channelUsers = append(channelUsers, ChannelUser{Email: u.Email, Role: role})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channelUsers)
}

// Channel owner: get channel users (for this channel only)
func getChannelUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	type ChannelUser struct {
		Email string      `json:"email"`
		Role  ChannelRole `json:"role"`
	}

	// Encoded straight to JSON: a nil slice would serialise as null, which the
	// user management screens do not accept.
	channelUsers := make([]ChannelUser, 0)
	for _, u := range users {
		if u.ChannelRoles != nil {
			if role, exists := u.ChannelRoles[slug]; exists {
				channelUsers = append(channelUsers, ChannelUser{Email: u.Email, Role: role})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channelUsers)
}

// Channel owner: set channel users (cannot assign owner role)
func setChannelUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var req struct {
		Users []struct {
			Email string      `json:"email"`
			Role  ChannelRole `json:"role"`
		} `json:"users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Read-modify-write under a WATCH so a concurrent role edit elsewhere is not
	// silently overwritten by this one.
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		userMap := make(map[string]int)
		for i, u := range users {
			userMap[u.Email] = i
		}

		for _, ru := range req.Users {
			if ru.Role == RoleOwner {
				continue // owner cannot promote others to owner
			}
			if i, exists := userMap[ru.Email]; exists {
				if users[i].ChannelRoles == nil {
					users[i].ChannelRoles = make(map[string]ChannelRole)
				}
				if ru.Role == "" {
					delete(users[i].ChannelRoles, slug)
				} else {
					users[i].ChannelRoles[slug] = ru.Role
				}
			} else if ru.Role != "" {
				userMap[ru.Email] = len(users)
				users = append(users, User{
					Email:        ru.Email,
					ChannelRoles: map[string]ChannelRole{slug: ru.Role},
				})
			}
		}
		return users
	}); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after setChannelUsers: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}
