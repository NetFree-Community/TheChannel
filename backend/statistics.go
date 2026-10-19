package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The live SSE connection count is shared through Redis so that the statistics
// screen is correct on multi-instance deployments (the whole point of the
// Redis-Streams SSE layer). Each instance publishes its own local count as a
// timestamped field of one hash per channel; readers sum the fields that are
// still fresh, so a crashed instance's contribution ages out instead of
// inflating the total forever.

// sseInstanceID identifies this process's field in the per-channel hash.
var sseInstanceID = generatedRandomID(8)

// channelSSEConnections holds this instance's local per-channel counters.
var channelSSEConnections sync.Map // slug -> *atomic.Int64

// sseConnFreshness is how long a published per-instance count stays credible
// without a refresh; the refresher below re-publishes well within it.
const sseConnFreshness = 90 * time.Second

func sseConnectionsKey(slug string) string {
	return fmt.Sprintf("channel:%s:sse_connections", slug)
}

// channelSSECountersMu orders counter creation against the refresher's
// removal of idle counters. Without it the refresher could read a counter as
// 0, a connect could then LoadOrStore that same counter, add 1 and publish
// it, and the refresher would still withdraw the field and drop the counter:
// a live connection counted nowhere, and its later disconnect taken off a
// fresh counter that another connection had just created.
var channelSSECountersMu sync.Mutex

// adjustChannelSSE applies delta to the channel's local counter under the
// creation lock and returns the new value, clamped at zero. The add itself
// must happen under the lock: a counter created and then incremented outside
// it could be forgotten by the refresher in between, leaving a live
// connection counted on a counter no longer in the map.
func adjustChannelSSE(slug string, delta int64) int64 {
	channelSSECountersMu.Lock()
	defer channelSSECountersMu.Unlock()
	v, _ := channelSSEConnections.LoadOrStore(slug, &atomic.Int64{})
	counter := v.(*atomic.Int64)
	n := counter.Add(delta)
	if n < 0 {
		// A decrement that outruns its increment must not persist a negative
		// datapoint into the statistics series.
		counter.Store(0)
		n = 0
	}
	return n
}

// forgetIdleChannelCounter drops the local counter for slug if it is still 0
// once the refresher holds the creation lock; it reports whether it did.
func forgetIdleChannelCounter(slug string, counter *atomic.Int64) bool {
	channelSSECountersMu.Lock()
	defer channelSSECountersMu.Unlock()
	if counter.Load() > 0 {
		return false
	}
	channelSSEConnections.Delete(slug)
	return true
}

// publishLocalSSECount writes this instance's current count for slug.
func publishLocalSSECount(ctx context.Context, slug string, count int64) {
	rdb.HSet(ctx, sseConnectionsKey(slug), sseInstanceID,
		fmt.Sprintf("%d:%d", count, time.Now().Unix()))
}

// dbGetSSEConnectionCount sums every instance's fresh published count for slug,
// pruning fields that have gone stale (crashed or scaled-down instances).
func dbGetSSEConnectionCount(ctx context.Context, slug string) int64 {
	fields, err := rdb.HGetAll(ctx, sseConnectionsKey(slug)).Result()
	if err != nil {
		// Best effort: fall back to this instance's own view without
		// creating a counter for a channel nobody is connected to.
		if v, ok := channelSSEConnections.Load(slug); ok {
			return v.(*atomic.Int64).Load()
		}
		return 0
	}
	var total int64
	cutoff := time.Now().Add(-sseConnFreshness).Unix()
	for field, v := range fields {
		i := strings.LastIndex(v, ":")
		if i <= 0 {
			rdb.HDel(ctx, sseConnectionsKey(slug), field)
			continue
		}
		count, cerr := strconv.ParseInt(v[:i], 10, 64)
		ts, terr := strconv.ParseInt(v[i+1:], 10, 64)
		if cerr != nil || terr != nil || ts < cutoff {
			rdb.HDel(ctx, sseConnectionsKey(slug), field)
			continue
		}
		total += count
	}
	return total
}

