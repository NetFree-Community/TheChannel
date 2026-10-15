package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/icza/dyno"
	"github.com/redis/go-redis/v9"
)

// redisType is the network the session store dials on. An empty value makes
// redistore.NewRediStore fail with "dial: unknown network", so it defaults.
var redisType = cmp.Or(os.Getenv("REDIS_PROTOCOL"), "tcp")
var redisAddr = os.Getenv("REDIS_ADDR")
var redisPass = os.Getenv("REDIS_PASSWORD")
var rdb redis.UniversalClient

// rdbEvents is the SSE-only client; see init() for why it is separate.
var rdbEvents redis.UniversalClient

type Message struct {
	ID        int          `json:"id" redis:"id"`
	Type      string       `json:"type" redis:"type"`
	Text      string       `json:"text" redis:"text"`
	Author    string       `json:"author" redis:"author"`
	AuthorId  string       `json:"authorId" redis:"authorId"`
	Timestamp time.Time    `json:"timestamp" redis:"timestamp"`
	LastEdit  time.Time    `json:"last_edit" redis:"last_edit"`
	File      FileResponse `json:"file" redis:"-"`
	Deleted   bool         `json:"deleted" redis:"deleted"`
	Views     int          `json:"views" redis:"views"`
	Reactions Reactions    `json:"reactions" redis:"reactions"`
	IsAds     bool         `json:"is_ads" redis:"is_ads"`
}

type User struct {
	ID           string                 `json:"id"`
	Username     string                 `json:"username"`
	Email        string                 `json:"email"`
	PublicName   string                 `json:"publicName"`
	GlobalRole   GlobalRole             `json:"globalRole,omitempty"`
	ChannelRoles map[string]ChannelRole `json:"channelRoles,omitempty"`
}

type PushMessage struct {
	Type string  `json:"type"`
	M    Message `json:"message"`
}

func init() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The data client could take a comma-separated REDIS_ADDR list (cluster)
	// or REDIS_MASTER (Sentinel), but the session store in main.go cannot and
	// refuses to boot with either, so in practice this is one address.
	addrs := strings.Split(redisAddr, ",")
	rdb = redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        addrs,
		Password:     redisPass,
		DB:           0,
		MasterName:   os.Getenv("REDIS_MASTER"),
		PoolSize:     100,
		MinIdleConns: 10,
		MaxRetries:   3,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 5 * time.Second,
	})

	// A second client, used only by the SSE event loop.
	//
	// Every connected viewer sits in a blocking XREAD, and a blocking command
	// holds its pooled connection for the whole block — the loop re-issues
	// immediately, so one viewer occupies one connection essentially all the
	// time. Sharing the main pool meant roughly PoolSize concurrent viewers
	// could consume every connection in the process, at which point logins,
	// uploads and message reads all queued behind them and timed out: a hundred
	// idle readers took the whole platform down.
	//
	// Giving SSE its own pool turns that platform-wide outage into a bounded
	// limit on concurrent viewers, which sseMaxConnections then refuses
	// cleanly. The real fix for viewer scale is one fan-out reader per stream
	// rather than one per viewer; this is the isolation that makes the current
	// design safe to run in the meantime.
	rdbEvents = redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        addrs,
		Password:     redisPass,
		DB:           0,
		MasterName:   os.Getenv("REDIS_MASTER"),
		PoolSize:     sseRedisPoolSize,
		MinIdleConns: 5,
		MaxRetries:   3,
		DialTimeout:  5 * time.Second,
		// Must exceed the XREAD block duration, or every block would surface as
		// a read timeout instead of an empty result.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Second,
	})

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Connection to db failed: %v \n", err)
	}

	log.Println("Connection to DB successful!")
}

// publishEvent appends an event to the channel's Redis Stream (replaces pub/sub).
// Streams allow multiple backend instances to serve SSE without missing events.
// The stream is capped at ~1000 entries to avoid unbounded growth.
func publishEvent(ctx context.Context, slug string, data []byte) {
	if err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: fmt.Sprintf("channel:%s:events", slug),
		MaxLen: 1000,
		Approx: true,
		Values: map[string]interface{}{"data": string(data)},
	}).Err(); err != nil {
		log.Printf("publishEvent: XAdd to channel:%s:events failed: %v\n", slug, err)
	}
}

// touchLastModified bumps a per-channel nano-timestamp used for HTTP ETags.
func touchLastModified(ctx context.Context, slug string) {
	rdb.Set(ctx, fmt.Sprintf("channel:%s:last_modified", slug), time.Now().UnixNano(), 0)
}

func getLastModified(ctx context.Context, slug string) string {
	v, _ := rdb.Get(ctx, fmt.Sprintf("channel:%s:last_modified", slug)).Result()
	return v
}

func getMessageNextId(ctx context.Context, slug string) (int, error) {
	id, err := rdb.Incr(ctx, fmt.Sprintf("channel:%s:message:next_id", slug)).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get next message id for %s: %w", slug, err)
	}

	return int(id), nil
}

func setMessage(ctx context.Context, slug string, m *Message, isUpdate bool) error {
	messageKey := fmt.Sprintf("channel:%s:messages:%d", slug, m.ID)

	// The channel's regex replacements run once, when a message is first
	// published. They must NOT run again on edit: the composer opens with the
	// stored text — which already carries the replacement — so re-applying the
	// same rule wrapped the markup a second time on every save (the documented
	// "**$1**" headline rule turned "**title!**" into "****title!****").
	if !isUpdate {
		settings, err := dbGetSettings(ctx, slug)
		if err == nil && !strings.HasPrefix(m.Text, "[quote-embedded#]") {
			cfg := settings.ToConfig()
			for _, regex := range cfg.RegexReplace {
				m.Text = regex.Pattern.ReplaceAllString(m.Text, regex.Replace)
			}
		}
	}

	// The hash and its index entry go in one transaction: written separately,
	// a failure between the two left an orphan hash that no listing showed,
	// no cleanup reached, and that still counted as existing for reactions
	// and reports.
	if _, err := rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		if isUpdate {
			// Only the fields an edit can change. Writing the whole struct
			// back replayed the views and reactions the handler had read
			// milliseconds earlier over every HINCRBY (addViewsToMessages)
			// and reaction toggle that landed in between, so both counters
			// silently lost increments on each save. The caller still fills
			// them in on m, but only for the SSE payload published below.
			fields := map[string]any{
				"type":      m.Type,
				"text":      m.Text,
				"last_edit": m.LastEdit,
				"deleted":   m.Deleted,
				"is_ads":    m.IsAds,
			}
			// The author travels with the edit only when the caller carries
			// it: updateMessage copies it from the stored record, re-labelled
			// when the record still names an operator (see currentOperators),
			// so a legacy operator post is stored anonymised from its first
			// edit. An update without an author leaves the stored one alone.
			if m.Author != "" {
				fields["author"], fields["authorId"] = m.Author, m.AuthorId
			}
			p.HSet(ctx, messageKey, fields)
		} else {
			p.HSet(ctx, messageKey, m)
		}
		if !isUpdate {
			p.ZAdd(ctx, fmt.Sprintf("channel:%s:m_times", slug), redis.Z{Score: float64(m.Timestamp.Unix()), Member: messageKey})
		}
		return nil
	}); err != nil {
		return err
	}

	pushType := "new-message"
	if isUpdate {
		pushType = "edit-message"
	}

	pushMessage := PushMessage{
		Type: pushType,
		M:    *m,
	}

	pushMessageData, _ := json.Marshal(pushMessage)
	publishEvent(ctx, slug, pushMessageData)
	touchLastModified(ctx, slug)

	return nil
}

