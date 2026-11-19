package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func TestRunManagedRequiresSharedAuthority(t *testing.T) {
	fixture, _, service := cronSchedulerFixture(t)
	foreign, err := workloadiam.NewAuthority(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	pm := pool.NewManager(pool.ManagerConfig{ManagedAuthority: foreign}, pool.SandboxTemplate{},
		fixture.db, registry.New(fixture.db), sandbox.NewLimiter(1))
	t.Cleanup(func() { _ = pm.Shutdown(context.Background()) })
	s := New(fixture.db, pm, t.TempDir(), nil, WithAsyncIAM(service), WithCronIAM(service))
	if err := s.RunManaged(t.Context()); !errors.Is(err, ErrManagedUnavailable) {
		t.Fatalf("foreign pool authority accepted: %v", err)
	}
}

func TestRunManagedChecksDueCronAndWaitsForExit(t *testing.T) {
	fixture, due, service := cronSchedulerFixture(t)
	pm := pool.NewManager(pool.ManagerConfig{ManagedAuthority: service.Authority}, pool.SandboxTemplate{},
		fixture.db, registry.New(fixture.db), sandbox.NewLimiter(1))
	t.Cleanup(func() { _ = pm.Shutdown(context.Background()) })
	s := New(fixture.db, pm, t.TempDir(), nil, WithAsyncIAM(service), WithCronIAM(service))
	observed := make(chan struct{}, 1)
	service.CurrentArtifact = func(string) string {
		select {
		case observed <- struct{}{}:
		default:
		}
		return ""
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.RunManaged(ctx) }()
	select {
	case <-observed:
	case err := <-done:
		t.Fatalf("managed scheduler ended before checking due work: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("managed scheduler did not observe due cron")
	}
	if err := s.RunManaged(ctx); !errors.Is(err, ErrManagedUnavailable) {
		t.Fatalf("duplicate scheduler start accepted: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("managed scheduler did not drain")
	}
	got, err := fixture.db.GetAsyncCron(t.Context(), due.ID)
	if err != nil || got.LastRunAt != nil {
		t.Fatalf("unauthorized cron ran: %v", err)
	}
}
