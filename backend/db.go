package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/icza/dyno"
	"github.com/redis/go-redis/v9"
)

var redisType = os.Getenv("REDIS_PROTOCOL")
var redisAddr = os.Getenv("REDIS_ADDR")
var redisPass = os.Getenv("REDIS_PASSWORD")
var rdb *redis.Client

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
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	Email      string     `json:"email"`
	PublicName string     `json:"publicName"`
	Privileges Privileges `json:"privileges"`
}

func init() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb = redis.NewClient(&redis.Options{
		Network:  redisType,
		Addr:     redisAddr,
		Password: redisPass,
		DB:       0,
	})

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Connection to db failed: %v \n", err)
	}

	log.Println("Connection to DB successful!")
}

func getMessageNextId(ctx context.Context) int {
	id, err := rdb.Incr(ctx, "message:next_id").Result()
	if err != nil {
		log.Fatalf("Failed to get id: %v\n", err)
	}

	return int(id)
}

func setMessage(ctx context.Context, m *Message, isUpdate bool) error {
	messageKey := fmt.Sprintf("messages:%d", m.ID)

	isScheduling := !m.Timestamp.IsZero()
	if isScheduling {
		// Add scheduled message to sorted set
		if err := rdb.ZAdd(ctx, "scheduled_messages", redis.Z{Score: float64(m.Timestamp.Unix()), Member: messageKey}).Err(); err != nil {
			return err
		}
	} else {
		m.Timestamp = time.Now()
	}

	// Set message in hash
	if err := rdb.HSet(ctx, messageKey, m).Err(); err != nil {
		return err
	}

	// Add message timestamp to sorted set
	if !isUpdate {
		if err := rdb.ZAdd(ctx, "m_times:1", redis.Z{Score: float64(m.Timestamp.Unix()), Member: messageKey}).Err(); err != nil {
			return err
		}
	}

	pushType := NewMessage
	if isScheduling {
		pushType = MsgBeforeScheduling
	} else if isUpdate {
		pushType = EditMessage
	}
	pushSseMessage(pushType, m)

	return nil
}

func setReaction(ctx context.Context, messageId int, emoji string, userId string) error {
	kay := fmt.Sprintf("message:%d:reactions", messageId)
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

	r, err := funcGetSumReactions(ctx, messageId)
	if err != nil {
		return err
	}

	if err := updateMessageReactions(ctx, messageId, r); err != nil {
		return err
	}

	m := &Message{
		ID:        messageId,
		Reactions: r,
	}

	pushSseMessage(Reaction, m)

	return nil
}

var getMessageRange = redis.NewScript(`
	local time_set_key = KEYS[1]
	local offset_key = KEYS[2]

	local required_length = tonumber(ARGV[1])
	local isAdmin = ARGV[2] == 'true'
	local countViews = ARGV[3] == 'true'
	local direction = ARGV[4] or 'desc'
	local nowTime = ARGV[5] 


	local start_index
	if direction == 'asc' then
	    start_index = redis.call('ZRANK', time_set_key, offset_key) or 0
	else
	    start_index = redis.call('ZREVRANK', time_set_key, offset_key) or 0
	end

	if start_index > 0 then
		start_index = start_index + 1
	end

	local messages = {}
	repeat
		local batch_size = required_length - #messages
		local stop_index = start_index + batch_size
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

			if (not message['deleted'] and message['timestamp'] < nowTime) or isAdmin then
				table.insert(messages, message)
			end
		end

		start_index = start_index + batch_size

	until #messages >= required_length

	return cjson.encode(messages)
`)

