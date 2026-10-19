package main

import (
	"context"
	"encoding/gob"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boj/redistore"
	"github.com/go-chi/chi"
)

// ensureSessionStore stands up the session store main() would have built:
// the import handler keys its rate limiter by session e-mail when there is
// one, which reads the store.
func ensureSessionStore(t *testing.T) {
	t.Helper()
	gob.Register(Session{})
	if store == nil || store.Pool == nil {
		s, err := redistore.NewRediStore(10, redisType, redisAddr, "", redisPass, []byte("test-secret-key-for-import-api-tests"))
		if err != nil {
			t.Fatalf("session store: %v", err)
		}
		store = s
	}
}

func callImport(t *testing.T, slug, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	ensureSessionStore(t)
	r := chi.NewRouter()
	r.Post("/api/channel/{slug}/import/post", addNewPost)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/"+slug+"/import/post", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:4242"
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// A mistyped slug used to answer 401 (no configured key for a channel that
// does not exist) and burn the caller's failed-auth budget, so an integrator
// with a perfectly valid key debugged an authentication problem. Existence
// and the kill switch are checked first; the key only matters for a channel
// that can be posted to.
func TestImportUnknownAndDisabledChannelsAnswerBeforeKeyCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if rec := callImport(t, "import-no-such-chan", "", `{"text":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug: got %d (%s), want 404", rec.Code, rec.Body.String())
	}

	const slug = "import-disabled"
	ch := &ChannelData{Slug: slug, Name: slug, CreatedAt: time.Now()}
	ch.Features.Disabled = true
	if err := dbCreateChannel(ctx, ch); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		dbDeleteChannel(cctx, slug)
	})
	if rec := callImport(t, slug, "whatever", `{"text":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("disabled channel: got %d (%s), want 403", rec.Code, rec.Body.String())
	}

	// A live channel with no key configured stays closed.
	const live = "import-live-nokey"
	if err := dbCreateChannel(ctx, &ChannelData{Slug: live, Name: live, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		dbDeleteChannel(cctx, live)
	})
	if rec := callImport(t, live, "guess", `{"text":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("live channel, wrong key: got %d (%s), want 401", rec.Code, rec.Body.String())
	}
}
