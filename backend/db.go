package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	// Support single-instance, Redis Sentinel, and Redis Cluster via env vars:
	//   REDIS_ADDRS   — comma-separated list; multiple = cluster mode
	//   REDIS_MASTER  — master name for Sentinel
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
	rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: fmt.Sprintf("channel:%s:events", slug),
		MaxLen: 1000,
		Approx: true,
		Values: map[string]interface{}{"data": string(data)},
	})
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

	// Load per-channel settings for regex replace
	settings, err := dbGetSettings(ctx, slug)
	if err == nil {
		cfg := settings.ToConfig()
		for _, regex := range cfg.RegexReplace {
			if !strings.HasPrefix(m.Text, "[quote-embedded#]") {
				t := regex.Pattern.ReplaceAllString(m.Text, regex.Replace)
				m.Text = t
			}
		}
	}

	// Set message in hash
	if err := rdb.HSet(ctx, messageKey, m).Err(); err != nil {
		return err
	}

	// Add message timestamp to sorted set
	if !isUpdate {
		if err := rdb.ZAdd(ctx, fmt.Sprintf("channel:%s:m_times", slug), redis.Z{Score: float64(m.Timestamp.Unix()), Member: messageKey}).Err(); err != nil {
			return err
		}
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
	kay := fmt.Sprintf("channel:%s:message:%d:reactions", slug, messageId)
	userId = fmt.Sprintf("%v", userId)

	react := map[string]string{
		userId: emoji,
	}

	prevReact, err := rdb.HGet(ctx, kay, userId).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("failed to get previous reaction: %v", err)
	}

	if prevReact == emoji {
		react = map[string]string{
			userId: "",
		}
	}

	if err := rdb.HSet(ctx, kay, react).Err(); err != nil {
		return err
	}

	r, err := funcGetSumReactions(ctx, slug, messageId)
	if err != nil {
		return err
	}

	if err := updateMessageReactions(ctx, slug, messageId, r); err != nil {
		return err
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
	local scan_rounds = 0
	repeat
		scan_rounds = scan_rounds + 1
		if scan_rounds > max_scan_rounds then break end
		local batch_size = required_length - #messages
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

func updateMessageReactions(ctx context.Context, slug string, messageId int, reactions Reactions) error {
	messageKey := fmt.Sprintf("channel:%s:messages:%d", slug, messageId)

	exists, err := rdb.Exists(ctx, messageKey).Result()
	if err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("message %d does not exist", messageId)
	}

	reactionsJSON, err := json.Marshal(reactions)
	if err != nil {
		return fmt.Errorf("failed to marshal reactions: %v", err)
	}

	if err := rdb.HSet(ctx, messageKey, "reactions", reactionsJSON).Err(); err != nil {
		return err
	}

	return nil
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

func funcDeleteMessage(ctx context.Context, slug string, id string) error {
	msgKey := fmt.Sprintf("channel:%s:messages:%s", slug, id)
	rdb.HSet(ctx, msgKey, "deleted", true)

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
	for _, m := range messages {
		rdb.HIncrBy(ctx, fmt.Sprintf("channel:%s:messages:%d", slug, m.ID), "views", 1)
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

func getSubcriptionsList(slug string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "subscriptions"
	if slug != "" {
		key = fmt.Sprintf("channel:%s:subscriptions", slug)
	}
	subscriptionsSet, err := rdb.SMembers(ctx, key).Result()
	if err != nil {
		log.Printf("Failed to get subscriptions: %v\n", err)
		return []string{}, err
	}
	return subscriptionsSet, nil
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

func dbSetUsersList(ctx context.Context, usersList []User) error {
	jsonUsersList, err := json.Marshal(usersList)
	if err != nil {
		return err
	}
	if err := rdb.Set(ctx, "users:list", jsonUsersList, 0).Err(); err != nil {
		return err
	}

	return nil
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
	const maxRetries = 5

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

func dbSavePeakSSEConnections(slug string, peak *PeakSSEConnections) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("channel:%s:peak_sse_connections", slug)
	rdb.HSet(ctx, key, "value", peak.Value, "timestamp", peak.Timestamp.Unix())
}

func dbGetPeakSSEConnections(ctx context.Context, slug string) (*PeakSSEConnections, error) {
	key := fmt.Sprintf("channel:%s:peak_sse_connections", slug)
	p, err := rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var peak PeakSSEConnections
	vel, _ := dyno.GetInteger(p["value"])
	timestamp, _ := dyno.GetInteger(p["timestamp"])

	peak.Value = vel
	peak.Timestamp = time.Unix(timestamp, 0)

	return &peak, nil
}

func dbSaveSSEStatistics(slug string, amount int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := fmt.Sprintf("channel:%s:sse_statistics:%d:%d", slug, time.Now().Month(), time.Now().Year())
	member := fmt.Sprintf("%d&%s", amount, time.Now().Format("02-01-2006 15:04"))

	rdb.ZAdd(ctx, key, redis.Z{Score: float64(time.Now().Unix()), Member: member})
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
	if earliest > 0 {
		rdb.ZAdd(ctx, "scheduled:due_channels", redis.Z{Score: earliest, Member: slug})
	} else {
		rdb.ZRem(ctx, "scheduled:due_channels", slug)
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
	}
	if h["slug"] == "" {
		channel.Slug = slug
	}

	if t, err := time.Parse(time.RFC3339, h["createdAt"]); err == nil {
		channel.CreatedAt = t
	}

	// Load features
	featuresKey := fmt.Sprintf("channel:%s:features", slug)
	featuresJSON, err := rdb.Get(ctx, featuresKey).Result()
	if err == nil {
		var features ChannelFeatures
		if err := json.Unmarshal([]byte(featuresJSON), &features); err == nil {
			channel.Features = features
		}
	}

	return channel, nil
}

func dbListChannels(ctx context.Context) ([]*ChannelData, error) {
	slugs, err := rdb.ZRange(ctx, "channels:list", 0, -1).Result()
	if err != nil {
		return nil, err
	}

	var channels []*ChannelData
	for _, slug := range slugs {
		ch, err := dbGetChannel(ctx, slug)
		if err != nil {
			log.Printf("Failed to get channel %s: %v\n", slug, err)
			continue
		}
		channels = append(channels, ch)
	}

	return channels, nil
}

func dbDeleteChannel(ctx context.Context, slug string) error {
	p := slug

	// Step 1: collect all message keys and reaction keys from the sorted set (avoids SCAN)
	messageKeys, _ := rdb.ZRange(ctx, fmt.Sprintf("channel:%s:m_times", p), 0, -1).Result()
	reactionKeys := make([]string, 0, len(messageKeys))
	for _, mk := range messageKeys {
		// mk is already the full key e.g. "channel:{slug}:messages:{id}"
		// derive reaction key from it by replacing "messages:" prefix with "message:" and appending ":reactions"
		id := strings.TrimPrefix(mk, fmt.Sprintf("channel:%s:messages:", p))
		reactionKeys = append(reactionKeys, fmt.Sprintf("channel:%s:message:%s:reactions", p, id))
	}

	// Step 1b: release every uploaded file. Drop the tracking set first so the
	// per-file cleanup below does not re-scan it once for each file, then reuse
	// the normal delete path (deleteFileByID) so hash refcounts are decremented
	// and the R2/disk blob is removed once the last reference is gone.
	fileMembers, _ := rdb.ZRange(ctx, fmt.Sprintf("channel:%s:files", p), 0, -1).Result()
	rdb.Del(ctx, fmt.Sprintf("channel:%s:files", p))
	fileKeys := make([]string, 0, len(fileMembers))
	for _, m := range fileMembers {
		// member format: "fileID:size"
		i := strings.LastIndex(m, ":")
		if i <= 0 {
			continue
		}
		fileID := m[:i]
		if len(fileID) >= 4 {
			deleteFileByID(ctx, p, fileID)
		}
		fileKeys = append(fileKeys, "file:"+fileID)
	}

	// Step 1c: collect the individual report hashes referenced by the reports index
	reportKeys, _ := rdb.ZRange(ctx, fmt.Sprintf("channel:%s:reports:list", p), 0, -1).Result()

	// Step 1d: monthly SSE statistics keys have no index; enumerate the possible
	// month/year combinations instead of running a SCAN across the keyspace.
	currentYear := time.Now().Year()
	statsKeys := make([]string, 0, 12*11)
	for year := currentYear - 10; year <= currentYear; year++ {
		for month := time.January; month <= time.December; month++ {
			statsKeys = append(statsKeys, fmt.Sprintf("channel:%s:sse_statistics:%d:%d", p, month, year))
		}
	}

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
		fmt.Sprintf("channel:%s:peak_sse_connections", p),
		fmt.Sprintf("channel:%s:storage:used_bytes", p),
		fmt.Sprintf("channel:%s:storage:quota_bytes", p),
		fmt.Sprintf("channel:%s:storage:auto_cleanup", p),
		fmt.Sprintf("channel:%s:files", p),
		fmt.Sprintf("channel:%s:events", p),
		fmt.Sprintf("channel:%s:last_modified", p),
	}
	allKeys := append(fixedKeys, messageKeys...)
	allKeys = append(allKeys, reactionKeys...)
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

	// Step 3: revoke every per-channel role for this slug, otherwise recreating
	// the same slug would immediately hand the old grantees their access back.
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		for i := range users {
			delete(users[i].ChannelRoles, slug) // delete on a nil map is a no-op
		}
		return users
	}); err != nil {
		log.Printf("dbDeleteChannel: clear roles for %s: %v\n", slug, err)
	} else if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after dbDeleteChannel: %v\n", err)
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

func dbAssignChannelRole(ctx context.Context, email, slug string, role ChannelRole) error {
	return dbUpdateUsersList(ctx, func(users []User) []User {
		for i, u := range users {
			if u.Email == email {
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

func dbDecrChannelStorageUsed(ctx context.Context, slug string, bytes int64) error {
	return rdb.DecrBy(ctx, "channel:"+slug+":storage:used_bytes", bytes).Err()
}

// dbAddChannelFile registers a file in the channel's file tracking sorted set.
func dbAddChannelFile(ctx context.Context, slug, fileID string, uploadedAt int64, size int64) error {
	// member encodes fileID and size separated by ":"
	member := fmt.Sprintf("%s:%d", fileID, size)
	return rdb.ZAdd(ctx, "channel:"+slug+":files", redis.Z{
		Score:  float64(uploadedAt),
		Member: member,
	}).Err()
}

// dbRemoveChannelFile removes a file from the channel's tracking set.
func dbRemoveChannelFile(ctx context.Context, slug, fileID string) error {
	// We need to find and remove the member that starts with fileID
	members, err := rdb.ZRange(ctx, "channel:"+slug+":files", 0, -1).Result()
	if err != nil {
		return err
	}
	for _, m := range members {
		if len(m) >= len(fileID) && m[:len(fileID)] == fileID {
			return rdb.ZRem(ctx, "channel:"+slug+":files", m).Err()
		}
	}
	return nil
}

// dbGetOldestChannelFiles returns the oldest file IDs and their sizes (oldest first).
func dbGetOldestChannelFiles(ctx context.Context, slug string, limit int64) ([]struct {
	ID   string
	Size int64
}, error) {
	members, err := rdb.ZRange(ctx, "channel:"+slug+":files", 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	result := make([]struct {
		ID   string
		Size int64
	}, 0, len(members))
	for _, m := range members {
		// format: "fileID:size"
		for i := len(m) - 1; i >= 0; i-- {
			if m[i] == ':' {
				fileID := m[:i]
				var size int64
				fmt.Sscanf(m[i+1:], "%d", &size)
				result = append(result, struct {
					ID   string
					Size int64
				}{fileID, size})
				break
			}
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

// dbDelFileHashRefs removes the ref counter entirely (used once it reaches zero).
func dbDelFileHashRefs(ctx context.Context, hash string) error {
	return rdb.Del(ctx, "file:hash:"+hash+":refs").Err()
}

// dbDecrFileHashRefs decrements ref count; returns new count.
func dbDecrFileHashRefs(ctx context.Context, hash string) (int64, error) {
	return rdb.Decr(ctx, "file:hash:"+hash+":refs").Result()
}
