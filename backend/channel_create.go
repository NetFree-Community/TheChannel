package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxChannelsPerOwner caps self-service creation per account. The endpoint is
	// open to every logged-in user, so without a cap one account can mint
	// channels (and their Redis keyspace) without bound.
	maxChannelsPerOwner = 5
	// Field caps on the stored channel record, in characters (the forms cap
	// the same fields with maxlength, which also counts characters).
	maxChannelNameLen = 80
	maxChannelDescLen = 2000
)

// channelFieldTooLong counts characters, not bytes: the byte-length check it
// replaces cut a Hebrew name off at half the advertised limit, since every
// Hebrew letter is two bytes in UTF-8, while the form let the user type the
// full 80 and then showed an unexplained error.
func channelFieldTooLong(s string, maxRunes int) bool {
	return utf8.RuneCountInString(s) > maxRunes
}

// reservedSlugs are the top-level frontend route paths (see
// frontend/src/app/app.routes.ts) plus the prefixes the backend serves itself.
// Channels live at /channel/<slug> today, so none of these can clash yet; they
// stay reserved so a short /<slug> address can be introduced later without a
// channel shadowing a real page.
var reservedSlugs = map[string]struct{}{
	"login":       {},
	"channel":     {},
	"channels":    {},
	"super-admin": {},
	"admin":       {},
	"api":         {},
	"assets":      {},
	"auth":        {},
	"static":      {},
	// Server-rendered pages and search-engine files (seo_content.go,
	// sitemap.go).
	"open-channel":         {},
	"community-channel":    {},
	"whatsapp-alternative": {},
	"netfree-channel":      {},
	"privacy":              {},
	"terms":                {},
	"robots":               {},
	"sitemap":              {},
}

func isReservedSlug(slug string) bool {
	_, ok := reservedSlugs[slug]
	return ok
}

// slugAvailability reports why a slug cannot be used, or "" when it is free.
// Kept separate from the handlers so both the create and the live-check
// endpoint answer identically.
func slugAvailability(ctx context.Context, slug string) (string, error) {
	if !slugRegex.MatchString(slug) {
		return "invalid", nil
	}
	if isReservedSlug(slug) {
		return "reserved", nil
	}
	exists, err := dbChannelExists(ctx, slug)
	if err != nil {
		return "", err
	}
	if exists {
		return "taken", nil
	}
	return "", nil
}

// countOwnedChannels counts the channels the email holds RoleOwner on. The
// users:list blob is the same source dbAssignChannelRole writes, so a channel
// created a moment ago is already counted.
func countOwnedChannels(ctx context.Context, email string) (int, error) {
	users, err := dbGetUsersList(ctx)
	if err != nil {
		return 0, err
	}
	email = normEmail(email)
	for _, u := range users {
		if normEmail(u.Email) != email {
			continue
		}
		var n int
		for _, role := range u.ChannelRoles {
			if role == RoleOwner {
				n++
			}
		}
		return n, nil
	}
	return 0, nil
}

// sessionEmail returns the authenticated email carried by the session cookie.
// Self-service creation must take the owner from here and never from the
// request body: an email accepted from the body would let anyone squat a slug
// and hand ownership to somebody else's address.
func sessionEmail(r *http.Request) (Session, bool) {
	session, _ := store.Get(r, cookieName)
	s, ok := session.Values["user"].(Session)
	if !ok || s.Email == "" {
		return Session{}, false
	}
	return s, true
}

// sessionDisplayName is the human label recorded on the audit trail.
func sessionDisplayName(s Session) string {
	if s.PublicName != "" {
		return s.PublicName
	}
	if s.Username != "" {
		return s.Username
	}
	return s.Email
}

