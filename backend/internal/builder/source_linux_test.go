//go:build linux

package builder

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestReadManagedTypeScriptSourceUsesAuthoredEntrypoint(t *testing.T) {
	s, ctx, owner := publicationQueueFixture(t)
	db := s.queue.db
	store, _, limits := outputTestStore(t)
	s.code = store
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	fn, err := db.GetFunction(owner)
	must(err)
	const authored, compiled = "handler.ts", "dist/handler.js"
	const source = "export const count: number = 42"
	const deps = `{"devDependencies":{"typescript":"5.9.3"}}`
	fn.Entrypoint = authored
	must(db.UpdateFunction(fn))

	stage, err := store.Begin(ctx, 4096)
	must(err)
	root, err := stage.Path()
	must(err)
	must(os.Chmod(root, 0700))
	must(os.Mkdir(filepath.Join(root, "dist"), 0700))
	must(os.Chmod(filepath.Join(root, "dist"), 0700))
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	manifest := artifactfs.Manifest{Version: 1, Entries: []artifactfs.Entry{
		{Path: ".", Type: artifactfs.TypeDirectory, Mode: 0700, UID: uid, GID: gid},
		{Path: "dist", Type: artifactfs.TypeDirectory, Mode: 0700, UID: uid, GID: gid},
	}}
	for name, body := range map[string]string{authored: source, compiled: "exports.count = 42", "package.json": deps} {
		path := filepath.Join(root, name)
		must(os.WriteFile(path, []byte(body), 0600))
		must(os.Chmod(path, 0600))
		manifest.Entries = append(manifest.Entries, artifactfs.Entry{
			Path: name, Type: artifactfs.TypeFile, Mode: 0600, UID: uid, GID: gid,
			Size: int64(len(body)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(body))),
		})
	}
	digest, err := artifactfs.Digest(manifest, limits)
	must(err)
	code, err := stage.Seal(ctx, artifactfs.KindCode, owner, manifest, digest)
	must(err)
	t.Cleanup(func() { must(store.Retire(code)); must(store.Reclaim(code)) })
	ref, _ := auth.OperatorFromContext(ctx)
	plan, err := db.CaptureWorkloadDeployment(ctx, ref, owner)
	must(err)
	output := database.WorkloadBuildOutput{
		CodeGeneration: code, CodeDigest: digest, RuntimeGeneration: ids.New(),
		RuntimeDigest: strings.Repeat("a", 64), CodeHash: strings.Repeat("b", 64), Entrypoint: compiled,
	}
	must(db.RecordWorkloadBuildOutput(ctx, plan.ID, output))
	published, err := db.PublishWorkloadDeployment(ctx, plan.ID, output)
	must(err)
	if published.Function.Entrypoint != authored || published.Function.RunEntrypoint != compiled {
		t.Fatalf("unexpected authored/run pair: %q, %q", published.Function.Entrypoint, published.Function.RunEntrypoint)
	}
	got, gotDeps, err := s.ReadSource(ctx, owner)
	if err != nil || got != source || gotDeps != deps {
		t.Fatalf("managed editor source = %q, %q, %v; want authored TypeScript and dependencies", got, gotDeps, err)
	}
}

func TestReadSourceTextStaysInSealedRoot(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "code")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"outside": "outside", "code/source": "authored"} {
		if err := os.WriteFile(filepath.Join(parent, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../outside", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if got, err := readSourceText(root, "source", 8); err != nil || got != "authored" {
		t.Fatalf("inside source: %q, %v", got, err)
	}
	for name, limit := range map[string]int64{"link": 8, "../outside": 8, "source": 7, ".": 8} {
		if got, err := readSourceText(root, name, limit); err == nil || got != "" {
			t.Fatalf("%s/%d returned content outside source constraints", name, limit)
		}
	}
}
