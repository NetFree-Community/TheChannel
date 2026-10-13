package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercised against real Redis like my_channels_test.go. The global magnet
// config is a single shared key, so each test saves the stored value and puts
// it back afterwards.

const (
	testStatsKeyA = "mgs_0123456789abcdef0123456789abcdef0123456789abcdef"
	testStatsKeyB = "mgs_fedcba9876543210fedcba9876543210fedcba9876543210"
)

// preserveGlobalMagnet restores global:magnet:config and magnetStatsURL when
// the test ends, and returns the original config so POST bodies can reuse its
// lock rules (the sync goroutine then has nothing to change on channels).
func preserveGlobalMagnet(t *testing.T, ctx context.Context) GlobalMagnetConfig {
	t.Helper()
	orig, origErr := rdb.Get(ctx, "global:magnet:config").Result()
	origURL := magnetStatsURL
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		magnetStatsURL = origURL
		if origErr != nil {
			_ = rdb.Del(cctx, "global:magnet:config").Err()
			return
		}
		_ = rdb.Set(cctx, "global:magnet:config", orig, 0).Err()
	})
	cfg, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		t.Fatalf("read global magnet config: %v", err)
	}
	return *cfg
}

func storeMagnetConfig(t *testing.T, ctx context.Context, cfg GlobalMagnetConfig) {
	t.Helper()
	if err := dbSetGlobalMagnetConfig(ctx, &cfg); err != nil {
		t.Fatalf("store global magnet config: %v", err)
	}
}

func storedStatsKey(t *testing.T, ctx context.Context) string {
	t.Helper()
	cfg, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		t.Fatalf("read global magnet config: %v", err)
	}
	return cfg.StatsKey
}

// assertRedacted fails when a response carries the key or a statsKey field,
// and returns the decoded statsKeySet.
func assertRedacted(t *testing.T, body []byte, keys ...string) bool {
	t.Helper()
	for _, k := range keys {
		if bytes.Contains(body, []byte(k)) {
			t.Fatalf("response leaks the stats key: %s", body)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, body)
	}
	if _, ok := m["statsKey"]; ok {
		t.Fatalf("response has a statsKey field: %s", body)
	}
	set, ok := m["statsKeySet"].(bool)
	if !ok {
		t.Fatalf("response has no boolean statsKeySet: %s", body)
	}
	return set
}

func TestGlobalMagnetConfigGetRedactsStatsKey(t *testing.T) {
	ctx := testCtx(t)
	base := preserveGlobalMagnet(t, ctx)

	for _, tc := range []struct {
		name    string
		key     string
		wantSet bool
	}{
		{"stored", testStatsKeyA, true},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.ApiKey = "public-embed-key"
			cfg.StatsKey = tc.key
			storeMagnetConfig(t, ctx, cfg)

			rec := httptest.NewRecorder()
			getGlobalMagnetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/magnet/config", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			if got := assertRedacted(t, rec.Body.Bytes(), testStatsKeyA); got != tc.wantSet {
				t.Errorf("statsKeySet = %v, want %v", got, tc.wantSet)
			}
			// The rest of the config still reaches the screen.
			if !strings.Contains(rec.Body.String(), `"apiKey":"public-embed-key"`) {
				t.Errorf("apiKey missing from view: %s", rec.Body)
			}
		})
	}
}

func TestGlobalMagnetConfigPostKeepReplaceClear(t *testing.T) {
	ctx := testCtx(t)
	base := preserveGlobalMagnet(t, ctx)

	post := func(t *testing.T, extra map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		body := map[string]any{
			"enabled":        base.Enabled,
			"snippet":        base.Snippet,
			"mode":           base.Mode,
			"apiKey":         base.ApiKey,
			"lockAll":        base.LockAll,
			"lockedChannels": base.LockedChannels,
		}
		for k, v := range extra {
			body[k] = v
		}
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		setGlobalMagnetConfig(rec, httptest.NewRequest(http.MethodPost, "/api/super-admin/magnet/config", bytes.NewReader(raw)))
		return rec
	}

	cases := []struct {
		name       string
		extra      map[string]any
		wantStatus int
		wantStored string
		wantSet    bool
	}{
		{"empty keeps", map[string]any{"statsKey": ""}, 200, testStatsKeyA, true},
		{"absent keeps", map[string]any{}, 200, testStatsKeyA, true},
		{"whitespace keeps", map[string]any{"statsKey": "   "}, 200, testStatsKeyA, true},
		{"valid replaces", map[string]any{"statsKey": testStatsKeyB}, 200, testStatsKeyB, true},
		{"valid with spaces is trimmed", map[string]any{"statsKey": "  " + testStatsKeyB + "\n"}, 200, testStatsKeyB, true},
		{"clear", map[string]any{"clearStatsKey": true}, 200, "", false},
		{"clear wins over a supplied key", map[string]any{"clearStatsKey": true, "statsKey": testStatsKeyB}, 200, "", false},
		{"uppercase rejected", map[string]any{"statsKey": strings.ToUpper(testStatsKeyB)}, 400, testStatsKeyA, false},
		{"embed key rejected", map[string]any{"statsKey": "public-embed-key"}, 400, testStatsKeyA, false},
		{"too short rejected", map[string]any{"statsKey": testStatsKeyB[:len(testStatsKeyB)-1]}, 400, testStatsKeyA, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := base
			start.StatsKey = testStatsKeyA
			storeMagnetConfig(t, ctx, start)

			rec := post(t, tc.extra)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if got := storedStatsKey(t, ctx); got != tc.wantStored {
				t.Errorf("stored key = %q, want %q", got, tc.wantStored)
			}
			if tc.wantStatus == http.StatusOK {
				if got := assertRedacted(t, rec.Body.Bytes(), testStatsKeyA, testStatsKeyB); got != tc.wantSet {
					t.Errorf("statsKeySet = %v, want %v", got, tc.wantSet)
				}
				return
			}
			var e map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
				t.Fatalf("error body is not JSON: %s", rec.Body)
			}
			if e["error"] != "invalid_stats_key" || e["message"] == "" {
				t.Errorf("error body = %v, want invalid_stats_key with a message", e)
			}
		})
	}
}

