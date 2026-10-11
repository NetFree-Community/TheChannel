package main

import (
	"bytes"
	"encoding/gob"
	"log"
	"mime"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/boj/redistore"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

var rootStaticFolder = os.Getenv("ROOT_STATIC_FOLDER")

// hideTicketTokenQuery moves a support-thread access token that still arrives
// as ?token= (a SPA tab loaded before the X-Ticket-Token header existed, or a
// hand-built link) into the header and strips it from the URL. It runs ahead
// of the request logger, whose line is built from r.RequestURI, so the only
// credential of an anonymous thread never reaches stdout. Scoped to the
// support routes so no other endpoint's query string is rewritten.
func hideTicketTokenQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/support/tickets/") && r.URL.RawQuery != "" {
			q := r.URL.Query()
			if t := q.Get("token"); t != "" {
				if r.Header.Get(ticketTokenHeader) == "" {
					r.Header.Set(ticketTokenHeader, t)
				}
				q.Del("token")
				r.URL.RawQuery = q.Encode()
				r.RequestURI = r.URL.RequestURI()
			}
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	// Go's built-in table has no entry for the web app manifest, so the static
	// handler served it as application/octet-stream and browsers ignored it.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	// Sessions are signed with SECRET_KEY; with it unset, redistore signs with
	// an empty key and every cookie is forgeable. Refuse to start rather than
	// run with no session security at all.
	if secretKey == "" {
		log.Fatal("SECRET_KEY is not set; refusing to start with unsigned sessions")
	}
	if len(secretKey) < 32 {
		log.Printf("WARNING: SECRET_KEY is only %d bytes; use at least 32 random bytes\n", len(secretKey))
	}

	gob.Register(Session{})
	initR2()
	if err := initializePrivilegeUsers(); err != nil {
		panic(err)
	}
	startPrivilegesRefresh()
	startGlobalSettingsRefresh()

	// The session store is built before the migrations run: a misconfiguration
	// here is fatal, and failing after a one-shot migration has been marked
	// applied would consume it without ever serving a request.
	// redistore dials REDIS_ADDR as one plain address. With Sentinel
	// (REDIS_MASTER) the data client resolved the master while sessions were
	// written to the sentinel itself, so the boot looked healthy and every
	// login answered 500; a comma-separated list failed to dial outright.
	// Neither is supported, so refuse loudly instead of half-working.
	if master := os.Getenv("REDIS_MASTER"); master != "" || strings.Contains(redisAddr, ",") {
		log.Fatalf("Session store cannot use Redis Sentinel or several addresses (REDIS_MASTER=%q, REDIS_ADDR=%q); point REDIS_ADDR at a single Redis/Kvrocks instance", master, redisAddr)
	}
	var err error
	store, err = redistore.NewRediStore(10, redisType, redisAddr, "", redisPass, []byte(secretKey))
	if err != nil {
		log.Fatalf("Session store init failed (REDIS_PROTOCOL=%q, REDIS_ADDR=%q): %v", redisType, redisAddr, err)
	}
	store.SetMaxAge(60 * 60 * 24 * 30)
	// redistore refuses to save a session larger than 4 KiB by default. The
	// session carries the user's whole channel-role map, so a moderator of a
	// few dozen channels could not log in at all: session.Save failed and the
	// login answered 500.
	store.SetMaxLength(64 << 10)
	store.Options.HttpOnly = true
	// Secure by default; set COOKIE_INSECURE=1 only for local plain-HTTP dev.
	store.Options.Secure = os.Getenv("COOKIE_INSECURE") != "1"
	// Lax (not Strict) so the cookie still rides along on the Google OAuth
	// redirect back to the site and on inbound links to authenticated views.
	store.Options.SameSite = http.SameSiteLaxMode
	defer store.Close()

	// Data migrations run to completion before the listener starts, so a request
	// never observes a half-migrated state. A failure is logged and retried on
	// the next boot rather than taking the server down.
	migCtx, migCancel := migrationContext()
	runMigrations(migCtx)
	migCancel()

	r := chi.NewRouter()
	// Before Logger, which prints RequestURI: the anonymous support token must
	// be lifted out of the query string before anything can write it down.
	r.Use(hideTicketTokenQuery)
	r.Use(middleware.Logger)
	// The shipped deployment fronts the backend with Caddy (docker-compose
	// exposes only the proxy), so RemoteAddr is the proxy's container IP for
	// every request. RealIP restores the client address from X-Real-IP, which
	// the Caddyfile sets from the connection itself (and it strips the other
	// headers RealIP would otherwise honour), so per-IP rate limiting works.
	// The backend port must therefore never be exposed directly, since RealIP
	// trusts whatever header it is given.
	r.Use(middleware.RealIP)
	// gzip for everything except the SSE stream; see compress.go.
	r.Use(compressResponses)
	r.Use(limitRequestBody)

	// Auth routes
	r.Get("/auth/google", getGoogleAuthValues)
	r.Post("/auth/login", login)
	r.Post("/auth/logout", logout)

	// Global assets
	r.Get("/assets/favicon.ico", getFavicon)
	r.Get("/favicon.ico", getFavicon)
	r.Get("/firebase-messaging-sw.js", getFirebaseMessagingSW)

	// User info (global, requires login)
	r.Group(func(r chi.Router) {
		r.Use(checkLogin)
		r.Get("/api/user-info", getUserInfo)
		// Self-service channel creation: the owner is taken from the session,
		// never from the body, so nobody can claim a slug for someone else.
		r.Post("/api/channels/create", createChannelSelfService)
		r.Get("/api/channels/slug-available", checkSlugAvailable)
		// Every channel the user holds a role on, with names and logos: what
		// the "my channels" page and the channel switcher list.
		r.Get("/api/my-channels", listMyChannels)
	})

	// Super admin routes
	r.Route("/api/super-admin", func(r chi.Router) {
		r.Use(checkLogin)
		r.Use(requireSuperAdmin)

		r.Get("/channels", listChannels)
		r.Post("/channels/create", createChannel)
		r.Get("/channels/{slug}", getSuperAdminChannel)
		r.Delete("/channels/{slug}", deleteChannel)
		r.Put("/channels/{slug}/features", updateChannelFeatures)
		r.Get("/channels/{slug}/users", superAdminGetChannelUsers)
		r.Post("/channels/{slug}/users", superAdminSetChannelUsers)
		r.Get("/users/list", getPrivilegeUsersList)
		r.Post("/users/set", setPrivilegeUsers)
		r.Get("/global-settings/get", getGlobalSettings)
		r.Post("/global-settings/set", setGlobalSettings)
		r.Get("/ads/config", getGlobalAdsConfig)
		r.Post("/ads/config", setGlobalAdsConfig)
		r.Get("/magnet/config", getGlobalMagnetConfig)
		r.Post("/magnet/config", setGlobalMagnetConfig)
		r.Get("/magnet/stats", getMagnetStats)
		r.Get("/storage/config", getSuperAdminStorageConfig)
		r.Post("/storage/config", setSuperAdminStorageConfig)
		r.Get("/channels/{slug}/storage", getSuperAdminChannelStorage)
		r.Put("/channels/{slug}/storage", setSuperAdminChannelStorage)
		r.Post("/statistics/reset", resetStatistics)
		r.Get("/support/tickets", adminListSupportTickets)
		r.Post("/support/tickets/{id}/reply", adminReplySupportTicket)
		r.Post("/support/tickets/{id}/status", adminSetSupportTicketStatus)
		r.Get("/channel-requests", listChannelRequests)
		r.Post("/channel-requests/{id}/approve", approveChannelRequest)
		r.Post("/channel-requests/{id}/reject", rejectChannelRequest)
	})

	// Support tickets. Creation and the thread view are deliberately outside
	// checkLogin: a visitor who cannot sign in (or has not yet) is exactly the
	// person who most needs to reach the operator. An anonymous thread is
	// guarded by the unguessable token minted at creation.
	r.Post("/api/support/tickets", createSupportTicket)
	r.Get("/api/support/tickets/{id}", getSupportTicket)
	r.Post("/api/support/tickets/{id}/reply", replySupportTicket)
	r.With(checkLogin).Get("/api/support/my-tickets", listMySupportTickets)

	// Per-channel API import (with API key, no channel middleware needed - slug from URL)
	r.Post("/api/channel/{slug}/import/post", addNewPost)

	// Per-channel routes
	r.Route("/api/channel/{slug}", func(r chi.Router) {
		r.Use(channelMiddleware)
		r.Use(channelIfRequireAuth)

		r.Get("/info", getChannelInfo)
		r.Get("/messages", getMessages)
		r.Get("/events", getEvents)
		r.With(channelIfRequireAuthFiles).Get("/files/{fileid}", serveFile)
		r.Get("/emojis/list", getEmojisList)
		r.Get("/notifications-config", getNotificationsConfig)
		r.Get("/ads/settings", getAdsSettings)
		r.Get("/ads/magnet", getMagnetAdsSettings)

		r.Group(func(r chi.Router) {
			r.Use(checkLogin)
			// Gated like the other feature routes: a channel whose push the
			// operator switched off must not keep collecting tokens.
			r.Post("/notifications-subscribe", requireFeature(func(f *ChannelFeatures) bool { return f.Notifications }, subscribeNotifications))
			r.Post("/reactions/set-reactions", requireFeature(func(f *ChannelFeatures) bool { return f.Reactions }, setReactions))
			r.Post("/messages/report", requireFeature(func(f *ChannelFeatures) bool { return f.Reports }, reportMessage))
			r.Get("/user-info", getUserInfo)

			r.Route("/admin", func(r chi.Router) {
				// Writer level
				r.Post("/new", protectedWithChannelRole(RoleWriter, addMessage))
				r.Post("/edit-message", protectedWithChannelRole(RoleWriter, updateMessage))
				// DELETE only. The GET alias that used to sit here was a CSRF
				// hole: the session cookie is SameSite=Lax, which deliberately
				// still rides along on a top-level GET navigation, so merely
				// visiting a link like
				// /api/channel/<slug>/admin/delete-message/42 was enough to
				// delete a writer's message from any site on the web. Lax
				// withholds the cookie from cross-site DELETE, so the verb is
				// the whole defence. The only client (admin.service.ts) has
				// always used DELETE.
				r.Delete("/delete-message/{id}", protectedWithChannelRole(RoleWriter, deleteMessage))
				r.Post("/upload", protectedWithChannelRole(RoleWriter, requireFeature(func(f *ChannelFeatures) bool { return f.FileUploads }, uploadRateLimit(uploadFile))))
				r.Get("/scheduled-messages/get", protectedWithChannelRole(RoleWriter, requireFeature(func(f *ChannelFeatures) bool { return f.ScheduledMessages }, getScheduledMessages)))
				r.Post("/scheduled-messages/update", protectedWithChannelRole(RoleWriter, requireFeature(func(f *ChannelFeatures) bool { return f.ScheduledMessages }, updateScheduledMessages)))

				// Moderator level
				r.Post("/edit-channel-info", protectedWithChannelRole(RoleModerator, editChannelInfo))
				r.Get("/statistics", protectedWithChannelRole(RoleModerator, getStatistics))
				r.Post("/set-emojis", protectedWithChannelRole(RoleModerator, setEmojis))
				r.Get("/reports/get", protectedWithChannelRole(RoleModerator, getReports))
				r.Post("/reports/set", protectedWithChannelRole(RoleModerator, setReports))

				// Owner level
				r.Get("/settings/get", protectedWithChannelRole(RoleOwner, getSettings))
				r.Post("/settings/set", protectedWithChannelRole(RoleOwner, setSettings))
				r.Get("/users/get", protectedWithChannelRole(RoleOwner, getChannelUsers))
				r.Post("/users/set", protectedWithChannelRole(RoleOwner, setChannelUsers))
				r.Get("/storage", protectedWithChannelRole(RoleOwner, getChannelStorageInfo))
				r.Post("/storage/auto-cleanup", protectedWithChannelRole(RoleOwner, setChannelAutoCleanup))
			})
		})
	})

	if cfg := getGlobalConfig(); cfg != nil && cfg.RootStaticFolder != "" {
		r.Handle("/assets/*", staticCacheHeaders(noDirectoryListing(http.StripPrefix("/assets/", http.FileServer(http.Dir(cfg.RootStaticFolder))))))
		r.NotFound(serveSpaFile)
	}

	// The pprof listener is a debugging aid, not part of the service. Binding it
	// must never be able to take the real server down, so the error is logged
	// instead of fatal, and it only starts when explicitly asked for.
	if pprofAddr := os.Getenv("PPROF_ADDR"); pprofAddr != "" {
		go func() {
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Printf("pprof listener stopped: %v\n", err)
			}
		}()
	}

	if err := http.ListenAndServe(":"+os.Getenv("SERVER_PORT"), r); err != nil {
		log.Fatal(err)
	}
}

