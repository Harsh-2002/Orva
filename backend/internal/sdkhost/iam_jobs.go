package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func (h *Host) jobsEnqueueIAM(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if h.asyncIAM == nil || h.asyncIAM.Authority != h.iam || caller.Exec == nil {
		return nil, iamDenied()
	}
	var req handlers.EnqueueJobRequest
	if e := decodeBody(args, handlers.MaxEnqueueBody, "request body exceeds limit", &req); e != nil {
		return nil, e
	}
	job, serr := handlers.PrepareEnqueueJob(h.DB, caller.FunctionID, caller.Exec.TraceID, caller.Exec.SpanID, &req)
	if serr != nil {
		return nil, iamDenied()
	}
	j := &database.AsyncJob{Job: *job, AsyncProvenance: database.AsyncProvenance{CreatorID: caller.FunctionID}}
	if err := h.asyncIAM.EnqueueFunction(ctx, func() workloadiam.Context { return h.iamContext(ctx, caller) }, j); err != nil {
		if workloadasyncDenied(err) {
			return nil, iamDenied()
		}
		return nil, fail(503, "INTERNAL", "async storage unavailable")
	}
	return j, nil
}

func workloadasyncDenied(err error) bool {
	return err == workloadiam.ErrDenied || err == database.ErrAsyncConflict
}
