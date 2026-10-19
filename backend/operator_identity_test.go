package main

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boj/redistore"
	"github.com/go-chi/chi"
)

// The operator, as their Google account would really identify them. None of
// these three may appear in anything served to anyone else.
const (
	testOperatorEmail = "real.operator@example.com"
	testOperatorID    = "109876543210987654321"
	testOperatorName  = "Real Operator Name"
)

// operatorFixture makes testOperatorEmail a super admin and ownerEmail the
// owner of slug, in both the live privileges map and the stored users list,
// and returns sessions for each. The operator also holds an explicit owner role
// on slug, exactly as self-service creation leaves it.
func operatorFixture(t *testing.T, slug, ownerEmail string) (operator, owner Session) {
	t.Helper()
	ctx := supportCtx(t)

	opUser := User{
		ID: testOperatorID, Username: testOperatorName, Email: testOperatorEmail,
		PublicName: testOperatorName, GlobalRole: RoleSuperAdmin,
		ChannelRoles: map[string]ChannelRole{slug: RoleOwner},
	}
	ownerUser := User{
		ID: "owner-" + slug, Username: "Channel Owner", Email: ownerEmail,
		PublicName:   "Channel Owner",
		ChannelRoles: map[string]ChannelRole{slug: RoleOwner},
	}
	privilegesUsers.Store(opUser.Email, opUser)
	privilegesUsers.Store(ownerUser.Email, ownerUser)
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		return append(users, opUser, ownerUser)
	}); err != nil {
		t.Fatalf("seed users list: %v", err)
	}
	t.Cleanup(func() {
		privilegesUsers.Delete(opUser.Email)
		privilegesUsers.Delete(ownerUser.Email)
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		dbUpdateUsersList(cctx, func(users []User) []User {
			kept := users[:0]
			for _, u := range users {
				if u.Email != opUser.Email && u.Email != ownerUser.Email {
					kept = append(kept, u)
				}
			}
			return kept
		})
		for _, pattern := range []string{"channel:" + slug + ":*"} {
			keys, _ := rdb.Keys(cctx, pattern).Result()
			if len(keys) > 0 {
				rdb.Del(cctx, keys...)
			}
		}
	})

	operator = Session{ID: opUser.ID, Username: opUser.Username, Email: opUser.Email,
		PublicName: opUser.PublicName, GlobalRole: RoleSuperAdmin}
	owner = Session{ID: ownerUser.ID, Username: ownerUser.Username, Email: ownerUser.Email,
		PublicName: ownerUser.PublicName, ChannelRoles: ownerUser.ChannelRoles}
	return operator, owner
}

// useTestSessionStore swaps in a real Redis-backed session store for the test,
// as main() does at boot, so handlers can resolve a session from a cookie.
func useTestSessionStore(t *testing.T) {
	t.Helper()
	gob.Register(Session{})
	s, err := redistore.NewRediStore(10, redisType, redisAddr, "", redisPass, []byte("operator-identity-test"))
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	prev := store
	store = s
	t.Cleanup(func() {
		store = prev
		s.Close()
	})
}

// requestAs builds a request carrying a signed session cookie for sess and the
// channel context channelMiddleware would have attached.
func requestAs(t *testing.T, sess Session, slug, method, target string, body io.Reader, params map[string]string) *http.Request {
	t.Helper()
	mr := httptest.NewRequest(http.MethodGet, "/", nil)
	mw := httptest.NewRecorder()
	session, _ := store.New(mr, cookieName)
	session.Values["user"] = sess
	if err := session.Save(mr, mw); err != nil {
		t.Fatalf("session save: %v", err)
	}
	// redistore keeps each session for 30 days under its default prefix.
	sessionKey := "session_" + session.ID
	t.Cleanup(func() { rdb.Del(context.Background(), sessionKey) })

	r := httptest.NewRequest(method, target, body)
	for _, c := range mw.Result().Cookies() {
		r.AddCookie(c)
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, channelCtxKey, &ChannelData{Slug: slug})
	return r.WithContext(ctx)
}

func assertNoOperatorIdentity(t *testing.T, where, data string) {
	t.Helper()
	for _, leak := range []string{testOperatorEmail, testOperatorID, testOperatorName} {
		if strings.Contains(data, leak) {
			t.Errorf("%s leaks the operator's identity (%q): %s", where, leak, data)
		}
	}
}

