package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

const (
	// supportTicketTTL bounds how long a ticket and its thread survive. Any
	// reply refreshes it, so an active conversation is never cut off mid-way.
	supportTicketTTL = 180 * 24 * time.Hour
	// maxSupportTicketsListed caps the admin inbox read, which does one GET per
	// entry — the same bound the channel-request queue uses.
	maxSupportTicketsListed = 500
	// maxSupportMessages caps a single thread. Without it one participant can
	// grow a record without limit, and every read of that ticket pays for it.
	maxSupportMessages = 100

	maxSupportSubjectLen = 200
	maxSupportBodyLen    = 5000
	maxSupportNameLen    = 100
	maxSupportEmailLen   = 254
)

type SupportStatus string

const (
	// Open: waiting on the operator. Answered: the operator replied and it is
	// with the requester. Closed: done, and no further replies are accepted.
	SupportStatusOpen     SupportStatus = "open"
	SupportStatusAnswered SupportStatus = "answered"
	SupportStatusClosed   SupportStatus = "closed"
)

// Sentinels returned by a dbUpdateSupportTicket mutator to abort the
// transaction, judged against the ticket as it stands at write time rather than
// the copy the handler read earlier.
var (
	errTicketClosed = errors.New("this ticket is closed")
	errThreadFull   = errors.New("this thread has reached its message limit")
)

// writeTicketUpdateError maps a dbUpdateSupportTicket failure onto a status
// code. A ticket that vanished (expired TTL) reads as not found, exactly as it
// would have before the transaction.
func writeTicketUpdateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTicketClosed), errors.Is(err, errThreadFull):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, redis.Nil):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		http.Error(w, "error", http.StatusInternalServerError)
	}
}

func validSupportStatus(s SupportStatus) bool {
	switch s {
	case SupportStatusOpen, SupportStatusAnswered, SupportStatusClosed:
		return true
	}
	return false
}

// SupportMessage is one entry in a ticket thread.
type SupportMessage struct {
	// "user" or "admin" — who wrote it, for rendering the two sides.
	Author     string    `json:"author"`
	AuthorName string    `json:"authorName"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

type SupportTicket struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	// Set when the ticket was opened from a channel's admin panel, so the
	// operator knows which tenant is asking.
	ChannelSlug string `json:"channelSlug,omitempty"`
	// True when the sender had a session. A ticket from a signed-in user has a
	// trustworthy email; an anonymous one carries whatever was typed.
	Authenticated bool `json:"authenticated"`

	Status   SupportStatus    `json:"status"`
	Messages []SupportMessage `json:"messages"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// AccessToken lets the anonymous sender come back and read the reply. Every
	// response goes through publicView, which clears it, so it leaves the server
	// exactly once: in the reply to the request that created the ticket.
	AccessToken string `json:"accessToken,omitempty"`
}

const (
	supportTicketKeyPrefix = "support_ticket:"
	supportTicketIndexKey  = "support_tickets:list"
	// One index per email so a signed-in user can list their own tickets
	// without scanning the global index.
	supportUserIndexPrefix = "support_tickets:user:"
)

func supportTicketKey(id string) string { return supportTicketKeyPrefix + id }

func supportUserIndexKey(email string) string {
	return supportUserIndexPrefix + strings.ToLower(email)
}

func dbSaveSupportTicket(ctx context.Context, t *SupportTicket) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := rdb.Set(ctx, supportTicketKey(t.ID), data, supportTicketTTL).Err(); err != nil {
		return err
	}
	score := redis.Z{Score: float64(t.UpdatedAt.Unix()), Member: t.ID}
	if err := rdb.ZAdd(ctx, supportTicketIndexKey, score).Err(); err != nil {
		return err
	}
	// Only a ticket opened by a signed-in user is listed under that email.
	// An anonymous ticket carries whatever address was typed, so indexing it
	// let a visitor plant a thread in any user's "my tickets" and, holding
	// the access token, read that user's and the operator's replies.
	if t.Authenticated && t.Email != "" {
		key := supportUserIndexKey(t.Email)
		if err := rdb.ZAdd(ctx, key, score).Err(); err != nil {
			return err
		}
		// The per-user index must not outlive the tickets it points at.
		rdb.Expire(ctx, key, supportTicketTTL)
	}
	return nil
}

