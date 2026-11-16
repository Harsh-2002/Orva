package secrets

import (
	"context"
	"errors"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// PreparedArtifactSnapshot owns durable inputs, not pinned/validated code or
// runtime identity, policy approval, or proof of current state at publication.
type PreparedArtifactSnapshot struct {
	Artifact database.WorkloadArtifactRecord
	Function *database.Function
	Secrets  map[string]string
}

var errInvalidArtifactSnapshot = errors.New("invalid artifact snapshot")

// PrepareArtifactSnapshot runs outside IAM/pool locks. Eventual publication must
// revalidate the expected generation; legacy mutations do not maintain it yet.
func (m *Manager) PrepareArtifactSnapshot(ctx context.Context, ownerID string, limits SecretSnapshotLimits) (*PreparedArtifactSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m == nil || m.db == nil {
		return nil, errInvalidArtifactSnapshot
	}
	raw, err := m.db.GetWorkloadArtifactSnapshot(ctx, ownerID)
	if err != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errInvalidArtifactSnapshot
	}
	ownedSecrets, err := m.SnapshotForIAM(ownerID, raw.Secrets, limits)
	if err != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errInvalidArtifactSnapshot
	}
	prepared := &PreparedArtifactSnapshot{
		Artifact: raw.Artifact,
		Function: raw.Function.Clone(),
		Secrets:  ownedSecrets,
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return prepared, nil
}
