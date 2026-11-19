// Package workloadtrigger is an opt-in owner-scoped public-exposure writer.
package workloadtrigger

import (
	"context"
	"errors"
	"strconv"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type Service struct {
	db        *database.Database
	reg       *registry.Registry
	authority *workloadiam.Authority
}

func New(db *database.Database, reg *registry.Registry, a *workloadiam.Authority) (*Service, error) {
	if db == nil || reg == nil || a == nil {
		return nil, database.ErrExposureUnavailable
	}
	return &Service{db: db, reg: reg, authority: a}, nil
}

// Apply prepares bounded trusted reads before taking sorted owner locks.
func (s *Service) Apply(ctx context.Context, ref database.OperatorCredentialReference, c database.ExposureMutation) (map[string]any, error) {
	if s == nil || ctx == nil {
		return nil, database.ErrExposureUnavailable
	}
	p, err := s.db.PrepareExposure(ctx, c)
	if err != nil {
		return nil, err
	}
	return s.applyPrepared(ctx, ref, c, p)
}

func (s *Service) applyPrepared(ctx context.Context, ref database.OperatorCredentialReference, c database.ExposureMutation, p *database.ExposurePlan) (map[string]any, error) {
	owners := p.Owners()
	var row *database.InboundWebhook
	err := func() error {
		for _, id := range owners {
			s.reg.MutationLock(id).Lock()
		}
		defer func() {
			for i := len(owners) - 1; i >= 0; i-- {
				s.reg.MutationLock(owners[i]).Unlock()
			}
		}()
		return s.authority.MutateOwners(p.Metadata(), func() error {
			var e error
			row, e = s.db.CommitExposure(ctx, ref, p)
			return e
		})
	}()
	if errors.Is(err, workloadiam.ErrConflict) {
		err = database.ErrExposureConflict
	}
	if err != nil {
		return nil, err
	}
	if s.reg.PublishEvent != nil {
		for _, id := range owners {
			s.reg.PublishEvent("function", map[string]string{"action": "updated", "id": id})
		}
	}
	out := map[string]any{}
	switch c.Operation {
	case "route_set":
		out["status"], out["path"], out["function_id"] = "saved", c.Key, c.FunctionID
	case "route_delete":
		out["status"], out["path"] = "deleted", c.Key
	case "inbound_create":
		out["inbound_webhook"], out["secret"], out["trigger_url"] = row, row.Secret, "/webhook/"+row.ID
	case "inbound_update":
		out["inbound_webhook"] = row
	case "inbound_delete":
		out["status"], out["id"] = "deleted", c.Key
	}
	return out, nil
}

// ListRoutes is the bounded READ-authorized mapping list shared by REST, MCP and AI.
func (s *Service) ListRoutes(ctx context.Context) ([]database.RouteExposureRow, bool, error) {
	if s == nil {
		return nil, false, database.ErrExposureUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return nil, false, database.ErrOperatorDenied
	}
	return s.db.ListRouteExposures(ctx, ref)
}

// ListInbound lists one exact-UUID owner's triggers with its CAS expectations.
func (s *Service) ListInbound(ctx context.Context, owner string) ([]database.InboundExposureRow, map[string]database.ExposureOwnerExpected, bool, error) {
	if s == nil {
		return nil, nil, false, database.ErrExposureUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return nil, nil, false, database.ErrOperatorDenied
	}
	return s.db.ListInboundExposures(ctx, ref, owner)
}

// Execute is the common strict boundary for REST, external MCP and shared AI.
func (s *Service) Execute(ctx context.Context, op string, raw []byte) (map[string]any, error) {
	if s == nil {
		return nil, database.ErrExposureUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return nil, database.ErrOperatorDenied
	}
	if op == "inspect" {
		kind, key, owner, err := decodeInspect(raw)
		if err != nil {
			return nil, err
		}
		if kind == "inbound" && key == "" {
			key = database.NewInboundWebhookID()
		}
		out, err := s.db.InspectExposure(ctx, ref, kind, key, owner)
		if err != nil {
			return nil, err
		}
		return map[string]any{"version": strconv.FormatUint(out.Version, 10), "expected": out.Expected, "route": out.Route, "inbound_webhook": out.InboundWebhook}, nil
	}
	c, err := Decode(op, raw)
	if err != nil {
		return nil, err
	}
	return s.Apply(ctx, ref, c)
}
