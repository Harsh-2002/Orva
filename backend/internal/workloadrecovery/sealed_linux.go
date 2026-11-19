//go:build linux

package workloadrecovery

import (
	"context"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
)

// PrepareSealedDeployment reconstructs an inactive Factory and deny evaluator
// during admission-closed startup. It never installs or approves either.
func PrepareSealedDeployment(ctx context.Context, db *database.Database, sm *secrets.Manager,
	owner *artifactfs.Owner, deployment string, limits workloadspawn.FactoryLimits,
) (*workloadspawn.Factory, *workloadiam.Evaluator, error) {
	return prepareDeployment(ctx, db, sm, owner, deployment, limits, false)
}

// PrepareCurrentDeployment is for admission-closed process startup after
// recovery. Unquarantined approval is valid only for the exact live pair.
func PrepareCurrentDeployment(ctx context.Context, db *database.Database, sm *secrets.Manager,
	owner *artifactfs.Owner, deployment string, limits workloadspawn.FactoryLimits,
) (*workloadspawn.Factory, *workloadiam.Evaluator, error) {
	return prepareDeployment(ctx, db, sm, owner, deployment, limits, true)
}

func prepareDeployment(ctx context.Context, db *database.Database, sm *secrets.Manager,
	owner *artifactfs.Owner, deployment string, limits workloadspawn.FactoryLimits, current bool,
) (*workloadspawn.Factory, *workloadiam.Evaluator, error) {
	if ctx == nil || db == nil || sm == nil || owner == nil {
		return nil, nil, ErrClosed
	}
	bindings, err := owner.Bindings(ctx)
	if err != nil {
		return nil, nil, err
	}
	plan, err := db.PrepareWorkloadDeploymentInstallation(ctx, deployment)
	if err != nil {
		return nil, nil, err
	}
	policy := plan.Policy()
	denied := policy.Quarantined || policy.ReviewRequired
	if !denied && !current {
		return nil, nil, ErrClosed
	}
	evaluator, err := policy.AdmissionEvaluator()
	if err != nil {
		return nil, nil, err
	}
	raw, output := plan.Snapshot(), plan.Output()
	fn := raw.Function
	entrypoint := fn.RunEntrypoint
	if entrypoint == "" {
		entrypoint = fn.Entrypoint
	}
	if fn.ID != policy.OwnerID || fn.ActiveDeploymentID != deployment ||
		fn.CodeHash != output.CodeHash || entrypoint != output.Entrypoint {
		return nil, nil, artifactfs.ErrIntegrity
	}
	plain, err := sm.SnapshotForIAM(fn.ID, raw.Secrets,
		secrets.SecretSnapshotLimits{MaxSecrets: secrets.MaxIAMSecretSnapshotCount, MaxTotalBytes: secrets.MaxIAMSecretSnapshotBytes})
	if err != nil {
		return nil, nil, err
	}
	paths, err := artifactfs.PreparePaths(bindings.Code, output.CodeGeneration, bindings.Runtime, output.RuntimeGeneration)
	if err != nil {
		return nil, nil, err
	}
	defer paths.Release()
	code, runtime := paths.Identities()
	if code.Owner != fn.ID || runtime.Owner != fn.Runtime || code.Digest != output.CodeDigest || runtime.Digest != output.RuntimeDigest {
		return nil, nil, artifactfs.ErrIntegrity
	}
	factory, err := workloadspawn.NewFactory(ctx, &secrets.PreparedArtifactSnapshot{
		Artifact: raw.Artifact, Function: fn, Secrets: plain,
	}, bindings.Code, output.CodeGeneration, bindings.Runtime, output.RuntimeGeneration, limits)
	if err != nil {
		return nil, nil, err
	}
	actualCode, actualRuntime := factory.Identities()
	if actualCode != code || actualRuntime != runtime {
		factory.Close()
		return nil, nil, artifactfs.ErrIntegrity
	}
	if !denied && policy.ApprovedArtifact != factory.ArtifactRevision() {
		factory.Close()
		return nil, nil, ErrClosed
	}
	return factory, evaluator, nil
}
