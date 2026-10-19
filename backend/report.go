package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type Report struct {
	Id            int64     `json:"id" redis:"id"`
	MessageId     int64     `json:"messageId" redis:"messageId"`
	Reason        string    `json:"reason" redis:"reason"`
	CreatedAt     time.Time `json:"createdAt" redis:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt" redis:"updatedAt"`
	ReporterID    string    `json:"reporterId" redis:"reporterId"`
	ReportedEmail string    `json:"reportedEmail" redis:"reportedEmail"`
	ReporterName  string    `json:"reporterName" redis:"reporterName"`
	Closed        bool      `json:"closed" redis:"closed"`
}

type Reports []*Report

type ReportStatus string

const (
	Open   ReportStatus = "open"
	Closed ReportStatus = "closed"
	All    ReportStatus = "all"
)

func (s ReportStatus) IsValid() bool {
	return s == Open || s == Closed || s == All
}

func reportMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()

	slug := channelSlugFromCtx(r)

	var report Report
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	session, _ := store.Get(r, cookieName)
	s, ok := session.Values["user"].(Session)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Everything the reporter may not choose is set here: an unbounded reason is
	// a free write into Redis, a report against a nonexistent message is noise,
	// and a client-sent closed:true would land a "closed" hash in the open queue.
	if len(report.Reason) > 500 {
		http.Error(w, "reason too long", http.StatusBadRequest)
		return
	}
	n, err := rdb.Exists(ctx, fmt.Sprintf("channel:%s:messages:%d", slug, report.MessageId)).Result()
	if err != nil {
		http.Error(w, "Error saving report", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "message not found", http.StatusBadRequest)
		return
	}

	// A known repeat is answered before the limiter: it is idempotent and
	// creates nothing, so it must not burn a token the user's next real report
	// needs. Read-only; the SADD below stays the atomic guard for the race.
	reportersKey := fmt.Sprintf("channel:%s:message:%d:reporters", slug, report.MessageId)
	if already, err := rdb.SIsMember(ctx, reportersKey, s.ID).Result(); err == nil && already {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Response{Success: true})
		return
	}

	// Rate-limit per user: reporting was unlimited, and each report is a
	// permanent write, so a loop was a cheap way to grow Redis without bound.
	if !allowOrRetryAfter(w, reportLimiter(s.Email), "too many reports — please slow down") {
		return
	}

	// One report per user per message. SADD returns 0 when the reporter is
	// already in the set, so a repeat report is acknowledged without creating
	// another record. The set is dropped with the channel.
	added, err := rdb.SAdd(ctx, reportersKey, s.ID).Result()
	if err != nil {
		http.Error(w, "Error saving report", http.StatusInternalServerError)
		return
	}
	if added == 0 {
		// Already reported by this user; idempotent success, no new record.
		var response Response
		response.Success = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	report.Closed = false
	report.CreatedAt = time.Now()
	report.ReporterID = s.ID
	report.ReportedEmail = s.Email
	report.ReporterName = s.Username
	// Reports are read by the channel's moderators, who must not learn who the
	// operator is. The reporters set above still dedups on the real id; it is
	// never returned.
	if isSuperAdmin(r) {
		report.ReporterID, report.ReportedEmail, report.ReporterName = operatorAuthorId, "", operatorName
	}

	if err := dbReportMessage(ctx, slug, &report); err != nil {
		log.Println("Error saving report:", err)
		// The dedup entry was added first (SADD is what makes the check
		// atomic); left in place after a failed save it would make every retry
		// look like a repeat and answer success without ever creating the
		// report. Detached from ctx: the likeliest failure is that very
		// deadline expiring inside the save, and go-redis refuses a command on
		// an expired context, which left the entry in place anyway.
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer rcancel()
		if err := rdb.SRem(rctx, reportersKey, s.ID).Err(); err != nil {
			log.Printf("reportMessage: %s: undo dedup for %s after failed save: %v\n", slug, s.ID, err)
		}
		http.Error(w, "Error saving report", http.StatusInternalServerError)
		return
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getReports(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	status := ReportStatus(r.URL.Query().Get("status"))
	if !status.IsValid() {
		http.Error(w, "Invalid status", http.StatusBadRequest)
		return
	}

	reports, err := dbGetReports(ctx, slug, status)
	if err != nil {
		log.Printf("Error retrieving reports: %v\n", err)
		http.Error(w, "Error retrieving reports", http.StatusInternalServerError)
		return
	}
	// Reports an operator filed before they were anonymised still hold their
	// email, Google name and id.
	ops := currentOperators()
	for _, rep := range reports {
		ops.anonymiseOperatorReport(rep)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reports)
}

func setReports(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()

	slug := channelSlugFromCtx(r)

	var report Report
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	report.UpdatedAt = time.Now()

	if err := dbSetReports(ctx, slug, &report); err != nil {
		if err == redis.Nil {
			http.Error(w, "report not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Error saving reports", http.StatusInternalServerError)
		return
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
