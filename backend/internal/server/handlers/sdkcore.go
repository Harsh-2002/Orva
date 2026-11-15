package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/scheduler"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// Identity-based cores shared by the HTTP handlers and the SDK channel host
// (internal/sdkhost). Each takes an already-authenticated function identity
// and a decoded request, so the two transports cannot drift apart.

// StatusErr is a failure with the HTTP status, code and message both
// transports report verbatim.
type StatusErr struct {
	Status  int
	Code    string
	Message string
}

func (e *StatusErr) write(w http.ResponseWriter, reqID string) {
	respond.Error(w, e.Status, e.Code, e.Message, reqID)
}

func statusErr(status int, code, msg string) *StatusErr {
	return &StatusErr{Status: status, Code: code, Message: msg}
}

// Body ceilings of the three SDK operations.
const (
	MaxEnqueueBody    = 1 << 20
	MaxCronUpsertBody = 1 << 20
	MaxUserSpanBody   = 1 << 16
)

// ---- jobs.enqueue ----

// EnqueueJobRequest is the body of POST /api/v1/jobs and of the jobs.enqueue
// SDK operation.
type EnqueueJobRequest struct {
	FunctionID               string          `json:"function_id"`
	FunctionName             string          `json:"function_name"`
	Payload                  json.RawMessage `json:"payload"`
	ScheduledAt              *time.Time      `json:"scheduled_at,omitempty"`
	MaxAttempts              int             `json:"max_attempts,omitempty"`
	IdempotencyKey           string          `json:"idempotency_key,omitempty"`
	IdempotencyWindowSeconds int             `json:"idempotency_window_seconds,omitempty"`
}

// EnqueueJob validates req and inserts the job. callerFnID/trace/span are the
// attribution of an SDK caller and are empty for dashboard/API-key requests.
func EnqueueJob(db *database.Database, callerFnID, callerTraceID, callerSpanID string, req *EnqueueJobRequest) (*database.Job, *StatusErr) {
	// Resolve function_id from either field. Adapter SDK sends function_name;
	// dashboard sends function_id directly.
	fnID := req.FunctionID
	if fnID == "" && req.FunctionName != "" {
		fn, err := db.GetFunctionByName(req.FunctionName)
		if err != nil {
			return nil, statusErr(http.StatusNotFound, "NOT_FOUND", "function not found: "+req.FunctionName)
		}
		fnID = fn.ID
	}
	if fnID == "" {
		return nil, statusErr(http.StatusBadRequest, "VALIDATION", "function_id or function_name is required")
	}
	if _, err := db.GetFunction(fnID); err != nil {
		return nil, statusErr(http.StatusNotFound, "NOT_FOUND", "function not found")
	}

	payload := []byte(req.Payload)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	job := &database.Job{
		FunctionID:               fnID,
		Payload:                  payload,
		MaxAttempts:              req.MaxAttempts,
		IdempotencyKey:           req.IdempotencyKey,
		IdempotencyWindowSeconds: req.IdempotencyWindowSeconds,
	}
	if req.ScheduledAt != nil {
		job.ScheduledAt = req.ScheduledAt.UTC()
	}
	// v0.5 trace context: when a function inside a sandbox enqueues this
	// job, the caller's trace is persisted on the row so the eventual
	// execution lands in the same trace. Empty values (dashboard or external
	// API caller) leave these blank → the picked-up job becomes a new root.
	job.TraceID = callerTraceID
	job.ParentSpanID = callerSpanID
	job.EnqueuedByFunctionID = callerFnID
	if err := db.EnqueueJob(job); err != nil {
		return nil, statusErr(http.StatusInternalServerError, "INTERNAL", "enqueue failed: "+err.Error())
	}
	return job, nil
}

// ---- crons.upsert ----

// UpsertCronRequest is the body of POST /api/v1/_internal/crons and of the
// crons.upsert SDK operation.
type UpsertCronRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

