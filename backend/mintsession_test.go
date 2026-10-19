package main

import (
	"encoding/gob"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/boj/redistore"
)

// TestMintSessionCookie is a local UI-testing aid, not a product test. It mints
// a real signed session cookie through the application's own session store so a
// browser can drive authenticated screens (the channel admin dialog) without
// standing up Google OAuth.
//
// It only runs when MINT_SESSION_FOR is set, so a normal `go test ./...` skips
// it entirely and it can never mint anything in CI or production.
func TestMintSessionCookie(t *testing.T) {
	email := os.Getenv("MINT_SESSION_FOR")
	if email == "" {
		t.Skip("set MINT_SESSION_FOR=<email> to mint a session cookie")
	}
	slug := os.Getenv("MINT_SESSION_SLUG")
	if slug == "" {
		slug = "click"
	}

	// store is built in main(), which never runs under test, so stand up the
	// same one here with the same secret — that is what makes the cookie valid
	// for a server started with this SECRET_KEY.
	gob.Register(Session{})
	if store == nil || store.Pool == nil {
		s, err := redistore.NewRediStore(10, redisType, redisAddr, "", redisPass, []byte(secretKey))
		if err != nil {
			t.Fatalf("session store: %v", err)
		}
		defer s.Close()
		store = s
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	// store.New rather than store.Get: Get goes through gorilla's per-request
	// registry, which a synthetic httptest request has not set up.
	session, err := store.New(r, cookieName)
	if err != nil {
		t.Logf("store.New returned %v (expected for a request with no cookie)", err)
	}
	if session == nil {
		t.Fatal("no session returned")
	}
	session.Values["user"] = Session{
		ID:         "mint-test",
		Username:   email,
		Email:      email,
		PublicName: "Test Owner",
		// Owner of the given channel: enough to open the admin dialog and reach
		// the owner-only settings screen.
		ChannelRoles: map[string]ChannelRole{slug: RoleOwner},
	}
	session.Options.MaxAge = 60 * 60
	if err := session.Save(r, w); err != nil {
		t.Fatalf("session.Save: %v", err)
	}

	cookie := w.Header().Get("Set-Cookie")
	if cookie == "" {
		t.Fatal("no Set-Cookie produced")
	}
	t.Logf("MINTED_COOKIE=%s", cookie)
}
