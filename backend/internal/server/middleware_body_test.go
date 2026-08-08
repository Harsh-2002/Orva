package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBodySizeMiddlewareDeployExempt is the M4 guard: the 6MB JSON body cap
// must NOT apply to the code-upload endpoints, otherwise a function larger than
// ~6MB fails to deploy even though the handler allows up to MaxCodeSize (50MB).
// A large body to a normal endpoint is still rejected with 413.
func TestBodySizeMiddlewareDeployExempt(t *testing.T) {
	const cap = 1 << 20 // 1MB cap for the test
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	mw := bodySizeMiddleware(cap, next)

	big := strings.Repeat("x", cap*3) // 3MB, well over the cap

	cases := []struct {
		name       string
		method     string
		path       string
		wantReach  bool
		wantStatus int
	}{
		{"deploy multipart exempt", http.MethodPost, "/api/v1/functions/abc/deploy", true, http.StatusOK},
		{"deploy-inline exempt", http.MethodPost, "/api/v1/functions/abc/deploy-inline", true, http.StatusOK},
		{"restore still exempt", http.MethodPost, "/api/v1/restore", true, http.StatusOK},
		{"other endpoint capped", http.MethodPost, "/api/v1/functions", false, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(big))
			req.ContentLength = int64(len(big))
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)
			if reached != c.wantReach {
				t.Errorf("reached=%v, want %v", reached, c.wantReach)
			}
			if w.Code != c.wantStatus {
				t.Errorf("status=%d, want %d", w.Code, c.wantStatus)
			}
		})
	}
}
