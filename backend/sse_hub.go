package main

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// One Redis reader per channel stream, shared by every viewer of that channel.
//
// Each viewer used to run its own blocking XREAD, which holds a pooled
// connection for the whole block and is re-issued immediately — so viewer count
// and Redis connection count were the same number, and the pool was the ceiling
// on concurrent viewers. Now the ceiling is per *channel with at least one
// viewer*, and a channel with ten thousand readers costs exactly one connection.
//
// The hub reads from the tip of the stream. A viewer that needs earlier events
// (a reconnect carrying Last-Event-ID) catches up with its own bounded XRANGE
// before joining the live feed; see sseCatchUp.

const (
	// sseSubBuffer is how many events a single slow viewer may fall behind by
	// before it is dropped. Dropping one viewer is correct: the alternative is
	// letting it block the hub and stall delivery for everybody else on the
	// channel. A dropped viewer's browser reconnects with Last-Event-ID and
	// catches up.
	sseSubBuffer = 256

	// sseHubBlock is how long the hub's read blocks before looping. It only
	// needs to be short enough to notice that the hub has been told to stop.
	sseHubBlock = 5 * time.Second

	// sseCatchUpLimit bounds a reconnecting viewer's replay. The stream itself
	// only retains ~1000 entries (publishEvent), so this cannot silently skip
	// events a client could otherwise have had.
	sseCatchUpLimit = 1000
)

// sseChannelGoneEvent and sseChannelDisabledEvent are the control events
// published to a channel's stream right before the channel is deleted or
// disabled. Clients close their EventSource on either, and every hub that
// fans one out retires itself (see run): on the other replicas that is the
// only signal there is. They differ only so the client can show the right
// page: "not found" for a deleted channel, "disabled" for a disabled one.
const (
	sseChannelGoneEvent     = `{"type":"channel-deleted"}`
	sseChannelDisabledEvent = `{"type":"channel-disabled"}`
)

// sseEvent is one stream entry, broadcast verbatim. Per-viewer transformation
// (author masking for sub-writer viewers) happens at the subscriber, because it
// depends on who is watching rather than on the event.
type sseEvent struct {
	id   string
	data string
}

type sseSubscriber struct {
	ch chan sseEvent
	// closed guards against a double close when a slow subscriber is dropped by
	// the hub and then unsubscribes itself.
	once sync.Once
}

func (s *sseSubscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

type sseHub struct {
	streamKey string

	mu   sync.Mutex
	subs map[*sseSubscriber]struct{}
	// stop ends the reader goroutine once the last subscriber leaves.
	stop context.CancelFunc
}

var (
	sseHubsMu sync.Mutex
	sseHubs   = map[string]*sseHub{}
)

// sseSubscribe joins the stream's hub, starting it if this is the first viewer.
// The returned function must be called when the viewer goes away.
func sseSubscribe(streamKey string) (*sseSubscriber, func()) {
	sub := &sseSubscriber{ch: make(chan sseEvent, sseSubBuffer)}

	sseHubsMu.Lock()
	hub, ok := sseHubs[streamKey]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		hub = &sseHub{
			streamKey: streamKey,
			subs:      map[*sseSubscriber]struct{}{},
			stop:      cancel,
		}
		sseHubs[streamKey] = hub
		go hub.run(ctx)
	}
	// The subscriber is inserted while sseHubsMu is still held, so the insertion
	// is atomic with the registry lookup above. Releasing sseHubsMu first left a
	// gap: a concurrent last-viewer unsubscribe re-checks emptiness under
	// sseHubsMu (see sseUnsubscribe) and, not yet seeing this subscriber, could
	// retire the hub — leaving this viewer attached to a stopped hub that never
	// delivers an event and never closes its channel, so its handler sits on
	// heartbeats forever and the browser never reconnects. Holding sseHubsMu
	// across the add forces the retire to either see this subscriber (and not
	// retire) or run first (so the lookup above misses the hub and builds a new
	// one). The lock order here — sseHubsMu then hub.mu — matches the retire
	// path, so it cannot deadlock.
	hub.mu.Lock()
	hub.subs[sub] = struct{}{}
	hub.mu.Unlock()
	sseHubsMu.Unlock()

	return sub, func() { sseUnsubscribe(hub, sub) }
}

