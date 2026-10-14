package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi"
)

// What a search result shows before cutting off: about 60 characters of
// title and 155 of description. The management screen counts down to the
// same numbers, and the server refuses more rather than storing text Google
// would cut mid-word anyway.
const (
	maxSeoTitleLen       = 60
	maxSeoDescriptionLen = 155
)

// channelSeoRequest is the search-engine part of a channel edit. Pointers,
// as in editChannelInfo, so a field the client did not send is left alone.
type channelSeoRequest struct {
	SeoHidden      *bool   `json:"seoHidden"`
	SeoTitle       *string `json:"seoTitle"`
	SeoDescription *string `json:"seoDescription"`
}

var (
	errSeoTitleTooLong       = errors.New("seo title is too long")
	errSeoDescriptionTooLong = errors.New("seo description is too long")
)

// cleanSeoText folds what was typed into one line: a search result has no
// line breaks, and control characters have no business in a <meta> tag.
func cleanSeoText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// hashArgs validates the request and returns the channel-hash fields to
// write, empty when nothing search-related was sent.
func (q *channelSeoRequest) hashArgs() ([]any, error) {
	args := []any{}
	if q.SeoHidden != nil {
		v := "0"
		if *q.SeoHidden {
			v = "1"
		}
		args = append(args, "seoHidden", v)
	}
	if q.SeoTitle != nil {
		t := cleanSeoText(*q.SeoTitle)
		if utf8.RuneCountInString(t) > maxSeoTitleLen {
			return nil, errSeoTitleTooLong
		}
		args = append(args, "seoTitle", t)
	}
	if q.SeoDescription != nil {
		d := cleanSeoText(*q.SeoDescription)
		if utf8.RuneCountInString(d) > maxSeoDescriptionLen {
			return nil, errSeoDescriptionTooLong
		}
		args = append(args, "seoDescription", d)
	}
	return args, nil
}

// channelSearchListed is whether a channel may appear in search results:
// public, active, and not switched off by its managers. The sitemap and the
// channel page's robots directive both ask this, so they cannot disagree.
func channelSearchListed(ch *ChannelData) bool {
	return ch != nil && !ch.Features.Disabled && !ch.Features.RequireAuth && !ch.SeoHidden
}

// channelSearchTitle and channelSearchDescription are the text a search
// result shows: what the managers wrote for it, or else the defaults the
// page always had.
func channelSearchTitle(ch *ChannelData, name, siteName string) string {
	if t := cleanSeoText(ch.SeoTitle); t != "" {
		return t
	}
	return name + " · " + siteName
}

func channelSearchDescription(ch *ChannelData, name string) string {
	if d := previewText(ch.SeoDescription); d != "" {
		return d
	}
	if d := previewText(ch.Description); d != "" {
		return d
	}
	return previewText("ערוץ העדכונים " + name + ": הודעות, תמונות ועדכונים, ישירות בדפדפן ובלי אפליקציה.")
}

// PUT /api/super-admin/channels/{slug}/seo
//
// The super admin's copy of the channel screen's search settings. It sits
// outside channelMiddleware, which refuses a disabled channel, so the
// settings of a channel the operator switched off stay editable.
func superAdminSetChannelSeo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")
	if !requireExistingChannel(ctx, w, slug) {
		return
	}
	var req channelSeoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	args, err := req.hashArgs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(args) > 0 {
		if err := rdb.HSet(ctx, "channel:"+slug, args...).Err(); err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		resetSitemapCache()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}
