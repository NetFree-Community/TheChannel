package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi"
)

func supportCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newTestTicket writes a ticket straight to the store, bypassing the handler,
// so the storage-level assertions below do not depend on HTTP plumbing.
func newTestTicket(t *testing.T, ctx context.Context, id, email string) *SupportTicket {
	t.Helper()
	now := time.Now()
	ticket := &SupportTicket{
		ID:            id,
		Subject:       "נושא",
		Name:          "בודק",
		Email:         email,
		Authenticated: true,
		Status:        SupportStatusOpen,
		Messages: []SupportMessage{
			{Author: "user", AuthorName: "בודק", Body: "גוף ההודעה", CreatedAt: now},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		AccessToken: "token-" + id,
	}
	if err := dbSaveSupportTicket(ctx, ticket); err != nil {
		t.Fatalf("save ticket %s: %v", id, err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rdb.Del(cctx, supportTicketKey(id))
		rdb.ZRem(cctx, supportTicketIndexKey, id)
		if email != "" {
			rdb.ZRem(cctx, supportUserIndexKey(email), id)
		}
	})
	return ticket
}

// The access token is the only credential guarding an anonymous thread, so it
// must never travel back out in a response body. Every handler serialises
// through publicView; this pins that.
func TestSupportPublicViewStripsAccessToken(t *testing.T) {
	ctx := supportCtx(t)
	ticket := newTestTicket(t, ctx, "sup-strip", "a@example.com")

	view := publicView(ticket)
	if view.AccessToken != "" {
		t.Errorf("access token must not survive publicView, got %q", view.AccessToken)
	}
	// The struct is copied, so the stored ticket keeps its token.
	if ticket.AccessToken == "" {
		t.Error("publicView must not clear the token on the original")
	}

	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "token-sup-strip") {
		t.Errorf("serialised ticket leaks the access token: %s", data)
	}
	// omitempty: the field should be absent entirely, not present and empty.
	if strings.Contains(string(data), "accessToken") {
		t.Errorf("accessToken key should be omitted, got %s", data)
	}
}

