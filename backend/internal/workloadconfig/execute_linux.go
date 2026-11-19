//go:build linux

package workloadconfig

import (
	"context"
	"strconv"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func publicationResult(out *database.WorkloadEnvironmentPublication) map[string]any {
	return map[string]any{
		"function_version":  strconv.Itoa(out.Function.Version),
		"artifact_revision": strconv.FormatUint(out.Artifact.Revision, 10),
		"artifact_epoch":    strconv.FormatUint(out.Artifact.Epoch, 10), "generation": out.Artifact.Generation,
		"policy_revision":   strconv.FormatUint(out.Policy.Revision, 10),
		"authority_epoch":   strconv.FormatUint(out.Policy.AuthorityEpoch, 10),
		"approved_artifact": out.Policy.ApprovedArtifact, "review_required": true,
	}
}

// Execute is shared by the optional external MCP and in-process AI adapters.
// The credential comes only from trusted authentication's private context key.
func (s *Service) Execute(ctx context.Context, operation, owner string, body []byte) (map[string]any, error) {
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return nil, database.ErrOperatorDenied
	}
	if operation == "inspect" {
		root, err := object(body)
		if err != nil || len(root) != 0 {
			return nil, database.ErrWorkloadArtifactInvalid
		}
		return s.Inspect(ctx, ref, owner)
	}
	expected, current, env, err := decodeRequest(body, operation == "install")
	var out *database.WorkloadEnvironmentPublication
	switch operation {
	case "environment":
		if err == nil {
			out, err = s.Environment(ctx, ref, owner, expected, current, env)
		}
	case "settings", "secret_upsert", "secret_delete":
		e, c, v, settings, secret, decodeErr := decodeExtendedRequest(body, operation == "settings", operation == "secret_delete")
		err = decodeErr
		if err == nil {
			if settings != nil {
				out, err = s.Settings(ctx, ref, owner, e, c, *settings, v)
			} else {
				out, err = s.Secret(ctx, ref, owner, e, c, secret.key, secret.value, operation == "secret_delete")
			}
		}
	case "install":
		if err == nil {
			artifact, e := s.Install(ctx, ref, owner, expected)
			if e != nil {
				return nil, e
			}
			return map[string]any{"current_artifact": artifact, "review_required": true}, nil
		}
	default:
		return nil, database.ErrWorkloadArtifactInvalid
	}
	if err != nil {
		return nil, err
	}
	return publicationResult(out), nil
}

func (s *Service) Inspect(ctx context.Context, ref database.OperatorCredentialReference, owner string) (map[string]any, error) {
	if s == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	if _, ok := s.binding(owner); !ok {
		return nil, ErrUnavailable
	}
	lock := s.reg.MutationLock(owner)
	lock.Lock()
	defer lock.Unlock()
	var out map[string]any
	var readErr error
	err := s.authority.ChangeHostState(func() {
		row, e := s.db.InspectWorkloadConfiguration(ctx, ref, owner)
		if e != nil {
			readErr = e
			return
		}
		out = map[string]any{"expected": row.Expected, "review_required": row.ReviewRequired, "quarantined": row.Quarantined,
			"environment_entries": row.EnvironmentEntries, "secret_entries": row.SecretEntries, "current_artifact": ""}
		if current := s.pool.CurrentFactory(owner); s.matchesBinding(owner, current) {
			code, runtime := current.Identities()
			out["current_artifact"], out["code_digest"], out["runtime_digest"] = current.ArtifactRevision(), code.Digest, runtime.Digest
		}
	})
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	return out, nil
}