// maxJSONRequestBody caps any non-multipart request body. Handlers decode
// straight into memory with json.NewDecoder(r.Body), which reads as much as the
// client sends: without a cap, a single authenticated writer could post a
// multi-gigabyte body to /admin/new and have it buffered, stored verbatim and
// then fanned out to every SSE viewer of the channel.
//
// 2 MiB is far above any legitimate payload here — the largest is a scheduled
// message list, and message text is separately capped at 100k — while bounding
// what one request can allocate.
const maxJSONRequestBody = 2 << 20

// limitRequestBody applies that cap to every route, so a handler added later
// cannot forget it. The file upload route is exempt: it enforces its own, much
// larger, per-channel size limit in uploadFile.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !isUploadRoute(r.URL.Path) {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

// isUploadRoute matches the one route that legitimately carries a large body,
// /api/channel/{slug}/admin/upload. The exemption is keyed on the route and not
// on the Content-Type, because the client chooses the Content-Type: sending
// "multipart/form-data" on any JSON route used to lift the cap there too, and
// the handler then decoded an unbounded body straight into memory.
func isUploadRoute(p string) bool {
	return strings.HasPrefix(p, "/api/channel/") && strings.HasSuffix(p, "/admin/upload")
}

func serveSpaFile(w http.ResponseWriter, r *http.Request) {
	// The SPA fallback must not answer for the API: a mistyped or removed API
	// path returned index.html with 200, so an integrator (or a fetch in the
	// frontend) saw success and then failed to parse HTML as JSON.
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
		http.NotFound(w, r)
		return
	}

	cfg := getGlobalConfig()
	if cfg == nil || cfg.RootStaticFolder == "" {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	htmlPath := filepath.Join(cfg.RootStaticFolder, "index.html")
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	siteName := strings.TrimSpace(cfg.CustomTitle)
	if siteName == "" {
		siteName = defaultSiteName
	}
	content = injectHeadTags(content, requestOrigin(r, cfg), siteName, r.URL.Path,
		previewChannel(r.Context(), r.URL.Path))

	if cfg.AnalyticsHead != "" {
		content = bytes.Replace(content, []byte("</head>"), []byte(cfg.AnalyticsHead+"</head>"), 1)
	}

	// index.html must never be cached without revalidation. Every deploy gives
	// the bundles new content-hashed names, and this file is the only thing
	// that says which names are current. Cached by a browser or an upstream
	// proxy, it outlives the bundles it points at: the next visitor with a cold
	// cache gets a stale document whose /assets/main-<hash>.js is gone, the
	// script 404s, and app-root is left empty — a white page with no error the
	// application could report, because the application never started.
	//
	// It is also rewritten per-deployment below (title, analytics), so a shared
	// cache holding it would serve another operator's markup.
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

// hashedAssetRe matches the content-hashed filenames the frontend build emits
// (main-EE7HE3OC.js, styles-ZXVD3ZCM.css, media/NotoColorEmoji-HW4CGOE5.ttf).
// The hash changes whenever the contents do, so these are safe to cache
// forever; anything else under /assets (favicon.ico and friends) is not.
var hashedAssetRe = regexp.MustCompile(`-[A-Z0-9]{8}\.[a-zA-Z0-9]+$`)

// noDirectoryListing refuses the index pages http.FileServer renders for a
// directory: /assets/media/ listed the whole bundle, which is nobody's
// business and a free inventory for anyone probing the deployment.
func noDirectoryListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// staticCacheHeaders pairs the immutable bundles with the no-cache index.html
// above: the hashed files are what make it safe for index.html to be
// revalidated on every visit, since only the small document is ever refetched.
func staticCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hashedAssetRe.MatchString(r.URL.Path) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Un-hashed and therefore replaceable in place: revalidate rather
			// than pin an old copy for a year.
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		next.ServeHTTP(w, r)
	})
}