func setReaction(ctx context.Context, slug string, messageId int, emoji string, userId string) error {
	// The message must exist and be live before anything is written (the
	// script checks both): an HSET on an invented id would mint an orphan
	// reactions key with no TTL, and a soft-deleted message must not keep
	// collecting votes.
	res, err := toggleReaction.Run(ctx, rdb, []string{
		fmt.Sprintf("channel:%s:message:%d:reactions", slug, messageId),
		fmt.Sprintf("channel:%s:messages:%d", slug, messageId),
	}, userId, emoji).Result()
	if err != nil {
		return fmt.Errorf("failed to set reaction: %v", err)
	}
	encoded, _ := dyno.GetString(res)
	switch encoded {
	case "missing":
		return fmt.Errorf("message %d does not exist", messageId)
	case "deleted":
		return fmt.Errorf("message %d is deleted", messageId)
	}

	var r Reactions
	if encoded != "" && encoded != "{}" {
		if err := json.Unmarshal([]byte(encoded), &r); err != nil {
			return fmt.Errorf("failed to unmarshal reactions: %v", err)
		}
	}

	pushMessage := PushMessage{
		Type: "reaction",
		M: Message{
			ID:        messageId,
			Reactions: r,
		},
	}

	pushMessageData, _ := json.Marshal(pushMessage)
	publishEvent(ctx, slug, pushMessageData)
	// The reaction counts are part of every /messages response, whose ETag is
	// derived from last_modified. Without this bump a reload after reacting
	// revalidated to 304 and the browser showed its cached, pre-reaction
	// counts until something else touched the channel.
	touchLastModified(ctx, slug)

	return nil
}

var getMessageRange = redis.NewScript(`
	local time_set_key = KEYS[1]
	local offset_key = KEYS[2]

	local required_length = tonumber(ARGV[1])
	local isAdmin = ARGV[2] == 'true'
	local countViews = ARGV[3] == 'true'
	local direction = ARGV[4] or 'desc'


	-- ZRANK returns false when the offset key is absent (initial load), which
	-- must start at rank 0; a real rank of 0 must still advance past itself.
	local rank
	if direction == 'asc' then
	    rank = redis.call('ZRANK', time_set_key, offset_key)
	else
	    rank = redis.call('ZREVRANK', time_set_key, offset_key)
	end

	local start_index = 0
	if rank then
		start_index = rank + 1
	end

	local messages = {}
	local max_scan_rounds = 20
	-- Bounding rounds alone is not a bound on work: each round does one HGETALL
	-- per entry, and a soft-deleted entry is not appended for a non-admin
	-- viewer, so #messages stops growing and batch_size stays at the full page
	-- size for every round. A page of 100 over a run of tombstones was 20 x 100
	-- HGETALLs inside one atomic script, and Redis is single-threaded, so every
	-- other tenant waited behind it. Cap the entries examined, not the rounds.
	--
	-- The trade-off, which the old round cap had too at 2000: a run of more
	-- than max_scanned consecutive tombstones exhausts the budget and returns
	-- an empty page, so a client paging through one stops early rather than
	-- reaching the live messages beneath it. Bounding the work is still the
	-- right side to err on — an unbounded scan stalls every tenant, not one
	-- feed. Removing deleted entries from this index (keeping a separate one
	-- for the admin view, which is the only reader that wants them) would make
	-- the scan never pay for tombstones at all and retire the cap.
	local max_scanned = 500
	local scanned = 0
	local scan_rounds = 0
	repeat
		scan_rounds = scan_rounds + 1
		if scan_rounds > max_scan_rounds then break end
		local batch_size = required_length - #messages
		-- Never look at more than the remaining budget allows.
		if batch_size > max_scanned - scanned then
			batch_size = max_scanned - scanned
		end
		if batch_size <= 0 then break end
		-- ZRANGE/ZREVRANGE are inclusive on both ends, so the window is
		-- batch_size wide only when stop is one short of start + batch_size.
		-- Line 'start_index = start_index + batch_size' below then advances to
		-- the next window without re-reading the boundary entry.
		local stop_index = start_index + batch_size - 1
		local message_ids

		if direction == 'asc' then
		 message_ids = redis.call('ZRANGE', time_set_key, start_index, stop_index)
		else
		 message_ids = redis.call('ZREVRANGE', time_set_key, start_index, stop_index)
		end

		if #message_ids == 0 then
			break
		end

		for i, message_key in ipairs(message_ids) do
			local message_data = redis.call('HGETALL', message_key)
			local message = {}

			for j = 1, #message_data, 2 do
				local key = message_data[j]
				local value = message_data[j+1]

				if key == 'id' then
					message[key] = tonumber(value)
                elseif key == 'views' then
					if countViews then
						message[key] = tonumber(value)
					else
						message[key] = 0
					end
				elseif key == 'deleted' then
					message[key] = value == '1'
				elseif key == 'author' then
				    if isAdmin then
				        message[key] = value
				    else
				        message[key] = "Anonymous"
				    end
				elseif key == 'authorId' then
					if isAdmin then
					   message[key] = value
                    else
                       message[key] = "Anonymous"
                    end
				elseif key == 'reactions' then
				    local success, parsedReactions = pcall(cjson.decode, value)
					if success then
						message[key] = parsedReactions
					else
						message[key] = {}
					end
				elseif key == 'is_ads' then
				    message[key] = value == '1'
				else
					message[key] = value
				end
			end

			if not message['deleted'] or isAdmin then
				table.insert(messages, message)
			end
		end

		scanned = scanned + #message_ids
		start_index = start_index + batch_size

	until #messages >= required_length

	return cjson.encode(messages)
`)

func funcGetMessageRange(ctx context.Context, slug string, start, stop int64, isAdmin, countViews bool, direction string) ([]Message, error) {
	offsetKeyName := fmt.Sprintf("channel:%s:messages:%d", slug, start)
	res, err := getMessageRange.Run(ctx, rdb, []string{
		fmt.Sprintf("channel:%s:m_times", slug),
		offsetKeyName,
	}, []string{strconv.FormatInt(stop, 10), strconv.FormatBool(isAdmin), strconv.FormatBool(countViews), direction}).Result()
	if err != nil {
		return []Message{}, err
	}

	if res == "{}" {
		return []Message{}, nil
	}

	var messages []Message
	resStr, _ := dyno.GetString(res)
	if err := json.Unmarshal([]byte(resStr), &messages); err != nil {
		return []Message{}, err
	}

	return messages, nil
}

var sumMessageReactions = redis.NewScript(`
  local reactions = redis.call('HVALS', KEYS[1])
  local result = {}

   for _, reaction in ipairs(reactions) do
   if reaction ~= "" then
    if result[reaction] then
	  result[reaction] = result[reaction] + 1
    else
	  result[reaction] = 1
   end
    end
  end
  return cjson.encode(result)
`)

// toggleReaction is the whole reaction write in one script: existence and
// deleted checks, the per-user toggle, the recount and the message-field
// write. Done as separate round trips, two viewers reacting within
// milliseconds interleaved so the message stored (and every viewer was
// pushed) a stale total, and one user's double-click read the same "no
// previous reaction" twice and never un-reacted.
// KEYS[1] reactions hash, KEYS[2] message hash; ARGV[1] user id, ARGV[2] emoji.
// Returns the encoded totals, "missing" or "deleted".
var toggleReaction = redis.NewScript(`
  if redis.call('EXISTS', KEYS[2]) == 0 then
    return 'missing'
  end
  if redis.call('HGET', KEYS[2], 'deleted') == '1' then
    return 'deleted'
  end
  local prev = redis.call('HGET', KEYS[1], ARGV[1])
  if prev == ARGV[2] then
    redis.call('HSET', KEYS[1], ARGV[1], '')
  else
    redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
  end
  local result = {}
  for _, reaction in ipairs(redis.call('HVALS', KEYS[1])) do
    if reaction ~= '' then
      result[reaction] = (result[reaction] or 0) + 1
    end
  end
  local encoded = cjson.encode(result)
  if encoded == '[]' then
    encoded = '{}'
  end
  redis.call('HSET', KEYS[2], 'reactions', encoded)
  return encoded
`)

func funcGetSumReactions(ctx context.Context, slug string, messageId int) (Reactions, error) {
	res, err := sumMessageReactions.Run(ctx, rdb, []string{
		fmt.Sprintf("channel:%s:message:%d:reactions", slug, messageId),
	}).Result()
	if err != nil || res == nil || res == "{}" {
		return nil, err
	}

	var reactions Reactions
	resStr, _ := dyno.GetString(res)
	if err := json.Unmarshal([]byte(resStr), &reactions); err != nil {
		return nil, fmt.Errorf("failed to unmarshal reactions: %v", err)
	}

	return reactions, nil
}

