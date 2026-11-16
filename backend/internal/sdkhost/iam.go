package sdkhost

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func iamDenied() *sandbox.SDKError {
	return fail(403, "IAM_DENIED", "SDK operation denied")
}

// This observer runs only inside admission; public SDKCaller fields never
// supply worker identity, artifact, phase or execution authority.
func iamContext(ctx context.Context, caller sandbox.SDKCaller) workloadiam.Context {
	s, ok := caller.WorkloadState()
	if ctx.Err() != nil || !ok || !s.Alive || caller.FunctionID != s.Identity.FunctionID {
		return workloadiam.Context{}
	}
	c := workloadiam.Context{
		Principal: workloadiam.Principal{FunctionID: s.Identity.FunctionID, WorkerID: s.Identity.WorkerID, ArtifactRevision: s.Identity.ArtifactRevision},
		Ready:     s.Ready,
	}
	switch s.Phase {
	case sandbox.WorkloadBootstrap:
		if s.Ready || s.Lease != "" || s.ExecutionID != "" {
			return workloadiam.Context{}
		}
		c.Phase = workloadiam.Bootstrap
	case sandbox.WorkloadExecution:
		c.Phase = workloadiam.Execution
		c.Lease = workloadiam.Lease{ID: s.Lease, WorkerID: s.Identity.WorkerID, ExecutionID: s.ExecutionID, Active: true}
	default:
		return workloadiam.Context{}
	}
	return c
}

func (h *Host) handleIAM(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (res any, serr *sandbox.SDKError) {
	// Closing an already-owned stream is cleanup, never a fresh grant.
	if op == "invoke.stream.close" {
		return h.streamClose(ctx, caller, args)
	}
	if op == "invoke" {
		return h.invokeIAM(ctx, caller, args)
	}
	switch op {
	case "kv.get", "kv.put", "kv.delete", "kv.list", "kv.incr", "kv.cas", "kv.batch":
	default:
		return nil, iamDenied()
	}
	if h.iam == nil {
		return nil, iamDenied()
	}
	requests, e := iamKVRequests(caller.FunctionID, op, args)
	if e != nil {
		return nil, e
	}
	observe := func() workloadiam.Context { return iamContext(ctx, caller) }
	effect := func() error {
		if op == "kv.list" {
			res, serr = h.kvListMode(ctx, caller.FunctionID, args, true)
		} else {
			res, serr = h.dispatch(ctx, caller, op, args)
		}
		return nil
	}
	var err error
	if op == "kv.batch" {
		err = h.iam.DoBatchObserved(observe, requests, effect)
	} else {
		err = h.iam.DoObserved(observe, requests[0], effect)
	}
	if err != nil {
		return nil, iamDenied()
	}
	return res, serr
}

// Reuse wire shapes and ceilings; existing handlers retain value/TTL validation.
// No reads or writes occur while deriving the exact own-namespace selectors.
func iamKVRequests(fn, op string, args json.RawMessage) ([]workloadiam.Request, *sandbox.SDKError) {
	if !utf8.Valid(args) {
		return nil, fail(400, "INVALID_JSON", "invalid request body")
	}
	request := func(op, key string, prefix bool) workloadiam.Request {
		kv := &workloadiam.KVResource{Key: &key}
		if prefix {
			kv.Key, kv.Prefix = nil, &key
		}
		return workloadiam.Request{Operation: workloadiam.Operation(op), FunctionID: fn, Resource: workloadiam.Resource{OwnKV: kv}}
	}
	var key string
	var e *sandbox.SDKError
	switch op {
	case "kv.get", "kv.delete":
		var a keyArgs
		e = decodeArgs(args, limitSmall, &a)
		key = a.Key
	case "kv.put":
		var a putArgs
		e = decodeArgs(args, limitValue, &a)
		key = a.Key
	case "kv.incr":
		var a incrArgs
		e = decodeArgs(args, limitSmall, &a)
		key = a.Key
	case "kv.cas":
		var a casArgs
		e = decodeArgs(args, limitValue, &a)
		key = a.Key
	case "kv.list":
		var a listArgs
		if e = decodeArgs(args, limitSmall, &a); e == nil {
			return []workloadiam.Request{request(op, a.Prefix, true)}, nil
		}
	case "kv.batch":
		var a batchArgs
		if e = decodeArgs(args, limitBatch, &a); e == nil {
			if len(a.Ops) == 0 || len(a.Ops) > workloadiam.MaxBatchOperations {
				return nil, iamDenied()
			}
			requests := make([]workloadiam.Request, len(a.Ops))
			for i, item := range a.Ops {
				requests[i] = request("kv."+item.Op, item.Key, false)
			}
			return requests, nil
		}
	default:
		return nil, iamDenied()
	}
	if e != nil {
		return nil, e
	}
	return []workloadiam.Request{request(op, key, false)}, nil
}
