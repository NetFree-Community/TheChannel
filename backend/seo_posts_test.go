package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func seedPost(t *testing.T, ctx context.Context, slug string, m Message) int {
	t.Helper()
	id, err := getMessageNextId(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	m.ID = id
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}
	if err := setMessage(ctx, slug, &m, false); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostPlainText(t *testing.T) {
	text, image := postPlainText("[quote-embedded#](3@ציטוט)\n# **כותרת** חשובה\n[image-embedded#800x600](/api/channel/x/files/abc)\nראו [כאן](https://example.org) ~~עוד~~\n\n\n\n> שורה שנייה")
	if want := "כותרת חשובה\n\nראו כאן עוד\n\nשורה שנייה"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if image != "/api/channel/x/files/abc" {
		t.Errorf("image = %q", image)
	}
}

// A public channel's page carries its newest posts as text, each linking to
// the post's own page; ads and deleted posts are left out.
func TestChannelPageListsPosts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-posts-list"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "חדשות", Features: defaultChannelFeatures()})
	kept := seedPost(t, ctx, slug, Message{Text: "**מבזק**: נפתח <גן> חדש"})
	seedPost(t, ctx, slug, Message{Text: "מודעה בתשלום", IsAds: true})
	gone := seedPost(t, ctx, slug, Message{Text: "הודעה שנמחקה"})
	if err := funcDeleteMessage(ctx, slug, strconv.Itoa(gone), "test"); err != nil {
		t.Fatal(err)
	}

	page := spaPage(t, "/channel/"+slug)
	mustContain(t, page,
		`<h2>הודעות אחרונות</h2>`,
		`href="/channel/`+slug+`/post/`+strconv.Itoa(kept)+`"`,
		`<p>מבזק: נפתח &lt;גן&gt; חדש</p>`,
	)
	for _, bad := range []string{"מודעה בתשלום", "הודעה שנמחקה", "<גן>"} {
		if strings.Contains(page, bad) {
			t.Errorf("channel page contains %q", bad)
		}
	}
}

func TestPostPage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-post-page"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "חדשות העיר", Features: defaultChannelFeatures()})
	id := seedPost(t, ctx, slug, Message{Text: "נפתח גן ציבורי חדש ברחוב הרצל\nהטקס יתקיים ביום שני", Timestamp: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)})
	path := "/channel/" + slug + "/post/" + strconv.Itoa(id)

	page := spaPage(t, path)
	mustContain(t, page,
		`<title>נפתח גן ציבורי חדש ברחוב הרצל · חדשות העיר</title>`,
		`<meta name="description" content="נפתח גן ציבורי חדש ברחוב הרצל הטקס יתקיים ביום שני">`,
		`<link rel="canonical" href="https://example.test`+path+`">`,
		`<meta name="robots" content="index, follow, max-image-preview:large">`,
		`<h1>נפתח גן ציבורי חדש ברחוב הרצל</h1>`,
		`<p>נפתח גן ציבורי חדש ברחוב הרצל<br>הטקס יתקיים ביום שני</p>`,
		`"@type":"SocialMediaPosting"`,
		`"datePublished":"2026-10-15T09:00:00Z"`,
		`<time datetime="2026-10-15T09:00:00Z">15.10.2026</time>`,
	)

	// No such post, an ad, a deleted post: a real 404 with noindex.
	ad := seedPost(t, ctx, slug, Message{Text: "מודעה", IsAds: true})
	for _, p := range []string{"/channel/" + slug + "/post/999999", "/channel/" + slug + "/post/" + strconv.Itoa(ad)} {
		rec := seoGet(t, p)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `content="noindex"`) {
			t.Errorf("GET %s: %d, want a noindex 404", p, rec.Code)
		}
	}

	// Taken out of search by its managers: still unfurls, never listed.
	if err := rdb.HSet(ctx, "channel:"+slug, "seoHidden", "1").Err(); err != nil {
		t.Fatal(err)
	}
	page = spaPage(t, path)
	mustContain(t, page, `<meta name="robots" content="noindex">`, `<h1>נפתח גן ציבורי חדש ברחוב הרצל</h1>`)
	if strings.Contains(page, `rel="canonical"`) {
		t.Errorf("a hidden channel's post has a canonical URL")
	}
}

// A private channel's post page says nothing about the post.
func TestPrivateChannelPostPage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-post-private"
	f := defaultChannelFeatures()
	f.RequireAuth = true
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "סודי", Features: f})
	id := seedPost(t, ctx, slug, Message{Text: "תוכן פרטי"})
	page := spaPage(t, "/channel/"+slug+"/post/"+strconv.Itoa(id))
	if strings.Contains(page, "תוכן פרטי") || !strings.Contains(page, `content="noindex"`) {
		t.Fatalf("private post leaked or indexable:\n%s", page)
	}
}

func TestSitemapListsPosts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "https://example.test")
	const slug = "seo-post-sitemap"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "מפה", Features: defaultChannelFeatures()})
	id := seedPost(t, ctx, slug, Message{Text: "הודעה למפה"})
	ad := seedPost(t, ctx, slug, Message{Text: "מודעה", IsAds: true})
	pic := seedPost(t, ctx, slug, Message{Text: "[image-embedded#](/api/channel/x/files/a)"})
	resetSitemapCache()
	t.Cleanup(resetSitemapCache)

	locs := sitemapLocs(t)
	base := "https://example.test/channel/" + slug + "/post/"
	if u, ok := locs[base+strconv.Itoa(id)]; !ok || u.Priority != priorityPost {
		t.Errorf("sitemap is missing the post (or its priority): %+v", u)
	}
	for _, other := range []int{ad, pic} {
		if _, ok := locs[base+strconv.Itoa(other)]; ok {
			t.Errorf("sitemap lists post %d, which has nothing to index", other)
		}
	}
}

func TestRobotsAllowsChannelReads(t *testing.T) {
	body := seoGet(t, "/robots.txt").Body.String()
	for _, want := range []string{"Allow: /api/channel/*/messages", "Allow: /api/channel/*/info", "Disallow: /api/"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt is missing %q:\n%s", want, body)
		}
	}
}

func TestIsCrawler(t *testing.T) {
	for ua, want := range map[string]bool{
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)": true,
		"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)":  true,
		"WhatsApp/2.23.20.0": true,
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/129.0 Mobile Safari/537.36": false,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15":          false,
	} {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("User-Agent", ua)
		if got := isCrawler(r); got != want {
			t.Errorf("isCrawler(%q) = %v", ua, got)
		}
	}
}