// dbGetMessageFields returns the raw Redis hash of a message.
// It reports redis.Nil when the message does not exist.
func dbGetMessageFields(ctx context.Context, slug string, id string) (map[string]string, error) {
	fields, err := rdb.HGetAll(ctx, fmt.Sprintf("channel:%s:messages:%s", slug, id)).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, redis.Nil
	}
	return fields, nil
}

func funcDeleteMessage(ctx context.Context, slug string, id string, deletedBy string) error {
	msgKey := fmt.Sprintf("channel:%s:messages:%s", slug, id)
	// The write's error used to be discarded: on a Redis failure the delete
	// event was still broadcast and the handler answered success, and the
	// message came back on the next reload. deletedBy records who took the
	// post down, so a writer cannot undo a moderator's takedown (see
	// updateMessage).
	if err := rdb.HSet(ctx, msgKey, "deleted", true, "deletedBy", deletedBy).Err(); err != nil {
		return err
	}

	var m Message
	idInt, _ := strconv.Atoi(id)
	m.ID = idInt
	m.Deleted = true
	m.LastEdit = time.Now()
	m.Text = "*ההודעה נמחקה*"
	m.File = FileResponse{}

	pushMessage := PushMessage{
		Type: "delete-message",
		M:    m,
	}
	pushMessageData, _ := json.Marshal(pushMessage)
	publishEvent(ctx, slug, pushMessageData)
	touchLastModified(ctx, slug)

	return nil
}

func addViewsToMessages(ctx context.Context, slug string, countViews bool, messages []Message) {
	if !countViews {
		return
	}
	// One pipeline, not one round trip per message. This runs on every page of
	// /messages, so at a page size of 100 it turned the platform's most-read
	// endpoint into 100 sequential writes per read — competing for the same
	// connection pool the rest of the application shares.
	pipe := rdb.Pipeline()
	for _, m := range messages {
		pipe.HIncrBy(ctx, fmt.Sprintf("channel:%s:messages:%d", slug, m.ID), "views", 1)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("addViewsToMessages: %s: recording %d views failed: %v\n", slug, len(messages), err)
	}
}

// https://redis.io/docs/latest/operate/oss_and_stack/management/security/#string-escaping-and-nosql-injection
func addSubscription(slug, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "subscriptions"
	if slug != "" {
		key = fmt.Sprintf("channel:%s:subscriptions", slug)
	}
	_, err := rdb.SAdd(ctx, key, token).Result()
	if err != nil {
		return err
	}

	return nil
}

// countSubscriptions is the size of the channel's token set, read before a
// subscribe so the set can be capped (see maxSubscriptionsPerChannel).
func countSubscriptions(slug string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "subscriptions"
	if slug != "" {
		key = fmt.Sprintf("channel:%s:subscriptions", slug)
	}
	return rdb.SCard(ctx, key).Result()
}

// removeSubscriptions drops tokens FCM has told us are dead (unregistered or
// malformed). Without this the subscription set only ever grows: every expired
// or rotated device token stays forever and every push wastes an FCM slot on
// it. Best effort — a failed prune just means we retry the dead tokens next push.
func removeSubscriptions(slug string, tokens []string) {
	if len(tokens) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "subscriptions"
	if slug != "" {
		key = fmt.Sprintf("channel:%s:subscriptions", slug)
	}
	members := make([]any, len(tokens))
	for i, t := range tokens {
		members[i] = t
	}
	if err := rdb.SRem(ctx, key, members...).Err(); err != nil {
		log.Printf("removeSubscriptions: pruning %d dead tokens from %s failed: %v\n", len(tokens), key, err)
	}
}

func getSubcriptionsList(slug string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "subscriptions"
	if slug != "" {
		key = fmt.Sprintf("channel:%s:subscriptions", slug)
	}
	// SSCAN in pages rather than one SMEMBERS: the set holds every device that
	// ever subscribed to the channel, and a single reply carrying all of it
	// blocked Redis for the duration on every post to a large channel.
	var tokens []string
	iter := rdb.SScan(ctx, key, 0, "", 1000).Iterator()
	for iter.Next(ctx) {
		tokens = append(tokens, iter.Val())
	}
	if err := iter.Err(); err != nil {
		log.Printf("Failed to get subscriptions: %v\n", err)
		return []string{}, err
	}
	return tokens, nil
}

func getChannelDetails(ctx context.Context, slug string) (map[string]string, error) {
	return rdb.HGetAll(ctx, fmt.Sprintf("channel:%s", slug)).Result()
}

func dbSetEmojisList(ctx context.Context, slug string, emojis []string) error {
	emojisJSON, err := json.Marshal(emojis)
	if err != nil {
		return fmt.Errorf("failed to marshal emojis: %v", err)
	}

	key := fmt.Sprintf("channel:%s:emojis:list", slug)
	if err := rdb.Set(ctx, key, emojisJSON, 0).Err(); err != nil {
		return fmt.Errorf("failed to set emojis in db: %v", err)
	}

	return nil
}

func dbGetEmojisList(ctx context.Context, slug string) ([]string, error) {
	key := fmt.Sprintf("channel:%s:emojis:list", slug)
	emojisJSON, err := rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to get emojis from db: %v", err)
	}

	var emojisList []string
	if err := json.Unmarshal([]byte(emojisJSON), &emojisList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal emojis: %v", err)
	}

	return emojisList, nil
}

func dbGetUsersList(ctx context.Context) ([]User, error) {
	u, err := rdb.Get(ctx, "users:list").Result()
	if err != nil {
		if err == redis.Nil {
			return []User{}, nil
		}
		return nil, err
	}
	var usersList []User
	err = json.Unmarshal([]byte(u), &usersList)
	if err != nil {
		return nil, err
	}

	return usersList, nil
}

// dbUpdateUsersList applies mutate to the whole users:list blob under a
// WATCH/MULTI, retrying if another writer got there first. Every role change is
// a read-modify-write of one JSON string, so doing it unguarded means two
// concurrent administrative edits silently discard one another.
func dbUpdateUsersList(ctx context.Context, mutate func([]User) []User) error {
	// Same budget and backoff as dbUpdateSupportTicket: five back-to-back
	// retries with no pause were exhausted by a handful of simultaneous
	// privileged logins re-colliding in lockstep, and each loser's login
	// answered 500. The jitter spreads the contenders apart.
	const maxRetries = 20

	txf := func(tx *redis.Tx) error {
		var users []User
		u, err := tx.Get(ctx, "users:list").Result()
		if err != nil && err != redis.Nil {
			return err
		}
		if err == nil {
			if err := json.Unmarshal([]byte(u), &users); err != nil {
				return err
			}
		}

		data, err := json.Marshal(mutate(users))
		if err != nil {
			return err
		}

		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, "users:list", data, 0)
			return nil
		})
		return err
	}

	for i := 0; i < maxRetries; i++ {
		err := rdb.Watch(ctx, txf, "users:list")
		if err != redis.TxFailedErr {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(rand.Int63n(int64(2*time.Millisecond))) + time.Millisecond):
		}
	}
	return errors.New("users:list: too much contention")
}

func dbSetSettings(ctx context.Context, slug string, settings *Settings) error {
	jsonSettings, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %v", err)
	}

	key := fmt.Sprintf("channel:%s:settings", slug)
	if err := rdb.Set(ctx, key, jsonSettings, 0).Err(); err != nil {
		return fmt.Errorf("failed to set settings in db: %v", err)
	}

	return nil
}

func dbGetSettings(ctx context.Context, slug string) (Settings, error) {
	key := fmt.Sprintf("channel:%s:settings", slug)
	settingsJSON, err := rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return Settings{}, nil
		}
		return nil, fmt.Errorf("failed to get settings from db: %v", err)
	}

	var settings Settings
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %v", err)
	}

	return settings, nil
}

// Global settings (FCM/VAPID) stored under global:settings
func dbSetGlobalSettings(ctx context.Context, settings *Settings) error {
	jsonSettings, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal global settings: %v", err)
	}

	if err := rdb.Set(ctx, "global:settings", jsonSettings, 0).Err(); err != nil {
		return fmt.Errorf("failed to set global settings in db: %v", err)
	}

	return nil
}

