//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/backend/internal/workloadrecovery"
)

type ManagedOptions struct {
	DataDir, NsjailBin string
	Artifacts          *artifactfs.Owner
	LayoutLock         *servingmode.Lock // Optional; caller retains it through Close.
	Output             builder.OutputConfig
	RuntimeGenerations map[sandbox.Language]string
	ChatGuard          firewall.Guard
	// RootfsDir holds the installed per-language runtime trees. When a
	// language has no recorded generation yet, startup seals its tree into the
	// runtime store and records the generation, so a fresh managed install can
	// run functions without an environment override. Empty disables sealing.
	RootfsDir string
}

// ManagedInstance owns the managed listener's recovery lock and database.
// The embedding process owns the artifact Owner and must retain it until Close
// succeeds.
type ManagedInstance struct {
	http.Handler
	mu          sync.Mutex
	service     *managedServer
	database    *database.Database
	coordinator *workloadrecovery.Coordinator
}

func StartManaged(ctx context.Context, in ManagedOptions) (*ManagedInstance, error) {
	if ctx == nil || in.Artifacts == nil {
		return nil, errManagedServerUnavailable
	}
	ownerID, err := in.Artifacts.InstanceID(ctx)
	if err != nil {
		return nil, err
	}
	var service *managedServer
	host := workloadrecovery.Host{
		Quiesce:         func(context.Context) error { return nil },
		AdmissionClosed: func() bool { return service == nil },
	}
	var recovery *workloadrecovery.Coordinator
	if in.LayoutLock == nil {
		recovery, err = workloadrecovery.OpenManaged(in.DataDir, host, ownerID)
	} else {
		recovery, err = workloadrecovery.OpenManagedLocked(in.LayoutLock, host, ownerID)
	}
	if err != nil {
		return nil, err
	}
	db, _, err := recovery.StartBound(ctx, ownerID, func(ctx context.Context, db *database.Database) error {
		return db.InitManagedServingSchemas(ctx)
	}, func(ctx context.Context, db *database.Database) error {
		runtimes, err := managedRuntimeSelections(ctx, in, db, initialRuntimeSealer(in.Artifacts))
		if err != nil {
			return err
		}
		service, err = newManagedServer(ctx, db, managedServerInput{
			artifacts: in.Artifacts, output: in.Output, runtimeGenerations: runtimes,
			dataDir: recovery.DataDir(), nsjail: in.NsjailBin, chatGuard: in.ChatGuard,
		})
		return err
	})
	if err != nil {
		return nil, errors.Join(err, recovery.Close())
	}
	return &ManagedInstance{Handler: service, service: service, database: db, coordinator: recovery}, nil
}

// Close requires the caller to drain the HTTP listener first. A timeout after
// closing admission retains the database and recovery lock for a retry.
func (s *ManagedInstance) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.database == nil {
		return nil
	}
	if err := s.service.Shutdown(ctx); err != nil {
		return err
	}
	err := s.database.Close()
	s.database = nil
	return errors.Join(err, s.coordinator.Close())
}

type runtimeSealer func(context.Context, sandbox.Language, string) (string, error)

func initialRuntimeSealer(owner *artifactfs.Owner) runtimeSealer {
	var store *artifactfs.Store
	return func(ctx context.Context, lang sandbox.Language, tree string) (string, error) {
		if store == nil {
			bindings, err := owner.Bindings(ctx)
			if err != nil {
				return "", err
			}
			store = bindings.Runtime
		}
		id, err := artifactfs.SealInitialRuntime(ctx, store, string(lang), tree, func(root string) error {
			return sandbox.CheckManagedRuntime(root, lang)
		})
		return id.Generation, err
	}
}

func managedRuntimeSelections(ctx context.Context, in ManagedOptions, db *database.Database, seal runtimeSealer) (map[sandbox.Language]string, error) {
	recorded, err := db.LegacyEnrollmentRuntimes(ctx)
	if err != nil {
		return nil, err
	}
	sealed := make(map[string]string, 2)
	for _, lang := range []sandbox.Language{sandbox.Node, sandbox.Python} {
		if in.RootfsDir == "" || recorded[string(lang)] != "" || in.RuntimeGenerations[lang] != "" {
			continue
		}
		tree, err := filepath.Abs(filepath.Join(in.RootfsDir, string(lang)))
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(tree); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		generation, err := seal(ctx, lang, tree)
		if err != nil {
			return nil, fmt.Errorf("seal installed %s runtime: %w", lang, err)
		}
		sealed[string(lang)] = generation
	}
	if len(sealed) != 0 {
		if err := db.RecordManagedRuntimeGenerations(ctx, sealed); err != nil {
			return nil, fmt.Errorf("record sealed runtime generations: %w", err)
		}
		recorded, err = db.LegacyEnrollmentRuntimes(ctx)
		if err != nil {
			return nil, err
		}
	}
	runtimes := make(map[sandbox.Language]string, 2)
	for lang, generation := range recorded {
		runtimes[sandbox.Language(lang)] = generation
	}
	for lang, generation := range in.RuntimeGenerations {
		runtimes[lang] = generation
	}
	if len(runtimes) == 0 {
		return nil, errors.New("no sealed Node or Python runtime generation; install trusted rootfs trees with current adapters (without --skip-rootfs), then retry orva serve")
	}
	return runtimes, nil
}
