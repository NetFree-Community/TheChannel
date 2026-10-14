package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi"
)

var ldRe = regexp.MustCompile(`<script type="application/ld\+json">(.*?)</script>`)

func sitemapLocs(t *testing.T) map[string]xmlURL {
	t.Helper()
	rec := seoGet(t, "/sitemap.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("sitemap: %d", rec.Code)
	}
	var set xmlURLSet
	if err := xml.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("sitemap is not a urlset: %v", err)
	}
	got := map[string]xmlURL{}
	for _, u := range set.URLs {
		got[u.Loc] = u
	}
	return got
}

// The managers' search title and description reach every tag a search
// engine or a link preview reads, escaped for HTML and for JSON-LD alike.
func TestChannelSeoTextIsUsedAndEscaped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-custom-text"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "חדשות", Description: "התיאור הרגיל", Features: defaultChannelFeatures()})

	title := `כותרת "מיוחדת" </script><script>alert(1)</script>`
	desc := "תיאור\nלגוגל & <b>עוד</b>"
	body, _ := json.Marshal(map[string]string{"seoTitle": title, "seoDescription": desc})
	callEditChannelInfo(t, slug, string(body), http.StatusOK)

	got, err := dbGetChannel(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.SeoDescription != "תיאור לגוגל & <b>עוד</b>" {
		t.Errorf("stored description %q: line breaks must fold to one line", got.SeoDescription)
	}

	page := spaPage(t, "/channel/"+slug)
	escTitle := `כותרת &#34;מיוחדת&#34; &lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;`
	escDesc := `תיאור לגוגל &amp; &lt;b&gt;עוד&lt;/b&gt;`
	mustContain(t, page,
		`<title>`+escTitle+`</title>`,
		`<meta name="description" content="`+escDesc+`">`,
		`<meta property="og:title" content="`+escTitle+`">`,
		`<meta property="og:description" content="`+escDesc+`">`,
		`<meta name="twitter:title" content="`+escTitle+`">`,
		`<meta name="twitter:description" content="`+escDesc+`">`,
		`<meta name="robots" content="index, follow, max-image-preview:large">`,
		// The page itself still names the channel.
		`<h1>חדשות</h1>`,
	)
	if strings.Contains(page, "<script>alert(1)") || strings.Contains(page, "<b>עוד") {
		t.Fatalf("search text reached the page unescaped:\n%s", page)
	}

	m := ldRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no JSON-LD on a listed channel")
	}
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("invalid JSON-LD: %v\n%s", err, m[1])
	}
	found := false
	for _, n := range doc.Graph {
		if n["@type"] == "CollectionPage" {
			found = true
			if n["name"] != `כותרת "מיוחדת" </script><script>alert(1)</script>` || n["description"] != "תיאור לגוגל & <b>עוד</b>" {
				t.Errorf("CollectionPage carries %q / %q", n["name"], n["description"])
			}
		}
	}
	if !found {
		t.Errorf("JSON-LD lacks the CollectionPage")
	}

	// Cleared, the defaults come back.
	callEditChannelInfo(t, slug, `{"seoTitle":"","seoDescription":"   "}`, http.StatusOK)
	mustContain(t, spaPage(t, "/channel/"+slug),
		`<title>חדשות · הערוץ</title>`,
		`<meta name="description" content="התיאור הרגיל">`,
	)
}

func TestChannelSeoLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-limits"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "גבולות", Features: defaultChannelFeatures()})

	long := func(n int) string { return strings.Repeat("א", n) }
	callEditChannelInfo(t, slug, `{"seoTitle":"`+long(maxSeoTitleLen)+`"}`, http.StatusOK)
	callEditChannelInfo(t, slug, `{"seoTitle":"`+long(maxSeoTitleLen+1)+`"}`, http.StatusBadRequest)
	callEditChannelInfo(t, slug, `{"seoDescription":"`+long(maxSeoDescriptionLen)+`"}`, http.StatusOK)
	callEditChannelInfo(t, slug, `{"seoDescription":"`+long(maxSeoDescriptionLen+1)+`"}`, http.StatusBadRequest)
	// A refused request writes nothing, the name included.
	callEditChannelInfo(t, slug, `{"name":"שם אחר","seoTitle":"`+long(maxSeoTitleLen+1)+`"}`, http.StatusBadRequest)
	if got, _ := dbGetChannel(ctx, slug); got.Name != "גבולות" || got.SeoTitle != long(maxSeoTitleLen) {
		t.Errorf("after a refused edit: name %q, title %q", got.Name, got.SeoTitle)
	}
	// A request that sends no search field leaves the search settings alone.
	callEditChannelInfo(t, slug, `{"description":"חדש"}`, http.StatusOK)
	if got, _ := dbGetChannel(ctx, slug); got.SeoTitle != long(maxSeoTitleLen) {
		t.Errorf("an unrelated edit cleared the search title: %q", got.SeoTitle)
	}
}

