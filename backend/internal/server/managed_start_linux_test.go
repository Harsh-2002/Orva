package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
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

func TestManagedRuntimeSealRefusalHint(t *testing.T) {
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "orva.db"))
	managedMust(t, err)
	defer func() { managedMust(t, db.Close()) }()
	in := ManagedOptions{RootfsDir: filepath.Join(dir, "rootfs")}
	tree := filepath.Join(in.RootfsDir, string(sandbox.Node))
	managedMust(t, os.MkdirAll(tree, 0755))
	for _, tc := range []struct {
		name string
		err  error
		hint bool
	}{
		{"ownership", errors.Join(artifactfs.ErrOwnership, artifactfs.ErrUnsupported), true},
		{"unsupported", fmt.Errorf("usr/bin/node: extended attributes present: %w", artifactfs.ErrUnsupported), false},
		{"io", artifactfs.ErrIO, false},
		{"capacity", artifactfs.ErrCapacity, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := managedRuntimeSelections(t.Context(), in, db, func(context.Context, sandbox.Language, string) (string, error) {
				return "", tc.err
			})
			if !errors.Is(err, tc.err) || !strings.Contains(err.Error(), "seal installed node runtime tree "+tree) ||
				strings.Contains(err.Error(), "chown") != tc.hint || strings.Contains(err.Error(), "chmod") != tc.hint {
				t.Fatalf("refusal = %v, want named tree and ownership hint=%t", err, tc.hint)
			}
		})
	}
}

func TestManagedRuntimePartialRetry(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orva.db")
	in := ManagedOptions{RootfsDir: filepath.Join(dir, "rootfs")}
	node, python := ids.New(), ids.New()
	for _, lang := range []sandbox.Language{sandbox.Node, sandbox.Python} {
		managedMust(t, os.MkdirAll(filepath.Join(in.RootfsDir, string(lang)), 0755))
	}
	db, err := database.New(dbPath)
	managedMust(t, err)
	_, err = managedRuntimeSelections(ctx, in, db, func(_ context.Context, lang sandbox.Language, _ string) (string, error) {
		if lang == sandbox.Node {
			return node, nil
		}
		recorded, err := db.LegacyEnrollmentRuntimes(ctx)
		if err != nil || !maps.Equal(recorded, map[string]string{"node": node}) {
			t.Fatalf("before Python seal, recorded = %v, %v", recorded, err)
		}
		return "", artifactfs.ErrCapacity
	})
	if !errors.Is(err, artifactfs.ErrCapacity) || !strings.Contains(err.Error(), "seal installed python runtime tree") ||
		!strings.Contains(err.Error(), "runtime store cannot fit this seal") {
		t.Fatalf("partial seal failure = %v, want Python capacity refusal", err)
	}
	managedMust(t, db.Close())
	db, err = database.New(dbPath)
	managedMust(t, err)
	defer func() { managedMust(t, db.Close()) }()
	got, err := managedRuntimeSelections(ctx, in, db, func(_ context.Context, lang sandbox.Language, _ string) (string, error) {
		if lang != sandbox.Python {
			t.Fatalf("retry tried to seal %s again", lang)
		}
		return python, nil
	})
	managedMust(t, err)
	if !maps.Equal(got, map[sandbox.Language]string{sandbox.Node: node, sandbox.Python: python}) {
		t.Fatalf("retry selections = %v", got)
	}
}

