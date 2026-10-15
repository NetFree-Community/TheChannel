package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi"
)

// maxMessagesPerRequest caps the client-supplied page size for /messages.
const maxMessagesPerRequest = 100

// maxStreamReadFailures bounds how long an SSE connection keeps retrying a
// stream read that consistently fails before the handler gives up.
const maxStreamReadFailures = 5

// sseRedisPoolSize is the size of the SSE-only Redis pool (see db.go init).
//
// Connections are now held per *channel with at least one viewer*, not per
// viewer: sse_hub.go runs one reader per stream and fans out in process. This
// therefore bounds how many distinct channels can have live viewers at once on
// one instance, and a channel with ten thousand readers still costs one
// connection.
const sseRedisPoolSize = 500

// sseMaxConnections caps concurrent SSE viewers per instance. It is no longer
// tied to the Redis pool — a viewer costs a goroutine, a socket and a 256-event
// buffer, not a connection — so it exists only to bound memory and file
// descriptors, and sits far above the old ceiling.
const sseMaxConnections = 20000

// sseActiveConnections counts live SSE handlers on this instance.
var sseActiveConnections atomic.Int64

// sseMaxPerClient caps concurrent /events streams per client (session e-mail
// or address). The global cap alone let one anonymous client open streams
// until every other viewer was refused. Generous, because a large office NAT
// behind one X-Real-IP is many real viewers; one client with more open tabs
// than this to the same site is not.
const sseMaxPerClient = 100

var (
	sseClientMu    sync.Mutex
	sseClientConns = map[string]int{}
)

// admitSSEClient reserves a per-client slot and returns how to release it,
// or false when the client already holds sseMaxPerClient streams.
func admitSSEClient(key string) (func(), bool) {
	sseClientMu.Lock()
	defer sseClientMu.Unlock()
	if sseClientConns[key] >= sseMaxPerClient {
		return nil, false
	}
	sseClientConns[key]++
	return func() {
		sseClientMu.Lock()
		defer sseClientMu.Unlock()
		if sseClientConns[key] <= 1 {
			delete(sseClientConns, key)
		} else {
			sseClientConns[key]--
		}
	}, true
}

// streamIDRegex matches a Redis stream entry ID ("<ms>" or "<ms>-<seq>").
var streamIDRegex = regexp.MustCompile(`^\d+(-\d+)?$`)

func getMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	ch := channelFromCtx(r)

	offsetFromClient := r.URL.Query().Get("offset")
	limitFromClient := r.URL.Query().Get("limit")
	direction := r.URL.Query().Get("direction")

	offset, err := strconv.Atoi(offsetFromClient)
	if err != nil {
		offset = 0
	}

	// The limit drives a blocking Lua scan (ZREVRANGE + one HGETALL per member)
	// on a shared Redis, so an unbounded client value must not reach it.
	limit, err := strconv.Atoi(limitFromClient)
	if err != nil || limit <= 0 || limit > maxMessagesPerRequest {
		limit = 20
	}

	isAdmin := hasChannelRole(r, slug, RoleWriter)
	countViews := ch != nil && ch.Features.CountViews

	// ETag: skip expensive query if content hasn't changed since client's copy.
	// The body varies by viewer (admins see real authors and soft-deleted
	// posts), so the validator has to carry those inputs and the response must
	// never be reused across identities. The "op" marker retires validators
	// minted before operator posts were re-labelled, whose cached bodies still
	// name the operator.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Cookie")
	// No 304 while views are being counted: the short-circuit skips the query,
	// and with it the per-message view increment, so repeat visitors to a
	// quiet channel — the very population the counter is for — were never
	// counted and kept seeing the numbers from their first visit.
	if lm := getLastModified(ctx, slug); lm != "" && !countViews {
		etag := `"` + lm + "-" + strconv.FormatBool(isAdmin) + "-" + strconv.FormatBool(countViews) + `-op"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	messages, err := funcGetMessageRange(ctx, slug, int64(offset), int64(limit), isAdmin, countViews, direction)
	if err != nil {
		log.Printf("Failed to get messages: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// Staff get real authors from the Lua, and posts an operator wrote before
	// they were signed as operatorName still carry the operator's name and
	// Google id. Everyone else already gets "Anonymous".
	if isAdmin {
		ops := currentOperators()
		for i := range messages {
			ops.anonymiseOperatorAuthor(&messages[i])
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)

	addViewsToMessages(ctx, slug, countViews && !isCrawler(r), messages)
}

// maxMessageTextLen is the server-side ceiling on a post's markdown, shared by
// every write path (composer, edit, import API, scheduled list) so no route can
// store a body the others would refuse. The composer itself stops far earlier;
// this only bounds what a hand-made request can push into every SSE client.
const maxMessageTextLen = 100_000

func addMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var message Message
	var err error
	defer r.Body.Close()

	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	if len(body.Text) > maxMessageTextLen {
		http.Error(w, "text too long", http.StatusBadRequest)
		return
	}

	if message.ID, err = getMessageNextId(ctx, slug); err != nil {
		log.Printf("Failed to allocate message id: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	recordAuthorID(ctx, user)

	message.Type = body.Type
	message.Author = sessionDisplayName(user)
	message.AuthorId = user.ID
	// An operator posting is the platform speaking, and the post is served to
	// the channel's staff, its webhook and the event stream: none of them may
	// learn who the operator is.
	if isSuperAdmin(r) {
		message.Author, message.AuthorId = operatorName, operatorAuthorId
	}
	message.Timestamp = time.Now()
	message.Text = body.Text
	message.File = body.File
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, slug, &message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), slug, "create", &message)
	go pushFcmMessage(slug, &message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}

// sessionUserID is the Google subject of the signed-in user, or "" when there
// is no session.
func sessionUserID(r *http.Request) string {
	session, _ := store.Get(r, cookieName)
	user, ok := session.Values["user"].(Session)
	if !ok {
		return ""
	}
	return user.ID
}

// canModifyMessage reports whether the current session may edit or delete a
// message written by authorId. Writers may only touch their own posts;
// moderators (and above, including super admins) may touch any message.
func canModifyMessage(r *http.Request, slug, authorId string) bool {
	if hasChannelRole(r, slug, RoleModerator) {
		return true
	}
	// System posts have no real author: the scheduler stamps "0" and the API
	// import path never sets the field at all. They are channel content rather
	// than anyone's personal post, so any writer may manage them — the caller is
	// already behind protectedWithChannelRole(RoleWriter, ...).
	if authorId == "" || authorId == "0" {
		return true
	}
	session, _ := store.Get(r, cookieName)
	user, ok := session.Values["user"].(Session)
	if !ok || user.ID == "" {
		return false
	}
	return authorId == user.ID
}

func updateMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var err error
	defer r.Body.Close()

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	if len(body.Text) > maxMessageTextLen {
		http.Error(w, "text too long", http.StatusBadRequest)
		return
	}

	// The message must already exist: setMessage would otherwise happily create
	// an unindexed hash from whatever ID the client sent.
	stored, err := dbGetMessageFields(ctx, slug, strconv.Itoa(body.ID))
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if !canModifyMessage(r, slug, stored["authorId"]) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Republishing a soft-deleted post is a moderation decision. The composer
	// always sends deleted:false, so re-saving was enough for a writer to undo
	// a moderator's takedown of their own message. A writer may still restore
	// what they deleted themselves; deletedBy is empty on posts deleted before
	// it was recorded, and those keep the old behaviour.
	if stored["deleted"] == "1" && !body.Deleted && !hasChannelRole(r, slug, RoleModerator) {
		if by := stored["deletedBy"]; by != "" && by != sessionUserID(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	// Only the body of the message is editable — identity, ordering and counters
	// stay as stored, so they cannot be forged through the request payload.
	// Deleted is deliberately NOT restored: re-saving a deleted message is how
	// the composer republishes it, which the edit form promises to the user.
	body.Author = stored["author"]
	body.AuthorId = stored["authorId"]
	// Re-saving an operator post written before operators were anonymised
	// would re-broadcast their identity; store it re-labelled instead.
	currentOperators().anonymiseOperatorAuthor(&body)
	body.Views, _ = strconv.Atoi(stored["views"])
	if ts, perr := time.Parse(time.RFC3339Nano, stored["timestamp"]); perr == nil {
		body.Timestamp = ts
	}
	body.Reactions = nil
	if raw := stored["reactions"]; raw != "" {
		var reactions Reactions
		if json.Unmarshal([]byte(raw), &reactions) == nil {
			body.Reactions = reactions
		}
	}

	body.LastEdit = time.Now()

	// A storage failure must not read as success: the client replaces the
	// composer contents on a 2xx, so the user's edit would be lost silently.
	if err := setMessage(ctx, slug, &body, true); err != nil {
		log.Printf("Failed to update message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), slug, "update", &body)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func deleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	id := chi.URLParam(r, "id")

	stored, err := dbGetMessageFields(ctx, slug, id)
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if !canModifyMessage(r, slug, stored["authorId"]) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	idInt, _ := strconv.Atoi(id)
	message := Message{ID: idInt, Deleted: true}

	if err := funcDeleteMessage(ctx, slug, id, sessionUserID(r)); err != nil {
		log.Printf("Failed to delete message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), slug, "delete", &message)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

// getEvents serves a Server-Sent Events stream backed by a Redis Stream.
//
// Using Redis Streams instead of pub/sub enables:
//   - Horizontal scaling: multiple backend instances can all serve SSE
//   - Reconnection: clients send Last-Event-ID to resume without missing events
//   - Durability: stream retains the last ~1000 events (configurable in publishEvent)
func getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Admit before doing any work, and release on every exit path.
	if n := sseActiveConnections.Add(1); n > sseMaxConnections {
		sseActiveConnections.Add(-1)
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many live connections, please retry", http.StatusServiceUnavailable)
		return
	}
	defer sseActiveConnections.Add(-1)
	releaseClient, ok := admitSSEClient(clientKey(r))
	if !ok {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many live connections from this client", http.StatusTooManyRequests)
		return
	}
	defer releaseClient()

	slug := channelSlugFromCtx(r)
	streamKey := fmt.Sprintf("channel:%s:events", slug)

	// The stream stores the full payload including real author identity; the
	// /messages Lua deliberately anonymizes author/authorId for anyone below
	// writer, and this stream must not leak what that endpoint withholds. The
	// identity (session e-mail) is fixed per connection and resolved once; the
	// role is looked up live at send time, because a moderator or writer
	// demoted mid-connection kept seeing real names until they reconnected.
	viewerEmail := ""
	if s, ok := sessionEmail(r); ok {
		viewerEmail = normEmail(s.Email)
	}
	isWriter := func() bool { return emailHasChannelRole(viewerEmail, slug, RoleWriter) }

	// Support SSE reconnection: browser sends Last-Event-ID with the stream entry ID
	// from the last event it received. On fresh connect, start from "now".
	// A malformed value would make every XREAD fail, turning the loop below into
	// a busy retry against Redis, so anything that is not a stream ID is ignored.
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		// The client's heartbeat watchdog re-creates the EventSource by hand,
		// and a new EventSource carries no Last-Event-ID, so it resumed from
		// the tip and lost every edit, delete and reaction of the gap. It
		// passes the last id it saw as a query parameter instead.
		lastID = r.URL.Query().Get("last_id")
	}
	if !streamIDRegex.MatchString(lastID) {
		lastID = "$"
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if _, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n"); err != nil {
		return
	}
	flusher.Flush()

	// Increment synchronously: the deferred decrement runs as soon as the loop
	// notices a dead client, which can happen before a spawned goroutine is even
	// scheduled, leaving the counter one below where it started.
	increaseCounterSSE(slug)
	defer decreaseCounterSSE(slug)

	clientCtx := r.Context()
	const heartbeatInterval = 25 * time.Second

	// The server runs with no WriteTimeout (an SSE response never ends), so a
	// peer that stops reading — a backgrounded mobile browser behind a proxy
	// that has stopped draining — left this goroutine blocked inside a write
	// until TCP itself gave up, some fifteen minutes later, holding a
	// connection slot the whole time. A per-write deadline turns that into a
	// write error, and the browser reconnects.
	const sseWriteTimeout = 30 * time.Second
	rc := http.NewResponseController(w)
	armWrite := func() { _ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)) }

	// Join the channel's shared reader BEFORE replaying history, so an event
	// published during the replay is buffered rather than missed. The replay is
	// then deduplicated against what the hub delivers, using the stream ids.
	sub, unsubscribe := sseSubscribe(streamKey)
	defer unsubscribe()

	// channelMiddleware admitted this request a moment ago, but a delete or
	// disable can land in between: the control event is then already the
	// stream tip, a hub built now starts past it and never reads it, and
	// catch-up skips it by design. Re-read the flag once, after subscribing,
	// so such a viewer is turned away instead of holding a live-looking page
	// on a channel that is gone (another replica's hub is the only one that
	// would otherwise outlive it).
	{
		rctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		ch, err := dbGetChannel(rctx, slug)
		cancel()
		if err != nil || ch == nil || ch.Features.Disabled {
			return
		}
	}

	send := func(ev sseEvent) bool {
		data := ev.data
		if !isWriter() {
			data = maskEventAuthor(data)
		} else {
			// Staff see real authors, but never an operator's: events stored
			// before operators were anonymised still name them. The snapshot is
			// per event so an operator added mid-connection is covered.
			data = currentOperators().anonymiseOperatorEvent(data)
		}
		armWrite()
		if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", ev.id, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// A reconnecting client carries Last-Event-ID; the hub reads from the tip,
	// so whatever it missed while away is replayed from the stream here. The
	// replay carries no channel-deleted/disabled entry (see sseCatchUp): this
	// request passed channelMiddleware, so one left in the stream is stale.
	replayedTo := ""
	for _, ev := range sseCatchUp(clientCtx, streamKey, lastID) {
		if !send(ev) {
			return
		}
		replayedTo = ev.id
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-clientCtx.Done():
			return

		case ev, ok := <-sub.ch:
			if !ok {
				// The hub stopped, or this connection fell too far behind and was
				// dropped. Returning ends the response; the browser reconnects
				// with Last-Event-ID and resumes.
				return
			}
			// Skip anything the catch-up above already delivered.
			if replayedTo != "" && streamIDLessOrEqual(ev.id, replayedTo) {
				continue
			}
			if !send(ev) {
				return
			}

		case <-heartbeat.C:
			armWrite()
			if _, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// maskEventAuthor rewrites an event payload the way the /messages Lua does for
// sub-writer viewers: author becomes "Anonymous" and authorId is blanked. Kept
// cheap: the payload is only re-marshalled when there is something to mask, and
// anything that is not a PushMessage (heartbeats, malformed entries) passes
// through untouched.
func maskEventAuthor(data string) string {
	var pm PushMessage
	if err := json.Unmarshal([]byte(data), &pm); err != nil {
		return data
	}
	if pm.M.Author == "" && pm.M.AuthorId == "" {
		return data
	}
	pm.M.Author = "Anonymous"
	pm.M.AuthorId = ""
	masked, err := json.Marshal(pm)
	if err != nil {
		return data
	}
	return string(masked)
}
