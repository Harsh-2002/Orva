package pool

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The SDK credential is bound to one worker process: Mint hands back a release
// that expires it, and the pool must call that release on every path where the
// worker it was minted for does not end up running.
//
// This is the half the sdkauth tests cannot cover. sdkauth proves a released
// credential stops verifying; this proves the pool actually releases. Get the
// wiring wrong and the credential silently reverts to its old lifetime -- valid
// until the process restarts -- with nothing failing anywhere.
//
// The reaper path (a worker that spawned and later died) is covered by the
// gated sandbox E2E, since it needs a real nsjail process to reap.
func TestSpawnFailureReleasesTheSDKCredential(t *testing.T) {
	m, reg := egressTestManager(t)
	tmpl := fakeSandboxTemplate(t)

	var mu sync.Mutex
	minted, released := 0, 0
	tmpl.SDKToken = func(string) (string, func()) {
		mu.Lock()
		minted++
		mu.Unlock()
		return "orva-test-token", func() {
			mu.Lock()
			released++
			mu.Unlock()
		}
	}

	t.Run("egress policy unavailable", func(t *testing.T) {
		// Fails closed BEFORE Spawn, and it is the path most likely to be
		// missed: it returns early from the middle of the closure.
		tmpl.EgressPolicy = func() (string, string, error) { return "", "", errTestNoPolicy }
		m.tmpl = tmpl
		fn := registerFn(t, reg, "cred-egress", "egress")

		p, err := m.getOrCreatePool(fn.ID)
		if err != nil {
			t.Fatalf("getOrCreatePool: %v", err)
		}
		if _, err := p.spawnFn(context.Background()); err == nil {
			t.Fatal("spawn succeeded without an egress policy")
		}
		mu.Lock()
		defer mu.Unlock()
		if minted == 0 {
			t.Fatal("no credential was minted, so this asserts nothing")
		}
		if released != minted {
			t.Errorf("minted %d credentials, released %d: a credential outlived a worker that never started",
				minted, released)
		}
	})

	t.Run("worker death releases it", func(t *testing.T) {
		mu.Lock()
		minted, released = 0, 0
		mu.Unlock()
		// network_mode=none needs no egress policy, so this reaches Spawn.
		tmpl.EgressPolicy = nil
		m.tmpl = tmpl
		fn := registerFn(t, reg, "cred-none", "none")

		p, err := m.getOrCreatePool(fn.ID)
		if err != nil {
			t.Fatalf("getOrCreatePool: %v", err)
		}
		w, err := p.spawnFn(context.Background())
		if err != nil {
			// Spawn failed instead: the release must still have run, and the
			// reaper path is then covered by the gated sandbox E2E.
			mu.Lock()
			defer mu.Unlock()
			if released != minted {
				t.Errorf("a failed spawn left %d of %d credentials valid", minted-released, minted)
			}
			return
		}

		mu.Lock()
		mintedNow := minted
		releasedNow := released
		mu.Unlock()
		if mintedNow == 0 {
			t.Fatal("no credential was minted, so this asserts nothing")
		}
		if releasedNow != 0 {
			t.Fatal("the credential was released while its worker was still alive: live SDK calls would 401")
		}

		// Kill it and let the reaper run. Kill returns as soon as the signal
		// is delivered, so poll for the release rather than assuming it is
		// synchronous.
		if err := w.Kill(); err != nil {
			t.Fatalf("kill: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			done := released == mintedNow
			mu.Unlock()
			if done {
				return
			}
			if time.Now().After(deadline) {
				mu.Lock()
				got := released
				mu.Unlock()
				t.Fatalf("released %d of %d credentials 5s after the worker died: the credential outlives its worker, which is the bug this change exists to fix",
					got, mintedNow)
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}
