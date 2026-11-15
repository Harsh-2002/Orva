package sdkauth

import (
	"testing"
	"time"
)

func TestExecutionDepthIsHostRecordedAndScoped(t *testing.T) {
	auth := New()
	release := auth.BindExecution("exec-d", "fn-a", "t", "s", time.Now(), 3)
	if d, ok := auth.ExecutionDepth("exec-d", "fn-a"); !ok || d != 3 {
		t.Fatalf("ExecutionDepth = %d, %v; want 3, true", d, ok)
	}
	if _, ok := auth.ExecutionDepth("exec-d", "fn-b"); ok {
		t.Fatal("ExecutionDepth answered for a different function")
	}
	if _, ok := auth.ExecutionDepth("missing", "fn-a"); ok {
		t.Fatal("ExecutionDepth answered for an unknown execution")
	}
	release()
	release()
	if _, ok := auth.ExecutionDepth("exec-d", "fn-a"); ok {
		t.Fatal("ExecutionDepth answered after the binding was released")
	}
}

func TestExecutionOwnershipIsScopedAndRemoved(t *testing.T) {
	auth := New()
	started := time.Now().Add(-time.Second)
	release := auth.BindExecution("exec-1", "fn-a", "trace-a", "span-a", started, 0)
	if !auth.OwnsExecution("exec-1", "fn-a") || auth.OwnsExecution("exec-1", "fn-b") {
		t.Fatal("execution ownership was not function scoped")
	}
	if got, ok := auth.ExecutionStart("exec-1", "fn-a"); !ok || !got.Equal(started) {
		t.Fatalf("ExecutionStart = %v, %v", got, ok)
	}
	traceID, spanID, gotStart, ok := auth.TraceContext("exec-1", "fn-a")
	if !ok || traceID != "trace-a" || spanID != "span-a" || !gotStart.Equal(started) {
		t.Fatalf("TraceContext = %q, %q, %v, %v", traceID, spanID, gotStart, ok)
	}
	if traceID, spanID, _, ok := auth.TraceContext("exec-1", "fn-b"); ok || traceID != "" || spanID != "" {
		t.Fatal("trace context leaked across function identity")
	}
	release()
	if auth.OwnsExecution("exec-1", "fn-a") {
		t.Fatal("execution remained active after release")
	}
	if _, _, _, ok := auth.TraceContext("exec-1", "fn-a"); ok {
		t.Fatal("trace context survived release")
	}
}

func TestExecutionBindingsAreIndependent(t *testing.T) {
	auth := New()
	releaseFirst := auth.BindExecution("exec-1", "fn-a", "t1", "s1", time.Now(), 1)
	defer auth.BindExecution("exec-2", "fn-a", "t2", "s2", time.Now(), 2)()
	releaseFirst()
	if auth.OwnsExecution("exec-1", "fn-a") || !auth.OwnsExecution("exec-2", "fn-a") {
		t.Fatal("releasing one execution affected another")
	}
	if depth, ok := auth.ExecutionDepth("exec-2", "fn-a"); !ok || depth != 2 {
		t.Fatal("surviving execution lost depth")
	}
}

func TestInvalidExecutionBindingIsHarmless(t *testing.T) {
	var absent *Authenticator
	for _, auth := range []*Authenticator{nil, New()} {
		for _, ids := range [][2]string{{"", "fn-a"}, {"exec-1", ""}} {
			auth.BindExecution(ids[0], ids[1], "t", "s", time.Now(), 0)()
			if auth.OwnsExecution(ids[0], ids[1]) {
				t.Fatal("invalid execution binding was stored")
			}
		}
	}
	absent.BindExecution("exec-1", "fn-a", "t", "s", time.Now(), 0)()
	if _, ok := absent.ExecutionDepth("exec-1", "fn-a"); ok {
		t.Fatal("nil tracker supplied depth")
	}
	if _, ok := absent.ExecutionStart("exec-1", "fn-a"); ok {
		t.Fatal("nil tracker supplied start")
	}
	if _, _, _, ok := absent.TraceContext("exec-1", "fn-a"); ok {
		t.Fatal("nil tracker supplied trace context")
	}
}
