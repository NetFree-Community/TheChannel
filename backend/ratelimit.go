package main

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// limiterEntry pairs a limiter with its last use so idle entries can be swept:
// the maps below otherwise grow monotonically for the process lifetime, one
// entry per distinct key.
type limiterEntry struct {
	l        *rate.Limiter
	lastSeen atomic.Int64
}

// limiterIdleTTL must cover the slowest refill window below, or a client can
// reset a limiter by idling: the channel-creation limiter refills one token
// per 20 minutes, so with a 10 minute TTL an account could create three
// channels, wait ten minutes, and get a fresh burst of three (a recreated
// limiter starts full). One hour is the time that limiter takes to refill
// completely, so sweeping after it changes nothing a client could notice.
const (
	limiterIdleTTL       = 60 * time.Minute
	limiterSweepInterval = 10 * time.Minute
)

// allowOrRetryAfter takes one token if one is free right now. When the bucket is
// empty it answers 429 with a Retry-After saying exactly how long to wait, and
// — crucially — consumes nothing, so a refused request costs the caller no quota.
//
// Callers must place this immediately before the work being rationed, not at the
// top of the handler: spending a token on a request that then fails validation
// makes a user pay full quota for a typo, which on the hourly creation limiter
// meant three mistakes locked the account out for twenty minutes.
func allowOrRetryAfter(w http.ResponseWriter, l *rate.Limiter, msg string) bool {
	res := l.Reserve()
	if !res.OK() {
		http.Error(w, msg, http.StatusTooManyRequests)
		return false
	}
	if d := res.Delay(); d > 0 {
		res.Cancel()
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
		http.Error(w, msg, http.StatusTooManyRequests)
		return false
	}
	return true
}

func init() {
	go func() {
		ticker := time.NewTicker(limiterSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-limiterIdleTTL).Unix()
			sweep := func(m *sync.Map) {
				m.Range(func(key, value any) bool {
					if e, ok := value.(*limiterEntry); ok && e.lastSeen.Load() < cutoff {
						m.Delete(key)
					}
					return true
				})
			}
			sweep(&uploadLimiters)
			sweep(&channelCreateLimiters)
			sweep(&slugCheckLimiters)
			sweep(&reportLimiters)
			// Support submissions are keyed by client IP when there is no
			// session, so this is the one map an anonymous caller can add
			// unbounded distinct keys to. Leaving it out of the sweep made it
			// the only limiter map that grew for the process lifetime.
			sweep(&supportSubmitLimiters)
			// Keyed by the From of each inbound mail (support_inbound.go):
			// as many distinct keys as there are senders.
			sweep(&inboundMailLimiters)
			sweep(&loginLimiters)
			sweep(&importAuthLimiters)
			sweep(&subscribeLimiters)
		}
	}()
}

func getLimiter(m *sync.Map, mu *sync.Mutex, key string, newLimiter func() *rate.Limiter) *rate.Limiter {
	if v, ok := m.Load(key); ok {
		e := v.(*limiterEntry)
		e.lastSeen.Store(time.Now().Unix())
		return e.l
	}
	mu.Lock()
	defer mu.Unlock()
	if v, ok := m.Load(key); ok {
		e := v.(*limiterEntry)
		e.lastSeen.Store(time.Now().Unix())
		return e.l
	}
	e := &limiterEntry{l: newLimiter()}
	e.lastSeen.Store(time.Now().Unix())
	m.Store(key, e)
	return e.l
}

// uploadLimiters stores per-user-per-channel rate limiters for file uploads.
// Key: "userEmail:channelSlug" — 30 uploads/min (one every 2s), burst of 10.
var (
	uploadLimiters  sync.Map
	uploadLimiterMu sync.Mutex
)

func getUploadLimiter(key string) *rate.Limiter {
	return getLimiter(&uploadLimiters, &uploadLimiterMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(2*time.Second), 10)
	})
}

// channelCreateLimiters throttles self-service channel creation per session
// user — 3 creations/hour, burst of 3. Creation is cheap for the caller and
// permanent for us, so the window is much wider than the upload limiter's.
var (
	channelCreateLimiters sync.Map
	channelCreateMu       sync.Mutex
)

func channelCreateLimiter(email string) *rate.Limiter {
	return getLimiter(&channelCreateLimiters, &channelCreateMu, email, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(20*time.Minute), 3)
	})
}

// slugCheckLimiters throttles the slug-availability probe per session user.
// It is a single EXISTS, but it is also a slug enumeration oracle, so it is
// generous rather than free — 60 checks/min (one per second), burst of 10.
var (
	slugCheckLimiters sync.Map
	slugCheckMu       sync.Mutex
)

func slugCheckLimiter(email string) *rate.Limiter {
	return getLimiter(&slugCheckLimiters, &slugCheckMu, email, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(time.Second), 10)
	})
}

// reportLimiters throttles message reports per user. Reporting is behind login
// but was otherwise unlimited, and every report is a permanent Redis write
// (a hash plus two sorted-set entries), so one user could grow the store
// without bound. 10 reports/min, burst of 5.
var (
	reportLimiters sync.Map
	reportMu       sync.Mutex
)

func reportLimiter(email string) *rate.Limiter {
	return getLimiter(&reportLimiters, &reportMu, email, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(6*time.Second), 5)
	})
}

// loginLimiters throttles OAuth code exchanges per client. Each POST
// /auth/login costs a round trip to Google and a Redis write, and it was the
// only unauthenticated write path with no limit at all. Twenty attempts at
// once, then one every 2 seconds, is far above what people behind one shared
// address produce by signing in (once per device per month).
var (
	loginLimiters  sync.Map
	loginLimiterMu sync.Mutex
)

func loginLimiter(key string) *rate.Limiter {
	return getLimiter(&loginLimiters, &loginLimiterMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(2*time.Second), 20)
	})
}

// importAuthLimiters counts FAILED API-key checks on the import endpoint per
// client and channel. The key is owner-chosen free text, and the endpoint is
// reachable without a session, so an unlimited number of wrong guesses made a
// short key brute-forceable at network speed. Successful imports are not
// charged, so a legitimate bulk import is unaffected.
var (
	importAuthLimiters sync.Map
	importAuthMu       sync.Mutex
)

func importAuthLimiter(key string) *rate.Limiter {
	return getLimiter(&importAuthLimiters, &importAuthMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(6*time.Second), 10)
	})
}

// subscribeLimiters throttles push-subscription registrations per client.
var (
	subscribeLimiters  sync.Map
	subscribeLimiterMu sync.Mutex
)

func subscribeLimiter(key string) *rate.Limiter {
	return getLimiter(&subscribeLimiters, &subscribeLimiterMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(10*time.Second), 5)
	})
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
