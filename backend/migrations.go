package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// migrationsAppliedKey holds the set of migration IDs that have already run.
const migrationsAppliedKey = "migrations:applied"

// channelFeaturesBackfillID is the one-shot backfill that derives the
// operator-controlled ChannelFeatures toggles from what each channel was
// actually configured to do before those toggles were enforced.
const channelFeaturesBackfillID = "2026-08-channel-features-backfill"

// fileChannelSlugBackfillID is the one-shot backfill that stamps the owning
// channel onto file records written before FileMetadata had a ChannelSlug.
const fileChannelSlugBackfillID = "2026-08-file-channel-slug-backfill"

// runMigrations applies any data migration that has not run yet.
//
// It is deliberately non-fatal: a migration that fails is simply left unmarked
// and retried on the next boot, rather than taking the server down. Callers run
// it before the HTTP listener starts, so a successful run is always complete
// before the first request is served.
func runMigrations(ctx context.Context) {
	if os.Getenv("SKIP_MIGRATIONS") == "1" {
		log.Println("migrations: skipped (SKIP_MIGRATIONS=1)")
		return
	}

	migrations := []struct {
		id  string
		run func(context.Context) error
	}{
		{channelFeaturesBackfillID, backfillChannelFeatures},
		{fileChannelSlugBackfillID, backfillFileChannelSlugs},
	}

	for _, m := range migrations {
		applied, err := rdb.SIsMember(ctx, migrationsAppliedKey, m.id).Result()
		if err != nil {
			log.Printf("migrations: cannot read applied set, skipping %s this boot: %v", m.id, err)
			continue
		}
		if applied {
			continue
		}

		log.Printf("migrations: running %s", m.id)
		if err := m.run(ctx); err != nil {
			log.Printf("migrations: %s FAILED (will retry on next start): %v", m.id, err)
			continue
		}
		if err := rdb.SAdd(ctx, migrationsAppliedKey, m.id).Err(); err != nil {
			log.Printf("migrations: %s succeeded but could not be marked applied, it will run again: %v", m.id, err)
			continue
		}
		log.Printf("migrations: %s completed", m.id)
	}
}

// settingString returns the value of key, or "" when it is absent.
func settingString(settings Settings, key string) string {
	for i := range settings {
		if settings[i].Key == key {
			return settings[i].GetString()
		}
	}
	return ""
}

// settingBool reports whether key is present and truthy, using the same
// parsing the rest of the app uses (booleans are persisted as "1").
func settingBool(settings Settings, key string) bool {
	for i := range settings {
		if settings[i].Key == key {
			return settings[i].GetBool()
		}
	}
	return false
}