func dbGetGlobalSettings(ctx context.Context) (Settings, error) {
	settingsJSON, err := rdb.Get(ctx, "global:settings").Result()
	if err != nil {
		if err == redis.Nil {
			// Fallback to legacy settings:list key
			settingsJSON2, err2 := rdb.Get(ctx, "settings:list").Result()
			if err2 != nil {
				if err2 == redis.Nil {
					return Settings{}, nil
				}
				return nil, fmt.Errorf("failed to get global settings from db: %v", err2)
			}
			settingsJSON = settingsJSON2
		} else {
			return nil, fmt.Errorf("failed to get global settings from db: %v", err)
		}
	}

	var settings Settings
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal global settings: %v", err)
	}

	return settings, nil
}

func dbGetUsersAmount(ctx context.Context, slug string) (int64, error) {
	key := fmt.Sprintf("channel:%s:registered_emails", slug)
	amount, err := rdb.SCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get users amount: %v", err)
	}
	return amount, nil
}

func getReportNextID(ctx context.Context, slug string) (int64, error) {
	return rdb.Incr(ctx, fmt.Sprintf("channel:%s:report:next_id", slug)).Result()
}

func dbReportMessage(ctx context.Context, slug string, report *Report) error {
	id, err := getReportNextID(ctx, slug)
	if err != nil {
		return err
	}
	reportKey := fmt.Sprintf("channel:%s:report:%d", slug, id)
	report.Id = id

	if err := rdb.HSet(ctx, reportKey, report).Err(); err != nil {
		return err
	}

	reportsListKey := fmt.Sprintf("channel:%s:reports:list", slug)
	reportsOpenKey := fmt.Sprintf("channel:%s:reports:open", slug)

	if err := rdb.ZAdd(ctx, reportsListKey, redis.Z{Score: float64(report.CreatedAt.Unix()), Member: reportKey}).Err(); err != nil {
		return err
	}

	if err := rdb.ZAdd(ctx, reportsOpenKey, redis.Z{Score: float64(report.CreatedAt.Unix()), Member: reportKey}).Err(); err != nil {
		return err
	}

	return nil
}

var getReportsScript = redis.NewScript(`
	local status = ARGV[1]
	local limit = tonumber(ARGV[2])

	local reportsStatusKey
	if status == 'open' then
		reportsStatusKey = KEYS[2]
	elseif status == 'all' then
		reportsStatusKey = KEYS[1]
	elseif status == 'closed' then
		reportsStatusKey = KEYS[3]
	end

	local reports = redis.call('ZREVRANGE', reportsStatusKey, 0, limit - 1)

	local result = {}
	for _, reportKey in ipairs(reports) do
		local report = redis.call('HGETALL', reportKey)

		local reportTable = {}
		for i = 1, #report, 2 do
		  local key = report[i]
		  local value = report[i + 1]

		   if key == 'messageId' then
			 reportTable[key] = tonumber(value)
		   elseif key == 'closed' then
			 reportTable[key] = value == '1'
		   elseif key == 'id' then
			 reportTable[key] = tonumber(value)
		   else
		     reportTable[key] = value
		   end

		end
		table.insert(result, reportTable)
	end

	return cjson.encode(result)
`)

func dbGetReports(ctx context.Context, slug string, status ReportStatus) (Reports, error) {
	listKey := fmt.Sprintf("channel:%s:reports:list", slug)
	openKey := fmt.Sprintf("channel:%s:reports:open", slug)
	closedKey := fmt.Sprintf("channel:%s:reports:closed", slug)

	jsonReports, err := getReportsScript.Run(ctx, rdb, []string{listKey, openKey, closedKey}, []string{string(status), "100"}).Result()
	if err != nil {
		return nil, err
	}

	// The handler encodes this value straight to the client, and a nil slice
	// serialises as null, which the reports screen does not accept.
	reports := make(Reports, 0)
	if jsonReports == nil || jsonReports == "{}" {
		return reports, nil
	}

	resStr, _ := dyno.GetString(jsonReports)
	if resStr == "" {
		return reports, nil
	}

	if err := json.Unmarshal([]byte(resStr), &reports); err != nil {
		return nil, err
	}
	if reports == nil {
		reports = make(Reports, 0)
	}

	return reports, nil
}

func dbSetReports(ctx context.Context, slug string, report *Report) error {
	reportKey := fmt.Sprintf("channel:%s:report:%d", slug, report.Id)
	openKey := fmt.Sprintf("channel:%s:reports:open", slug)
	closedKey := fmt.Sprintf("channel:%s:reports:closed", slug)

	// The report must already exist: HSET below would otherwise fabricate a
	// phantom hash and index it under open/closed while reports:list (which only
	// dbReportMessage writes) never learns about it — a permanent desync.
	// messageId is always written by dbReportMessage's full-struct HSet.
	exists, err := rdb.HExists(ctx, reportKey, "messageId").Result()
	if err != nil {
		return err
	}
	if !exists {
		return redis.Nil
	}

	switch report.Closed {
	case true:
		if err := rdb.ZRem(ctx, openKey, reportKey).Err(); err != nil {
			return err
		}
		if err := rdb.ZAdd(ctx, closedKey, redis.Z{Score: float64(report.UpdatedAt.Unix()), Member: reportKey}).Err(); err != nil {
			return err
		}
	case false:
		if err := rdb.ZRem(ctx, closedKey, reportKey).Err(); err != nil {
			return err
		}
		if err := rdb.ZAdd(ctx, openKey, redis.Z{Score: float64(report.UpdatedAt.Unix()), Member: reportKey}).Err(); err != nil {
			return err
		}
	}

	if err := rdb.HSet(ctx, reportKey, "closed", report.Closed, "updatedAt", report.UpdatedAt).Err(); err != nil {
		return err
	}

	return nil
}

// peakSSEMaxWrite persists a peak only when it exceeds the stored one, so a
// replica with a small local count can never overwrite a larger recorded peak,
// and clearing the key (resetStatistics) is a complete cross-replica reset.
var peakSSEMaxWrite = redis.NewScript(`
	local cur = tonumber(redis.call('HGET', KEYS[1], 'value') or '0')
	if tonumber(ARGV[1]) > cur then
		redis.call('HSET', KEYS[1], 'value', ARGV[1], 'timestamp', ARGV[2])
		return 1
	end
	return 0
`)

func dbSavePeakSSEConnections(slug string, peak *PeakSSEConnections) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("channel:%s:peak_sse_connections", slug)
	peakSSEMaxWrite.Run(ctx, rdb, []string{key},
		strconv.FormatInt(peak.Value, 10), strconv.FormatInt(peak.Timestamp.Unix(), 10))
}

func dbGetPeakSSEConnections(ctx context.Context, slug string) (*PeakSSEConnections, error) {
	key := fmt.Sprintf("channel:%s:peak_sse_connections", slug)
	p, err := rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	// No record (a new channel, or right after resetStatistics) is a zero
	// value with a zero time. time.Unix(0, 0) here was a real 1970 timestamp,
	// which the statistics page rendered as a peak of 0 users on 01/01/1970.
	var peak PeakSSEConnections
	if len(p) == 0 {
		return &peak, nil
	}
	vel, _ := dyno.GetInteger(p["value"])
	timestamp, _ := dyno.GetInteger(p["timestamp"])

	peak.Value = vel
	if timestamp > 0 {
		peak.Timestamp = time.Unix(timestamp, 0)
	}

	return &peak, nil
}

// sseStatisticsMaxPoints bounds one channel-month series. dbGetSSEStatistics
// reads at most 1000 points back, so keeping more only costs memory.
const sseStatisticsMaxPoints = 1000

// sseStatisticsTTL retires a channel-month series well after the month it
// covers, so an abandoned channel cannot leave keys behind for ever.
const sseStatisticsTTL = 400 * 24 * time.Hour

// sseStatisticsKeysFor returns every monthly SSE-statistics key a channel could
// hold, over the retention window. These keys have no index, so both channel
// deletion and the statistics reset enumerate the month/year space rather than
// SCAN the keyspace. It is centralised here so the enumeration cannot drift from
// the write-side format in dbSaveSSEStatistics.
func sseStatisticsKeysFor(slug string) []string {
	currentYear := time.Now().Year()
	keys := make([]string, 0, 12*11)
	for year := currentYear - 10; year <= currentYear; year++ {
		for month := time.January; month <= time.December; month++ {
			keys = append(keys, fmt.Sprintf("channel:%s:sse_statistics:%d:%d", slug, month, year))
		}
	}
	return keys
}

