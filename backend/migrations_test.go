package main

import (
	"context"
	"testing"
	"time"
)

// These tests exercise the real Redis-backed migration path. They need a live
// Redis, which the package init() already requires, and they clean up after
// themselves. Run with:
//
//	REDIS_ADDR=127.0.0.1:6399 go test ./... -run Migration
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// seedChannel creates a channel with the feature flags a pre-migration channel
// would have had, plus the given settings.
func seedChannel(t *testing.T, ctx context.Context, slug string, settings Settings) {
	t.Helper()

	ch := &ChannelData{
		Slug:      slug,
		Name:      slug,
		CreatedAt: time.Now(),
		Features: ChannelFeatures{
			// Exactly what the old creation path produced: the three toggles the
			// migration is responsible for are off.
			Reactions:         true,
			FileUploads:       true,
			Reports:           true,
			ScheduledMessages: true,
		},
	}
	if err := dbCreateChannel(ctx, ch); err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
	if err := dbSetSettings(ctx, slug, &settings); err != nil {
		t.Fatalf("seed settings %s: %v", slug, err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dbDeleteChannel(cctx, slug)
	})
}

func featuresOf(t *testing.T, ctx context.Context, slug string) ChannelFeatures {
	t.Helper()
	ch, err := dbGetChannel(ctx, slug)
	if err != nil {
		t.Fatalf("get channel %s: %v", slug, err)
	}
	return ch.Features
}

