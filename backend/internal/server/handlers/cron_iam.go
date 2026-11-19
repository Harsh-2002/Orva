package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
)

func cronControlError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		respond.Error(w, 404, "NOT_FOUND", "schedule not found", "")
		return
	}
	if errors.Is(err, workloadasync.ErrCronInput) {
		respond.Error(w, 400, "VALIDATION", "invalid cron configuration", "")
		return
	}
	asyncJobError(w, err)
}
func (h *CronHandler) iamReady(w http.ResponseWriter) bool {
	if h.CronIAM == nil || h.CronIAM.DB != h.DB {
		cronControlError(w, database.ErrOperatorDenied)
		return false
	}
	return true
}
func (h *CronHandler) iamList(w http.ResponseWriter, r *http.Request, owner string) {
	if !h.iamReady(w) {
		return
	}
	rows, err := h.DB.ListAsyncCrons(r.Context(), owner, nil)
	if err != nil {
		cronControlError(w, err)
		return
	}
	ref, ok := auth.OperatorFromContext(r.Context())
	respond.JSON(w, 200, map[string]any{"schedules": rows, "cron_controls": true, "can_manage": ok && h.DB.CanManageAsync(r.Context(), ref)})
}
func cronDecode(r *http.Request, dst any) error {
	body, err := readBoundedBody(r.Body, 1<<20)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if !json.Valid(body) {
		return workloadasync.ErrCronInput
	}
	return decoder.Decode(dst)
}
func (h *CronHandler) iamCreate(w http.ResponseWriter, r *http.Request) {
	if !h.iamReady(w) {
		return
	}
	ref, ok := auth.OperatorFromContext(r.Context())
	if !ok {
		cronControlError(w, database.ErrOperatorDenied)
		return
	}
	var in cronRequest
	if cronDecode(r, &in) != nil {
		respond.Error(w, 400, "INVALID_JSON", "invalid request body", "")
		return
	}
	payload, err := encodePayload(in.Payload)
	if err != nil {
		cronControlError(w, workloadasync.ErrCronInput)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	c := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: r.PathValue("fn_id"), CronExpr: in.CronExpr, Timezone: in.Timezone, Enabled: enabled, Payload: payload}}
	if err := h.CronIAM.CreateCronOperator(r.Context(), ref, c); err != nil {
		cronControlError(w, err)
		return
	}
	respond.JSON(w, 201, c)
}

// Resume is bound only by explicit scratch construction, never the normal router.
func (h *CronHandler) Resume(w http.ResponseWriter, r *http.Request) {
	if !h.CronEnabled {
		respond.Error(w, 404, "NOT_FOUND", "cron controls unavailable", "")
		return
	}
	h.iamMutate(w, r, "resume")
}
func (h *CronHandler) iamMutate(w http.ResponseWriter, r *http.Request, action string) {
	if !h.iamReady(w) {
		return
	}
	ref, ok := auth.OperatorFromContext(r.Context())
	if !ok {
		cronControlError(w, database.ErrOperatorDenied)
		return
	}
	var in workloadasync.CronControl
	if action == "delete" {
		if r.URL.Query().Get("confirm") != "true" {
			respond.Error(w, 400, "VALIDATION", "delete requires confirm=true", "")
			return
		}
		in.ExpectedConfigRevision = r.URL.Query().Get("expected_config_revision")
		in.ExpectedAuthorityGeneration = r.URL.Query().Get("expected_authority_generation")
	} else if cronDecode(r, &in) != nil {
		respond.Error(w, 400, "INVALID_JSON", "invalid request body", "")
		return
	}
	c, err := h.CronIAM.ControlCron(r.Context(), ref, r.PathValue("fn_id"), r.PathValue("id"), action, in)
	if err != nil {
		cronControlError(w, err)
		return
	}
	if action == "delete" {
		respond.JSON(w, 200, map[string]string{"id": r.PathValue("id"), "status": "deleted"})
		return
	}
	respond.JSON(w, 200, c)
}
