package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/config"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func applyManagedEnrollment(ctx context.Context, root string, lock *servingmode.Lock, scratch string, plan *enrollmentPlan,
	skipped map[string]bool, step func(string) error,
) error {
	raw, err := enrollmentPlanBytes(plan)
	if err != nil {
		return err
	}
	required := make(map[string]bool)
	count := 0
	for _, fn := range plan.Functions {
		if !skipped[fn.ID] {
			required[fn.Runtime] = true
			count++
		}
	}
	limits := managedOwnerConfig(root).CodeLimits
	maxCurrent := limits.Bytes / managedOutputConfig(root, limits).ReservedBytes
	if int64(count) > maxCurrent {
		return fmt.Errorf("sealed store can reserve at most %d current builds; explicitly exclude more functions", maxCurrent)
	}
	runtimes := []string{}
	for _, lang := range []string{"node", "python"} {
		path := filepath.Join(root, "rootfs", lang)
		resolved, err := filepath.EvalSymlinks(path)
		if errors.Is(err, os.ErrNotExist) && !required[lang] {
			continue
		}
		if err != nil || resolved != path {
			return errors.Join(fmt.Errorf("installed %s runtime must be a local rootfs", lang), err)
		}
		if err := sandbox.CheckManagedRuntime(path, sandbox.Language(lang)); err != nil {
			return fmt.Errorf("validate installed %s runtime: %w", lang, err)
		}
		runtimes = append(runtimes, lang)
	}
	nsjail := config.Defaults().Sandbox.NsjailBin
	if len(required) != 0 {
		if st, err := os.Stat(nsjail); err != nil || !st.Mode().IsRegular() || st.Mode()&0111 == 0 {
			return errors.New("the installed nsjail executable is required before enrollment")
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	intent, err := lock.BeginEnrollment(digest)
	if err != nil {
		return err
	}
	if err := step("intent"); err != nil {
		return err
	}
	record := intent.Record()
	dbPlan := database.LegacyEnrollment{Owner: record.Owner, Operation: record.Operation, PlanDigest: digest, Runtimes: make(map[string]string)}
	if err := produceEnrollment(ctx, root, scratch, nsjail, record, runtimes, plan, skipped, &dbPlan, step); err != nil {
		return fmt.Errorf("enrollment incomplete: %w", err)
	}
	if err := commitEnrollment(ctx, root, dbPlan); err != nil {
		return fmt.Errorf("enrollment incomplete: %w", err)
	}
	if err := step("database_committed"); err != nil {
		return err
	}
	return intent.Publish()
}

func produceEnrollment(ctx context.Context, root, scratch, nsjail string, record servingmode.Record, runtimes []string,
	plan *enrollmentPlan, skipped map[string]bool, dbPlan *database.LegacyEnrollment, step func(string) error,
) (err error) {
	owner, budget, err := openManagedBootOwner(ctx, root, record, true)
	if err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), managedShutdownTimeout)
		defer cancel()
		err = errors.Join(err, owner.Close(stop), budget.Close())
	}()
	if err := step("owner_provisioned"); err != nil {
		return err
	}
	assets, err := owner.Bindings(ctx)
	if err != nil {
		return err
	}
	for _, lang := range runtimes {
		runtime, err := artifactfs.SealInstalledRuntime(ctx, assets.Runtime, lang, filepath.Join(root, "rootfs", lang))
		if err != nil {
			return fmt.Errorf("seal installed %s runtime: %w", lang, err)
		}
		lease, err := assets.Runtime.Acquire(runtime.Generation)
		if err != nil {
			return err
		}
		err = sandbox.CheckManagedRuntime(lease.Path(), sandbox.Language(lang))
		lease.Release()
		if err != nil {
			return fmt.Errorf("validate sealed %s runtime: %w", lang, err)
		}
		dbPlan.Runtimes[lang] = runtime.Generation
		if err := step("runtime_sealed"); err != nil {
			return err
		}
	}
	outputConfig := managedOutputConfig(root, managedOwnerConfig(root).CodeLimits)
	outputConfig.CodeStore, outputConfig.ParentDomain = assets.Code, assets.Domain
	output, err := builder.NewOutputOwner(outputConfig)
	if err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = errors.Join(err, output.RetryCleanup(stop), output.Close())
	}()
	build := builder.New(builder.WithManagedOutput(output), builder.WithRuntimeBinding(func(_ context.Context, req builder.RuntimeRequest) (builder.RuntimeBinding, error) {
		return builder.RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: assets.Runtime, Generation: dbPlan.Runtimes[string(req.Language)]}}, nil
	}))
	build.NsjailBin, build.DataDir = nsjail, root
	snapshot, err := database.New(filepath.Join(scratch, "snapshot.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, snapshot.Close()) }()
	for _, fn := range plan.Functions {
		e := database.LegacyEnrollmentFunction{
			ID: fn.ID, Generation: ids.New(), LegacyHash: fn.CodeHash, Revision: fn.Revision,
			LegacySDK: fn.SDK, DeploymentID: fn.deployment, Excluded: skipped[fn.ID],
		}
		if !e.Excluded {
			if err := buildEnrollmentFunction(ctx, root, scratch, build, assets, snapshot, fn, &e); err != nil {
				return fmt.Errorf("rebuild enrolled function %s: %w", fn.ID, err)
			}
			if err := step("code_sealed"); err != nil {
				return err
			}
		}
		dbPlan.Functions = append(dbPlan.Functions, e)
	}
	return nil
}

func buildEnrollmentFunction(ctx context.Context, root, scratch string, build *builder.Builder, assets artifactfs.OwnerBindings,
	snapshot *database.Database, entry enrollmentFunction, e *database.LegacyEnrollmentFunction,
) error {
	fn, err := snapshot.GetFunction(e.ID)
	if err != nil {
		return err
	}
	built, err := build.Build(ctx, fn, entry.archive(scratch))
	if err != nil {
		return err
	}
	// Keep the legacy deployment hash for operator history; the independently
	// verified concrete artifacts have their own generations and manifest digests.
	e.Build = database.WorkloadBuildOutput{
		CodeGeneration: built.CodeArtifact.Generation, CodeDigest: built.CodeArtifact.Digest,
		RuntimeGeneration: built.RuntimeArtifact.Generation, RuntimeDigest: built.RuntimeArtifact.Digest,
		CodeHash: e.LegacyHash, Entrypoint: built.Entrypoint,
	}
	rows, err := snapshot.ListSecrets(fn.ID)
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		f, err := openEnrollmentFile(filepath.Join(root, ".master.key"))
		if err != nil {
			return errors.New("legacy secrets have no safe master key")
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	plain, err := secrets.New(snapshot, root).SnapshotForIAM(fn.ID, rows, secrets.SecretSnapshotLimits{
		MaxSecrets: secrets.MaxIAMSecretSnapshotCount, MaxTotalBytes: secrets.MaxIAMSecretSnapshotBytes,
	})
	if err != nil {
		return err
	}
	fn.SDK = true // Managed transport is not a grant; original SDK state is recorded separately.
	factory, err := workloadspawn.NewFactory(ctx, &secrets.PreparedArtifactSnapshot{
		Artifact: database.WorkloadArtifactRecord{OwnerID: fn.ID, Generation: e.Generation, Revision: 1, Epoch: 1},
		Function: fn, Secrets: plain,
	}, assets.Code, e.Build.CodeGeneration, assets.Runtime, e.Build.RuntimeGeneration,
		workloadspawn.FactoryLimits{MaxPreparedPlans: 16, MaxPreparedBytes: 64 << 20, MaxSnapshotBytes: 64 << 20})
	if err != nil {
		return err
	}
	defer factory.Close()
	if code, runtime := factory.Identities(); code != built.CodeArtifact || runtime != built.RuntimeArtifact {
		return artifactfs.ErrIntegrity
	}
	if !e.LegacySDK {
		e.ApprovedArtifact = factory.ArtifactRevision()
	}
	return nil
}

func commitEnrollment(ctx context.Context, root string, plan database.LegacyEnrollment) (err error) {
	db, err := database.New(filepath.Join(root, "orva.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if err := db.CommitLegacyEnrollment(ctx, plan); err != nil {
		return err
	}
	var busy, pages, checkpointed int
	err = db.WriteDB().QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &pages, &checkpointed)
	if err != nil || busy != 0 || pages != checkpointed {
		return errors.Join(errors.New("legacy SQLite checkpoint incomplete"), err)
	}
	return nil
}
