package workloadpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

type ManagedHostDTO struct {
	Revision         string  `json:"revision"`
	AuthorityEpoch   string  `json:"authority_epoch"`
	CurrentArtifact  *string `json:"current_artifact"`
	ApprovedArtifact *string `json:"approved_artifact"`
	Quarantined      bool    `json:"quarantined"`
	ReviewRequired   bool    `json:"review_required"`
	CanManage        bool    `json:"can_manage"`
	Enforcement      string  `json:"enforcement"`
}
type ManagedAttachmentDTO struct {
	PolicyID string          `json:"policy_id"`
	Name     string          `json:"name"`
	Version  string          `json:"version"`
	Enabled  bool            `json:"enabled"`
	Revoked  bool            `json:"revoked"`
	Policy   json.RawMessage `json:"policy"`
}
type FunctionSourcesDTO struct {
	Function        snapshotFunction       `json:"function"`
	Host            ManagedHostDTO         `json:"host"`
	InlinePolicy    json.RawMessage        `json:"inline_policy"`
	EffectivePolicy json.RawMessage        `json:"effective_policy"`
	Attachments     []ManagedAttachmentDTO `json:"attachments"`
	History         []snapshotAudit        `json:"history"`
}
type ManagedPolicyDTO struct {
	CanManage   bool                        `json:"can_manage"`
	Policy      *database.IAMManagedPolicy  `json:"policy"`
	Version     any                         `json:"version"`
	Attachments []database.IAMAttachedOwner `json:"attachments"`
	History     []ManagedAuditDTO           `json:"history"`
}

