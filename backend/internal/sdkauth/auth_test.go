package sdkauth

import (
	"testing"
	"time"
)

func TestScopedCredential(t *testing.T) {
	a := New([]byte("01234567890123456789012345678901"))
	token, release := a.Mint("function-a")
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
	t.Run("dead once its worker is released", func(t *testing.T) {
		release()
		if _, err := a.Verify(token); err == nil {
			t.Fatal("credential outlived the worker it was minted for")
		}
	})
}

// The credential names one worker process, not just a function. That is what
// makes redeploy a real remediation: it used to be a pure function of
// (signing key, function id), so a copy taken by a compromised dependency was
// re-minted byte-identical by the deploy meant to remove it, and only a full
// server restart cleared it.
func TestEachSpawnGetsADistinctCredential(t *testing.T) {
	a := New([]byte("01234567890123456789012345678901"))

	first, releaseFirst := a.Mint("fn-a")
	second, releaseSecond := a.Mint("fn-a")
	defer releaseSecond()

	if first == second {
		t.Fatal("two spawns of the same function got identical credentials: a stolen one survives redeploy")
	}
	for _, tok := range []string{first, second} {
		if got, err := a.Verify(tok); err != nil || got != "fn-a" {
			t.Fatalf("Verify = %q, %v; both live credentials must work", got, err)
		}
	}

	// Retiring one worker must not touch the other's -- deploys drain busy
	// workers, so the two overlap.
	releaseFirst()
	if _, err := a.Verify(first); err == nil {
		t.Error("the retired worker's credential still verified")
	}
	if got, err := a.Verify(second); err != nil || got != "fn-a" {
		t.Errorf("the surviving worker's credential was invalidated too: %q, %v", got, err)
	}
}

// Releasing twice, or releasing a credential that was never issued, must be
// harmless: the pool calls release on spawn error paths as well as from the
// reaper, and both can run for the same mint.
func TestReleaseIsIdempotent(t *testing.T) {
	a := New([]byte("01234567890123456789012345678901"))
	token, release := a.Mint("fn-a")
	release()
	release()
	if _, err := a.Verify(token); err == nil {
		t.Fatal("credential verified after release")
	}
}

// A nil authenticator and an empty function id both yield an unusable
// credential and a release that does nothing, so callers need no nil checks.
func TestMintDegradesSafely(t *testing.T) {
	var nilAuth *Authenticator
	if tok, rel := nilAuth.Mint("fn-a"); tok != "" || rel == nil {
		t.Error("nil authenticator did not degrade safely")
	} else {
		rel()
	}
	a := New([]byte("01234567890123456789012345678901"))
	if tok, rel := a.Mint(""); tok != "" || rel == nil {
		t.Error("empty function id did not degrade safely")
	} else {
		rel()
	}
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
