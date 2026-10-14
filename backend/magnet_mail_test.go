package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The mail endpoint is Magnet's; here it is an httptest server that records
// what it was sent. Handlers run against real Redis like the other handler
// tests, with a real session store.

const testMailSecret = "test-channels-secret-0123456789"

type mailHit struct {
	header http.Header
	body   map[string]any
}

type fakeMagnet struct {
	mu   sync.Mutex
	hits []mailHit
	// respond decides each answer; nil answers 200.
	respond func(n int, w http.ResponseWriter, r *http.Request)
	srv     *httptest.Server
}

func (f *fakeMagnet) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

func (f *fakeMagnet) last(t *testing.T) mailHit {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hits) == 0 {
		t.Fatal("magnet received no mail request")
	}
	return f.hits[len(f.hits)-1]
}

// useFakeMagnet points the mailer at a fresh fake, pins the base URL to the
// default and shortens the retry pauses. Every in-flight delivery is waited
// for before the variables are restored, so no goroutine outlives its test.
func useFakeMagnet(t *testing.T, respond func(n int, w http.ResponseWriter, r *http.Request)) *fakeMagnet {
	t.Helper()
	f := &fakeMagnet{respond: respond}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.hits = append(f.hits, mailHit{header: r.Header.Clone(), body: body})
		n := len(f.hits)
		f.mu.Unlock()
		if f.respond != nil {
			f.respond(n, w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))

	prevURL, prevSecret, prevEnv := magnetMailURL, magnetMailSecret, publicBaseURLEnv
	prevBackoff, prevClient, prevCfg := magnetMailBackoff, magnetMailClient, getGlobalConfig()
	magnetMailURL, magnetMailSecret, publicBaseURLEnv = f.srv.URL+"/functions/v1/channels-mail", testMailSecret, ""
	magnetMailBackoff = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	magnetMailClient = &http.Client{Timeout: 300 * time.Millisecond}
	setGlobalConfigCache(&SettingConfig{})
	t.Cleanup(func() {
		magnetMailInFlight.Wait()
		f.srv.CloseClientConnections()
		f.srv.Close()
		magnetMailURL, magnetMailSecret, publicBaseURLEnv = prevURL, prevSecret, prevEnv
		magnetMailBackoff, magnetMailClient = prevBackoff, prevClient
		setGlobalConfigCache(prevCfg)
	})
	return f
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("field %q = %#v, want a string", key, m[key])
	}
	return v
}

func obj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("field %q = %#v, want an object", key, m[key])
	}
	return v
}

// cleanupOwnedChannel removes a channel a handler created, and the owner
// grant and audit record that came with it.
func cleanupOwnedChannel(t *testing.T, slug, email string) {
	t.Helper()
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dbDeleteChannel(cctx, slug)
		_ = dbUpdateUsersList(cctx, func(users []User) []User {
			kept := make([]User, 0, len(users))
			for _, u := range users {
				if u.Email != email {
					kept = append(kept, u)
				}
			}
			return kept
		})
		rdb.Del(cctx, "channel_create:lock:"+email)
	})
}

func createChannelAs(t *testing.T, email, slug string) *httptest.ResponseRecorder {
	t.Helper()
	cleanupOwnedChannel(t, slug, email)
	sess := Session{ID: "mail-" + slug, Email: email, Username: email, PublicName: "בעל הערוץ"}
	r := requestAs(t, sess, "", http.MethodPost, "/api/channels/create",
		strings.NewReader(`{"slug":"`+slug+`","name":"ערוץ הבדיקה","description":"תיאור"}`), nil)
	w := httptest.NewRecorder()
	createChannelSelfService(w, r)
	return w
}

