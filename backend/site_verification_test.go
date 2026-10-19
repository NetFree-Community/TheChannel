package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi"
)

func TestGoogleVerificationFile(t *testing.T) {
	r := chi.NewRouter()
	for p := range googleVerificationFiles {
		r.Get(p, serveGoogleVerificationFile)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google8be09fcc16689402.html", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "google-site-verification: google8be09fcc16689402.html" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google0000000000000000.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unlisted verification file answered %d", rec.Code)
	}
}
