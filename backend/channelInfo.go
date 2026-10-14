package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
	// Search settings, for the management screen. Nothing secret: the page
	// itself publishes all of it (robots meta, title, description), and a
	// private channel is never in search whatever its toggle says.
	RequireAuth    bool   `json:"require_auth"`
	SeoHidden      bool   `json:"seo_hidden"`
	SeoTitle       string `json:"seo_title"`
	SeoDescription string `json:"seo_description"`
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
		channel.RequireAuth = ch.Features.RequireAuth
		channel.SeoHidden = ch.SeoHidden
		channel.SeoTitle = ch.SeoTitle
		channel.SeoDescription = ch.SeoDescription
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

// isSafeLogoURL accepts an empty value, a relative path (what the upload flow
// produces, e.g. /api/channel/<slug>/files/<id>), or an http(s) URL. Anything
// carrying another scheme is rejected, since the value is reflected into an
// <img src> for every visitor.
// isSafeContactURL accepts an empty value (button hidden), an http(s) URL or a
// mailto: address — the only schemes the header button opens. A bare
// "example.com" is refused rather than stored: with no scheme the browser would
// open it as a path under this site and the button would lead nowhere.
func isSafeContactURL(u string) bool {
	if u == "" {
		return true
	}
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "mailto:") {
		return len(u) > len("mailto:") && !strings.ContainsAny(u, " \t\r\n")
	}
	return (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) &&
		!strings.ContainsAny(u, " \t\r\n\"'<>")
}

func isSafeLogoURL(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return true
	}
	// A scheme is "<letters>:" at the very start; a relative path has none.
	if i := strings.IndexByte(u, ':'); i >= 0 {
		scheme := u[:i]
		// A "/" or "?" or "#" before the colon means it is a path, not a scheme
		// (e.g. "/a:b" is a relative path whose first segment contains a colon).
		if !strings.ContainsAny(scheme, "/?#") {
			lower := strings.ToLower(scheme)
			return lower == "http" || lower == "https"
		}
	}
	return true
}

func editChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	// Every field is a pointer so an ABSENT field can be told apart from a
	// deliberately cleared one — only a field the client actually sent is
	// written. Previously name/description/logoUrl were plain strings and always
	// written, so any partial request (a client that omitted a field, or a form
	// that opened before the channel had loaded) blanked the stored value. That
	// is what left a channel with an empty name whose /info then came back with
	// every field empty.
	type Request struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		LogoUrl     *string `json:"logoUrl"`
		ContactUs   *string `json:"contactUs"`
		channelSeoRequest
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// A channel must keep a name: refuse to blank it. Description, logo and
	// contact can be cleared, and an omitted field is simply left as-is.
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		req.Name = &trimmed
		if trimmed == "" {
			http.Error(w, "channel name cannot be empty", http.StatusBadRequest)
			return
		}
		if channelFieldTooLong(trimmed, maxChannelNameLen) {
			http.Error(w, "channel name is too long", http.StatusBadRequest)
			return
		}
	}
	if req.Description != nil && channelFieldTooLong(*req.Description, maxChannelDescLen) {
		http.Error(w, "description is too long", http.StatusBadRequest)
		return
	}
	// The logo is reflected to every visitor as an <img src>. A relative path
	// (what the upload flow produces) or an http(s) URL is fine; anything with
	// another scheme — javascript:, data: — is refused.
	if req.LogoUrl != nil && !isSafeLogoURL(*req.LogoUrl) {
		http.Error(w, "invalid logo URL", http.StatusBadRequest)
		return
	}
	if req.ContactUs != nil {
		trimmed := strings.TrimSpace(*req.ContactUs)
		req.ContactUs = &trimmed
		if !isSafeContactURL(trimmed) {
			http.Error(w, "invalid contact URL", http.StatusBadRequest)
			return
		}
	}

	hashKey := "channel:" + slug
	args := []any{}
	if req.Name != nil {
		args = append(args, "name", *req.Name)
	}
	if req.Description != nil {
		args = append(args, "description", *req.Description)
	}
	if req.LogoUrl != nil {
		args = append(args, "logoUrl", *req.LogoUrl)
	}
	if req.ContactUs != nil {
		args = append(args, "contactUs", *req.ContactUs)
	}
	seoArgs, err := req.channelSeoRequest.hashArgs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	args = append(args, seoArgs...)
	if len(args) == 0 {
		// Nothing to change; do not issue an empty HSet.
		res := Response{Success: true}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
		return
	}
	if _, err := rdb.HSet(ctx, hashKey, args...).Result(); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// The sitemap is cached for a quarter of an hour; a channel taken out of
	// search must leave it now, not when the cache happens to expire.
	if len(seoArgs) > 0 {
		resetSitemapCache()
	}

	res := Response{Success: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
