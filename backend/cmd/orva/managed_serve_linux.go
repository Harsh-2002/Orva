package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/config"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
)

const (
	managedNodeRuntimeEnv   = "ORVA_MANAGED_NODE_GENERATION"
	managedPythonRuntimeEnv = "ORVA_MANAGED_PYTHON_GENERATION"
	managedShutdownTimeout  = 25 * time.Second
)

func managedExit(err error) int {
	if err != nil {
		slog.Error("managed startup or shutdown refused", "error", err)
		return exitConfig
	}
	return 0
}

func managedOwnerConfig(dataDir string) artifactfs.OwnerConfig {
	witness, set := os.LookupEnv(managedWitnessEnv)
	if !set {
		witness = defaultManagedWitness
	}
	return artifactfs.OwnerConfig{
		MetadataRoot: filepath.Join(dataDir, "sealed-owner"),
		CodeRoot:     filepath.Join(dataDir, "sealed-code"),
		RuntimeRoot:  filepath.Join(dataDir, "sealed-runtime"),
		Parent:       os.Getenv(managedParentEnv),
		CodeLimits: artifactfs.Limits{Generations: 1024, Leases: 512, Staging: 8, Entries: 16384,
			PathBytes: 4096, LinkBytes: 4096, ManifestBytes: 8 << 20, Bytes: 8 << 30, FileBytes: 64 << 20},
		RuntimeLimits: artifactfs.Limits{Generations: 8, Leases: 512, Staging: 2, Entries: 32768,
			PathBytes: 4096, LinkBytes: 4096, ManifestBytes: 64 << 20, Bytes: 4 << 30, FileBytes: 256 << 20},
		MaxEpochs: 8,
		Host:      &artifactfs.OwnerHostConfig{WitnessPath: witness, BackupRoots: managedBackupRoots(dataDir)},
	}
}

func managedOutputConfig(dataDir string, limits artifactfs.Limits) builder.OutputConfig {
	return builder.OutputConfig{
		MetadataRoot: filepath.Join(dataDir, "managed-build"), Limits: limits,
		ReservedBytes: 128 << 20, MaxReservedBytes: 512 << 20, ArchiveBytes: 64 << 20,
		MaxBuilds: 4, BuildTimeout: 5 * time.Minute, CleanupTimeout: 5 * time.Second,
	}
}

func openManagedBootOwner(ctx context.Context, dataDir string, record servingmode.Record, create bool) (*artifactfs.Owner, *workloadfence.Budget, error) {
	cfg := managedOwnerConfig(dataDir)
	if _, set := os.LookupEnv(managedParentEnv); !set {
		parent, err := workloadfence.DefaultServingParent()
		if err != nil {
			return nil, nil, fmt.Errorf("derive managed workload parent: %w", err)
		}
		cfg.Parent = parent
	}
	if create {
		for _, path := range []string{cfg.MetadataRoot, cfg.CodeRoot, cfg.RuntimeRoot, managedOutputConfig(dataDir, cfg.CodeLimits).MetadataRoot} {
			if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, nil, fmt.Errorf("prepare managed roots: %w", err)
			}
		}
	}
	budget, err := workloadfence.CaptureServingBudget(cfg.Parent)
	if err != nil {
		return nil, nil, fmt.Errorf("capture managed workload budget: %w", err)
	}
	if err := sandbox.PrepareManagedServingCgroup(cfg.Parent); err != nil {
		return nil, nil, errors.Join(err, budget.Close())
	}
	cfg.Budget = budget
	owner, err := artifactfs.OpenOwnerForBoot(ctx, cfg, record.Owner, create)
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("open managed artifact owner: %w", err), budget.Close())
	}
	return owner, budget, nil
}