func TestMagnetMailChannelCreatedSelfService(t *testing.T) {
	useTestSessionStore(t)
	f := useFakeMagnet(t, nil)

	const email, slug = "mail-owner-a@example.com", "mail-created-a"
	w := createChannelAs(t, email, slug)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %q", w.Code, w.Body.String())
	}
	magnetMailInFlight.Wait()

	if n := f.count(); n != 1 {
		t.Fatalf("magnet got %d requests, want 1", n)
	}
	hit := f.last(t)
	if got := hit.header.Get("x-channels-secret"); got != testMailSecret {
		t.Errorf("x-channels-secret = %q", got)
	}
	if got := hit.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}

	b := hit.body
	base := defaultPublicBaseURL
	if str(t, b, "event") != "channel_created" {
		t.Errorf("event = %v", b["event"])
	}
	if str(t, b, "idempotency_key") != "channel_created:"+slug {
		t.Errorf("idempotency_key = %v", b["idempotency_key"])
	}
	if str(t, b, "to") != email {
		t.Errorf("to = %v", b["to"])
	}
	if str(t, b, "to_name") != "בעל הערוץ" {
		t.Errorf("to_name = %v", b["to_name"])
	}
	ch := obj(t, b, "channel")
	if str(t, ch, "slug") != slug || str(t, ch, "name") != "ערוץ הבדיקה" ||
		str(t, ch, "url") != base+"/channel/"+slug || str(t, ch, "manage_url") != base+"/channel/"+slug+"/manage" {
		t.Errorf("channel = %v", ch)
	}
	if str(t, b, "contact_url") != base+"/?channel="+slug+"&support=1" {
		t.Errorf("contact_url = %v", b["contact_url"])
	}
	if _, has := b["ticket"]; has {
		t.Errorf("channel_created must not carry a ticket: %v", b["ticket"])
	}
}

// Unconfigured means off: the channel opens and nothing leaves the server.
func TestMagnetMailOffWhenUnconfigured(t *testing.T) {
	useTestSessionStore(t)
	f := useFakeMagnet(t, nil)

	for _, tc := range []struct{ name, url, secret, slug string }{
		{"no url", "", testMailSecret, "mail-off-nourl"},
		{"no secret", f.srv.URL, "", "mail-off-nosecret"},
	} {
		magnetMailURL, magnetMailSecret = tc.url, tc.secret
		w := createChannelAs(t, "mail-off-"+tc.slug+"@example.com", tc.slug)
		if w.Code != http.StatusCreated {
			t.Fatalf("%s: create status %d", tc.name, w.Code)
		}
	}
	magnetMailInFlight.Wait()
	if n := f.count(); n != 0 {
		t.Errorf("magnet got %d requests with the feature off, want 0", n)
	}
}

// A failing Magnet is retried a bounded number of times in the background,
// and the channel is created regardless.
func TestMagnetMailServerErrorIsRetriedAndCreationSucceeds(t *testing.T) {
	useTestSessionStore(t)
	f := useFakeMagnet(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	})

	w := createChannelAs(t, "mail-500@example.com", "mail-upstream-500")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d", w.Code)
	}
	magnetMailInFlight.Wait()
	if n := f.count(); n != magnetMailAttempts {
		t.Errorf("magnet got %d attempts, want %d", n, magnetMailAttempts)
	}
	keys := map[string]bool{}
	for _, h := range f.hits {
		keys[str(t, h.body, "idempotency_key")] = true
	}
	if len(keys) != 1 {
		t.Errorf("retries must reuse one idempotency key, got %v", keys)
	}
}

// A hanging Magnet must not hold the user's response: the handler answers
// before the mailer has even given up.
func TestMagnetMailHangingServerDoesNotBlockCreation(t *testing.T) {
	useTestSessionStore(t)
	release := make(chan struct{})
	f := useFakeMagnet(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	// Long enough that a handler waiting on even one attempt would show.
	// useFakeMagnet restores the client afterwards.
	magnetMailClient = &http.Client{Timeout: time.Second}

	start := time.Now()
	w := createChannelAs(t, "mail-hang@example.com", "mail-upstream-hang")
	elapsed := time.Since(start)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d", w.Code)
	}
	if elapsed > 900*time.Millisecond {
		t.Errorf("create took %v with Magnet hanging; it must not wait for the mail", elapsed)
	}
	magnetMailInFlight.Wait()
	if n := f.count(); n != magnetMailAttempts {
		t.Errorf("timed-out attempts = %d, want %d", n, magnetMailAttempts)
	}
}

