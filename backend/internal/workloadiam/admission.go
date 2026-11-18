package workloadiam

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrDenied   = errors.New("IAM denied")
	ErrConflict = errors.New("IAM revision conflict")
	ErrCapacity = errors.New("IAM registration capacity")
	ErrInvalid  = errors.New("invalid IAM authority input")
)

// MaxHandoffBytes matches the SDK's decoded stream-chunk limit, before base64.
const MaxHandoffBytes = 256 << 10

// Authority is an inert single-host coordinator; its zero value denies.
// Lock order is build/mutation (if needed), gate, registrations, then DB.
// Callbacks must be synchronous, bounded, and never reenter Authority/Admission,
// acquire build/registry locks, spawn work, or wait on an IAM-acquiring writer.
// No callback may activate source. Host state must be fresh and host-owned;
// callers must serialize its invalidation with the same publication boundary.
type Authority struct {
	gate               sync.RWMutex
	policies           map[string]*Evaluator
	retired            map[string]bool
	registrationMu     sync.Mutex
	registrations      map[*Admission]struct{}
	asyncRegistrations map[*AsyncAdmission]struct{}
	maxRegistered      int
}

// NewAuthority copies and validates compiled snapshots. maxRegistered bounds
// waiting plus admitted calls; cancelled calls retain their slot until Finish.
func NewAuthority(maxRegistered int, initial []*Evaluator) (*Authority, error) {
	if maxRegistered < 0 {
		return nil, ErrInvalid
	}
	a := &Authority{
		policies:      make(map[string]*Evaluator, len(initial)),
		registrations: make(map[*Admission]struct{}), maxRegistered: maxRegistered,
	}
	for _, e := range initial {
		frozen, err := copyEvaluator(e)
		if err != nil {
			return nil, err
		}
		if _, exists := a.policies[frozen.metadata.OwnerID]; exists {
			return nil, ErrInvalid
		}
		a.policies[frozen.metadata.OwnerID] = frozen
	}
	return a, nil
}

func copyEvaluator(e *Evaluator) (*Evaluator, error) {
	if e == nil || !e.valid {
		return nil, ErrInvalid
	}
	frozen, err := Compile(e.metadata, e.policy)
	if err != nil {
		return nil, ErrInvalid
	}
	return frozen, nil
}

// Do evaluates a short operation and retains the read gate through its effect.
// Calls/streams must use BeginCall instead. Jobs/cron also check current inbound
// acceptance; this method does not authorize their later execution.
func (a *Authority) Do(c Context, r Request, effect func() error) error {
	return a.DoObserved(func() Context { return c }, r, effect)
}

