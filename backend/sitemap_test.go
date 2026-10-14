package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

func seoRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/robots.txt", getRobotsTxt)
	r.Get("/sitemap.xml", getSitemap)
	r.Get("/sitemap-pages.xml", getSitemapPages)
	r.Get("/sitemap-channels-{n:[0-9]+}.xml", getSitemapChannels)
	for _, p := range contentPages {
		page := p
		h := func(w http.ResponseWriter, r *http.Request) { serveContentPage(w, r, &page, getGlobalConfig()) }
		r.Get(page.Path, h)
		r.Get(page.Path+"/", h)
	}
	r.NotFound(serveSpaFile)
	return r
}

func seoGet(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "example.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	seoRouter().ServeHTTP(rec, req)
	return rec
}

func TestRobotsTxt(t *testing.T) {
	withSpaFolder(t, "", "https://channels.example.org")
	rec := seoGet(t, "/robots.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"User-agent: *",
		"Disallow: /api/",
		"Disallow: /auth/",
		"Disallow: /login",
		"Disallow: /super-admin",
		"Disallow: /channel/*/manage",
		"Sitemap: https://channels.example.org/sitemap.xml",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt is missing %q:\n%s", want, body)
		}
	}
	// Blocking /channel/ would hide every public channel from search.
	if regexp.MustCompile(`(?m)^Disallow: /channel/?$`).MatchString(body) {
		t.Errorf("robots.txt blocks the channel pages:\n%s", body)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("content type %q", rec.Header().Get("Content-Type"))
	}
}

// Only channels a stranger may read belong in the sitemap.
func TestSitemapListsOnlyPublicChannels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")

	pub := "seo-sm-public"
	priv := "seo-sm-private"
	off := "seo-sm-disabled"
	seedPreviewChannel(t, ctx, pub, ChannelData{Name: "ציבורי", Features: defaultChannelFeatures()})
	pf := defaultChannelFeatures()
	pf.RequireAuth = true
	seedPreviewChannel(t, ctx, priv, ChannelData{Name: "פרטי", Features: pf})
	df := defaultChannelFeatures()
	df.Disabled = true
	seedPreviewChannel(t, ctx, off, ChannelData{Name: "מושבת", Features: df})

	// The newest message dates the public channel.
	// Later than the channel's creation (now), so it is the newest date.
	posted := time.Now().UTC().AddDate(0, 0, 2)
	if err := rdb.ZAdd(ctx, "channel:"+pub+":m_times", redis.Z{Score: float64(posted.Unix()), Member: "channel:" + pub + ":messages:1"}).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rdb.Del(context.Background(), "channel:"+pub+":m_times") })

	resetSitemapCache()
	t.Cleanup(resetSitemapCache)

	rec := seoGet(t, "/sitemap.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var set xmlURLSet
	if err := xml.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("sitemap is not a urlset: %v\n%s", err, rec.Body.String())
	}
	got := map[string]string{}
	for _, u := range set.URLs {
		got[u.Loc] = u.LastMod
	}
	for _, want := range []string{"https://example.test/", "https://example.test/open-channel", "https://example.test/community-channel"} {
		if _, ok := got[want]; !ok {
			t.Errorf("sitemap is missing %s", want)
		}
	}
	if lm, ok := got["https://example.test/channel/"+pub]; !ok {
		t.Errorf("sitemap is missing the public channel")
	} else if lm != posted.Format("2006-01-02") {
		t.Errorf("public channel lastmod = %q, want the newest message's date", lm)
	}
	for _, hidden := range []string{priv, off} {
		if _, ok := got["https://example.test/channel/"+hidden]; ok {
			t.Errorf("sitemap lists %s, which a stranger may not read", hidden)
		}
	}
	if strings.Contains(rec.Body.String(), "פרטי") {
		t.Errorf("sitemap leaks a private channel's name")
	}
}

func TestSitemapIndexPaging(t *testing.T) {
	withSpaFolder(t, "", "")
	// Fill the cache directly: building ten thousand channels in Redis
	// would test Redis, not the paging.
	entries := make([]sitemapEntry, sitemapMaxURLs+5)
	for i := range entries {
		entries[i] = sitemapEntry{Path: "/channel/c" + "xxx" + strconv.Itoa(i)}
	}
	sitemapCache.Lock()
	sitemapCache.channels = entries
	sitemapCache.built = time.Now()
	sitemapCache.Unlock()
	t.Cleanup(resetSitemapCache)

	rec := seoGet(t, "/sitemap.xml")
	var idx xmlSitemapIndex
	if err := xml.Unmarshal(rec.Body.Bytes(), &idx); err != nil || idx.XMLName.Local != "sitemapindex" {
		t.Fatalf("expected a sitemap index, got %v\n%s", err, rec.Body.String())
	}
	if len(idx.Sitemaps) != 3 {
		t.Fatalf("index has %d sitemaps, want pages + 2 channel files", len(idx.Sitemaps))
	}
	var second xmlURLSet
	if err := xml.Unmarshal(seoGet(t, "/sitemap-channels-2.xml").Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.URLs) != 5 {
		t.Errorf("second channel file has %d URLs, want 5", len(second.URLs))
	}
	if c := seoGet(t, "/sitemap-channels-3.xml").Code; c != http.StatusNotFound {
		t.Errorf("past the last file: %d, want 404", c)
	}
}

