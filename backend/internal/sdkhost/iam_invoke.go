package sdkhost

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

// WithIAMTargetState supplies trusted active/current-generation state, not policy
// existence. It is constructor-only and does not enable IAM. Nil denies calls.
// The observer runs only under admission: bounded atomic reads, no DB/I/O or
// worker locks. Future host-state updates must share Authority.ChangeHostState.
func WithIAMTargetState(observe func(string, *sandbox.Worker) workloadiam.TargetState) Option {
	return func(h *Host) { h.iamTargetState = observe }
}

func (h *Host) beginIAMInvoke(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (*prepared, *workloadiam.Admission, *sandbox.SDKError) {
	return h.beginIAMCall(ctx, caller, args, workloadiam.OpInvoke)
}

func (h *Host) beginIAMCall(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage, op workloadiam.Operation) (*prepared, *workloadiam.Admission, *sandbox.SDKError) {
	inv := h.invoker.Load()
	if h.iam == nil || h.iamTargetState == nil || inv == nil || caller.Exec == nil || caller.Exec.Ctx == nil {
		return nil, nil, iamDenied()
	}
	var a invokeArgs
	if !utf8.Valid(args) {
		return nil, nil, fail(400, "INVALID_JSON", "invalid request body")
	}
	if e := decodeArgs(args, sandbox.MaxSDKFrame, &a); e != nil {
		return nil, nil, e
	}
	if a.Name == "" {
		return nil, nil, fail(400, "VALIDATION", "function name is required")
	}
	body := []byte(a.Body)
	if len(body) == 0 {
		body = []byte("{}")
	}
	if len(body) > maxInvokeBody {
		return nil, nil, fail(400, "INVALID_BODY", "failed to read body")
	}
	exec := *caller.Exec
	if e := inv.CheckDepth(exec.Depth); e != nil {
		return nil, nil, fromInvokeErr(e)
	}
	fn, e := inv.ResolveCallee(a.Name)
	if e != nil {
		return nil, nil, iamDenied()
	}
	targetID := fn.ID
	call, err := h.iam.BeginCallObserved(exec.Ctx, func() workloadiam.Context {
		return h.iamInvokeContext(ctx, caller, exec)
	}, workloadiam.Request{Operation: op, FunctionID: caller.FunctionID,
		Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: targetID}}})
	if err != nil {
		return nil, nil, iamDenied()
	}
	return &prepared{inv: inv, fn: fn, body: body, parent: exec.Ctx, caller: handlers.F2FCaller{
		FunctionID: caller.FunctionID, TraceID: exec.TraceID, SpanID: exec.SpanID, Depth: exec.Depth,
	}}, call, nil
}

func (h *Host) iamInvokeContext(ctx context.Context, caller sandbox.SDKCaller, exec sandbox.ExecInfo) workloadiam.Context {
	c := h.iamContext(ctx, caller)
	if exec.Ctx == nil || exec.Ctx.Err() != nil || c.Phase != workloadiam.Execution ||
		c.Lease.ID != exec.Lease || c.Lease.ExecutionID != exec.ExecutionID {
		return workloadiam.Context{}
	}
	return c
}

func (h *Host) admitIAMInvoke(ctx context.Context, caller sandbox.SDKCaller, exec sandbox.ExecInfo, call *workloadiam.Admission, targetID string, worker *sandbox.Worker) *handlers.InvokeError {
	err := call.AdmitObserved(func() (workloadiam.Context, workloadiam.TargetState) {
		return h.iamInvokeContext(ctx, caller, exec), h.iamInvokeTarget(targetID, worker)
	})
	if err != nil {
		return &handlers.InvokeError{Status: 403, Opts: respond.ErrorOpts{Code: "IAM_DENIED", Message: "SDK operation denied"}}
	}
	return nil
}

func (h *Host) iamInvokeTarget(targetID string, worker *sandbox.Worker) workloadiam.TargetState {
	if h.iamTargetState == nil {
		return workloadiam.TargetState{}
	}
	s, ok := worker.WorkloadState()
	if !ok || !s.Alive || !s.Ready || s.Identity.FunctionID != targetID {
		return workloadiam.TargetState{}
	}
	target := h.iamTargetState(targetID, worker)
	actual := workloadiam.Principal{FunctionID: s.Identity.FunctionID, WorkerID: s.Identity.WorkerID, ArtifactRevision: s.Identity.ArtifactRevision}
	if target.Principal != actual {
		return workloadiam.TargetState{}
	}
	return target
}

func (h *Host) invokeIAM(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if caller.Exec != nil {
		exec := *caller.Exec
		caller.Exec = &exec
	}
	p, call, e := h.beginIAMInvoke(ctx, caller, args)
	if e != nil {
		return nil, e
	}
	defer call.Finish()
	exec := *caller.Exec
	targetID := p.fn.ID
	admitted := false
	resp, ie := p.inv.RunInvokeWithAdmission(call.Context(), p.caller, p.fn, p.body, "", func(worker *sandbox.Worker) *handlers.InvokeError {
		err := h.admitIAMInvoke(ctx, caller, exec, call, targetID, worker)
		admitted = err == nil
		return err
	})
	if ie != nil {
		// Before acquired-target admission, even pool errors can reveal status.
		if !admitted || call.Context().Err() != nil {
			return nil, iamDenied()
		}
		return nil, fromInvokeErr(ie)
	}
	return json.RawMessage(resp), nil
}
