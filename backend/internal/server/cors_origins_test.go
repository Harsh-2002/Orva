package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Access-Control-Allow-Origin takes one origin or "*", never a list. Joining
// the configured origins into "a, b" produced a value every browser rejects,
// so configuring two origins silently broke CORS for both.
func TestCORSMultiOriginEchoesRequestOrigin(t *testing.T) {
	origins := []string{"https://a.example.com", "https://b.example.com"}
	h := corsMiddleware(origins, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, want := range origins {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
		req.Header.Set("Origin", want)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("Allow-Origin for %s = %q, want %q", want, got, want)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); strings.Contains(got, ",") {
			t.Errorf("Allow-Origin = %q, must never be a list", got)
		}
		if !strings.Contains(w.Header().Get("Vary"), "Origin") {
			t.Error("Vary: Origin missing — caches would serve one origin's response to another")
		}
	}
}

func TestCORSRejectsUnlistedOrigin(t *testing.T) {
	h := corsMiddleware([]string{"https://a.example.com"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q for an unlisted origin, want no header", got)
	}
}

// The default (no origins configured, or an explicit "*") stays wide open.
func TestCORSWildcardUnchanged(t *testing.T) {
	for _, cfg := range [][]string{nil, {"*"}} {
		h := corsMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
		req.Header.Set("Origin", "https://anything.example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Allow-Origin for cfg %v = %q, want *", cfg, got)
		}
	}
}
