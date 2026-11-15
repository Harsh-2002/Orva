// Package sdkhost executes SDK channel operations on behalf of a sandboxed
// function. Identity is always sandbox.SDKCaller.FunctionID: no argument can
// name another function, so a function can only ever reach its own namespace.
package sdkhost

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// Metrics receives one observation per handled op. status is the HTTP-like
// status of the reply (200 on success). Implementations must be cheap and
// concurrency-safe; the host never blocks on them.
type Metrics interface {
	ObserveSDKOp(op string, d time.Duration, status int)
}

// Option configures a Host.
type Option func(*Host)

// WithMetrics supplies an external metrics sink in addition to the built-in
// counters returned by Host.Stats.
func WithMetrics(m Metrics) Option { return func(h *Host) { h.metrics = m } }

// OpStats is a cumulative per-op counter set.
type OpStats struct {
	Op        string
	Count     uint64
	Errors    uint64
	LatencyNS uint64
}

var opNames = [...]string{
	"kv.get", "kv.put", "kv.delete", "kv.list", "kv.batch", "kv.incr", "kv.cas",
	"unimplemented", "unknown",
}

const (
	idxUnimplemented = len(opNames) - 2
	idxUnknown       = len(opNames) - 1
)

type opCounter struct{ count, errs, ns atomic.Uint64 }

// Host is the server-side SDK operation dispatcher.
type Host struct {
	DB      *database.Database
	metrics Metrics
	ops     [len(opNames)]opCounter
}

// New builds a Host over db.
func New(db *database.Database, opts ...Option) *Host {
	h := &Host{DB: db}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Stats returns the built-in per-op counters.
func (h *Host) Stats() []OpStats {
	out := make([]OpStats, len(opNames))
	for i, n := range opNames {
		out[i] = OpStats{Op: n, Count: h.ops[i].count.Load(), Errors: h.ops[i].errs.Load(), LatencyNS: h.ops[i].ns.Load()}
	}
	return out
}

func opIndex(op string) int {
	for i := 0; i < idxUnimplemented; i++ {
		if opNames[i] == op {
			return i
		}
	}
	if isUnimplemented(op) {
		return idxUnimplemented
	}
	return idxUnknown
}

func isUnimplemented(op string) bool {
	switch op {
	case "invoke", "invoke.stream.open", "invoke.stream.next", "invoke.stream.close",
		"jobs.enqueue", "crons.upsert", "spans.emit":
		return true
	}
	return false
}

// Handle implements sandbox.SDKHandler. It never panics: a panic inside an op
// is converted to a 500 reply so one bad frame cannot take the daemon down.
func (h *Host) Handle(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (res any, serr *sandbox.SDKError) {
	started := time.Now()
	idx := opIndex(op)
	defer func() {
		if r := recover(); r != nil {
			res, serr = nil, &sandbox.SDKError{Status: 500, Code: "INTERNAL", Message: "sdk operation failed"}
		}
		d := time.Since(started)
		c := &h.ops[idx]
		c.count.Add(1)
		c.ns.Add(uint64(d))
		status := 200
		if serr != nil {
			c.errs.Add(1)
			status = serr.Status
		}
		if h.metrics != nil {
			// Unknown ops share one label so a hostile client cannot grow cardinality.
			h.metrics.ObserveSDKOp(opNames[idx], d, status)
		}
	}()

	if caller.FunctionID == "" {
		return nil, &sandbox.SDKError{Status: 403, Code: "SDK_SCOPE_VIOLATION", Message: "no function identity"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	switch op {
	case "kv.get":
		return h.kvGet(ctx, caller.FunctionID, args)
	case "kv.put":
		return h.kvPut(ctx, caller.FunctionID, args)
	case "kv.delete":
		return h.kvDelete(ctx, caller.FunctionID, args)
	case "kv.list":
		return h.kvList(ctx, caller.FunctionID, args)
	case "kv.batch":
		return h.kvBatch(ctx, caller.FunctionID, args)
	case "kv.incr":
		return h.kvIncr(ctx, caller.FunctionID, args)
	case "kv.cas":
		return h.kvCAS(ctx, caller.FunctionID, args)
	}
	if isUnimplemented(op) {
		return nil, &sandbox.SDKError{Status: 501, Code: "NOT_IMPLEMENTED", Message: "operation not implemented on the SDK channel yet"}
	}
	return nil, &sandbox.SDKError{Status: 400, Code: "UNKNOWN_OP", Message: "unknown operation"}
}
