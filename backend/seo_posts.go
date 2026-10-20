package main

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// postPagePathRe matches the page of one post, /channel/<slug>/post/<id>:
// the address a search result or a shared post link points at. Before it a
// post was only a #<id> fragment of the channel page, which search engines
// ignore, so no single post could ever be a result of its own.
var postPagePathRe = regexp.MustCompile(`^/channel/([^/]+)/post/([1-9][0-9]{0,9})/?$`)

// channelPagePosts is how many of the newest posts the channel page carries
// as readable text, for a crawler that does not run the app (and for the
// moment before it starts).
const channelPagePosts = 20

// channelPagePostChars bounds each post's text on the channel page; the
// post's own page carries it whole.
const channelPagePostChars = 600

// postTitleMax is about what a search result shows of a title.
const postTitleMax = 60

// seoPost is what a search engine may see of one post: its text with the
// markup taken out, and nothing about who wrote it.
type seoPost struct {
	ID        int
	Text      string
	Image     string
	Published time.Time
	Edited    time.Time
}

var (
	// [image-embedded#800x600](url), [video-embedded#](url), [audio-embedded#](url)
	mediaTokenRe = regexp.MustCompile(`\[(image|video|audio)-embedded#(?:\d+x\d+)?\]\(([^)\s]*)\)`)
	// A quote of another post fills its whole line: [quote-embedded#](id@text)
	quoteTokenRe = regexp.MustCompile(`(?m)^\[quote-embedded#\]\([^\n]*\)[ \t]*$`)
	mdImageRe    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLinkRe     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	htmlTagRe    = regexp.MustCompile(`</?[a-zA-Z][a-zA-Z0-9-]*(?:\s[^>]*)?/?>`)
	mdHeadingRe  = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]+`)
	mdQuoteRe    = regexp.MustCompile(`(?m)^[ \t]*>[ \t]?`)
	blankRunRe   = regexp.MustCompile(`\n{3,}`)
	mdEmphasis   = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "||", "")
)

// postPlainText turns a post's markdown into the text a reader sees, and
// returns the first embedded image (the post's picture for a link preview).
func postPlainText(md string) (text, image string) {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = quoteTokenRe.ReplaceAllString(md, "")
	for _, m := range mediaTokenRe.FindAllStringSubmatch(md, -1) {
		if m[1] == "image" && image == "" {
			image = m[2]
		}
	}
	md = mediaTokenRe.ReplaceAllString(md, "")
	md = mdImageRe.ReplaceAllString(md, "$1")
	md = mdLinkRe.ReplaceAllString(md, "$1")
	md = htmlTagRe.ReplaceAllString(md, "")
	md = mdHeadingRe.ReplaceAllString(md, "")
	md = mdQuoteRe.ReplaceAllString(md, "")
	md = mdEmphasis.Replace(md)
	md = html.UnescapeString(md)

	lines := strings.Split(md, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	text = strings.TrimSpace(blankRunRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
	return text, image
}

// seoPostFrom builds the public view of a post, false for anything a
// stranger must not see or that is not the channel's own content: a deleted
// post or a paid ad. A post with no text (a picture alone) is still a post,
// but has nothing to be found by: callers list only posts with Text.
func seoPostFrom(m Message) (seoPost, bool) {
	if m.Deleted || m.IsAds || m.ID <= 0 {
		return seoPost{}, false
	}
	text, image := postPlainText(m.Text)
	return seoPost{ID: m.ID, Text: text, Image: image, Published: m.Timestamp, Edited: m.LastEdit}, true
}

// latestSeoPosts is the newest posts of a channel, newest first. A failure
// is an empty list: the page is still served, only without them.
func latestSeoPosts(ctx context.Context, slug string, n int) []seoPost {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// A few more than needed, since ads are dropped after the read.
	msgs, err := funcGetMessageRange(ctx, slug, 0, int64(n+5), false, false, false, "desc", "")
	if err != nil {
		return nil
	}
	posts := make([]seoPost, 0, n)
	for _, m := range msgs {
		if p, ok := seoPostFrom(m); ok && p.Text != "" {
			posts = append(posts, p)
			if len(posts) == n {
				break
			}
		}
	}
	return posts
}

// loadSeoPost reads one post. found is false when there is no such public
// post; err is set only when the lookup itself failed.
func loadSeoPost(ctx context.Context, slug string, id int) (p seoPost, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	f, err := dbGetMessageFields(ctx, slug, strconv.Itoa(id))
	if err == redis.Nil {
		return seoPost{}, false, nil
	}
	if err != nil {
		return seoPost{}, false, err
	}
	m := Message{
		ID:      id,
		Text:    f["text"],
		Deleted: f["deleted"] == "1",
		IsAds:   f["is_ads"] == "1",
	}
	m.Timestamp, _ = time.Parse(time.RFC3339Nano, f["timestamp"])
	m.LastEdit, _ = time.Parse(time.RFC3339Nano, f["last_edit"])
	p, ok := seoPostFrom(m)
	return p, ok, nil
}

func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// postTitleText is the post's first line, short enough for a result title.
func postTitleText(p seoPost) string {
	first, _, _ := strings.Cut(p.Text, "\n")
	return cutRunes(first, postTitleMax)
}

func postPath(slug string, id int) string {
	return "/channel/" + slug + "/post/" + strconv.Itoa(id)
}

// postTextHTML renders plain post text: blank lines are paragraphs, single
// line breaks stay line breaks.
func postTextHTML(text string) string {
	var b strings.Builder
	for _, para := range strings.Split(text, "\n\n") {
		lines := strings.Split(para, "\n")
		for i, l := range lines {
			lines[i] = html.EscapeString(l)
		}
		b.WriteString("<p>" + strings.Join(lines, "<br>") + "</p>")
	}
	return b.String()
}

// hebrewDate is a post's date as the page shows it, e.g. 15.10.2026.
func hebrewDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(israelTime).Format("2.1.2006")
}

var israelTime = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jerusalem"); err == nil {
		return loc
	}
	return time.FixedZone("IST", 2*60*60)
}()

func timeTag(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return `<time datetime="` + t.UTC().Format(time.RFC3339) + `">` + hebrewDate(t) + `</time>`
}

// channelPostsHTML is the newest posts, each linking to its own page.
func channelPostsHTML(slug string, posts []seoPost) string {
	if len(posts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<h2>הודעות אחרונות</h2>")
	for _, p := range posts {
		b.WriteString("<article>")
		b.WriteString(`<p><a href="` + postPath(slug, p.ID) + `">` + fallback(timeTag(p.Published), "להודעה") + `</a></p>`)
		b.WriteString(postTextHTML(cutRunes(p.Text, channelPagePostChars)))
		b.WriteString("</article>")
	}
	return b.String()
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// postStaticBody is one post's page as a crawler that runs no script sees it.
func postStaticBody(siteName, slug, channelName string, p seoPost, title string) string {
	var b strings.Builder
	b.WriteString(staticShellStyle)
	b.WriteString(`<main class="ssr">`)
	b.WriteString(`<p><a href="/channel/` + slug + `">` + html.EscapeString(channelName) + `</a></p>`)
	b.WriteString("<article>")
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	if t := timeTag(p.Published); t != "" {
		b.WriteString("<p>" + t + "</p>")
	}
	b.WriteString(postTextHTML(p.Text))
	b.WriteString("</article>")
	b.WriteString(`<p><a href="/channel/` + slug + `">לכל ההודעות בערוץ ` + html.EscapeString(channelName) + `</a></p>`)
	b.WriteString(`<p><a href="/">פתיחת ערוץ עדכונים משלכם ב` + html.EscapeString(siteName) + `, בחינם</a></p>`)
	b.WriteString("</main>")
	return b.String()
}

// postStructuredData describes one post as a SocialMediaPosting inside its
// channel, with a breadcrumb site → channel → post.
func postStructuredData(origin, siteName, channelName string, slug string, p seoPost, title, image string) []string {
	url := origin + postPath(slug, p.ID)
	channelURL := origin + "/channel/" + slug
	posting := map[string]any{
		"@type":            "SocialMediaPosting",
		"@id":              url,
		"url":              url,
		"headline":         title,
		"articleBody":      p.Text,
		"inLanguage":       "he",
		"image":            image,
		"mainEntityOfPage": url,
		"isPartOf":         map[string]any{"@id": channelURL},
		"author":           map[string]any{"@type": "Organization", "name": channelName, "url": channelURL},
	}
	if !p.Published.IsZero() {
		posting["datePublished"] = p.Published.UTC().Format(time.RFC3339)
	}
	if !p.Edited.IsZero() && p.Edited.After(p.Published) {
		posting["dateModified"] = p.Edited.UTC().Format(time.RFC3339)
	}
	return []string{jsonLD(map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			posting,
			websiteLD(origin, siteName),
			breadcrumbLD([2]string{siteName, origin + "/"}, [2]string{channelName, channelURL}, [2]string{title, url}),
		},
	})}
}

// buildPostPageMeta is buildPageMeta for /channel/<slug>/post/<id>. It
// follows the channel page's rules: a channel a stranger may not read says
// nothing; a channel its managers took out of search still unfurls a
// forwarded post but is not listed; a missing post is a real 404.
func buildPostPageMeta(ctx context.Context, origin, siteName string, m pageMeta, slug, idStr string) pageMeta {
	ch, vis := lookupPageChannel(ctx, slug)
	switch vis {
	case channelHidden:
		m.NoIndex = true
		return m
	case channelMissing:
		m.Status, m.NoIndex = 404, true
		return m
	case channelUnknown:
		return m
	}
	id, _ := strconv.Atoi(idStr)
	p, found, err := loadSeoPost(ctx, slug, id)
	if err != nil {
		// Same as a failed channel lookup: neutral, never a 404 for a real post.
		return m
	}
	if !found {
		m.Status, m.NoIndex = 404, true
		return m
	}
	name := strings.TrimSpace(ch.Name)
	if name == "" {
		name = ch.Slug
	}
	title := postTitleText(p)
	m.Title = title + " · " + name
	m.Description = previewText(p.Text)
	if p.Text == "" {
		// A picture alone: the channel's own title and description.
		title = name
		m.Title = channelSearchTitle(ch, name, siteName)
		m.Description = channelSearchDescription(ch, name)
	}
	m.ImageAlt = name
	if !ch.Features.RequireAuthFiles {
		if p.Image != "" {
			m.Image = absoluteURL(origin, p.Image)
		} else if logo := strings.TrimSpace(ch.LogoUrl); logo != "" {
			m.Image = absoluteURL(origin, logo)
			m.Card = "summary"
		}
	}
	m.Body = postStaticBody(siteName, ch.Slug, name, p, title)
	if ch.SeoHidden || p.Text == "" {
		m.NoIndex = true
		return m
	}
	m.Canonical = postPath(ch.Slug, p.ID)
	m.StructuredData = postStructuredData(origin, siteName, name, ch.Slug, p, title, m.Image)
	return m
}

// sitemapPostsPerChannel is how many of each channel's newest posts the
// sitemap lists. Older posts stay reachable through the channel page.
const sitemapPostsPerChannel = 100

// channelPostEntries lists the newest public posts of each channel, for the
// sitemap: two pipelined round trips per batch of channels.
func channelPostEntries(ctx context.Context, slugs []string) ([]sitemapEntry, error) {
	var entries []sitemapEntry
	for start := 0; start < len(slugs); start += dbListChannelsBatch {
		batch := slugs[start:min(start+dbListChannelsBatch, len(slugs))]
		pipe := rdb.Pipeline()
		idx := make([]*redis.ZSliceCmd, len(batch))
		for i, slug := range batch {
			idx[i] = pipe.ZRevRangeWithScores(ctx, fmt.Sprintf("channel:%s:m_times", slug), 0, sitemapPostsPerChannel-1)
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, err
		}

		type cand struct {
			slug string
			id   int
			at   time.Time
			cmd  *redis.SliceCmd
		}
		var cands []cand
		pipe = rdb.Pipeline()
		for i, slug := range batch {
			zs, err := idx[i].Result()
			if err != nil {
				continue
			}
			prefix := fmt.Sprintf("channel:%s:messages:", slug)
			for _, z := range zs {
				key, _ := z.Member.(string)
				id, err := strconv.Atoi(strings.TrimPrefix(key, prefix))
				if err != nil || !strings.HasPrefix(key, prefix) || id <= 0 {
					continue
				}
				cands = append(cands, cand{slug, id, time.Unix(int64(z.Score), 0),
					pipe.HMGet(ctx, key, "deleted", "is_ads", "last_edit", "text")})
			}
		}
		if len(cands) == 0 {
			continue
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, err
		}
		for _, c := range cands {
			v, err := c.cmd.Result()
			if err != nil || len(v) != 4 {
				continue
			}
			str := func(i int) string { s, _ := v[i].(string); return s }
			msg := Message{ID: c.id, Deleted: str(0) == "1", IsAds: str(1) == "1", Text: str(3), Timestamp: c.at}
			msg.LastEdit, _ = time.Parse(time.RFC3339Nano, str(2))
			if p, ok := seoPostFrom(msg); !ok || p.Text == "" {
				continue
			}
			last := c.at
			if msg.LastEdit.After(last) {
				last = msg.LastEdit
			}
			entries = append(entries, sitemapEntry{Path: postPath(c.slug, c.id), LastMod: last, Priority: priorityPost})
		}
	}
	return entries, nil
}
