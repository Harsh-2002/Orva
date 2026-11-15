package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
)

func TestInternalInvokeChainStopsAtMaxDepth(t *testing.T) {
	auth := sdkauth.New()
	h := &InternalInvokeHandler{SDKAuth: auth}
	for depth := 0; depth <= defaultMaxCallDepth; depth++ {
		release := auth.BindExecution("exec-hop", "fn-self", "t", "s", time.Now(), depth)
		got, ok := auth.ExecutionDepth("exec-hop", "fn-self")
		if !ok || got != depth {
			t.Fatalf("depth=%d got=%d,%v", depth, got, ok)
		}
		err := h.CheckDepth(got)
		release()
		if depth == defaultMaxCallDepth {
			if err == nil || err.Status != http.StatusInsufficientStorage || err.Opts.Code != "MAX_CALL_DEPTH" {
				t.Fatalf("cap was not enforced: %+v", err)
			}
		} else if err != nil {
			t.Fatalf("depth %d rejected: %v", depth, err)
		}
	}
}

func TestInternalInvokeCustomDepthCap(t *testing.T) {
	h := &InternalInvokeHandler{MaxCallDepth: 2}
	if err := h.CheckDepth(1); err != nil {
		t.Fatal(err)
	}
	if err := h.CheckDepth(2); err == nil || err.Opts.Code != "MAX_CALL_DEPTH" {
		t.Fatalf("custom cap: %v", err)
	}
}
