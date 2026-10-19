package main

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// channelPagePathRe matches the public page of one channel, the URL people
// forward to each other.
var channelPagePathRe = regexp.MustCompile(`^/channel/([^/]+)/?$`)

// appOnlyPathRe matches the SPA routes that exist but are nobody's search
// result: the login screen, the "my channels" hub, a channel's management
// screens and the super-admin panel. They answer 200 (the app must load) but
// carry noindex. Keep in step with frontend/src/app/app.routes.ts.
var appOnlyPathRe = regexp.MustCompile(`^/(login|channel|super-admin|channel/[^/]+/manage(/[^/]+)?)/?$`)

// descriptionMetaRe finds the generic description index.html ships, so a
// page can carry its own instead of a second, conflicting tag.
var descriptionMetaRe = regexp.MustCompile(`<meta name="description" content="[^"]*">`)

// titleRe matches the <title> index.html ships (a default, so the tab is
// never blank in a dev server either), which every served page replaces.
var titleRe = regexp.MustCompile(`<title>[^<]*</title>`)

// appRootRe is the empty mount point the SPA boots into. Whatever the server
// puts inside it is shown until Angular starts, and then replaced.
var appRootRe = regexp.MustCompile(`<app-root>\s*</app-root>`)

// defaultSiteName is the product name link previews and the tab show when
// the operator has not set a custom title.
const defaultSiteName = "הערוץ"

// previewDescriptionMax is what WhatsApp, Telegram and Google show before
// cutting a description off.
const previewDescriptionMax = 160

// ogImageWidth/Height describe frontend/public/og.jpg, so a crawler can lay
// the card out before fetching the picture.
const (
	ogImageWidth  = "1200"
	ogImageHeight = "630"
)