// An operator reply is served to the requester, so it must be signed by the
// management and carry nothing of the operator's own identity. The handler must
// not even look at the session for a name: whatever it would find there (display
// name, Google username, email) is exactly what must stay hidden.
func TestSupportAdminReplyIsSignedByManagement(t *testing.T) {
	ctx := supportCtx(t)
	newTestTicket(t, ctx, "sup-anon-op", "requester@example.com")

	r := httptest.NewRequest(http.MethodPost, "/api/super-admin/support/tickets/sup-anon-op/reply",
		strings.NewReader(`{"body":"תשובה מהמערכת"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "sup-anon-op")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	adminReplySupportTicket(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reply: status %d, body %q", w.Code, w.Body.String())
	}

	stored, err := dbGetSupportTicket(ctx, "sup-anon-op")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	last := stored.Messages[len(stored.Messages)-1]
	if last.Author != "admin" || last.AuthorName != operatorName {
		t.Errorf("stored operator reply = %+v, want author admin signed %q", last, operatorName)
	}
}

// Replies written before operator replies were signed by the management still
// hold the operator's name or email, and are served for the ticket's lifetime.
// publicView is what every handler sends, so it must re-sign them — without
// touching the requester's own name or the stored ticket.
func TestSupportPublicViewHidesOperatorIdentity(t *testing.T) {
	ctx := supportCtx(t)
	ticket := newTestTicket(t, ctx, "sup-legacy-op", "requester@example.com")
	const operator = "Real Operator op.private@gmail.com"
	appendSupportMessage(ticket, "admin", operator, "תשובה", SupportStatusAnswered)

	view := publicView(ticket)
	if got := view.Messages[1].AuthorName; got != operatorName {
		t.Errorf("operator reply shown as %q, want %q", got, operatorName)
	}
	if got := view.Messages[0].AuthorName; got != "בודק" {
		t.Errorf("the requester's own name must be kept, got %q", got)
	}
	if ticket.Messages[1].AuthorName != operator {
		t.Error("publicView must not rewrite the stored ticket")
	}

	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"Real Operator", "op.private"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("serialised ticket leaks the operator's identity (%q): %s", leak, data)
		}
	}
}

// A ticket must be reachable by its own token and by nothing else.
func TestSupportTicketRoundTrip(t *testing.T) {
	ctx := supportCtx(t)
	newTestTicket(t, ctx, "sup-rt", "b@example.com")

	got, err := dbGetSupportTicket(ctx, "sup-rt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccessToken != "token-sup-rt" {
		t.Errorf("token not persisted, got %q", got.AccessToken)
	}
	if len(got.Messages) != 1 || got.Messages[0].Body != "גוף ההודעה" {
		t.Errorf("thread not persisted: %+v", got.Messages)
	}
	if got.Status != SupportStatusOpen {
		t.Errorf("status = %q, want open", got.Status)
	}
}

// The per-user index is what makes "my tickets" work without scanning every
// ticket, and it must be case-insensitive: Google hands back addresses whose
// capitalisation is not stable.
func TestSupportUserIndexIsCaseInsensitive(t *testing.T) {
	ctx := supportCtx(t)
	newTestTicket(t, ctx, "sup-case", "Mixed.Case@Example.COM")

	for _, probe := range []string{
		"Mixed.Case@Example.COM",
		"mixed.case@example.com",
		"MIXED.CASE@EXAMPLE.COM",
	} {
		tickets, err := dbListSupportTickets(ctx, supportUserIndexKey(probe))
		if err != nil {
			t.Fatalf("list for %s: %v", probe, err)
		}
		if len(tickets) != 1 || tickets[0].ID != "sup-case" {
			t.Errorf("%s: expected to find the ticket, got %d", probe, len(tickets))
		}
	}
}

// A user index must only ever surface that user's own tickets.
func TestSupportUserIndexIsolatesUsers(t *testing.T) {
	ctx := supportCtx(t)
	newTestTicket(t, ctx, "sup-mine", "mine@example.com")
	newTestTicket(t, ctx, "sup-theirs", "theirs@example.com")

	tickets, err := dbListSupportTickets(ctx, supportUserIndexKey("mine@example.com"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, tk := range tickets {
		if tk.ID == "sup-theirs" {
			t.Fatal("another user's ticket appeared in a personal listing")
		}
	}
	if len(tickets) != 1 {
		t.Errorf("expected exactly the caller's ticket, got %d", len(tickets))
	}
}

// An anonymous ticket carries whatever address the visitor typed, so it must
// never be listed under that address: otherwise anyone could plant a thread in
// a user's "my tickets" and, holding the access token, read the replies.
func TestSupportAnonymousTicketIsNotIndexedByEmail(t *testing.T) {
	ctx := supportCtx(t)
	victim := "victim@example.com"
	newTestTicket(t, ctx, "sup-victim", victim)

	fresh := &SupportTicket{
		ID: "sup-anon-2", Subject: "נושא", Name: "אורח", Email: victim,
		Status: SupportStatusOpen, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Messages: []SupportMessage{{Author: "user", Body: "x", CreatedAt: time.Now()}},
	}
	if err := dbSaveSupportTicket(ctx, fresh); err != nil {
		t.Fatalf("save fresh anonymous ticket: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rdb.Del(cctx, supportTicketKey("sup-anon-2"))
		rdb.ZRem(cctx, supportTicketIndexKey, "sup-anon-2")
		rdb.ZRem(cctx, supportUserIndexKey(victim), "sup-anon-2")
	})

	tickets, err := dbListSupportTickets(ctx, supportUserIndexKey(victim))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, tk := range tickets {
		if tk.ID == "sup-anon-2" {
			t.Fatal("an anonymous ticket was listed under the typed email")
		}
	}
	if len(tickets) != 1 || tickets[0].ID != "sup-victim" {
		t.Errorf("expected only the signed-in user's ticket, got %d", len(tickets))
	}
}

// The update path (a reply, a status change) re-indexes the ticket, and used
// to do so under the typed e-mail regardless of Authenticated — so the anonymous
// thread the save path kept out of the victim's list reappeared there as soon
// as anyone answered it.
func TestSupportAnonymousTicketStaysUnindexedAfterUpdate(t *testing.T) {
	ctx := supportCtx(t)
	victim := "victim-upd@example.com"
	newTestTicket(t, ctx, "sup-victim-upd", victim)

	anon := &SupportTicket{
		ID: "sup-anon-upd", Subject: "נושא", Name: "אורח", Email: victim,
		Status: SupportStatusOpen, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Messages: []SupportMessage{{Author: "user", Body: "x", CreatedAt: time.Now()}},
	}
	if err := dbSaveSupportTicket(ctx, anon); err != nil {
		t.Fatalf("save anonymous ticket: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rdb.Del(cctx, supportTicketKey("sup-anon-upd"))
		rdb.ZRem(cctx, supportTicketIndexKey, "sup-anon-upd")
		rdb.ZRem(cctx, supportUserIndexKey(victim), "sup-anon-upd")
	})

	if _, err := dbUpdateSupportTicket(ctx, "sup-anon-upd", func(tk *SupportTicket) error {
		appendSupportMessage(tk, "admin", "מנהל", "תשובה", SupportStatusAnswered)
		return nil
	}); err != nil {
		t.Fatalf("reply to anonymous ticket: %v", err)
	}

	tickets, err := dbListSupportTickets(ctx, supportUserIndexKey(victim))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, tk := range tickets {
		if tk.ID == "sup-anon-upd" {
			t.Fatal("a reply re-indexed the anonymous ticket under the typed email")
		}
	}
	if len(tickets) != 1 || tickets[0].ID != "sup-victim-upd" {
		t.Errorf("expected only the signed-in user's ticket, got %d", len(tickets))
	}
	// The same update on a signed-in ticket must still refresh its index entry.
	if _, err := dbUpdateSupportTicket(ctx, "sup-victim-upd", func(tk *SupportTicket) error {
		appendSupportMessage(tk, "admin", "מנהל", "תשובה", SupportStatusAnswered)
		return nil
	}); err != nil {
		t.Fatalf("reply to signed-in ticket: %v", err)
	}
	if n, _ := rdb.ZScore(ctx, supportUserIndexKey(victim), "sup-victim-upd").Result(); n == 0 {
		t.Error("signed-in ticket lost its per-user index entry after an update")
	}
}

// The operator inbox is ordered by last activity, so a thread that just
// received a reply rises to the top rather than staying at its creation time.
func TestSupportInboxOrdersByLastActivity(t *testing.T) {
	ctx := supportCtx(t)
	older := newTestTicket(t, ctx, "sup-order-a", "a@example.com")
	newTestTicket(t, ctx, "sup-order-b", "b@example.com")

	// The older ticket gets an answer and should overtake the newer one.
	appendSupportMessage(older, "admin", "מנהל", "תשובה", SupportStatusAnswered)
	older.UpdatedAt = time.Now().Add(time.Minute)
	if err := dbSaveSupportTicket(ctx, older); err != nil {
		t.Fatalf("save: %v", err)
	}

	tickets, err := dbListSupportTickets(ctx, supportTicketIndexKey)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var seenA, seenB int = -1, -1
	for i, tk := range tickets {
		switch tk.ID {
		case "sup-order-a":
			seenA = i
		case "sup-order-b":
			seenB = i
		}
	}
	if seenA < 0 || seenB < 0 {
		t.Fatalf("both tickets should be listed, got a=%d b=%d", seenA, seenB)
	}
	if seenA > seenB {
		t.Error("the recently answered ticket should sort ahead of the untouched newer one")
	}
}

// appendSupportMessage is what keeps the thread, the status and the index
// score in step; a reply that did not move UpdatedAt would sort wrongly.
func TestSupportAppendMessageAdvancesStatusAndTime(t *testing.T) {
	ctx := supportCtx(t)
	ticket := newTestTicket(t, ctx, "sup-append", "c@example.com")
	before := ticket.UpdatedAt

	time.Sleep(10 * time.Millisecond)
	appendSupportMessage(ticket, "admin", "מנהל", "תשובה", SupportStatusAnswered)

	if len(ticket.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ticket.Messages))
	}
	last := ticket.Messages[1]
	if last.Author != "admin" || last.Body != "תשובה" {
		t.Errorf("unexpected appended message: %+v", last)
	}
	if ticket.Status != SupportStatusAnswered {
		t.Errorf("status = %q, want answered", ticket.Status)
	}
	if !ticket.UpdatedAt.After(before) {
		t.Error("UpdatedAt must advance, or the inbox ordering is wrong")
	}
}

func TestSupportStatusValidation(t *testing.T) {
	for _, s := range []SupportStatus{SupportStatusOpen, SupportStatusAnswered, SupportStatusClosed} {
		if !validSupportStatus(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	// Anything else arrives straight from a request body.
	for _, s := range []SupportStatus{"", "deleted", "OPEN", "open "} {
		if validSupportStatus(s) {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestSupportEmailShapeCheck(t *testing.T) {
	valid := []string{"a@b.co", "first.last@sub.example.com", "x+tag@example.org"}
	for _, e := range valid {
		if !looksLikeEmail(e) {
			t.Errorf("%q should pass", e)
		}
	}
	invalid := []string{"", "nope", "@example.com", "a@", "a@b", "a b@example.com", "a@ex ample.com", "a@example.com\n"}
	for _, e := range invalid {
		if looksLikeEmail(e) {
			t.Errorf("%q should be rejected", e)
		}
	}
}

// Free-text fields land in a Redis value read back in full on every view, so
// the cap is enforced on write. Truncation must not panic on any input.
func TestSupportTrimTo(t *testing.T) {
	if got := trimTo("  hello  ", 100); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if got := trimTo("abcdef", 3); got != "abc" {
		t.Errorf("got %q, want %q", got, "abc")
	}
	if got := trimTo("   ", 10); got != "" {
		t.Errorf("whitespace-only should trim to empty, got %q", got)
	}
	if got := trimTo("", 0); got != "" {
		t.Errorf("empty input with zero cap should stay empty, got %q", got)
	}
}

// A signed-in ticket is reachable by its session only. Tickets created before
// tokens stopped being issued to signed-in senders still carry one, and the
// browser keeps sending it from localStorage after logout — so the token path
// must not open those, while it stays the one way into an anonymous thread.
func TestSupportTokenOnlyAuthorisesAnonymousTickets(t *testing.T) {
	ctx := supportCtx(t)
	newTestTicket(t, ctx, "sup-authed-tok", "owner@example.com") // Authenticated, with a legacy token

	anon := &SupportTicket{
		ID: "sup-anon-tok", Subject: "נושא", Name: "אורח", Email: "guest@example.com",
		Status: SupportStatusOpen, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Messages:    []SupportMessage{{Author: "user", Body: "x", CreatedAt: time.Now()}},
		AccessToken: "token-sup-anon-tok",
	}
	if err := dbSaveSupportTicket(ctx, anon); err != nil {
		t.Fatalf("save anonymous ticket: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rdb.Del(cctx, supportTicketKey("sup-anon-tok"))
		rdb.ZRem(cctx, supportTicketIndexKey, "sup-anon-tok")
	})

	withToken := func(id, token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/support/tickets/"+id, nil)
		r.Header.Set(ticketTokenHeader, token)
		return r.WithContext(ctx)
	}

	if _, ok := authoriseTicket(ctx, withToken("sup-authed-tok", "token-sup-authed-tok"), "sup-authed-tok"); ok {
		t.Fatal("a legacy token opened a signed-in ticket without its session")
	}
	if _, ok := authoriseTicket(ctx, withToken("sup-anon-tok", "token-sup-anon-tok"), "sup-anon-tok"); !ok {
		t.Fatal("the token did not open the anonymous ticket it was issued for")
	}
	if _, ok := authoriseTicket(ctx, withToken("sup-anon-tok", "wrong"), "sup-anon-tok"); ok {
		t.Fatal("a wrong token opened an anonymous ticket")
	}
	if _, ok := authoriseTicket(ctx, withToken("sup-anon-tok", ""), "sup-anon-tok"); ok {
		t.Fatal("an empty token opened an anonymous ticket")
	}
}
