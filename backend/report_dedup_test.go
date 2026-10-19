package main

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// One report per (user, message): reporting was unlimited and each report is a
// permanent write, so a loop grew Redis without bound. The dedup set caps it.
func TestReportDedupSetIsIdempotentPerUser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "report-dedup"
	const msgID = 42
	reportersKey := "channel:" + slug + ":message:42:reporters"
	rdb.Del(ctx, reportersKey)
	t.Cleanup(func() { rdb.Del(context.Background(), reportersKey) })

	// First report from user A adds them; a repeat is a no-op.
	first, _ := rdb.SAdd(ctx, reportersKey, "userA").Result()
	if first != 1 {
		t.Fatalf("first report by A: SAdd returned %d, want 1", first)
	}
	repeat, _ := rdb.SAdd(ctx, reportersKey, "userA").Result()
	if repeat != 0 {
		t.Errorf("repeat report by A: SAdd returned %d, want 0 (dedup)", repeat)
	}
	// A different user is counted separately.
	other, _ := rdb.SAdd(ctx, reportersKey, "userB").Result()
	if other != 1 {
		t.Errorf("report by B: SAdd returned %d, want 1", other)
	}
	if n, _ := rdb.SCard(ctx, reportersKey).Result(); n != 2 {
		t.Errorf("reporters set holds %d, want 2 (A and B, no duplicates)", n)
	}
	_ = msgID
}

// The per-user report limiter admits its burst and then refuses.
func TestReportLimiterThrottles(t *testing.T) {
	l := reportLimiter("report-limit-test@example.com")
	allowed := 0
	for i := 0; i < 20; i++ {
		if l.Allow() {
			allowed++
		}
	}
	// Burst of 5, refill every 6s, so a tight loop admits ~5 then stops.
	if allowed < 1 || allowed > 6 {
		t.Errorf("admitted %d of 20 rapid reports, want roughly the burst of 5", allowed)
	}
}

// The dedup set must be removed when its channel is deleted, or it leaks.
func TestReportersSetRemovedOnChannelDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "report-cleanup"
	ch := &ChannelData{Slug: slug, Name: "N", CreatedAt: time.Now()}
	if err := dbCreateChannel(ctx, ch); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// A message and a reporters set under it.
	msgKey := "channel:" + slug + ":messages:7"
	rdb.HSet(ctx, msgKey, "id", "7", "text", "x")
	rdb.ZAdd(ctx, "channel:"+slug+":m_times", redis.Z{Score: 1, Member: msgKey})
	reportersKey := "channel:" + slug + ":message:7:reporters"
	rdb.SAdd(ctx, reportersKey, "userA")

	if err := dbDeleteChannel(ctx, slug); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	if n, _ := rdb.Exists(ctx, reportersKey).Result(); n != 0 {
		t.Errorf("reporters set survived channel deletion (leak): %s", reportersKey)
	}
}