// POST /api/channels/create (authenticated)
//
// The only self-service channel creation path: a logged-in user gets their
// channel instantly. There is no approval queue; the super admin sees every
// channel, with its owner and creation date, on the channels screen.
func createChannelSelfService(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	// The creation throttle is spent further down, immediately before the channel
	// is written. At three per hour, charging quota for a rejected slug meant
	// three typos locked the account out for twenty minutes having created nothing.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()

	var body struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		// Optional: lets a client accept the terms in the same request. The
		// app accepts them first through POST /api/me/consent.
		AcceptTerms bool  `json:"accept_terms"`
		Marketing   *bool `json:"marketing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if channelFieldTooLong(body.Name, maxChannelNameLen) || channelFieldTooLong(body.Description, maxChannelDescLen) {
		http.Error(w, "field too long", http.StatusBadRequest)
		return
	}

	// Sessions minted before emails were normalised may still carry the
	// address in Google's casing; every grant below must use the canonical
	// form or the owner would not match their own channel.
	ownerEmail := normEmail(s.Email)

	// Nobody opens a channel without having agreed to the terms of service and
	// the privacy policy. 428 tells the app to show the consent dialog.
	consent, err := dbGetOwnerConsent(ctx, ownerEmail)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if consent == nil {
		if !body.AcceptTerms {
			http.Error(w, "terms not accepted", http.StatusPreconditionRequired)
			return
		}
		marketing := body.Marketing != nil && *body.Marketing
		if _, err := dbSaveOwnerConsent(ctx, ownerEmail, sessionDisplayName(s), marketing, "channel_create"); err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
	}

	reason, err := slugAvailability(ctx, body.Slug)
	if err != nil {
		http.Error(w, "error checking slug", http.StatusInternalServerError)
		return
	}
	switch reason {
	case "invalid":
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	case "reserved":
		http.Error(w, "slug is reserved", http.StatusBadRequest)
		return
	case "taken":
		http.Error(w, "slug already taken", http.StatusConflict)
		return
	}

	// Serialise creates by the same owner. The cap below is a check-then-act:
	// countOwnedChannels reads the roles list, but the role that would make this
	// channel count is only assigned after dbCreateChannel further down. Three
	// concurrent requests (which the creation limiter's burst of 3 allows) all
	// read the same count, all pass, and all create — an account holding five
	// channels ends up with seven. dbCreateChannel's atomic slug claim does not
	// help, because the slugs are different.
	//
	// The lock is short-lived and owner-scoped, so it costs nothing to anyone
	// else and cannot outlive a crashed request.
	createLock := "channel_create:lock:" + ownerEmail
	gotLock, lockErr := rdb.SetNX(ctx, createLock, 1, 15*time.Second).Result()
	if lockErr != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if !gotLock {
		http.Error(w, "another channel is already being created for this account", http.StatusConflict)
		return
	}
	defer rdb.Del(ctx, createLock)

	owned, err := countOwnedChannels(ctx, ownerEmail)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if owned >= maxChannelsPerOwner {
		http.Error(w, "channel limit reached for this account", http.StatusForbidden)
		return
	}

	// Validation is done and nothing has been persisted yet, so this is the first
	// point where the request actually costs a channel.
	if !allowOrRetryAfter(w, channelCreateLimiter(ownerEmail), "too many requests — please try again later") {
		return
	}

	channel := &ChannelData{
		Slug:        body.Slug,
		Name:        body.Name,
		Description: body.Description,
		OwnerEmail:  ownerEmail,
		CreatedAt:   time.Now(),
		Features:    defaultChannelFeatures(),
	}

	if err := dbCreateChannel(ctx, channel); err != nil {
		// dbCreateChannel claims the slug atomically, so this is the branch that
		// actually decides a race between two concurrent creates.
		if errors.Is(err, errChannelExists) {
			http.Error(w, "slug already taken", http.StatusConflict)
			return
		}
		http.Error(w, "error creating channel", http.StatusInternalServerError)
		return
	}

	if err := dbAssignChannelRole(ctx, ownerEmail, channel.Slug, RoleOwner); err != nil {
		log.Printf("createChannelSelfService: %s created but owner role for %s not assigned: %v\n", channel.Slug, ownerEmail, err)
	}
	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after createChannelSelfService(%s): %v", channel.Slug, err)
	}

	// The channel is stored; the welcome mail goes out in the background and
	// cannot hold up or fail this response.
	created, ownerName := *channel, sessionDisplayName(s)
	dispatchMagnetMail(func(context.Context) (*magnetMailEvent, bool) {
		return channelCreatedMail(&created, ownerEmail, ownerName)
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"slug": channel.Slug,
		"name": channel.Name,
	})
}

// GET /api/channels/slug-available?slug=<slug> (authenticated)
// Live feedback for the create form.
func checkSlugAvailable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	// Stays at the top: this endpoint's whole cost IS the lookup, so there is no
	// later point to move it to. Retry-After lets the form back off precisely
	// instead of leaving the live check silently stuck.
	if !allowOrRetryAfter(w, slugCheckLimiter(s.Email), "too many requests — please try again later") {
		return
	}

	reason, err := slugAvailability(ctx, r.URL.Query().Get("slug"))
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"available": reason == "",
		"reason":    reason,
	})
}
