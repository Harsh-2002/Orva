package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/backend/internal/workloadrecovery"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/spf13/cobra"
)

func newManagedRestoreCmd() *cobra.Command {
	var archive, dataDir string
	var confirm, sameOwner bool
	cmd := &cobra.Command{
		Use: "restore", Short: "Verify a managed v2 archive; with --confirm, restore offline into a fresh local Owner",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := restoreManaged(cmd.Context(), dataDir, archive, confirm, sameOwner, func(string) error { return nil })
			if err != nil {
				return err
			}
			status := "Archive and destination preflight passed; no restore applied"
			if confirm {
				status = "Offline restore committed; review quarantined authority before re-enabling it"
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), status)
			return err
		},
	}
	cmd.Flags().StringVar(&archive, "archive", "", "managed v2 archive outside the instance data directory")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "existing offline instance data directory (default ORVA_DATA_DIR)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "apply an offline restore instead of only checking")
	cmd.Flags().BoolVar(&sameOwner, "same-owner", false, "use existing guarded recovery for this archive's original local Owner")
	_ = cmd.MarkFlagRequired("archive")
	return cmd
}

func restoreManaged(ctx context.Context, dir, archive string, confirm, sameOwner bool, step func(string) error) (err error) {
	root, err := managedSetupDataDir(dir)
	if err != nil {
		return err
	}
	path, err := enrollmentOutside(root, archive)
	if err != nil {
		return err
	}
	input, err := openEnrollmentFile(path)
	if err != nil {
		return err
	}
	defer input.Close()
	var lock *servingmode.Lock
	if confirm {
		lock, err = servingmode.Acquire(root)
	} else {
		lock, err = servingmode.AcquireForInspection(root)
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	cfg := managedOwnerConfig(root)
	if err := artifactfs.CheckProvisionedWitness(cfg.Host, root); err != nil {
		return fmt.Errorf("local witness: %w", err)
	}
	// Dry runs never write in the instance. Confirmed staging is on its
	// filesystem so all live publication moves can be no-replace renames.
	var scratch string
	if confirm && !sameOwner {
		scratch, err = os.MkdirTemp(root, ".orva-portable-stage-")
	} else {
		scratch, err = enrollmentScratch(root)
	}
	if err != nil {
		return err
	}
	retain := false
	defer func() {
		if !retain || err == nil {
			err = errors.Join(err, os.RemoveAll(scratch))
		}
	}()
	stage, err := backup.StageManaged(ctx, input, scratch)
	if err != nil {
		return err
	}
	if sameOwner {
		record, err := lock.Read()
		if err != nil || record.Mode != servingmode.Managed || record.Owner != stage.PriorOwner {
			return errors.Join(database.ErrManagedOwnerMismatch, err)
		}
		if !confirm {
			return nil
		}
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return restoreSameOwner(ctx, root, lock, record, input)
	}
	record, err := lock.CheckPortableRestore()
	if err != nil {
		return err
	}
	if record.Owner == stage.PriorOwner {
		return database.ErrManagedOwnerMismatch
	}
	required := make(map[string]bool)
	for _, d := range stage.Deployments {
		required[d.Runtime] = true
	}
	runtimes, nsjail, err := managedBuildPreflight(root, required)
	if err != nil || !confirm {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	retain = true
	intent, err := lock.BeginPortableRestore(stage.PriorOwner)
	if err != nil {
		return err
	}
	if err := step("intent"); err != nil {
		return err
	}
	if err := rebuildPortable(ctx, root, nsjail, intent, stage, runtimes, step); err != nil {
		return fmt.Errorf("portable restore incomplete: %w", err)
	}
	if err := step("owner_closed"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stageDir, err := os.Open(scratch)
	if err != nil {
		return err
	}
	defer stageDir.Close()
	return intent.Install(int(stageDir.Fd()), step)
}

func restoreSameOwner(ctx context.Context, root string, lock *servingmode.Lock, record servingmode.Record, r io.Reader) (err error) {
	owner, budget, err := openManagedBootOwner(ctx, root, record, false)
	if err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), managedShutdownTimeout)
		defer cancel()
		err = errors.Join(err, owner.Close(stop), budget.Close())
	}()
	recovery, err := workloadrecovery.OpenManagedLocked(lock, workloadrecovery.Host{
		Quiesce: func(context.Context) error { return nil }, AdmissionClosed: func() bool { return true },
	}, record.Owner)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, recovery.Close()) }()
	_, err = recovery.RestoreBound(ctx, record.Owner, r)
	return err
}

