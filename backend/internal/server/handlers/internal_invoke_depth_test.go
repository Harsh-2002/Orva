package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
)

// callInternal posts to the F2F entrypoint as the caller bound to execID,
// optionally sending a forged X-Orva-Call-Depth. The target function does not
// exist, so a request that passes the depth gate ends in 404 and one that
// does not ends in 507, with no pool needed.
func callInternal(t *testing.T, h *InternalInvokeHandler, stream bool, execID, fnID string, header *string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/_internal/invoke/missing-target"
	if stream {
		path += "/stream"
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
	req.SetPathValue("name", "missing-target")
	req.Header.Set("X-Orva-Internal-Token", mintLive(h.SDKAuth, fnID))
	req.Header.Set("X-Orva-Execution-Id", execID)
	if header != nil {
		req.Header.Set("X-Orva-Call-Depth", *header)
	}
	w := httptest.NewRecorder()
	if stream {
		h.InvokeStream(w, req)
	} else {
		h.Invoke(w, req)
	}
	return w
}

// The depth cap must hold when the caller omits or forges the header: it is
// the only thing bounding A -> A -> A recursion.
func TestInternalInvokeDepthIgnoresCallerHeader(t *testing.T) {
	forged := []*string{nil}
	for _, v := range []string{"0", "-1", "-1000000", "1", "99999999999999999999"} {
		v := v
		forged = append(forged, &v)
	}
	for _, stream := range []bool{false, true} {
		for _, hdr := range forged {
			name := "omitted"
			if hdr != nil {
				name = "header=" + *hdr
			}
			t.Run(name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				auth := sdkauth.New([]byte("process-secret"))
				h := &InternalInvokeHandler{DB: newTestDB(t), SDKAuth: auth}
				// A caller already at the cap: its callee would be depth 9.
				defer auth.BindExecution("exec-deep", "fn-loop", "t", "s", time.Now(), defaultMaxCallDepth)()
				w := callInternal(t, h, stream, "exec-deep", "fn-loop", hdr)
				if w.Code != http.StatusInsufficientStorage {
					t.Fatalf("status=%d body=%s; want 507", w.Code, w.Body.String())
				}
				// A caller one below the cap is still allowed through the gate
				// (and then fails to find its target) whatever it claims.
				defer auth.BindExecution("exec-ok", "fn-loop", "t", "s", time.Now(), defaultMaxCallDepth-1)()
				w = callInternal(t, h, stream, "exec-ok", "fn-loop", hdr)
				if w.Code != http.StatusNotFound {
					t.Fatalf("status=%d body=%s; want 404 past the depth gate", w.Code, w.Body.String())
				}
			})
		}
	}
}

// Walks the chain the host would build: each hop binds callerDepth+1 and the
// 8th callee is the last one admitted.
func TestInternalInvokeChainStopsAtMaxDepth(t *testing.T) {
	auth := sdkauth.New([]byte("process-secret"))
	h := &InternalInvokeHandler{DB: newTestDB(t), SDKAuth: auth}
	depth := 0
	for hop := 1; ; hop++ {
		release := auth.BindExecution("exec-hop", "fn-self", "t", "s", time.Now(), depth)
		got, ok := auth.ExecutionDepth("exec-hop", "fn-self")
		if !ok || got != depth {
			t.Fatalf("hop %d: ExecutionDepth=%d,%v want %d", hop, got, ok, depth)
		}
		w := callInternal(t, h, false, "exec-hop", "fn-self", nil) // never sends a header
		release()
		if w.Code == http.StatusInsufficientStorage {
			if depth != defaultMaxCallDepth {
				t.Fatalf("stopped at caller depth %d, want %d", depth, defaultMaxCallDepth)
			}
			return
		}
		if hop > 2*defaultMaxCallDepth {
			t.Fatal("recursion was never stopped")
		}
		depth++ // what a successful F2F callee binds
	}
}

func TestInternalInvokeRejectsUnboundCaller(t *testing.T) {
	auth := sdkauth.New([]byte("process-secret"))
	h := &InternalInvokeHandler{DB: newTestDB(t), SDKAuth: auth}
	defer auth.BindExecution("exec-1", "fn-owner", "t", "s", time.Now(), 0)()
	if w := callInternal(t, h, false, "exec-1", "fn-someone-else", nil); w.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", w.Code)
	}
}
