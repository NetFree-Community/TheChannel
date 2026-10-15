package main

import (
	"context"
	"sync"
	"time"
)

// Reading a file used to cost two calls to R2 before the browser got
// anywhere: a HeadObject to make sure the blob was there, then a fresh
// pre-signed URL. The fresh URL differed on every request, so the browser
// could never reuse a cached copy and downloaded every image of the feed
// again on every visit. The caches below keep one answer of each for a while.
//
// Both are per process. The backend runs as a single instance (see
// DEPLOY.md), and r2Delete evicts the key here, so a blob this process
// removes is never reported present afterwards.

const (
	// r2PresentTTL bounds how long a successful HeadObject is trusted. Blobs
	// are content-addressed and only this process deletes them, so the TTL
	// only matters for a delete made by some other tool.
	r2PresentTTL = 10 * time.Minute

	// r2PresignTTL is how long a pre-signed URL stays valid at R2, and
	// r2PresignReuse how long the same URL is handed out again. The gap is the
	// minimum validity left when a browser follows a redirect it cached.
	r2PresignTTL   = time.Hour
	r2PresignReuse = 30 * time.Minute

	// r2CacheMaxEntries caps each map; when full it is simply emptied.
	r2CacheMaxEntries = 20000
)

type r2PresignEntry struct {
	url string
	at  time.Time
}

var (
	r2CacheMu      sync.Mutex
	r2PresentCache = map[string]time.Time{}
	r2PresignCache = map[string]r2PresignEntry{}
)

// r2ExistsCached is r2Exists for the read paths. Only a hit is remembered: a
// miss falls back to the local copy and must be re-checked once the
// migration has copied the blob.
func r2ExistsCached(ctx context.Context, key string) bool {
	now := time.Now()
	r2CacheMu.Lock()
	at, ok := r2PresentCache[key]
	r2CacheMu.Unlock()
	if ok && now.Sub(at) < r2PresentTTL {
		return true
	}
	if !r2Exists(ctx, key) {
		return false
	}
	r2CacheMu.Lock()
	if len(r2PresentCache) >= r2CacheMaxEntries {
		r2PresentCache = map[string]time.Time{}
	}
	r2PresentCache[key] = now
	r2CacheMu.Unlock()
	return true
}

// r2PresignCached returns a pre-signed GET for key and how long a browser may
// keep following it. The same URL is reused for r2PresignReuse, so repeat
// views hit the browser's cache instead of downloading the file again.
func r2PresignCached(ctx context.Context, key, disposition string) (string, time.Duration, error) {
	cacheKey := key + "\x00" + disposition
	now := time.Now()
	r2CacheMu.Lock()
	e, ok := r2PresignCache[cacheKey]
	r2CacheMu.Unlock()
	if ok {
		if left := r2PresignReuse - now.Sub(e.at); left > time.Minute {
			return e.url, left, nil
		}
	}
	u, err := r2PresignURL(ctx, key, r2PresignTTL, disposition)
	if err != nil {
		return "", 0, err
	}
	r2CacheMu.Lock()
	if len(r2PresignCache) >= r2CacheMaxEntries {
		r2PresignCache = map[string]r2PresignEntry{}
	}
	r2PresignCache[cacheKey] = r2PresignEntry{url: u, at: now}
	r2CacheMu.Unlock()
	return u, r2PresignReuse, nil
}

// r2ForgetKey drops every cached answer about key; r2Delete calls it.
func r2ForgetKey(key string) {
	r2CacheMu.Lock()
	defer r2CacheMu.Unlock()
	delete(r2PresentCache, key)
	prefix := key + "\x00"
	for k := range r2PresignCache {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(r2PresignCache, k)
		}
	}
}