// DoObserved snapshots host state once under the read gate, before evaluation.
// Observers must be synchronous, bounded atomic host-state reads: no DB/I/O,
// worker waits, earlier locks or coordinator reentry. Lifecycle invalidation
// still needs a future host transition hook sharing the exclusive gate.
func (a *Authority) DoObserved(observe func() Context, r Request, effect func() error) error {
	if a == nil || observe == nil || effect == nil {
		return ErrDenied
	}
	switch r.Operation {
	case OpInvoke, OpStreamOpen, OpStreamNext:
		return ErrDenied
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	c := observe()
	if !a.allows(c, r) {
		return ErrDenied
	}
	return effect()
}

// SpanDecision is immutable emission provenance, not authority to emit again.
// Its zero value explicitly means no IAM decision (legacy telemetry).
type SpanDecision struct {
	ownerID  string
	revision uint64
}

func (d SpanDecision) OwnerID() string  { return d.ownerID }
func (d SpanDecision) Revision() uint64 { return d.revision }

// EmitSpanObserved binds provenance from the deciding evaluator under the same
// gate as admission and handoff; the callback follows DoObserved's contract.
func (a *Authority) EmitSpanObserved(observe func() Context, r Request, effect func(SpanDecision) error) error {
	if a == nil || observe == nil || effect == nil || r.Operation != OpSpansEmit {
		return ErrDenied
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	c := observe()
	if !a.allows(c, r) {
		return ErrDenied
	}
	m := a.policies[c.Principal.FunctionID].metadata
	return effect(SpanDecision{ownerID: m.OwnerID, revision: m.Revision})
}

// allows requires the gate, and never loads from storage on a cache miss.
func (a *Authority) allows(c Context, r Request) bool {
	caller := a.policies[c.Principal.FunctionID]
	if !caller.Allows(c, r) {
		return false
	}
	switch r.Operation {
	case OpInvoke, OpStreamOpen, OpStreamNext, OpJobsEnqueue:
		return AllowsEdge(caller, a.policies[r.Resource.Function.ID], c, r)
	case OpCronsUpsert:
		return AllowsEdge(caller, a.policies[r.Resource.OwnCron.TargetID], c, r)
	default:
		return true
	}
}

// TargetState describes an acquired worker, never SDK-supplied claims.
type TargetState struct {
	Active    bool
	Principal Principal
}

// Admission owns one cancellable call. Always defer Finish after BeginCall,
// including on parent cancellation and acquisition/dispatch failure. Parent
// cancellation propagates without a per-wait goroutine but does not free a slot.
// The stream owner must observe Context cancellation and close its handle.
// Pool acquisition, Dispatch, NextFrame, protocol writes and teardown run outside
// these methods. Dispatch must refuse an already-cancelled Context before I/O.
type Admission struct {
	authority *Authority
	ctx       context.Context
	cancel    context.CancelFunc
	caller    Context
	request   Request
	targetID  string
	// The following fields are protected by authority.registrationMu.
	target   Principal
	admitted bool
	revoked  bool
}

// BeginCall registers a waiting edge without retaining a gate during pool wait.
func (a *Authority) BeginCall(parent context.Context, c Context, r Request) (*Admission, error) {
	return a.BeginCallObserved(parent, func() Context { return c }, r)
}

// BeginCallObserved copies one caller snapshot under the read gate, before
// taking the registration lock. The observer follows DoObserved's contract.
func (a *Authority) BeginCallObserved(parent context.Context, observe func() Context, r Request) (*Admission, error) {
	if a == nil || observe == nil || parent == nil || parent.Err() != nil || (r.Operation != OpInvoke && r.Operation != OpStreamOpen) {
		return nil, ErrDenied
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	c := observe()
	if !a.allows(c, r) {
		return nil, ErrDenied
	}
	// A validated call has only this resource; never retain the caller's pointer.
	r.Resource = Resource{Function: &FunctionResource{ID: r.Resource.Function.ID}}
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	if a.maxRegistered <= 0 || len(a.registrations)+len(a.asyncRegistrations) >= a.maxRegistered {
		return nil, ErrCapacity
	}
	ctx, cancel := context.WithCancel(parent)
	call := &Admission{authority: a, ctx: ctx, cancel: cancel, caller: c, request: r, targetID: r.Resource.Function.ID}
	a.registrations[call] = struct{}{}
	return call, nil
}

// Context is the owned child context for acquisition and nested dispatch.
// A zero Admission returns an already-cancelled context.
func (call *Admission) Context() context.Context {
	if call != nil && call.ctx != nil {
		return call.ctx
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func (a *Authority) validTarget(id string, target TargetState) bool {
	e := a.policies[id]
	return target.Active && e != nil && e.valid && target.Principal.FunctionID == id &&
		validHostID(target.Principal.WorkerID) && target.Principal.ArtifactRevision == e.metadata.ApprovedArtifact
}

func (call *Admission) live(c Context, target TargetState) bool {
	a := call.authority
	_, registered := a.registrations[call]
	return registered && !call.revoked && call.ctx.Err() == nil && c == call.caller &&
		a.allows(c, call.request) && a.validTarget(call.targetID, target)
}

// Admit is the waiting-to-admitted linearization point. Revalidate the original
// caller/lease and the acquired target's approved artifact using fresh host state.
func (call *Admission) Admit(c Context, target TargetState) error {
	return call.AdmitObserved(func() (Context, TargetState) { return c, target })
}

// AdmitObserved copies fresh caller and target state once under the read gate,
// before taking the registration lock; original caller/lease identity must match.
// The observer follows DoObserved's contract and is not retained.
func (call *Admission) AdmitObserved(observe func() (Context, TargetState)) error {
	if call == nil || call.authority == nil || observe == nil {
		return ErrDenied
	}
	a := call.authority
	a.gate.RLock()
	defer a.gate.RUnlock()
	c, target := observe()
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	if call.admitted || !call.live(c, target) {
		return ErrDenied
	}
	call.target, call.admitted = target.Principal, true
	return nil
}

// HandoffFrame reauthorizes a bounded frame for an admitted stream. transfer must
// copy/transfer to pre-reserved storage without blocking; it must not perform I/O.
// frame is decoded chunk data; serialize/base64-encode only after this handoff.
// Frame acquisition and protocol delivery happen outside the gate. A transfer
// completed before revocation may physically reach its caller afterward.
func (call *Admission) HandoffFrame(c Context, target TargetState, frame []byte, transfer func([]byte) error) error {
	return call.HandoffFrameObserved(func() (Context, TargetState) { return c, target }, frame, transfer)
}

// HandoffFrameObserved snapshots caller and target once under the read gate,
// before taking the registration lock, then applies HandoffFrame's checks and
// transfer contract. The observer follows DoObserved's contract and is not retained.
func (call *Admission) HandoffFrameObserved(observe func() (Context, TargetState), frame []byte, transfer func([]byte) error) error {
	if call == nil || call.authority == nil || observe == nil || transfer == nil || len(frame) > MaxHandoffBytes {
		return ErrDenied
	}
	a := call.authority
	a.gate.RLock()
	defer a.gate.RUnlock()
	c, target := observe()
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	if !call.admitted || call.request.Operation != OpStreamOpen || target.Principal != call.target || !call.live(c, target) {
		return ErrDenied
	}
	return transfer(frame)
}

// Finish is idempotent, permits revoked cleanup, and cancels only after unlocking.
func (call *Admission) Finish() {
	if call == nil || call.authority == nil {
		return
	}
	a := call.authority
	a.registrationMu.Lock()
	call.revoked = true
	delete(a.registrations, call)
	a.registrationMu.Unlock()
	call.cancel()
}

// Publish commits storage synchronously before publishing a copied candidate.
// expected=0 permits a previously unavailable owner; otherwise revision must be
// exactly expected+1. Every edge touching the owner is conservatively revoked,
// even if still allowed. Failed persistence/conflicts do not cancel any calls.
// Cancellation (including context callbacks) runs after both locks are released.
func (a *Authority) Publish(expected uint64, candidate *Evaluator, persist func() error) error {
	if a == nil || persist == nil {
		return ErrInvalid
	}
	frozen, err := copyEvaluator(candidate)
	if err != nil {
		return err
	}
	if expected == ^uint64(0) || frozen.metadata.Revision != expected+1 {
		return ErrConflict
	}
	a.gate.Lock()
	if a.retired[frozen.metadata.OwnerID] {
		a.gate.Unlock()
		return ErrDenied
	}
	current := a.policies[frozen.metadata.OwnerID].Metadata().Revision
	if a.policies == nil || current != expected {
		a.gate.Unlock()
		return ErrConflict
	}
	if err := persist(); err != nil {
		a.gate.Unlock()
		return err
	}
	a.policies[frozen.metadata.OwnerID] = frozen
	a.registrationMu.Lock()
	var cancelled []context.CancelFunc
	for call := range a.registrations {
		if call.caller.Principal.FunctionID == frozen.metadata.OwnerID || call.targetID == frozen.metadata.OwnerID {
			call.revoked = true
			cancelled = append(cancelled, call.cancel)
		}
	}
	for call := range a.asyncRegistrations {
		if call.origin.CreatorID == frozen.metadata.OwnerID || call.origin.TargetID == frozen.metadata.OwnerID {
			call.revoked = true
			cancelled = append(cancelled, call.cancel)
		}
	}
	a.registrationMu.Unlock()
	a.gate.Unlock()
	for _, cancel := range cancelled {
		cancel()
	}
	return nil
}