func TestMigrationBackfillsFeaturesFromSettings(t *testing.T) {
	ctx := testCtx(t)

	// Ads must be off globally, otherwise every channel would qualify via the
	// lock branch and the per-channel assertions would be meaningless.
	if err := dbSetGlobalAdsConfig(ctx, &GlobalAdsConfig{}); err != nil {
		t.Fatalf("reset global ads: %v", err)
	}

	seedChannel(t, ctx, "mig-test-webhook", Settings{
		{Key: "webhook_url", Value: "https://example.com/hook"},
	})
	seedChannel(t, ctx, "mig-test-push", Settings{
		{Key: "on_notification", Value: "1"},
	})
	seedChannel(t, ctx, "mig-test-ads", Settings{
		{Key: "ad-iframe-src", Value: "https://ads.example.com/b.html"},
	})
	seedChannel(t, ctx, "mig-test-auth", Settings{
		{Key: "require_auth", Value: "1"},
		{Key: "require_auth_for_view_files", Value: "1"},
		{Key: "count_views", Value: "1"},
	})
	// Uses nothing: must come out untouched, so enforcement stays off for it.
	seedChannel(t, ctx, "mig-test-bare", Settings{})
	// An empty webhook_url is configuration debris, not usage.
	seedChannel(t, ctx, "mig-test-empty", Settings{
		{Key: "webhook_url", Value: ""},
		{Key: "on_notification", Value: "0"},
	})

	if err := backfillChannelFeatures(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if f := featuresOf(t, ctx, "mig-test-webhook"); !f.Webhook {
		t.Error("channel with webhook_url should have Webhook enabled")
	} else if f.Ads || f.Notifications {
		t.Error("webhook channel should not have gained Ads or Notifications")
	}

	if f := featuresOf(t, ctx, "mig-test-push"); !f.Notifications {
		t.Error("channel with on_notification=1 should have Notifications enabled")
	}

	if f := featuresOf(t, ctx, "mig-test-ads"); !f.Ads {
		t.Error("channel with ad-iframe-src should have Ads enabled")
	}

	// require_auth / require_auth_for_view_files / count_views are no longer in
	// the settings form, so a leftover value must not silently change behaviour.
	if f := featuresOf(t, ctx, "mig-test-auth"); f.RequireAuth || f.RequireAuthFiles || f.CountViews {
		t.Errorf("auth/view settings must not be applied by default, got %+v", f)
	}

	if f := featuresOf(t, ctx, "mig-test-bare"); f.Ads || f.Notifications || f.Webhook {
		t.Errorf("unused channel should not have gained anything, got %+v", f)
	}
	// The flags the old creation path did set must survive untouched.
	if f := featuresOf(t, ctx, "mig-test-bare"); !f.Reactions || !f.FileUploads || !f.Reports || !f.ScheduledMessages {
		t.Errorf("pre-existing true flags must be preserved, got %+v", f)
	}

	if f := featuresOf(t, ctx, "mig-test-empty"); f.Webhook || f.Notifications {
		t.Errorf("empty/false settings must not enable anything, got %+v", f)
	}
}

// The auth/view toggles are opt-in: an operator who knows the leftover settings
// still reflect their owners' wishes turns them on with MIGRATION_APPLY_AUTH_FLAGS.
func TestMigrationAppliesAuthFlagsWhenOptedIn(t *testing.T) {
	ctx := testCtx(t)

	if err := dbSetGlobalAdsConfig(ctx, &GlobalAdsConfig{}); err != nil {
		t.Fatalf("reset global ads: %v", err)
	}
	seedChannel(t, ctx, "mig-test-auth-optin", Settings{
		{Key: "require_auth", Value: "1"},
		{Key: "require_auth_for_view_files", Value: "1"},
		{Key: "count_views", Value: "1"},
	})

	t.Setenv("MIGRATION_APPLY_AUTH_FLAGS", "1")

	if err := backfillChannelFeatures(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if f := featuresOf(t, ctx, "mig-test-auth-optin"); !f.RequireAuth || !f.RequireAuthFiles || !f.CountViews {
		t.Errorf("auth/view settings should have been honoured, got %+v", f)
	}
}

// Reactions, uploads, reports and scheduling were universally available before
// requireFeature started gating them, so the backfill must guarantee them.
func TestMigrationDefaultsEverydayFeaturesOn(t *testing.T) {
	ctx := testCtx(t)

	if err := dbSetGlobalAdsConfig(ctx, &GlobalAdsConfig{}); err != nil {
		t.Fatalf("reset global ads: %v", err)
	}

	// A channel whose features blob predates dbCreateChannel setting the four.
	seedChannel(t, ctx, "mig-test-nofeatures", Settings{})
	if err := dbSetChannelFeatures(ctx, "mig-test-nofeatures", &ChannelFeatures{}); err != nil {
		t.Fatalf("clear features: %v", err)
	}

	if err := backfillChannelFeatures(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	f := featuresOf(t, ctx, "mig-test-nofeatures")
	if !f.Reactions || !f.FileUploads || !f.Reports || !f.ScheduledMessages {
		t.Errorf("everyday features should have been defaulted on, got %+v", f)
	}
}

// A channel with no ad of its own still shows ads when the super admin has
// locked it to a configured global ad, so the migration must enable Ads for it.
func TestMigrationEnablesAdsForGloballyLockedChannel(t *testing.T) {
	ctx := testCtx(t)

	seedChannel(t, ctx, "mig-test-locked", Settings{})

	if err := dbSetGlobalAdsConfig(ctx, &GlobalAdsConfig{
		Src:     "https://ads.example.com/global.html",
		Width:   300,
		LockAll: true,
	}); err != nil {
		t.Fatalf("set global ads: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dbSetGlobalAdsConfig(cctx, &GlobalAdsConfig{})
	})

	if err := backfillChannelFeatures(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if f := featuresOf(t, ctx, "mig-test-locked"); !f.Ads {
		t.Error("channel locked to a configured global ad should have Ads enabled")
	}
}

// The migration must not undo a deliberate super-admin decision on a re-run,
// and runMigrations must not execute an already-applied migration at all.
func TestMigrationIsOneShotAndOnlyEverEnables(t *testing.T) {
	ctx := testCtx(t)

	if err := dbSetGlobalAdsConfig(ctx, &GlobalAdsConfig{}); err != nil {
		t.Fatalf("reset global ads: %v", err)
	}
	seedChannel(t, ctx, "mig-test-oneshot", Settings{
		{Key: "webhook_url", Value: "https://example.com/hook"},
	})

	if err := backfillChannelFeatures(ctx); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if f := featuresOf(t, ctx, "mig-test-oneshot"); !f.Webhook {
		t.Fatal("expected Webhook enabled after first run")
	}

	// The super admin then deliberately withdraws webhooks from this tenant.
	f := featuresOf(t, ctx, "mig-test-oneshot")
	f.Webhook = false
	if err := dbSetChannelFeatures(ctx, "mig-test-oneshot", &f); err != nil {
		t.Fatalf("set features: %v", err)
	}

	// runMigrations must be a no-op now: the ID is already in the applied set.
	rdb.SAdd(ctx, migrationsAppliedKey, channelFeaturesBackfillID)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rdb.SRem(cctx, migrationsAppliedKey, channelFeaturesBackfillID)
	})

	runMigrations(ctx)

	if f := featuresOf(t, ctx, "mig-test-oneshot"); f.Webhook {
		t.Error("an applied migration must not run again and resurrect a withdrawn flag")
	}
}
