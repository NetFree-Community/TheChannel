package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi"
)

type BroadcastList struct {
	sync.Mutex
	Clients map[*EventListener]bool
}

func (bl *BroadcastList) Count() int {
	bl.Lock()
	defer bl.Unlock()
	return len(bl.Clients)
}

var broadcastList = &BroadcastList{
	Clients: make(map[*EventListener]bool),
}

type EventListener struct {
	Privileges Privileges
	ClientCh   chan string
}

func (el *EventListener) Add() {
	broadcastList.Lock()
	defer broadcastList.Unlock()
	broadcastList.Clients[el] = true
	go upCounterSSE()
}

func (el *EventListener) Close() {
	broadcastList.Lock()
	defer broadcastList.Unlock()
	delete(broadcastList.Clients, el)
	close(el.ClientCh)
}

func (el *EventListener) Send(mp string) {
	el.ClientCh <- mp
}

type PushType int

const (
	NewMessage PushType = iota
	EditMessage
	DeleteMessage
	MsgAfterScheduling
	MsgBeforeScheduling
	Reaction
)

type PushMessage struct {
	Type string  `json:"type"`
	M    Message `json:"message"`
}

func init() {
	const waitingTime = 30 * time.Second
	go func() {
		for {
			scheduledMessage, err := dbGetScheduledMessages()
			if err != nil {
				time.Sleep(waitingTime)
				continue
			}
			for _, msg := range scheduledMessage {
				go pushSseMessage(MsgAfterScheduling, msg)
				go dbRemoveScheduledMessage(msg.ID)
			}
			time.Sleep(waitingTime)
		}
	}()
}

func getMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	offsetFromClient := r.URL.Query().Get("offset")
	limitFromClient := r.URL.Query().Get("limit")
	direction := r.URL.Query().Get("direction")

	offset, err := strconv.Atoi(offsetFromClient)
	if err != nil {
		offset = 0
	}

	limit, err := strconv.Atoi(limitFromClient)
	if err != nil {
		limit = 20
	}

	messages, err := funcGetMessageRange(ctx, int64(offset), int64(limit), checkPrivilege(r, Writer), settingConfig.CountViews, direction)
	if err != nil {
		log.Printf("Failed to get messages: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)

	addViewsToMessages(ctx, messages)
}

func addMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	message := &Message{}
	var err error
	defer r.Body.Close()

	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	for _, regex := range settingConfig.RegexReplace {
		if !strings.HasPrefix(body.Text, "[quote-embedded#]") {
			t := regex.Pattern.ReplaceAllString(body.Text, regex.Replace)
			body.Text = t
		}
	}

	message.ID = getMessageNextId(ctx)
	message.Type = body.Type
	message.Author = user.PublicName
	message.AuthorId = user.ID
	message.Timestamp = body.Timestamp
	message.Text = body.Text
	message.File = body.File
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), "create", message)
	go pushFcmMessage(message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}

func updateMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var err error
	defer r.Body.Close()

	body := &Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	body.LastEdit = time.Now()

	if err := setMessage(ctx, body, true); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	go SendWebhook(context.Background(), "update", body)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func deleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := chi.URLParam(r, "id")

	idInt, _ := strconv.Atoi(id)
	message := &Message{ID: idInt, Deleted: true}

	if err := funcDeleteMessage(ctx, id); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	go SendWebhook(context.Background(), "delete", message)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func getEvents(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	clientCtx := r.Context()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n")
	if err != nil {
		return
	}
	flusher.Flush()

	el := &EventListener{
		Privileges: user.Privileges,
		ClientCh:   make(chan string, 10),
	}
	// חוסם את התוכנית, אחרת הלקוח לא יהיה רשום לקבלת הודעות.
	el.Add()
	defer el.Close()

	ch := el.ClientCh

	for {
		select {
		case <-clientCtx.Done():
			return

		case <-heartbeat.C:
			_, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n")
			if err != nil {
				return
			}
			flusher.Flush()

		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func pushSseMessage(pushType PushType, message *Message) {
	var pt string
	switch pushType {
	case NewMessage, MsgBeforeScheduling, MsgAfterScheduling:
		pt = "new-message"
	case EditMessage:
		pt = "edit-message"
	case DeleteMessage:
		pt = "delete-message"
	case Reaction:
		pt = "reaction"
	}
	pushMessage := &PushMessage{
		Type: pt,
		M:    *message,
	}
	pushMessageData, _ := json.Marshal(pushMessage)
	pmStr := string(pushMessageData)

	broadcastList.Lock()
	defer broadcastList.Unlock()
	for client := range broadcastList.Clients {
		switch pushType {
		case NewMessage, EditMessage, DeleteMessage, Reaction:
			client.Send(pmStr)

		case MsgBeforeScheduling:
			if client.Privileges[Writer] {
				client.Send(pmStr)
			}

		case MsgAfterScheduling:
			if !client.Privileges[Writer] {
				client.Send(pmStr)
			}
		}
	}
}
