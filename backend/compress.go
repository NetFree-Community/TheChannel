package main

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/middleware"
)

// compressedTypes are the responses worth gzipping: the bundles under /assets,
// index.html and every JSON reply. Uploaded media is already compressed and
// the SSE stream is handled below, so neither is listed.
var compressedTypes = []string{
	"text/html", "text/css", "text/plain", "text/javascript",
	"application/javascript", "application/x-javascript", "application/json",
	"application/manifest+json", "image/svg+xml",
}

// isEventStreamRoute matches /api/channel/{slug}/events, the one long-lived
// response the server sends.
func isEventStreamRoute(p string) bool {
	return strings.HasPrefix(p, "/api/channel/") && strings.HasSuffix(p, "/events")
}

// compressResponses is chi's gzip middleware for everything except the SSE
// stream. Caddy in the shipped docker-compose does not `encode`, so without
// this a first visit downloaded the JavaScript and CSS (over 3 MB before the
// change) uncompressed.
//
// The stream is routed around the middleware rather than merely left out of
// the type list: chi v1's compressing writer wraps the connection for every
// request and, even when it passes text/event-stream through untouched, it
// implements neither SetWriteDeadline nor Unwrap. getEvents arms a write
// deadline through http.ResponseController so a phone that stopped reading is
// dropped in 30 s; through the wrapper that call silently failed and a dead
// viewer held its goroutine, its event buffer and one of the connection slots
// until TCP gave up, about a quarter of an hour later.
func compressResponses(next http.Handler) http.Handler {
	compressed := middleware.Compress(5, compressedTypes...)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isEventStreamRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		compressed.ServeHTTP(w, r)
	})
}