func rebuildPortable(ctx context.Context, root, nsjail string, intent *servingmode.PortableRestore, stage *backup.ManagedStage,
	runtimes []string, step func(string) error,
) (err error) {
	record := intent.Record()
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
	gens := make(map[string]string)
	build, dispose, err := managedRebuilder(ctx, root, nsjail, assets, runtimes, gens, step)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dispose()) }()
	for key, name := range map[string]string{"master.key": ".master.key", "admin.key": ".admin-key"} {
		src := filepath.Join(stage.Dir, "keys", key)
		if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(src, filepath.Join(stage.Dir, name)); err != nil {
			return err
		}
	}
	path := filepath.Join(stage.Dir, "orva.db")
	db, err := database.New(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	rebuilt := make(map[string]database.WorkloadBuildOutput)
	for _, d := range stage.Deployments {
		source, want := stage.Source(d.Deployment)
		fn, err := db.GetFunction(d.Owner)
		if err != nil {
			return err
		}
		built, err := build.Build(ctx, fn, source)
		if err != nil {
			return fmt.Errorf("rebuild deployment %s: %w", d.Deployment, err)
		}
		lease, err := assets.Code.Acquire(built.CodeArtifact.Generation)
		if err != nil {
			return err
		}
		hash := sha256.New()
		err = lease.WriteBuildSource(ctx, hash)
		lease.Release()
		if err != nil || fmt.Sprintf("%x", hash.Sum(nil)) != want || built.SourceArchiveSHA256 != want {
			return errors.Join(artifactfs.ErrIntegrity, err)
		}
		rebuilt[d.Deployment] = database.WorkloadBuildOutput{
			CodeGeneration: built.CodeArtifact.Generation, CodeDigest: built.CodeArtifact.Digest,
			RuntimeGeneration: built.RuntimeArtifact.Generation, RuntimeDigest: built.RuntimeArtifact.Digest,
			CodeHash: d.Output.CodeHash, Entrypoint: built.Entrypoint,
		}
		if err := step("code_sealed"); err != nil {
			return err
		}
	}
	if err := db.RebindPortableRestore(ctx, stage.PriorOwner, record.Owner, record.Operation, rebuilt, gens); err != nil {
		return err
	}
	if err := step("database_committed"); err != nil {
		return err
	}
	sm := secrets.New(db, stage.Dir)
	for _, d := range stage.Deployments {
		fn, err := db.GetFunction(d.Owner)
		if err != nil {
			return err
		}
		if fn.Status != "active" {
			continue
		}
		factory, _, err := workloadrecovery.PrepareSealedDeployment(ctx, db, sm, owner, d.Deployment,
			workloadspawn.FactoryLimits{MaxPreparedPlans: 16, MaxPreparedBytes: 64 << 20, MaxSnapshotBytes: 64 << 20})
		if err != nil {
			return err
		}
		sdk := factory.Resources().SDK
		factory.Close()
		if !sdk {
			return artifactfs.ErrIntegrity
		}
	}
	if err := step("associations_verified"); err != nil {
		return err
	}
	var busy, pages, checkpointed int
	err = db.WriteDB().QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &pages, &checkpointed)
	if err != nil || busy != 0 || pages != checkpointed {
		return errors.Join(errors.New("portable SQLite checkpoint incomplete"), err)
	}
	return step("database_checkpointed")
}
