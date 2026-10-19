package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Handlers decode straight into memory with json.NewDecoder(r.Body). The cap is
// applied once as router middleware rather than at each of the ~25 decode sites,
// so this pins both halves of that contract: JSON bodies are bounded, and the
// multipart upload path — which enforces its own, much larger, per-channel
// limit — is left alone.
func TestLimitRequestBody(t *testing.T) {
	// Reads the body exactly as a real handler would.
	drain := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		for {
			_, err := r.Body.Read(buf)
			if err != nil {
				if strings.Contains(err.Error(), "too large") {
					http.Error(w, "too large", http.StatusRequestEntityTooLarge)
					return
				}
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	h := limitRequestBody(drain)

	cases := []struct {
		name        string
		path        string
		contentType string
		size        int
		wantStatus  int
	}{
		{"small json passes", "/api/anything", "application/json", 1024, http.StatusOK},
		{"json just under the cap passes", "/api/anything", "application/json", maxJSONRequestBody - 1024, http.StatusOK},
		{"oversized json is refused", "/api/anything", "application/json", maxJSONRequestBody + (1 << 20), http.StatusRequestEntityTooLarge},
		// uploadFile sets its own MaxBytesReader from the channel's configured
		// limit, which is far above the JSON cap; capping it here would break
		// every file upload.
		{"multipart upload route is exempt", "/api/channel/x/admin/upload", "multipart/form-data; boundary=xyz", maxJSONRequestBody + (1 << 20), http.StatusOK},
		// The exemption is by route, not by Content-Type: a client can label
		// any body multipart, and the JSON handlers must stay capped regardless.
		{"multipart content type on a json route is still capped", "/api/channel/x/admin/new", "multipart/form-data; boundary=xyz", maxJSONRequestBody + (1 << 20), http.StatusRequestEntityTooLarge},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(strings.Repeat("a", c.size)))
			req.Header.Set("Content-Type", c.contentType)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Errorf("%d byte %s body: got status %d, want %d",
					c.size, c.contentType, rec.Code, c.wantStatus)
			}
		})
	}
}
