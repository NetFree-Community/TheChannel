package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var openSSEConnections = atomic.Int64{}

// Per-channel SSE counters
var channelSSEConnections sync.Map // slug -> *atomic.Int64
var channelPeakSSE sync.Map        // slug -> *PeakSSEConnections
var channelPeakMu sync.Map         // slug -> *sync.Mutex

type PeakSSEConnections struct {
	Value     int64     `json:"value" redis:"value"`
	Timestamp time.Time `json:"timestamp" redis:"timestamp"`
}
type Statistics struct {
	Data   []int64  `json:"date"`
	Labels []string `json:"labels"`
}

func getOrCreateChannelCounter(slug string) *atomic.Int64 {
	v, _ := channelSSEConnections.LoadOrStore(slug, &atomic.Int64{})
	return v.(*atomic.Int64)
}

func getOrCreateChannelPeak(slug string) (*PeakSSEConnections, *sync.Mutex) {
	mu, _ := channelPeakMu.LoadOrStore(slug, &sync.Mutex{})
	m := mu.(*sync.Mutex)

	if v, ok := channelPeakSSE.Load(slug); ok {
		return v.(*PeakSSEConnections), m
	}

	// Publish the entry only once it is fully loaded. Storing a placeholder
	// first and replacing it afterwards hands concurrent callers a pointer that
	// is then orphaned, so their peak updates are silently dropped.
	p := &PeakSSEConnections{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if loaded, err := dbGetPeakSSEConnections(ctx, slug); err == nil && loaded != nil {
		p = loaded
	}

	actual, _ := channelPeakSSE.LoadOrStore(slug, p)
	return actual.(*PeakSSEConnections), m
}

func statLogger() {
	var old int64
	for {
		new := openSSEConnections.Load()
		if old != new {
			old = new
		}
		time.Sleep(5 * time.Minute)
	}
}

func increaseCounterSSE(slug string) {
	// Global counter
	openSSEConnections.Add(1)

	// Per-channel counter
	counter := getOrCreateChannelCounter(slug)
	newVal := counter.Add(1)

	peak, mu := getOrCreateChannelPeak(slug)
	mu.Lock()
	defer mu.Unlock()
	if newVal > peak.Value {
		peak.Value = newVal
		peak.Timestamp = time.Now()
		p := *peak
		go dbSavePeakSSEConnections(slug, &p)
	}

	// Save stat
	go dbSaveSSEStatistics(slug, newVal)
}

func decreaseCounterSSE(slug string) {
	openSSEConnections.Add(-1)

	counter := getOrCreateChannelCounter(slug)
	newVal := counter.Add(-1)
	// A decrement that outruns its increment must not persist a negative
	// datapoint into the statistics series.
	if newVal < 0 {
		counter.Store(0)
		newVal = 0
	}
	go dbSaveSSEStatistics(slug, newVal)
}

func getStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	amount, err := dbGetUsersAmount(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	s, err := dbGetSSEStatistics(ctx, slug, 1000)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	counter := getOrCreateChannelCounter(slug)
	peak, mu := getOrCreateChannelPeak(slug)

	mu.Lock()
	defer mu.Unlock()

	response := struct {
		UsersAmount           int64               `json:"usersAmount"`
		ConnectedUsersAmount  int64               `json:"connectedUsersAmount"`
		PeakSSEConnections    *PeakSSEConnections `json:"peakSSEConnections"`
		ConnectionsStatistics Statistics          `json:"connectionsStatistics"`
	}{
		UsersAmount:           amount,
		ConnectedUsersAmount:  counter.Load(),
		PeakSSEConnections:    peak,
		ConnectionsStatistics: *s,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// resetStatistics clears the recorded peak for every channel. Everything the
// statistics endpoint serves comes from the per-channel state (channelPeakSSE
// plus channel:<slug>:peak_sse_connections), so both have to be cleared or the
// reset is reported as successful while changing nothing.
func resetStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	channels, err := dbListChannels(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	for _, ch := range channels {
		peak, mu := getOrCreateChannelPeak(ch.Slug)
		mu.Lock()
		peak.Value = 0
		peak.Timestamp = time.Time{}
		mu.Unlock()
		rdb.Del(ctx, fmt.Sprintf("channel:%s:peak_sse_connections", ch.Slug))
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
