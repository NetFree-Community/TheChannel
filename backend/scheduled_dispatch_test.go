package main

import (
	"context"
	"testing"
	"time"
)

// A tab that missed the dispatch event still holds the entry the scheduler
// already posted and hands it back on its next save; the next tick then
// posted, pushed and webhooked it a second time. Dispatched entries are
// remembered by (scheduled time, text) and dropped from a submitted list.
func TestDispatchedScheduledEntriesAreDroppedFromSave(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "sched-dispatched"
	key := scheduledDispatchedKey(slug)
	rdb.Del(ctx, key)
	t.Cleanup(func() { rdb.Del(context.Background(), key) })

	at := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	posted := Message{Type: "md", Text: "בוקר טוב", Timestamp: at}
	pending := Message{Type: "md", Text: "ערב טוב", Timestamp: at.Add(12 * time.Hour)}
	// Same text, another time: a different entry, and it stays.
	sameTextLater := Message{Type: "md", Text: "בוקר טוב", Timestamp: at.Add(24 * time.Hour)}

	// Nothing dispatched yet: the list passes through untouched.
	if got := dropDispatchedScheduled(ctx, slug, []Message{posted, pending}); len(got) != 2 {
		t.Fatalf("with nothing dispatched, %d of 2 entries kept", len(got))
	}

	rememberScheduledDispatch(ctx, slug, posted)

	got := dropDispatchedScheduled(ctx, slug, []Message{posted, pending, sameTextLater})
	if len(got) != 2 {
		t.Fatalf("kept %d entries, want 2 (the posted one dropped): %+v", len(got), got)
	}
	if got[0].Text != pending.Text || !got[1].Timestamp.Equal(sameTextLater.Timestamp) {
		t.Errorf("wrong entries kept: %+v", got)
	}

	// The memory expires on its own; every dispatch refreshes it.
	ttl, err := rdb.TTL(ctx, key).Result()
	if err != nil || ttl <= 6*24*time.Hour || ttl > scheduledDispatchedTTL {
		t.Errorf("dispatched set TTL = %v (%v), want about %v", ttl, err, scheduledDispatchedTTL)
	}
}
