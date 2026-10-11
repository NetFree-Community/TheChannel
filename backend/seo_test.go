package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const seoTestIndex = `<!doctype html>
<html dir="rtl" lang="he">
<head>
  <meta charset="utf-8">
  <title>הערוץ (ברירת מחדל)</title>
  <meta name="description" content="generic description">
</head>
<body><app-root></app-root></body>
</html>`

func withSpaFolder(t *testing.T, customTitle, projectDomain string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(seoTestIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := getGlobalConfig()
	setGlobalConfigCache(&SettingConfig{RootStaticFolder: dir, CustomTitle: customTitle, ProjectDomain: projectDomain})
	t.Cleanup(func() { setGlobalConfigCache(prev) })
}

func seedPreviewChannel(t *testing.T, ctx context.Context, slug string, ch ChannelData) {
	t.Helper()
	ch.Slug = slug
	ch.CreatedAt = time.Now()
	if err := dbCreateChannel(ctx, &ch); err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dbDeleteChannel(cctx, slug)
	})
}

func spaPage(t *testing.T, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "example.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	serveSpaFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	return rec.Body.String()
}

func mustContain(t *testing.T, page string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(page, p) {
			t.Errorf("page is missing %q\n%s", p, page)
		}
	}
}

// A forwarded channel link must unfurl with the channel's own name,
// description and logo; the rest of the app gets the platform card.
func TestServeSpaFileInjectsChannelPreview(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "", "")
	slug := "seo-public-" + strings.ToLower(t.Name()[len("TestServeSpaFile"):])
	seedPreviewChannel(t, ctx, slug, ChannelData{
		Name:        `חדשות <השכונה>`,
		Description: "  עדכונים   יומיים\nלכל התושבים  ",
		LogoUrl:     "/api/channel/" + slug + "/files/abcd1234",
		Features:    defaultChannelFeatures(),
	})

	page := spaPage(t, "/channel/"+slug)
	mustContain(t, page,
		`<title>חדשות &lt;השכונה&gt; · הערוץ</title>`,
		`<meta name="description" content="עדכונים יומיים לכל התושבים">`,
		`<meta property="og:title" content="חדשות &lt;השכונה&gt; · הערוץ">`,
		`<meta property="og:description" content="עדכונים יומיים לכל התושבים">`,
		`<meta property="og:image" content="https://example.test/api/channel/`+slug+`/files/abcd1234">`,
		`<meta property="og:url" content="https://example.test/channel/`+slug+`">`,
		`<meta name="twitter:card" content="summary">`,
	)
	if strings.Count(page, `name="description"`) != 1 {
		t.Errorf("expected exactly one description meta, got:\n%s", page)
	}

	landing := spaPage(t, "/")
	mustContain(t, landing,
		`<title>הערוץ</title>`,
		`<meta name="description" content="generic description">`,
		`<meta property="og:image" content="https://example.test/assets/og.jpg">`,
		`<meta name="twitter:card" content="summary_large_image">`,
	)
	if strings.Contains(landing, "og:description") {
		t.Errorf("landing must keep the static description only:\n%s", landing)
	}
}

// A private or disabled channel must not leak its name through the preview,
// and the operator's title and domain take precedence when set.
func TestServeSpaFilePreviewRespectsPrivacyAndOperatorSettings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withSpaFolder(t, "הקהילה שלנו", "https://channels.example.org/")

	private := "seo-private-" + strings.ToLower(t.Name()[len("TestServeSpaFile"):])
	f := defaultChannelFeatures()
	f.RequireAuth = true
	seedPreviewChannel(t, ctx, private, ChannelData{Name: "סודי", Description: "לא לפרסום", Features: f})

	page := spaPage(t, "/channel/"+private)
	if strings.Contains(page, "סודי") || strings.Contains(page, "לא לפרסום") {
		t.Fatalf("private channel leaked into the preview:\n%s", page)
	}
	mustContain(t, page,
		`<title>הקהילה שלנו</title>`,
		`<meta property="og:site_name" content="הקהילה שלנו">`,
		`<meta property="og:url" content="https://channels.example.org/channel/`+private+`">`,
		`<meta property="og:image" content="https://channels.example.org/assets/og.jpg">`,
	)

	// A logo behind the files gate cannot be fetched by a crawler: fall back
	// to the platform card rather than show a broken image.
	gated := "seo-gated-" + strings.ToLower(t.Name()[len("TestServeSpaFile"):])
	g := defaultChannelFeatures()
	g.RequireAuthFiles = true
	seedPreviewChannel(t, ctx, gated, ChannelData{Name: "שער", LogoUrl: "/api/channel/" + gated + "/files/zzzz9999", Features: g})
	mustContain(t, spaPage(t, "/channel/"+gated),
		`<title>שער · הקהילה שלנו</title>`,
		`<meta property="og:image" content="https://channels.example.org/assets/og.jpg">`,
	)

	if got := previewText(strings.Repeat("א", 200)); len([]rune(got)) != previewDescriptionMax {
		t.Errorf("previewText length = %d runes, want %d", len([]rune(got)), previewDescriptionMax)
	}
}
