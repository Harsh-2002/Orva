package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// invokeError maps an error returned from the proxy/pool/worker stack to
// a structured HTTP response. Centralizing this in one place keeps the
// wire taxonomy consistent across handlers and avoids drift when new
// failure modes are added.
//
// Returns the response opts ready for respond.ErrorWithDetail. The caller
// supplies the request_id; everything else is derived from the error +
// the function context (so we can emit pool sizes, timeouts etc. as
// `details`).
func invokeError(err error, fn *database.Function, requestID string) (status int, opts respond.ErrorOpts) {
	opts.RequestID = requestID
	opts.Message = err.Error()

	switch {
	// — Pool / capacity / memory —
	case errors.Is(err, pool.ErrManagerClosed):
		return http.StatusServiceUnavailable, respond.ErrorOpts{
			Code: "SHUTTING_DOWN", Message: "server is shutting down",
			RequestID:   requestID,
			Hint:        "redirect traffic to a replacement instance; redeploy after upgrade",
			RetryAfterS: 30,
		}

	case errors.Is(err, pool.ErrFunctionBusy):
		details := map[string]any{}
		if fn != nil {
			details["function_id"] = fn.ID
			details["function_name"] = fn.Name
			if fn.MaxConcurrency > 0 {
				details["max_concurrency"] = fn.MaxConcurrency
			}
		}
		return http.StatusTooManyRequests, respond.ErrorOpts{
			Code: "FUNCTION_BUSY",
			Message: fmt.Sprintf("function %s is at its concurrency cap", funcLabel(fn)),
			RequestID: requestID,
			Hint: "raise functions.max_concurrency or switch the policy to 'queue' to wait for a slot",
			RetryAfterS: 1,
			Details: details,
		}

	case errors.Is(err, pool.ErrPoolAtCapacity):
		details := map[string]any{}
		if fn != nil {
			details["function_id"] = fn.ID
			details["function_name"] = fn.Name
		}
		return http.StatusServiceUnavailable, respond.ErrorOpts{
			Code: "POOL_AT_CAPACITY",
			Message: fmt.Sprintf("function pool at capacity for %s", funcLabel(fn)),
			RequestID: requestID,
			Hint: "raise pool_config.max_warm via PUT /api/v1/pool/config or reduce client concurrency",
			RetryAfterS: 5,
			Details: details,
		}

	case errors.Is(err, pool.ErrMemoryExhausted):
		return http.StatusServiceUnavailable, respond.ErrorOpts{
			Code: "MEMORY_EXHAUSTED", Message: "host memory budget exhausted",
			RequestID: requestID,
			Hint:      "deploy fewer concurrent functions or increase host RAM; see /api/v1/system/metrics.json host.mem_*",
			RetryAfterS: 30,
		}

	case errors.Is(err, sandbox.ErrTooManyRequests):
		return http.StatusTooManyRequests, respond.ErrorOpts{
			Code: "TOO_MANY_REQUESTS", Message: "host concurrency cap reached",
			RequestID: requestID,
			Hint:      "back off briefly and retry; raise cfg.Sandbox.MaxConcurrent if persistent",
			RetryAfterS: 1,
		}

	// — Worker process state —
	case errors.Is(err, sandbox.ErrWorkerExited):
		return http.StatusBadGateway, respond.ErrorOpts{
			Code: "WORKER_CRASHED", Message: "function worker exited unexpectedly",
			RequestID: requestID,
			Hint:      "check stderr in the latest execution log; common causes: process.exit, OOM, syntax error in handler",
		}

	// — Per-function timeout (different ctx than pool's) —
	case errors.Is(err, context.DeadlineExceeded):
		details := map[string]any{}
		if fn != nil && fn.TimeoutMS > 0 {
			details["timeout_ms"] = fn.TimeoutMS
		}
		return http.StatusGatewayTimeout, respond.ErrorOpts{
			Code: "TIMEOUT", Message: "function exceeded configured timeout",
			RequestID: requestID,
			Hint:      "raise functions.timeout_ms or optimize the handler",
			Details:   details,
		}

	// — Generic context cancellation (client disconnect) —
	case errors.Is(err, context.Canceled):
		return 499, respond.ErrorOpts{ // 499 Client Closed Request — nginx convention
			Code: "CLIENT_DISCONNECTED", Message: "client closed the connection",
			RequestID: requestID,
		}

	// — Unmapped sandbox / dispatch failure —
	default:
		return http.StatusServiceUnavailable, respond.ErrorOpts{
			Code: "SANDBOX_ERROR", Message: err.Error(),
			RequestID: requestID,
			Hint:      "see request_id in server logs; contact operator if this persists",
		}
	}
}

func funcLabel(fn *database.Function) string {
	if fn == nil {
		return "(unknown)"
	}
	if fn.Name != "" {
		return fn.Name
	}
	return fn.ID
}

// deployError maps build queue + deploy errors to the wire envelope.
func deployError(err error, requestID string, queueDepth int) (status int, opts respond.ErrorOpts) {
	switch {
	case errors.Is(err, builder.ErrQueueFull):
		// Estimate Retry-After from queue depth × an assumed 30s/build.
		retry := queueDepth * 30
		if retry < 5 {
			retry = 5
		}
		if retry > 300 {
			retry = 300
		}
		return http.StatusServiceUnavailable, respond.ErrorOpts{
			Code: "BUILD_QUEUE_FULL",
			Message: fmt.Sprintf("build queue full (%d pending)", queueDepth),
			RequestID:   requestID,
			Hint:        "wait for current builds to drain; consider raising NumCPU or staggering deploys",
			RetryAfterS: retry,
			Details:     map[string]any{"queue_depth": queueDepth},
		}
	case errors.Is(err, builder.ErrQueueStopping):
		return http.StatusServiceUnavailable, respond.ErrorOpts{
			Code: "SHUTTING_DOWN", Message: "build queue is stopping",
			RequestID: requestID, RetryAfterS: 30,
		}
	case errors.Is(err, builder.ErrInsufficientDisk):
		return http.StatusServiceUnavailable, respond.ErrorOpts{
			Code: "INSUFFICIENT_DISK",
			Message: "insufficient free disk space to start the build",
			RequestID: requestID,
			Hint:      "free space on the data volume or lower system_config.min_free_disk_mb; see docs/CAPACITY.md",
		}
	default:
		return http.StatusInternalServerError, respond.ErrorOpts{
			Code: "INTERNAL", Message: err.Error(), RequestID: requestID,
		}
	}
}
