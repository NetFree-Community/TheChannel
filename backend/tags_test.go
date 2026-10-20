package main

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func cleanupTagChannel(t *testing.T, slug string, tags []Tag) {
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, channelTagsKey(slug), "channel:"+slug+":events", "channel:"+slug+":last_modified",
			"channel:"+slug+":settings")
		for _, tg := range tags {
			rdb.Del(cctx, channelTagIndexKey(slug, tg.ID))
		}
	})
}

func TestNormaliseTagsValidates(t *testing.T) {
	current := []Tag{{ID: "aaaaaaaaaaaa", Name: "חדשות", Color: "#ff0000"}}

	out, err := normaliseTags([]Tag{
		{ID: "aaaaaaaaaaaa", Name: "  חדשות   דחופות ", Color: "#FF0000"},
		{ID: "made-up", Name: "אירועים", Color: "#00aa00"},
	}, current)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if out[0].ID != "aaaaaaaaaaaa" || out[0].Name != "חדשות דחופות" || out[0].Color != "#ff0000" {
		t.Errorf("existing tag not kept/cleaned: %+v", out[0])
	}
	if out[1].ID == "made-up" || !tagIDRegex.MatchString(out[1].ID) {
		t.Errorf("unknown id must be replaced by a server id, got %q", out[1].ID)
	}

	bad := [][]Tag{
		{{Name: "", Color: "#000000"}},
		{{Name: "x", Color: "red"}},
		{{Name: "שם ארוך מדי בהרבה עבור תגית אחת", Color: "#000000"}},
		{{Name: "כפול", Color: "#000000"}, {Name: "כפול", Color: "#111111"}},
	}
	for i, in := range bad {
		if _, err := normaliseTags(in, current); err == nil {
			t.Errorf("case %d: want an error for %+v", i, in)
		}
	}
	many := make([]Tag, maxTagsPerChannel+1)
	for i := range many {
		many[i] = Tag{Name: string(rune('א'+i%27)) + string(rune('0'+i/27)), Color: "#000000"}
	}
	if _, err := normaliseTags(many, nil); err == nil {
		t.Errorf("want an error above %d tags", maxTagsPerChannel)
	}
}

// Posting, retagging and filtering: the tag index follows the message, the
// filtered range returns only tagged posts, and deleting a tag strips it.
func TestTagIndexFollowsMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "tags-flow"
	tags := []Tag{{ID: "aaaaaaaaaaaa", Name: "א", Color: "#ff0000"}, {ID: "bbbbbbbbbbbb", Name: "ב", Color: "#00ff00"}}
	cleanupTagChannel(t, slug, tags)
	if err := dbSetTags(ctx, slug, tags, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		for i := 1; i <= 5; i++ {
			rdb.Del(cctx, "channel:"+slug+":messages:"+strconv.Itoa(i))
		}
		rdb.Del(cctx, "channel:"+slug+":m_times")
	})

	base := time.Now().Add(-time.Hour)
	for i := 1; i <= 5; i++ {
		m := Message{ID: i, Type: "md", Text: "m" + strconv.Itoa(i), Timestamp: base.Add(time.Duration(i) * time.Minute)}
		switch i {
		case 2, 4:
			m.Tag = "aaaaaaaaaaaa"
		case 5:
			m.Tag = "nosuchtag123" // not a channel tag: dropped
		}
		if err := setMessage(ctx, slug, &m, false); err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
	}

	ids := func(tag string) []int {
		msgs, err := funcGetMessageRange(ctx, slug, 0, 20, false, false, false, "desc", tag)
		if err != nil {
			t.Fatalf("range: %v", err)
		}
		out := []int{}
		for _, m := range msgs {
			out = append(out, m.ID)
		}
		return out
	}
	if got := ids("aaaaaaaaaaaa"); len(got) != 2 || got[0] != 4 || got[1] != 2 {
		t.Fatalf("filtered = %v, want [4 2]", got)
	}
	if got := ids(""); len(got) != 5 {
		t.Fatalf("unfiltered = %v, want 5 messages", got)
	}
	if f, _ := dbGetMessageFields(ctx, slug, "5"); f["tag"] != "" {
		t.Errorf("unknown tag stored: %q", f["tag"])
	}

	// Retag 4 to ב: it leaves א's index and joins ב's.
	m4 := Message{ID: 4, Type: "md", Text: "m4", Timestamp: base.Add(4 * time.Minute), Tag: "bbbbbbbbbbbb"}
	if err := setMessage(ctx, slug, &m4, true); err != nil {
		t.Fatal(err)
	}
	if got := ids("aaaaaaaaaaaa"); len(got) != 1 || got[0] != 2 {
		t.Errorf("after retag, א = %v, want [2]", got)
	}
	if got := ids("bbbbbbbbbbbb"); len(got) != 1 || got[0] != 4 {
		t.Errorf("after retag, ב = %v, want [4]", got)
	}

	// An asc page from a cursor that is not in the tag's index resumes by
	// time, not from the oldest tagged post.
	msgs, err := funcGetMessageRange(ctx, slug, 3, 20, false, false, false, "asc", "bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != 4 {
		t.Errorf("asc after untagged cursor = %+v, want only 4", msgs)
	}
	msgs, err = funcGetMessageRange(ctx, slug, 5, 20, false, false, false, "asc", "aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("asc after the newest post returned %d messages, want none", len(msgs))
	}

	// Deleting א strips it from message 2 and drops its index.
	if err := dbSetTags(ctx, slug, tags[1:], tags); err != nil {
		t.Fatal(err)
	}
	if f, _ := dbGetMessageFields(ctx, slug, "2"); f["tag"] != "" {
		t.Errorf("message 2 still tagged %q after the tag was deleted", f["tag"])
	}
	if n, _ := rdb.Exists(ctx, channelTagIndexKey(slug, "aaaaaaaaaaaa")).Result(); n != 0 {
		t.Errorf("deleted tag's index still exists")
	}
	if f, _ := dbGetMessageFields(ctx, slug, "4"); f["tag"] != "bbbbbbbbbbbb" {
		t.Errorf("message 4 lost its surviving tag: %q", f["tag"])
	}
}

func TestResolveTagByIdOrName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const slug = "tags-resolve"
	tags := []Tag{{ID: "cccccccccccc", Name: "Sport", Color: "#123456"}}
	cleanupTagChannel(t, slug, tags)
	if err := dbSetTags(ctx, slug, tags, nil); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"cccccccccccc": "cccccccccccc", "sport": "cccccccccccc", "nope": "", "": ""} {
		if got := resolveTag(ctx, slug, in); got != want {
			t.Errorf("resolveTag(%q) = %q, want %q", in, got, want)
		}
	}
}
