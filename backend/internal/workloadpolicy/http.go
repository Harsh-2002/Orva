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
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/google/uuid"
)

// HTTPAdapter is deliberately unregistered. Authenticate must resolve a live
// credential, not trust identity, capabilities or actor fields from the request.
type HTTPAdapter struct {
	Service      *Service
	Authenticate func(*http.Request) (*auth.Principal, error)
	// Targets returns only metadata visible to this authenticated operator.
	// It runs outside admission; it is advisory picker data, never authority.
	Targets func(context.Context, *auth.Principal) ([]FunctionTarget, error)
}

type FunctionTarget struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type snapshotTarget struct {
	FunctionTarget
	State string `json:"state"`
}
type snapshotFunction struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type snapshotCapabilities struct {
	CanRead               bool `json:"canRead"`
	CanManage             bool `json:"canManage"`
	CanReview             bool `json:"canReview"`
	CanEditPrivilegedCode bool `json:"canEditPrivilegedCode"`
}
type snapshotHost struct {
	Revision         string               `json:"revision"`
	AuthorityEpoch   string               `json:"authorityEpoch"`
	CurrentArtifact  *string              `json:"currentArtifact"`
	ApprovedArtifact *string              `json:"approvedArtifact"`
	ReviewRequired   bool                 `json:"reviewRequired"`
	Quarantined      bool                 `json:"quarantined"`
	Enforcement      string               `json:"enforcement"`
	Capabilities     snapshotCapabilities `json:"capabilities"`
}
type snapshotAudit struct {
	Revision       string `json:"revision"`
	AuthorityEpoch string `json:"authorityEpoch"`
	Digest         string `json:"digest"`
	Actor          struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"actor"`
	ChangedAt string `json:"changedAt"`
}
type policySnapshot struct {
	Function snapshotFunction `json:"function"`
	Host     snapshotHost     `json:"host"`
	Policy   json.RawMessage  `json:"policy"`
	Targets  []snapshotTarget `json:"targets"`
	History  []snapshotAudit  `json:"history"`
}

func artifactPointer(s string) *string {
	if !digest(s) {
		return nil
	}
	return &s
}

func (h *HTTPAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.Service == nil || h.Service.db == nil || h.Service.registry == nil ||
		h.Service.authority == nil || h.Authenticate == nil {
		respond.Error(w, 503, "IAM_UNAVAILABLE", "Permissions unavailable", "")
		return
	}
	p, err := h.Authenticate(r)
	if err != nil || p == nil || p.Kind == auth.KindChannel {
		respond.Error(w, 401, "UNAUTHORIZED", "Operator authentication required", "")
		return
	}
	ctx := auth.WithOperator(r.Context(), p)
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok || !p.Perms.Has("read") {
		respond.Error(w, 403, "FORBIDDEN", "Read permission required", "")
		return
	}
	owner := r.PathValue("id")
	u, err := uuid.Parse(owner)
	if err != nil || u.String() != owner || u.Version() < 1 || u.Version() > 8 || u.Variant() != uuid.RFC4122 {
		respond.Error(w, 400, "VALIDATION", "Canonical function UUID required", "")
		return
	}
	s := h.Service
	targets := []snapshotTarget{}
	if h.Targets != nil {
		rows, e := h.Targets(ctx, p)
		if e != nil || len(rows) > 256 {
			respond.Error(w, 503, "IAM_UNAVAILABLE", "Permissions targets unavailable", "")
			return
		}
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			id, e := uuid.Parse(row.ID)
			if e != nil || id.String() != row.ID || id == uuid.Nil || id.Version() < 1 || id.Version() > 8 ||
				id.Variant() != uuid.RFC4122 || seen[row.ID] || row.Label == "" || len(row.Label) > 1024 || !utf8.ValidString(row.Label) {
				respond.Error(w, 503, "IAM_UNAVAILABLE", "Permissions targets unavailable", "")
				return
			}
			seen[row.ID] = true
			targets = append(targets, snapshotTarget{FunctionTarget: row, State: "resolved"})
		}
	}
	if r.Method != http.MethodGet {
		if !p.Perms.Has("admin") {
			respond.Error(w, 403, "FORBIDDEN", "Admin permission required", "")
			return
		}
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxManagementBytes))
		if readErr != nil {
			respond.Error(w, 400, "VALIDATION", "Invalid permissions request", "")
			return
		}
		switch {
		case r.Method == http.MethodPut && !strings.HasSuffix(r.URL.Path, "/review"):
			replacement, e := DecodeReplacement(body)
			err = e
			if err == nil {
				_, err = s.Replace(ctx, ref, owner, replacement.Expected, replacement.PolicyJSON)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/review"):
			expected, e := DecodeReview(body)
			err = e
			if err == nil {
				_, err = s.Review(ctx, ref, owner, expected)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err != nil {
			policyHTTPError(w, err)
			return
		}
	}
	// A mutation acknowledgement is a new read, not a synthetic candidate.
	lock := s.registry.MutationLock(owner)
	lock.Lock()
	defer lock.Unlock()
	fn, err := s.db.GetFunction(owner)
	if err != nil {
		respond.Error(w, 404, "NOT_FOUND", "Function unavailable", "")
		return
	}
	record, err := s.db.GetIAMPolicy(ctx, owner)
	if err != nil {
		policyHTTPError(w, err)
		return
	}
	history, err := s.db.ListIAMPolicyAudit(ctx, owner)
	if err != nil {
		policyHTTPError(w, err)
		return
	}
	var state ArtifactState
	err = s.authority.ChangeHostState(func() {
		if s.observe != nil {
			state = s.observe(owner)
		}
	})
	if err != nil {
		policyHTTPError(w, err)
		return
	}
	current := (*string)(nil)
	if state.Known && state.OwnerID == owner {
		current = artifactPointer(state.Artifact)
	}
	out := policySnapshot{
		Function: snapshotFunction{ID: fn.ID, Name: fn.Name},
		Host: snapshotHost{Revision: strconv.FormatUint(record.Revision, 10), AuthorityEpoch: strconv.FormatUint(record.AuthorityEpoch, 10),
			CurrentArtifact: current, ApprovedArtifact: artifactPointer(record.ApprovedArtifact),
			ReviewRequired: record.ReviewRequired, Quarantined: record.Quarantined, Enforcement: "active",
			Capabilities: snapshotCapabilities{CanRead: true, CanManage: p.Perms.Has("admin"), CanReview: p.Perms.Has("admin")}},
		Policy: json.RawMessage(record.PolicyJSON), Targets: targets, History: []snapshotAudit{},
	}
	for _, row := range history {
		if len(out.History) == 32 {
			break
		}
		e := snapshotAudit{Revision: strconv.FormatUint(row.Revision, 10), AuthorityEpoch: strconv.FormatUint(row.AuthorityEpoch, 10),
			Digest: row.Digest, ChangedAt: row.ChangedAt.UTC().Format(time.RFC3339Nano)}
		e.Actor.Kind, e.Actor.ID = row.Actor.Kind, row.Actor.ID
		out.History = append(out.History, e)
	}
	respond.JSON(w, 200, out)
}

func policyHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, database.ErrOperatorDenied):
		respond.Error(w, 403, "FORBIDDEN", "Operator permission no longer valid", "")
	case errors.Is(err, database.ErrIAMConflict):
		respond.Error(w, 409, "IAM_CONFLICT", "Permissions changed; refresh before retrying", "")
	case errors.Is(err, database.ErrIAMInvalidPolicy), errors.Is(err, database.ErrIAMRevisionExhausted):
		respond.Error(w, 400, "VALIDATION", "Invalid permissions request", "")
	case errors.Is(err, ErrUnavailable):
		respond.Error(w, 409, "IAM_UNAVAILABLE", "Current artifact unavailable", "")
	default:
		respond.Error(w, 500, "INTERNAL", "Permissions unavailable", "")
	}
}
