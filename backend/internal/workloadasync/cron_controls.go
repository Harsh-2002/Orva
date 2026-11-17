package workloadasync

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

type CronControl struct {
	ExpectedConfigRevision      string          `json:"expected_config_revision"`
	ExpectedAuthorityGeneration string          `json:"expected_authority_generation"`
	CronExpr                    *string         `json:"cron_expr,omitempty"`
	Timezone                    *string         `json:"timezone,omitempty"`
	Enabled                     *bool           `json:"enabled,omitempty"`
	Payload                     json.RawMessage `json:"payload,omitempty"`
}

func cronRevision(text string) (uint64, error) {
	n, err := strconv.ParseUint(text, 10, 63)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != text {
		return 0, database.ErrAsyncConflict
	}
	return n, nil
}

// ControlCron keeps adapters from substituting operator origin or fresh CAS values.
func (s *Service) ControlCron(ctx context.Context, ref database.OperatorCredentialReference, owner, id, action string, in CronControl) (*database.AsyncCron, error) {
	if !s.cronReady() {
		return nil, database.ErrOperatorDenied
	}
	c, err := s.DB.GetAsyncCron(ctx, id)
	if err != nil {
		return nil, err
	}
	if owner != "" && owner != c.FunctionID {
		return nil, database.ErrAsyncConflict
	}
	revision, err := cronRevision(in.ExpectedConfigRevision)
	if err != nil {
		return nil, err
	}
	generation, err := cronRevision(in.ExpectedAuthorityGeneration)
	if err != nil {
		return nil, err
	}
	if c.ConfigRevision != revision || c.AuthorityGeneration != generation {
		return nil, database.ErrAsyncConflict
	}
	if action != "update" && (in.CronExpr != nil || in.Timezone != nil || in.Enabled != nil || in.Payload != nil) {
		return nil, ErrCronInput
	}
	if in.CronExpr != nil {
		c.CronExpr = *in.CronExpr
	}
	if in.Timezone != nil {
		c.Timezone = *in.Timezone
	}
	if in.Payload != nil {
		if !json.Valid(in.Payload) {
			return nil, ErrCronInput
		}
		c.Payload = string(in.Payload)
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if action == "update" && in.Enabled != nil && !*in.Enabled && in.CronExpr == nil && in.Timezone == nil && in.Payload == nil {
		action = "disable"
	}
	if err := s.MutateCron(ctx, ref, c, action); err != nil {
		return nil, err
	}
	if action == "delete" {
		return nil, nil
	}
	return s.DB.GetAsyncCron(ctx, id)
}