// Switched off, a public channel leaves the sitemap at once (not when the
// cache expires) and its page says noindex in the markup and in the header,
// with no structured data — while the page keeps working for people.
func TestChannelSeoHidden(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-hidden-toggle"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "נסתר", Description: "תיאור", Features: defaultChannelFeatures()})
	resetSitemapCache()
	t.Cleanup(resetSitemapCache)

	loc := "https://example.test/channel/" + slug
	// Listed by default: the state every existing channel is in.
	if _, ok := sitemapLocs(t)[loc]; !ok {
		t.Fatalf("a public channel is not listed by default")
	}
	if rec := seoGet(t, "/channel/"+slug); rec.Header().Get("X-Robots-Tag") != "" {
		t.Errorf("a listed channel sent X-Robots-Tag %q", rec.Header().Get("X-Robots-Tag"))
	}

	callEditChannelInfo(t, slug, `{"seoHidden":true}`, http.StatusOK)
	// The sitemap above is cached; the save must have dropped it.
	if _, ok := sitemapLocs(t)[loc]; ok {
		t.Errorf("a channel switched off is still in the sitemap")
	}
	rec := seoGet(t, "/channel/"+slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("hidden channel page: %d", rec.Code)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
	page := rec.Body.String()
	mustContain(t, page,
		`<meta name="robots" content="noindex">`,
		// Still a working page with a working preview.
		`<title>נסתר · הערוץ</title>`,
		`<meta property="og:title" content="נסתר · הערוץ">`,
		`<h1>נסתר</h1>`,
	)
	if ldRe.MatchString(page) {
		t.Errorf("a hidden channel still sends structured data")
	}
	if strings.Contains(page, `rel="canonical"`) {
		t.Errorf("a hidden channel still claims a canonical URL")
	}

	callEditChannelInfo(t, slug, `{"seoHidden":false}`, http.StatusOK)
	if _, ok := sitemapLocs(t)[loc]; !ok {
		t.Errorf("switched back on, the channel did not return to the sitemap")
	}
	if !strings.Contains(spaPage(t, "/channel/"+slug), `"@type":"CollectionPage"`) {
		t.Errorf("switched back on, the structured data did not return")
	}
}

// The super admin edits the same settings, a disabled channel's included
// (the channel routes refuse a disabled channel outright).
func TestSuperAdminSetChannelSeo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-sa-disabled"
	f := defaultChannelFeatures()
	f.Disabled = true
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "מושבת", Features: f})

	call := func(slug, body string, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/super-admin/channels/"+slug+"/seo", bytes.NewBufferString(body))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("slug", slug)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		superAdminSetChannelSeo(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d, want %d (%s)", slug, body, rec.Code, want, rec.Body.String())
		}
	}
	call(slug, `{"seoHidden":true,"seoTitle":"כותרת","seoDescription":"תיאור"}`, http.StatusOK)
	got, err := dbGetChannel(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SeoHidden || got.SeoTitle != "כותרת" || got.SeoDescription != "תיאור" {
		t.Errorf("stored %+v", got)
	}
	call(slug, `{"seoTitle":"`+strings.Repeat("א", maxSeoTitleLen+1)+`"}`, http.StatusBadRequest)
	call("seo-sa-no-such-channel", `{"seoHidden":true}`, http.StatusNotFound)
	if n, _ := rdb.Exists(ctx, "channel:seo-sa-no-such-channel").Result(); n != 0 {
		t.Errorf("a missing channel got a hash")
	}

	// Disabled stays out of search whatever the toggle says.
	call(slug, `{"seoHidden":false}`, http.StatusOK)
	resetSitemapCache()
	t.Cleanup(resetSitemapCache)
	if _, ok := sitemapLocs(t)["https://example.test/channel/"+slug]; ok {
		t.Errorf("a disabled channel is in the sitemap")
	}
}

// The home page is the site's most important URL, and every public channel
// page links back to it in its first HTML.
func TestSitemapPrioritiesAndChannelLinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	const slug = "seo-priority"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "עדיפות", Features: defaultChannelFeatures()})
	resetSitemapCache()
	t.Cleanup(resetSitemapCache)

	locs := sitemapLocs(t)
	if p := locs["https://example.test/"].Priority; p != priorityHome {
		t.Errorf("home priority %q, want %q", p, priorityHome)
	}
	for _, c := range contentPages {
		if p := locs["https://example.test"+c.Path].Priority; p != priorityGuide {
			t.Errorf("%s priority %q, want %q", c.Path, p, priorityGuide)
		}
	}
	if p := locs["https://example.test/channel/"+slug].Priority; p != priorityChannel {
		t.Errorf("channel priority %q, want %q", p, priorityChannel)
	}
	for _, u := range locs {
		if u.Loc != "https://example.test/" && u.Priority >= priorityHome {
			t.Errorf("%s ranks with the home page (%s)", u.Loc, u.Priority)
		}
	}

	mustContain(t, spaPage(t, "/channel/"+slug), `<a href="/">פתיחת ערוץ עדכונים משלכם, בחינם</a>`)
}

// The NetFree guide shows a channel address on this deployment's own domain,
// and every guide links to the home page and to each other guide.
func TestGuidePagesLinkTogether(t *testing.T) {
	withSpaFolder(t, "", "https://channels.example.org")
	mustContain(t, seoGet(t, "/netfree-channel").Body.String(),
		`<p class="url" dir="ltr">https://channels.example.org/channel/your-channel</p>`,
		faqNetFree.Q,
	)
	mustContain(t, seoGet(t, "/whatsapp-alternative").Body.String(), faqWhatsApp.Q)
	for _, p := range contentPages {
		body := seoGet(t, p.Path).Body.String()
		mustContain(t, body, `<a class="brand" href="/">`, `<a href="/">הערוץ: פתיחת ערוץ עדכונים בחינם</a>`)
		for _, o := range contentPages {
			if o.Path != p.Path && !strings.Contains(body, `<a href="`+o.Path+`">`) {
				t.Errorf("%s does not link to %s", p.Path, o.Path)
			}
		}
	}
	home := spaPage(t, "/")
	for _, p := range contentPages {
		if !strings.Contains(home, `<a href="`+p.Path+`">`) {
			t.Errorf("the home page does not link to %s", p.Path)
		}
	}
	mustContain(t, home, faqNetFree.Q, faqWhatsApp.Q)
}