// requestOrigin is the absolute base of this deployment, which link previews
// need because crawlers do not resolve relative og:image / og:url values.
// The operator's configured domain wins; otherwise it is derived from the
// request, trusting the proxy's X-Forwarded-Proto only for the scheme name.
func requestOrigin(r *http.Request, cfg *SettingConfig) string {
	if cfg != nil && strings.TrimSpace(cfg.ProjectDomain) != "" {
		return strings.TrimRight(strings.TrimSpace(cfg.ProjectDomain), "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := strings.ToLower(r.Header.Get("X-Forwarded-Proto")); p == "https" || p == "http" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

// siteNameOf is the product name every page is branded with.
func siteNameOf(cfg *SettingConfig) string {
	if cfg != nil {
		if s := strings.TrimSpace(cfg.CustomTitle); s != "" {
			return s
		}
	}
	return defaultSiteName
}

// channelVisibility is what a /channel/<slug> page may say about its channel.
type channelVisibility int

const (
	// channelUnknown: the lookup failed for a reason other than "no such
	// channel" (Redis slow or down). The page is served neutrally and without
	// noindex, so a transient error never drops a real channel from search.
	channelUnknown channelVisibility = iota
	channelPublic
	// channelHidden: private or disabled. Indistinguishable from the outside
	// beyond "not indexable" — same rule getFavicon applies.
	channelHidden
	channelMissing
)

// lookupPageChannel loads the channel a /channel/<slug> page is about.
func lookupPageChannel(ctx context.Context, slug string) (*ChannelData, channelVisibility) {
	if !isPlausibleSlug(slug) {
		return nil, channelMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ch, err := dbGetChannel(ctx, slug)
	if err == redis.Nil {
		return nil, channelMissing
	}
	if err != nil {
		return nil, channelUnknown
	}
	if ch.Features.Disabled || ch.Features.RequireAuth {
		return nil, channelHidden
	}
	return ch, channelPublic
}

// previewText collapses whitespace and cuts a description to one preview line.
func previewText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= previewDescriptionMax {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:previewDescriptionMax-1])) + "…"
}

func metaProperty(prop, content string) string {
	return `<meta property="` + prop + `" content="` + html.EscapeString(content) + `">` + "\n"
}

func metaName(name, content string) string {
	return `<meta name="` + name + `" content="` + html.EscapeString(content) + `">` + "\n"
}

// absoluteURL turns the stored logo URL (an /api/channel/... path for
// uploads, or an external https URL) into something a crawler can fetch.
func absoluteURL(origin, u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	return origin + u
}

// jsonLD renders one structured-data block. json.Marshal escapes <, > and &
// as < etc., so free text (a channel name) cannot close the <script>.
func jsonLD(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return `<script type="application/ld+json">` + string(b) + `</script>` + "\n"
}

// pageMeta is everything the server says about one page before any script
// runs: the head tags crawlers and link previews read, the status code, and
// the readable content shown until the app starts.
type pageMeta struct {
	Status      int
	Title       string
	Description string
	// Canonical is a path; empty means the page has no canonical URL (it is
	// noindex, or the lookup behind it failed).
	Canonical string
	NoIndex   bool
	Image     string
	ImageAlt  string
	Card      string
	// StructuredData are complete <script type="application/ld+json"> blocks.
	StructuredData []string
	// Body is server-rendered HTML placed inside <app-root>.
	Body string
}

// siteVerificationTags are the ownership proofs Google Search Console and
// Bing Webmaster Tools look for on the home page. Emitted on every page,
// which both accept, so verification does not depend on which URL the tool
// happens to fetch.
func siteVerificationTags(cfg *SettingConfig) string {
	var b strings.Builder
	google, bing := "", ""
	if cfg != nil {
		google, bing = cfg.GoogleSiteVerification, cfg.BingSiteVerification
	}
	if google == "" {
		google = verificationToken(os.Getenv("GOOGLE_SITE_VERIFICATION"))
	}
	if bing == "" {
		bing = verificationToken(os.Getenv("BING_SITE_VERIFICATION"))
	}
	if google != "" {
		b.WriteString(metaName("google-site-verification", google))
	}
	if bing != "" {
		b.WriteString(metaName("msvalidate.01", bing))
	}
	return b.String()
}

// verificationContentRe pulls the token out of a pasted <meta ... content="…">
// tag: both consoles hand the operator the whole tag, and pasting it as is is
// the obvious thing to do.
var verificationContentRe = regexp.MustCompile(`content\s*=\s*["']([^"']*)["']`)

// verificationTokenRe is the shape of both providers' tokens (Google:
// base64url, Bing: hex). Anything else is refused rather than echoed into
// every page's <head>.
var verificationTokenRe = regexp.MustCompile(`^[A-Za-z0-9_\-+/=.]{6,200}$`)

// verificationToken normalises what the operator pasted to the bare token,
// or "" when it is not one.
func verificationToken(s string) string {
	s = strings.TrimSpace(s)
	if m := verificationContentRe.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	if !verificationTokenRe.MatchString(s) {
		return ""
	}
	return s
}

// buildPageMeta decides, per URL, what the served document claims. The SPA
// is one index.html for every route, so without this the home page, a
// channel, the login screen and a mistyped address all answered 200 with
// the same title and nothing a crawler could read.
func buildPageMeta(ctx context.Context, origin, siteName, p string) pageMeta {
	m := pageMeta{
		Status: http.StatusOK,
		Title:  siteName,
		Image:  origin + "/assets/og.jpg",
		Card:   "summary_large_image",
	}
	m.ImageAlt = siteName

	switch {
	case p == "/" || p == "":
		m.Title = homeTitle + " · " + siteName
		m.Description = homeDescription
		m.Canonical = "/"
		m.StructuredData = homeStructuredData(origin, siteName)
		m.Body = homeStaticBody(siteName)

	case postPagePathRe.MatchString(p):
		sm := postPagePathRe.FindStringSubmatch(p)
		m = buildPostPageMeta(ctx, origin, siteName, m, sm[1], sm[2])

	case channelPagePathRe.MatchString(p):
		slug := channelPagePathRe.FindStringSubmatch(p)[1]
		ch, vis := lookupPageChannel(ctx, slug)
		switch vis {
		case channelPublic:
			name := strings.TrimSpace(ch.Name)
			if name == "" {
				name = ch.Slug
			}
			// The managers' own search text when they wrote one (channel_seo.go),
			// else the name and description, as before.
			m.Title = channelSearchTitle(ch, name, siteName)
			m.Description = channelSearchDescription(ch, name)
			m.ImageAlt = name
			// A logo behind the files gate is unreachable for a crawler; the
			// platform card is better than a broken image.
			if logo := strings.TrimSpace(ch.LogoUrl); logo != "" && !ch.Features.RequireAuthFiles {
				m.Image = absoluteURL(origin, logo)
				m.Card = "summary"
			}
			// The newest posts as text, each linking to its own page: without
			// them a channel could only be found by its name and description.
			var posts []seoPost
			if !ch.SeoHidden {
				posts = latestSeoPosts(ctx, ch.Slug, channelPagePosts)
			}
			m.Body = channelStaticBody(siteName, name, ch.Description, ch.Slug, posts)
			if ch.SeoHidden {
				// Taken out of search by its managers: the page works as
				// always, and a forwarded link still unfurls, but nothing
				// invites a crawler to list it.
				m.NoIndex = true
				break
			}
			m.Canonical = "/channel/" + ch.Slug
			pageName := name
			if t := cleanSeoText(ch.SeoTitle); t != "" {
				pageName = t
			}
			m.StructuredData = channelStructuredData(origin, siteName, name, pageName, m.Description, m.Canonical, m.Image)
		case channelHidden:
			m.NoIndex = true
		case channelMissing:
			// A real 404, so a deleted or mistyped channel leaves the index
			// instead of lingering as a soft 404. The app still loads and
			// shows its own "no such channel" card.
			m.Status = http.StatusNotFound
			m.NoIndex = true
		}

	case appOnlyPathRe.MatchString(p):
		m.NoIndex = true

	default:
		// Every other path used to answer 200 with the app, which then
		// redirected to the home page: to a crawler, an unbounded number of
		// duplicate home pages. The body is still the app, so a person who
		// follows a stale link lands somewhere useful.
		m.Status = http.StatusNotFound
		m.NoIndex = true
	}
	return m
}

// injectHeadTags fills the <title>, the description, the canonical link, the
// robots directive, the Open Graph / Twitter tags and the structured data,
// and puts the page's readable content inside <app-root>. Before this the
// page shipped one generic title and no og:* tags, and everything else was
// set only by the Angular app at runtime — which link-preview crawlers never
// run — so every forwarded link rendered as a blank card and search engines
// saw the same empty document for every URL.
func injectHeadTags(content []byte, origin, siteName, path string, m pageMeta, cfg *SettingConfig) []byte {
	// The tag itself must survive: substituting bare text for it would leave
	// the document with no <title> at all, and stray text inside <head> makes
	// the parser close the head early. Escaped, because both the operator's
	// title and a channel name are free text landing inside markup.
	content = titleRe.ReplaceAllLiteral(content, []byte("<title>"+html.EscapeString(m.Title)+"</title>"))
	if m.Description != "" {
		content = descriptionMetaRe.ReplaceAllLiteral(content,
			[]byte(`<meta name="description" content="`+html.EscapeString(m.Description)+`">`))
	}

	var tags strings.Builder
	if m.NoIndex {
		tags.WriteString(metaName("robots", "noindex"))
	} else {
		tags.WriteString(metaName("robots", "index, follow, max-image-preview:large"))
	}
	if m.Canonical != "" {
		tags.WriteString(`<link rel="canonical" href="` + html.EscapeString(origin+m.Canonical) + `">` + "\n")
	}
	// The site name, for the app: the landing page builds its tab title from
	// it so the rendered title matches what the server sent.
	tags.WriteString(metaName("application-name", siteName))
	tags.WriteString(siteVerificationTags(cfg))

	ogURL := origin + path
	if m.Canonical != "" {
		ogURL = origin + m.Canonical
	}
	tags.WriteString(metaProperty("og:site_name", siteName))
	tags.WriteString(metaProperty("og:type", "website"))
	tags.WriteString(metaProperty("og:locale", "he_IL"))
	tags.WriteString(metaProperty("og:url", ogURL))
	tags.WriteString(metaProperty("og:title", m.Title))
	if m.Description != "" {
		tags.WriteString(metaProperty("og:description", m.Description))
	}
	tags.WriteString(metaProperty("og:image", m.Image))
	if m.Card == "summary_large_image" {
		tags.WriteString(metaProperty("og:image:width", ogImageWidth))
		tags.WriteString(metaProperty("og:image:height", ogImageHeight))
	}
	tags.WriteString(metaProperty("og:image:alt", m.ImageAlt))
	tags.WriteString(metaName("twitter:card", m.Card))
	tags.WriteString(metaName("twitter:title", m.Title))
	if m.Description != "" {
		tags.WriteString(metaName("twitter:description", m.Description))
	}
	for _, s := range m.StructuredData {
		tags.WriteString(s)
	}
	content = bytes.Replace(content, []byte("</head>"), []byte(tags.String()+"</head>"), 1)

	if m.Body != "" {
		content = appRootRe.ReplaceAllLiteral(content, []byte("<app-root>"+m.Body+"</app-root>"))
	}
	return content
}
