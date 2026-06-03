package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// deadlineRW is a ResponseWriter that supports SetWriteDeadline and records when
// it's called — standing in for the real *http.response.
type deadlineRW struct {
	http.ResponseWriter
	deadlineSet bool
}

func (d *deadlineRW) SetWriteDeadline(time.Time) error { d.deadlineSet = true; return nil }
func (d *deadlineRW) Flush()                           {}

// TestStatusRecorderUnwrapReachesDeadline guards the SSE-no-freeze fix: the
// logger's statusRecorder must expose Unwrap() so http.ResponseController can
// reach the underlying connection and clear the server WriteTimeout. Without
// Unwrap, SetWriteDeadline silently no-ops and long SSE chats/builds get cut at
// the 60s write deadline.
func TestStatusRecorderUnwrapReachesDeadline(t *testing.T) {
	under := &deadlineRW{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{ResponseWriter: under, status: 200}

	if err := http.NewResponseController(rec).SetWriteDeadline(time.Time{}); err != nil {
		t.Fatalf("SetWriteDeadline through statusRecorder returned error: %v", err)
	}
	if !under.deadlineSet {
		t.Fatal("SetWriteDeadline did not reach the underlying ResponseWriter — statusRecorder.Unwrap() missing or broken")
	}

	// Flush must still reach through too (SSE relies on it).
	if _, ok := interface{}(rec).(http.Flusher); !ok {
		t.Error("statusRecorder no longer implements http.Flusher")
	}
}
