package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Everyone who ever signed in with Google, for the super admin. The address
// alone has always been kept (the registered_emails sets, global and per
// channel); signInsKey adds the name and the first and last sign-in from now
// on. Readers never agreed to marketing mail: the export says who did, and
// only those rows may be mailed (owner_consent.go).

const signInsKey = "signins"

type signInRecord struct {
	Name  string    `json:"name"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

// recordSignIn runs in the background after a successful Google login.
func recordSignIn(email, name string) {
	email = normEmail(email)
	if email == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	rec := signInRecord{Name: oneLine(name), First: now, Last: now}
	if raw, err := rdb.HGet(ctx, signInsKey, email).Result(); err == nil {
		var prev signInRecord
		if json.Unmarshal([]byte(raw), &prev) == nil && !prev.First.IsZero() {
			rec.First = prev.First
		}
	}
	if raw, err := json.Marshal(rec); err == nil {
		rdb.HSet(ctx, signInsKey, email, raw)
	}
}

type signInRow struct {
	Email     string
	Name      string
	First     time.Time
	Last      time.Time
	Channels  []string
	Marketing string
}

func dbListSignIns(ctx context.Context) ([]signInRow, error) {
	rows := map[string]*signInRow{}
	row := func(email string) *signInRow {
		email = normEmail(email)
		r, ok := rows[email]
		if !ok {
			r = &signInRow{Email: email}
			rows[email] = r
		}
		return r
	}

	emails, err := rdb.SMembers(ctx, "registered_emails").Result()
	if err != nil {
		return nil, err
	}
	for _, e := range emails {
		row(e)
	}

	recs, err := rdb.HGetAll(ctx, signInsKey).Result()
	if err != nil {
		return nil, err
	}
	for e, raw := range recs {
		var rec signInRecord
		if json.Unmarshal([]byte(raw), &rec) == nil {
			r := row(e)
			r.Name, r.First, r.Last = rec.Name, rec.First, rec.Last
		}
	}

	channels, err := dbListChannels(ctx)
	if err != nil {
		return nil, err
	}
	pipe := rdb.Pipeline()
	members := make([]*redis.StringSliceCmd, len(channels))
	for i, ch := range channels {
		members[i] = pipe.SMembers(ctx, fmt.Sprintf("channel:%s:registered_emails", ch.Slug))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for i, ch := range channels {
		label := ch.Name
		if label == "" {
			label = ch.Slug
		}
		for _, e := range members[i].Val() {
			r := row(e)
			r.Channels = append(r.Channels, label)
		}
	}

	consents, err := dbListOwnerConsents(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range consents {
		r := row(c.Email)
		if r.Name == "" {
			r.Name = c.Name
		}
		r.Marketing = "no"
		if c.Marketing {
			r.Marketing = "yes"
		}
	}

	out := make([]signInRow, 0, len(rows))
	for _, r := range rows {
		if r.Email != "" {
			out = append(out, *r)
		}
	}
	// Most recent sign-in first; addresses from before the times were kept
	// follow, alphabetically.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Email < out[j].Email
	})
	return out, nil
}

// GET /api/super-admin/signins.csv
func exportSignIns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := dbListSignIns(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="channel-signins.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	cw.Write([]string{"email", "name", "first_signin", "last_signin", "channels", "marketing"})
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format(time.RFC3339)
	}
	for _, s := range rows {
		names := make([]string, len(s.Channels))
		for i, c := range s.Channels {
			names[i] = oneLine(c)
		}
		sort.Strings(names)
		cw.Write([]string{
			csvSafe(s.Email), csvSafe(s.Name), stamp(s.First), stamp(s.Last),
			csvSafe(strings.Join(names, "; ")), s.Marketing,
		})
	}
	cw.Flush()
}