// A post the operator writes is stored, returned and published under the
// management label; an ordinary staff member's post keeps their own name.
func TestOperatorPostIsSignedAsManagement(t *testing.T) {
	useTestSessionStore(t)
	const slug = "op-id-post"
	operator, owner := operatorFixture(t, slug, "owner.post@example.com")
	ctx := supportCtx(t)

	post := func(sess Session, text string) Message {
		t.Helper()
		r := requestAs(t, sess, slug, http.MethodPost, "/api/channel/"+slug+"/admin/new",
			strings.NewReader(`{"type":"md","text":"`+text+`"}`), nil)
		w := httptest.NewRecorder()
		addMessage(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("addMessage: status %d, body %q", w.Code, w.Body.String())
		}
		assertNoOperatorIdentity(t, "addMessage response", w.Body.String())
		var m Message
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return m
	}

	opPost := post(operator, "from the operator")
	if opPost.Author != operatorName || opPost.AuthorId != operatorAuthorId {
		t.Errorf("operator post signed %q/%q, want %q/%q", opPost.Author, opPost.AuthorId, operatorName, operatorAuthorId)
	}
	stored, err := dbGetMessageFields(ctx, slug, fmt.Sprint(opPost.ID))
	if err != nil {
		t.Fatalf("stored post: %v", err)
	}
	if stored["author"] != operatorName || stored["authorId"] != operatorAuthorId {
		t.Errorf("operator post stored as %q/%q", stored["author"], stored["authorId"])
	}

	ownerPost := post(owner, "from the owner")
	if ownerPost.Author != "Channel Owner" || ownerPost.AuthorId != owner.ID {
		t.Errorf("an ordinary author must keep their own name, got %q/%q", ownerPost.Author, ownerPost.AuthorId)
	}

	events, err := rdb.XRange(ctx, "channel:"+slug+":events", "-", "+").Result()
	if err != nil || len(events) == 0 {
		t.Fatalf("expected published events, got %d (%v)", len(events), err)
	}
	for _, ev := range events {
		data, _ := ev.Values["data"].(string)
		assertNoOperatorIdentity(t, "stored event", data)
	}
}

// Posts written before operators were anonymised still carry the operator's
// name and Google id. Staff read real authors, so the read path must re-label
// them — and re-saving one must store it re-labelled.
func TestLegacyOperatorPostIsRelabelledForStaff(t *testing.T) {
	useTestSessionStore(t)
	const slug = "op-id-legacy"
	operator, owner := operatorFixture(t, slug, "owner.legacy@example.com")
	ctx := supportCtx(t)

	legacy := &Message{ID: 1, Type: "md", Text: "old post", Author: testOperatorName,
		AuthorId: testOperatorID, Timestamp: time.Now().Add(-time.Hour)}
	if err := setMessage(ctx, slug, legacy, false); err != nil {
		t.Fatalf("seed legacy post: %v", err)
	}
	staff := &Message{ID: 2, Type: "md", Text: "staff post", Author: "Channel Owner",
		AuthorId: owner.ID, Timestamp: time.Now()}
	if err := setMessage(ctx, slug, staff, false); err != nil {
		t.Fatalf("seed staff post: %v", err)
	}

	r := requestAs(t, owner, slug, http.MethodGet, "/api/channel/"+slug+"/messages?offset=0&limit=20", nil, nil)
	w := httptest.NewRecorder()
	getMessages(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("getMessages: status %d", w.Code)
	}
	assertNoOperatorIdentity(t, "GET /messages for the owner", w.Body.String())
	var got []Message
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[int]Message{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if m := byID[1]; m.Author != operatorName || m.AuthorId != operatorAuthorId {
		t.Errorf("legacy operator post shown as %q/%q", m.Author, m.AuthorId)
	}
	if m := byID[2]; m.Author != "Channel Owner" || m.AuthorId != owner.ID {
		t.Errorf("staff post must keep its real author for staff, got %q/%q", m.Author, m.AuthorId)
	}

	// The operator edits the old post: it is stored re-labelled from now on.
	r = requestAs(t, operator, slug, http.MethodPost, "/api/channel/"+slug+"/admin/edit-message",
		strings.NewReader(`{"id":1,"type":"md","text":"edited"}`), nil)
	w = httptest.NewRecorder()
	updateMessage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("updateMessage: status %d, body %q", w.Code, w.Body.String())
	}
	stored, _ := dbGetMessageFields(ctx, slug, "1")
	if stored["author"] != operatorName || stored["authorId"] != operatorAuthorId {
		t.Errorf("re-saved legacy post stored as %q/%q", stored["author"], stored["authorId"])
	}
}

