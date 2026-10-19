package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

type AdsSettings struct {
	Src   string `json:"src"`
	Width int64  `json:"width"`
	// Locked tells the owner's settings screen that the super admin's
	// configuration is being served instead of the channel's own, which used
	// to be invisible: saves succeeded and changed nothing.
	Locked bool `json:"locked"`
}

// isChannelAdsLocked returns true if the super admin has locked ads settings for this channel.
func isChannelAdsLocked(globalAds *GlobalAdsConfig, ch *ChannelData) bool {
	if globalAds.LockAll {
		return true
	}
	// Guard the deref that follows: the LockedChannels lookup reads ch.Slug
	// unconditionally, so a nil ch (a caller outside channelMiddleware) would
	// panic. LockAll is already handled above; with no channel, nothing else can
	// be locked.
	if ch == nil {
		return false
	}
	if ch.Features.AdsLockedByAdmin {
		return true
	}
	return slices.Contains(globalAds.LockedChannels, ch.Slug)
}

func getAdsSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	ch := channelFromCtx(r)

	globalAds, err := dbGetGlobalAdsConfig(ctx)
	if err != nil {
		globalAds = &GlobalAdsConfig{}
	}

	var settings AdsSettings
	switch {
	case ch != nil && !ch.Features.Ads:
		// Operator-level kill switch. Answer with empty settings rather than an
		// error so the client simply renders no ad frame.
		settings = AdsSettings{}
	case isChannelAdsLocked(globalAds, ch):
		settings = AdsSettings{Src: globalAds.Src, Width: globalAds.Width, Locked: true}
	default:
		cfg := getChannelConfig(ctx, slug)
		settings = AdsSettings{Src: cfg.AdSrc, Width: cfg.AdWidth}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// getGlobalAdsConfig – super admin: read global ads config
func getGlobalAdsConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := dbGetGlobalAdsConfig(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// setGlobalAdsConfig – super admin: save global ads config + lock rules
func setGlobalAdsConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cfg GlobalAdsConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// The same rule the per-channel ad-iframe-src gets: this source is bound
	// to an <iframe src> on every locked channel.
	cfg.Src = strings.TrimSpace(cfg.Src)
	if cfg.Src != "" && !isFramableURL(cfg.Src) {
		http.Error(w, "src must be an absolute http(s) URL", http.StatusBadRequest)
		return
	}

	if err := dbSetGlobalAdsConfig(ctx, &cfg); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go syncAdsLockFlags(&cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// syncLockFlagsMu serialises the lock syncs. Two saves in quick succession
// (lock, then unlock) used to run as two interleaving goroutines with
// different snapshots, so the stale "locked" could land after the "unlocked"
// and the channel stayed locked with nothing in the global list naming it.
var syncLockFlagsMu sync.Mutex

// syncLockFlags propagates a global lock config (LockAll + LockedChannels) into
// the per-channel feature flag selected by get. Shared by the ads and magnet
// syncs so a robustness fix applied to one can never leave the other drifting.
//
// Each channel's flag is updated atomically against the stored features
// (dbUpdateChannelFeatures) rather than written back from the listing
// snapshot, so a features save made while the sync runs — disabling the
// channel, say — is not reverted by it. Failures are logged: a sync that died
// half-way used to leave the remaining channels locked, silently, with the
// admin shown a success.
func syncLockFlags(lockAll bool, lockedChannels []string, get func(*ChannelFeatures) *bool) {
	syncLockFlagsMu.Lock()
	defer syncLockFlagsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	channels, err := dbListChannels(ctx)
	if err != nil {
		log.Printf("syncLockFlags: listing channels failed, lock flags not synced: %v\n", err)
		return
	}

	failed := 0
	for _, ch := range channels {
		shouldLock := lockAll || slices.Contains(lockedChannels, ch.Slug)
		if *get(&ch.Features) == shouldLock {
			continue
		}
		err := dbUpdateChannelFeatures(ctx, ch.Slug, func(f *ChannelFeatures) {
			*get(f) = shouldLock
		})
		if err != nil {
			failed++
			log.Printf("syncLockFlags: %s: setting lock flag to %v failed: %v\n", ch.Slug, shouldLock, err)
		}
	}
	if failed > 0 {
		log.Printf("syncLockFlags: %d of %d channels not synced; saving the global config again retries\n", failed, len(channels))
	}
}

// syncAdsLockFlags updates AdsLockedByAdmin on all channels based on the new global config.
func syncAdsLockFlags(globalAds *GlobalAdsConfig) {
	syncLockFlags(globalAds.LockAll, globalAds.LockedChannels,
		func(f *ChannelFeatures) *bool { return &f.AdsLockedByAdmin })
}

type MagnetAdsSettings struct {
	Enabled              bool   `json:"enabled"`
	Snippet              string `json:"snippet"`
	Mode                 string `json:"mode"`
	PerMessages          int64  `json:"perMessages"`
	MinTimeSeconds       int64  `json:"minTimeSeconds"`
	PerSeconds           int64  `json:"perSeconds"`
	MinMessagesSinceLast int64  `json:"minMessagesSinceLast"`
	// Locked: see AdsSettings.Locked.
	Locked bool `json:"locked"`
}

// isChannelMagnetLocked returns true if the super admin has locked magnet settings for this channel.
func isChannelMagnetLocked(globalMagnet *GlobalMagnetConfig, ch *ChannelData) bool {
	if globalMagnet.LockAll {
		return true
	}
	// Same nil guard as isChannelAdsLocked: the LockedChannels lookup derefs
	// ch.Slug, so a nil channel must be handled before it.
	if ch == nil {
		return false
	}
	if ch.Features.MagnetLockedByAdmin {
		return true
	}
	return slices.Contains(globalMagnet.LockedChannels, ch.Slug)
}

func getMagnetAdsSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	ch := channelFromCtx(r)

	globalMagnet, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		globalMagnet = &GlobalMagnetConfig{}
	}

	var settings MagnetAdsSettings

	if isChannelMagnetLocked(globalMagnet, ch) && !globalMagnet.Enabled {
		// This endpoint is public and is also how a locked channel's managers
		// learn of the lock. The platform's placement rules are needed by a
		// viewer only while its ads run, so with them off nothing of the
		// global config goes out beyond the lock itself.
		settings = MagnetAdsSettings{Locked: true}
	} else if isChannelMagnetLocked(globalMagnet, ch) {
		// Super admin override: use global magnet config. The snippet and the
		// placement rules are what every viewer's browser needs to show the
		// ad, so they are public by nature; nothing else (embed API key,
		// stats key, lock list) is part of this view.
		settings = MagnetAdsSettings{
			Enabled:              globalMagnet.Enabled,
			Mode:                 globalMagnet.Mode,
			PerMessages:          globalMagnet.PerMessages,
			MinTimeSeconds:       globalMagnet.MinTimeSeconds,
			PerSeconds:           globalMagnet.PerSeconds,
			MinMessagesSinceLast: globalMagnet.MinMessagesSinceLast,
			Locked:               true,
		}
		if settings.Enabled {
			settings.Snippet = globalMagnet.Snippet
		}
	} else {
		// Use channel's own magnet settings
		cfg := getChannelConfig(ctx, slug)
		settings = MagnetAdsSettings{
			Enabled:              cfg.MagnetEnabled,
			Mode:                 cfg.MagnetMode,
			PerMessages:          cfg.MagnetPerMessages,
			MinTimeSeconds:       cfg.MagnetMinTimeSeconds,
			PerSeconds:           cfg.MagnetPerSeconds,
			MinMessagesSinceLast: cfg.MagnetMinMessagesSince,
		}
		if settings.Enabled {
			settings.Snippet = cfg.MagnetSnippet
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// statsKeyRe is the shape of a Magnet stats key (rotate_site_stats_key mints
// "mgs_" + 24 random bytes as lowercase hex). Checking it on save turns a
// mis-pasted key into an immediate Hebrew error instead of a 401 later on the
// statistics screen.
var statsKeyRe = regexp.MustCompile(`^mgs_[0-9a-f]{48}$`)

// globalMagnetConfigView is what the super-admin screen receives. It lists
// its fields explicitly, with no StatsKey at all, rather than embedding
// GlobalMagnetConfig and shadowing the field: a secret must not depend on
// encoding/json's tie-breaking rules to stay out of a response.
type globalMagnetConfigView struct {
	Enabled              bool     `json:"enabled"`
	Snippet              string   `json:"snippet"`
	Mode                 string   `json:"mode"`
	PerMessages          int64    `json:"perMessages"`
	MinTimeSeconds       int64    `json:"minTimeSeconds"`
	PerSeconds           int64    `json:"perSeconds"`
	MinMessagesSinceLast int64    `json:"minMessagesSinceLast"`
	ApiKey               string   `json:"apiKey"`
	LockAll              bool     `json:"lockAll"`
	LockedChannels       []string `json:"lockedChannels"`
	StatsKeySet          bool     `json:"statsKeySet"`
}

func magnetConfigView(cfg *GlobalMagnetConfig) globalMagnetConfigView {
	return globalMagnetConfigView{
		Enabled:              cfg.Enabled,
		Snippet:              cfg.Snippet,
		Mode:                 cfg.Mode,
		PerMessages:          cfg.PerMessages,
		MinTimeSeconds:       cfg.MinTimeSeconds,
		PerSeconds:           cfg.PerSeconds,
		MinMessagesSinceLast: cfg.MinMessagesSinceLast,
		ApiKey:               cfg.ApiKey,
		LockAll:              cfg.LockAll,
		LockedChannels:       cfg.LockedChannels,
		StatsKeySet:          cfg.StatsKey != "",
	}
}

// globalMagnetConfigInput is the POST body. The browser never holds the
// stored stats key, so an empty statsKey means "keep what is stored" and
// removing it needs its own explicit flag.
type globalMagnetConfigInput struct {
	GlobalMagnetConfig
	ClearStatsKey bool `json:"clearStatsKey"`
}

// writeJSONError answers with {"error":code,"message":message}, the shape the
// frontend maps to Hebrew text.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}

// getGlobalMagnetConfig – super admin: read global magnet config
func getGlobalMagnetConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "config_read_failed", "הגדרות מגנט לא נטענו. נסו שוב בעוד רגע.")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(magnetConfigView(cfg))
}

// setGlobalMagnetConfig – super admin: save global magnet config + lock rules
func setGlobalMagnetConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var in globalMagnetConfigInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// The stored key is needed to keep it when the form leaves the field
	// empty; saving over a config we could not read would silently erase it.
	current, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "config_read_failed", "הגדרות מגנט לא נטענו. נסו שוב בעוד רגע.")
		return
	}

	cfg := in.GlobalMagnetConfig
	k := strings.TrimSpace(in.StatsKey)
	switch {
	case in.ClearStatsKey:
		cfg.StatsKey = ""
	case k == "":
		cfg.StatsKey = current.StatsKey
	case statsKeyRe.MatchString(k):
		cfg.StatsKey = k
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_stats_key",
			"מפתח הסטטיסטיקה אינו תקין — הוא מתחיל ב-mgs_ ואחריו 48 תווים. העתיקו אותו שוב ממגנט: קוד הטמעה ← מפתח סטטיסטיקה לשרת.")
		return
	}

	if err := dbSetGlobalMagnetConfig(ctx, &cfg); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// Sync MagnetLockedByAdmin flag on each affected channel's features
	go syncMagnetLockFlags(&cfg)

	// The redacted view, so the screen learns statsKeySet without the key.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(magnetConfigView(&cfg))
}

