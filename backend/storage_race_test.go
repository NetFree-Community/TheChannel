package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStorageQuotaConcurrentUploadsRespectQuota pins the upload path's quota
// accounting under concurrency.
//
// This used to be a check-then-act: enforceStorageQuota read used_bytes,
// compared, and the counter only moved after the blob was written — a window
// spanning a full R2 PutObject (60s budget). Every request starting inside that
// window read the same "used" and passed a check only one of them should have.
// reserveStorageQuota now claims the bytes with an atomic INCRBY and judges its
// own post-increment total, so concurrent callers get distinct values.
//
// The invariant: once the dust settles, a channel with auto-cleanup off must
// never be storing more than its quota.
func TestStorageQuotaConcurrentUploadsRespectQuota(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const slug = "quota-race-test"
	const quota = 10_000
	const fileSize = 1_000
	const workers = 40 // 40 x 1000 = 40,000 bytes offered against a 10,000 quota

	if err := dbSetChannelStorageQuota(ctx, slug, quota); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	rdb.Del(ctx, "channel:"+slug+":storage:used_bytes")
	// Auto-cleanup off: exceeding the quota must be refused outright rather
	// than making room, which is what makes the invariant checkable.
	rdb.Del(ctx, "channel:"+slug+":storage:auto_cleanup")
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		rdb.Del(cctx,
			"channel:"+slug+":storage:used_bytes",
			"channel:"+slug+":storage:quota_bytes",
			"channel:"+slug+":storage:auto_cleanup",
		)
	})

	var admitted int64
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)

	for i := 0; i < workers; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			// Line every worker up so they hit the check in the same window,
			// which is exactly the production shape: a burst of uploads.
			start.Wait()
			if err := reserveStorageQuota(ctx, slug, fileSize); err != nil {
				return // refused, as it should be once the quota is reached
			}
			atomic.AddInt64(&admitted, 1)
			// Stands in for the blob write the real upload does while holding
			// its reservation — in production an R2 PutObject with a 60-second
			// budget. The reservation must already be counted for the whole of
			// it, which is exactly what the old code got wrong.
			time.Sleep(50 * time.Millisecond)
		}()
	}

	start.Done()
	done.Wait()

	used, err := dbGetChannelStorageUsed(ctx, slug)
	if err != nil {
		t.Fatalf("read used: %v", err)
	}

	t.Logf("quota=%d admitted=%d stored=%d bytes (%.1fx quota)",
		quota, atomic.LoadInt64(&admitted), used, float64(used)/float64(quota))

	if used > quota {
		t.Errorf("stored %d bytes against a %d byte quota: %d concurrent uploads were "+
			"admitted when only %d fit — the reservation is not atomic",
			used, quota, atomic.LoadInt64(&admitted), quota/fileSize)
	}
	if want := int64(quota / fileSize); atomic.LoadInt64(&admitted) != want {
		t.Errorf("admitted %d uploads, want exactly %d: the quota should be filled "+
			"completely and not one file further", atomic.LoadInt64(&admitted), want)
	}
}
