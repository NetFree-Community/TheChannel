package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

type AdsSettings struct {
	Src   string `json:"src"`
	Width int64  `json:"width"`
}

// isChannelAdsLocked returns true if the super admin has locked ads settings for this channel.
func isChannelAdsLocked(globalAds *GlobalAdsConfig, ch *ChannelData) bool {
	if globalAds.LockAll {
		return true
	}
	if ch != nil && ch.Features.AdsLockedByAdmin {
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
		settings = AdsSettings{Src: globalAds.Src, Width: globalAds.Width}
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

	if err := dbSetGlobalAdsConfig(ctx, &cfg); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go syncAdsLockFlags(&cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// syncLockFlags propagates a global lock config (LockAll + LockedChannels) into
// the per-channel feature flag selected by get. Shared by the ads and magnet
// syncs so a robustness fix applied to one can never leave the other drifting.
func syncLockFlags(lockAll bool, lockedChannels []string, get func(*ChannelFeatures) *bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channels, err := dbListChannels(ctx)
	if err != nil {
		return
	}

	for _, ch := range channels {
		shouldLock := lockAll || slices.Contains(lockedChannels, ch.Slug)
		flag := get(&ch.Features)
		if *flag != shouldLock {
			*flag = shouldLock
			dbSetChannelFeatures(ctx, ch.Slug, &ch.Features)
		}
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
}

// isChannelMagnetLocked returns true if the super admin has locked magnet settings for this channel.
func isChannelMagnetLocked(globalMagnet *GlobalMagnetConfig, ch *ChannelData) bool {
	if globalMagnet.LockAll {
		return true
	}
	if ch != nil && ch.Features.MagnetLockedByAdmin {
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

	if isChannelMagnetLocked(globalMagnet, ch) {
		// Super admin override: use global magnet config
		settings = MagnetAdsSettings{
			Enabled:              globalMagnet.Enabled,
			Mode:                 globalMagnet.Mode,
			PerMessages:          globalMagnet.PerMessages,
			MinTimeSeconds:       globalMagnet.MinTimeSeconds,
			PerSeconds:           globalMagnet.PerSeconds,
			MinMessagesSinceLast: globalMagnet.MinMessagesSinceLast,
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

// getGlobalMagnetConfig – super admin: read global magnet config
func getGlobalMagnetConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// setGlobalMagnetConfig – super admin: save global magnet config + lock rules
func setGlobalMagnetConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cfg GlobalMagnetConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if err := dbSetGlobalMagnetConfig(ctx, &cfg); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// Sync MagnetLockedByAdmin flag on each affected channel's features
	go syncMagnetLockFlags(&cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// syncMagnetLockFlags updates MagnetLockedByAdmin on all channels based on the new global config.
// Called in a goroutine after saving global magnet config.
func syncMagnetLockFlags(globalMagnet *GlobalMagnetConfig) {
	syncLockFlags(globalMagnet.LockAll, globalMagnet.LockedChannels,
		func(f *ChannelFeatures) *bool { return &f.MagnetLockedByAdmin })
}

const magnetStatsURL = "https://rucltqmtefvlrjhbedqu.supabase.co/functions/v1/publisher-stats"

var magnetStatsClient = &http.Client{Timeout: 15 * time.Second}

func getMagnetStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	globalMagnet, err := dbGetGlobalMagnetConfig(ctx)
	if err != nil || globalMagnet.ApiKey == "" {
		http.Error(w, `{"error":"missing_api_key","message":"Magnet API key is not configured"}`, http.StatusBadRequest)
		return
	}

	q := url.Values{}
	q.Set("k", globalMagnet.ApiKey)
	reqURL := magnetStatsURL + "?" + q.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, reqURL, nil)
	if err != nil {
		http.Error(w, `{"error":"request_build_failed"}`, http.StatusInternalServerError)
		return
	}

	resp, err := magnetStatsClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"upstream_unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"upstream_read_failed"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}
