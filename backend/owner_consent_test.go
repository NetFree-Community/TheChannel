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

func consentRequest(t *testing.T, sess Session, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	r := requestAs(t, sess, "", method, "/api/me/consent", rdr, nil)
	w := httptest.NewRecorder()
	if method == http.MethodGet {
		getMyConsent(w, r)
	} else {
		setMyConsent(w, r)
	}
	return w
}

func cleanupConsent(t *testing.T, email string) {
	t.Helper()
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rdb.HDel(cctx, ownerConsentsKey, normEmail(email))
	})
}

func TestOwnerConsentRoundTrip(t *testing.T) {
	ctx := testCtx(t)
	useTestSessionStore(t)
	const email = "consent-rt@example.com"
	cleanupConsent(t, email)
	sess := Session{ID: "consent-rt", Email: email, Username: email, PublicName: "=בודק"}

	w := consentRequest(t, sess, http.MethodGet, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"terms_accepted":false`) ||
		!strings.Contains(w.Body.String(), `"marketing":null`) {
		t.Fatalf("fresh account: %d %s, want terms_accepted false and marketing null", w.Code, w.Body)
	}

	// Refusing the terms is not a way to record anything.
	if w := consentRequest(t, sess, http.MethodPost, `{"accept_terms":false,"marketing":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("accept_terms false: %d, want 400", w.Code)
	}

	if w := consentRequest(t, sess, http.MethodPost, `{"accept_terms":true,"marketing":true}`); w.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	first, err := dbGetOwnerConsent(ctx, email)
	if err != nil || first == nil || !first.Marketing || first.TermsVersion == "" {
		t.Fatalf("stored = %+v, %v; want marketing yes with a terms version", first, err)
	}

	// Withdrawing the mail keeps the original acceptance date.
	time.Sleep(10 * time.Millisecond)
	if w := consentRequest(t, sess, http.MethodPost, `{"accept_terms":true,"marketing":false}`); w.Code != http.StatusOK {
		t.Fatalf("withdraw: %d", w.Code)
	}
	second, _ := dbGetOwnerConsent(ctx, email)
	if second.Marketing || !second.TermsAcceptedAt.Equal(first.TermsAcceptedAt) || !second.MarketingAt.After(first.MarketingAt) {
		t.Fatalf("after withdraw = %+v (first %+v)", second, first)
	}

	// The export neutralises a formula in the display name.
	rec := httptest.NewRecorder()
	exportOwnerConsents(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/consents.csv", nil))
	if !strings.Contains(rec.Body.String(), email+",'=בודק,no,") {
		t.Fatalf("export row missing or unsafe:\n%s", rec.Body)
	}
}

func TestCreateChannelNeedsTermsAccepted(t *testing.T) {
	ctx := testCtx(t)
	useTestSessionStore(t)
	const email = "consent-create@example.com"
	const slug = "consent-create-ch"
	cleanupOwnedChannel(t, slug, email)
	sess := Session{ID: "consent-create", Email: email, Username: email, PublicName: "בעלים"}

	create := func(body string) *httptest.ResponseRecorder {
		r := requestAs(t, sess, "", http.MethodPost, "/api/channels/create", strings.NewReader(body), nil)
		w := httptest.NewRecorder()
		createChannelSelfService(w, r)
		return w
	}

	if w := create(`{"slug":"` + slug + `","name":"ערוץ"}`); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("no consent: %d %s, want 428", w.Code, w.Body)
	}
	if exists, _ := dbChannelExists(ctx, slug); exists {
		t.Fatal("channel was created without the terms accepted")
	}

	if w := consentRequest(t, sess, http.MethodPost, `{"accept_terms":true,"marketing":false}`); w.Code != http.StatusOK {
		t.Fatalf("accept: %d", w.Code)
	}
	w := create(`{"slug":"` + slug + `","name":"ערוץ"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("after consent: %d %s, want 201", w.Code, w.Body)
	}
	var out map[string]string
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["slug"] != slug {
		t.Fatalf("created %v", out)
	}
}
