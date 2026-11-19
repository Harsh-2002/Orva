package builder

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedDependencyProfile(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, entry, manifest, config, want string
	}{
		{"npm", "node", "handler.js", "package.json", "", "handler.js"},
		{"pip", "python", "handler.py", "requirements.txt", "", "handler.py"},
		{"tsc", "node", "handler.ts", "package.json", `{"compilerOptions":{"outDir":"build"}}`, "build/handler.js"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(tc.entry, "")
			write(tc.manifest, `{"devDependencies":{"typescript":"5.9.3"}}`)
			if tc.config != "" {
				write("tsconfig.json", tc.config)
			}
			fn := &database.Function{ID: ids.New(), Runtime: tc.runtime, Entrypoint: tc.entry}
			p, err := managedProfile(fn, dir)
			if err != nil || !p.install || p.compile != (tc.config != "") || p.entrypoint != tc.want {
				t.Fatalf("profile = %+v, %v", p, err)
			}
			b := &Builder{DataDir: t.TempDir()}
			install := b.installNPM
			if tc.runtime == "python" {
				install = b.installPip
			}
			if err := install(t.Context(), fn.ID, dir); !errors.Is(err, sandbox.ErrEgressPolicyMissing) {
				t.Fatalf("install without policy = %v", err)
			}
			for _, owner := range []string{fn.ID, ids.New()} {
				cfg := b.buildConfigFor(buildStep{fnID: owner})
				want := filepath.Join(b.DataDir, "build-cache", owner)
				if cfg.CacheDir != want {
					t.Fatalf("cache = %q, want %q", cfg.CacheDir, want)
				}
			}
		})
	}
}

func TestManagedStepsDenyInstallWithNoEgressPolicy(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"handler.ts":    "const n: number = 1",
		"package.json":  `{"devDependencies":{"typescript":"5.9.3"}}`,
		"tsconfig.json": `{"compilerOptions":{"outDir":"dist"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fn := &database.Function{ID: ids.New(), Runtime: "node", Entrypoint: "handler.ts"}
	profile, err := managedProfile(fn, dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Builder{NsjailBin: filepath.Join(dir, "must-not-start")}
	// A .ts entrypoint defers syntax checking to tsc. The managed pipeline
	// must fail at the install's policy gate, before attempting to start npm.
	if err := b.runManagedSteps(t.Context(), fn, dir, profile); !errors.Is(err, sandbox.ErrEgressPolicyMissing) || !strings.Contains(err.Error(), "npm install failed") {
		t.Fatalf("managed install failed outside the policy gate: %v", err)
	}
}
