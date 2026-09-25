package builder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestSyntaxCheckTypeScriptRequiresConfig(t *testing.T) {
	b := &Builder{}
	err := b.checkSyntax(t.Context(), t.TempDir(), "node", "handler.ts")
	if err == nil || !strings.Contains(err.Error(), "tsconfig.json") {
		t.Fatalf("expected actionable TypeScript configuration error, got %v", err)
	}
}

func TestBuildChecksSyntaxBeforeDependencyInstall(t *testing.T) {
	archive := createTestArchive(t, map[string]string{
		"handler.js":   "const broken = ;",
		"package.json": `{"dependencies":{"example":"1.0.0"}}`,
	})
	want := errors.New("invalid source")
	called := false
	b := &Builder{DataDir: t.TempDir(), checkSyntaxHook: func(_ context.Context, dir, runtime, entrypoint string) error {
		called = true
		if runtime != "node" || entrypoint != "handler.js" {
			t.Errorf("syntax target = %s/%s", runtime, entrypoint)
		}
		if _, err := os.Stat(filepath.Join(dir, entrypoint)); err != nil {
			t.Errorf("source not extracted: %v", err)
		}
		return want
	}}
	_, err := b.Build(t.Context(), &database.Function{ID: "fn", Name: "fn", Runtime: "node", Entrypoint: "handler.js"}, archive)
	if !called || !errors.Is(err, want) {
		t.Fatalf("syntax check must fail before npm/policy lookup: called=%v err=%v", called, err)
	}
}
