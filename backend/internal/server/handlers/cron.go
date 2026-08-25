package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/scheduler"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// CronHandler exposes CRUD over per-function cron schedules. The scheduler
// goroutine in internal/scheduler reads these rows independently — the
// handler doesn't need a back-channel to notify it because the goroutine
// re-queries every tick.
type CronHandler struct {
	DB       *database.Database
	Registry *registry.Registry
	SDKAuth  *sdkauth.Authenticator
}

// cronRequest is the shape of POST/PUT bodies. Payload is optional and
// defaults to "{}" when absent. Timezone is an IANA name (e.g.
// "Asia/Kolkata"); empty string defaults to "UTC" so older clients keep
// working unchanged.
type cronRequest struct {
	CronExpr string `json:"cron_expr"`
	Timezone string `json:"timezone,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

// resolveTZ validates a timezone string and returns its *time.Location.
// Empty string maps to UTC. Invalid IANA names return an error so the
// caller can reject the request with a 400 instead of silently
// reverting to UTC (which would surprise the operator).
func resolveTZ(name string) (*time.Location, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.UTC, "UTC", nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, "", errors.New("invalid timezone: " + name)
	}
	return loc, name, nil
}

// List handles GET /api/v1/functions/{fn_id}/cron.
func (h *CronHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")

	if _, err := h.Registry.Get(fnID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	rows, err := h.DB.ListCronSchedulesForFunction(fnID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list schedules: "+err.Error(), reqID)
		return
	}
	if rows == nil {
		rows = []*database.CronSchedule{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"schedules": rows})
}

// Create handles POST /api/v1/functions/{fn_id}/cron.
func (h *CronHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")

	if _, err := h.Registry.Get(fnID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	var req cronRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	expr := strings.TrimSpace(req.CronExpr)
	if expr == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "cron_expr is required", reqID)
		return
	}
	sched, err := scheduler.ParseCronExpr(expr)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "invalid cron_expr: "+err.Error(), reqID)
		return
	}
	loc, tzName, err := resolveTZ(req.Timezone)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	payload, err := encodePayload(req.Payload)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	row := &database.CronSchedule{
		FunctionID: fnID,
		CronExpr:   expr,
		Timezone:   tzName,
		Enabled:    enabled,
		Payload:    payload,
	}
	// Compute next fire time IN THE SCHEDULE'S TIMEZONE so "0 9 * * *"
	// in Asia/Kolkata fires at 9 AM IST, not 9 AM UTC. Store as UTC.
	next := sched.Next(time.Now().In(loc)).UTC()
	row.NextRunAt = &next

	if err := h.DB.InsertCronSchedule(row); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create schedule: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusCreated, row)
}

// Update handles PUT /api/v1/functions/{fn_id}/cron/{id}. Accepts any
// subset of {cron_expr, enabled, payload}; omitted fields keep their
// previous value.
func (h *CronHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	id := r.PathValue("id")

	row, err := h.DB.GetCronSchedule(id)
	if err != nil || row.FunctionID != fnID {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "schedule not found", reqID)
		return
	}

	var req cronRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}

	changed := false
	if expr := strings.TrimSpace(req.CronExpr); expr != "" && expr != row.CronExpr {
		if _, err := scheduler.ParseCronExpr(expr); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "invalid cron_expr: "+err.Error(), reqID)
			return
		}
		row.CronExpr = expr
		changed = true
	}
	if tz := strings.TrimSpace(req.Timezone); tz != "" && tz != row.Timezone {
		if _, _, err := resolveTZ(tz); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
			return
		}
		row.Timezone = tz
		changed = true
	}
	// Capture the PRIOR enabled state before overwriting it. The recompute
	// below keys on "disabled -> enabled", but read row.Enabled after this
	// assignment, so it was true for any edit of an already-enabled
	// schedule -- and pushed next_run_at forward, silently skipping a fire
	// that was already due.
	wasEnabled := row.Enabled
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	if req.Payload != nil {
		payload, err := encodePayload(req.Payload)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
			return
		}
		row.Payload = payload
	}

	// Recompute next_run_at when the expression OR timezone changes, or
	// when toggling from disabled→enabled (so a long-paused schedule
	// fires soon, not according to its stale next_run_at).
	if changed || (row.Enabled && !wasEnabled) {
		sched, _ := scheduler.ParseCronExpr(row.CronExpr)
		loc, _, _ := resolveTZ(row.Timezone)
		next := sched.Next(time.Now().In(loc)).UTC()
		row.NextRunAt = &next
	}

	if err := h.DB.UpdateCronSchedule(row); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to update schedule: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, row)
}

// Delete handles DELETE /api/v1/functions/{fn_id}/cron/{id}.
func (h *CronHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	id := r.PathValue("id")

	row, err := h.DB.GetCronSchedule(id)
	if err != nil || row.FunctionID != fnID {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "schedule not found", reqID)
		return
	}
	if err := h.DB.DeleteCronSchedule(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to delete schedule: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// ListAll handles GET /api/v1/cron — every schedule across the system.
// Used by the dashboard "Schedules" page that doesn't scope to a single
// function. Joins with the functions table so the UI gets function_name
// without a second roundtrip per row.
func (h *CronHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rows, err := h.DB.ListAllCronSchedulesWithFunction()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list schedules: "+err.Error(), reqID)
		return
	}
	if rows == nil {
		rows = []*database.CronScheduleWithFunction{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"schedules": rows})
}

// upsertInternalRequest carries the body for the SDK's crons.upsert call.
// The function ID is derived from the verified scoped SDK credential rather
// than a caller-controlled body or header.
type upsertInternalRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

// maxSDKSchedulesPerFunction bounds how many distinct named schedules one
// function may declare for itself. Generous for any real use -- the pattern is
// a handful of named jobs -- and low enough that a runaway or hostile loop
// cannot fill the scheduler.
const maxSDKSchedulesPerFunction = 25

// UpsertInternal handles POST /api/v1/_internal/crons. SDK-only path
// guarded by X-Orva-Internal-Token; the function being scheduled is the
// signed caller's own function, so the SDK doesn't need to send its UUID.
func (h *CronHandler) UpsertInternal(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, authErr := h.SDKAuth.Verify(r.Header.Get("X-Orva-Internal-Token"))
	if authErr != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid SDK credential", reqID)
		return
	}

	// A signature alone is not enough here, unlike KV.
	//
	// A schedule is a standing invocation that nothing in internal/scheduler
	// checks auth_mode for, and it is a database row — so it outlives the
	// process-random signing key that authorised it. Restarting orvad
	// invalidates every other use of a leaked ORVA_INTERNAL_TOKEN and does not
	// touch a schedule that token planted. That asymmetry, not symmetry with
	// the other SDK surfaces, is why this one gets the gate: it is the only
	// capability here that a restart cannot clean up.
	//
	// OwnsExecution rather than TraceContext: cron needs no trace context, and
	// this fails closed on a missing header for free, because BindExecution
	// refuses to store under an empty execution ID so the lookup always misses.
	if !h.SDKAuth.OwnsExecution(r.Header.Get("X-Orva-Execution-Id"), fnID) {
		respond.Error(w, http.StatusForbidden, "SDK_SCOPE_VIOLATION",
			"crons.upsert must be called from inside your handler, not at module scope", reqID)
		return
	}

	body, err := readBoundedBody(r.Body, 1<<20)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", err.Error(), reqID)
		return
	}
	var req upsertInternalRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Schedule = strings.TrimSpace(req.Schedule)
	if req.Name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	if req.Schedule == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "schedule is required", reqID)
		return
	}
	sched, err := scheduler.ParseCronExpr(req.Schedule)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "invalid schedule: "+err.Error(), reqID)
		return
	}
	loc, tzName, err := resolveTZ(req.Timezone)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	payload, err := encodePayload(req.Payload)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	row := &database.CronSchedule{
		FunctionID: fnID,
		Name:       req.Name,
		CronExpr:   req.Schedule,
		Timezone:   tzName,
		Enabled:    enabled,
		Payload:    payload,
	}
	if enabled {
		next := sched.Next(time.Now().In(loc)).UTC()
		row.NextRunAt = &next
	}
	// Upsert is keyed by (function, name), so distinct names multiply without
	// bound, and nothing validates the interval either -- "* * * * *" is
	// accepted. One compromised execution could otherwise register hundreds of
	// once-a-minute schedules against its own function and leave them running.
	//
	// The cap is on this path only. Schedules an operator creates from the
	// dashboard are not limited; a function declaring its own is.
	existing, listErr := h.DB.ListCronSchedulesForFunction(fnID)
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
			if e.Name == req.Name {
				fresh = false
			}
		}
		if fresh && declared >= maxSDKSchedulesPerFunction {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				fmt.Sprintf("a function may declare at most %d schedules; delete one before adding another",
					maxSDKSchedulesPerFunction), reqID)
			return
		}
	}

	id, err := h.DB.UpsertCronScheduleByName(row)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL",
			"upsert failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"id":          id,
		"function_id": fnID,
		"name":        req.Name,
		"schedule":    req.Schedule,
		"timezone":    tzName,
		"enabled":     enabled,
	})
}

// encodePayload normalizes a JSON value to a string for storage. Accepts
// either a JSON-serializable value or a pre-encoded string. Empty input
// stores "{}".
func encodePayload(p any) (string, error) {
	if p == nil {
		return "{}", nil
	}
	switch v := p.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return "{}", nil
		}
		// Validate it's parseable JSON so cron payloads can't poison user code.
		var probe any
		if err := json.Unmarshal([]byte(s), &probe); err != nil {
			return "", errors.New("payload must be valid JSON")
		}
		return s, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", errors.New("payload must be JSON-serializable")
		}
		return string(b), nil
	}
}