// dbUpdateSupportTicket applies mutate to a ticket under a WATCH-guarded
// read-modify-write, following dbUpdateUsersList.
//
// A ticket is one JSON blob holding the whole thread, so the obvious
// read-mutate-Set loses writes: an operator answering while the requester adds
// a message means both load the same thread, both append, and whichever SET
// lands second silently discards the other's message — with a 200 for both. The
// same race let a reply composed from a pre-close snapshot reopen a ticket an
// operator had just closed.
//
// mutate may be called more than once and must therefore only act on the ticket
// it is handed. It returns an error to abort the transaction and report back
// (used for the conditions that must be judged against fresh state, such as a
// ticket that has since been closed).
func dbUpdateSupportTicket(ctx context.Context, id string, mutate func(*SupportTicket) error) (*SupportTicket, error) {
	// Optimistic concurrency: every loser of a race retries, so the budget has
	// to cover the contenders, not just a couple of collisions. With a flat
	// retry and no pause, simultaneous writers re-collide in lockstep and one
	// can exhaust a small budget while its peers commit — an honest error, but
	// an avoidable one. The jittered backoff spreads them apart.
	const maxRetries = 20
	key := supportTicketKey(id)
	var result *SupportTicket

	txf := func(tx *redis.Tx) error {
		data, err := tx.Get(ctx, key).Result()
		if err != nil {
			return err // redis.Nil surfaces as "not found" to the caller
		}
		var t SupportTicket
		if err := json.Unmarshal([]byte(data), &t); err != nil {
			return err
		}
		if err := mutate(&t); err != nil {
			return err
		}
		encoded, err := json.Marshal(&t)
		if err != nil {
			return err
		}

		score := redis.Z{Score: float64(t.UpdatedAt.Unix()), Member: t.ID}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, key, encoded, supportTicketTTL)
			p.ZAdd(ctx, supportTicketIndexKey, score)
			// Same rule as dbSaveSupportTicket: an anonymous ticket's address is
			// unverified, so a reply must not plant it in that user's list.
			if t.Authenticated && t.Email != "" {
				userKey := supportUserIndexKey(t.Email)
				p.ZAdd(ctx, userKey, score)
				p.Expire(ctx, userKey, supportTicketTTL)
			}
			return nil
		})
		if err == nil {
			result = &t
		}
		return err
	}

	for i := 0; i < maxRetries; i++ {
		err := rdb.Watch(ctx, txf, key)
		if err != redis.TxFailedErr {
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		// Someone else committed first. Pause briefly, with jitter so the
		// contenders do not all wake and collide again together.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(rand.Int63n(int64(2*time.Millisecond))) + time.Millisecond):
		}
	}
	return nil, errors.New("support ticket: too much contention")
}

func dbGetSupportTicket(ctx context.Context, id string) (*SupportTicket, error) {
	data, err := rdb.Get(ctx, supportTicketKey(id)).Result()
	if err != nil {
		return nil, err
	}
	var t SupportTicket
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// dbListSupportTickets reads an index newest-first, dropping entries whose
// bodies have already expired.
func dbListSupportTickets(ctx context.Context, indexKey string) ([]*SupportTicket, error) {
	cutoff := strconv.FormatInt(time.Now().Add(-supportTicketTTL).Unix(), 10)
	rdb.ZRemRangeByScore(ctx, indexKey, "-inf", cutoff)

	ids, err := rdb.ZRevRange(ctx, indexKey, 0, maxSupportTicketsListed-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]*SupportTicket, 0, len(ids))
	for _, id := range ids {
		t, err := dbGetSupportTicket(ctx, id)
		if err != nil {
			// Expired body with a surviving index entry: drop it rather than
			// letting it reappear on every listing.
			rdb.ZRem(ctx, indexKey, id)
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// publicView strips the access token before a ticket is sent anywhere. The
// token is the only credential on an anonymous thread, so it must never travel
// back out in a response body.
//
// It also re-signs every operator reply as operatorName. New replies are
// stored that way already; this covers the ones written before, which carry
// the operator's own display name or email and are still served for as long
// as the ticket lives.
func publicView(t *SupportTicket) SupportTicket {
	c := *t
	c.AccessToken = ""
	// A fresh slice: the struct copy above shares the original's backing array,
	// and the stored ticket must come out of this unchanged.
	c.Messages = make([]SupportMessage, len(t.Messages))
	copy(c.Messages, t.Messages)
	for i := range c.Messages {
		if c.Messages[i].Author == "admin" {
			c.Messages[i].AuthorName = operatorName
		}
	}
	return c
}

// supportSubmitLimiters throttles ticket creation. Anonymous submissions are
// keyed by IP, which is all we have; a signed-in sender is keyed by email so a
// shared address (office NAT, mobile carrier) cannot exhaust another user's
// quota. 5 per hour with a burst of 3.
var (
	supportSubmitLimiters sync.Map
	supportSubmitMu       sync.Mutex
)

func supportSubmitLimiter(key string) *rate.Limiter {
	return getLimiter(&supportSubmitLimiters, &supportSubmitMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(12*time.Minute), 3)
	})
}

// clientKey identifies the sender for rate limiting: session email when there
// is one, otherwise the remote address. middleware.RealIP has already resolved
// RemoteAddr from X-Real-IP / X-Forwarded-For.
func clientKey(r *http.Request) string {
	if s, ok := sessionEmail(r); ok {
		return "email:" + normEmail(s.Email)
	}
	return "ip:" + remoteHost(r.RemoteAddr)
}

// remoteHost strips the port from a RemoteAddr. After RealIP the address has
// no port at all, and a bare IPv6 address cut at its last colon lost its
// final hextet, so every host in the same /112 shared one limiter.
func remoteHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return strings.Trim(addr, "[]")
}

