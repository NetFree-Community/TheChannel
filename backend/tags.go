package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// A channel's message tags: a short list of named, coloured labels that the
// channel's managers define, writers may put on a post (at most one, never
// required), and every reader can filter the feed by.
//
// Storage:
//   - channel:<slug>:tags            JSON array of Tag, in display order
//   - channel:<slug>:tag:<id>:m_times ZSET of message keys carrying the tag,
//     scored like channel:<slug>:m_times, so the /messages Lua pages through
//     a filtered feed exactly the way it pages the full one
//   - the message hash's "tag" field holds the tag id ("" or absent = none)
//
// Tag ids are random and never reused, so a message whose tag was deleted can
// never come back labelled with an unrelated new tag.

type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

const (
	maxTagsPerChannel = 30
	maxTagNameRunes   = 24
)

var (
	tagColorRegex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	tagIDRegex    = regexp.MustCompile(`^[a-z0-9]{6,16}$`)
)

func channelTagsKey(slug string) string { return fmt.Sprintf("channel:%s:tags", slug) }

func channelTagIndexKey(slug, tagID string) string {
	return fmt.Sprintf("channel:%s:tag:%s:m_times", slug, tagID)
}

func dbGetTags(ctx context.Context, slug string) ([]Tag, error) {
	raw, err := rdb.Get(ctx, channelTagsKey(slug)).Result()
	if err == redis.Nil {
		return []Tag{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get tags: %w", err)
	}
	var tags []Tag
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
	}
	if tags == nil {
		tags = []Tag{}
	}
	return tags, nil
}

// tagExists reports whether id is one of the channel's current tags. A lookup
// failure answers false: a post then goes out untagged rather than into an
// index nobody can reach.
func tagExists(ctx context.Context, slug, id string) bool {
	if id == "" {
		return false
	}
	tags, err := dbGetTags(ctx, slug)
	if err != nil {
		return false
	}
	for _, t := range tags {
		if t.ID == id {
			return true
		}
	}
	return false
}

// resolveTag maps what an integrator sent (a tag id, or a tag's name) to a
// current tag id, or "" when it matches none.
func resolveTag(ctx context.Context, slug, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	tags, err := dbGetTags(ctx, slug)
	if err != nil {
		return ""
	}
	for _, t := range tags {
		if t.ID == v {
			return t.ID
		}
	}
	for _, t := range tags {
		if strings.EqualFold(t.Name, v) {
			return t.ID
		}
	}
	return ""
}

// normaliseTags validates the list a manager saved. Existing ids are kept,
// new entries (no id, or one the server never issued) get a fresh one.
func normaliseTags(in []Tag, current []Tag) ([]Tag, error) {
	if len(in) > maxTagsPerChannel {
		return nil, fmt.Errorf("אפשר להגדיר עד %d תגיות", maxTagsPerChannel)
	}
	known := make(map[string]bool, len(current))
	for _, t := range current {
		known[t.ID] = true
	}
	out := make([]Tag, 0, len(in))
	seenID := map[string]bool{}
	seenName := map[string]bool{}
	for _, t := range in {
		name := strings.Join(strings.Fields(t.Name), " ")
		if name == "" {
			return nil, fmt.Errorf("לכל תגית צריך שם")
		}
		if utf8.RuneCountInString(name) > maxTagNameRunes {
			return nil, fmt.Errorf("שם תגית יכול להיות עד %d תווים", maxTagNameRunes)
		}
		if seenName[strings.ToLower(name)] {
			return nil, fmt.Errorf("התגית \"%s\" מופיעה פעמיים", name)
		}
		seenName[strings.ToLower(name)] = true
		if !tagColorRegex.MatchString(t.Color) {
			return nil, fmt.Errorf("צבע לא תקין לתגית \"%s\"", name)
		}
		id := t.ID
		if !known[id] || !tagIDRegex.MatchString(id) || seenID[id] {
			id = generatedRandomID(6) // 12 hex characters
		}
		seenID[id] = true
		out = append(out, Tag{ID: id, Name: name, Color: strings.ToLower(t.Color)})
	}
	return out, nil
}

// dbSetTags stores the list and retires every tag that is no longer in it:
// its messages lose the label and its index is dropped.
func dbSetTags(ctx context.Context, slug string, tags []Tag, current []Tag) error {
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	keep := make(map[string]bool, len(tags))
	for _, t := range tags {
		keep[t.ID] = true
	}
	if err := rdb.Set(ctx, channelTagsKey(slug), raw, 0).Err(); err != nil {
		return err
	}
	for _, t := range current {
		if keep[t.ID] {
			continue
		}
		if err := dbRetireTag(ctx, slug, t.ID); err != nil {
			log.Printf("retire tag %s of %s: %v\n", t.ID, slug, err)
		}
	}
	return nil
}

// clearTagIfMatches drops a message's tag only while it still names the tag
// being retired, so an edit that re-tagged it in between is not undone.
var clearTagIfMatches = redis.NewScript(`
	for _, key in ipairs(KEYS) do
		if redis.call('HGET', key, 'tag') == ARGV[1] then
			redis.call('HDEL', key, 'tag')
		end
	end
	return 1
`)

func dbRetireTag(ctx context.Context, slug, tagID string) error {
	indexKey := channelTagIndexKey(slug, tagID)
	members, err := rdb.ZRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		return err
	}
	for i := 0; i < len(members); i += 200 {
		end := i + 200
		if end > len(members) {
			end = len(members)
		}
		if err := clearTagIfMatches.Run(ctx, rdb, members[i:end], tagID).Err(); err != nil {
			return err
		}
	}
	return rdb.Del(ctx, indexKey).Err()
}

// tagIndexKeysFor lists the index keys of a channel's current tags, for the
// channel delete.
func tagIndexKeysFor(ctx context.Context, slug string) []string {
	tags, err := dbGetTags(ctx, slug)
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for _, t := range tags {
		keys = append(keys, channelTagIndexKey(slug, t.ID))
	}
	return keys
}

func getTagsList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tags, err := dbGetTags(ctx, channelSlugFromCtx(r))
	if err != nil {
		http.Error(w, "Failed to get tags", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

func setTags(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	req := struct {
		Tags []Tag `json:"tags"`
	}{}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	current, err := dbGetTags(ctx, slug)
	if err != nil {
		http.Error(w, "Failed to get tags", http.StatusInternalServerError)
		return
	}
	tags, err := normaliseTags(req.Tags, current)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
		return
	}
	if err := dbSetTags(ctx, slug, tags, current); err != nil {
		http.Error(w, "Failed to set tags", http.StatusInternalServerError)
		return
	}

	// Open feeds reload the list (new names and colours, a removed filter),
	// and cached /messages bodies may carry a retired tag.
	publishEvent(ctx, slug, []byte(`{"type":"tags-updated"}`))
	touchLastModified(ctx, slug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "tags": tags})
}