func sseUnsubscribe(hub *sseHub, sub *sseSubscriber) {
	hub.mu.Lock()
	delete(hub.subs, sub)
	remaining := len(hub.subs)
	hub.mu.Unlock()
	sub.close()

	if remaining > 0 {
		return
	}

	// Last viewer of this channel left: retire the hub and its connection.
	// Re-checked under the registry lock, because a new subscriber may have
	// arrived in the meantime and must not be left attached to a stopped hub.
	sseHubsMu.Lock()
	defer sseHubsMu.Unlock()
	if sseHubs[hub.streamKey] != hub {
		return
	}
	hub.mu.Lock()
	stillEmpty := len(hub.subs) == 0
	hub.mu.Unlock()
	if stillEmpty {
		delete(sseHubs, hub.streamKey)
		hub.stop()
	}
}

// run is the single reader for one stream. It starts at the tip: viewers that
// need earlier events replay them themselves before joining.
func (h *sseHub) run(ctx context.Context) {
	lastID := h.streamTip()
	failures := 0

	for {
		if ctx.Err() != nil {
			h.shutdown()
			return
		}

		streams, err := rdbEvents.XRead(ctx, &redis.XReadArgs{
			Streams: []string{h.streamKey, lastID},
			Count:   100,
			Block:   sseHubBlock,
		}).Result()

		if ctx.Err() != nil {
			h.shutdown()
			return
		}
		if err != nil {
			// redis.Nil is an ordinary "nothing arrived before the block
			// elapsed". Anything else is retried, but not for ever: a stream
			// that keeps failing must not spin.
			if err != redis.Nil {
				failures++
				if failures > maxStreamReadFailures {
					log.Printf("SSE hub %s: giving up after %d failed reads: %v\n", h.streamKey, failures, err)
					h.shutdown()
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		failures = 0

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				data, _ := msg.Values["data"].(string)
				h.broadcast(sseEvent{id: msg.ID, data: data})
				if data == sseChannelGoneEvent || data == sseChannelDisabledEvent {
					// The channel is going away. deleteChannel and the
					// disable path stop the hub on their own instance
					// directly; on every other replica this is the only
					// signal, and a hub left running kept a stale writer's
					// stream open — unmasked, and later fed by whatever
					// tenant re-created the slug. Its viewers' reconnects
					// meet channelMiddleware's 404/403 instead.
					h.stop()
					h.shutdown()
					return
				}
			}
		}
	}
}

// streamTip resolves the stream's current last id, so the reader has a fixed
// starting point.
//
// Reading from "$" instead would mean "entries added after THIS call was
// received", re-evaluated on every call: each time the block elapsed with
// nothing new, the next XREAD started from a fresh now, and an entry appended
// in the gap between the two calls — a round trip every five seconds — was
// never delivered to anyone on this instance. Falls back to "$" only when the
// tip cannot be read, which is the old behaviour rather than a replay of old
// events to every viewer.
func (h *sseHub) streamTip() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msgs, err := rdbEvents.XRevRangeN(ctx, h.streamKey, "+", "-", 1).Result()
	if err != nil {
		return "$"
	}
	if len(msgs) == 0 {
		return "0-0"
	}
	return msgs[0].ID
}

// broadcast delivers to every subscriber without ever blocking on one of them.
func (h *sseHub) broadcast(ev sseEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.ch <- ev:
		default:
			// This viewer is not draining fast enough. Drop it rather than
			// stalling the channel for everyone; its browser will reconnect and
			// resume from Last-Event-ID.
			delete(h.subs, sub)
			sub.close()
			log.Printf("SSE hub %s: dropped a subscriber that fell more than %d events behind\n",
				h.streamKey, sseSubBuffer)
		}
	}
}

// shutdown releases every subscriber still attached when the reader stops, so
// their handlers return instead of waiting on a channel nothing will write to.
func (h *sseHub) shutdown() {
	sseHubsMu.Lock()
	if sseHubs[h.streamKey] == h {
		delete(sseHubs, h.streamKey)
	}
	sseHubsMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		delete(h.subs, sub)
		sub.close()
	}
}