// Paths that are not pages answer 404 (the app is still served, so a
// person is not stranded), and app-only screens are noindex.
func TestSpaStatusAndRobotsMeta(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")

	for _, c := range []struct {
		path    string
		status  int
		noindex bool
	}{
		{"/", 200, false},
		{"/login", 200, true},
		{"/channel", 200, true},
		{"/super-admin", 200, true},
		{"/channel/whatever/manage", 200, true},
		{"/channel/whatever/manage/users", 200, true},
		{"/no-such-page", 404, true},
		{"/wp-login.php", 404, true},
		{"/channel/seo-no-such-channel", 404, true},
	} {
		rec := seoGet(t, c.path)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.path, rec.Code, c.status)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<app-root") {
			t.Errorf("%s: the app is not served", c.path)
		}
		if got := strings.Contains(body, `<meta name="robots" content="noindex">`); got != c.noindex {
			t.Errorf("%s: noindex = %v, want %v", c.path, got, c.noindex)
		}
		if c.noindex && strings.Contains(body, `rel="canonical"`) {
			t.Errorf("%s: a noindex page must not claim a canonical URL", c.path)
		}
	}

	// A private channel: noindex, but not a 404 — its members still open it.
	private := "seo-status-private"
	f := defaultChannelFeatures()
	f.RequireAuth = true
	seedPreviewChannel(t, ctx, private, ChannelData{Name: "סודי", Features: f})
	rec := seoGet(t, "/channel/"+private)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `content="noindex"`) {
		t.Errorf("private channel: %d, noindex=%v", rec.Code, strings.Contains(rec.Body.String(), `content="noindex"`))
	}
	if strings.Contains(rec.Body.String(), "סודי") {
		t.Errorf("private channel name leaked")
	}
}

func TestContentPages(t *testing.T) {
	withSpaFolder(t, "", "https://channels.example.org")
	for _, p := range contentPages {
		rec := seoGet(t, p.Path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", p.Path, rec.Code)
		}
		body := rec.Body.String()
		mustContain(t, body,
			`<html lang="he" dir="rtl">`,
			`<link rel="canonical" href="https://channels.example.org`+p.Path+`">`,
			`<meta name="robots" content="index, follow, max-image-preview:large">`,
			`<h1>`+p.H1+`</h1>`,
			`"@type":"FAQPage"`,
			`"@type":"BreadcrumbList"`,
		)
		if strings.Count(body, "<title>") != 1 {
			t.Errorf("%s: expected one <title>", p.Path)
		}
		// Every structured-data block must be valid JSON.
		for _, m := range regexp.MustCompile(`<script type="application/ld\+json">(.*?)</script>`).FindAllStringSubmatch(body, -1) {
			var v any
			if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
				t.Errorf("%s: invalid JSON-LD: %v", p.Path, err)
			}
		}

		red := seoGet(t, p.Path+"/")
		if red.Code != http.StatusMovedPermanently || red.Header().Get("Location") != p.Path {
			t.Errorf("%s/: %d → %q, want 301 to the canonical path", p.Path, red.Code, red.Header().Get("Location"))
		}
		if !isReservedSlug(strings.TrimPrefix(p.Path, "/")) {
			t.Errorf("%s is not a reserved slug; a channel could take its name", p.Path)
		}
	}
}

func TestSiteVerification(t *testing.T) {
	if got := verificationToken(`<meta name="google-site-verification" content="AbC-123_xyz" />`); got != "AbC-123_xyz" {
		t.Errorf("pasted tag → %q", got)
	}
	if got := verificationToken("  0123456789ABCDEF0123456789ABCDEF "); got != "0123456789ABCDEF0123456789ABCDEF" {
		t.Errorf("bare token → %q", got)
	}
	for _, bad := range []string{`"><script>alert(1)</script>`, "short", "has space inside"} {
		if got := verificationToken(bad); got != "" {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
	if err := validateSettings(&Settings{{Key: "google_site_verification", Value: `"><script>`}}); err == nil {
		t.Errorf("an injection attempt was accepted as a verification code")
	}

	withSpaFolder(t, "", "")
	cfg := *getGlobalConfig()
	cfg.GoogleSiteVerification = "google-token-1"
	cfg.BingSiteVerification = "BING0123456789"
	setGlobalConfigCache(&cfg)
	page := spaPage(t, "/")
	mustContain(t, page,
		`<meta name="google-site-verification" content="google-token-1">`,
		`<meta name="msvalidate.01" content="BING0123456789">`,
	)
}

// The home page's static FAQ, its FAQPage data and the questions list are
// one source; the JSON-LD must parse and carry every question.
func TestHomeStructuredData(t *testing.T) {
	withSpaFolder(t, "", "")
	page := spaPage(t, "/")
	m := regexp.MustCompile(`<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no JSON-LD on the home page")
	}
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("invalid JSON-LD: %v", err)
	}
	types := map[string]map[string]any{}
	for _, n := range doc.Graph {
		types[n["@type"].(string)] = n
	}
	for _, want := range []string{"Organization", "WebSite", "WebApplication", "FAQPage"} {
		if types[want] == nil {
			t.Errorf("home JSON-LD lacks %s", want)
		}
	}
	if qs, _ := types["FAQPage"]["mainEntity"].([]any); len(qs) != len(homeFAQ) {
		t.Errorf("FAQPage has %d questions, want %d", len(qs), len(homeFAQ))
	}
	for _, f := range homeFAQ {
		if !strings.Contains(page, f.Q) {
			t.Errorf("question %q is not in the static HTML", f.Q)
		}
	}
	// Nothing the product cannot back up.
	if strings.Contains(page, "aggregateRating") || strings.Contains(page, `"Review"`) {
		t.Errorf("home page claims ratings or reviews")
	}
}
