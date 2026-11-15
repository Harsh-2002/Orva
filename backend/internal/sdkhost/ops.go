package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

// jobs.enqueue, crons.upsert and spans.emit run the same identity-based cores
// as the HTTP handlers (handlers.EnqueueJob / UpsertCron / CreateUserSpan), so
// validation, limits and reply bodies cannot drift between the transports.

// decodeBody is decodeArgs with the HTTP handler's oversize message.
func decodeBody(args json.RawMessage, limit int, tooBig string, dst any) *sandbox.SDKError {
	if len(args) > limit {
		return fail(400, "INVALID_BODY", tooBig)
	}
	return decodeArgs(args, limit, dst)
}

func coreErr(e *handlers.StatusErr) *sandbox.SDKError {
	return fail(e.Status, e.Code, e.Message)
}

// revoked reports whether the dispatch that issued exec has ended. Called
// immediately before every side effect so a revoked lease cancels the work.
func revoked(ctx context.Context, exec *sandbox.ExecInfo) *sandbox.SDKError {
	if ctx.Err() != nil || (exec != nil && exec.Ctx != nil && exec.Ctx.Err() != nil) {
		return fail(403, "SDK_SCOPE_VIOLATION", "execution is no longer active")
	}
	return nil
}

func (h *Host) jobsEnqueue(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if caller.Exec == nil {
		return nil, fail(403, "SDK_SCOPE_VIOLATION", "SDK request is not associated with an active caller execution")
	}
	var req handlers.EnqueueJobRequest
	if e := decodeBody(args, handlers.MaxEnqueueBody, "request body exceeds limit", &req); e != nil {
		return nil, e
	}
	if e := revoked(ctx, caller.Exec); e != nil {
		return nil, e
	}
	job, serr := handlers.EnqueueJob(h.DB, caller.FunctionID, caller.Exec.TraceID, caller.Exec.SpanID, &req)
	if serr != nil {
		return nil, coreErr(serr)
	}
	return job, nil
}

func (h *Host) cronsUpsert(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if caller.Exec == nil {
		return nil, fail(403, "SDK_SCOPE_VIOLATION", "crons.upsert must be called from inside your handler, not at module scope")
	}
	var req handlers.UpsertCronRequest
	if e := decodeBody(args, handlers.MaxCronUpsertBody, "request body exceeds limit", &req); e != nil {
		return nil, e
	}
	if e := revoked(ctx, caller.Exec); e != nil {
		return nil, e
	}
	res, serr := handlers.UpsertCron(h.DB, caller.FunctionID, &req)
	if serr != nil {
		return nil, coreErr(serr)
	}
	return res, nil
}

func (h *Host) spansEmit(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	// The HTTP path answers 400 VALIDATION for a missing execution header and
	// 403 for a stale one; the channel cannot tell module scope from a revoked
	// lease, so both are the 403.
	if caller.Exec == nil {
		return nil, fail(403, "SDK_SCOPE_VIOLATION", "span execution is not active for the calling function")
	}
	var req handlers.UserSpanRequest
	if e := decodeBody(args, handlers.MaxUserSpanBody, "failed to read body", &req); e != nil {
		return nil, e
	}
	if e := revoked(ctx, caller.Exec); e != nil {
		return nil, e
	}
	x := caller.Exec
	res, serr := handlers.CreateUserSpan(h.DB, caller.FunctionID, x.ExecutionID, x.TraceID, x.SpanID, x.StartedAt, &req)
	if serr != nil {
		return nil, coreErr(serr)
	}
	return res, nil
}