func TestMagnetMailClientErrorIsNotRetried(t *testing.T) {
	useTestSessionStore(t)
	f := useFakeMagnet(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_url"}`, http.StatusBadRequest)
	})

	if w := createChannelAs(t, "mail-400@example.com", "mail-upstream-400"); w.Code != http.StatusCreated {
		t.Fatalf("create: status %d", w.Code)
	}
	magnetMailInFlight.Wait()
	if n := f.count(); n != 1 {
		t.Errorf("magnet got %d attempts after a 400, want exactly 1", n)
	}
}

func adminReply(t *testing.T, sess *Session, id, body string) {
	t.Helper()
	r := requestAs(t, *sess, "", http.MethodPost, "/api/super-admin/support/tickets/"+id+"/reply",
		strings.NewReader(`{"body":"`+body+`"}`), map[string]string{"id": id})
	w := httptest.NewRecorder()
	adminReplySupportTicket(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin reply: status %d, body %q", w.Code, w.Body.String())
	}
}

var testOperator = Session{ID: "op-1", Email: "operator-mail@example.com", PublicName: "Real Operator Name"}

func TestMagnetMailSupportReply(t *testing.T) {
	ctx := supportCtx(t)
	useTestSessionStore(t)
	f := useFakeMagnet(t, nil)

	ticket := newTestTicket(t, ctx, "mail-sup-1", "asker@example.com")
	adminReply(t, &testOperator, ticket.ID, "התשובה שלנו")
	magnetMailInFlight.Wait()

	hit := f.last(t)
	if hit.header.Get("x-channels-secret") != testMailSecret {
		t.Error("missing secret header")
	}
	b := hit.body
	base := defaultPublicBaseURL
	if str(t, b, "event") != "support_reply" || str(t, b, "to") != "asker@example.com" ||
		str(t, b, "to_name") != "בודק" {
		t.Errorf("support mail = %v", b)
	}
	if str(t, b, "idempotency_key") != "support_reply:mail-sup-1:1" {
		t.Errorf("idempotency_key = %v", b["idempotency_key"])
	}
	if v, present := b["channel"]; !present || v != nil {
		t.Errorf("channel = %#v, want an explicit null for a general ticket", v)
	}
	if str(t, b, "contact_url") != base+"/?support=1" {
		t.Errorf("contact_url = %v", b["contact_url"])
	}
	tk := obj(t, b, "ticket")
	if str(t, tk, "id") != "mail-sup-1" || str(t, tk, "subject") != "נושא" ||
		str(t, tk, "reply_text") != "התשובה שלנו" ||
		str(t, tk, "url") != base+"/?support=1&ticket=mail-sup-1" {
		t.Errorf("ticket = %v", tk)
	}

	// Nothing of the operator, and never the anonymous access token.
	raw, _ := json.Marshal(b)
	for _, leak := range []string{testOperator.Email, testOperator.PublicName, ticket.AccessToken} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("mail payload leaks %q: %s", leak, raw)
		}
	}

	// A second answer is a second mail with its own stable key.
	adminReply(t, &testOperator, ticket.ID, "עוד תשובה")
	magnetMailInFlight.Wait()
	if got := str(t, f.last(t).body, "idempotency_key"); got != "support_reply:mail-sup-1:2" {
		t.Errorf("second reply key = %q", got)
	}
}

