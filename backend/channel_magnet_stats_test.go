package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A channel's own Magnet numbers (ads.go: getChannelMagnetStats and the
// stats-key routes). The owner's rule: an unlocked channel sees its own
// numbers through its own key; a locked one sees nothing, and Magnet is never
// even asked. Real Redis, like magnet_stats_test.go.

const (
	cmsOwnerEmail     = "cms-owner@example.com"
	cmsModeratorEmail = "cms-moderator@example.com"
	cmsStrangerEmail  = "cms-stranger@example.com"
)

type cmsFixture struct {
	slug     string
	upstream *atomic.Int32
	// seenKey is the x-magnet-stats-key of the last upstream call.
	seenKey *atomic.Value
	base    GlobalMagnetConfig
}

// newCMSFixture seeds an unlocked channel with an owner and a moderator, a
// fake site-stats that counts its calls, and a global config whose own stats
// key (testStatsKeyA) must never be the one a channel route uses.
func newCMSFixture(t *testing.T, slug string) *cmsFixture {
	t.Helper()
	ctx := testCtx(t)
	useTestSessionStore(t)
	base := preserveGlobalMagnet(t, ctx)

	seedChannel(t, ctx, slug, Settings{{Key: "magnet_enabled", Value: "1"}})

	privilegesUsers.Store(cmsOwnerEmail, User{Email: cmsOwnerEmail, ChannelRoles: map[string]ChannelRole{slug: RoleOwner}})
	privilegesUsers.Store(cmsModeratorEmail, User{Email: cmsModeratorEmail, ChannelRoles: map[string]ChannelRole{slug: RoleModerator}})
	privilegesUsers.Store(cmsStrangerEmail, User{Email: cmsStrangerEmail})
	t.Cleanup(func() {
		privilegesUsers.Delete(cmsOwnerEmail)
		privilegesUsers.Delete(cmsModeratorEmail)
		privilegesUsers.Delete(cmsStrangerEmail)
	})

	f := &cmsFixture{slug: slug, upstream: &atomic.Int32{}, seenKey: &atomic.Value{}}
	f.seenKey.Store("")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.upstream.Add(1)
		f.seenKey.Store(r.Header.Get("x-magnet-stats-key"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"site":{"id":"s1","domain":"channel.example"},"currency":"ILS","clicks":{"today":1,"week":2,"month":3}}`))
	}))
	t.Cleanup(up.Close)
	magnetStatsURL = up.URL + "/functions/v1/site-stats"

	cfg := base
	cfg.LockAll = false
	cfg.LockedChannels = nil
	cfg.Enabled = true
	cfg.ApiKey = "platform-public-embed-key"
	cfg.StatsKey = testStatsKeyA
	storeMagnetConfig(t, ctx, cfg)
	f.base = cfg
	return f
}

// call runs handler behind the route's real role gate, as the session user,
// with the channel as channelMiddleware would load it (live features).
func (f *cmsFixture) call(t *testing.T, email string, handler http.HandlerFunc, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	r := requestAs(t, Session{ID: email, Email: email}, f.slug, method, "/api/channel/"+f.slug+"/admin/magnet", rd, nil)
	ch, err := dbGetChannel(r.Context(), f.slug)
	if err != nil {
		t.Fatalf("load channel: %v", err)
	}
	r = r.WithContext(context.WithValue(r.Context(), channelCtxKey, ch))
	rec := httptest.NewRecorder()
	protectedWithChannelRole(RoleOwner, handler)(rec, r)
	return rec
}

func (f *cmsFixture) setChannelKey(t *testing.T, key string) {
	t.Helper()
	if err := dbSetChannelMagnetStatsKey(testCtx(t), f.slug, key); err != nil {
		t.Fatalf("store channel key: %v", err)
	}
}

func (f *cmsFixture) storedChannelKey(t *testing.T) string {
	t.Helper()
	k, err := dbGetChannelMagnetStatsKey(testCtx(t), f.slug)
	if err != nil {
		t.Fatalf("read channel key: %v", err)
	}
	return k
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var e map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body is not JSON: %s", rec.Body)
	}
	return e["error"], e["message"]
}

func TestChannelMagnetStatsLockedRefusesWithoutCallingMagnet(t *testing.T) {
	f := newCMSFixture(t, "cms-test-locked")
	ctx := testCtx(t)

	locks := []struct {
		name  string
		apply func(t *testing.T)
	}{
		{"lockAll", func(t *testing.T) {
			cfg := f.base
			cfg.LockAll = true
			storeMagnetConfig(t, ctx, cfg)
		}},
		{"locked list", func(t *testing.T) {
			cfg := f.base
			cfg.LockedChannels = []string{"someone-else", f.slug}
			storeMagnetConfig(t, ctx, cfg)
		}},
		{"feature flag", func(t *testing.T) {
			storeMagnetConfig(t, ctx, f.base)
			if err := dbUpdateChannelFeatures(ctx, f.slug, func(cf *ChannelFeatures) { cf.MagnetLockedByAdmin = true }); err != nil {
				t.Fatalf("lock flag: %v", err)
			}
			t.Cleanup(func() {
				_ = dbUpdateChannelFeatures(context.Background(), f.slug, func(cf *ChannelFeatures) { cf.MagnetLockedByAdmin = false })
			})
		}},
	}
	for _, lk := range locks {
		t.Run(lk.name, func(t *testing.T) {
			// A key saved while the channel was still its own.
			f.setChannelKey(t, testStatsKeyB)
			lk.apply(t)
			before := f.upstream.Load()

			for _, c := range []struct {
				name    string
				handler http.HandlerFunc
				method  string
				body    any
			}{
				{"stats", getChannelMagnetStats, http.MethodGet, nil},
				{"key get", getChannelMagnetStatsKey, http.MethodGet, nil},
				{"key set", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"statsKey": testStatsKeyA}},
				{"key clear", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"clearStatsKey": true}},
			} {
				rec := f.call(t, cmsOwnerEmail, c.handler, c.method, c.body)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s: status = %d, want 403; body %s", c.name, rec.Code, rec.Body)
				}
				if code, msg := errorCode(t, rec); code != "magnet_locked" || msg == "" {
					t.Errorf("%s: error = %q %q, want magnet_locked with a Hebrew message", c.name, code, msg)
				}
				if strings.Contains(rec.Body.String(), "statsKeySet") {
					t.Errorf("%s: a locked channel learns about the key: %s", c.name, rec.Body)
				}
			}
			if n := f.upstream.Load() - before; n != 0 {
				t.Errorf("Magnet called %d times for a locked channel", n)
			}
			if got := f.storedChannelKey(t); got != testStatsKeyB {
				t.Errorf("stored key changed under the lock: %q", got)
			}
		})
	}
}

func TestChannelMagnetStatsWithoutKeyDoesNotCallMagnet(t *testing.T) {
	f := newCMSFixture(t, "cms-test-nokey")
	f.setChannelKey(t, "")

	rec := f.call(t, cmsOwnerEmail, getChannelMagnetStats, http.MethodGet, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
	}
	if code, _ := errorCode(t, rec); code != "missing_stats_key" {
		t.Errorf("error = %q, want missing_stats_key", code)
	}
	if n := f.upstream.Load(); n != 0 {
		t.Errorf("Magnet called %d times without a channel key", n)
	}
	// The platform's key is configured, and must not stand in for the channel's.
	if k := f.seenKey.Load().(string); k != "" {
		t.Errorf("upstream saw a key: %q", k)
	}
}

func TestChannelMagnetStatsProxiesWithChannelKey(t *testing.T) {
	f := newCMSFixture(t, "cms-test-proxy")
	f.setChannelKey(t, testStatsKeyB)

	rec := f.call(t, cmsOwnerEmail, getChannelMagnetStats, http.MethodGet, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if n := f.upstream.Load(); n != 1 {
		t.Fatalf("Magnet called %d times, want 1", n)
	}
	if k := f.seenKey.Load().(string); k != testStatsKeyB {
		t.Errorf("upstream saw key %q, want the channel's own (never the platform's)", k)
	}
	if !strings.Contains(rec.Body.String(), "channel.example") {
		t.Errorf("Magnet's body not passed through: %s", rec.Body)
	}
	for _, k := range []string{testStatsKeyA, testStatsKeyB} {
		if strings.Contains(rec.Body.String(), k) {
			t.Errorf("response leaks a stats key: %s", rec.Body)
		}
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestChannelMagnetStatsKeyIsNeverReturned(t *testing.T) {
	f := newCMSFixture(t, "cms-test-redact")
	f.setChannelKey(t, "")

	steps := []struct {
		name       string
		handler    http.HandlerFunc
		method     string
		body       any
		wantStatus int
		wantStored string
		wantSet    bool
	}{
		{"get empty", getChannelMagnetStatsKey, http.MethodGet, nil, 200, "", false},
		{"set", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"statsKey": "  " + testStatsKeyB + "\n"}, 200, testStatsKeyB, true},
		{"get stored", getChannelMagnetStatsKey, http.MethodGet, nil, 200, testStatsKeyB, true},
		{"empty refused", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"statsKey": ""}, 400, testStatsKeyB, false},
		{"embed key refused", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"statsKey": "public-embed-key"}, 400, testStatsKeyB, false},
		{"uppercase refused", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"statsKey": strings.ToUpper(testStatsKeyA)}, 400, testStatsKeyB, false},
		{"replace", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"statsKey": testStatsKeyA}, 200, testStatsKeyA, true},
		{"clear wins", setChannelMagnetStatsKey, http.MethodPost, map[string]any{"clearStatsKey": true, "statsKey": testStatsKeyB}, 200, "", false},
	}
	for _, s := range steps {
		rec := f.call(t, cmsOwnerEmail, s.handler, s.method, s.body)
		if rec.Code != s.wantStatus {
			t.Fatalf("%s: status = %d, want %d; body %s", s.name, rec.Code, s.wantStatus, rec.Body)
		}
		if got := f.storedChannelKey(t); got != s.wantStored {
			t.Errorf("%s: stored = %q, want %q", s.name, got, s.wantStored)
		}
		if s.wantStatus != http.StatusOK {
			if code, msg := errorCode(t, rec); code != "invalid_stats_key" || msg == "" {
				t.Errorf("%s: error = %q %q, want invalid_stats_key with a message", s.name, code, msg)
			}
			continue
		}
		if got := assertRedacted(t, rec.Body.Bytes(), testStatsKeyA, testStatsKeyB); got != s.wantSet {
			t.Errorf("%s: statsKeySet = %v, want %v", s.name, got, s.wantSet)
		}
	}

	// Nor through the settings blob the magnet tab loads, nor the public
	// magnet endpoint every viewer reads.
	f.setChannelKey(t, testStatsKeyB)
	for name, h := range map[string]http.HandlerFunc{"settings/get": getSettings, "ads/magnet": getMagnetAdsSettings} {
		rec := f.call(t, cmsOwnerEmail, h, http.MethodGet, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), testStatsKeyB) || strings.Contains(rec.Body.String(), testStatsKeyA) {
			t.Errorf("%s leaks a stats key: %s", name, rec.Body)
		}
	}
}

func TestChannelMagnetStatsRequiresOwner(t *testing.T) {
	f := newCMSFixture(t, "cms-test-roles")
	f.setChannelKey(t, testStatsKeyB)

	for _, email := range []string{cmsModeratorEmail, cmsStrangerEmail} {
		for name, h := range map[string]http.HandlerFunc{
			"stats": getChannelMagnetStats, "key get": getChannelMagnetStatsKey, "key set": setChannelMagnetStatsKey,
		} {
			method, body := http.MethodGet, any(nil)
			if name == "key set" {
				method, body = http.MethodPost, map[string]any{"clearStatsKey": true}
			}
			rec := f.call(t, email, h, method, body)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s as %s: status = %d, want 403", name, email, rec.Code)
			}
		}
	}
	if n := f.upstream.Load(); n != 0 {
		t.Errorf("Magnet called %d times for a non-owner", n)
	}
	if got := f.storedChannelKey(t); got != testStatsKeyB {
		t.Errorf("a non-owner changed the key: %q", got)
	}
}

// The public magnet endpoint is also what a locked channel's managers read to
// learn of the lock. It carries what a viewer needs to show the platform's ad
// and nothing more of the global config.
func TestLockedChannelMagnetSettingsCarryNoGlobalData(t *testing.T) {
	f := newCMSFixture(t, "cms-test-public")
	ctx := testCtx(t)

	cfg := f.base
	cfg.LockedChannels = []string{"cms-other-locked", f.slug}
	cfg.Snippet = `<script src="https://magnet.example/platform-embed.js"></script>`
	cfg.Mode = "by_time"
	cfg.PerSeconds = 777
	cfg.MinMessagesSinceLast = 33

	for _, enabled := range []bool{false, true} {
		cfg.Enabled = enabled
		storeMagnetConfig(t, ctx, cfg)
		rec := f.call(t, cmsOwnerEmail, getMagnetAdsSettings, http.MethodGet, nil)
		body := rec.Body.String()
		var got MagnetAdsSettings
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("enabled=%v: %v: %s", enabled, err, body)
		}
		if !got.Locked {
			t.Errorf("enabled=%v: locked = false: %s", enabled, body)
		}
		for _, secret := range []string{cfg.ApiKey, testStatsKeyA, "cms-other-locked", "lockedChannels", "apiKey", "statsKey"} {
			if strings.Contains(body, secret) {
				t.Errorf("enabled=%v: response carries %q: %s", enabled, secret, body)
			}
		}
		if !enabled {
			if got.Snippet != "" || got.Mode != "" || got.PerSeconds != 0 || got.MinMessagesSinceLast != 0 {
				t.Errorf("global ads are off, yet their settings went out: %s", body)
			}
		} else if got.Snippet != cfg.Snippet || got.PerSeconds != 777 {
			t.Errorf("viewers of a locked channel lost the platform's ad: %s", body)
		}
	}
}
