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
	Value     int64     `json:"value"`
	Timestamp time.Time `json:"timestamp"`
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
	for {
		dbSaveSSEStatistics(openSSEConnections.Load())
		time.Sleep(1 * time.Minute)
	}
}

func increaseCounterSSE() {
	new := openSSEConnections.Add(1)
	if new > peakSSEConnections.Value {
		peakMu.Lock()
		defer peakMu.Unlock()
		peakSSEConnections.Value = new
		peakSSEConnections.Timestamp = time.Now()
		dbSavePeakSSEConnections(peakSSEConnections)
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

	peakMu.Lock()
	defer peakMu.Unlock()

	response := struct {
		UsersAmount          int64               `json:"usersAmount"`
		ConnectedUsersAmount int64               `json:"connectedUsersAmount"`
		PeakSSEConnections   *PeakSSEConnections `json:"peakSSEConnections"`
	}{
		UsersAmount:          amount,
		ConnectedUsersAmount: openSSEConnections.Load(),
		PeakSSEConnections:   peakSSEConnections,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
