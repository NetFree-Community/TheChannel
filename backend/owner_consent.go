package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// What a channel owner agreed to. Opening a channel needs the terms of service
// and the privacy policy accepted first; the mailing list is a separate,
// optional yes or no asked in the same dialog. Israeli law (Communications
// Law §30A) wants marketing mail to rest on an explicit opt-in that is not
// bundled with the service, so the two never share a checkbox and saying no to
// the mail never blocks the channel.
//
// One record per account, keyed by the normalised email, in a single hash.
// Readers who only follow a channel are never asked: the dialog belongs to the
// create flow alone.

const ownerConsentsKey = "owner_consents"

type OwnerConsent struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
	// TermsVersion is the Revised date of /terms at the time of acceptance,
	// so a later change of the text can tell who agreed to which wording.
	TermsVersion    string    `json:"terms_version"`
	TermsAcceptedAt time.Time `json:"terms_accepted_at"`
	Marketing       bool      `json:"marketing"`
	// MarketingAt is when the current marketing answer was given.
	MarketingAt time.Time `json:"marketing_at"`
	Source      string    `json:"source"`
}

func currentTermsVersion() string {
	for _, p := range legalPages {
		if p.Path == "/terms" {
			return p.Revised
		}
	}
	return ""
}

func dbGetOwnerConsent(ctx context.Context, email string) (*OwnerConsent, error) {
	raw, err := rdb.HGet(ctx, ownerConsentsKey, normEmail(email)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c OwnerConsent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// dbSaveOwnerConsent records an acceptance of the terms with the given
// marketing answer. The first acceptance date is kept; the marketing time only
// moves when the answer changes, so it always says when the current answer
// was given.
func dbSaveOwnerConsent(ctx context.Context, email, name string, marketing bool, source string) (*OwnerConsent, error) {
	email = normEmail(email)
	prev, err := dbGetOwnerConsent(ctx, email)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	c := &OwnerConsent{
		Email:           email,
		Name:            oneLine(name),
		TermsVersion:    currentTermsVersion(),
		TermsAcceptedAt: now,
		Marketing:       marketing,
		MarketingAt:     now,
		Source:          source,
	}
	if prev != nil {
		c.TermsAcceptedAt = prev.TermsAcceptedAt
		if prev.Marketing == marketing {
			c.MarketingAt = prev.MarketingAt
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := rdb.HSet(ctx, ownerConsentsKey, email, raw).Err(); err != nil {
		return nil, err
	}
	return c, nil
}

type ownerConsentState struct {
	TermsAccepted bool `json:"terms_accepted"`
	// Marketing is null until the question was answered.
	Marketing *bool `json:"marketing"`
}

func consentState(c *OwnerConsent) ownerConsentState {
	if c == nil {
		return ownerConsentState{}
	}
	m := c.Marketing
	return ownerConsentState{TermsAccepted: true, Marketing: &m}
}

// GET /api/me/consent (authenticated)
func getMyConsent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}
	c, err := dbGetOwnerConsent(ctx, s.Email)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(consentState(c))
}

// POST /api/me/consent (authenticated)
// {"accept_terms": true, "marketing": bool}
//
// Also how an owner changes the marketing answer later: the terms stay
// accepted, only the mail answer moves.
func setMyConsent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	defer r.Body.Close()
	var body struct {
		AcceptTerms bool `json:"accept_terms"`
		Marketing   bool `json:"marketing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !body.AcceptTerms {
		http.Error(w, "terms must be accepted", http.StatusBadRequest)
		return
	}
	c, err := dbSaveOwnerConsent(ctx, s.Email, sessionDisplayName(s), body.Marketing, "channel_create")
	if err != nil {
		log.Printf("setMyConsent: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(consentState(c))
}

func dbListOwnerConsents(ctx context.Context) ([]OwnerConsent, error) {
	all, err := rdb.HGetAll(ctx, ownerConsentsKey).Result()
	if err != nil {
		return nil, err
	}
	out := make([]OwnerConsent, 0, len(all))
	for _, raw := range all {
		var c OwnerConsent
		if json.Unmarshal([]byte(raw), &c) == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TermsAcceptedAt.After(out[j].TermsAcceptedAt) })
	return out, nil
}

// GET /api/super-admin/consents.csv
// Every channel owner's answers, newest first. The mailing list is the rows
// whose marketing column is "yes".
func exportOwnerConsents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := dbListOwnerConsents(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="channel-owner-consents.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	// A BOM so Excel opens the Hebrew names as UTF-8.
	w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	cw.Write([]string{"email", "name", "marketing", "marketing_at", "terms_version", "terms_accepted_at"})
	for _, c := range rows {
		m := "no"
		if c.Marketing {
			m = "yes"
		}
		cw.Write([]string{
			csvSafe(c.Email), csvSafe(c.Name), m,
			c.MarketingAt.Format(time.RFC3339), c.TermsVersion, c.TermsAcceptedAt.Format(time.RFC3339),
		})
	}
	cw.Flush()
}

// csvSafe defuses spreadsheet formulas: a Google display name is free text,
// and a cell starting with = + - @ would run when the file is opened.
func csvSafe(s string) string {
	if s != "" {
		switch s[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + s
		}
	}
	return s
}
