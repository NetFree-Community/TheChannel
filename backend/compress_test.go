package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi"
)

// The SSE handler must reach the real connection through the middleware
// chain: a write deadline that cannot be armed is exactly the bug the
// stream-aware compression wrapper exists to prevent.
func TestCompressResponsesLeavesEventStreamUnwrapped(t *testing.T) {
	r := chi.NewRouter()
	r.Use(compressResponses)
	r.Get("/api/channel/{slug}/events", func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.Errorf("SetWriteDeadline through the middleware chain: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: ping\ndata: {}\n\n")
	})
	r.Get("/api/channel/{slug}/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, `{"slug":"`+chi.URLParam(r, "slug")+`","name":"`+string(make([]byte, 2000))+`"}`)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	get := func(path string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		// The default transport would transparently decode gzip; a raw one
		// shows what actually travelled.
		resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}

	ev := get("/api/channel/x/events")
	defer ev.Body.Close()
	if enc := ev.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("event stream must not be compressed, got Content-Encoding %q", enc)
	}
	body, _ := io.ReadAll(ev.Body)
	if string(body) != "event: ping\ndata: {}\n\n" {
		t.Fatalf("event stream body altered: %q", body)
	}

	info := get("/api/channel/x/info")
	defer info.Body.Close()
	if enc := info.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("JSON must be gzipped, got Content-Encoding %q", enc)
	}
	zr, err := gzip.NewReader(info.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	if len(plain) < 2000 {
		t.Fatalf("decoded JSON too short: %d bytes", len(plain))
	}
}