func dbSaveSSEStatistics(slug string, amount int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := fmt.Sprintf("channel:%s:sse_statistics:%d:%d", slug, time.Now().Month(), time.Now().Year())
	member := fmt.Sprintf("%d&%s", amount, time.Now().Format("02-01-2006 15:04"))

	// A member is added on every connect and every disconnect, and distinct
	// counts within the same minute are distinct members — so on a busy channel
	// with churning mobile clients this series grew without bound, for every
	// channel, with nothing but a channel delete or a statistics reset ever
	// removing it. Trim to the most recent entries and expire the key, so the
	// series stays a rolling window rather than a permanent leak. Only the last
	// 1000 are ever read back (dbGetSSEStatistics).
	pipe := rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(time.Now().Unix()), Member: member})
	pipe.ZRemRangeByRank(ctx, key, 0, -(sseStatisticsMaxPoints + 1))
	pipe.Expire(ctx, key, sseStatisticsTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("dbSaveSSEStatistics: %s: recording connection count failed: %v\n", slug, err)
	}
}

func dbGetSSEStatistics(ctx context.Context, slug string, length int64) (*Statistics, error) {
	key := fmt.Sprintf("channel:%s:sse_statistics:%d:%d", slug, time.Now().Month(), time.Now().Year())
	result := &Statistics{
		Data:   []int64{},
		Labels: []string{},
	}

	r, err := rdb.ZRevRangeWithScores(ctx, key, 0, length-1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get SSE statistics: %v", err)
	}

	if len(r) == 0 {
		return result, nil
	}

	for _, item := range r {
		itemMember, _ := dyno.GetString(item.Member)
		p := strings.Split(itemMember, "&")
		if len(p) != 2 {
			continue
		}
		val, _ := dyno.GetInteger(p[0])
		result.Data = append(result.Data, val)
		result.Labels = append(result.Labels, p[1])
	}

	return result, nil
}

func dbSaveScheduledMessages(ctx context.Context, slug string, messages *[]Message) error {
	jsonMessages, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal scheduled messages: %v", err)
	}

	key := fmt.Sprintf("channel:%s:scheduled_messages:list", slug)
	if err := rdb.Set(ctx, key, jsonMessages, 0).Err(); err != nil {
		return fmt.Errorf("failed to set scheduled messages in db: %v", err)
	}

	// Maintain a global sorted set of "next due time" per channel so the
	// scheduler only wakes channels that actually have pending messages.
	// A zero time.Time scores far below any real timestamp, so including it
	// would win the minimum, fail the earliest > 0 test and drop the channel
	// from due_channels entirely — silently stalling its other pending posts.
	var earliest float64
	for _, m := range *messages {
		ts := float64(m.Timestamp.Unix())
		if ts <= 0 {
			continue
		}
		if earliest == 0 || ts < earliest {
			earliest = ts
		}
	}
	// The index write must be reported: the list is already saved, so a lost
	// ZADD strands a due message that the scheduler never wakes for, while the
	// writer was told the save succeeded.
	if earliest > 0 {
		if err := rdb.ZAdd(ctx, "scheduled:due_channels", redis.Z{Score: earliest, Member: slug}).Err(); err != nil {
			return fmt.Errorf("index scheduled messages of %s: %w", slug, err)
		}
	} else if err := rdb.ZRem(ctx, "scheduled:due_channels", slug).Err(); err != nil {
		return fmt.Errorf("unindex scheduled messages of %s: %w", slug, err)
	}

	return nil
}

func dbGetScheduledMessages(ctx context.Context, slug string) (*[]Message, error) {
	key := fmt.Sprintf("channel:%s:scheduled_messages:list", slug)
	messagesJSON, err := rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return &[]Message{}, nil
		}
		return nil, fmt.Errorf("failed to get scheduled messages from db: %v", err)
	}

	var messages []Message
	if err := json.Unmarshal([]byte(messagesJSON), &messages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal scheduled messages: %v", err)
	}

	return &messages, nil
}

// Channel CRUD functions

func dbChannelExists(ctx context.Context, slug string) (bool, error) {
	n, err := rdb.Exists(ctx, fmt.Sprintf("channel:%s", slug)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// errChannelExists reports that the slug was already claimed. Callers must map
// it to 409 rather than 500.
var errChannelExists = errors.New("channel already exists")

func dbCreateChannel(ctx context.Context, channel *ChannelData) error {
	hashKey := fmt.Sprintf("channel:%s", channel.Slug)

	// Claim the slug atomically. A plain HSet lets two concurrent creates for the
	// same slug both succeed, overwriting one channel's metadata and granting two
	// owners; the caller's prior existence check cannot prevent that on its own.
	claimed, err := rdb.HSetNX(ctx, hashKey, "slug", channel.Slug).Result()
	if err != nil {
		return err
	}
	if !claimed {
		return errChannelExists
	}

	// The claim above is what makes the slug unavailable, so anything that fails
	// after it has to release the claim or the slug stays wedged forever.
	releaseClaim := func() {
		rdb.Del(context.WithoutCancel(ctx), hashKey)
	}

	if err := rdb.HSet(ctx, hashKey,
		"name", channel.Name,
		"description", channel.Description,
		"logoUrl", channel.LogoUrl,
		"ownerEmail", channel.OwnerEmail,
		"createdAt", channel.CreatedAt.Format(time.RFC3339),
		"contactUs", channel.ContactUs,
	).Err(); err != nil {
		releaseClaim()
		return err
	}

	featuresJSON, err := json.Marshal(channel.Features)
	if err != nil {
		releaseClaim()
		return fmt.Errorf("failed to marshal features: %v", err)
	}

	featuresKey := fmt.Sprintf("channel:%s:features", channel.Slug)
	if err := rdb.Set(ctx, featuresKey, featuresJSON, 0).Err(); err != nil {
		releaseClaim()
		return err
	}

	if err := rdb.ZAdd(ctx, "channels:list", redis.Z{
		Score:  float64(channel.CreatedAt.Unix()),
		Member: channel.Slug,
	}).Err(); err != nil {
		releaseClaim()
		return err
	}

	return nil
}

func dbGetChannel(ctx context.Context, slug string) (*ChannelData, error) {
	hashKey := fmt.Sprintf("channel:%s", slug)
	h, err := rdb.HGetAll(ctx, hashKey).Result()
	if err != nil {
		return nil, err
	}

	featuresKey := fmt.Sprintf("channel:%s:features", slug)
	featuresJSON, err := rdb.Get(ctx, featuresKey).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("load features for %s: %w", slug, err)
	}
	return channelFromRecords(slug, h, featuresJSON, err == nil)
}

// channelFromRecords assembles a ChannelData from its two Redis records, so
// the single-channel read and the pipelined list read cannot drift apart.
// hasFeatures is false when the features key does not exist.
func channelFromRecords(slug string, h map[string]string, featuresJSON string, hasFeatures bool) (*ChannelData, error) {
	if len(h) == 0 {
		return nil, redis.Nil
	}

	channel := &ChannelData{
		Slug:        h["slug"],
		Name:        h["name"],
		Description: h["description"],
		LogoUrl:     h["logoUrl"],
		OwnerEmail:  h["ownerEmail"],
		ContactUs:   h["contactUs"],

		SeoHidden:      h["seoHidden"] == "1",
		SeoTitle:       h["seoTitle"],
		SeoDescription: h["seoDescription"],
	}
	if h["slug"] == "" {
		channel.Slug = slug
	}

	if t, err := time.Parse(time.RFC3339, h["createdAt"]); err == nil {
		channel.CreatedAt = t
	}

	// A failed features read must surface rather than silently serving a
	// zero-valued ChannelFeatures: RequireAuth/RequireAuthFiles defaulting to
	// false on a transient error would fail open on an auth toggle. Only a
	// genuinely absent key keeps the zero value.
	if hasFeatures {
		var features ChannelFeatures
		if err := json.Unmarshal([]byte(featuresJSON), &features); err != nil {
			return nil, fmt.Errorf("unmarshal features for %s: %w", slug, err)
		}
		channel.Features = features
	}

	return channel, nil
}

// dbListChannelsBatch bounds one pipeline so a platform with tens of
// thousands of channels never builds a single multi-megabyte reply.
const dbListChannelsBatch = 500

func dbListChannels(ctx context.Context) ([]*ChannelData, error) {
	slugs, err := rdb.ZRange(ctx, "channels:list", 0, -1).Result()
	if err != nil {
		return nil, err
	}

	// Never nil: an empty platform must list as [] and not as JSON null, which
	// the super-admin screen does not treat as a list.
	channels := make([]*ChannelData, 0, len(slugs))

	// Pipelined: the two records per channel used to be read one channel at a
	// time, so the super-admin list, the statistics reset and the ads/magnet
	// lock sync all cost two round trips per channel and ran out of their
	// handler budgets somewhere in the low thousands of channels.
	for start := 0; start < len(slugs); start += dbListChannelsBatch {
		end := min(start+dbListChannelsBatch, len(slugs))
		batch := slugs[start:end]

		pipe := rdb.Pipeline()
		hashes := make([]*redis.MapStringStringCmd, len(batch))
		features := make([]*redis.StringCmd, len(batch))
		for i, slug := range batch {
			hashes[i] = pipe.HGetAll(ctx, fmt.Sprintf("channel:%s", slug))
			features[i] = pipe.Get(ctx, fmt.Sprintf("channel:%s:features", slug))
		}
		// Exec reports the first failed command, and a missing features key
		// is a redis.Nil "failure" here; only a real error aborts the list.
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, err
		}

		for i, slug := range batch {
			h, err := hashes[i].Result()
			if err != nil {
				log.Printf("Failed to get channel %s: %v\n", slug, err)
				continue
			}
			featuresJSON, err := features[i].Result()
			if err != nil && err != redis.Nil {
				log.Printf("Failed to get channel %s: load features: %v\n", slug, err)
				continue
			}
			ch, err := channelFromRecords(slug, h, featuresJSON, err == nil)
			if err != nil {
				log.Printf("Failed to get channel %s: %v\n", slug, err)
				continue
			}
			channels = append(channels, ch)
		}
	}

	return channels, nil
}

