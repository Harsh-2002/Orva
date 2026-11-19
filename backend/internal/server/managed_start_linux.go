//go:build linux

package server

import (
	"context"
	"errors"
	"net/http"
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
		defaults, err := db.LegacyEnrollmentRuntimes(ctx)
		if err != nil {
			return err
		}
		runtimes := in.RuntimeGenerations
		if len(defaults) != 0 {
			runtimes = make(map[sandbox.Language]string, len(defaults)+len(in.RuntimeGenerations))
			for lang, generation := range defaults {
				runtimes[sandbox.Language(lang)] = generation
			}
			for lang, generation := range in.RuntimeGenerations {
				runtimes[lang] = generation
			}
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

// StopEventStreams ends every open /api/v1/events stream so that draining the
// HTTP listener cannot wait on a client that never goes idle. It is idempotent
// and is called before the listener drains.
func (s *ManagedInstance) StopEventStreams() {
	s.mu.Lock()
	service := s.service
	s.mu.Unlock()
	if service != nil && service.runtime != nil && service.runtime.hub != nil {
		service.runtime.hub.Close()
	}
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
