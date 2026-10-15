package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Inbound mail runs against real Redis like the other support tests. Each
// test uses its own sender address, so the per-sender limiter and the match
// index never carry state from one test into another.

func useInboundSecret(t *testing.T, secret string) {
	t.Helper()
	prev := magnetMailSecret
	magnetMailSecret = secret
	t.Cleanup(func() { magnetMailSecret = prev })
}

// cleanupInbound removes everything a test's mails may have written: the
// sender's match index, every ticket it points at, and the message ids.
func cleanupInbound(t *testing.T, email string, messageIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		key := supportMatchIndexKey(email)
		ids, _ := rdb.ZRange(ctx, key, 0, -1).Result()
		for _, id := range ids {
			rdb.Del(ctx, supportTicketKey(id))
			rdb.ZRem(ctx, supportTicketIndexKey, id)
			rdb.ZRem(ctx, supportUserIndexKey(email), id)
		}
		rdb.Del(ctx, key)
		for _, m := range messageIDs {
			rdb.Del(ctx, inboundSeenKey(m))
		}
	})
}

func postInbound(t *testing.T, secret string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/magnet/inbound-mail", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	if secret != "" {
		r.Header.Set("x-channels-secret", secret)
	}
	w := httptest.NewRecorder()
	magnetInboundMail(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func inboundMail(from, msgID string, extra map[string]any) map[string]any {
	m := map[string]any{
		"from_email":      from,
		"from_name":       "שולח במייל",
		"subject":         "Re: התקבלה תשובה לפנייה שלך: נושא",
		"text":            "תשובה במייל",
		"message_id":      msgID,
		"sender_verified": true,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// saveInboundFixture writes a ticket with a chosen id, email and status.
func saveInboundFixture(t *testing.T, ctx context.Context, id, email string, authed bool, status SupportStatus, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	tk := &SupportTicket{
		ID: id, Subject: "נושא", Name: "בודק", Email: email, Authenticated: authed,
		Status:    status,
		Messages:  []SupportMessage{{Author: "user", AuthorName: "בודק", Body: "פתיחה", CreatedAt: at}},
		CreatedAt: at, UpdatedAt: at,
	}
	if err := dbSaveSupportTicket(ctx, tk); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
}

func mustTicket(t *testing.T, ctx context.Context, id string) *SupportTicket {
	t.Helper()
	tk, err := dbGetSupportTicket(ctx, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return tk
}

func TestInboundMailSecret(t *testing.T) {
	useInboundSecret(t, "")
	if w, out := postInbound(t, "anything", inboundMail("s0@example.com", "<m0@x>", nil)); w.Code != http.StatusServiceUnavailable || out["error"] == nil {
		t.Errorf("no secret configured: status %d body %s, want 503 with a JSON error", w.Code, w.Body)
	}

	useInboundSecret(t, testMailSecret)
	if w, _ := postInbound(t, "", inboundMail("s0@example.com", "<m0@x>", nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("missing secret: status %d, want 401", w.Code)
	}
	if w, _ := postInbound(t, testMailSecret+"x", inboundMail("s0@example.com", "<m0@x>", nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: status %d, want 401", w.Code)
	}
	if w, out := postInbound(t, testMailSecret, inboundMail("not-an-address", "<m0@x>", nil)); w.Code != http.StatusBadRequest || out["error"] == nil {
		t.Errorf("bad sender: status %d, want 400 with a JSON error", w.Code)
	}
	if w, _ := postInbound(t, testMailSecret, inboundMail("s0@example.com", "", nil)); w.Code != http.StatusBadRequest {
		t.Errorf("no message id: status %d, want 400", w.Code)
	}
}

// The ref picks the thread among the sender's own, even when another of their
// threads is more recent.
func TestInboundMailMatchesByRefAndEmail(t *testing.T) {
	ctx := supportCtx(t)
	useInboundSecret(t, testMailSecret)
	from := "inb-ref@example.com"
	cleanupInbound(t, from, "<ref-1@x>")
	saveInboundFixture(t, ctx, "a1a1a1a1aaaa0001", from, true, SupportStatusAnswered, time.Hour)
	saveInboundFixture(t, ctx, "b2b2b2b2bbbb0002", from, true, SupportStatusOpen, time.Minute)

	w, out := postInbound(t, testMailSecret, inboundMail("Inb-Ref@Example.com", "<ref-1@x>", map[string]any{"ticket_ref": "A1A1A1A1"}))
	if w.Code != http.StatusOK || out["action"] != "appended" || out["ticket_id"] != "a1a1a1a1aaaa0001" {
		t.Fatalf("status %d body %s, want appended to a1a1…", w.Code, w.Body)
	}
	tk := mustTicket(t, ctx, "a1a1a1a1aaaa0001")
	last := tk.Messages[len(tk.Messages)-1]
	if len(tk.Messages) != 2 || last.Author != "user" || last.Body != "תשובה במייל" || last.Source != "email" {
		t.Errorf("thread = %+v", tk.Messages)
	}
	if tk.Status != SupportStatusOpen {
		t.Errorf("status = %s, want open (waiting on the operator)", tk.Status)
	}
	if n := len(mustTicket(t, ctx, "b2b2b2b2bbbb0002").Messages); n != 1 {
		t.Errorf("the other thread got the mail too (%d messages)", n)
	}
}

// The ref is printed in a mail and is not a secret: the right ref from the
// wrong address must not reach the thread.
func TestInboundMailRefWithOtherEmailOpensNewTicket(t *testing.T) {
	ctx := supportCtx(t)
	useInboundSecret(t, testMailSecret)
	owner, other := "inb-owner@example.com", "inb-intruder@example.com"
	cleanupInbound(t, owner)
	cleanupInbound(t, other, "<intr-1@x>")
	saveInboundFixture(t, ctx, "c3c3c3c3cccc0003", owner, true, SupportStatusOpen, time.Minute)

	w, out := postInbound(t, testMailSecret, inboundMail(other, "<intr-1@x>", map[string]any{"ticket_ref": "c3c3c3c3"}))
	if w.Code != http.StatusOK || out["action"] != "created" || out["ticket_id"] == "c3c3c3c3cccc0003" {
		t.Fatalf("status %d body %s, want a new ticket", w.Code, w.Body)
	}
	if n := len(mustTicket(t, ctx, "c3c3c3c3cccc0003").Messages); n != 1 {
		t.Errorf("the owner's thread was written to by another address (%d messages)", n)
	}
	created := mustTicket(t, ctx, out["ticket_id"].(string))
	if created.Email != other {
		t.Errorf("new ticket email = %q", created.Email)
	}
}

// Without a ref: the sender's most recent ticket that is not closed.
func TestInboundMailMatchesByEmailOnly(t *testing.T) {
	ctx := supportCtx(t)
	useInboundSecret(t, testMailSecret)
	from := "inb-email@example.com"
	cleanupInbound(t, from, "<em-1@x>")
	saveInboundFixture(t, ctx, "d4d4d4d4dddd0004", from, false, SupportStatusAnswered, time.Hour)
	saveInboundFixture(t, ctx, "e5e5e5e5eeee0005", from, false, SupportStatusClosed, time.Minute)

	w, out := postInbound(t, testMailSecret, inboundMail(from, "<em-1@x>", nil))
	if w.Code != http.StatusOK || out["action"] != "appended" || out["ticket_id"] != "d4d4d4d4dddd0004" {
		t.Fatalf("status %d body %s, want appended to the open ticket, skipping the closed one", w.Code, w.Body)
	}
	if n := len(mustTicket(t, ctx, "e5e5e5e5eeee0005").Messages); n != 1 {
		t.Error("a closed ticket was reopened by mail")
	}
}

// A ref naming a closed ticket opens a new one, not some other thread.
func TestInboundMailRefToClosedTicketOpensNewTicket(t *testing.T) {
	ctx := supportCtx(t)
	useInboundSecret(t, testMailSecret)
	from := "inb-closed@example.com"
	cleanupInbound(t, from, "<cl-1@x>")
	saveInboundFixture(t, ctx, "f6f6f6f6ffff0006", from, false, SupportStatusOpen, time.Hour)
	saveInboundFixture(t, ctx, "a7a7a7a7aaaa0007", from, false, SupportStatusClosed, time.Minute)

	_, out := postInbound(t, testMailSecret, inboundMail(from, "<cl-1@x>", map[string]any{"ticket_ref": "a7a7a7a7"}))
	if out["action"] != "created" {
		t.Fatalf("got %v, want a new ticket", out)
	}
	if n := len(mustTicket(t, ctx, "f6f6f6f6ffff0006").Messages); n != 1 {
		t.Error("the mail landed in a thread it did not reply to")
	}
}

// An unverified From proves nothing, so it never joins an existing thread.
func TestInboundMailUnverifiedSenderOpensNewTicket(t *testing.T) {
	ctx := supportCtx(t)
	useInboundSecret(t, testMailSecret)
	from := "inb-unverified@example.com"
	cleanupInbound(t, from, "<uv-1@x>")
	saveInboundFixture(t, ctx, "b8b8b8b8bbbb0008", from, true, SupportStatusOpen, time.Minute)

	_, out := postInbound(t, testMailSecret, inboundMail(from, "<uv-1@x>", map[string]any{
		"ticket_ref": "b8b8b8b8", "sender_verified": false,
	}))
	if out["action"] != "created" || out["ticket_id"] == "b8b8b8b8bbbb0008" {
		t.Fatalf("got %v, want a new ticket", out)
	}
	if n := len(mustTicket(t, ctx, "b8b8b8b8bbbb0008").Messages); n != 1 {
		t.Error("an unverified sender wrote into an existing thread")
	}
}

// Magnet retries, and the provider redelivers: one mail, one message.
func TestInboundMailIsIdempotent(t *testing.T) {
	ctx := supportCtx(t)
	useInboundSecret(t, testMailSecret)
	from := "inb-idem@example.com"
	cleanupInbound(t, from, "<idem-1@x>", "<idem-2@x>")
	saveInboundFixture(t, ctx, "c9c9c9c9cccc0009", from, false, SupportStatusOpen, time.Minute)

	_, first := postInbound(t, testMailSecret, inboundMail(from, "<idem-1@x>", nil))
	w, second := postInbound(t, testMailSecret, inboundMail(from, "<idem-1@x>", nil))
	if w.Code != http.StatusOK || second["duplicate"] != true || second["ticket_id"] != first["ticket_id"] {
		t.Fatalf("second delivery: status %d body %s", w.Code, w.Body)
	}
	if n := len(mustTicket(t, ctx, "c9c9c9c9cccc0009").Messages); n != 2 {
		t.Errorf("thread has %d messages, want 2", n)
	}

	// The same for a mail that opened a ticket: no second ticket.
	_, a := postInbound(t, testMailSecret, inboundMail(from, "<idem-2@x>", map[string]any{"sender_verified": false}))
	_, b := postInbound(t, testMailSecret, inboundMail(from, "<idem-2@x>", map[string]any{"sender_verified": false}))
	if a["action"] != "created" || b["duplicate"] != true || a["ticket_id"] != b["ticket_id"] {
		t.Errorf("new-ticket redelivery: %v then %v", a, b)
	}
}

// No thread to join: a new ticket, anonymous, marked as mail, with no token.
func TestInboundMailOpensNewTicket(t *testing.T) {
	ctx := supportCtx(t)
	useInboundSecret(t, testMailSecret)
	from := "inb-new@example.com"
	cleanupInbound(t, from, "<new-1@x>", "<new-2@x>")

	_, out := postInbound(t, testMailSecret, inboundMail(from, "<new-1@x>", map[string]any{
		"subject": "Re: Fwd: התקבלה תשובה לפנייה שלך: בעיה בערוץ [פנייה #1a2b3c4d]",
	}))
	if out["action"] != "created" {
		t.Fatalf("got %v", out)
	}
	tk := mustTicket(t, ctx, out["ticket_id"].(string))
	if tk.Authenticated || tk.AccessToken != "" || tk.Source != "email" || tk.Status != SupportStatusOpen {
		t.Errorf("ticket = %+v", tk)
	}
	if tk.Subject != "בעיה בערוץ" || tk.Name != "שולח במייל" || tk.Email != from {
		t.Errorf("subject %q name %q email %q", tk.Subject, tk.Name, tk.Email)
	}
	if len(tk.Messages) != 1 || tk.Messages[0].Source != "email" || tk.Messages[0].Body != "תשובה במייל" {
		t.Errorf("messages = %+v", tk.Messages)
	}

	_, out = postInbound(t, testMailSecret, inboundMail(from, "<new-2@x>", map[string]any{
		"subject": "", "text": "", "from_name": "", "sender_verified": false,
	}))
	tk = mustTicket(t, ctx, out["ticket_id"].(string))
	if tk.Subject != "פנייה במייל" || tk.Messages[0].Body != "(ללא תוכן)" || tk.Name != from {
		t.Errorf("defaults: subject %q body %q name %q", tk.Subject, tk.Messages[0].Body, tk.Name)
	}
}

// The match index covers anonymous tickets, so it must never feed a user's
// "my tickets" — that list stays signed-in tickets only.
func TestInboundMatchIndexIsNotMyTickets(t *testing.T) {
	ctx := supportCtx(t)
	useTestSessionStore(t)
	useInboundSecret(t, testMailSecret)
	from := "inb-mine@example.com"
	cleanupInbound(t, from, "<mine-1@x>")
	saveInboundFixture(t, ctx, "d0d0d0d0dddd0010", from, false, SupportStatusOpen, time.Minute)
	_, out := postInbound(t, testMailSecret, inboundMail(from, "<mine-1@x>", map[string]any{"sender_verified": false}))

	ids, err := rdb.ZRange(ctx, supportMatchIndexKey(from), 0, -1).Result()
	if err != nil || len(ids) != 2 {
		t.Fatalf("match index = %v (%v), want both tickets", ids, err)
	}

	r := requestAs(t, Session{ID: "inb-mine", Email: from}, "", http.MethodGet, "/api/support/my-tickets", nil, nil)
	w := httptest.NewRecorder()
	listMySupportTickets(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("my-tickets: %d", w.Code)
	}
	var mine []SupportTicket
	_ = json.Unmarshal(w.Body.Bytes(), &mine)
	for _, tk := range mine {
		if tk.ID == "d0d0d0d0dddd0010" || tk.ID == out["ticket_id"] {
			t.Errorf("my-tickets lists %s, which was not opened signed-in", tk.ID)
		}
	}
}

func TestCleanInboundSubject(t *testing.T) {
	cases := map[string]string{
		"Re: התקבלה תשובה לפנייה שלך: בעיה [פנייה #abcdef12]": "בעיה",
		"RE: re: Fwd: שאלה":     "שאלה",
		"תגובה: נושא כלשהו":     "נושא כלשהו",
		"[פנייה #abcdef12]":     "",
		"Regarding the channel": "Regarding the channel",
		"  שורה\r\nשנייה  ":     "שורה שנייה",
	}
	for in, want := range cases {
		if got := cleanInboundSubject(in); got != want {
			t.Errorf("cleanInboundSubject(%q) = %q, want %q", in, got, want)
		}
	}
}
