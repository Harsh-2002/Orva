package pool

import (
	"context"
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
)

func TestManagedModeRequiresConcreteInstalledFactory(t *testing.T) {
	authority, err := workloadiam.NewAuthority(4, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(ManagerConfig{ManagedAuthority: authority}, SandboxTemplate{}, nil, nil, nil)
	defer m.Shutdown(context.Background())
	for _, factory := range []*workloadspawn.Factory{nil, {}} {
		if err := m.InstallFactory(t.Context(), factory); err == nil {
			t.Fatal("unminted Factory accepted")
		}
	}
	if _, err := m.Acquire(t.Context(), "not-installed"); !errors.Is(err, ErrNoFunction) {
		t.Fatal("managed mode fell back to legacy registry", err)
	}
	if m.CurrentArtifact("not-installed") != "" || m.IsCurrentArtifact("not-installed", "forged") {
		t.Fatal("missing current authority minted")
	}
	if _, ok := (&AcquireResult{}).ManagedResources(); ok {
		t.Fatal("legacy/unowned acquire reported managed resources")
	}
}