// The event stream replays up to ~1000 old entries to reconnecting staff, so
// the re-labelling is applied per event on the way out.
func TestOperatorEventIsRelabelledForStaff(t *testing.T) {
	ops := operatorSet{
		ids:    map[string]struct{}{testOperatorID: {}},
		emails: map[string]struct{}{testOperatorEmail: {}},
	}

	opEvent, _ := json.Marshal(PushMessage{Type: "new-message", M: Message{
		ID: 7, Text: "hi", Author: testOperatorName, AuthorId: testOperatorID}})
	out := ops.anonymiseOperatorEvent(string(opEvent))
	assertNoOperatorIdentity(t, "operator event", out)
	var pm PushMessage
	if err := json.Unmarshal([]byte(out), &pm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pm.Type != "new-message" || pm.M.ID != 7 || pm.M.Text != "hi" ||
		pm.M.Author != operatorName || pm.M.AuthorId != operatorAuthorId {
		t.Errorf("re-labelled event = %+v", pm)
	}

	// Anything else passes through byte for byte.
	for _, data := range []string{
		`{"type":"heartbeat"}`,
		`{"type":"delete-message","message":{"id":7,"deleted":true}}`,
		string(mustJSON(t, PushMessage{Type: "new-message", M: Message{ID: 8, Author: "Someone", AuthorId: "555"}})),
		"not json " + testOperatorID,
	} {
		if got := ops.anonymiseOperatorEvent(data); got != data {
			t.Errorf("event changed:\n in: %s\nout: %s", data, got)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// A report the operator files, and one filed before operators were anonymised,
// both reach the channel's moderators without the operator's email, name or id.
func TestOperatorReportIsAnonymous(t *testing.T) {
	useTestSessionStore(t)
	const slug = "op-id-report"
	operator, owner := operatorFixture(t, slug, "owner.report@example.com")
	ctx := supportCtx(t)

	for id := 1; id <= 2; id++ {
		if err := setMessage(ctx, slug, &Message{ID: id, Type: "md", Text: "post", Timestamp: time.Now()}, false); err != nil {
			t.Fatalf("seed post: %v", err)
		}
	}

	// The report limiter is process-wide; a repeated run must not trip it.
	reportLimiters.Delete(testOperatorEmail)
	t.Cleanup(func() { reportLimiters.Delete(testOperatorEmail) })

	r := requestAs(t, operator, slug, http.MethodPost, "/api/channel/"+slug+"/messages/report",
		strings.NewReader(`{"messageId":1,"reason":"spam"}`), nil)
	w := httptest.NewRecorder()
	reportMessage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reportMessage: status %d, body %q", w.Code, w.Body.String())
	}
	// Checked at rest, not only through getReports: the read path re-labels
	// anything it recognises, which would hide a report stored with the
	// operator's details.
	stored, err := rdb.HGetAll(ctx, "channel:"+slug+":report:1").Result()
	if err != nil {
		t.Fatalf("stored report: %v", err)
	}
	if stored["reporterName"] != operatorName || stored["reportedEmail"] != "" || stored["reporterId"] != operatorAuthorId {
		t.Errorf("operator report stored as %q/%q/%q", stored["reporterName"], stored["reportedEmail"], stored["reporterId"])
	}
	assertNoOperatorIdentity(t, "stored report", fmt.Sprint(stored))

	legacy := &Report{MessageId: 2, Reason: "old", CreatedAt: time.Now(),
		ReporterID: testOperatorID, ReportedEmail: testOperatorEmail, ReporterName: testOperatorName}
	if err := dbReportMessage(ctx, slug, legacy); err != nil {
		t.Fatalf("seed legacy report: %v", err)
	}

	r = requestAs(t, owner, slug, http.MethodGet, "/api/channel/"+slug+"/admin/reports/get?status=all", nil, nil)
	w = httptest.NewRecorder()
	getReports(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("getReports: status %d", w.Code)
	}
	assertNoOperatorIdentity(t, "reports for the owner", w.Body.String())
	var reps []Report
	if err := json.Unmarshal(w.Body.Bytes(), &reps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(reps) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reps))
	}
	for _, rep := range reps {
		if rep.ReporterName != operatorName || rep.ReportedEmail != "" || rep.ReporterID != operatorAuthorId {
			t.Errorf("report %d shown as %q/%q/%q", rep.MessageId, rep.ReporterName, rep.ReportedEmail, rep.ReporterID)
		}
	}
}

// A channel's owner screen lists who holds a role on it. The operator holds
// owner on every channel they created, and that row must not be listed to its
// co-owners; the operator's own view of the list still shows everyone.
func TestOwnerUserListHidesOperators(t *testing.T) {
	useTestSessionStore(t)
	const slug = "op-id-users"
	operator, owner := operatorFixture(t, slug, "owner.users@example.com")

	list := func(sess Session) string {
		r := requestAs(t, sess, slug, http.MethodGet, "/api/channel/"+slug+"/admin/users/get", nil, nil)
		w := httptest.NewRecorder()
		getChannelUsers(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("getChannelUsers: status %d", w.Code)
		}
		return w.Body.String()
	}

	ownerView := list(owner)
	assertNoOperatorIdentity(t, "owner's user list", ownerView)
	if !strings.Contains(ownerView, owner.Email) {
		t.Errorf("the owner must still see the channel's other users: %s", ownerView)
	}
	if opView := list(operator); !strings.Contains(opView, testOperatorEmail) {
		t.Errorf("the operator's own view should list everyone: %s", opView)
	}
}

// A writer role an owner grants to the operator's email is the owner's own
// doing: it must be listed like any other, or the row would vanish after the
// save and confirm that the address they typed is the operator's.
func TestOwnerSeesOperatorEmailTheyGranted(t *testing.T) {
	useTestSessionStore(t)
	const slug = "op-id-granted"
	const other = "someone.else.granted@example.com"
	_, owner := operatorFixture(t, slug, "owner.granted@example.com")
	ctx := supportCtx(t)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		privilegesUsers.Delete(other)
		dbUpdateUsersList(cctx, func(users []User) []User {
			kept := users[:0]
			for _, u := range users {
				if u.Email != other {
					kept = append(kept, u)
				}
			}
			return kept
		})
	})

	// This time the operator holds no role on the channel.
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		for i := range users {
			if users[i].Email == testOperatorEmail {
				delete(users[i].ChannelRoles, slug)
			}
		}
		return users
	}); err != nil {
		t.Fatalf("drop operator role: %v", err)
	}

	r := requestAs(t, owner, slug, http.MethodPost, "/api/channel/"+slug+"/admin/users/set",
		strings.NewReader(`{"users":[{"email":"`+testOperatorEmail+`","role":"writer"},{"email":"`+other+`","role":"writer"}]}`), nil)
	w := httptest.NewRecorder()
	setChannelUsers(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("setChannelUsers: status %d, body %q", w.Code, w.Body.String())
	}

	r = requestAs(t, owner, slug, http.MethodGet, "/api/channel/"+slug+"/admin/users/get", nil, nil)
	w = httptest.NewRecorder()
	getChannelUsers(w, r)
	for _, email := range []string{testOperatorEmail, other} {
		if !strings.Contains(w.Body.String(), email) {
			t.Errorf("a role the owner granted to %s must be listed: %s", email, w.Body.String())
		}
	}
}

