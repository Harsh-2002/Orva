//go:build linux

package artifactfs

import (
	"errors"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func transferPair(t *testing.T) (*Store, string, string, *PreparedPaths) {
	t.Helper()
	s := newStore(t, testLimits())
	code := sealed(t, s, KindCode, ids.New())
	runtime := sealed(t, s, KindRuntime, "node")
	p, err := PreparePaths(s, code, s, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return s, code, runtime, p
}

func assertPairBusy(t *testing.T, s *Store, code, runtime string) {
	t.Helper()
	for _, id := range []string{code, runtime} {
		if err := s.Reclaim(id); !errors.Is(err, ErrBusy) {
			t.Fatalf("reclaim %s: %v, want busy", id, err)
		}
	}
}

func TestPreparedPathsTransferCopies(t *testing.T) {
	s, code, runtime, p := transferPair(t)
	copyBefore := *p
	wantCode, wantRuntime := p.Paths()
	codeID, runtimeID := p.Identities()
	if codeID.Generation != code || runtimeID.Generation != runtime ||
		codeID.Kind != KindCode || runtimeID.Kind != KindRuntime || codeID.Digest == "" || runtimeID.Digest == "" {
		t.Fatal("missing store identity")
	}
	_ = s.Retire(code)
	_ = s.Retire(runtime)
	if err := s.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	next, err := copyBefore.Transfer()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	for _, old := range []*PreparedPaths{p, &copyBefore} {
		old.Release()
		if c, r := old.Paths(); c != "" || r != "" {
			t.Fatal("consumed handle exposes paths")
		}
		if c, r := old.Identities(); c != (Identity{}) || r != (Identity{}) {
			t.Fatal("consumed handle exposes bindings")
		}
		if _, err := old.Transfer(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("second transfer: %v", err)
		}
	}
	if c, r := next.Paths(); c != wantCode || r != wantRuntime {
		t.Fatal("transfer changed paths")
	}
	assertPairBusy(t, s, code, runtime)
	nextCopy := *next
	final, err := nextCopy.Transfer()
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
	assertPairBusy(t, s, code, runtime)
	final.Release()
	final.Release()
	if c, r := final.Paths(); c != "" || r != "" {
		t.Fatal("released paths remain visible")
	}
	for _, id := range []string{code, runtime} {
		if err := s.Reclaim(id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreparedPathsConcurrentTransfer(t *testing.T) {
	s, code, runtime, p := transferPair(t)
	results := make(chan *PreparedPaths, 32)
	var wg sync.WaitGroup
	for range cap(results) {
		copy := *p
		wg.Go(func() {
			next, err := copy.Transfer()
			if err == nil {
				results <- next
			} else if !errors.Is(err, ErrInvalid) {
				t.Errorf("transfer: %v", err)
			}
			copy.Release()
			_, _ = copy.Paths()
			_, _ = copy.Identities()
		})
	}
	wg.Wait()
	close(results)
	_ = s.Retire(code)
	_ = s.Retire(runtime)
	count := 0
	for next := range results {
		count++
		assertPairBusy(t, s, code, runtime)
		next.Release()
	}
	if count != 1 {
		t.Fatalf("transfer winners = %d", count)
	}
}

func TestPreparedPathsReleaseTransferRace(t *testing.T) {
	s, code, runtime, initial := transferPair(t)
	initial.Release()
	for range 100 {
		p, err := PreparePaths(s, code, s, runtime)
		if err != nil {
			t.Fatal(err)
		}
		copy := *p
		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			<-start
			copy.Release()
			close(done)
		}()
		close(start)
		next, err := p.Transfer()
		<-done
		if err == nil {
			s.mu.Lock()
			refs := s.records[code].refs + s.records[runtime].refs
			s.mu.Unlock()
			if refs != 2 {
				t.Fatalf("partial/released transferred pair: %d refs", refs)
			}
			next.Release()
		} else if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		s.mu.Lock()
		refs, leases := s.records[code].refs+s.records[runtime].refs, s.leases
		s.mu.Unlock()
		if refs != 0 || leases != 0 {
			t.Fatalf("leaked pair: refs=%d leases=%d", refs, leases)
		}
	}
}

func TestPreparedPathsZeroTransfer(t *testing.T) {
	for _, p := range []*PreparedPaths{nil, {}} {
		p.Release()
		if c, r := p.Paths(); c != "" || r != "" {
			t.Fatal("zero paths")
		}
		if next, err := p.Transfer(); next != nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("zero transfer: %v, %v", next, err)
		}
	}
}
