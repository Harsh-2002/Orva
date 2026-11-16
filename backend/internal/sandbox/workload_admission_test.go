package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func iamObservation(c SDKCaller) workloadiam.Context {
	s, ok := c.WorkloadState()
	if !ok || !s.Alive {
		return workloadiam.Context{}
	}
	out := workloadiam.Context{
		Principal: workloadiam.Principal{FunctionID: s.Identity.FunctionID, WorkerID: s.Identity.WorkerID, ArtifactRevision: s.Identity.ArtifactRevision},
		Ready:     s.Ready,
	}
	switch s.Phase {
	case WorkloadBootstrap:
		out.Phase = workloadiam.Bootstrap
	case WorkloadExecution:
		out.Phase = workloadiam.Execution
		out.Lease = workloadiam.Lease{ID: s.Lease, WorkerID: s.Identity.WorkerID, ExecutionID: s.ExecutionID, Active: true}
	default:
		return workloadiam.Context{}
	}
	return out
}

func TestObservedAdmissionSharesWorkerLifecycleGate(t *testing.T) {
	owner, workerID := ids.New(), ids.New()
	key := "config"
	policy := workloadiam.Policy{SchemaVersion: workloadiam.SchemaVersion, Inbound: []workloadiam.Acceptance{}, Allow: []workloadiam.Statement{
		{Actions: []workloadiam.Action{workloadiam.KVGet}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}, Context: workloadiam.Bootstrap},
		{Actions: []workloadiam.Action{workloadiam.KVGet}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}, Context: workloadiam.Execution},
	}}
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: owner, Revision: 1, ApprovedArtifact: "reviewed"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := workloadiam.NewAuthority(1, []*workloadiam.Evaluator{e})
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{
		sdkEnabled: true,
		identity:   &WorkloadIdentity{FunctionID: owner, WorkerID: workerID, ArtifactRevision: "reviewed"},
		frames:     make(chan frameRead, 1),
		workloadTransition: func(change func()) {
			if err := authority.ChangeHostState(change); err != nil {
				panic(err)
			}
		},
	}
	defer w.revokeLease()
	req := workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: owner, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}
	effects := 0
	decide := func(c SDKCaller) error {
		return authority.DoObserved(func() workloadiam.Context { return iamObservation(c) }, req, func() error { effects++; return nil })
	}
	module := workloadCaller(w, nil, "")
	if err := decide(module); err != nil {
		t.Fatalf("explicit bootstrap read denied: %v", err)
	}
	w.frames <- frameRead{payload: []byte(`{"type":"ready"}`)}
	if err := w.AwaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := decide(module); !errors.Is(err, workloadiam.ErrDenied) {
		t.Fatalf("module caller retained bootstrap authority after readiness: %v", err)
	}
	w.beginLease(WithExecution(context.Background(), ExecInfo{ExecutionID: ids.New(), Lease: "first-lease"}))
	current := workloadCaller(w, w.disp.Load(), "first-lease")
	if err := decide(current); err != nil {
		t.Fatalf("current execution denied: %v", err)
	}
	w.revokeLease()
	if err := decide(current); !errors.Is(err, workloadiam.ErrDenied) {
		t.Fatalf("revoked lease retained authority: %v", err)
	}
	w.beginLease(WithExecution(context.Background(), ExecInfo{ExecutionID: ids.New(), Lease: "first-lease"}))
	if err := decide(current); !errors.Is(err, workloadiam.ErrDenied) {
		t.Fatalf("old caller upgraded to replacement execution: %v", err)
	}
	fresh := workloadCaller(w, w.disp.Load(), "first-lease")
	if err := decide(fresh); err != nil {
		t.Fatalf("replacement execution denied: %v", err)
	}
	w.markDead()
	if err := decide(fresh); !errors.Is(err, workloadiam.ErrDenied) {
		t.Fatalf("dead worker retained authority: %v", err)
	}
	if effects != 3 {
		t.Fatalf("unauthorized effects ran: got %d, want 3", effects)
	}
}
