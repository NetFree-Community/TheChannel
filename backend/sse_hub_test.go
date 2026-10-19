package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func drainHubs(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for sseHubCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// The point of the hub: viewer count and Redis connection count are no longer
// the same number. Many viewers of one channel share a single reader.
func TestSSEHubOneReaderPerChannelRegardlessOfViewers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	drainHubs(t)
	const slug = "hub-fanout"
	streamKey := "channel:" + slug + ":events"
	t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

	const viewers = 200
	subs := make([]*sseSubscriber, viewers)
	stops := make([]func(), viewers)
	for i := 0; i < viewers; i++ {
		subs[i], stops[i] = sseSubscribe(streamKey)
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()

	if got := sseHubCount(); got != 1 {
		t.Fatalf("%d viewers of one channel produced %d hubs, want exactly 1", viewers, got)
	}

	// Give the reader a moment to reach its first blocking read at the tip,
	// so the event below is genuinely published after it started listening.
	time.Sleep(300 * time.Millisecond)
	publishEvent(ctx, slug, []byte(`{"type":"new-message","message":{"id":1}}`))

	// Every viewer must receive it.
	var received int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func(s *sseSubscriber) {
			defer wg.Done()
			select {
			case ev, ok := <-s.ch:
				if ok && ev.data != "" {
					mu.Lock()
					received++
					mu.Unlock()
				}
			case <-time.After(10 * time.Second):
			}
		}(subs[i])
	}
	wg.Wait()

	if received != viewers {
		t.Errorf("%d of %d viewers received the event", received, viewers)
	}
	t.Logf("%d viewers, %d hub(s), %d deliveries", viewers, 1, received)
}

// Distinct channels each get their own reader, and a channel's reader is
// retired once its last viewer leaves — otherwise connections leak per channel
// ever visited.
func TestSSEHubPerChannelLifecycle(t *testing.T) {
	drainHubs(t)

	keys := []string{"channel:hub-a:events", "channel:hub-b:events", "channel:hub-c:events"}
	stops := make([]func(), 0, len(keys))
	for _, k := range keys {
		_, stop := sseSubscribe(k)
		stops = append(stops, stop)
	}
	if got := sseHubCount(); got != len(keys) {
		t.Fatalf("hubs = %d, want %d (one per channel)", got, len(keys))
	}

	// A second viewer on the first channel must not create a second reader.
	_, stopExtra := sseSubscribe(keys[0])
	if got := sseHubCount(); got != len(keys) {
		t.Errorf("a second viewer created a new hub: %d, want %d", got, len(keys))
	}
	stopExtra()
	if got := sseHubCount(); got != len(keys) {
		t.Errorf("one of two viewers leaving retired the hub: %d, want %d", got, len(keys))
	}

	for _, stop := range stops {
		stop()
	}
	if !waitFor(t, 5*time.Second, func() bool { return sseHubCount() == 0 }) {
		t.Errorf("hubs still running after every viewer left: %d", sseHubCount())
	}
}

// The hub reads from the tip, so a reconnecting viewer's history has to come
// from the stream. This is what stops a reconnect from silently losing events.
func TestSSECatchUpReplaysMissedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "hub-catchup"
	streamKey := "channel:" + slug + ":events"
	rdb.Del(ctx, streamKey)
	t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

	publishEvent(ctx, slug, []byte(`{"n":1}`))
	publishEvent(ctx, slug, []byte(`{"n":2}`))
	publishEvent(ctx, slug, []byte(`{"n":3}`))

	all, err := rdb.XRangeN(ctx, streamKey, "-", "+", 10).Result()
	if err != nil || len(all) != 3 {
		t.Fatalf("seed: got %d entries, err %v", len(all), err)
	}

	// A client that last saw the first event replays exactly the other two.
	missed := sseCatchUp(ctx, streamKey, all[0].ID)
	if len(missed) != 2 {
		t.Fatalf("replayed %d events, want 2", len(missed))
	}
	if missed[0].id != all[1].ID || missed[1].id != all[2].ID {
		t.Errorf("replayed the wrong entries: %s,%s want %s,%s",
			missed[0].id, missed[1].id, all[1].ID, all[2].ID)
	}
	// The client's own last event must not be resent.
	for _, ev := range missed {
		if ev.id == all[0].ID {
			t.Error("catch-up resent the event the client already had")
		}
	}

	// A fresh connection asks for no history.
	if got := sseCatchUp(ctx, streamKey, "$"); got != nil {
		t.Errorf("fresh connect replayed %d events, want none", len(got))
	}
}

