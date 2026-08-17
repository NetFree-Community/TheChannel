package main

import (
	"context"
	"encoding/json"
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
		pct = float64(used) / float64(quota) * 100
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

	bytes := int64(req.DefaultQuotaGB * 1024 * 1024 * 1024)
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

	var req struct {
		QuotaGB float64 `json:"quotaGb"` // 0 = use global
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	bytes := int64(req.QuotaGB * 1024 * 1024 * 1024)
	if err := dbSetChannelStorageQuota(ctx, slug, bytes); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}