func funcGetMessageRange(ctx context.Context, start, stop int64, isAdmin, countViews bool, direction string) ([]Message, error) {
	offsetKeyName := fmt.Sprintf("messages:%d", start)
	res, err := getMessageRange.Run(
		ctx,
		rdb,
		[]string{
			"m_times:1",
			offsetKeyName,
		},
		[]string{
			strconv.FormatInt(stop, 10),
			strconv.FormatBool(isAdmin),
			strconv.FormatBool(countViews),
			direction,
			time.Now().UTC().Format(time.RFC3339Nano),
		}).Result()
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

func funcGetSumReactions(ctx context.Context, messageId int) (Reactions, error) {
	res, err := sumMessageReactions.Run(ctx, rdb, []string{fmt.Sprintf("message:%d:reactions", messageId)}).Result()
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

func updateMessageReactions(ctx context.Context, messageId int, reactions Reactions) error {
	messageKey := fmt.Sprintf("messages:%d", messageId)

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

func funcDeleteMessage(ctx context.Context, id string) error {
	msgKey := fmt.Sprintf("messages:%s", id)
	rdb.HSet(ctx, msgKey, "deleted", true)

	m := &Message{}
	idInt, _ := strconv.Atoi(id)
	m.ID = idInt
	m.Deleted = true
	m.LastEdit = time.Now()
	m.Text = "*ההודעה נמחקה*"
	m.File = FileResponse{}

	pushSseMessage(DeleteMessage, m)

	return nil
}

func addViewsToMessages(ctx context.Context, messages []Message) {
	if !settingConfig.CountViews {
		return
	}
	for _, m := range messages {
		rdb.HIncrBy(ctx, fmt.Sprintf("messages:%d", m.ID), "views", 1)
	}
}

// https://redis.io/docs/latest/operate/oss_and_stack/management/security/#string-escaping-and-nosql-injection
func addSubscription(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rdb.SAdd(ctx, "subscriptions", token).Result()
	if err != nil {
		return err
	}

	return nil
}

func getSubcriptionsList() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subscriptionsSet, err := rdb.SMembers(ctx, "subscriptions").Result()
	if err != nil {
		log.Printf("Failed to get subscriptions: %v\n", err)
		return []string{}, err
	}
	return subscriptionsSet, nil
}

func getChannelDetails(ctx context.Context) (map[string]string, error) {
	return rdb.HGetAll(ctx, "channel:1").Result()
}

func dbSetEmojisList(ctx context.Context, emojis []string) error {
	// if len(emojis) == 0 {
	// 	return fmt.Errorf("emojis list cannot be empty")
	// }

	emojisJSON, err := json.Marshal(emojis)
	if err != nil {
		return fmt.Errorf("failed to marshal emojis: %v", err)
	}

	if err := rdb.Set(ctx, "emojis:list", emojisJSON, 0).Err(); err != nil {
		return fmt.Errorf("failed to set emojis in db: %v", err)
	}

	return nil

}

func dbGetEmojisList(ctx context.Context) ([]string, error) {
	emojisJSON, err := rdb.Get(ctx, "emojis:list").Result()
	if err != nil {
		if err == redis.Nil {
			return emojis, nil
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

func dbSetSettings(ctx context.Context, settings *Settings) error {
	jsonSettings, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %v", err)
	}

	if err := rdb.Set(ctx, "settings:list", jsonSettings, 0).Err(); err != nil {
		return fmt.Errorf("failed to set settings in db: %v", err)
	}

	return nil
}

func dbGetSettings(ctx context.Context) (Settings, error) {
	settingsJSON, err := rdb.Get(ctx, "settings:list").Result()
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

func dbGetUsersAmount(ctx context.Context) (int64, error) {
	amount, err := rdb.SCard(ctx, "registered_emails").Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get users amount: %v", err)
	}
	return amount, nil
}

func getReportNextID(ctx context.Context) (int64, error) {
	return rdb.Incr(ctx, "report:next_id").Result()
}

func dbReportMessage(ctx context.Context, report *Report) error {
	id, err := getReportNextID(ctx)
	if err != nil {
		return err
	}
	reportKey := fmt.Sprintf("report:%d", id)
	report.Id = id

	if err := rdb.HSet(ctx, reportKey, report).Err(); err != nil {
		return err
	}

	if err := rdb.ZAdd(ctx, "reports:list", redis.Z{Score: float64(report.CreatedAt.Unix()), Member: reportKey}).Err(); err != nil {
		return err
	}

	if err := rdb.ZAdd(ctx, "reports:open", redis.Z{Score: float64(report.CreatedAt.Unix()), Member: reportKey}).Err(); err != nil {
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

func dbGetReports(ctx context.Context, status ReportStatus) (Reports, error) {
	jsonReports, err := getReportsScript.Run(ctx, rdb, []string{"reports:list", "reports:open", "reports:closed"}, []string{string(status), "100"}).Result()
	if err != nil {
		return nil, err
	}

	var reports Reports
	if jsonReports == nil || jsonReports == "{}" {
		return reports, nil
	}

	resStr, _ := dyno.GetString(jsonReports)
	if resStr == "" {
		return nil, nil
	}

	if err := json.Unmarshal([]byte(resStr), &reports); err != nil {
		return nil, err
	}

	return reports, nil
}

func dbSetReports(ctx context.Context, report *Report) error {
	reportKey := fmt.Sprintf("report:%d", report.Id)
	switch report.Closed {
	case true:
		if err := rdb.ZRem(ctx, "reports:open", reportKey).Err(); err != nil {
			return err
		}
		if err := rdb.ZAdd(ctx, "reports:closed", redis.Z{Score: float64(report.UpdatedAt.Unix()), Member: reportKey}).Err(); err != nil {
			return err
		}
	case false:
		if err := rdb.ZRem(ctx, "reports:closed", reportKey).Err(); err != nil {
			return err
		}
		if err := rdb.ZAdd(ctx, "reports:open", redis.Z{Score: float64(report.UpdatedAt.Unix()), Member: reportKey}).Err(); err != nil {
			return err
		}
	}

	if err := rdb.HSet(ctx, reportKey, "closed", report.Closed, "updatedAt", report.UpdatedAt).Err(); err != nil {
		return err
	}

	return nil
}

func dbSavePeakSSEConnections(peak *PeakSSEConnections) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb.HSet(ctx, "peak_sse_connections", "value", peak.Value, "timestamp", peak.Timestamp.Unix())
}

func dbGetPeakSSEConnections(ctx context.Context) (*PeakSSEConnections, error) {
	p, err := rdb.HGetAll(ctx, "peak_sse_connections").Result()
	if err != nil {
		return nil, err
	}

	var peak PeakSSEConnections
	vel, _ := dyno.GetInt(p["value"])
	timestamp, _ := dyno.GetInteger(p["timestamp"])

	peak.Value = vel
	peak.Timestamp = time.Unix(timestamp, 0)

	return &peak, nil
}

func dbSaveSSEStatistics(amount int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := fmt.Sprintf("sse_statistics:%d:%d", time.Now().Month(), time.Now().Year())
	member := fmt.Sprintf("%d&%s", amount, time.Now().Format("02-01-2006 15:04"))

	rdb.ZAdd(ctx, key, redis.Z{Score: float64(time.Now().Unix()), Member: member})
}

func dbGetSSEStatistics(ctx context.Context, length int64) (*Statistics, error) {
	key := fmt.Sprintf("sse_statistics:%d:%d", time.Now().Month(), time.Now().Year())
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

var getScheduledMessagesScript = redis.NewScript(`
    local listKey = KEYS[1]
	local nowTime = ARGV[1]

	local scheduledMessages = redis.call('ZRANGEBYSCORE', listKey, '-inf', nowTime)
	if #scheduledMessages == 0 then
		return '{}'
	end

	local result = {}
	for _, messageKey in ipairs(scheduledMessages) do
		local messageData = redis.call('HGETALL', messageKey)
		local message = {}

		for j = 1, #messageData, 2 do
			local key = messageData[j]
			local value = messageData[j+1]

			if key == 'id' then
				message[key] = tonumber(value)
            elseif key == 'views' then
				message[key] = 0
			elseif key == 'deleted' then
				message[key] = value == '1'
			elseif key == 'author' then
				message[key] = "Anonymous"
			elseif key == 'authorId' then
                message[key] = "Anonymous"
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

		if not message['deleted'] then
			table.insert(result, message)
		end
	end

	return cjson.encode(result)
`)

func dbGetScheduledMessages() ([]*Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jsonMessages, err := getScheduledMessagesScript.Run(
		ctx,
		rdb,
		[]string{
			"scheduled_messages",
		},
		[]string{
			strconv.FormatInt(time.Now().Unix(), 10),
		},
	).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get scheduled messages: %v", err)
	}

	if jsonMessages == "{}" {
		return []*Message{}, nil
	}

	var messages []*Message
	if err := json.Unmarshal([]byte(jsonMessages.(string)), &messages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal scheduled messages: %v", err)
	}

	return messages, nil
}

func dbRemoveScheduledMessage(key int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return rdb.ZRem(ctx, "scheduled_messages", fmt.Sprintf("messages:%d", key)).Err()
}
