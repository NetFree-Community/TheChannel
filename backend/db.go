package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

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
	Timestamp time.Time    `json:"timestamp" redis:"timestamp"`
	LastEdit  time.Time    `json:"last_edit" redis:"last_edit"`
	File      FileResponse `json:"file" redis:"-"`
	Deleted   bool         `json:"deleted" redis:"deleted"`
	Views     int          `json:"views" redis:"views"`
	Reactions Reactions    `json:"reactions" redis:"reactions"`
	ReplyTo   int          `json:"replyTo,omitempty" redis:"reply_to"`
	IsThread  bool         `json:"isThread" redis:"is_thread"`
}

type User struct {
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	Email      string     `json:"email"`
	PublicName string     `json:"publicName"`
	Privileges Privileges `json:"privileges"`
	Blocked    bool       `json:"blocked"`
}

type PushMessage struct {
	Type string  `json:"type"`
	M    Message `json:"message"`
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

func setMessage(ctx context.Context, m Message, isUpdate bool) error {
	messageKey := fmt.Sprintf("messages:%d", m.ID)

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

	pushType := "new-message"
	if isUpdate {
		pushType = "edit-message"
	}

	pushMessage := PushMessage{
		Type: pushType,
		M:    m,
	}

	pushMessageData, _ := json.Marshal(pushMessage)
	rdb.Publish(ctx, "events", pushMessageData)

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

	pushMessage := PushMessage{
		Type: "reaction",
		M: Message{
			ID:        messageId,
			Reactions: r,
		},
	}

	pushMessageData, _ := json.Marshal(pushMessage)
	rdb.Publish(ctx, "events", pushMessageData)

	return nil
}

var getMessageRange = redis.NewScript(`
	local time_set_key = KEYS[1]
	local offset_key = KEYS[2]

	local required_length = tonumber(ARGV[1])
	local isAdmin = ARGV[2] == 'true'
	local countViews = ARGV[3] == 'true'
	local isAuthenticated = ARGV[4] == 'true'
	local showAuthorToAuthenticated = ARGV[5] == 'true'

	local start_index = redis.call('ZREVRANK', time_set_key, offset_key) or 0
	if start_index > 0 then
		start_index = start_index + 1
	end

	local messages = {}
	repeat
		local batch_size = required_length - #messages
		local stop_index = start_index + batch_size
		local message_ids = redis.call('ZREVRANGE', time_set_key, start_index, stop_index)

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
				    elseif showAuthorToAuthenticated and isAuthenticated then
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

func funcGetMessageRange(ctx context.Context, start, stop int64, isAdmin, countViews, isAuthenticated bool) ([]Message, error) {
	offsetKeyName := fmt.Sprintf("messages:%d", start)
	res, err := getMessageRange.Run(ctx, rdb, []string{"m_times:1", offsetKeyName}, []string{
		strconv.FormatInt(stop, 10), 
		strconv.FormatBool(isAdmin), 
		strconv.FormatBool(countViews),
		strconv.FormatBool(isAuthenticated),
		strconv.FormatBool(settingConfig.ShowAuthorToAuthenticated),
	}).Result()
	if err != nil {
		return []Message{}, err
	}

	if res == "{}" {
		return []Message{}, nil
	}

	var messages []Message
	if err := json.Unmarshal([]byte(res.(string)), &messages); err != nil {
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
	if err := json.Unmarshal([]byte(res.(string)), &reactions); err != nil {
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
	rdb.Publish(ctx, "events", pushMessageData)

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
	if len(emojis) == 0 {
		return fmt.Errorf("emojis list cannot be empty")
	}

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