// syncMagnetLockFlags updates MagnetLockedByAdmin on all channels based on the new global config.
// Called in a goroutine after saving global magnet config.
func syncMagnetLockFlags(globalMagnet *GlobalMagnetConfig) {
	syncLockFlags(globalMagnet.LockAll, globalMagnet.LockedChannels,
		func(f *ChannelFeatures) *bool { return &f.MagnetLockedByAdmin })
}

// magnetStatsURL is Magnet's server-to-server stats endpoint (F37). It
// replaced the old stats function, which took the public embed key in the query
// string — a key printed on every page that shows the widget — and so let
// anyone read the site's earnings. A var so tests can point it at httptest.
var magnetStatsURL = "https://rucltqmtefvlrjhbedqu.supabase.co/functions/v1/site-stats"

var magnetStatsClient = &http.Client{Timeout: 15 * time.Second}

func getMagnetStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	globalMagnet, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "config_read_failed", "הגדרות מגנט לא נטענו. נסו שוב בעוד רגע.")
		return
	}
	if globalMagnet.StatsKey == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_stats_key", "מפתח הסטטיסטיקה של מגנט לא הוגדר")
		return
	}
	proxyMagnetStats(w, r, globalMagnet.StatsKey)
}

// proxyMagnetStats asks Magnet's site-stats for the site that key belongs to
// and passes the answer through. Shared by the platform's own numbers (the
// global key) and an unlocked channel's (that channel's key): the two screens
// read one response shape and one set of error codes.
func proxyMagnetStats(w http.ResponseWriter, r *http.Request, key string) {
	// The key travels in a header and never in the URL, so it stays out of
	// access logs on both sides. Errors below never include it.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, magnetStatsURL, nil)
	if err != nil {
		http.Error(w, `{"error":"request_build_failed"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("x-magnet-stats-key", key)
	req.Header.Set("Accept", "application/json")

	resp, err := magnetStatsClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"upstream_unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"upstream_read_failed"}`, http.StatusBadGateway)
		return
	}

	// Magnet's 401 (invalid_key/missing_key) is about the stored stats key,
	// not the operator: forwarded as 401 it would hit the frontend's
	// session-expired interceptor, which signs the super admin out on any 401
	// under /api/super-admin/. It goes out as 502 with Magnet's body intact,
	// and the screen tells the cases apart by the body's error code.
	status := resp.StatusCode
	if status == http.StatusUnauthorized {
		status = http.StatusBadGateway
		// A 401 without an error code did not come from site-stats itself but
		// from the platform's JWT gate in front of it ({"code":401,...}), which
		// means site-stats was deployed with verify_jwt on. Passed through, the
		// screen could only call it an outage and the operator would keep
		// retrying a permanent setup fault, so it gets a code of its own.
		var upstream struct {
			Error any `json:"error"`
		}
		if json.Unmarshal(body, &upstream) != nil || upstream.Error == nil || upstream.Error == "" {
			w.Header().Set("Cache-Control", "no-store")
			writeJSONError(w, status, "upstream_unauthorized",
				"מגנט דחה את הבקשה — בדקו את מפתח הסטטיסטיקה ואת פריסת site-stats במגנט.")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(body)
}

// ─── A channel's own Magnet numbers ────────────────────────────────────────
//
// The owner's rule: a channel that manages its own Magnet embed (not locked)
// earns from it, so its managers may see its numbers, read with a stats key
// from their own Magnet account. A locked channel serves the platform's embed
// and the earnings are the platform's: its managers get nothing at all — no
// key field, no numbers — and these routes refuse before touching Magnet,
// even when a key was saved before the lock.

const channelMagnetLockedMessage = "הנהלת המערכת קבעה הגדרות פרסומת אחידות לערוץ הזה, ולכן אין כאן נתוני מגנט."

// channelMagnetUnlocked answers 403 (or 500) and returns false unless the
// channel in the request manages its own Magnet embed. A global config that
// cannot be read counts as locked: guessing "unlocked" would hand the
// platform's channels a route to Magnet.
func channelMagnetUnlocked(ctx context.Context, w http.ResponseWriter, r *http.Request) bool {
	globalMagnet, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "config_read_failed", "הגדרות מגנט לא נטענו. נסו שוב בעוד רגע.")
		return false
	}
	ch := channelFromCtx(r)
	if ch == nil || isChannelMagnetLocked(globalMagnet, ch) {
		writeJSONError(w, http.StatusForbidden, "magnet_locked", channelMagnetLockedMessage)
		return false
	}
	return true
}