// backfillChannelFeatures turns on the ChannelFeatures toggles for channels that
// were already using the corresponding feature.
//
// Why this is needed: createChannel and approveChannelRequest only ever set
// Reactions, FileUploads, Reports and ScheduledMessages, so Ads, Notifications
// and Webhook are false on every channel that exists today. Those three were
// never enforced, so channels have been happily using them with the flag off.
// Enforcing them without this backfill would switch the features off platform
// wide.
//
// The migration only ever turns a flag ON. It runs exactly once, so a super
// admin who later turns something off is never overridden.
func backfillChannelFeatures(ctx context.Context) error {
	channels, err := dbListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	// A channel under an ads lock serves the GLOBAL ad source and ignores its own
	// settings (see isChannelAdsLocked / getAdsSettings), so "has no ad-iframe-src
	// of its own" does not mean "shows no ads".
	globalAds, err := dbGetGlobalAdsConfig(ctx)
	if err != nil {
		return fmt.Errorf("get global ads config: %w", err)
	}

	// require_auth / require_auth_for_view_files / count_views were removed from
	// the owner settings form, so any surviving value is leftover configuration
	// the owner can neither see nor undo. Honouring it would wall a channel off
	// or start 401ing file requests that serve today, with no way back through
	// the UI — unlike the ads/notifications/webhook part above, which only keeps
	// live behaviour alive. So it is opt-in, for an operator who knows the
	// leftovers are still what their owners want.
	applyAuthFlags := os.Getenv("MIGRATION_APPLY_AUTH_FLAGS") == "1"
	if applyAuthFlags {
		log.Println("migrations: MIGRATION_APPLY_AUTH_FLAGS=1, applying leftover auth/view settings to features")
	}

	var changed int
	for _, ch := range channels {
		settings, err := dbGetSettings(ctx, ch.Slug)
		if err != nil {
			// Do not mark the migration applied if we could not read a channel.
			return fmt.Errorf("get settings for %s: %w", ch.Slug, err)
		}

		before := ch.Features
		f := ch.Features

		// Reactions, uploads, reports and scheduling were available to everyone
		// before requireFeature started gating them, and nothing outside
		// dbCreateChannel ever set them — so a channel whose features blob was
		// written before that would silently lose all four on the first boot
		// after the gates ship.
		f.Reactions = true
		f.FileUploads = true
		f.Reports = true
		f.ScheduledMessages = true

		// Configured to call out to a webhook.
		if settingString(settings, "webhook_url") != "" {
			f.Webhook = true
		}

		// Owner switched push on for this channel.
		if settingBool(settings, "on_notification") {
			f.Notifications = true
		}

		// Either the channel has its own iframe ad, or it is locked to a global
		// one that is actually configured.
		if settingString(settings, "ad-iframe-src") != "" {
			f.Ads = true
		} else if isChannelAdsLocked(globalAds, ch) && globalAds.Src != "" {
			f.Ads = true
		}

		if applyAuthFlags {
			// These three keys were offered in the channel settings UI but the
			// backend only ever read the Features equivalents, so the owner's
			// choice has silently had no effect until now.
			if settingBool(settings, "count_views") {
				f.CountViews = true
			}
			if settingBool(settings, "require_auth") {
				f.RequireAuth = true
			}
			if settingBool(settings, "require_auth_for_view_files") {
				f.RequireAuthFiles = true
			}
		}

		if f == before {
			continue
		}

		if err := dbSetChannelFeatures(ctx, ch.Slug, &f); err != nil {
			return fmt.Errorf("set features for %s: %w", ch.Slug, err)
		}
		changed++
		log.Printf("migrations: %s features updated: %+v -> %+v", ch.Slug, before, f)
	}

	log.Printf("migrations: channel features backfill touched %d of %d channels", changed, len(channels))
	return nil
}

// backfillFileChannelSlugs stamps the owning channel onto file records that
// predate FileMetadata.ChannelSlug.
//
// Why this is needed: fileVisibleToChannel now denies any record whose
// ChannelSlug does not match the channel serving it, and every file uploaded
// before that field existed has an empty one. Without this backfill, historical
// attachments and channel logos 404 on the first boot after deploy, and the only
// way out is ALLOW_LEGACY_UNSCOPED_FILES, which re-opens the cross-tenant read
// the isolation check was written to close.
//
// Ownership is taken from the per-channel file index each record is already
// listed in, so nothing is guessed. Records that already carry a slug are left
// alone. As with the features backfill, a read/write failure returns the error
// and leaves the ID unmarked so the whole thing is retried on the next boot.
func backfillFileChannelSlugs(ctx context.Context) error {
	channels, err := dbListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	var stamped int
	for _, ch := range channels {
		members, err := rdb.ZRange(ctx, fmt.Sprintf("channel:%s:files", ch.Slug), 0, -1).Result()
		if err != nil {
			return fmt.Errorf("list files for %s: %w", ch.Slug, err)
		}

		for _, m := range members {
			// member format: "fileID:size"
			i := strings.LastIndex(m, ":")
			if i <= 0 {
				continue
			}
			fileID := m[:i]

			meta, err := dbGetFileMetadata(ctx, fileID)
			if err != nil {
				// A tracked id whose metadata is gone (neither in Redis nor on
				// disk) is index debris, not a reason to abandon the run.
				log.Printf("migrations: %s: no metadata for tracked file %s, skipping", ch.Slug, fileID)
				continue
			}
			if meta.ChannelSlug != "" {
				continue
			}

			// A YAML-era record reaches this point through the fallback path,
			// which does not populate ChannelSlug either; saving it promotes it
			// into Redis with the owner attached.
			meta.ChannelSlug = ch.Slug
			if err := dbSaveFileMetadata(ctx, meta); err != nil {
				return fmt.Errorf("save metadata for %s: %w", fileID, err)
			}
			stamped++
		}
	}

	log.Printf("migrations: stamped channel ownership onto %d legacy file records across %d channels", stamped, len(channels))
	return nil
}

// migrationContext is the budget for the whole migration step at boot.
func migrationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}
