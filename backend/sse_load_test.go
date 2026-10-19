package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestSSEBlockingReadsDoNotStarveMainPool pins the isolation between the SSE
// event loop and the rest of the application.
//
// Every connected viewer sits in a blocking XREAD, which holds its pooled
// connection for the whole block and is re-issued immediately — so a viewer
// occupies one connection for as long as it is connected. While SSE shared the
// main pool, roughly PoolSize concurrent viewers consumed every connection in
// the process and ordinary requests (a login, an upload, a message read) queued
// behind them until they timed out.
//
// This drives more concurrent blocking reads than the main pool has connections
// and asserts that ordinary traffic is still served promptly.
func TestSSEBlockingReadsDoNotStarveMainPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if rdbEvents == nil {
		t.Fatal("rdbEvents is nil: the SSE client must be initialised alongside rdb")
	}

	const streamKey = "sse-load-test:events"
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		rdb.Del(cctx, streamKey)
	})

	// More simultaneous blocking readers than the main pool has connections. If
	// these ran on rdb, the pool would be fully consumed for the duration.
	const readers = 140

	readCtx, stopReaders := context.WithCancel(ctx)
	defer stopReaders()

	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Mirrors getEvents: block on the stream, re-issue immediately.
			for readCtx.Err() == nil {
				rdbEvents.XRead(readCtx, &redis.XReadArgs{
					Streams: []string{streamKey, "$"},
					Count:   50,
					Block:   2 * time.Second,
				}).Result()
			}
		}()
	}

	// Let every reader get into its blocking read before measuring.
	time.Sleep(1500 * time.Millisecond)

	// Ordinary application traffic, on the main client, while all of that is in
	// flight. Each of these is a trivial command that should return immediately.
	const probes = 25
	slowest := time.Duration(0)
	for i := 0; i < probes; i++ {
		start := time.Now()
		if err := rdb.Ping(ctx).Err(); err != nil {
			stopReaders()
			wg.Wait()
			t.Fatalf("ordinary request %d failed while %d SSE readers were blocked: %v", i, readers, err)
		}
		if d := time.Since(start); d > slowest {
			slowest = d
		}
	}

	stopReaders()
	wg.Wait()

	t.Logf("%d concurrent blocking SSE reads; slowest ordinary request %v", readers, slowest)

	// A starved pool shows up as multi-second waits (the blocking reads only
	// release their connections when their block elapses). Served from its own
	// pool, ordinary traffic is unaffected.
	if slowest > time.Second {
		t.Errorf("ordinary request took %v while %d SSE readers were blocked: the "+
			"event loop is starving the main connection pool", slowest, readers)
	}
}