func dbDeleteChannel(ctx context.Context, slug string) error {
	p := slug

	// Step 0: revoke every per-channel role for this slug FIRST. This used to
	// be the last step, after the keys were gone: if it failed (or the 60s
	// budget ran out on a channel with many uploads), the channel was already
	// deleted but the grants survived, and recreating the same slug handed the
	// old grantees their access back. Revoking first means a failure further
	// down leaves a channel that still exists and can simply be deleted again.
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		for i := range users {
			delete(users[i].ChannelRoles, slug) // delete on a nil map is a no-op
		}
		return users
	}); err != nil {
		return fmt.Errorf("clear roles for %s: %w", slug, err)
	}
	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after dbDeleteChannel: %v\n", err)
	}

	// Step 0b: fence the channel. Revoking roles does not stop every writer:
	// the API-key import path authenticates with the channel's secret, not a
	// role, and a super admin passes hasChannelRole outright — so until the
	// hash went in the final pipeline both could still post during the
	// (long) file sweep below and leave an unindexed message hash behind.
	// Disabled is the one flag channelMiddleware and addNewPost both refuse
	// on. A delete that fails past this point leaves a disabled channel,
	// which is the safer state: the operator deletes it again (deleteChannel
	// checks existence only) or re-enables it from the features form.
	if err := dbUpdateChannelFeatures(ctx, slug, func(f *ChannelFeatures) { f.Disabled = true }); err != nil {
		return fmt.Errorf("fence %s: %w", slug, err)
	}
	// Tell the hubs once more, now that the fence is up. deleteChannel
	// published the control event before calling here, but until the fence
	// channelMiddleware still admitted /events: a viewer whose stream had
	// just been closed reconnected and built a fresh hub — on another replica
	// one that no stop reaches — which then never saw a control event, sat on
	// the deleted key, and fanned the events of whatever tenant re-created
	// the slug to viewers who never passed that channel's middleware. Any hub
	// built in the window reads this entry and retires; a second control
	// event is a no-op for a client and a hub that already got the first.
	// The entry goes with the stream in the final pipeline.
	publishEvent(ctx, slug, []byte(sseChannelGoneEvent))

	// Step 1: release every uploaded file through the normal delete path so
	// hash refcounts are decremented and the R2/disk blob is removed once the
	// last reference is gone. The tracking set is NOT deleted up front: it is
	// the only record of which blobs still need cleanup, so destroying it before
	// the loop finishes would orphan every remaining blob permanently if the
	// context expires or the process dies mid-delete. The per-file index removal
	// is skipped instead (removeFromIndex=false) to keep the loop O(N); the set
	// itself is dropped with the fixed keys below once the loop completes.
	//
	// Two passes over the set: the fence above refuses new uploads, but one
	// admitted just before it can still index its file while this sweep runs
	// (an R2 write has a minute's budget), and a single snapshot left that
	// blob and its reference count orphaned when the set was dropped. The
	// second pass re-reads the set and releases only what the first missed.
	fileKeys := make([]string, 0)
	swept := make(map[string]bool)
	for pass := 0; pass < 2; pass++ {
		fileMembers, err := rdb.ZRange(ctx, channelFilesKey(p), 0, -1).Result()
		if err != nil {
			return fmt.Errorf("list files of %s: %w", slug, err)
		}
		unreleased := 0
		for _, m := range fileMembers {
			if err := ctx.Err(); err != nil {
				return err
			}
			fileID, _, ok := decodeFileMember(m)
			if !ok || swept[fileID] {
				continue
			}
			swept[fileID] = true
			if len(fileID) >= 4 {
				// A file that could not be released (a failed read or claim)
				// keeps its record and its index: deleting them regardless
				// orphaned the blob and its reference count for good, behind a
				// success response. The channel survives and is deleted again.
				if _, released := deleteFileByID(ctx, p, fileID, false); !released {
					unreleased++
					continue
				}
			}
			fileKeys = append(fileKeys, "file:"+fileID)
		}
		if unreleased > 0 {
			return fmt.Errorf("%d of %d files of %s could not be released; delete the channel again", unreleased, len(fileMembers), slug)
		}
	}

	// Step 1b: collect all message keys and reaction keys from the sorted set
	// (avoids SCAN). Every index read below must succeed before anything is
	// deleted: an ignored error left the slice empty, Step 2 then dropped the
	// index itself, and every message, reaction, report hash and — worst —
	// every file blob and hash refcount it pointed at was orphaned for good,
	// while the handler still reported success.
	//
	// Taken after the file sweep, not before it: a post admitted before the
	// fence can land while the sweep runs, and a snapshot from before it left
	// that message hash behind when the index was deleted — to resurface on a
	// re-created slug.
	messageKeys, err := rdb.ZRange(ctx, fmt.Sprintf("channel:%s:m_times", p), 0, -1).Result()
	if err != nil {
		return fmt.Errorf("list messages of %s: %w", slug, err)
	}
	reactionKeys := make([]string, 0, len(messageKeys))
	reporterKeys := make([]string, 0, len(messageKeys))
	for _, mk := range messageKeys {
		// mk is already the full key e.g. "channel:{slug}:messages:{id}"
		// derive reaction key from it by replacing "messages:" prefix with "message:" and appending ":reactions"
		id := strings.TrimPrefix(mk, fmt.Sprintf("channel:%s:messages:", p))
		reactionKeys = append(reactionKeys, fmt.Sprintf("channel:%s:message:%s:reactions", p, id))
		// The per-message report-dedup set lives under the same message id.
		reporterKeys = append(reporterKeys, fmt.Sprintf("channel:%s:message:%s:reporters", p, id))
	}

	// Step 1c: collect the individual report hashes referenced by the reports index
	reportKeys, err := rdb.ZRange(ctx, fmt.Sprintf("channel:%s:reports:list", p), 0, -1).Result()
	if err != nil {
		return fmt.Errorf("list reports of %s: %w", slug, err)
	}

	// Step 1d: monthly SSE statistics keys have no index; enumerate the possible
	// month/year combinations instead of running a SCAN across the keyspace.
	statsKeys := sseStatisticsKeysFor(p)

	// Step 2: delete all known fixed keys in one pipeline
	fixedKeys := []string{
		fmt.Sprintf("channel:%s", p),
		fmt.Sprintf("channel:%s:settings", p),
		fmt.Sprintf("channel:%s:features", p),
		fmt.Sprintf("channel:%s:m_times", p),
		fmt.Sprintf("channel:%s:message:next_id", p),
		fmt.Sprintf("channel:%s:subscriptions", p),
		fmt.Sprintf("channel:%s:registered_emails", p),
		fmt.Sprintf("channel:%s:emojis:list", p),
		fmt.Sprintf("channel:%s:reports:list", p),
		fmt.Sprintf("channel:%s:reports:open", p),
		fmt.Sprintf("channel:%s:reports:closed", p),
		fmt.Sprintf("channel:%s:report:next_id", p),
		fmt.Sprintf("channel:%s:scheduled_messages:list", p),
		fmt.Sprintf("channel:%s:scheduled:dispatched", p),
		fmt.Sprintf("channel:%s:peak_sse_connections", p),
		fmt.Sprintf("channel:%s:storage:used_bytes", p),
		fmt.Sprintf("channel:%s:storage:quota_bytes", p),
		fmt.Sprintf("channel:%s:storage:auto_cleanup", p),
		fmt.Sprintf("channel:%s:files", p),
		fmt.Sprintf("channel:%s:sse_connections", p),
		fmt.Sprintf("channel:%s:events", p),
		fmt.Sprintf("channel:%s:last_modified", p),
		channelMagnetStatsKeyKey(p),
	}
	allKeys := append(fixedKeys, messageKeys...)
	allKeys = append(allKeys, reactionKeys...)
	allKeys = append(allKeys, reporterKeys...)
	allKeys = append(allKeys, fileKeys...)
	allKeys = append(allKeys, reportKeys...)
	allKeys = append(allKeys, statsKeys...)

	pipe := rdb.Pipeline()
	// Delete in batches of 200 to avoid huge single commands
	for i := 0; i < len(allKeys); i += 200 {
		end := i + 200
		if end > len(allKeys) {
			end = len(allKeys)
		}
		pipe.Del(ctx, allKeys[i:end]...)
	}
	pipe.ZRem(ctx, "channels:list", slug)
	pipe.ZRem(ctx, "scheduled:due_channels", slug)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	return nil
}

