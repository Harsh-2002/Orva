package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelfCgroupBaseStaysWithinProcessMembership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cgroup")
	base, err := selfCgroupBase(root, "12:cpu:/unrelated\n0::/system.slice/orva.service\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "system.slice", "orva.service"); base != want {
		t.Fatalf("base = %q, want %q", base, want)
	}
	for _, membership := range []string{"0::/\n", "0::\n", "4:cpu:/legacy\n"} {
		if base, err := selfCgroupBase(root, membership); err == nil {
			t.Fatalf("accepted unscoped membership %q as %q", membership, base)
		}
	}
}

func TestCgroupControllerRequirements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cgroup.controllers")
	if err := os.WriteFile(path, []byte("cpu io memory pids\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	have, err := controllerSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if missing := missingControllers(have); len(missing) != 0 {
		t.Fatalf("complete delegate missing controllers: %v", missing)
	}
	delete(have, "memory")
	delete(have, "pids")
	if got, want := missingControllers(have), []string{"memory", "pids"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
}

func TestCgroupOverrideCannotEscapeServiceBudget(t *testing.T) {
	base := filepath.Join(t.TempDir(), "orva.service")
	for _, path := range []string{base, filepath.Dir(base), filepath.Join(filepath.Dir(base), "other.service"), "relative/group"} {
		if err := validateCgroupOverride(base, path); err == nil {
			t.Fatalf("accepted override outside own delegate: %q", path)
		}
	}
	if err := validateCgroupOverride(base, filepath.Join(base, "workers")); err != nil {
		t.Fatalf("rejected owned child: %v", err)
	}
}