// trimTo bounds a free-text field to max characters. Every one of these lands
// in a Redis value that is read back in full on each view, so length is
// enforced on write. The limit counts characters, as the form's maxlength
// does: counted in bytes, a 3,000-letter Hebrew message (two bytes a letter)
// was silently cut in half, and a cut through a letter left invalid UTF-8
// that the JSON encoder showed the operator as U+FFFD.
func trimTo(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return strings.TrimSpace(s[:i])
		}
		n++
	}
	return s
}

// looksLikeEmail is a shape check, not validation: the address is only ever
// shown to the operator, never sent to. It exists so an obviously empty or
// junk value is refused at the door.
func looksLikeEmail(s string) bool {
	at := strings.Index(s, "@")
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".") && !strings.ContainsAny(s, " \t\r\n")
}

// POST /api/support/tickets — open to anonymous visitors.
func createSupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req struct {
		Subject     string `json:"subject"`
		Body        string `json:"body"`
		Name        string `json:"name"`
		Email       string `json:"email"`
		ChannelSlug string `json:"channelSlug"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	subject := trimTo(req.Subject, maxSupportSubjectLen)
	body := trimTo(req.Body, maxSupportBodyLen)
	if subject == "" || body == "" {
		http.Error(w, "subject and body are required", http.StatusBadRequest)
		return
	}

	// A signed-in sender's identity comes from the session and is never taken
	// from the body, so a ticket cannot be attributed to someone else.
	session, authed := sessionEmail(r)
	name, email := trimTo(req.Name, maxSupportNameLen), trimTo(req.Email, maxSupportEmailLen)
	if authed {
		email = session.Email
		name = sessionDisplayName(session)
	} else {
		if !looksLikeEmail(email) {
			http.Error(w, "a valid email address is required", http.StatusBadRequest)
			return
		}
		if name == "" {
			name = email
		}
	}

	// Rated immediately before the write, never at the top: a refused request
	// must not spend quota on a message that failed validation.
	if !allowOrRetryAfter(w, supportSubmitLimiter(clientKey(r)),
		"too many requests — please try again later") {
		return
	}

	id, token := generatedRandomID(16), generatedRandomID(32)
	if id == "" || token == "" {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// A signed-in sender reaches the thread through the session (my-tickets),
	// so the ticket carries no token at all. Handing one out anyway had the
	// browser keep it in local storage past logout, where the next person at
	// a shared machine could read and answer the thread.
	if authed {
		token = ""
	}

	now := time.Now()
	ticket := &SupportTicket{
		ID:            id,
		Subject:       subject,
		Name:          name,
		Email:         email,
		ChannelSlug:   trimTo(req.ChannelSlug, 64),
		Authenticated: authed,
		Status:        SupportStatusOpen,
		Messages: []SupportMessage{{
			Author: "user", AuthorName: name, Body: body, CreatedAt: now,
		}},
		CreatedAt:   now,
		UpdatedAt:   now,
		AccessToken: token,
	}
	if err := dbSaveSupportTicket(ctx, ticket); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// The token is returned exactly once, here, so an anonymous sender can keep
	// the link and read the answer later.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id, "accessToken": token})
}

// ticketTokenHeader carries the anonymous access token. It used to travel as
// a ?token= query parameter, which the request logger writes to stdout on
// every read and reply — so the only credential on an anonymous thread ended
// up in the container logs for the thread's 180-day life.
const ticketTokenHeader = "X-Ticket-Token"

// ticketToken returns the caller's access token. A client loaded before the
// header existed (the SPA keeps running for days in an open tab) still sends
// ?token=; hideTicketTokenQuery in main.go lifts that into the header before
// the request is logged or routed, so the header is the only source here.
func ticketToken(r *http.Request) string {
	return r.Header.Get(ticketTokenHeader)
}

// authoriseTicket resolves the ticket for a requester-side request and reports
// whether this caller may see it. Two ways in: the session email matches a
// ticket that was opened signed-in, or the access token does. A ticket opened
// anonymously is token-only: its email is unverified, so a session matching
// it proves nothing (see dbSaveSupportTicket).
func authoriseTicket(ctx context.Context, r *http.Request, id string) (*SupportTicket, bool) {
	t, err := dbGetSupportTicket(ctx, id)
	if err != nil {
		return nil, false
	}
	if t.Authenticated {
		if s, ok := sessionEmail(r); ok && strings.EqualFold(s.Email, t.Email) {
			return t, true
		}
	}
	// A signed-in ticket is reachable by its session only. Tickets created
	// before tokens stopped being issued to signed-in senders still carry one,
	// and the browser keeps sending it from localStorage after logout — so the
	// token must not open those either.
	if t.Authenticated {
		return nil, false
	}
	token := ticketToken(r)
	// Constant time: this is the only credential guarding an anonymous thread.
	if token != "" && t.AccessToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(t.AccessToken)) == 1 {
		return t, true
	}
	return nil, false
}

// GET /api/support/tickets/{id} (X-Ticket-Token) — the requester's view of a thread.
func getSupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, ok := authoriseTicket(ctx, r, chi.URLParam(r, "id"))
	if !ok {
		// Deliberately not 403: a wrong token must not confirm that the ticket
		// exists.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(t))
}

// POST /api/support/tickets/{id}/reply (X-Ticket-Token) — the requester answers.
func replySupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, ok := authoriseTicket(ctx, r, chi.URLParam(r, "id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if t.Status == SupportStatusClosed {
		http.Error(w, "this ticket is closed", http.StatusConflict)
		return
	}
	body, ok := decodeSupportBody(w, r)
	if !ok {
		return
	}
	if !allowOrRetryAfter(w, supportSubmitLimiter(clientKey(r)),
		"too many requests — please try again later") {
		return
	}

	// The thread-length and closed checks are re-made inside the transaction,
	// against the ticket as it stands at write time rather than the copy read
	// for authorisation.
	updated, err := dbUpdateSupportTicket(ctx, t.ID, func(cur *SupportTicket) error {
		if cur.Status == SupportStatusClosed {
			return errTicketClosed
		}
		if len(cur.Messages) >= maxSupportMessages {
			return errThreadFull
		}
		appendSupportMessage(cur, "user", cur.Name, body, SupportStatusOpen)
		return nil
	})
	if err != nil {
		writeTicketUpdateError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(updated))
}

// GET /api/support/my-tickets — a signed-in user's own threads.
func listMySupportTickets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tickets, err := dbListSupportTickets(ctx, supportUserIndexKey(s.Email))
	if err != nil {
		// An empty 200 read as "no tickets" during a Redis outage.
		log.Printf("listMySupportTickets: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	out := make([]SupportTicket, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, publicView(t))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// GET /api/super-admin/support/tickets — the operator inbox.
func adminListSupportTickets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	tickets, err := dbListSupportTickets(ctx, supportTicketIndexKey)
	if err != nil {
		// Same as listMySupportTickets: the inbox must not look empty on a
		// storage error.
		log.Printf("adminListSupportTickets: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	out := make([]SupportTicket, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, publicView(t))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// POST /api/super-admin/support/tickets/{id}/reply
func adminReplySupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, err := dbGetSupportTicket(ctx, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, ok := decodeSupportBody(w, r)
	if !ok {
		return
	}

	updated, err := dbUpdateSupportTicket(ctx, t.ID, func(cur *SupportTicket) error {
		if len(cur.Messages) >= maxSupportMessages {
			return errThreadFull
		}
		// An operator reply reopens a closed ticket: answering it is a
		// deliberate act, and leaving it closed would silently discard the
		// reply from the requester's view.
		//
		// Signed as the management and never from the session: this record is
		// served to the requester, so whoever answered must not be named in it.
		appendSupportMessage(cur, "admin", operatorName, body, SupportStatusAnswered)
		return nil
	})
	if err != nil {
		writeTicketUpdateError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(updated))
}

// POST /api/super-admin/support/tickets/{id}/status
func adminSetSupportTicketStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, err := dbGetSupportTicket(ctx, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Status SupportStatus `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil ||
		!validSupportStatus(req.Status) {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	updated, err := dbUpdateSupportTicket(ctx, t.ID, func(cur *SupportTicket) error {
		cur.Status = req.Status
		cur.UpdatedAt = time.Now()
		return nil
	})
	if err != nil {
		writeTicketUpdateError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(updated))
}

// decodeSupportBody reads and bounds the one field every reply carries.
func decodeSupportBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return "", false
	}
	body := trimTo(req.Body, maxSupportBodyLen)
	if body == "" {
		http.Error(w, "message body is required", http.StatusBadRequest)
		return "", false
	}
	return body, true
}

// appendSupportMessage adds one entry and moves the ticket to status, keeping
// UpdatedAt (which is also the index score) in step.
func appendSupportMessage(t *SupportTicket, author, name, body string, status SupportStatus) {
	now := time.Now()
	t.Messages = append(t.Messages, SupportMessage{
		Author: author, AuthorName: name, Body: body, CreatedAt: now,
	})
	t.Status = status
	t.UpdatedAt = now
}