func TestManagedRuntimeValidatesSelections(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "orva.db"))
	managedMust(t, err)
	defer func() { managedMust(t, db.Close()) }()
	storeDir := filepath.Join(dir, "store")
	managedMust(t, os.Mkdir(storeDir, 0700))
	limits := artifactfs.Limits{Generations: 4, Leases: 1, Staging: 1, Entries: 16,
		PathBytes: 256, LinkBytes: 256, Bytes: 4096, FileBytes: 1024, ManifestBytes: 8192}
	store, err := artifactfs.New(storeDir, limits)
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, store.Close()) })
	valid := sealManagedRuntimeFixture(t, store, limits, "orva-sdk-proto:1")
	stale := sealManagedRuntimeFixture(t, store, limits, "old adapter")
	missing := ids.New()
	managedMust(t, db.RecordManagedRuntimeGenerations(ctx, map[string]string{"node": missing}))
	choose := func(overrides map[sandbox.Language]string) error {
		in := ManagedOptions{RootfsDir: filepath.Join(dir, "rootfs"), RuntimeGenerations: overrides}
		managedMust(t, os.MkdirAll(filepath.Join(in.RootfsDir, "node"), 0755))
		runtimes, err := managedRuntimeSelections(ctx, in, db, func(context.Context, sandbox.Language, string) (string, error) {
			t.Fatal("recorded or overridden runtime tried to reseal the installed tree")
			return "", nil
		})
		managedMust(t, err)
		return validateManagedRuntimeSelections(store, runtimes)
	}
	for _, tc := range []struct {
		name                  string
		overrides             map[sandbox.Language]string
		lang, generation, why string
	}{
		{"recorded-missing", nil, "node", missing, "not found in the bound runtime store"},
		{"override-missing", map[sandbox.Language]string{sandbox.Node: "missing-override"}, "node", "missing-override", "not found in the bound runtime store"},
		{"override-stale-adapter", map[sandbox.Language]string{sandbox.Node: stale}, "node", stale, "does not support the managed SDK channel"},
		{"override-wrong-language", map[sandbox.Language]string{sandbox.Node: valid, sandbox.Python: valid}, "python", valid, `belongs to runtime "node"`},
		{"override-valid", map[sandbox.Language]string{sandbox.Node: valid}, "", "", ""},
		{"recorded-still-missing-after-override", nil, "node", missing, "not found in the bound runtime store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := choose(tc.overrides)
			if tc.why == "" {
				managedMust(t, err)
				return
			}
			prefix := fmt.Sprintf("selected %s runtime generation %q: ", tc.lang, tc.generation)
			if err == nil || !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), tc.why) {
				t.Fatalf("validation = %v, want %q and %q", err, prefix, tc.why)
			}
		})
	}
	recorded, err := db.LegacyEnrollmentRuntimes(ctx)
	managedMust(t, err)
	if !maps.Equal(recorded, map[string]string{"node": missing}) {
		t.Fatalf("recorded selection changed: %v", recorded)
	}
}

func sealManagedRuntimeFixture(t *testing.T, store *artifactfs.Store, limits artifactfs.Limits, adapter string) string {
	t.Helper()
	stage, err := store.Begin(t.Context(), 1024)
	managedMust(t, err)
	var generation string
	t.Cleanup(func() {
		if generation == "" {
			managedMust(t, stage.Abort())
			return
		}
		managedMust(t, store.Retire(generation))
		managedMust(t, store.Reclaim(generation))
	})
	root, err := stage.Path()
	managedMust(t, err)
	manifest := artifactfs.Manifest{Version: 1}
	entry := func(name string, kind artifactfs.EntryType) artifactfs.Entry {
		return artifactfs.Entry{Path: name, Type: kind, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	}
	for _, name := range []string{".", "usr", "usr/local", "usr/local/bin", "opt", "opt/orva"} {
		p := filepath.Join(root, name)
		if name != "." {
			managedMust(t, os.Mkdir(p, 0700))
		}
		managedMust(t, os.Chmod(p, 0700))
		e := entry(name, artifactfs.TypeDirectory)
		e.Mode = 0700
		manifest.Entries = append(manifest.Entries, e)
	}
	for name, contents := range map[string]string{"usr/local/bin/node": "fixture", "opt/orva/adapter.js": adapter} {
		p := filepath.Join(root, name)
		managedMust(t, os.WriteFile(p, []byte(contents), 0700))
		managedMust(t, os.Chmod(p, 0700))
		e := entry(name, artifactfs.TypeFile)
		e.Mode, e.Size, e.SHA256 = 0700, int64(len(contents)), fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
		manifest.Entries = append(manifest.Entries, e)
	}
	digest, err := artifactfs.Digest(manifest, limits)
	managedMust(t, err)
	generation, err = stage.Seal(t.Context(), artifactfs.KindRuntime, "node", manifest, digest)
	managedMust(t, err)
	return generation
}
