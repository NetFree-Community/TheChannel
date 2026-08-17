package main

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// uploadLimiters stores per-user-per-channel rate limiters for file uploads.
// Key: "userEmail:channelSlug" — 30 uploads/min (one every 2s), burst of 10.
var (
	uploadLimiters  sync.Map
	uploadLimiterMu sync.Mutex
)

func getUploadLimiter(key string) *rate.Limiter {
	if v, ok := uploadLimiters.Load(key); ok {
		return v.(*rate.Limiter)
	}
	uploadLimiterMu.Lock()
	defer uploadLimiterMu.Unlock()
	if v, ok := uploadLimiters.Load(key); ok {
		return v.(*rate.Limiter)
	}
	l := rate.NewLimiter(rate.Every(2*time.Second), 10)
	uploadLimiters.Store(key, l)
	return l
}

// channelRequestLimiters throttles the public channel-request endpoint per
// client IP — 3 submissions/min, burst of 3.
var (
	channelRequestLimiters sync.Map
	channelRequestMu       sync.Mutex
)

func channelRequestLimiter(r *http.Request) *rate.Limiter {
	key, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		key = r.RemoteAddr
	}

	if v, ok := channelRequestLimiters.Load(key); ok {
		return v.(*rate.Limiter)
	}
	channelRequestMu.Lock()
	defer channelRequestMu.Unlock()
	if v, ok := channelRequestLimiters.Load(key); ok {
		return v.(*rate.Limiter)
	}
	l := rate.NewLimiter(rate.Every(20*time.Second), 3)
	channelRequestLimiters.Store(key, l)
	return l
}

func uploadRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := channelSlugFromCtx(r)

		// Key by user identity so limits are per-user, not per-channel
		session, _ := store.Get(r, cookieName)
		user, _ := session.Values["user"].(Session)
		key := user.Email + ":" + slug
		if key == ":" {
			key = slug // unauthenticated fallback (shouldn't happen, upload requires login)
		}

		if !getUploadLimiter(key).Allow() {
			http.Error(w, "too many uploads — please slow down", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
