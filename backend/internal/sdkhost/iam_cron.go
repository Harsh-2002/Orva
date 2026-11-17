package sdkhost

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

// WithCronIAM is independent of jobs activation; explicit nil remains denied.
func WithCronIAM(service *workloadasync.Service) Option {
	return func(h *Host) { h.cronEnabled = true; h.cronIAM = service }
}
func (h *Host) cronsUpsertIAM(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if !h.cronEnabled || h.cronIAM == nil || h.cronIAM.Authority != h.iam || caller.Exec == nil {
		return nil, iamDenied()
	}
	var req handlers.UpsertCronRequest
	if e := decodeBody(args, handlers.MaxCronUpsertBody, "request body exceeds limit", &req); e != nil {
		return nil, e
	}
	payload, err := cronPayload(req.Payload)
	if err != nil {
		return nil, fail(400, "VALIDATION", "invalid payload")
	}
	if req.Payload == nil {
		payload = []byte("{}")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	c := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: caller.FunctionID, Name: req.Name,
		CronExpr: req.Schedule, Timezone: req.Timezone, Enabled: enabled, Payload: string(payload)}, AsyncProvenance: database.AsyncProvenance{CreatorID: caller.FunctionID}}
	if err := h.cronIAM.UpsertCronFunction(ctx, func() workloadiam.Context { return h.iamContext(ctx, caller) }, c); err != nil {
		return nil, iamDenied()
	}
	return map[string]any{"id": c.ID, "function_id": c.FunctionID, "name": c.Name, "schedule": c.CronExpr, "timezone": c.Timezone, "enabled": c.Enabled}, nil
}

func cronPayload(value any) ([]byte, error) {
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			text = "{}"
		}
		raw := []byte(text)
		if !json.Valid(raw) {
			return nil, workloadasync.ErrCronInput
		}
		return raw, nil
	}
	if value == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(value)
}
