package server

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedRuntimeSelections(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "orva.db"))
	managedMust(t, err)
	defer func() { managedMust(t, db.Close()) }()
	in := ManagedOptions{RootfsDir: filepath.Join(dir, "rootfs")}
	want := map[sandbox.Language]string{sandbox.Node: ids.New(), sandbox.Python: ids.New()}
	calls := make(map[sandbox.Language]int)
	seal := func(_ context.Context, lang sandbox.Language, path string) (string, error) {
		calls[lang]++
		if path != filepath.Join(in.RootfsDir, string(lang)) {
			t.Fatalf("wrong source tree: %s", path)
		}
		return want[lang], nil
	}
	if _, err := managedRuntimeSelections(ctx, in, db, seal); err == nil || !strings.Contains(err.Error(), "without --skip-rootfs") {
		t.Fatalf("no usable runtime: %v", err)
	}
	for lang := range want {
		managedMust(t, os.MkdirAll(filepath.Join(in.RootfsDir, string(lang)), 0755))
	}
	got, err := managedRuntimeSelections(ctx, in, db, seal)
	managedMust(t, err)
	if !maps.Equal(got, want) || calls[sandbox.Node] != 1 || calls[sandbox.Python] != 1 {
		t.Fatalf("first selection = %v; seal calls = %v", got, calls)
	}
	nodeOverride := ids.New()
	in.RuntimeGenerations = map[sandbox.Language]string{sandbox.Node: nodeOverride}
	got, err = managedRuntimeSelections(ctx, in, db, seal)
	managedMust(t, err)
	if got[sandbox.Node] != nodeOverride || got[sandbox.Python] != want[sandbox.Python] {
		t.Fatalf("override not applied: %v", got)
	}
	in.RuntimeGenerations = nil
	got, err = managedRuntimeSelections(ctx, in, db, seal)
	managedMust(t, err)
	if !maps.Equal(got, want) || calls[sandbox.Node] != 1 || calls[sandbox.Python] != 1 {
		t.Fatalf("restart selection = %v; seal calls = %v", got, calls)
	}
}