// A viewer that stops reading must not be able to stall delivery for everyone
// else on the channel. It is dropped instead; its browser reconnects.
func TestSSEHubDropsSlowSubscriberWithoutStallingOthers(t *testing.T) {
	drainHubs(t)

	streamKey := "channel:hub-slow:events"
	hub := &sseHub{streamKey: streamKey, subs: map[*sseSubscriber]struct{}{}, stop: func() {}}

	slow := &sseSubscriber{ch: make(chan sseEvent, sseSubBuffer)}
	fast := &sseSubscriber{ch: make(chan sseEvent, sseSubBuffer)}
	hub.subs[slow] = struct{}{}
	hub.subs[fast] = struct{}{}

	// Fill the slow viewer's buffer; it never reads.
	for i := 0; i < sseSubBuffer; i++ {
		hub.broadcast(sseEvent{id: fmt.Sprintf("%d-0", i), data: "x"})
		// Keep the fast one drained so only the slow one backs up.
		<-fast.ch
	}

	// The next broadcast must not block, and must drop the slow viewer.
	done := make(chan struct{})
	go func() {
		hub.broadcast(sseEvent{id: "999-0", data: "y"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast blocked on a subscriber that stopped reading")
	}

	hub.mu.Lock()
	_, slowStillAttached := hub.subs[slow]
	_, fastStillAttached := hub.subs[fast]
	hub.mu.Unlock()

	if slowStillAttached {
		t.Error("the slow subscriber was not dropped")
	}
	if !fastStillAttached {
		t.Error("the healthy subscriber was dropped too")
	}
	// A dropped subscriber's channel is closed, so its handler returns.
	if _, ok := <-slow.ch; ok {
		// Buffered events drain first; keep draining to the close.
		for range slow.ch {
		}
	}
	// The healthy viewer still received the latest event.
	select {
	case ev := <-fast.ch:
		if ev.id != "999-0" {
			t.Errorf("healthy subscriber got %s, want 999-0", ev.id)
		}
	default:
		t.Error("healthy subscriber received nothing after the slow one was dropped")
	}
}

// Redis stream ids are "<ms>-<seq>", and both parts are numbers. The catch-up
// dedup compares them, and a lexical compare misorders same-millisecond ids
// once the sequence crosses the single->double digit boundary. String compare
// would drop a genuinely new event as already-seen (message loss) or fail to
// skip a duplicate.
func TestStreamIDLessOrEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1000-0", "1000-0", true},   // equal
		{"1000-9", "1000-55", true},  // seq 9 < 55 (the case string compare gets wrong)
		{"1000-55", "1000-9", false}, // seq 55 > 9
		{"1000-9", "1000-10", true},  // 9 < 10 (string would say false)
		{"1000-10", "1000-9", false}, // 10 > 9 (string would say true -> would drop new event)
		{"999-0", "1000-0", true},    // different ms digit lengths
		{"1000-0", "999-0", false},
		{"1526919030474-0", "1526919030474-1", true},
		{"1000", "1000-0", true},  // bare ms == "<ms>-0"
		{"1000-5", "1000", false}, // seq 5 > implied 0
	}
	for _, c := range cases {
		if got := streamIDLessOrEqual(c.a, c.b); got != c.want {
			t.Errorf("streamIDLessOrEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}

	// The specific regression: string comparison would drop a new event. Prove
	// the two disagree exactly where it matters, and that ours is right.
	if ("1000-10" <= "1000-9") == streamIDLessOrEqual("1000-10", "1000-9") {
		t.Error("test precondition: string and numeric compare should disagree on 1000-10 vs 1000-9")
	}
	if streamIDLessOrEqual("1000-10", "1000-9") {
		t.Error("1000-10 (seq 10) must sort AFTER 1000-9 (seq 9); dropping it would lose a live message")
	}
}

// Subscribe/retire race: a viewer arriving on a channel exactly as its last
// existing viewer leaves must end up on a live hub (or cleanly closed), never
// attached to a hub that has just been retired and will never deliver again.
//
// The invariant checked after each round: the subscriber that is still attached
// receives a freshly published event. Under the pre-fix code, a subscriber
// occasionally landed on a retired hub and its channel stayed silent. Runs many
// rounds under -race.
func TestSSEHubSubscribeRetireRace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	drainHubs(t)
	const slug = "hub-race"
	streamKey := "channel:" + slug + ":events"
	t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

	for round := 0; round < 300; round++ {
		// One existing viewer, about to leave.
		_, stopA := sseSubscribe(streamKey)

		var wg sync.WaitGroup
		wg.Add(2)
		// A leaves and B arrives concurrently — the exact contended window.
		var subB *sseSubscriber
		var stopB func()
		go func() { defer wg.Done(); stopA() }()
		go func() { defer wg.Done(); subB, stopB = sseSubscribe(streamKey) }()
		wg.Wait()

		// B is the surviving viewer. It must be on a live hub: publish and it
		// must receive the event.
		if sseHubCount() == 0 {
			t.Fatalf("round %d: hub was retired while a viewer (B) is still subscribed", round)
		}
		// Let the hub reach its blocking read, then publish.
		time.Sleep(5 * time.Millisecond)
		publishEvent(ctx, slug, []byte(`{"r":`+itoa(round)+`}`))

		select {
		case ev, ok := <-subB.ch:
			if !ok {
				t.Fatalf("round %d: surviving viewer's channel was closed (attached to a dead hub)", round)
			}
			_ = ev
		case <-time.After(3 * time.Second):
			t.Fatalf("round %d: surviving viewer received no event — attached to a retired hub", round)
		}
		stopB()
		// Drain any buffered events so the next round starts clean.
		drainHubs(t)
	}
}

// Retiring a hub closes every subscriber so its handler returns; a viewer
// that comes back builds a fresh hub or, after a delete or disable, meets
// channelMiddleware. This is what ends a stale writer's unmasked stream.
func TestSSEStopHubClosesSubscribers(t *testing.T) {
	drainHubs(t)
	const streamKey = "channel:hub-stop:events"
	sub, stop := sseSubscribe(streamKey)
	defer stop()

	sseStopHub(streamKey)

	select {
	case _, ok := <-sub.ch:
		if ok {
			t.Fatal("got an event, want the subscriber channel closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber channel not closed by sseStopHub")
	}
	if got := sseHubCount(); got != 0 {
		t.Errorf("hubs = %d after the stop, want 0", got)
	}
	// A stream with no hub is a no-op.
	sseStopHub("channel:hub-never:events")
}

// On every other replica the control event is the only signal: a hub that
// fans it out delivers it and then retires itself.
func TestSSEHubRetiresItselfOnChannelGoneEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	drainHubs(t)
	const slug = "hub-gone"
	streamKey := "channel:" + slug + ":events"
	t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

	sub, stop := sseSubscribe(streamKey)
	defer stop()
	time.Sleep(300 * time.Millisecond)
	publishEvent(ctx, slug, []byte(sseChannelGoneEvent))

	select {
	case ev, ok := <-sub.ch:
		if !ok || ev.data != sseChannelGoneEvent {
			t.Fatalf("got (%q, %v), want the control event delivered first", ev.data, ok)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("control event not delivered")
	}
	select {
	case _, ok := <-sub.ch:
		if ok {
			t.Fatal("got another event, want the subscriber channel closed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hub did not retire after fanning out the control event")
	}
	if !waitFor(t, 5*time.Second, func() bool { return sseHubCount() == 0 }) {
		t.Errorf("hubs still registered after the control event: %d", sseHubCount())
	}
}

// The disable path publishes the control event and then retires this
// instance's hub. Stopping the hub right after the publish closed every viewer
// before the hub's blocking read came back with the event, so the local
// viewers — every viewer, on a single replica — never learned the channel was
// disabled. The stop is now delayed; the hub retires itself on the event.
func TestSSEDisableDeliversControlEventToLocalViewer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	drainHubs(t)
	// One slug per round: the delayed stop of one round must not hit the hub
	// of the next, and each stream is dropped at the end.
	const rounds = 8
	for i := 0; i < rounds; i++ {
		slug := "hub-disable-" + itoa(i)
		streamKey := "channel:" + slug + ":events"
		t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

		sub, stop := sseSubscribe(streamKey)
		time.Sleep(300 * time.Millisecond) // the hub is blocked in XREAD at the tip

		announceChannelDisabled(ctx, slug)

		got := false
	drain:
		for {
			select {
			case ev, ok := <-sub.ch:
				if !ok {
					break drain
				}
				if ev.data == sseChannelDisabledEvent {
					got = true
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("round %d: subscriber neither closed nor delivered", i)
			}
		}
		stop()
		if !got {
			t.Fatalf("round %d: the local viewer's stream was closed without the channel-disabled event", i)
		}
		if !waitFor(t, 5*time.Second, func() bool { return sseHubCount() == 0 }) {
			t.Fatalf("round %d: hub still registered after the control event: %d", i, sseHubCount())
		}
	}
}

// A disable leaves its control entry in the stream. A viewer that reconnects
// later with a last id from before it — after the channel was re-enabled, so
// the request passed channelMiddleware — must not have it replayed: the client
// would mark a live channel disabled until a manual reload. Live delivery of a
// fresh control event is the hub's job (TestSSEHubRetiresItselfOnChannelGoneEvent).
func TestSSECatchUpSkipsStaleControlEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "hub-catchup-control"
	streamKey := "channel:" + slug + ":events"
	rdb.Del(ctx, streamKey)
	t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

	publishEvent(ctx, slug, []byte(`{"type":"new-message","message":{"id":1}}`))
	tip, err := rdb.XRevRangeN(ctx, streamKey, "+", "-", 1).Result()
	if err != nil || len(tip) != 1 {
		t.Fatalf("seed: %v", err)
	}
	lastSeen := tip[0].ID
	publishEvent(ctx, slug, []byte(sseChannelDisabledEvent)) // the disable
	publishEvent(ctx, slug, []byte(sseChannelGoneEvent))     // a delete that failed past its fence leaves this one
	const after = `{"type":"new-message","message":{"id":2}}`
	publishEvent(ctx, slug, []byte(after)) // posted after the re-enable

	events := sseCatchUp(ctx, streamKey, lastSeen)
	for _, ev := range events {
		if ev.data == sseChannelDisabledEvent || ev.data == sseChannelGoneEvent {
			t.Errorf("catch-up replayed a stale control event: %+v", events)
		}
	}
	if len(events) != 1 || events[0].data != after {
		t.Errorf("catch-up = %+v, want only the message posted after the re-enable", events)
	}
}

// deleteChannel publishes the control event before dbDeleteChannel fences the
// channel, and until the fence channelMiddleware still admits /events: a
// viewer whose stream had just been closed reconnects and builds a fresh hub
// that starts at the tip and never saw the event. On another replica no stop
// reaches it. dbDeleteChannel publishes the event again behind the fence, so
// such a hub retires too instead of outliving the channel on the deleted key.
func TestSSEHubBuiltBeforeFenceRetiresOnDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	drainHubs(t)
	const slug = "hub-delete-window"
	streamKey := "channel:" + slug + ":events"
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_ = dbDeleteChannel(cctx, slug)
		rdb.Del(cctx, streamKey)
	})
	if err := dbCreateChannel(ctx, &ChannelData{Slug: slug, Name: "N", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// The window: the first control event is already in the stream when the
	// reconnect builds its hub, which therefore starts past it.
	publishEvent(ctx, slug, []byte(sseChannelGoneEvent))
	sub, stop := sseSubscribe(streamKey)
	defer stop()
	time.Sleep(300 * time.Millisecond)

	if err := dbDeleteChannel(ctx, slug); err != nil {
		t.Fatalf("dbDeleteChannel: %v", err)
	}

	select {
	case ev, ok := <-sub.ch:
		if !ok || ev.data != sseChannelGoneEvent {
			t.Fatalf("got (%q, %v), want the control event delivered to the hub built in the window", ev.data, ok)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a hub built between the control event and the fence never got a control event")
	}
	select {
	case _, ok := <-sub.ch:
		if ok {
			t.Fatal("got another event, want the subscriber channel closed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hub did not retire after the second control event")
	}
	if !waitFor(t, 5*time.Second, func() bool { return sseHubCount() == 0 }) {
		t.Errorf("hubs still registered after the delete: %d", sseHubCount())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
