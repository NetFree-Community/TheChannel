package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
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
	report.CreatedAt = time.Now()
	report.ReporterID = s.ID
	report.ReportedEmail = s.Email
	report.ReporterName = s.Username

	if err := dbReportMessage(ctx, &report); err != nil {
		log.Println("Error saving report:", err)
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

	status := ReportStatus(r.URL.Query().Get("status"))
	if !status.IsValid() {
		http.Error(w, "Invalid status", http.StatusBadRequest)
		return
	}

	reports, err := dbGetReports(ctx, status)
	if err != nil {
		log.Printf("Error retrieving reports: %v\n", err)
		http.Error(w, "Error retrieving reports", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reports)
}

func setReports(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()
	var report Report
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	report.UpdatedAt = time.Now()

	if err := dbSetReports(ctx, &report); err != nil {
		http.Error(w, "Error saving reports", http.StatusInternalServerError)
		return
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
