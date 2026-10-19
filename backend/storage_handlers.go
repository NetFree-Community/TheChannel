package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi"
)

// ─── Storage Info Response ────────────────────────────────────────────────────

type StorageInfo struct {
	UsedBytes   int64   `json:"usedBytes"`
	QuotaBytes  int64   `json:"quotaBytes"`
	UsedPercent float64 `json:"usedPercent"`
	AutoCleanup bool    `json:"autoCleanup"`
	// Warning levels: "ok" | "warning" (>80%) | "critical" (>90%)
	Level string `json:"level"`
}

// gbToBytes converts a GB quota to bytes, reporting whether the value is usable.
// A negative quota makes every upload fail the used<=quota test platform-wide
// while the dashboard still shows "ok"; a value so large that gb*2^30 overflows
// int64 wraps to a negative number and bypasses a plain ">= 0" guard, producing
// the same silent breakage. Both are rejected here.
func gbToBytes(gb float64) (int64, bool) {
	const maxInt64 = float64(1<<63 - 1)
	bytesFloat := gb * (1024 * 1024 * 1024)
	if gb < 0 || bytesFloat > maxInt64 {
		return 0, false
	}
	return int64(bytesFloat), true
}

func buildStorageInfo(ctx context.Context, slug string) (*StorageInfo, error) {
	quota, err := dbGetEffectiveStorageQuota(ctx, slug)
	if err != nil {
		return nil, err
	}
	used, err := dbGetChannelStorageUsed(ctx, slug)
	if err != nil {
		return nil, err
	}
	autoCleanup, err := dbGetChannelAutoCleanup(ctx, slug)
	if err != nil {
		return nil, err
	}

	var pct float64
	if quota > 0 {
		// One decimal: the super-admin storage bar binds the value as-is and
		// printed the raw division ("43.333333333333336%").
		pct = math.Round(float64(used)/float64(quota)*1000) / 10
	}

	level := "ok"
	if pct >= 90 {
		level = "critical"
	} else if pct >= 80 {
		level = "warning"
	}

	return &StorageInfo{
		UsedBytes:   used,
		QuotaBytes:  quota,
		UsedPercent: pct,
		AutoCleanup: autoCleanup,
		Level:       level,
	}, nil
}

// ─── Channel Owner Handlers ───────────────────────────────────────────────────

// GET /api/channel/{slug}/admin/storage
func getChannelStorageInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	info, err := buildStorageInfo(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// POST /api/channel/{slug}/admin/storage/auto-cleanup
func setChannelAutoCleanup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if err := dbSetChannelAutoCleanup(ctx, slug, req.Enabled); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// ─── Super Admin Handlers ─────────────────────────────────────────────────────

type GlobalStorageConfig struct {
	DefaultQuotaGB float64 `json:"defaultQuotaGb"` // in GB for display
}

// GET /api/super-admin/storage/config
func getSuperAdminStorageConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	quotaBytes, err := dbGetGlobalStorageQuota(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	cfg := GlobalStorageConfig{
		DefaultQuotaGB: float64(quotaBytes) / (1024 * 1024 * 1024),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// POST /api/super-admin/storage/config
func setSuperAdminStorageConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req GlobalStorageConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// A negative quota would make every upload fail the used+size <= quota test
	// platform-wide while the dashboard still shows "ok" (pct math is guarded by
	// quota > 0), so it must be rejected up front. Zero is refused too: unlike a
	// per-channel quota, the global default has no "use the default" meaning —
	// a cleared form field posted 0, and 0 made every channel's storage
	// unlimited while the dashboards showed "of 0 B".
	bytes, ok := gbToBytes(req.DefaultQuotaGB)
	if !ok || req.DefaultQuotaGB <= 0 {
		http.Error(w, "quota must be a positive number of gigabytes", http.StatusBadRequest)
		return
	}
	if err := dbSetGlobalStorageQuota(ctx, bytes); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

type ChannelStorageConfig struct {
	QuotaGB     float64     `json:"quotaGb"`     // 0 = use global
	StorageInfo StorageInfo `json:"storageInfo"` // current usage
}

// GET /api/super-admin/channels/{slug}/storage
func getSuperAdminChannelStorage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")
	if !requireExistingChannel(ctx, w, slug) {
		return
	}

	quotaBytes, err := dbGetChannelStorageQuota(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	info, err := buildStorageInfo(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	cfg := ChannelStorageConfig{
		QuotaGB:     float64(quotaBytes) / (1024 * 1024 * 1024),
		StorageInfo: *info,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// PUT /api/super-admin/channels/{slug}/storage
func setSuperAdminChannelStorage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")
	// Like the other super-admin writers: a quota written for a slug that
	// does not exist is an orphan key behind a success response.
	if !requireExistingChannel(ctx, w, slug) {
		return
	}

	var req struct {
		QuotaGB float64 `json:"quotaGb"` // 0 = use global
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	bytes, ok := gbToBytes(req.QuotaGB)
	if !ok {
		http.Error(w, "quota must be between 0 and a reasonable maximum (0 = use global)", http.StatusBadRequest)
		return
	}
	if err := dbSetChannelStorageQuota(ctx, slug, bytes); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}