func dbSetChannelFeatures(ctx context.Context, slug string, features *ChannelFeatures) error {
	featuresJSON, err := json.Marshal(features)
	if err != nil {
		return fmt.Errorf("failed to marshal features: %v", err)
	}

	featuresKey := fmt.Sprintf("channel:%s:features", slug)
	if err := rdb.Set(ctx, featuresKey, featuresJSON, 0).Err(); err != nil {
		return err
	}

	return nil
}

// dbUpdateChannelFeatures applies mutate to the channel's stored features
// under WATCH/MULTI, retrying when another writer got in between. The
// features blob is one JSON string with several independent writers (the
// super-admin features form, the ads and magnet lock syncs), and a blind
// read-modify-SET from any of them replayed a stale snapshot over the others':
// a channel disabled while a lock sync was running came back enabled.
func dbUpdateChannelFeatures(ctx context.Context, slug string, mutate func(*ChannelFeatures)) error {
	featuresKey := fmt.Sprintf("channel:%s:features", slug)
	const maxRetries = 10
	for i := 0; i < maxRetries; i++ {
		err := rdb.Watch(ctx, func(tx *redis.Tx) error {
			var features ChannelFeatures
			raw, err := tx.Get(ctx, featuresKey).Result()
			if err != nil && err != redis.Nil {
				return err
			}
			if err == nil {
				if err := json.Unmarshal([]byte(raw), &features); err != nil {
					return fmt.Errorf("unmarshal features for %s: %w", slug, err)
				}
			}
			mutate(&features)
			out, err := json.Marshal(&features)
			if err != nil {
				return fmt.Errorf("failed to marshal features: %v", err)
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, featuresKey, out, 0)
				return nil
			})
			return err
		}, featuresKey)
		if err == redis.TxFailedErr {
			continue
		}
		return err
	}
	return fmt.Errorf("update features for %s: too many concurrent writers", slug)
}

func dbAssignChannelRole(ctx context.Context, email, slug string, role ChannelRole) error {
	email = normEmail(email)
	return dbUpdateUsersList(ctx, func(users []User) []User {
		for i, u := range users {
			if normEmail(u.Email) == email {
				if users[i].ChannelRoles == nil {
					users[i].ChannelRoles = make(map[string]ChannelRole)
				}
				users[i].ChannelRoles[slug] = role
				return users
			}
		}
		return append(users, User{
			Email:        email,
			ChannelRoles: map[string]ChannelRole{slug: role},
		})
	})
}

// GlobalMagnetConfig stored at global:magnet:config
type GlobalMagnetConfig struct {
	Enabled              bool     `json:"enabled"`
	Snippet              string   `json:"snippet"`
	Mode                 string   `json:"mode"`
	PerMessages          int64    `json:"perMessages"`
	MinTimeSeconds       int64    `json:"minTimeSeconds"`
	PerSeconds           int64    `json:"perSeconds"`
	MinMessagesSinceLast int64    `json:"minMessagesSinceLast"`
	ApiKey               string   `json:"apiKey"`
	LockAll              bool     `json:"lockAll"`        // override ALL channels
	LockedChannels       []string `json:"lockedChannels"` // override specific channels
	// StatsKey is the Magnet server-to-server stats secret (mgs_…). It is
	// stored here and sent only to Magnet; no handler may serialize it —
	// GET/POST /magnet/config answer with magnetConfigView instead.
	StatsKey string `json:"statsKey"`
}

func dbGetGlobalMagnetConfig(ctx context.Context) (*GlobalMagnetConfig, error) {
	data, err := rdb.Get(ctx, "global:magnet:config").Result()
	if err != nil {
		if err == redis.Nil {
			return &GlobalMagnetConfig{}, nil
		}
		return nil, err
	}
	var cfg GlobalMagnetConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func dbSetGlobalMagnetConfig(ctx context.Context, cfg *GlobalMagnetConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, "global:magnet:config", data, 0).Err()
}

// channelMagnetStatsKeyKey holds an unlocked channel's own Magnet stats key.
// It lives outside channel:<slug>:settings on purpose: settings/get hands that
// whole blob to the owner's browser, and this secret must never reach one.
func channelMagnetStatsKeyKey(slug string) string {
	return fmt.Sprintf("channel:%s:magnet:stats_key", slug)
}

