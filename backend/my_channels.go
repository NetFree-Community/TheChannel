package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// myChannel is one row of GET /api/my-channels: what the "my channels" page
// and the channel switcher need to show a channel by name and logo rather
// than by raw slug, say what the user may do there, and warn when it is off.
type myChannel struct {
	Slug         string      `json:"slug"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	LogoUrl      string      `json:"logoUrl"`
	Role         ChannelRole `json:"role"`
	CreatedAt    time.Time   `json:"createdAt"`
	Disabled     bool        `json:"disabled"`
	Participants int64       `json:"participants"`
}

// myChannelsFor resolves every channel the user holds a role on. Until now the
// frontend only had the slug→role map from /api/user-info, so the "my
// channels" page listed bare slugs and had no way to tell a disabled channel
// from a live one.
//
// A role whose channel no longer exists (deleted by a super admin after the
// grant) is skipped rather than failing the whole list, so one stale grant
// cannot blank the page. Owner channels come first, then by role, then
// newest first; the order is stable so the switcher does not reshuffle
// between loads.
func myChannelsFor(ctx context.Context, u User) ([]myChannel, error) {
	rows := make([]myChannel, 0, len(u.ChannelRoles))
	for slug, role := range u.ChannelRoles {
		ch, err := dbGetChannel(ctx, slug)
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, err
		}
		participants, err := dbGetUsersAmount(ctx, slug)
		if err != nil {
			return nil, err
		}
		rows = append(rows, myChannel{
			Slug:         ch.Slug,
			Name:         ch.Name,
			Description:  ch.Description,
			LogoUrl:      ch.LogoUrl,
			Role:         role,
			CreatedAt:    ch.CreatedAt,
			Disabled:     ch.Features.Disabled,
			Participants: participants,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if ri, rj := channelRoleLevels[rows[i].Role], channelRoleLevels[rows[j].Role]; ri != rj {
			return ri > rj
		}
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].Slug < rows[j].Slug
	})
	return rows, nil
}

// listMyChannels answers GET /api/my-channels for the session user. It sits
// behind checkLogin; a signed-in user who holds no role yet has no privileges
// record at all, which is an empty list and not an error.
func listMyChannels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	u, _ := sessionUser(r)
	rows, err := myChannelsFor(ctx, u)
	if err != nil {
		log.Printf("list my channels for %s: %v", u.Email, err)
		http.Error(w, "error listing channels", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// Roles change under the user (a grant, a channel switched off); the page
	// must never show a cached answer from before that.
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(rows)
}