type ManagedAuditDTO struct {
	Sequence  string `json:"sequence"`
	Operation string `json:"operation"`
	Digest    string `json:"digest"`
	Actor     struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"actor"`
	ChangedAt string `json:"changed_at"`
}

func (s *Service) inspectSources(ctx context.Context, ref auth.OperatorReference, id string) (*FunctionSourcesDTO, error) {
	lock := s.registry.MutationLock(id)
	lock.Lock()
	defer lock.Unlock()
	src, err := s.db.GetIAMSources(ctx, id)
	if err != nil {
		return nil, err
	}
	fn, err := s.db.GetFunction(id)
	if err != nil {
		return nil, database.ErrIAMFunctionNotFound
	}
	history, err := s.db.ListIAMPolicyAudit(ctx, id)
	if err != nil {
		return nil, err
	}
	var state ArtifactState
	if err := s.authority.ChangeHostState(func() {
		if s.observe != nil {
			state = s.observe(id)
		}
	}); err != nil {
		return nil, err
	}
	var current *string
	if state.Known && state.OwnerID == id {
		current = artifactPointer(state.Artifact)
	}
	r := src.Record
	enforcement := "scratch_opt_in"
	if s.serving {
		enforcement = "managed"
	}
	out := &FunctionSourcesDTO{Function: snapshotFunction{ID: id, Name: fn.Name}, InlinePolicy: json.RawMessage(src.InlineJSON), EffectivePolicy: json.RawMessage(r.PolicyJSON),
		Host: ManagedHostDTO{Revision: strconv.FormatUint(r.Revision, 10), AuthorityEpoch: strconv.FormatUint(r.AuthorityEpoch, 10), CurrentArtifact: current, ApprovedArtifact: artifactPointer(r.ApprovedArtifact),
			Quarantined: r.Quarantined, ReviewRequired: r.ReviewRequired, CanManage: s.db.CheckIAMOperator(ctx, ref, true) == nil, Enforcement: enforcement},
		Attachments: []ManagedAttachmentDTO{}, History: []snapshotAudit{}}
	for _, a := range src.Attachments {
		out.Attachments = append(out.Attachments, ManagedAttachmentDTO{PolicyID: a.PolicyID, Name: a.Name, Version: strconv.FormatUint(a.Version, 10), Enabled: a.Enabled, Revoked: a.Revoked, Policy: json.RawMessage(a.PolicyJSON)})
	}
	for _, a := range history {
		e := snapshotAudit{Revision: strconv.FormatUint(a.Revision, 10), AuthorityEpoch: strconv.FormatUint(a.AuthorityEpoch, 10), Digest: a.Digest, ChangedAt: a.ChangedAt.UTC().Format(time.RFC3339Nano)}
		e.Actor.Kind, e.Actor.ID = a.Actor.Kind, a.Actor.ID
		out.History = append(out.History, e)
	}
	return out, nil
}

// ExecuteManaged is the raw management boundary used identically by REST, MCP
// and the in-process agent. Authentication references must come from the host.
func (s *Service) ExecuteManaged(ctx context.Context, op, id string, body []byte) (any, error) {
	if s == nil || !s.managed {
		return nil, ErrUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return nil, database.ErrOperatorDenied
	}
	read := op == "list" || op == "get" || op == "get_function"
	if err := s.db.CheckIAMOperator(ctx, ref, !read); err != nil {
		return nil, err
	}
	m, err := managementObject(body)
	if err != nil {
		return nil, err
	}
	switch op {
	case "list":
		if !exactFields(m, nil, "offset") {
			return nil, database.ErrIAMInvalidPolicy
		}
		var offset uint64
		if m["offset"] != nil {
			offset, err = decimal(m["offset"], true)
			if err != nil || offset > mathMaxOffset {
				return nil, database.ErrIAMInvalidPolicy
			}
		}
		items, err := s.db.ListIAMManagedPolicies(ctx, int(offset))
		if err != nil {
			return nil, err
		}
		return struct {
			Items     []database.IAMManagedPolicy `json:"items"`
			Offset    string                      `json:"offset"`
			CanManage bool                        `json:"can_manage"`
		}{items, strconv.FormatUint(offset, 10), s.db.CheckIAMOperator(ctx, ref, true) == nil}, nil
	case "get":
		if !exactFields(m, nil, "version", "history") {
			return nil, database.ErrIAMInvalidPolicy
		}
		if m["history"] != nil && string(m["history"]) != "true" && string(m["history"]) != "false" {
			return nil, database.ErrIAMInvalidPolicy
		}
		p, err := s.db.GetIAMManagedPolicy(ctx, id)
		if err != nil {
			return nil, err
		}
		version := p.LatestVersion
		if m["version"] != nil {
			version, err = decimal(m["version"], false)
			if err != nil {
				return nil, err
			}
		}
		v, err := s.db.GetIAMManagedVersion(ctx, id, version)
		if err != nil {
			return nil, err
		}
		owners, err := s.db.ListIAMAttachedOwners(ctx, id)
		if err != nil {
			return nil, err
		}
		history := []ManagedAuditDTO{}
		if string(m["history"]) == "true" {
			rows, e := s.db.ListIAMManagedAudit(ctx, id)
			err = e
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				a := ManagedAuditDTO{Sequence: strconv.FormatUint(row.Sequence, 10), Operation: row.Operation, Digest: row.Digest, ChangedAt: row.ChangedAt.UTC().Format(time.RFC3339Nano)}
				a.Actor.Kind, a.Actor.ID = row.Actor.Kind, row.Actor.ID
				history = append(history, a)
			}
		}
		return ManagedPolicyDTO{CanManage: s.db.CheckIAMOperator(ctx, ref, true) == nil, Policy: p, Version: struct {
			Number    string          `json:"number"`
			Policy    json.RawMessage `json:"policy"`
			CreatedAt string          `json:"created_at"`
		}{strconv.FormatUint(v.Version, 10), v.Policy, v.CreatedAt.UTC().Format(time.RFC3339Nano)}, Attachments: owners, History: history}, nil
	case "get_function":
		if !exactFields(m, nil) {
			return nil, database.ErrIAMInvalidPolicy
		}
		return s.inspectSources(ctx, ref, id)
	case "impact":
		raw := m["operation"]
		delete(m, "operation")
		var operation string
		if json.Unmarshal(raw, &operation) != nil || (operation != "attach" && operation != "detach" && operation != "revoke") {
			return nil, database.ErrIAMInvalidPolicy
		}
		canonical, _ := json.Marshal(m)
		r, err := DecodeManagedRequest(operation, id, canonical)
		if err != nil {
			return nil, err
		}
		r.RequireRead = true
		plan, err := s.db.PrepareIAMManagedMutation(ctx, ref, r)
		if err != nil {
			return nil, err
		}
		type impact struct {
			FunctionID string          `json:"function_id"`
			Before     json.RawMessage `json:"before"`
			After      json.RawMessage `json:"after"`
			Reduction  bool            `json:"reduction"`
		}
		items := []impact{}
		for _, c := range plan.Publications() {
			items = append(items, impact{c.Expected.OwnerID, json.RawMessage(c.Before.PolicyJSON), json.RawMessage(c.After.PolicyJSON), c.Reduction})
		}
		return struct {
			Owners      []impact `json:"owners"`
			PreviewOnly bool     `json:"preview_only"`
		}{items, true}, nil
	default:
		r, err := DecodeManagedRequest(op, id, body)
		if err != nil {
			return nil, err
		}
		r.RequireRead = true
		result, err := s.MutateManaged(ctx, ref, r)
		if err != nil {
			return nil, err
		}
		if op == "inline" || op == "review" {
			return s.inspectSources(ctx, ref, id)
		}
		owners := make([]string, 0, len(result.Owners))
		for _, r := range result.Owners {
			owners = append(owners, r.OwnerID)
		}
		return struct {
			Policy         *database.IAMManagedPolicy `json:"policy"`
			AffectedOwners []string                   `json:"affected_owners"`
		}{result.Policy, owners}, nil
	}
}

const mathMaxOffset = 1_000_000

// ManagedHTTPAdapter is explicit fixture-only wiring; normal routing stays off.
type ManagedHTTPAdapter struct {
	Service      *Service
	Authenticate func(*http.Request) (*auth.Principal, error)
}

func (h *ManagedHTTPAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.Service == nil || h.Authenticate == nil {
		policyHTTPError(w, ErrUnavailable)
		return
	}
	principal, err := h.Authenticate(r)
	if err != nil || principal == nil || principal.Kind == auth.KindChannel {
		respond.Error(w, 401, "UNAUTHORIZED", "Operator authentication required", "")
		return
	}
	ctx := auth.WithOperator(r.Context(), principal)
	op, id := r.PathValue("operation"), r.PathValue("id")
	if op == "" {
		if strings.Contains(r.URL.Path, "/functions/") {
			op = "get_function"
			if r.Method == "PUT" {
				op = "inline"
			}
		} else {
			op = "get"
			if id == "" {
				op = "list"
				if r.Method == "POST" {
					op = "create"
				}
			}
		}
	}
	if r.Method == "POST" && id != "" {
		allowed := op == "update" || op == "impact" || op == "attach" || op == "detach" || op == "revoke" || op == "delete"
		if strings.Contains(r.URL.Path, "/functions/") {
			allowed = op == "review"
		}
		if !allowed {
			w.WriteHeader(405)
			return
		}
	}
	if (r.Method == "GET" && op != "get" && op != "list" && op != "get_function") ||
		(r.Method == "PUT" && op != "inline") ||
		(r.Method != "GET" && r.Method != "PUT" && r.Method != "POST") {
		w.WriteHeader(405)
		return
	}
	body := []byte("{}")
	if r.Method == "GET" {
		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) != 1 {
				policyHTTPError(w, database.ErrIAMInvalidPolicy)
				return
			}
			query[key] = values[0]
		}
		m := map[string]any{}
		for key, value := range query {
			if key == "history" {
				if value != "true" && value != "false" {
					policyHTTPError(w, database.ErrIAMInvalidPolicy)
					return
				}
				m[key] = value == "true"
			} else {
				m[key] = value
			}
		}
		body, _ = json.Marshal(m)
	} else {
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, MaxManagementBytes))
		if err != nil {
			policyHTTPError(w, database.ErrIAMInvalidPolicy)
			return
		}
	}
	result, err := h.Service.ExecuteManaged(ctx, op, id, body)
	if err != nil {
		if errors.Is(err, database.ErrIAMPolicyNotFound) || errors.Is(err, database.ErrIAMFunctionNotFound) {
			respond.Error(w, 404, "NOT_FOUND", "Policy or function unavailable", "")
			return
		}
		policyHTTPError(w, err)
		return
	}
	respond.JSON(w, 200, result)
}

func (h *ManagedHTTPAdapter) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/iam/policies", h)
	mux.Handle("POST /api/v1/iam/policies", h)
	mux.Handle("GET /api/v1/iam/policies/{id}", h)
	mux.Handle("POST /api/v1/iam/policies/{id}/{operation}", h)
	mux.Handle("GET /api/v1/functions/{id}/permissions", h)
	mux.Handle("PUT /api/v1/functions/{id}/permissions", h)
	mux.Handle("POST /api/v1/functions/{id}/permissions/{operation}", h)
}
