package workloadiam

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPublishBatchAtomicFailure(t *testing.T) {
	for _, failure := range []string{"cas", "persist"} {
		t.Run(failure, func(t *testing.T) {
			a, caller, target := authorityFixture(t, 2)
			call := begin(t, a, OpInvoke)
			batch := []Publication{{1, nextPolicy(t, caller, true)}, {1, nextPolicy(t, target, true)}}
			want := errors.New("commit failed")
			persisted := 0
			if failure == "cas" {
				batch[1].Expected = 2
				batch[1].Candidate.metadata.Revision = 3
				want = ErrConflict
			}
			err := a.PublishBatch(batch, func() error { persisted++; return want })
			if !errors.Is(err, want) || (failure == "cas" && persisted != 0) {
				t.Fatalf("error=%v persisted=%d", err, persisted)
			}
			if a.policies[owner].Metadata().Revision != 1 || a.policies[other].Metadata().Revision != 1 ||
				call.Context().Err() != nil || call.Admit(executionContext(), targetWorker()) != nil {
				t.Fatal("failed batch changed authority or cancelled work")
			}
		})
	}
}

func TestPublishBatchCopiesBeforeCommitAndCancelsOutsideGate(t *testing.T) {
	a, caller, target := authorityFixture(t, 2)
	call := begin(t, a, OpInvoke)
	done := make(chan struct{})
	stop := context.AfterFunc(call.Context(), func() {
		_ = a.ChangeHostState(func() {})
		call.Finish()
		close(done)
	})
	defer stop()
	batch := []Publication{{1, nextPolicy(t, caller, false)}, {1, nextPolicy(t, target, false)}}
	commits := 0
	err := a.PublishBatch(batch, func() error {
		commits++
		// The publication must no longer consult caller-owned candidates or slices.
		batch[0].Candidate.policy.Allow[0].Resource.Function.ID = owner
		batch[0].Candidate.metadata.OwnerID = other
		batch[1].Expected = 99
		return nil
	})
	if err != nil || commits != 1 || a.policies[owner].Metadata().Revision != 2 ||
		a.policies[other].Metadata().Revision != 2 {
		t.Fatalf("batch not atomically installed: %v, commits=%d", err, commits)
	}
	assertCancelled(t, call.Context())
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation callback cannot reenter authority")
	}
	fresh, err := a.BeginCall(context.Background(), executionContext(), request(OpInvoke, function(other)))
	if err != nil {
		t.Fatal("caller-owned mutation changed frozen grants", err)
	}
	fresh.Finish()
}

func TestPublishBatchBoundsAndPanicUnlock(t *testing.T) {
	if err := (&Authority{}).PublishBatch(nil, func() error { t.Fatal("zero authority persisted"); return nil }); err != ErrInvalid {
		t.Fatal(err)
	}
	a, caller, _ := authorityFixture(t, 2)
	one := Publication{1, nextPolicy(t, caller, true)}
	for _, batch := range [][]Publication{{one, one}, {{1, nil}}, make([]Publication, MaxPublicationOwners+1)} {
		if err := a.PublishBatch(batch, func() error { t.Fatal("invalid batch persisted"); return nil }); err != ErrInvalid {
			t.Fatal(err)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("missing persist panic")
			}
		}()
		_ = a.PublishBatch([]Publication{one}, func() error { panic("test") })
	}()
	if err := a.PublishBatch([]Publication{one}, func() error { return nil }); err != nil {
		t.Fatal("panic retained gate", err)
	}
	var batch []Publication
	for i := 0; i < MaxPublicationOwners; i++ {
		m := metadata(fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1))
		e, err := Compile(m, emptyPolicy())
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, Publication{0, e})
	}
	fresh := mustAuthority(t, 1, nil)
	if err := fresh.PublishBatch(batch, func() error { return nil }); err != nil {
		t.Fatal("exact cap denied", err)
	}
}

func TestPublishBatchConcurrentCAS(t *testing.T) {
	a, caller, target := authorityFixture(t, 2)
	batch := []Publication{{1, nextPolicy(t, caller, true)}, {1, nextPolicy(t, target, true)}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- a.PublishBatch(batch, func() error { return nil }) })
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		switch err {
		case nil:
			ok++
		case ErrConflict:
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", ok, conflicts)
	}
}

func TestPublishBatchDeferredMarksBeforeSynchronousReentrantSignal(t *testing.T) {
	a, caller, target := authorityFixture(t, 2)
	call := begin(t, a, OpInvoke)
	originalCancel := call.cancel
	var once sync.Once
	// This hook is deliberately synchronous, unlike context.AfterFunc. Holding
	// the IAM gate during signalling would deadlock the publication itself.
	call.cancel = func() {
		once.Do(func() {
			if err := a.ChangeHostState(func() {}); err != nil {
				t.Error(err)
			}
			originalCancel()
		})
	}
	signal, err := a.PublishBatchDeferred([]Publication{
		{1, nextPolicy(t, caller, false)}, {1, nextPolicy(t, target, false)},
	}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if call.Context().Err() != nil || call.Admit(executionContext(), targetWorker()) != ErrDenied {
		t.Fatal("marking and signalling were not distinct, or revoked waiter admitted")
	}
	done := make(chan struct{})
	go func() { signal(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("synchronous cancellation callback reentered a held IAM gate")
	}
	assertCancelled(t, call.Context())
}