// channelMagnetStatsKeyView is all a browser learns about the channel's key.
type channelMagnetStatsKeyView struct {
	StatsKeySet bool `json:"statsKeySet"`
}

// getChannelMagnetStatsKey – owner: is a stats key stored for this channel?
func getChannelMagnetStatsKey(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if !channelMagnetUnlocked(ctx, w, r) {
		return
	}
	key, err := dbGetChannelMagnetStatsKey(ctx, channelSlugFromCtx(r))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "config_read_failed", "הגדרות מגנט לא נטענו. נסו שוב בעוד רגע.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(channelMagnetStatsKeyView{StatsKeySet: key != ""})
}

// setChannelMagnetStatsKey – owner: store, replace or remove the channel's
// key. Unlike the global form there is nothing else in the body, so an empty
// key is refused rather than read as "keep".
func setChannelMagnetStatsKey(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var in struct {
		StatsKey      string `json:"statsKey"`
		ClearStatsKey bool   `json:"clearStatsKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if !channelMagnetUnlocked(ctx, w, r) {
		return
	}

	k := strings.TrimSpace(in.StatsKey)
	switch {
	case in.ClearStatsKey:
		k = ""
	case !statsKeyRe.MatchString(k):
		writeJSONError(w, http.StatusBadRequest, "invalid_stats_key",
			"מפתח הסטטיסטיקה אינו תקין — הוא מתחיל ב-mgs_ ואחריו 48 תווים. העתיקו אותו שוב מחשבון המגנט שלכם: קוד הטמעה ← מפתח סטטיסטיקה.")
		return
	}

	if err := dbSetChannelMagnetStatsKey(ctx, channelSlugFromCtx(r), k); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "config_write_failed", "שמירת המפתח נכשלה. נסו שוב בעוד רגע.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(channelMagnetStatsKeyView{StatsKeySet: k != ""})
}

// getChannelMagnetStats – owner: the channel's own numbers from Magnet.
func getChannelMagnetStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if !channelMagnetUnlocked(ctx, w, r) {
		return
	}
	key, err := dbGetChannelMagnetStatsKey(ctx, channelSlugFromCtx(r))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "config_read_failed", "הגדרות מגנט לא נטענו. נסו שוב בעוד רגע.")
		return
	}
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_stats_key", "מפתח הסטטיסטיקה של הערוץ לא הוגדר")
		return
	}
	proxyMagnetStats(w, r, key)
}