func TestMagnetStatsWithoutKeyDoesNotCallMagnet(t *testing.T) {
	ctx := testCtx(t)
	base := preserveGlobalMagnet(t, ctx)

	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer up.Close()
	magnetStatsURL = up.URL + "/functions/v1/site-stats"

	cfg := base
	// The public embed key alone must no longer reach Magnet at all.
	cfg.ApiKey = "public-embed-key"
	cfg.StatsKey = ""
	storeMagnetConfig(t, ctx, cfg)

	rec := httptest.NewRecorder()
	getMagnetStats(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/magnet/stats", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
	}
	var e map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e["error"] != "missing_stats_key" {
		t.Errorf("error = %q, want missing_stats_key", e["error"])
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("upstream called %d times without a stats key", n)
	}
}

func TestMagnetStatsProxiesWithHeader(t *testing.T) {
	ctx := testCtx(t)
	base := preserveGlobalMagnet(t, ctx)

	type seen struct {
		method, path, rawQuery, key string
	}
	var got seen
	status := http.StatusOK
	respBody := `{"site":{"id":"s1","domain":"example.com"},"currency":"ILS","clicks":{"today":1,"week":2,"month":3},"earnings":{"today":0.5,"week":1,"month":1.5},"generated_at":"2026-10-13T09:30:00.000Z"}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("x-magnet-stats-key")}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(respBody))
	}))
	defer up.Close()
	magnetStatsURL = up.URL + "/functions/v1/site-stats"

	cfg := base
	cfg.ApiKey = "public-embed-key"
	cfg.StatsKey = testStatsKeyA
	storeMagnetConfig(t, ctx, cfg)

	rec := httptest.NewRecorder()
	getMagnetStats(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/magnet/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if rec.Body.String() != respBody {
		t.Errorf("body not passed through:\n got %s\nwant %s", rec.Body, respBody)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	want := seen{http.MethodGet, "/functions/v1/site-stats", "", testStatsKeyA}
	if got != want {
		t.Errorf("upstream saw %+v, want %+v (key in header, nothing in the query string)", got, want)
	}

	// Other upstream statuses pass through unchanged.
	status = http.StatusNotFound
	respBody = `{"error":"site_not_active","message":"הפלטפורמה אינה מאושרת במגנט או שהחשבון מושעה"}`
	rec = httptest.NewRecorder()
	getMagnetStats(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/magnet/stats", nil))
	if rec.Code != http.StatusNotFound || rec.Body.String() != respBody {
		t.Errorf("got %d %s, want 404 passed through", rec.Code, rec.Body)
	}

	// A revoked or wrong key: Magnet's body reaches the screen, but never as
	// a 401, which the frontend reads as the operator's session ending.
	status = http.StatusUnauthorized
	respBody = `{"error":"invalid_key","message":"מפתח הסטטיסטיקה שגוי או שבוטל"}`
	rec = httptest.NewRecorder()
	getMagnetStats(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/magnet/stats", nil))
	if rec.Code != http.StatusBadGateway || rec.Body.String() != respBody {
		t.Errorf("got %d %s, want 502 with Magnet's invalid_key body", rec.Code, rec.Body)
	}

	// The platform's JWT gate answers 401 with no error code (site-stats
	// deployed with verify_jwt on). It must not read as an outage: it gets
	// upstream_unauthorized, still as 502 so the operator stays signed in.
	for _, gateBody := range []string{`{"code":401,"message":"Missing authorization header"}`, `Unauthorized`, ``} {
		status = http.StatusUnauthorized
		respBody = gateBody
		rec = httptest.NewRecorder()
		getMagnetStats(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/magnet/stats", nil))
		var e map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusBadGateway || e["error"] != "upstream_unauthorized" || e["message"] == "" {
			t.Errorf("upstream 401 %q: got %d %s, want 502 upstream_unauthorized with a message", gateBody, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), testStatsKeyA) {
			t.Errorf("upstream 401 %q: response leaks the stats key: %s", gateBody, rec.Body)
		}
	}
}
