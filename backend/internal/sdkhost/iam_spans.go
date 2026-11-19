package sdkhost

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/trace"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func (h *Host) spansEmitIAM(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if h.iam == nil || !h.iamCurrentEnabled || h.iamCurrent == nil {
		return nil, iamDenied()
	}
	var req handlers.UserSpanRequest
	if !utf8.Valid(args) {
		return nil, fail(400, "INVALID_JSON", "invalid request body")
	}
	if err := decodeBody(args, handlers.MaxUserSpanBody, "failed to read body", &req); err != nil {
		return nil, err
	}
	if req.Name == "" || req.DurationMS < 0 || (req.Status != "" && req.Status != "ok" && req.Status != "error") {
		return nil, fail(400, "VALIDATION", "invalid span")
	}
	x, ok := caller.WorkloadExecution()
	if !ok {
		return nil, iamDenied()
	}
	span, serr := handlers.PrepareUserSpan(caller.FunctionID, x.ExecutionID, x.TraceID, x.SpanID, x.StartedAt, &req)
	if serr != nil {
		return nil, coreErr(serr)
	}
	span.ID = trace.NewSpanID()
	if span.Status == "" {
		span.Status = "ok"
	}
	r := workloadiam.Request{Operation: workloadiam.OpSpansEmit, FunctionID: caller.FunctionID,
		ExecutionID: x.ExecutionID, Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}}
	accepted := false
	err := h.iam.EmitSpanObserved(func() workloadiam.Context {
		c := h.iamContext(ctx, caller)
		if c.Phase != workloadiam.Execution || x.Lease != c.Lease.ID || x.ExecutionID != c.Lease.ExecutionID ||
			x.TraceID == "" || x.SpanID == "" || x.StartedAt.IsZero() {
			return workloadiam.Context{}
		}
		return c
	}, r, func(decision workloadiam.SpanDecision) error {
		accepted = h.DB.TryInsertAdmittedUserSpan(*span, decision)
		return nil
	})
	if err != nil {
		return nil, iamDenied()
	}
	status := "dropped"
	if accepted {
		status = "queued"
	}
	return map[string]string{"status": status, "id": span.ID}, nil
}
