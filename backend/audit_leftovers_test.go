package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A support token that still arrives as ?token= must be moved into the header
// and out of the URL before the request logger (which prints RequestURI) sees
// it; a token already in the header wins; other routes are left untouched.
func TestHideTicketTokenQuery(t *testing.T) {
	var seen *http.Request
	h := hideTicketTokenQuery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r }))

	r := httptest.NewRequest("GET", "/api/support/tickets/abc?token=s3cr3t&x=1", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got := seen.Header.Get(ticketTokenHeader); got != "s3cr3t" {
		t.Errorf("header = %q, want the query token", got)
	}
	if strings.Contains(seen.URL.RawQuery, "s3cr3t") || strings.Contains(seen.RequestURI, "s3cr3t") {
		t.Errorf("token still in URL: RawQuery=%q RequestURI=%q", seen.URL.RawQuery, seen.RequestURI)
	}
	if seen.URL.Query().Get("x") != "1" || seen.RequestURI != "/api/support/tickets/abc?x=1" {
		t.Errorf("other query parameters lost: RawQuery=%q RequestURI=%q", seen.URL.RawQuery, seen.RequestURI)
	}
	if ticketToken(seen) != "s3cr3t" {
		t.Errorf("ticketToken did not see the lifted token")
	}

	// Header first: a query token never overrides one the client sent properly.
	r = httptest.NewRequest("GET", "/api/support/tickets/abc?token=stale", nil)
	r.Header.Set(ticketTokenHeader, "fresh")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got := seen.Header.Get(ticketTokenHeader); got != "fresh" {
		t.Errorf("header = %q, want the header token to win", got)
	}
	if strings.Contains(seen.RequestURI, "stale") {
		t.Errorf("stale query token still logged: %q", seen.RequestURI)
	}

	// Any other route keeps its query string as sent.
	r = httptest.NewRequest("GET", "/api/channel/x/messages?token=keep", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen.URL.RawQuery != "token=keep" || seen.Header.Get(ticketTokenHeader) != "" {
		t.Errorf("non-support route was rewritten: RawQuery=%q header=%q", seen.URL.RawQuery, seen.Header.Get(ticketTokenHeader))
	}
}

// Only the FCM token alphabet is accepted; anything else could never be
// delivered to and would only fill the subscription set.
func TestFcmTokenShape(t *testing.T) {
	ok := []string{"abc:DEF-ghi_123", strings.Repeat("a", 50) + ":" + strings.Repeat("Z", 100)}
	for _, tok := range ok {
		if !fcmTokenRe.MatchString(tok) {
			t.Errorf("%q should be accepted", tok)
		}
	}
	bad := []string{"", "abc def", "abc/def", "abc+def", "abc=def", "אבג", "abc\n"}
	for _, tok := range bad {
		if fcmTokenRe.MatchString(tok) {
			t.Errorf("%q should be refused", tok)
		}
	}
}

// The SSE stream re-reads the role per event from the live map, so a change to
// the map must change the answer with no reconnect.
func TestEmailHasChannelRoleFollowsLiveMap(t *testing.T) {
	const email = "sse-role-test@example.com"
	t.Cleanup(func() { privilegesUsers.Delete(email) })

	if emailHasChannelRole(email, "ch", RoleWriter) {
		t.Fatal("unknown e-mail should hold no role")
	}
	if emailHasChannelRole("", "ch", RoleWriter) {
		t.Fatal("empty e-mail should hold no role")
	}
	privilegesUsers.Store(email, User{Email: email, ChannelRoles: map[string]ChannelRole{"ch": RoleModerator}})
	if !emailHasChannelRole(email, "ch", RoleWriter) {
		t.Error("moderator should satisfy writer")
	}
	if emailHasChannelRole(email, "ch", RoleOwner) {
		t.Error("moderator should not satisfy owner")
	}
	if emailHasChannelRole(email, "other", RoleWriter) {
		t.Error("role on one channel must not leak to another")
	}
	// Demotion takes effect on the next call.
	privilegesUsers.Store(email, User{Email: email})
	if emailHasChannelRole(email, "ch", RoleWriter) {
		t.Error("demoted user still seen as writer")
	}
	privilegesUsers.Store(email, User{Email: email, GlobalRole: RoleSuperAdmin})
	if !emailHasChannelRole(email, "ch", RoleOwner) {
		t.Error("super admin should satisfy every channel role")
	}
}
