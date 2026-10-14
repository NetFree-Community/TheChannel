package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

// robotsDisallow are the paths no search engine has any business crawling:
// the API and OAuth endpoints, and the screens that only make sense to a
// signed-in operator. The channel management pages sit under /channel/, so
// they are matched with a wildcard rather than by blocking /channel/ itself,
// which would hide every public channel.
var robotsDisallow = []string{
	"/api/",
	"/auth/",
	"/login",
	"/super-admin",
	"/channel/*/manage",
}

// GET /robots.txt
func getRobotsTxt(w http.ResponseWriter, r *http.Request) {
	origin := requestOrigin(r, getGlobalConfig())
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
	for _, p := range robotsDisallow {
		b.WriteString("Disallow: " + p + "\n")
	}
	b.WriteString("\nSitemap: " + origin + "/sitemap.xml\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(b.String()))
}

// sitemapMaxURLs is how many channel URLs go in one sitemap file. The
// protocol allows 50,000; a smaller file keeps each response quick to build
// and to download.
const sitemapMaxURLs = 10000

// sitemapCacheTTL bounds how often the channel list is read from Redis for
// the sitemap: crawlers fetch it repeatedly, and building it walks every
// channel.
const sitemapCacheTTL = 15 * time.Minute

type sitemapEntry struct {
	Path    string
	LastMod time.Time
}

var sitemapCache struct {
	sync.Mutex
	built    time.Time
	channels []sitemapEntry
}

// publicChannelEntries lists every channel a stranger may read and its
// managers left in search — not private, not disabled, not switched off
// (channelSearchListed) — with the time of its newest message (or its
// creation, for a channel that never posted). Cached, so a crawler hammering
// the sitemap costs one pass over the channel list per TTL.
func publicChannelEntries(ctx context.Context) ([]sitemapEntry, error) {
	sitemapCache.Lock()
	defer sitemapCache.Unlock()
	if sitemapCache.channels != nil && time.Since(sitemapCache.built) < sitemapCacheTTL {
		return sitemapCache.channels, nil
	}

	channels, err := dbListChannels(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]sitemapEntry, 0, len(channels))
	public := make([]*ChannelData, 0, len(channels))
	for _, ch := range channels {
		if !channelSearchListed(ch) || !isPlausibleSlug(ch.Slug) {
			continue
		}
		public = append(public, ch)
	}

	// The newest message's time is the top score of the channel's m_times
	// index (seconds since the epoch), one ZREVRANGE per channel, pipelined
	// in batches. Deleted messages stay in that index, so a deletion also
	// counts as a change — which it is.
	for start := 0; start < len(public); start += dbListChannelsBatch {
		end := min(start+dbListChannelsBatch, len(public))
		batch := public[start:end]
		pipe := rdb.Pipeline()
		cmds := make([]*redis.ZSliceCmd, len(batch))
		for i, ch := range batch {
			cmds[i] = pipe.ZRevRangeWithScores(ctx, fmt.Sprintf("channel:%s:m_times", ch.Slug), 0, 0)
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, err
		}
		for i, ch := range batch {
			last := ch.CreatedAt
			if zs, err := cmds[i].Result(); err == nil && len(zs) > 0 {
				if t := time.Unix(int64(zs[0].Score), 0); t.After(last) {
					last = t
				}
			}
			entries = append(entries, sitemapEntry{Path: "/channel/" + ch.Slug, LastMod: last})
		}
	}

	sitemapCache.channels = entries
	sitemapCache.built = time.Now()
	return entries, nil
}

type xmlURL struct {
	Loc      string `xml:"loc"`
	LastMod  string `xml:"lastmod,omitempty"`
	Priority string `xml:"priority,omitempty"`
}

// Sitemap priorities: relative weights within this site only. The home page
// (where channels are opened) is the page the site exists to promote, the
// guides lead to it, and each channel is one of many.
const (
	priorityHome    = "1.0"
	priorityGuide   = "0.8"
	priorityChannel = "0.5"
)

