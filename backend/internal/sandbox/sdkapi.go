package sandbox

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Protocol limits (see the SDK channel wire protocol). They are constants of the
// protocol, not tunables: an adapter and the host must agree on them.
const (
	MaxSDKFrame    = 4 << 20
	MaxSDKInflight = 32
	SDKProtoV1     = 1
)

// SDKError is a failure reply for one SDK operation.
type SDKError struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *SDKError) Error() string { return e.Code + ": " + e.Message }

// ExecInfo is the host's record of the dispatch currently running on a worker.
// Identity is never taken from the sandbox: every SDK frame is attributed to this.
type ExecInfo struct {
	ExecutionID string
	TraceID     string
	SpanID      string
	Depth       int
	StartedAt   time.Time
	Deadline    time.Time
	// Lease identifies one dispatch; the host issues it and revokes it when the
	// dispatch ends. Filled in by Dispatch if empty.
	Lease string
	// Ctx is cancelled when the dispatch ends or the worker is killed. Work the
	// host starts on behalf of an SDK call (an F2F callee) derives from it.
	Ctx context.Context
}

// SDKCaller is who an SDK frame is attributed to.
type SDKCaller struct {
	FunctionID string
	// Exec is nil at module scope or after the dispatch ended; operations that
	// need a live execution must refuse a nil Exec.
	Exec *ExecInfo

	workload *sdkWorkloadSource
}

// WorkloadIdentity is captured once at Spawn, independently of sandbox inputs.
type WorkloadIdentity struct {
	FunctionID       string
	WorkerID         string
	ArtifactRevision string
}

type WorkloadPhase string

const (
	WorkloadBootstrap  WorkloadPhase = "bootstrap"
	WorkloadExecution  WorkloadPhase = "execution"
	WorkloadUnbound    WorkloadPhase = "unbound"
	WorkloadNoChannel  WorkloadPhase = "no_channel"
	WorkloadStaleLease WorkloadPhase = "stale_lease"
	WorkloadInactive   WorkloadPhase = "inactive"
)

// WorkloadState is an observation, not admission authority. Future IAM integration
// must read it under its gate and serialize readiness/lease invalidation with that gate.
// Channel-less workers expose identity/readiness only, never bootstrap or execution.
type WorkloadState struct {
	Identity    WorkloadIdentity
	Ready       bool
	Alive       bool
	Phase       WorkloadPhase
	ExecutionID string
	Lease       string
}

type sdkWorkloadSource struct {
	worker   *Worker
	original *dispatchState
	lease    string
	ctx      context.Context
}

// WorkloadState observes current host state for this caller's ORIGINAL lease.
// Exported caller fields cannot change the capture; unattached callers return false.
func (c SDKCaller) WorkloadState() (WorkloadState, bool) {
	if c.workload == nil {
		return WorkloadState{}, false
	}
	s := c.workload
	return s.worker.observeWorkload(s.original, s.lease, s.ctx)
}

// SDKHandler executes one SDK operation. args is the raw "a" object of the frame.
// It returns the "r" result or an error reply. It may block (invoke does).
type SDKHandler func(ctx context.Context, caller SDKCaller, op string, args json.RawMessage) (any, *SDKError)

type execCtxKey struct{}

// WithExecution attaches the dispatch identity to ctx for Dispatch/DispatchEx.
func WithExecution(ctx context.Context, info ExecInfo) context.Context {
	return context.WithValue(ctx, execCtxKey{}, info)
}

// ExecutionFrom returns the identity attached by WithExecution.
func ExecutionFrom(ctx context.Context) (ExecInfo, bool) {
	info, ok := ctx.Value(execCtxKey{}).(ExecInfo)
	return info, ok
}

// NewExecInfo builds the identity a dispatch call site attaches to its ctx.
// Deadline is the ctx deadline when there is one.
func NewExecInfo(ctx context.Context, executionID, traceID, spanID string, depth int, startedAt time.Time) ExecInfo {
	info := ExecInfo{ExecutionID: executionID, TraceID: traceID, SpanID: spanID, Depth: depth, StartedAt: startedAt}
	if d, ok := ctx.Deadline(); ok {
		info.Deadline = d
	}
	return info
}

// SDKSessionState is handler-owned state for one SDK channel (one worker). The
// host calls LeaseRevoked when a dispatch's lease ends and Closed once when the
// channel closes; both must be quick and must not call back into the worker
// that owns the channel.
type SDKSessionState interface {
	LeaseRevoked(lease string)
	Closed()
}

// SDKSession is the per-channel session the sandbox creates when the channel
// starts serving. Handlers reach it through SDKSessionFrom(ctx).
type SDKSession struct {
	mu     sync.Mutex
	state  SDKSessionState
	closed bool
}

// State returns the handler's state, creating it with init on first use. After
// the channel closed it returns nil (the caller must refuse the operation).
func (s *SDKSession) State(init func() SDKSessionState) SDKSessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.state == nil {
		s.state = init()
	}
	return s.state
}

func (s *SDKSession) leaseRevoked(lease string) {
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	if st != nil {
		st.LeaseRevoked(lease)
	}
}

func (s *SDKSession) close() {
	s.mu.Lock()
	st := s.state
	s.closed = true
	s.mu.Unlock()
	if st != nil {
		st.Closed()
	}
}

type sessionCtxKey struct{}

// SDKSessionFrom returns the channel session attached to an SDK op's ctx.
func SDKSessionFrom(ctx context.Context) *SDKSession {
	s, _ := ctx.Value(sessionCtxKey{}).(*SDKSession)
	return s
}

// WithSDKSession attaches a session to ctx; the sandbox does this for every
// op it serves, so only tests and embedders of a Handler need it.
func WithSDKSession(ctx context.Context, s *SDKSession) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}
