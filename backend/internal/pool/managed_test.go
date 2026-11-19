package pool

import (
	"context"
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
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

// The managed server wires the same firewall manager the operator UI edits, so
// every egress spawn loads a compiled generation. A pool without that source
// must keep refusing rather than spawning unfiltered.
func TestManagedEgressSpawnRequiresWiredPolicy(t *testing.T) {
	authority, err := workloadiam.NewAuthority(4, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(ManagerConfig{ManagedAuthority: authority}, SandboxTemplate{}, nil, nil, nil)
	defer m.Shutdown(context.Background())

	if _, _, err := m.egressPolicyForSpawn("egress"); !errors.Is(err, sandbox.ErrEgressPolicyMissing) {
		t.Fatalf("unwired egress spawn: got %v, want ErrEgressPolicyMissing", err)
	}
	if path, gen, err := m.egressPolicyForSpawn("none"); err != nil || path != "" || gen != "" {
		t.Fatalf("network_mode=none asked for a policy: %q %q %v", path, gen, err)
	}

	m.SetEgressPolicy(func() (string, string, error) { return "/firewall/policy/g1.cfg", "g1", nil })
	if path, gen, err := m.egressPolicyForSpawn("egress"); err != nil || path != "/firewall/policy/g1.cfg" || gen != "g1" {
		t.Fatalf("wired egress spawn: %q %q %v", path, gen, err)
	}

	compileErr := errors.New("no policy compiled")
	m.SetEgressPolicy(func() (string, string, error) { return "", "", compileErr })
	if _, _, err := m.egressPolicyForSpawn("egress"); !errors.Is(err, compileErr) {
		t.Fatalf("compile failure did not fail closed: %v", err)
	}
}