// Only the management's answer is mailed: the requester adding to their own
// thread, an operator answering a thread they opened, and a ticket with no
// address all send nothing.
func TestMagnetMailSupportReplyNotSent(t *testing.T) {
	ctx := supportCtx(t)
	useTestSessionStore(t)
	f := useFakeMagnet(t, nil)

	// The requester replies (anonymous ticket, token header).
	anon := newTestTicket(t, ctx, "mail-sup-self", "self@example.com")
	anon.Authenticated = false
	if err := dbSaveSupportTicket(ctx, anon); err != nil {
		t.Fatal(err)
	}
	r := requestAs(t, Session{}, "", http.MethodPost, "/api/support/tickets/"+anon.ID+"/reply",
		strings.NewReader(`{"body":"עוד פרט"}`), map[string]string{"id": anon.ID})
	r.Header.Set(ticketTokenHeader, anon.AccessToken)
	// Its own address, so the shared per-IP submit limiter cannot refuse it.
	r.RemoteAddr = "203.0.113.71:4000"
	w := httptest.NewRecorder()
	replySupportTicket(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("requester reply: status %d %q", w.Code, w.Body.String())
	}

	// The operator answers their own ticket.
	own := newTestTicket(t, ctx, "mail-sup-own", testOperator.Email)
	adminReply(t, &testOperator, own.ID, "בדיקה")

	// No address on the ticket.
	noMail := newTestTicket(t, ctx, "mail-sup-nomail", "")
	adminReply(t, &testOperator, noMail.ID, "תשובה")

	magnetMailInFlight.Wait()
	if n := f.count(); n != 0 {
		t.Errorf("magnet got %d requests, want 0: %v", n, f.hits)
	}
}

// ChannelSlug is free text from the requester, so a restricted channel's name
// is only mailed to someone entitled to it.
func TestMagnetMailSupportReplyChannel(t *testing.T) {
	ctx := supportCtx(t)
	useTestSessionStore(t)
	f := useFakeMagnet(t, nil)

	seedChannel(t, ctx, "mail-public-ch", Settings{})
	private := &ChannelData{Slug: "mail-private-ch", Name: "סודי", CreatedAt: time.Now(),
		Features: ChannelFeatures{RequireAuth: true}}
	if err := dbCreateChannel(ctx, private); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbDeleteChannel(context.Background(), private.Slug) })

	pub := newTestTicket(t, ctx, "mail-sup-pub", "pub@example.com")
	pub.ChannelSlug = "mail-public-ch"
	priv := newTestTicket(t, ctx, "mail-sup-priv", "stranger@example.com")
	priv.ChannelSlug = private.Slug
	priv.Authenticated = false
	for _, tk := range []*SupportTicket{pub, priv} {
		if err := dbSaveSupportTicket(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}

	adminReply(t, &testOperator, pub.ID, "א")
	magnetMailInFlight.Wait()
	b := f.last(t).body
	ch := obj(t, b, "channel")
	if str(t, ch, "slug") != "mail-public-ch" {
		t.Errorf("public channel = %v", ch)
	}
	if str(t, b, "contact_url") != defaultPublicBaseURL+"/?channel=mail-public-ch&support=1" {
		t.Errorf("contact_url = %v", b["contact_url"])
	}

	adminReply(t, &testOperator, priv.ID, "ב")
	magnetMailInFlight.Wait()
	b = f.last(t).body
	if v := b["channel"]; v != nil {
		t.Errorf("a restricted channel named by a stranger must not be mailed: %v", v)
	}
	if strings.Contains(str(t, b, "contact_url"), "mail-private-ch") {
		t.Errorf("contact_url names the private channel: %v", b["contact_url"])
	}
}