// A post written before its author's record learned their Google id must
// still be attributable, or a later promotion to operator could not re-label
// it. addMessage records the id as it posts.
func TestAuthorIDIsRecordedWhenPosting(t *testing.T) {
	useTestSessionStore(t)
	const slug = "op-id-record"
	_, _ = operatorFixture(t, slug, "owner.record@example.com")
	const lateEmail, lateID = "late.writer@example.com", "200000000000000000001"
	ctx := supportCtx(t)

	// As dbAssignChannelRole or an owner's invitation leaves it: a role, no id.
	late := User{Email: lateEmail, ChannelRoles: map[string]ChannelRole{slug: RoleWriter}}
	privilegesUsers.Store(lateEmail, late)
	if err := dbUpdateUsersList(ctx, func(users []User) []User { return append(users, late) }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		privilegesUsers.Delete(lateEmail)
		dbUpdateUsersList(cctx, func(users []User) []User {
			kept := users[:0]
			for _, u := range users {
				if u.Email != lateEmail {
					kept = append(kept, u)
				}
			}
			return kept
		})
	})

	sess := Session{ID: lateID, Username: "Late Writer", Email: lateEmail, PublicName: "Late Writer",
		ChannelRoles: late.ChannelRoles}
	r := requestAs(t, sess, slug, http.MethodPost, "/api/channel/"+slug+"/admin/new",
		strings.NewReader(`{"type":"md","text":"hello"}`), nil)
	w := httptest.NewRecorder()
	addMessage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("addMessage: status %d, body %q", w.Code, w.Body.String())
	}

	if v, _ := privilegesUsers.Load(lateEmail); v.(User).ID != lateID {
		t.Errorf("live record id = %q, want %q", v.(User).ID, lateID)
	}
	users, err := dbGetUsersList(ctx)
	if err != nil {
		t.Fatalf("users list: %v", err)
	}
	for _, u := range users {
		if u.Email == lateEmail && u.ID != lateID {
			t.Errorf("stored record id = %q, want %q", u.ID, lateID)
		}
	}

	// Promoted later: the post is now recognised as an operator's.
	v, _ := privilegesUsers.Load(lateEmail)
	promoted := v.(User)
	promoted.GlobalRole = RoleSuperAdmin
	privilegesUsers.Store(lateEmail, promoted)
	if !currentOperators().hasID(lateID) {
		t.Error("a promoted author's posts must be recognisable as an operator's")
	}
}