// dbGetChannelMagnetStatsKey returns "" when the channel has none.
func dbGetChannelMagnetStatsKey(ctx context.Context, slug string) (string, error) {
	v, err := rdb.Get(ctx, channelMagnetStatsKeyKey(slug)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

// dbSetChannelMagnetStatsKey stores key, or removes it when key is "".
func dbSetChannelMagnetStatsKey(ctx context.Context, slug, key string) error {
	if key == "" {
		return rdb.Del(ctx, channelMagnetStatsKeyKey(slug)).Err()
	}
	return rdb.Set(ctx, channelMagnetStatsKeyKey(slug), key, 0).Err()
}

// GlobalAdsConfig stored at global:ads:config
type GlobalAdsConfig struct {
	Src            string   `json:"src"`
	Width          int64    `json:"width"`
	LockAll        bool     `json:"lockAll"`        // override ALL channels
	LockedChannels []string `json:"lockedChannels"` // override specific channels
}

func dbGetGlobalAdsConfig(ctx context.Context) (*GlobalAdsConfig, error) {
	data, err := rdb.Get(ctx, "global:ads:config").Result()
	if err != nil {
		if err == redis.Nil {
			return &GlobalAdsConfig{}, nil
		}
		return nil, err
	}
	var cfg GlobalAdsConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func dbSetGlobalAdsConfig(ctx context.Context, cfg *GlobalAdsConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, "global:ads:config", data, 0).Err()
}

// ─── Storage Quota & Usage ────────────────────────────────────────────────────

const defaultStorageQuotaBytes = int64(5 * 1024 * 1024 * 1024) // 5 GB

func dbGetGlobalStorageQuota(ctx context.Context) (int64, error) {
	v, err := rdb.Get(ctx, "global:storage:quota_bytes").Int64()
	if err != nil {
		if err == redis.Nil {
			return defaultStorageQuotaBytes, nil
		}
		return 0, err
	}
	return v, nil
}

func dbSetGlobalStorageQuota(ctx context.Context, bytes int64) error {
	return rdb.Set(ctx, "global:storage:quota_bytes", bytes, 0).Err()
}

// dbGetChannelStorageQuota returns the quota for a channel.
// 0 means "use global default".
func dbGetChannelStorageQuota(ctx context.Context, slug string) (int64, error) {
	v, err := rdb.Get(ctx, "channel:"+slug+":storage:quota_bytes").Int64()
	if err != nil {
		if err == redis.Nil {
			return 0, nil // 0 = use global
		}
		return 0, err
	}
	return v, nil
}

func dbSetChannelStorageQuota(ctx context.Context, slug string, bytes int64) error {
	if bytes == 0 {
		return rdb.Del(ctx, "channel:"+slug+":storage:quota_bytes").Err()
	}
	return rdb.Set(ctx, "channel:"+slug+":storage:quota_bytes", bytes, 0).Err()
}

// dbGetEffectiveStorageQuota returns the effective quota for a channel (channel override or global default).
func dbGetEffectiveStorageQuota(ctx context.Context, slug string) (int64, error) {
	v, err := dbGetChannelStorageQuota(ctx, slug)
	if err != nil {
		return 0, err
	}
	if v > 0 {
		return v, nil
	}
	return dbGetGlobalStorageQuota(ctx)
}

func dbGetChannelStorageUsed(ctx context.Context, slug string) (int64, error) {
	v, err := rdb.Get(ctx, "channel:"+slug+":storage:used_bytes").Int64()
	if err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}

func dbIncrChannelStorageUsed(ctx context.Context, slug string, bytes int64) error {
	return rdb.IncrBy(ctx, "channel:"+slug+":storage:used_bytes", bytes).Err()
}

// dbIncrChannelStorageUsedResult increments and returns the caller's own
// post-increment total. That return value is what makes a quota check safe
// under concurrency: every concurrent uploader gets a different number, so only
// one of them can be the request that crosses the limit.
func dbIncrChannelStorageUsedResult(ctx context.Context, slug string, bytes int64) (int64, error) {
	return rdb.IncrBy(ctx, "channel:"+slug+":storage:used_bytes", bytes).Result()
}

func dbDecrChannelStorageUsed(ctx context.Context, slug string, bytes int64) error {
	return rdb.DecrBy(ctx, "channel:"+slug+":storage:used_bytes", bytes).Err()
}

// channelFilesKey is the sorted set tracking a channel's uploaded files.
func channelFilesKey(slug string) string { return "channel:" + slug + ":files" }

// encodeFileMember encodes a tracking-set member as "fileID:size".
func encodeFileMember(id string, size int64) string { return fmt.Sprintf("%s:%d", id, size) }

// decodeFileMember is the single inverse of encodeFileMember; every reader of
// the tracking set must go through it rather than re-implementing the parse.
func decodeFileMember(m string) (id string, size int64, ok bool) {
	i := strings.LastIndex(m, ":")
	if i <= 0 {
		return "", 0, false
	}
	size, err := strconv.ParseInt(m[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[:i], size, true
}

// dbAddChannelFile registers a file in the channel's file tracking sorted set.
func dbAddChannelFile(ctx context.Context, slug, fileID string, uploadedAt int64, size int64) error {
	return rdb.ZAdd(ctx, channelFilesKey(slug), redis.Z{
		Score:  float64(uploadedAt),
		Member: encodeFileMember(fileID, size),
	}).Err()
}

// dbRemoveChannelFile removes a file from the channel's tracking set.
// size is the file's recorded size, which the caller already holds. Because a
// member is exactly "id:size", knowing the size makes removal a direct ZREM
// instead of a scan: this used to pull the channel's entire file index over the
// wire to locate one entry, and auto-cleanup calls it up to 200 times inside a
// single upload — on a channel with 100k files that was millions of member
// transfers per request, with Redis blocked on each one.
//
// The scan survives only as a fallback for an index entry whose size disagrees
// with the metadata (a record written before the size was tracked), so a
// mismatch still removes the entry rather than silently leaking it.
func dbRemoveChannelFile(ctx context.Context, slug, fileID string, size int64) error {
	removed, err := rdb.ZRem(ctx, channelFilesKey(slug), encodeFileMember(fileID, size)).Result()
	if err != nil {
		return err
	}
	if removed > 0 {
		return nil
	}

	members, err := rdb.ZRange(ctx, channelFilesKey(slug), 0, -1).Result()
	if err != nil {
		return err
	}
	for _, m := range members {
		if id, _, ok := decodeFileMember(m); ok && id == fileID {
			return rdb.ZRem(ctx, channelFilesKey(slug), m).Err()
		}
	}
	return nil
}

// dbGetOldestChannelFiles returns the oldest file IDs and their sizes (oldest first).
func dbGetOldestChannelFiles(ctx context.Context, slug string, limit int64) ([]struct {
	ID   string
	Size int64
}, error) {
	members, err := rdb.ZRange(ctx, channelFilesKey(slug), 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	result := make([]struct {
		ID   string
		Size int64
	}, 0, len(members))
	for _, m := range members {
		if fileID, size, ok := decodeFileMember(m); ok {
			result = append(result, struct {
				ID   string
				Size int64
			}{fileID, size})
		}
	}
	return result, nil
}

func dbGetChannelAutoCleanup(ctx context.Context, slug string) (bool, error) {
	v, err := rdb.Get(ctx, "channel:"+slug+":storage:auto_cleanup").Result()
	// "unset" (redis.Nil) means disabled; a real failure must not be reported as
	// a successful "disabled", or the caller silently acts on a guess.
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == "true" || v == "1", nil
}

func dbSetChannelAutoCleanup(ctx context.Context, slug string, enabled bool) error {
	val := "false"
	if enabled {
		val = "true"
	}
	return rdb.Set(ctx, "channel:"+slug+":storage:auto_cleanup", val, 0).Err()
}

// dbIncrFileHashRefs increments the reference count for a file hash (dedup tracking).
func dbIncrFileHashRefs(ctx context.Context, hash string) error {
	return rdb.Incr(ctx, "file:hash:"+hash+":refs").Err()
}

// dbIncrFileHashRefsResult claims a reference and returns the resulting count.
// The upload path claims BEFORE deciding whether the blob needs writing, and
// the returned count is what tells it whether it is the first reference.
func dbIncrFileHashRefsResult(ctx context.Context, hash string) (int64, error) {
	return rdb.Incr(ctx, "file:hash:"+hash+":refs").Result()
}

// delFileHashRefsIfUnreferenced removes the counter only while nothing holds a
// reference; otherwise it answers the count that keeps it alive. GET on an
// absent key is false in Lua, hence the "or 0".
var delFileHashRefsIfUnreferenced = redis.NewScript(`
	local n = tonumber(redis.call('GET', KEYS[1]) or 0)
	if n <= 0 then
		redis.call('DEL', KEYS[1])
		return 0
	end
	return n
`)

// dbDelFileHashRefs removes the ref counter once it reached zero, and returns
// 0 when it did (or when the counter was already gone). A plain DEL was not
// safe: the last-reference delete reads the counter before removing the blob,
// and an upload of the same bytes could claim (INCR) the hash between that
// read and the DEL — it then waits on the per-hash lock and writes the blob
// anew, but its reference was wiped. The check and the removal are one
// atomic step here; a positive return is the count that kept the counter.
func dbDelFileHashRefs(ctx context.Context, hash string) (int64, error) {
	return delFileHashRefsIfUnreferenced.Run(ctx, rdb, []string{"file:hash:" + hash + ":refs"}).Int64()
}

// dbGetFileHashRefs reads the current count; an absent counter is redis.Nil.
func dbGetFileHashRefs(ctx context.Context, hash string) (int64, error) {
	return rdb.Get(ctx, "file:hash:"+hash+":refs").Int64()
}

// dbDecrFileHashRefs decrements ref count; returns new count.
func dbDecrFileHashRefs(ctx context.Context, hash string) (int64, error) {
	return rdb.Decr(ctx, "file:hash:"+hash+":refs").Result()
}
