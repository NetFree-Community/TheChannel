package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegalPagesRender(t *testing.T) {
	for i := range legalPages {
		p := &legalPages[i]
		rec := httptest.NewRecorder()
		serveLegalPage(rec, httptest.NewRequest(http.MethodGet, p.Path, nil), p)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", p.Path, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{"<h1>" + p.Title + "</h1>", "<h2>", `href="/privacy"`, `href="/terms"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", p.Path, want)
			}
		}
		if strings.Contains(body, "&lt;h2&gt;") {
			t.Errorf("%s: body was escaped", p.Path)
		}

		rec = httptest.NewRecorder()
		serveLegalPage(rec, httptest.NewRequest(http.MethodGet, p.Path+"/", nil), p)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != p.Path {
			t.Errorf("%s/: got %d to %q, want a redirect to the canonical path", p.Path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestLegalPathsAreReservedSlugs(t *testing.T) {
	for _, p := range legalPages {
		if !isReservedSlug(strings.TrimPrefix(p.Path, "/")) {
			t.Errorf("%s is not a reserved slug", p.Path)
		}
	}
}
