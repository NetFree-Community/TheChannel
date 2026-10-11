package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Exercised against real Redis like channel_create_test.go, and cleaned up by
// seedChannel's t.Cleanup.

func TestMyChannelsForListsEveryRoleAndSkipsGhosts(t *testing.T) {
	ctx := testCtx(t)

	seedChannel(t, ctx, "mych-owned", Settings{})
	seedChannel(t, ctx, "mych-mod", Settings{})
	seedChannel(t, ctx, "mych-off", Settings{})

	// The disabled flag is what the page uses to warn the owner; it must come
	// through from the features record, not default to false.
	if err := dbUpdateChannelFeatures(ctx, "mych-off", func(f *ChannelFeatures) { f.Disabled = true }); err != nil {
		t.Fatalf("disable mych-off: %v", err)
	}

	// Two registered viewers on the owned channel.
	const viewers = "channel:mych-owned:registered_emails"
	if err := rdb.SAdd(ctx, viewers, "one@example.com", "two@example.com").Err(); err != nil {
		t.Fatalf("seed viewers: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = rdb.Del(cctx, viewers).Err()
	})

	u := User{
		Email: "mych@example.com",
		ChannelRoles: map[string]ChannelRole{
			"mych-off":   RoleWriter,
			"mych-mod":   RoleModerator,
			"mych-owned": RoleOwner,
			// A grant left behind by a deleted channel: must be skipped, not
			// fail the list or appear as an empty row.
			"mych-ghost": RoleOwner,
		},
	}

	rows, err := myChannelsFor(ctx, u)
	if err != nil {
		t.Fatalf("myChannelsFor: %v", err)
	}

	wantOrder := []string{"mych-owned", "mych-mod", "mych-off"}
	if len(rows) != len(wantOrder) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(wantOrder), rows)
	}
	for i, want := range wantOrder {
		if rows[i].Slug != want {
			t.Errorf("row %d: slug = %q, want %q (owner first, then by role)", i, rows[i].Slug, want)
		}
	}

	if rows[0].Role != RoleOwner || rows[0].Participants != 2 || rows[0].Disabled {
		t.Errorf("owned row = %+v, want owner / 2 participants / enabled", rows[0])
	}
	if rows[0].Name != "mych-owned" {
		t.Errorf("owned row name = %q, want the channel's name, not its slug only", rows[0].Name)
	}
	if rows[2].Role != RoleWriter || !rows[2].Disabled {
		t.Errorf("disabled row = %+v, want writer / disabled", rows[2])
	}
}

// A user without any grant must get `[]`, never `null`: the frontend iterates
// the answer and a null would be a crash on the very first page a new user
// sees after signing in.
func TestMyChannelsForNoRolesEncodesAsEmptyArray(t *testing.T) {
	ctx := testCtx(t)

	rows, err := myChannelsFor(ctx, User{})
	if err != nil {
		t.Fatalf("myChannelsFor: %v", err)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(rows); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := bytes.TrimSpace(buf.Bytes()); string(got) != "[]" {
		t.Errorf("encoded %s, want []", got)
	}
}
