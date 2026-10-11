package main

import (
	"bytes"
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// channelPagePathRe matches the public page of one channel, the URL people
// forward to each other.
var channelPagePathRe = regexp.MustCompile(`^/channel/([^/]+)/?$`)

// descriptionMetaRe finds the generic description index.html ships, so a
// channel page can carry its own instead of a second, conflicting tag.
var descriptionMetaRe = regexp.MustCompile(`<meta name="description" content="[^"]*">`)

// titleRe matches the <title> index.html ships (a default, so the tab is
// never blank in a dev server either), which every served page replaces.
var titleRe = regexp.MustCompile(`<title>[^<]*</title>`)

// defaultSiteName is the product name link previews and the tab show when
// the operator has not set a custom title.
const defaultSiteName = "הערוץ"

// previewDescriptionMax is what WhatsApp, Telegram and Google show before
// cutting a description off.
const previewDescriptionMax = 160

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

// previewChannel loads the channel a /channel/<slug> page is about, when it
// may be shown to a stranger. A private or disabled channel yields nothing,
// so the preview reveals neither its name nor that it exists — the same rule
// getFavicon applies.
func previewChannel(ctx context.Context, p string) *ChannelData {
	m := channelPagePathRe.FindStringSubmatch(p)
	if m == nil || !isPlausibleSlug(m[1]) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ch, err := dbGetChannel(ctx, m[1])
	if err != nil || ch.Features.Disabled || ch.Features.RequireAuth {
		return nil
	}
	return ch
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

// injectHeadTags fills the <title> and adds the Open Graph tags a shared link
// unfurls with. Before this the page shipped `<title></title>` and no og:*
// tags, and the channel name was set only by the Angular app at runtime —
// which crawlers never run — so every forwarded link rendered as a blank
// card: no name, no description, no picture, nothing to tell a stranger what
// they were tapping on.
func injectHeadTags(content []byte, origin, siteName, path string, ch *ChannelData) []byte {
	title := siteName
	description := ""
	image := origin + "/assets/og.jpg"
	card := "summary_large_image"

	if ch != nil {
		name := strings.TrimSpace(ch.Name)
		if name == "" {
			name = ch.Slug
		}
		title = name + " · " + siteName
		description = previewText(ch.Description)
		// A logo behind the files gate is unreachable for a crawler; the
		// platform card is better than a broken image.
		if logo := strings.TrimSpace(ch.LogoUrl); logo != "" && !ch.Features.RequireAuthFiles {
			image = absoluteURL(origin, logo)
			card = "summary"
		}
	}

	// The tag itself must survive: substituting bare text for it would leave
	// the document with no <title> at all, and stray text inside <head> makes
	// the parser close the head early. Escaped, because both the operator's
	// title and a channel name are free text landing inside markup.
	content = titleRe.ReplaceAllLiteral(content, []byte("<title>"+html.EscapeString(title)+"</title>"))
	if description != "" {
		content = descriptionMetaRe.ReplaceAllLiteral(content,
			[]byte(`<meta name="description" content="`+html.EscapeString(description)+`">`))
	}

	var tags strings.Builder
	tags.WriteString(metaProperty("og:site_name", siteName))
	tags.WriteString(metaProperty("og:type", "website"))
	tags.WriteString(metaProperty("og:url", origin+path))
	tags.WriteString(metaProperty("og:title", title))
	if description != "" {
		tags.WriteString(metaProperty("og:description", description))
	}
	tags.WriteString(metaProperty("og:image", image))
	tags.WriteString(metaName("twitter:card", card))
	return bytes.Replace(content, []byte("</head>"), []byte(tags.String()+"</head>"), 1)
}
