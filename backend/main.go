package main

import (
	"bytes"
	"encoding/gob"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"

	"github.com/boj/redistore"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

var rootStaticFolder = os.Getenv("ROOT_STATIC_FOLDER")

func main() {
	gob.Register(Session{})
	initR2()
	if err := initializePrivilegeUsers(); err != nil {
		panic(err)
	}

	// The session store is built before the migrations run: a misconfiguration
	// here is fatal, and failing after a one-shot migration has been marked
	// applied would consume it without ever serving a request.
	var err error
	store, err = redistore.NewRediStore(10, redisType, redisAddr, "", redisPass, []byte(secretKey))
	if err != nil {
		log.Fatalf("Session store init failed (REDIS_PROTOCOL=%q, REDIS_ADDR=%q): %v", redisType, redisAddr, err)
	}
	store.SetMaxAge(60 * 60 * 24 * 30)
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

	go statLogger()

	r := chi.NewRouter()
	r.Use(middleware.Logger)

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
		r.Get("/channel-requests", listChannelRequests)
		r.Post("/channel-requests/{id}/approve", approveChannelRequest)
		r.Post("/channel-requests/{id}/reject", rejectChannelRequest)
	})

	// Public: submit channel request (no auth required)
	r.Post("/api/channel-request", submitChannelRequest)

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
			r.Post("/notifications-subscribe", subscribeNotifications)
			r.Post("/reactions/set-reactions", requireFeature(func(f *ChannelFeatures) bool { return f.Reactions }, setReactions))
			r.Post("/messages/report", requireFeature(func(f *ChannelFeatures) bool { return f.Reports }, reportMessage))
			r.Get("/user-info", getUserInfo)

			r.Route("/admin", func(r chi.Router) {
				// Writer level
				r.Post("/new", protectedWithChannelRole(RoleWriter, addMessage))
				r.Post("/edit-message", protectedWithChannelRole(RoleWriter, updateMessage))
				// Deletion is a state change, so it belongs on DELETE. The GET
				// route stays registered for existing clients until they migrate.
				r.Delete("/delete-message/{id}", protectedWithChannelRole(RoleWriter, deleteMessage))
				r.Get("/delete-message/{id}", protectedWithChannelRole(RoleWriter, deleteMessage))
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
		r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.Dir(cfg.RootStaticFolder))))
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

func serveSpaFile(w http.ResponseWriter, r *http.Request) {
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

	if cfg.CustomTitle != "" {
		content = bytes.ReplaceAll(content, []byte("<title></title>"), []byte(cfg.CustomTitle))
	}

	if cfg.AnalyticsHead != "" {
		content = bytes.Replace(content, []byte("</head>"), []byte(cfg.AnalyticsHead+"</head>"), 1)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}
