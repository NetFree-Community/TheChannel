package main

import (
	"context"
	"errors"
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

// The flags are pure super-admin kill switches: the backfill enables Webhook,
// Notifications and Ads for every channel, configured or not, so a tenant that
// first adopts one of those features after the deploy is not silently dead.
func TestMigrationEnablesFeaturesUnconditionally(t *testing.T) {
	ctx := testCtx(t)

	seedChannel(t, ctx, "mig-test-webhook", Settings{
		{Key: "webhook_url", Value: "https://example.com/hook"},
	})
	// Uses nothing today: must STILL come out fully enabled, so adopting a
	// feature later works.
	seedChannel(t, ctx, "mig-test-bare", Settings{})
	// Empty/false settings are just as irrelevant as absent ones.
	seedChannel(t, ctx, "mig-test-empty", Settings{
		{Key: "webhook_url", Value: ""},
		{Key: "on_notification", Value: "0"},
	})
	seedChannel(t, ctx, "mig-test-auth", Settings{
		{Key: "require_auth", Value: "1"},
		{Key: "require_auth_for_view_files", Value: "1"},
		{Key: "count_views", Value: "1"},
	})

	if err := backfillChannelFeatures(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	for _, slug := range []string{"mig-test-webhook", "mig-test-bare", "mig-test-empty"} {
		f := featuresOf(t, ctx, slug)
		if !f.Webhook || !f.Notifications || !f.Ads {
			t.Errorf("%s: expected Webhook/Notifications/Ads all enabled, got %+v", slug, f)
		}
		if !f.Reactions || !f.FileUploads || !f.Reports || !f.ScheduledMessages {
			t.Errorf("%s: pre-existing true flags must be preserved, got %+v", slug, f)
		}
	}

	// require_auth / require_auth_for_view_files / count_views are no longer in
	// the settings form, so a leftover value must not silently change behaviour.
	if f := featuresOf(t, ctx, "mig-test-auth"); f.RequireAuth || f.RequireAuthFiles || f.CountViews {
		t.Errorf("auth/view settings must not be applied by default, got %+v", f)
	}
}

// The v2 backfill converges environments where v1 already ran with its old
// conditional logic: it too enables everything unconditionally.
func TestMigrationV2EnablesFeaturesUnconditionally(t *testing.T) {
	ctx := testCtx(t)

	seedChannel(t, ctx, "mig-test-v2-bare", Settings{})

	if err := backfillChannelFeaturesV2(ctx); err != nil {
		t.Fatalf("backfill v2: %v", err)
	}

	f := featuresOf(t, ctx, "mig-test-v2-bare")
	if !f.Webhook || !f.Notifications || !f.Ads ||
		!f.Reactions || !f.FileUploads || !f.Reports || !f.ScheduledMessages {
		t.Errorf("v2 must enable all seven default-on toggles, got %+v", f)
	}
}

// The auth/view toggles are opt-in: an operator who knows the leftover settings
// still reflect their owners' wishes turns them on with MIGRATION_APPLY_AUTH_FLAGS.
func TestMigrationAppliesAuthFlagsWhenOptedIn(t *testing.T) {
	ctx := testCtx(t)

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

// The migration must not undo a deliberate super-admin decision on a re-run,
// and runMigrations must not execute an already-applied migration at all.
// Both the v1 and v2 feature backfills are marked applied here, so the
// withdrawn flag proves neither of them ran again through the full registry.
func TestMigrationIsOneShotAndOnlyEverEnables(t *testing.T) {
	ctx := testCtx(t)

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

	// runMigrations must be a no-op for both feature backfills now: their IDs
	// are in the applied set. (The file-slug migrations may run; they do not
	// touch features and their marks are removed again below.)
	appliedIDs := []string{
		channelFeaturesBackfillID,
		channelFeaturesBackfillV2ID,
	}
	for _, id := range appliedIDs {
		rdb.SAdd(ctx, migrationsAppliedKey, id)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rdb.SRem(cctx, migrationsAppliedKey,
			channelFeaturesBackfillID,
			channelFeaturesBackfillV2ID,
			fileChannelSlugBackfillID,
			fileSlugFromMessagesID,
		)
	})

	runMigrations(ctx)

	if f := featuresOf(t, ctx, "mig-test-oneshot"); f.Webhook {
		t.Error("an applied migration must not run again and resurrect a withdrawn flag")
	}
}

// With R2 off there is nowhere to copy to, and marking the migration applied
// would consume the one-shot before it could ever run. It must report the skip
// sentinel (which runMigrations leaves unmarked) and touch nothing.
func TestMigrationLocalFilesToR2SkippedWhenR2Off(t *testing.T) {
	ctx := testCtx(t)

	if r2Enabled {
		t.Fatal("test environment unexpectedly has R2 enabled")
	}

	seedChannel(t, ctx, "mig-test-r2-off", Settings{})

	err := migrateLocalFilesToR2(ctx)
	if !errors.Is(err, errMigrationSkipped) {
		t.Fatalf("expected errMigrationSkipped with R2 off, got %v", err)
	}

	// runMigrations must not mark it applied either, or it would never run once
	// R2 is configured.
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rdb.SRem(cctx, migrationsAppliedKey,
			channelFeaturesBackfillID,
			channelFeaturesBackfillV2ID,
			fileChannelSlugBackfillID,
			fileSlugFromMessagesID,
			localFilesToR2ID,
		)
	})
	runMigrations(ctx)

	applied, err := rdb.SIsMember(ctx, migrationsAppliedKey, localFilesToR2ID).Result()
	if err != nil {
		t.Fatalf("read applied set: %v", err)
	}
	if applied {
		t.Error("local-files-to-R2 must not be marked applied while R2 is off")
	}
}

// Index members and metadata hashes are both sliced [:2]/[2:4]; legacy/YAML-era
// records can carry a short or empty hash, and those must be skipped rather than
// panicking the boot. No blob exists locally for any of them, so the pass never
// reaches an R2 call even with the flag forced on.
func TestMigrationLocalFilesToR2ShortHashDoesNotPanic(t *testing.T) {
	ctx := testCtx(t)

	slug := "mig-test-r2-shorthash"
	seedChannel(t, ctx, slug, Settings{})

	records := []struct {
		id   string
		hash string
	}{
		{"ab", "0011223344556677"},                 // short index member
		{"aabbccddeeff00112233", ""},               // no hash at all
		{"aabbccddeeff00112234", "a"},              // 1-char hash
		{"aabbccddeeff00112235", "abc"},            // 3-char hash
		{"aabbccddeeff00112236", "00112233445566"}, // well-formed, no local blob
	}
	for _, rec := range records {
		meta := &FileMetadata{ID: rec.id, Filename: "x.bin", Hash: rec.hash, ChannelSlug: slug, Size: 1}
		if err := dbSaveFileMetadata(ctx, meta); err != nil {
			t.Fatalf("save metadata %s: %v", rec.id, err)
		}
		if err := dbAddChannelFile(ctx, slug, rec.id, time.Now().Unix(), 1); err != nil {
			t.Fatalf("index %s: %v", rec.id, err)
		}
		t.Cleanup(func() {
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rdb.Del(cctx, "file:"+rec.id)
		})
	}

	// Force the flag on to get past the R2-off guard. Nothing here has a local
	// blob, so no R2 client call is made.
	prev := r2Enabled
	r2Enabled = true
	t.Cleanup(func() { r2Enabled = prev })

	if err := migrateLocalFilesToR2(ctx); err != nil {
		t.Fatalf("migration must survive malformed hashes, got %v", err)
	}
}
