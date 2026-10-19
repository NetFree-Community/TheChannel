package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			runScheduledMessages()
		}
	}()
}

const scheduledLockTTL = 55 * time.Second

func scheduledLockKey(slug string) string { return "scheduled:lock:" + slug }

// releaseScheduledLock deletes the claim only if it still holds our token, on a
// fresh context: the caller's context may already be expired (which would leave
// the lock lingering for its full TTL), and a plain DEL could release a lock a
// second instance has since acquired after ours expired.
var scheduledLockRelease = redis.NewScript(`
	if redis.call('get', KEYS[1]) == ARGV[1] then
		return redis.call('del', KEYS[1])
	end
	return 0
`)

func releaseScheduledLock(slug, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scheduledLockRelease.Run(ctx, rdb, []string{scheduledLockKey(slug)}, token)
}

// claimScheduled takes the per-channel dispatch claim for ttl, retrying
// briefly, and returns the token to release it with. Channel deletion holds
// it for the whole delete: a dispatch that straddled the delete wrote the
// pruned list and the due entry back after the keys were gone, and a channel
// re-created under the same slug inherited — and posted — them.
func claimScheduled(ctx context.Context, slug string, ttl, wait time.Duration) (string, bool) {
	token := generatedRandomID(16)
	if token == "" {
		return "", false
	}
	attempts := int(wait / (200 * time.Millisecond))
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		ok, err := rdb.SetNX(ctx, scheduledLockKey(slug), token, ttl).Result()
		if err == nil && ok {
			return token, true
		}
		if ctx.Err() != nil {
			return "", false
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", false
}

// deferScheduledChannel pushes a channel's due score forward so a channel the
// dispatcher cannot serve (disabled, feature off) rotates out of the window
// of lowest scores. Left in place, such entries accumulated at the front of
// the due set until the bounded read returned nothing but them, and every
// channel with a genuinely due post behind them was never dispatched again.
// A re-enabled channel resumes within this delay, or on its next list save.
const scheduledSkipBackoff = 10 * time.Minute

func deferScheduledChannel(slug string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb.ZAdd(ctx, "scheduled:due_channels", redis.Z{
		Score:  float64(time.Now().Add(scheduledSkipBackoff).Unix()),
		Member: slug,
	})
}

// forgetScheduledChannel drops the due entry and the pending list of a
// channel that no longer exists.
func forgetScheduledChannel(slug string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pipe := rdb.Pipeline()
	pipe.ZRem(ctx, "scheduled:due_channels", slug)
	pipe.Del(ctx, fmt.Sprintf("channel:%s:scheduled_messages:list", slug))
	pipe.Del(ctx, scheduledDispatchedKey(slug))
	pipe.Exec(ctx)
}

// scheduledDispatchedKey is the set of fingerprints of the entries the
// dispatcher has posted, kept for scheduledDispatchedTTL so that a stale
// client snapshot cannot resurrect one (see dropDispatchedScheduled).
func scheduledDispatchedKey(slug string) string {
	return fmt.Sprintf("channel:%s:scheduled:dispatched", slug)
}

// scheduledDispatchedTTL is how long a dispatched entry is remembered. A tab
// left open over a weekend still holds its snapshot, so an hour is too short;
// the set is one short line per post and is refreshed on every dispatch.
const scheduledDispatchedTTL = 7 * 24 * time.Hour

// scheduledFingerprint identifies an entry by the two things a client hands
// back unchanged: its scheduled time and its text.
func scheduledFingerprint(m Message) string {
	return fmt.Sprintf("%d:%x", m.Timestamp.UnixNano(), sha256.Sum256([]byte(m.Text)))
}

// rememberScheduledDispatch records that msg was posted. Best effort: a lost
// write only leaves the old behaviour (a stale save can repost the entry).
func rememberScheduledDispatch(ctx context.Context, slug string, msg Message) {
	key := scheduledDispatchedKey(slug)
	pipe := rdb.Pipeline()
	pipe.SAdd(ctx, key, scheduledFingerprint(msg))
	pipe.Expire(ctx, key, scheduledDispatchedTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("rememberScheduledDispatch: %s: %v\n", slug, err)
	}
}

// dropDispatchedScheduled removes from a submitted list every entry the
// dispatcher has already posted. A tab that missed the dispatch event (a
// backgrounded phone, an SSE gap) still holds the entry with its past
// timestamp and hands it back on its next save, and the next tick posted,
// pushed and webhooked it a second time. Not an error: the rest of the save
// is what the user meant, and the entry is simply already done.
func dropDispatchedScheduled(ctx context.Context, slug string, messages []Message) []Message {
	dispatched, err := rdb.SMembers(ctx, scheduledDispatchedKey(slug)).Result()
	if err != nil || len(dispatched) == 0 {
		return messages
	}
	seen := make(map[string]struct{}, len(dispatched))
	for _, fp := range dispatched {
		seen[fp] = struct{}{}
	}
	kept := make([]Message, 0, len(messages))
	for _, m := range messages {
		if _, ok := seen[scheduledFingerprint(m)]; ok {
			log.Printf("updateScheduledMessages: %s: dropping an entry the scheduler already posted (due %s)\n", slug, m.Timestamp.Format(time.RFC3339))
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// runScheduledMessages only processes channels that have at least one message
// due before now, using the "scheduled:due_channels" sorted set (score = earliest
// due timestamp). This avoids querying every channel every minute.
// maxDueChannelsPerTick bounds one scheduler pass.
const maxDueChannelsPerTick = 500

func runScheduledMessages() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := float64(time.Now().Unix())

	// Get all channels with at least one message due by now
	// Bounded: an unbounded read meant a post-outage catch-up, where every
	// channel is due at once, produced a tick that ran far past its own budget
	// while the next tick started anyway and piled up on top of it. Whatever
	// does not fit is still due and is picked up by the following tick.
	slugs, err := rdb.ZRangeByScore(ctx, "scheduled:due_channels", &redis.ZRangeBy{
		Min:   "0",
		Max:   fmt.Sprintf("%f", now),
		Count: maxDueChannelsPerTick,
	}).Result()
	if err != nil || len(slugs) == 0 {
		return
	}

	for _, slug := range slugs {
		slug := slug

		// The outer budget was declared and then never consulted: every
		// operation inside this loop builds its own context, so the loop could
		// run indefinitely. Stop when the tick's time is up and leave the rest
		// for the next one.
		if ctx.Err() != nil {
			log.Printf("runScheduledMessages: tick budget exhausted; %d channels deferred to the next tick\n", len(slugs))
			return
		}

		// Claim the channel for this tick. Every instance runs this ticker, and
		// dispatch is a read-then-write over the pending list with no atomicity,
		// so without a claim two replicas post the same message twice. The value
		// is a unique token so only the claimant can release it, and a per-slug
		// context is used so one slow channel does not consume the outer budget
		// and starve the SetNX of every remaining due channel this tick.
		lockKey := scheduledLockKey(slug)
		token := generatedRandomID(16)
		lockCtx, lockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		ok, lerr := rdb.SetNX(lockCtx, lockKey, token, scheduledLockTTL).Result()
		lockCancel()
		if lerr != nil || !ok {
			continue
		}

		// The dispatcher is the one writer that never passes through
		// channelMiddleware, so neither the super admin's kill switch nor the
		// scheduled-messages feature toggle reached it: a disabled channel
		// kept posting, firing its webhook and pushing to subscribers every
		// minute. The pending list and the due-set entry are left untouched,
		// so re-enabling the channel simply resumes it.
		ctxCh, cancelCh := context.WithTimeout(context.Background(), 5*time.Second)
		channel, cherr := dbGetChannel(ctxCh, slug)
		cancelCh()
		switch {
		case cherr == redis.Nil:
			// Deleted: nothing will ever serve this entry, and the slug is
			// free to be re-created, so its leftovers go now.
			forgetScheduledChannel(slug)
			releaseScheduledLock(slug, token)
			continue
		case cherr != nil:
			releaseScheduledLock(slug, token)
			continue
		case channel.Features.Disabled || !channel.Features.ScheduledMessages:
			deferScheduledChannel(slug)
			releaseScheduledLock(slug, token)
			continue
		}

		ctxGet, cancelGet := context.WithTimeout(context.Background(), 5*time.Second)
		list, err := dbGetScheduledMessages(ctxGet, slug)
		cancelGet()
		if err != nil {
			releaseScheduledLock(slug, token)
			continue
		}

		nowTime := time.Now()
		// One snapshot per channel per tick, for the re-labelling below.
		ops := currentOperators()
		newList := make([]Message, 0)
		for _, msg := range *list {
			if !msg.Timestamp.Before(nowTime) {
				newList = append(newList, msg)
				continue
			}

			// Posted synchronously: the save below erases everything not in
			// newList, so a message that failed to post must stay pending
			// instead of vanishing with only a log line.
			postCtx, postCancel := context.WithTimeout(context.Background(), 5*time.Second)
			id, ierr := getMessageNextId(postCtx, slug)
			if ierr != nil {
				log.Printf("Failed to allocate message id for scheduled post on %s: %v\n", slug, ierr)
				postCancel()
				newList = append(newList, msg)
				continue
			}
			m := msg
			m.ID = id
			m.Timestamp = time.Now()
			// Entries scheduled before the author was recorded carry no name;
			// the literal is kept for them (and for the client, which still
			// recognises it as a scheduled post).
			if m.Author == "" {
				m.Author = "Scheduled"
				m.AuthorId = "0"
			}
			// An entry an operator scheduled before operators were anonymised
			// still carries their name; the post it becomes must not.
			ops.anonymiseOperatorAuthor(&m)
			if serr := setMessage(postCtx, slug, &m, false); serr != nil {
				log.Printf("Failed to post scheduled message on %s: %v\n", slug, serr)
				postCancel()
				newList = append(newList, msg)
				continue
			}
			// Remembered under the entry's own scheduled time (msg, not m):
			// that is what a tab holding a stale snapshot hands back, and
			// what updateScheduledMessages drops so the next tick cannot
			// post it a second time.
			rememberScheduledDispatch(postCtx, slug, msg)
			// Posting can take a while under a degraded Redis; refresh the claim
			// after each message so it cannot expire mid-run and let a second
			// replica re-read the still-unsaved list and re-post everything.
			rdb.Expire(postCtx, lockKey, scheduledLockTTL)
			postCancel()

			go SendWebhook(context.Background(), slug, "create", &m)
			go pushFcmMessage(slug, &m)
		}

		// The messages above are already posted; if this save fails they stay in
		// the pending list and repost on the next tick, so the failure must not
		// be swallowed. The save is a plain SET of the pruned list, so retrying
		// is idempotent.
		var saveErr error
		for attempt := 0; attempt < 3; attempt++ {
			ctxSave, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
			saveErr = dbSaveScheduledMessages(ctxSave, slug, &newList) // also updates the sorted set
			cancelSave()
			if saveErr == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
		if saveErr != nil {
			log.Printf("CRITICAL: failed to persist scheduled list for %s after dispatch: %v — dispatched messages will repost next tick\n", slug, saveErr)
		}

		releaseScheduledLock(slug, token)
	}
}

func getScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	messages, err := dbGetScheduledMessages(ctx, slug)
	if err != nil {
		http.Error(w, "error getting messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func updateScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()

	slug := channelSlugFromCtx(r)

	var messages []Message
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, "error decoding messages", http.StatusBadRequest)
		return
	}
	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

	// Any non-positive Unix timestamp (not just the exact zero time) would be
	// stored as an entry the earliest-due computation in dbSaveScheduledMessages
	// skips, and a lone such entry takes the channel out of the scheduler's due
	// set entirely — stranding it forever.
	for i, m := range messages {
		if m.Timestamp.Unix() <= 0 {
			http.Error(w, "scheduled message requires a timestamp", http.StatusBadRequest)
			return
		}
		// Dispatch stores the text verbatim, so the cap the live write paths
		// enforce has to hold here too.
		if len(m.Text) > maxMessageTextLen {
			http.Error(w, "text too long", http.StatusBadRequest)
			return
		}
		// Only the fields a scheduled post is made of are kept. The dispatcher
		// stores the struct as sent, so client-supplied views, deleted or
		// reactions used to land in the live message hash — a "deleted" post
		// born already hidden. is_ads stays: the composer's ad toggle is an
		// ordinary writer control on the live path too.
		typ := m.Type
		if typ == "" {
			typ = "md"
		}
		messages[i] = Message{Type: typ, Text: m.Text, Timestamp: m.Timestamp, IsAds: m.IsAds}
	}

	// Take the same per-channel claim the dispatcher holds: dbSaveScheduledMessages
	// is a blind SET of the whole list, so a save landing between the dispatcher's
	// read and write would be overwritten by its stale snapshot — additions vanish
	// and deleted messages get resurrected and posted.
	lockKey := scheduledLockKey(slug)
	token := generatedRandomID(16)
	acquired := false
	for attempt := 0; attempt < 10; attempt++ {
		ok, err := rdb.SetNX(ctx, lockKey, token, 10*time.Second).Result()
		if err == nil && ok {
			acquired = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !acquired {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "scheduled messages busy, try again", http.StatusServiceUnavailable)
		return
	}
	defer releaseScheduledLock(slug, token)

	// Entries the dispatcher has already posted are not saved back, whatever
	// snapshot the client built the list from.
	messages = dropDispatchedScheduled(ctx, slug, messages)

	// Dispatch used to post every scheduled message as the English literal
	// "Scheduled", shown to moderators and sent to webhooks. The scheduler's
	// own name is recorded now: an entry that already exists in the stored
	// list (same time and text) keeps its original author, so a moderator
	// editing one message does not re-attribute the others, and a new entry
	// is stamped with the session — never with a client-supplied name.
	//
	// An operator's entries are signed operatorName, as addMessage signs an
	// operator's post: the stored list is read back by every moderator, and
	// the dispatcher posts the entry under the author recorded here.
	ops := currentOperators()
	if stored, err := dbGetScheduledMessages(ctx, slug); err == nil && stored != nil {
		type authorKey struct {
			at   int64
			text string
		}
		authors := make(map[authorKey]Message, len(*stored))
		for _, sm := range *stored {
			authors[authorKey{sm.Timestamp.UnixNano(), sm.Text}] = sm
		}
		for i := range messages {
			if sm, ok := authors[authorKey{messages[i].Timestamp.UnixNano(), messages[i].Text}]; ok && sm.Author != "" {
				messages[i].Author, messages[i].AuthorId = sm.Author, sm.AuthorId
				// Recorded before operators were anonymised.
				ops.anonymiseOperatorAuthor(&messages[i])
			}
		}
	}
	superAdmin := isSuperAdmin(r)
	for i := range messages {
		if messages[i].Author == "" {
			messages[i].Author = sessionDisplayName(user)
			messages[i].AuthorId = user.ID
			if superAdmin {
				messages[i].Author, messages[i].AuthorId = operatorName, operatorAuthorId
			}
		}
	}

	if err := dbSaveScheduledMessages(ctx, slug, &messages); err != nil {
		http.Error(w, "error saving messages", http.StatusInternalServerError)
		return
	}

	var res = Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
