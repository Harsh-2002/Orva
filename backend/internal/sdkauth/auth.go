// Package sdkauth tracks host-owned execution identity, trace context and F2F depth.
// It does not issue credentials; SDK access belongs to each worker's private channel.
package sdkauth

import (
	"sync"
	"time"
)

type Authenticator struct {
	active sync.Map // execution ID -> activeExecution while a worker is dispatching
}

type activeExecution struct {
	functionID string
	startedAt  time.Time
	traceID    string
	spanID     string
	depth      int
}

// BindExecution records a live execution: depth 0 for an external entrypoint,
// callerDepth+1 for an F2F callee. Only the host supplies these values.
func (a *Authenticator) BindExecution(executionID, functionID, traceID, spanID string, startedAt time.Time, depth int) func() {
	if a == nil || executionID == "" || functionID == "" {
		return func() {}
	}
	a.active.Store(executionID, activeExecution{
		functionID: functionID, startedAt: startedAt, traceID: traceID, spanID: spanID, depth: depth,
	})
	return func() { a.active.Delete(executionID) }
}

// ExecutionDepth returns the host-recorded call depth of a live execution
// owned by functionID.
func (a *Authenticator) ExecutionDepth(executionID, functionID string) (int, bool) {
	if a == nil {
		return 0, false
	}
	value, ok := a.active.Load(executionID)
	if !ok {
		return 0, false
	}
	execution := value.(activeExecution)
	if execution.functionID != functionID {
		return 0, false
	}
	return execution.depth, true
}

func (a *Authenticator) OwnsExecution(executionID, functionID string) bool {
	if a == nil {
		return false
	}
	execution, ok := a.active.Load(executionID)
	return ok && execution.(activeExecution).functionID == functionID
}

func (a *Authenticator) ExecutionStart(executionID, functionID string) (time.Time, bool) {
	if a == nil {
		return time.Time{}, false
	}
	value, ok := a.active.Load(executionID)
	if !ok {
		return time.Time{}, false
	}
	execution := value.(activeExecution)
	return execution.startedAt, execution.functionID == functionID
}

func (a *Authenticator) TraceContext(executionID, functionID string) (traceID, spanID string, startedAt time.Time, ok bool) {
	if a == nil {
		return "", "", time.Time{}, false
	}
	value, found := a.active.Load(executionID)
	if !found {
		return "", "", time.Time{}, false
	}
	execution := value.(activeExecution)
	if execution.functionID != functionID {
		return "", "", time.Time{}, false
	}
	return execution.traceID, execution.spanID, execution.startedAt, true
}

func New() *Authenticator { return &Authenticator{} }