func TestMagnetMailIdempotencyKeysAreStable(t *testing.T) {
	ch := &ChannelData{Slug: "stable-slug", Name: "x"}
	a, _ := channelCreatedMail(ch, "o@example.com", "")
	b, _ := channelCreatedMail(ch, "o@example.com", "שם אחר")
	if a.IdempotencyKey != b.IdempotencyKey || a.IdempotencyKey != "channel_created:stable-slug" {
		t.Errorf("channel keys %q / %q", a.IdempotencyKey, b.IdempotencyKey)
	}

	tk := &SupportTicket{ID: "tid", Email: "r@example.com", Messages: []SupportMessage{
		{Author: "user", Body: "q"}, {Author: "admin", Body: "a"},
	}}
	x, ok1 := supportReplyMail(tk, 1, nil)
	y, ok2 := supportReplyMail(tk, 1, nil)
	if !ok1 || !ok2 || x.IdempotencyKey != y.IdempotencyKey || x.IdempotencyKey != "support_reply:tid:1" {
		t.Errorf("support keys %v / %v", x, y)
	}
	// The requester's own message is never mailed.
	if _, ok := supportReplyMail(tk, 0, nil); ok {
		t.Error("a user message must not build a support_reply mail")
	}
	if _, ok := channelCreatedMail(ch, "", ""); ok {
		t.Error("no address, no mail")
	}
}

// Magnet answers 409 while the same key is mid-send elsewhere; that is a
// "try again", unlike the other 4xx answers.
func TestMagnetMailInProgressIsRetried(t *testing.T) {
	f := useFakeMagnet(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			http.Error(w, `{"error":"in_progress"}`, http.StatusConflict)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	ev, _ := channelCreatedMail(&ChannelData{Slug: "inprogress-slug", Name: "x"}, "o@example.com", "")
	if err := deliverMagnetMail(context.Background(), ev); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if n := f.count(); n != 2 {
		t.Errorf("attempts = %d, want 2", n)
	}
}

// Magnet refuses control characters outside reply_text with a 400, which is
// never retried — so they are flattened here instead of losing the mail.
func TestMagnetMailFlattensControlCharacters(t *testing.T) {
	ch := &ChannelData{Slug: "ctl-slug", Name: "שם\nעם\tשורות"}
	ev, _ := channelCreatedMail(ch, "o@example.com", "בעלים\r\nשני")
	if ev.Channel.Name != "שם עם שורות" || ev.ToName == nil || *ev.ToName != "בעלים שני" {
		t.Errorf("name = %q, to_name = %v", ev.Channel.Name, ev.ToName)
	}
	tk := &SupportTicket{ID: "ctl", Subject: "נושא\nשבור", Email: "r@example.com",
		Messages: []SupportMessage{{Author: "admin", Body: "שורה\nשנייה"}}}
	sr, _ := supportReplyMail(tk, 0, nil)
	if sr.Ticket.Subject != "נושא שבור" || sr.Ticket.ReplyText != "שורה\nשנייה" {
		t.Errorf("subject = %q, reply = %q (reply_text keeps its newlines)", sr.Ticket.Subject, sr.Ticket.ReplyText)
	}
}

func TestPublicBaseURLPrecedence(t *testing.T) {
	prevCfg, prevEnv := getGlobalConfig(), publicBaseURLEnv
	t.Cleanup(func() { setGlobalConfigCache(prevCfg); publicBaseURLEnv = prevEnv })

	setGlobalConfigCache(&SettingConfig{})
	publicBaseURLEnv = ""
	if got := publicBaseURL(); got != defaultPublicBaseURL {
		t.Errorf("default = %q", got)
	}
	publicBaseURLEnv = "https://env.example.com/"
	if got := publicBaseURL(); got != "https://env.example.com" {
		t.Errorf("env = %q", got)
	}
	setGlobalConfigCache(&SettingConfig{ProjectDomain: "https://arootz.magnetads.co.il/"})
	if got := publicBaseURL(); got != "https://arootz.magnetads.co.il" {
		t.Errorf("project_domain = %q", got)
	}
	// A non-https setting is skipped rather than mailed.
	setGlobalConfigCache(&SettingConfig{ProjectDomain: "http://insecure.example.com"})
	publicBaseURLEnv = ""
	if got := publicBaseURL(); got != defaultPublicBaseURL {
		t.Errorf("http project_domain = %q", got)
	}
}