// UpsertCron validates req and upserts the named schedule of fnID. The caller
// has already established that fnID is inside a live execution.
func UpsertCron(db *database.Database, fnID string, req *UpsertCronRequest) (map[string]any, *StatusErr) {
	name := strings.TrimSpace(req.Name)
	schedule := strings.TrimSpace(req.Schedule)
	if name == "" {
		return nil, statusErr(http.StatusBadRequest, "VALIDATION", "name is required")
	}
	if schedule == "" {
		return nil, statusErr(http.StatusBadRequest, "VALIDATION", "schedule is required")
	}
	sched, err := scheduler.ParseCronExpr(schedule)
	if err != nil {
		return nil, statusErr(http.StatusBadRequest, "VALIDATION", "invalid schedule: "+err.Error())
	}
	loc, tzName, err := resolveTZ(req.Timezone)
	if err != nil {
		return nil, statusErr(http.StatusBadRequest, "VALIDATION", err.Error())
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	payload, err := encodePayload(req.Payload)
	if err != nil {
		return nil, statusErr(http.StatusBadRequest, "VALIDATION", err.Error())
	}
	row := &database.CronSchedule{
		FunctionID: fnID,
		Name:       name,
		CronExpr:   schedule,
		Timezone:   tzName,
		Enabled:    enabled,
		Payload:    payload,
	}
	if enabled {
		next := sched.Next(time.Now().In(loc)).UTC()
		row.NextRunAt = &next
	}
	// Upsert is keyed by (function, name), so distinct names multiply without
	// bound, and nothing validates the interval -- "* * * * *" is accepted.
	// One compromised execution could otherwise register hundreds of
	// once-a-minute schedules against its own function and leave them running.
	//
	// The cap is on this path only. Schedules an operator creates from the
	// dashboard are not limited; a function declaring its own is.
	existing, listErr := db.ListCronSchedulesForFunction(fnID)
	if listErr != nil {
		// Fail open rather than fail a deploy over a read, but say so: the cap
		// is a guard rail, and a guard rail that silently stops applying is
		// worse than one that was never there.
		slog.Warn("could not count existing schedules; SDK schedule cap not applied",
			"fn", fnID, "err", listErr)
	} else {
		declared, fresh := 0, true
		for _, e := range existing {
			// Only rows the SDK declared count against the cap. Operator
			// schedules carry no name -- counting them would lock a function
			// out of its FIRST self-declared schedule on a function that
			// happens to have a busy Schedules page.
			if e.Name == "" {
				continue
			}
			declared++
			if e.Name == name {
				fresh = false
			}
		}
		if fresh && declared >= maxSDKSchedulesPerFunction {
			return nil, statusErr(http.StatusBadRequest, "VALIDATION",
				fmt.Sprintf("a function may declare at most %d schedules; delete one before adding another",
					maxSDKSchedulesPerFunction))
		}
	}

	id, err := db.UpsertCronScheduleByName(row)
	if err != nil {
		return nil, statusErr(http.StatusInternalServerError, "INTERNAL", "upsert failed: "+err.Error())
	}
	return map[string]any{
		"id":          id,
		"function_id": fnID,
		"name":        name,
		"schedule":    schedule,
		"timezone":    tzName,
		"enabled":     enabled,
	}, nil
}

// ---- spans.emit ----

// UserSpanRequest is the body of POST /api/v1/_internal/spans and of the
// spans.emit SDK operation. Trace, parent span and execution come from the
// caller's execution, never from the body.
type UserSpanRequest struct {
	Name         string          `json:"name"`
	StartedAt    time.Time       `json:"started_at"`
	DurationMS   int64           `json:"duration_ms"`
	Status       string          `json:"status,omitempty"`        // "ok" | "error"
	ErrorMessage string          `json:"error_message,omitempty"` // populated when status="error"
	Attributes   json.RawMessage `json:"attributes,omitempty"`    // arbitrary JSON
}

// CreateUserSpan validates req and queues the span for the async writer.
func CreateUserSpan(db *database.Database, fnID, execID, traceID, parentSpanID string, executionStart time.Time, req *UserSpanRequest) (map[string]string, *StatusErr) {
	if req.Name == "" {
		return nil, statusErr(http.StatusBadRequest, "VALIDATION", "name is required")
	}
	if req.StartedAt.IsZero() {
		req.StartedAt = time.Now().UTC()
	}
	// offset_ms relative to the parent execution row; negative clamps to 0.
	offset := req.StartedAt.Sub(executionStart).Milliseconds()
	if offset < 0 {
		offset = 0
	}
	span := &database.UserSpan{
		FunctionID:   fnID,
		TraceID:      traceID,
		ParentSpanID: parentSpanID,
		ExecutionID:  execID,
		Name:         req.Name,
		StartedAt:    req.StartedAt.UTC(),
		DurationMS:   req.DurationMS,
		Status:       req.Status,
		ErrorMessage: req.ErrorMessage,
		OffsetMS:     offset,
	}
	if len(req.Attributes) > 0 {
		span.Attributes = string(req.Attributes)
	}
	db.AsyncInsertUserSpan(span)
	return map[string]string{"status": "queued", "id": span.ID}, nil
}
