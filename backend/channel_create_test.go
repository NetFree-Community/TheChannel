package main

import (
	"context"
	"testing"
	"time"
)

// These tests exercise the self-service creation checks against real Redis,
// same as migrations_test.go, and clean up after themselves.

func TestSlugAvailabilityRejectsReservedAndInvalid(t *testing.T) {
	ctx := testCtx(t)

	// Every top-level frontend route must be refused, or the channel would be
	// shadowed by the SPA page at the same path.
	for _, slug := range []string{"login", "channel", "super-admin", "admin", "api", "assets"} {
		reason, err := slugAvailability(ctx, slug)
		if err != nil {
			t.Fatalf("slugAvailability(%q): %v", slug, err)
		}
		if reason != "reserved" {
			t.Errorf("slugAvailability(%q) = %q, want \"reserved\"", slug, reason)
		}
	}

	for _, slug := range []string{"", "ab", "Upper", "-lead", "trail-", "has_underscore", "has space"} {
		reason, err := slugAvailability(ctx, slug)
		if err != nil {
			t.Fatalf("slugAvailability(%q): %v", slug, err)
		}
		if reason != "invalid" {
			t.Errorf("slugAvailability(%q) = %q, want \"invalid\"", slug, reason)
		}
	}
}

func TestSlugAvailabilityTakenAndFree(t *testing.T) {
	ctx := testCtx(t)

	if reason, err := slugAvailability(ctx, "selfsvc-free-slug"); err != nil || reason != "" {
		t.Fatalf("free slug: reason=%q err=%v", reason, err)
	}

	seedChannel(t, ctx, "selfsvc-taken-slug", Settings{})

	reason, err := slugAvailability(ctx, "selfsvc-taken-slug")
	if err != nil {
		t.Fatalf("slugAvailability: %v", err)
	}
	if reason != "taken" {
		t.Errorf("existing channel: reason = %q, want \"taken\"", reason)
	}
}

// The per-account cap is what stops one logged-in user minting channels
// without bound, so it must count exactly the owner grants of that email.
func TestCountOwnedChannelsFeedsTheCap(t *testing.T) {
	ctx := testCtx(t)

	const email = "selfsvc-cap@example.com"
	const other = "selfsvc-other@example.com"

	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dbUpdateUsersList(cctx, func(users []User) []User {
			kept := make([]User, 0, len(users))
			for _, u := range users {
				if u.Email != email && u.Email != other {
					kept = append(kept, u)
				}
			}
			return kept
		})
	})

	if n, err := countOwnedChannels(ctx, email); err != nil || n != 0 {
		t.Fatalf("unknown email: n=%d err=%v", n, err)
	}

	// Moderator/writer grants are not ownership and must not count towards it.
	if err := dbAssignChannelRole(ctx, email, "selfsvc-mod", RoleModerator); err != nil {
		t.Fatalf("assign moderator: %v", err)
	}
	if err := dbAssignChannelRole(ctx, email, "selfsvc-write", RoleWriter); err != nil {
		t.Fatalf("assign writer: %v", err)
	}
	if n, err := countOwnedChannels(ctx, email); err != nil || n != 0 {
		t.Fatalf("non-owner roles counted: n=%d err=%v", n, err)
	}

	// Another account's channels are irrelevant to this account's cap.
	if err := dbAssignChannelRole(ctx, other, "selfsvc-other-1", RoleOwner); err != nil {
		t.Fatalf("assign other owner: %v", err)
	}

	for i := 0; i < maxChannelsPerOwner; i++ {
		slug := "selfsvc-owned-" + string(rune('a'+i))
		if err := dbAssignChannelRole(ctx, email, slug, RoleOwner); err != nil {
			t.Fatalf("assign owner %s: %v", slug, err)
		}
		n, err := countOwnedChannels(ctx, email)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		want := i + 1
		if n != want {
			t.Fatalf("after %d owner grants: n=%d, want %d", want, n, want)
		}
		// The handler refuses at >= maxChannelsPerOwner.
		if capped := n >= maxChannelsPerOwner; capped != (want == maxChannelsPerOwner) {
			t.Errorf("cap decision at %d owned channels = %v", n, capped)
		}
	}
}

// A newly created channel must not come out disabled, and the flag must be a
// pure zero value for every features blob written before it existed.
func TestDisabledDefaultsOff(t *testing.T) {
	ctx := testCtx(t)

	if defaultChannelFeatures().Disabled {
		t.Error("defaultChannelFeatures must leave Disabled false")
	}

	seedChannel(t, ctx, "selfsvc-disabled-default", Settings{})

	// Overwrite with a blob shaped exactly like the ones stored before the field
	// existed: dbGetChannel must unmarshal the missing key to false.
	if err := rdb.Set(ctx, "channel:selfsvc-disabled-default:features",
		`{"reactions":true,"fileUploads":true,"reports":true,"scheduledMessages":true}`, 0).Err(); err != nil {
		t.Fatalf("write legacy features blob: %v", err)
	}
	if f := featuresOf(t, ctx, "selfsvc-disabled-default"); f.Disabled {
		t.Error("a features blob without the field must unmarshal Disabled to false")
	}
}
