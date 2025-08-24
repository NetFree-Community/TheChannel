package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var openSSEConnections = atomic.Int64{}
var peakSSEConnections = &PeakSSEConnections{}
var peakMu = sync.Mutex{}

type PeakSSEConnections struct {
	Value     int64     `json:"value" redis:"value"`
	Timestamp time.Time `json:"timestamp" redis:"timestamp"`
}
type Statistics struct {
	Data   []int64  `json:"date"`
	Labels []string `json:"labels"`
}

func init() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p, _ := dbGetPeakSSEConnections(ctx)
	peakMu.Lock()
	defer peakMu.Unlock()
	peakSSEConnections = p
}

func statLogger() {
	var old int64
	for {
		new := openSSEConnections.Load()
		if old != new {
			dbSaveSSEStatistics(new)
		}
		old = new
		time.Sleep(5 * time.Minute)
	}
}

func increaseCounterSSE() {
	new := openSSEConnections.Add(1)

	peakMu.Lock()
	defer peakMu.Unlock()
	if new > peakSSEConnections.Value {
		peakSSEConnections.Value = new
		peakSSEConnections.Timestamp = time.Now()
		peak := *peakSSEConnections
		go dbSavePeakSSEConnections(&peak)
	}
}

func decreaseCounterSSE() {
	openSSEConnections.Add(-1)
}

func getStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	amount, err := dbGetUsersAmount(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	s, err := dbGetSSEStatistics(ctx, 5000)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	peakMu.Lock()
	defer peakMu.Unlock()

	response := struct {
		UsersAmount           int64               `json:"usersAmount"`
		ConnectedUsersAmount  int64               `json:"connectedUsersAmount"`
		PeakSSEConnections    *PeakSSEConnections `json:"peakSSEConnections"`
		ConnectionsStatistics Statistics          `json:"connectionsStatistics"`
	}{
		UsersAmount:           amount,
		ConnectedUsersAmount:  openSSEConnections.Load(),
		PeakSSEConnections:    peakSSEConnections,
		ConnectionsStatistics: *s,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
