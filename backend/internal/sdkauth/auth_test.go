package sdkauth

import (
	"testing"
	"time"
)

func TestScopedCredential(t *testing.T) {
	a := New([]byte("01234567890123456789012345678901"))
	token := a.Mint("function-a")
	got, err := a.Verify(token)
	if err != nil || got != "function-a" {
		t.Fatalf("verify = %q, %v", got, err)
	}

	t.Run("tampered", func(t *testing.T) {
		if _, err := a.Verify(token + "x"); err == nil {
			t.Fatal("tampered credential was accepted")
		}
	})
	t.Run("stale after restart", func(t *testing.T) {
		other := New([]byte("abcdefghijklmnopqrstuvwxyz123456"))
		if _, err := other.Verify(token); err == nil {
			t.Fatal("credential signed by a prior process was accepted")
		}
	})
}

func TestExecutionOwnershipIsScopedAndRemoved(t *testing.T) {
	auth := New([]byte("secret"))
	started := time.Now().Add(-time.Second)
	release := auth.BindExecution("exec-1", "fn-a", "trace-a", "span-a", started)
	if !auth.OwnsExecution("exec-1", "fn-a") || auth.OwnsExecution("exec-1", "fn-b") {
		t.Fatal("execution ownership was not function scoped")
	}
	if got, ok := auth.ExecutionStart("exec-1", "fn-a"); !ok || !got.Equal(started) {
		t.Fatalf("ExecutionStart = %v, %v", got, ok)
	}
	traceID, spanID, _, ok := auth.TraceContext("exec-1", "fn-a")
	if !ok || traceID != "trace-a" || spanID != "span-a" {
		t.Fatalf("TraceContext = %q, %q, %v", traceID, spanID, ok)
	}
	release()
	if auth.OwnsExecution("exec-1", "fn-a") {
		t.Fatal("execution remained active after release")
	}
}