func setupManagedLayout(ctx context.Context, action, dataDir string) (err error) {
	dataDir, err = managedSetupDataDir(dataDir)
	if err != nil {
		return err
	}
	lock, err := servingmode.Acquire(dataDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	switch action {
	case "provision":
		return lock.BeginProvisioning()
	case "publish":
		if err := lock.CheckProvisioning(); err != nil {
			return err
		}
		record, err := lock.Read()
		if err != nil {
			return err
		}
		if err := record.CheckEnvironment(); err != nil {
			return err
		}
		owner, budget, err := openManagedBootOwner(ctx, dataDir, record, false)
		if err != nil {
			return err
		}
		id, identityErr := owner.InstanceID(ctx)
		stop, cancel := context.WithTimeout(context.Background(), managedShutdownTimeout)
		defer cancel()
		if err := errors.Join(identityErr, owner.Close(stop), budget.Close()); err != nil {
			return err
		}
		return lock.PublishProvisioned(id)
	default:
		return errors.New("--managed-layout requires provision or publish")
	}
}

func runServeManaged(ctx context.Context, cfg *config.Config) (err error) {
	lock, err := servingmode.Acquire(cfg.Data.Dir)
	if err != nil {
		return err
	}
	releaseLayout := true
	defer func() {
		if releaseLayout {
			err = errors.Join(err, lock.Close())
		}
	}()
	record, err := lock.Read()
	if err != nil {
		return err
	}
	if err := record.CheckEnvironment(); err != nil {
		return err
	}
	switch record.Mode {
	case servingmode.Managed:
		_, dir, err := lock.OpenData()
		if err != nil {
			return err
		}
		if err := dir.Close(); err != nil {
			return err
		}
	case servingmode.Provisioning:
		if err := lock.CheckProvisioning(); err != nil {
			return err
		}
	default:
		return errors.New("a mode marker does not enable legacy serving")
	}
	create := record.Mode == servingmode.Provisioning
	owner, budget, err := openManagedBootOwner(ctx, cfg.Data.Dir, record, create)
	if err != nil {
		return err
	}
	var instance *server.ManagedInstance
	var httpServer *http.Server
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), managedShutdownTimeout)
		defer cancel()
		closeErr := closeManaged(stop, func(context.Context) error {
			// An SSE stream never idles, so http.Server.Shutdown would wait out the
			// whole budget on one open dashboard tab. End the streams first.
			if instance != nil {
				instance.StopEventStreams()
			}
			return nil
		}, func(ctx context.Context) error {
			if httpServer == nil {
				return nil
			}
			err := httpServer.Shutdown(ctx)
			if err != nil {
				// Do not leave handlers running against the instance closed next.
				err = errors.Join(err, httpServer.Close())
			}
			return err
		}, func(ctx context.Context) error {
			if instance == nil {
				return nil
			}
			return instance.Close(ctx)
		}, func(ctx context.Context) error {
			return errors.Join(owner.Close(ctx), budget.Close())
		})
		if closeErr != nil {
			// Leave the outer lock held if an HTTP/DB user did not drain.
			releaseLayout = false
			strandedManagedLayout = lock
		}
		err = errors.Join(err, closeErr)
	}()
	if create {
		slog.Info("managed artifact owner provisioned; run setup --managed-layout publish offline before serving")
		return nil
	}
	runtimes := make(map[sandbox.Language]string)
	for lang, key := range map[sandbox.Language]string{sandbox.Node: managedNodeRuntimeEnv, sandbox.Python: managedPythonRuntimeEnv} {
		if gen, set := os.LookupEnv(key); set {
			if gen == "" {
				return fmt.Errorf("%s must name a sealed generation or be unset", key)
			}
			runtimes[lang] = gen
		}
	}
	instance, err = server.StartManaged(ctx, server.ManagedOptions{
		DataDir: cfg.Data.Dir, LayoutLock: lock, Artifacts: owner, NsjailBin: cfg.Sandbox.NsjailBin,
		Output:             managedOutputConfig(cfg.Data.Dir, managedOwnerConfig(cfg.Data.Dir).CodeLimits),
		RuntimeGenerations: runtimes,
	})
	if err != nil {
		return fmt.Errorf("start managed server: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)))
	if err != nil {
		return fmt.Errorf("listen for managed HTTP: %w", err)
	}
	defer ln.Close()
	httpServer = &http.Server{Handler: instance, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 120 * time.Second,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSec) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSec) * time.Second}
	slog.Info("managed server listening", "addr", ln.Addr())
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(ln) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		return fmt.Errorf("managed HTTP server stopped: %w", err)
	}
}

var strandedManagedLayout *servingmode.Lock // Retained only until the command exits 78.

// closeManaged runs every stage in order and joins their errors: a failed HTTP
// drain must not skip the instance and Owner cleanup that follow it. Any error
// still leaves the layout lock held (the caller strands it).
func closeManaged(ctx context.Context, stages ...func(context.Context) error) error {
	var err error
	for _, stage := range stages {
		err = errors.Join(err, stage(ctx))
	}
	return err
}
