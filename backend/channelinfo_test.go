package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// editChannelInfo must only write fields the client actually sent. A partial
// request (name only) must leave description and logo untouched — the previous
// always-write behaviour blanked them, which is what left a channel with an
// empty name whose /info then came back empty.
func TestEditChannelInfoPartialRequestPreservesOtherFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "editinfo-partial"
	ch := &ChannelData{
		Slug: slug, Name: "Original", Description: "Original desc",
		LogoUrl: "/api/channel/" + slug + "/files/abc", CreatedAt: time.Now(),
	}
	if err := dbCreateChannel(ctx, ch); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		dbDeleteChannel(cctx, slug)
	})

	// Send ONLY a new name.
	callEditChannelInfo(t, slug, `{"name":"Renamed"}`, http.StatusOK)

	got, err := dbGetChannel(ctx, slug)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Renamed" {
		t.Errorf("name = %q, want Renamed", got.Name)
	}
	if got.Description != "Original desc" {
		t.Errorf("description was blanked to %q; a partial request must not touch it", got.Description)
	}
	if got.LogoUrl != "/api/channel/"+slug+"/files/abc" {
		t.Errorf("logoUrl was blanked to %q; a partial request must not touch it", got.LogoUrl)
	}
}

// A blank name would leave the channel nameless (the empty-/info symptom), so
// it is refused.
func TestEditChannelInfoRejectsBlankName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "editinfo-blank"
	if err := dbCreateChannel(ctx, &ChannelData{Slug: slug, Name: "Keep", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		dbDeleteChannel(cctx, slug)
	})

	callEditChannelInfo(t, slug, `{"name":"   "}`, http.StatusBadRequest)

	got, err := dbGetChannel(ctx, slug)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Keep" {
		t.Errorf("name = %q, want Keep — a rejected request must not have changed it", got.Name)
	}
}

// A clear (explicit empty string) on a clearable field IS honoured.
func TestEditChannelInfoClearsDescriptionWhenSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "editinfo-clear"
	if err := dbCreateChannel(ctx, &ChannelData{Slug: slug, Name: "N", Description: "to be cleared", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		dbDeleteChannel(cctx, slug)
	})

	callEditChannelInfo(t, slug, `{"description":""}`, http.StatusOK)

	got, _ := dbGetChannel(ctx, slug)
	if got.Description != "" {
		t.Errorf("description = %q, want empty (an explicit clear must be honoured)", got.Description)
	}
	if got.Name != "N" {
		t.Errorf("name = %q, want N (untouched)", got.Name)
	}
}

func TestEditChannelInfoRejectsDangerousLogoScheme(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "editinfo-logo"
	if err := dbCreateChannel(ctx, &ChannelData{Slug: slug, Name: "N", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		dbDeleteChannel(cctx, slug)
	})

	callEditChannelInfo(t, slug, `{"logoUrl":"javascript:alert(1)"}`, http.StatusBadRequest)
	callEditChannelInfo(t, slug, `{"logoUrl":"data:text/html,x"}`, http.StatusBadRequest)
	// Legit values are accepted.
	callEditChannelInfo(t, slug, `{"logoUrl":"/api/channel/`+slug+`/files/xyz"}`, http.StatusOK)
	callEditChannelInfo(t, slug, `{"logoUrl":"https://cdn.example.com/logo.png"}`, http.StatusOK)
}

func TestIsSafeLogoURL(t *testing.T) {
	ok := []string{"", "/api/channel/a/files/b", "https://x.com/l.png", "http://x.com/l.png", "/relative/path"}
	for _, u := range ok {
		if !isSafeLogoURL(u) {
			t.Errorf("%q should be accepted", u)
		}
	}
	bad := []string{"javascript:alert(1)", "data:text/html,x", "vbscript:x", "JAVASCRIPT:alert(1)"}
	for _, u := range bad {
		if isSafeLogoURL(u) {
			t.Errorf("%q should be rejected", u)
		}
	}
}

// callEditChannelInfo drives the handler with a body and asserts the status.
// The slug is injected the way channelMiddleware would, via the request context.
func callEditChannelInfo(t *testing.T, slug, body string, wantStatus int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/channel/"+slug+"/admin/edit-channel-info", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), channelCtxKey, &ChannelData{Slug: slug}))
	rec := httptest.NewRecorder()

	editChannelInfo(rec, req)

	if rec.Code != wantStatus {
		t.Fatalf("body %s: got status %d, want %d (%s)", body, rec.Code, wantStatus, rec.Body.String())
	}
	if wantStatus == http.StatusOK {
		var resp Response
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.Success {
			t.Errorf("body %s: expected success response, got %s", body, rec.Body.String())
		}
	}
}