type xmlURLSet struct {
	XMLName xml.Name `xml:"urlset"`
	XMLNS   string   `xml:"xmlns,attr"`
	URLs    []xmlURL `xml:"url"`
}

type xmlSitemap struct {
	Loc string `xml:"loc"`
}

type xmlSitemapIndex struct {
	XMLName  xml.Name     `xml:"sitemapindex"`
	XMLNS    string       `xml:"xmlns,attr"`
	Sitemaps []xmlSitemap `xml:"sitemap"`
}

const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

func lastModString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func writeXML(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("sitemap encode: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=900")
	w.Write(buf.Bytes())
}

// staticSitemapURLs are the pages that exist regardless of the channels.
func staticSitemapURLs(origin string) []xmlURL {
	urls := []xmlURL{{Loc: origin + "/", LastMod: contentRevised, Priority: priorityHome}}
	for _, p := range contentPages {
		urls = append(urls, xmlURL{Loc: origin + p.Path, LastMod: contentRevised, Priority: priorityGuide})
	}
	return urls
}

func channelURLs(origin string, entries []sitemapEntry) []xmlURL {
	urls := make([]xmlURL, 0, len(entries))
	for _, e := range entries {
		urls = append(urls, xmlURL{Loc: origin + e.Path, LastMod: lastModString(e.LastMod), Priority: priorityChannel})
	}
	return urls
}

// GET /sitemap.xml
//
// One file while everything fits; past that, an index pointing at
// /sitemap-pages.xml and /sitemap-channels-<n>.xml.
func getSitemap(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	origin := requestOrigin(r, getGlobalConfig())
	entries, err := publicChannelEntries(ctx)
	if err != nil {
		log.Printf("sitemap: %v\n", err)
		http.Error(w, "error", http.StatusServiceUnavailable)
		return
	}

	static := staticSitemapURLs(origin)
	if len(static)+len(entries) <= sitemapMaxURLs {
		writeXML(w, xmlURLSet{XMLNS: sitemapNS, URLs: append(static, channelURLs(origin, entries)...)})
		return
	}

	idx := xmlSitemapIndex{XMLNS: sitemapNS, Sitemaps: []xmlSitemap{{Loc: origin + "/sitemap-pages.xml"}}}
	for n := 1; (n-1)*sitemapMaxURLs < len(entries); n++ {
		idx.Sitemaps = append(idx.Sitemaps, xmlSitemap{Loc: origin + "/sitemap-channels-" + strconv.Itoa(n) + ".xml"})
	}
	writeXML(w, idx)
}

// GET /sitemap-pages.xml
func getSitemapPages(w http.ResponseWriter, r *http.Request) {
	writeXML(w, xmlURLSet{XMLNS: sitemapNS, URLs: staticSitemapURLs(requestOrigin(r, getGlobalConfig()))})
}

// GET /sitemap-channels-{n}.xml
func getSitemapChannels(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil || n < 1 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	entries, err := publicChannelEntries(ctx)
	if err != nil {
		log.Printf("sitemap: %v\n", err)
		http.Error(w, "error", http.StatusServiceUnavailable)
		return
	}
	start := (n - 1) * sitemapMaxURLs
	if start >= len(entries) {
		http.NotFound(w, r)
		return
	}
	end := min(start+sitemapMaxURLs, len(entries))
	writeXML(w, xmlURLSet{XMLNS: sitemapNS, URLs: channelURLs(requestOrigin(r, getGlobalConfig()), entries[start:end])})
}

// resetSitemapCache drops the cached channel list, so the next sitemap
// request rebuilds it. Called when a channel's search visibility changes
// (its own search toggle, privacy, the kill switch), and by tests, which
// create and delete channels between requests. The cache is per process: on
// a multi-instance deployment another instance still serves its copy until
// the TTL runs out.
func resetSitemapCache() {
	sitemapCache.Lock()
	sitemapCache.channels = nil
	sitemapCache.Unlock()
}
