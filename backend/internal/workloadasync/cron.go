package workloadasync

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/robfig/cron/v3"
)

var ErrCronInput = errors.New("invalid cron configuration")
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseCronExpr shares exactly the legacy scheduler grammar.
func ParseCronExpr(expr string) (cron.Schedule, error) { return cronParser.Parse(expr) }

func NextCron(c *database.AsyncCron, now time.Time) (time.Time, error) {
	if c == nil || len(c.CronExpr) > 1024 || len(c.Timezone) > 256 {
		return time.Time{}, ErrCronInput
	}
	s, err := ParseCronExpr(c.CronExpr)
	if err != nil {
		return time.Time{}, ErrCronInput
	}
	tz := c.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, ErrCronInput
	}
	next := s.Next(now.In(loc)).UTC()
	if !next.After(now) {
		return time.Time{}, ErrCronInput
	}
	return next, nil
}
func prepareCron(c *database.AsyncCron) error {
	if c == nil {
		return ErrCronInput
	}
	c.CronExpr = strings.TrimSpace(c.CronExpr)
	c.Name = strings.TrimSpace(c.Name)
	c.Timezone = strings.TrimSpace(c.Timezone)
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if c.Payload == "" {
		c.Payload = "{}"
	}
	next, err := NextCron(c, time.Now().UTC())
	if err != nil {
		return err
	}
	c.NextRunAt = nil
	if c.Enabled {
		c.NextRunAt = &next
	}
	return nil
}
func (s *Service) cronReady() bool {
	return s != nil && s.DB != nil && s.Authority != nil && s.CurrentArtifact != nil
}

func (s *Service) UpsertCronFunction(ctx context.Context, observe func() workloadiam.Context, c *database.AsyncCron) error {
	if !s.cronReady() || observe == nil || c == nil || c.CreatorID != c.FunctionID {
		return workloadiam.ErrDenied
	}
	if err := prepareCron(c); err != nil {
		return err
	}
	r := workloadiam.Request{Operation: workloadiam.OpCronsUpsert, FunctionID: c.CreatorID,
		Resource: workloadiam.Resource{OwnCron: &workloadiam.CronResource{Name: &c.Name, TargetID: c.FunctionID}}}
	return s.Authority.DoAsyncCreateObserved(func() workloadiam.Context {
		state := observe()
		if ctx.Err() != nil || state.Principal.FunctionID != c.CreatorID || s.CurrentArtifact(c.CreatorID) != state.Principal.ArtifactRevision {
			return workloadiam.Context{}
		}
		return state
	}, func() string { return s.CurrentArtifact(c.FunctionID) }, r, func(revs workloadiam.AsyncRevisions) error {
		c.OriginKind = "function"
		c.OperatorKind = ""
		c.OperatorID = ""
		return s.DB.SaveAsyncCron(ctx, c, nil, revs, "upsert")
	})
}
func (s *Service) CreateCronOperator(ctx context.Context, ref database.OperatorCredentialReference, c *database.AsyncCron) error {
	if !s.cronReady() || c == nil {
		return workloadiam.ErrDenied
	}
	if err := prepareCron(c); err != nil {
		return err
	}
	o := workloadiam.AsyncOrigin{Kind: "operator", OperatorKind: ref.Kind, OperatorID: ref.ID, TargetID: c.FunctionID, Trigger: workloadiam.Cron}
	return s.Authority.DoStandingObserved(o, func() workloadiam.StandingState { return s.standing(o) }, func(revs workloadiam.AsyncRevisions) error {
		return s.DB.SaveAsyncCron(ctx, c, &ref, revs, "create")
	})
}

// MutateCron preserves original origin, with caller-supplied CAS expectations.
func (s *Service) MutateCron(ctx context.Context, ref database.OperatorCredentialReference, expected *database.AsyncCron, action string) error {
	if !s.cronReady() || expected == nil {
		return workloadiam.ErrDenied
	}
	current, err := s.DB.GetAsyncCron(ctx, expected.ID)
	if err != nil {
		return err
	}
	if current.FunctionID != expected.FunctionID || current.ConfigRevision != expected.ConfigRevision || current.AuthorityGeneration != expected.AuthorityGeneration {
		return database.ErrAsyncConflict
	}
	if action == "disable" || action == "delete" {
		return s.DB.SaveAsyncCron(ctx, current, &ref, workloadiam.AsyncRevisions{Target: 1}, action)
	}
	if action != "update" && action != "resume" {
		return database.ErrAsyncConflict
	}
	if action == "update" {
		current.CronExpr = expected.CronExpr
		current.Timezone = expected.Timezone
		current.Payload = expected.Payload
		current.Enabled = expected.Enabled
	} else {
		current.Enabled = true
	}
	if err := prepareCron(current); err != nil {
		return err
	}
	o := current.Origin()
	o.ExpectedCreatorRevision = 0
	o.ExpectedTargetRevision = 0
	// Edits cannot silently rebind suspended/revoked work; only Resume can.
	if action != "resume" {
		o = current.Origin()
	}
	return s.Authority.DoStandingObserved(o, func() workloadiam.StandingState { return s.standing(o) }, func(revs workloadiam.AsyncRevisions) error {
		return s.DB.SaveAsyncCron(ctx, current, &ref, revs, action)
	})
}
func (s *Service) BeginCron(ctx context.Context, c *database.AsyncCron) (*workloadiam.AsyncAdmission, error) {
	if !s.cronReady() || c == nil || !c.Enabled || c.AuthorityState != "active" || c.AdmissionTargetRevision == 0 ||
		(c.OriginKind == "function" && c.AdmissionCallerRevision == 0) {
		return nil, workloadiam.ErrDenied
	}
	o := c.Origin()
	return s.Authority.BeginAsyncObserved(ctx, o, func() workloadiam.StandingState { return s.standing(o) })
}
func (s *Service) ClaimCron(ctx context.Context, c *database.AsyncCron, now time.Time) error {
	if !s.cronReady() {
		return workloadiam.ErrDenied
	}
	next, err := NextCron(c, now)
	if err != nil {
		return err
	}
	o := c.Origin()
	return s.Authority.DoStandingObserved(o, func() workloadiam.StandingState { return s.standing(o) }, func(workloadiam.AsyncRevisions) error {
		return s.DB.AdvanceAsyncCron(ctx, c, now, next)
	})
}
func (s *Service) AdmitCron(ctx context.Context, c *database.AsyncCron, call *workloadiam.AsyncAdmission, observe func() workloadiam.TargetState, at time.Time) error {
	if !s.cronReady() || c == nil || observe == nil {
		return workloadiam.ErrDenied
	}
	return call.AdmitObserved(func() (workloadiam.StandingState, workloadiam.TargetState) { return s.standing(c.Origin()), observe() },
		func() error { return s.DB.AdmitAsyncCron(ctx, c, at) })
}