// sseCatchUp replays entries after lastID for a reconnecting viewer.
//
// The hub reads from the tip, so anything published between the viewer's last
// received event and the moment it subscribed exists only in the stream. The
// caller subscribes first and replays second, so an event arriving during the
// replay is buffered rather than lost; sending is then deduplicated on id.
//
// Control events are never replayed, only delivered live by the hub.
func sseCatchUp(ctx context.Context, streamKey, lastID string) []sseEvent {
	if lastID == "" || lastID == "$" {
		return nil
	}
	// "(" makes the range exclusive, so the client's last event is not resent.
	msgs, err := rdbEvents.XRangeN(ctx, streamKey, "("+lastID, "+", sseCatchUpLimit).Result()
	if err != nil {
		if err != redis.Nil {
			log.Printf("SSE catch-up on %s from %s failed: %v\n", streamKey, lastID, err)
		}
		return nil
	}
	out := make([]sseEvent, 0, len(msgs))
	for _, m := range msgs {
		data, _ := m.Values["data"].(string)
		if data == sseChannelGoneEvent || data == sseChannelDisabledEvent {
			// A control entry outlives its moment: a disable leaves it in the
			// stream, and a viewer whose tab was open then reconnects with a
			// last id from before it. Once the channel is re-enabled that
			// request passes channelMiddleware, and replaying the entry made
			// the client mark a live channel disabled until a manual reload.
			// A request that reached the replay has just passed the
			// middleware, so any control entry behind it is stale by
			// definition. A fresh one still arrives live through the hub.
			continue
		}
		out = append(out, sseEvent{id: m.ID, data: data})
	}
	return out
}

// streamIDLessOrEqual reports whether a <= b for Redis stream ids of the form
// "<ms>-<seq>". Both parts are compared numerically, because a lexical compare
// misorders them: within one millisecond "1000-9" and "1000-55" have
// single- and double-digit sequence parts, and as strings "1000-9" sorts
// AFTER "1000-55". In the catch-up dedup that meant a brand-new event could be
// skipped as already-seen (message loss) or a duplicate not skipped, whenever a
// channel produced more than nine events in the same millisecond and a viewer
// reconnected across that point.
func streamIDLessOrEqual(a, b string) bool {
	ams, aseq := splitStreamID(a)
	bms, bseq := splitStreamID(b)
	if ams != bms {
		return ams < bms
	}
	return aseq <= bseq
}

// splitStreamID parses "<ms>-<seq>" into its two numbers. A missing sequence
// part is 0, matching how Redis treats a bare "<ms>". Unparseable input yields
// zeros, which only makes the dedup more conservative (it never causes a live
// event to be dropped, since the ids the hub emits are always well-formed).
func splitStreamID(id string) (ms, seq uint64) {
	dash := strings.IndexByte(id, '-')
	if dash < 0 {
		ms, _ = strconv.ParseUint(id, 10, 64)
		return ms, 0
	}
	ms, _ = strconv.ParseUint(id[:dash], 10, 64)
	seq, _ = strconv.ParseUint(id[dash+1:], 10, 64)
	return ms, seq
}

// sseStopHub retires a stream's hub on this instance, closing every subscriber
// so its handler returns; a browser that reconnects then meets
// channelMiddleware, which refuses a deleted (404) or disabled (403) channel.
// Nothing did this before: a connection resolved its writer role once, when
// it opened, and kept it for as long as it stayed open — so a demoted writer's
// tab kept an unmasked stream, and after a delete and a re-creation of the
// slug it received the new tenant's events unmasked.
func sseStopHub(streamKey string) {
	sseHubsMu.Lock()
	hub, ok := sseHubs[streamKey]
	if ok {
		delete(sseHubs, streamKey)
	}
	sseHubsMu.Unlock()
	if !ok {
		return
	}
	hub.stop()
	// Not left to the reader: it notices the cancel only once its blocking
	// read returns, up to sseHubBlock later. shutdown tolerates running twice.
	hub.shutdown()
}

// sseHubCount reports how many hubs are running, i.e. how many Redis
// connections the event system is holding. Used by tests.
func sseHubCount() int {
	sseHubsMu.Lock()
	defer sseHubsMu.Unlock()
	return len(sseHubs)
}