// The refresher keeps long-lived quiet connections visible: without it an
// instance whose count has not changed for sseConnFreshness would age out of
// every reader's sum despite still serving clients.
func init() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			channelSSEConnections.Range(func(key, value any) bool {
				slug := key.(string)
				counter := value.(*atomic.Int64)
				if counter.Load() > 0 {
					publishLocalSSECount(ctx, slug, counter.Load())
					return true
				}
				// Nothing connected here: withdraw our field, then forget the
				// slug — in that order, and only if the counter is still idle
				// under the creation lock. A connect that lands between the
				// two publishes its own count after the withdrawal and keeps
				// the counter, so it is never lost.
				rdb.HDel(ctx, sseConnectionsKey(slug), sseInstanceID)
				if !forgetIdleChannelCounter(slug, counter) {
					// A connect got in before the withdrawal and its publish
					// may just have been wiped: publish the live count again.
					publishLocalSSECount(ctx, slug, counter.Load())
				}
				return true
			})
			cancel()
		}
	}()
}

type PeakSSEConnections struct {
	Value     int64     `json:"value" redis:"value"`
	Timestamp time.Time `json:"timestamp" redis:"timestamp"`
}
type Statistics struct {
	Data   []int64  `json:"date"`
	Labels []string `json:"labels"`
}

func increaseCounterSSE(slug string) {
	local := adjustChannelSSE(slug, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	publishLocalSSECount(ctx, slug, local)

	total := dbGetSSEConnectionCount(ctx, slug)
	if total < local {
		total = local
	}

	// dbSavePeakSSEConnections is a Lua max-write, so publishing the current
	// total unconditionally can never regress a larger recorded peak.
	go dbSavePeakSSEConnections(slug, &PeakSSEConnections{Value: total, Timestamp: time.Now()})
	go dbSaveSSEStatistics(slug, total)
}

func decreaseCounterSSE(slug string) {
	local := adjustChannelSSE(slug, -1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A viewer that disconnects after the channel was deleted (a tab that
	// missed the channel-deleted event) would recreate the per-channel stats
	// keys dbDeleteChannel just removed. Skipped only on a definite "gone":
	// a Redis error keeps the old behaviour rather than losing a sample.
	if exists, err := dbChannelExists(ctx, slug); err == nil && !exists {
		return
	}
	publishLocalSSECount(ctx, slug, local)

	go dbSaveSSEStatistics(slug, dbGetSSEConnectionCount(ctx, slug))
}

func getStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	amount, err := dbGetUsersAmount(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	s, err := dbGetSSEStatistics(ctx, slug, 1000)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// Redis is authoritative for both the live count and the peak, so every
	// replica answers the same numbers and resetStatistics sticks everywhere.
	peak, err := dbGetPeakSSEConnections(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	response := struct {
		UsersAmount           int64               `json:"usersAmount"`
		ConnectedUsersAmount  int64               `json:"connectedUsersAmount"`
		PeakSSEConnections    *PeakSSEConnections `json:"peakSSEConnections"`
		ConnectionsStatistics Statistics          `json:"connectionsStatistics"`
	}{
		UsersAmount:           amount,
		ConnectedUsersAmount:  dbGetSSEConnectionCount(ctx, slug),
		PeakSSEConnections:    peak,
		ConnectionsStatistics: *s,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// resetStatistics clears everything the statistics endpoint serves: the
// recorded peak AND the monthly connection series. The peak lives only in
// Redis (max-written, read back on demand), so deleting the key is a complete
// reset on every replica.
func resetStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	channels, err := dbListChannels(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// Pipelined, and flushed per channel. Each channel means one peak key plus
	// 11 years x 12 months of series keys: issued one at a time that is ~133
	// sequential round trips per channel, so a few thousand channels ran well
	// past this handler's own deadline and left statistics half-reset — with
	// every Del's error discarded, silently. dbDeleteChannel already batches
	// the same key set this way.
	for _, ch := range channels {
		if err := ctx.Err(); err != nil {
			// Out of time: say so rather than reporting a reset that did not finish.
			log.Printf("resetStatistics: aborted after partial reset: %v\n", err)
			http.Error(w, "reset did not complete", http.StatusGatewayTimeout)
			return
		}
		pipe := rdb.Pipeline()
		pipe.Del(ctx, fmt.Sprintf("channel:%s:peak_sse_connections", ch.Slug))
		// The monthly series keys have no index; the shared helper enumerates
		// the same month/year space channel deletion uses.
		for _, key := range sseStatisticsKeysFor(ch.Slug) {
			pipe.Del(ctx, key)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("resetStatistics: %s: clearing statistics failed: %v\n", ch.Slug, err)
		}
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
