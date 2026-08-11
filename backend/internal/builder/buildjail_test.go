package builder

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// TestInstallDependencies_NoDepsNeedsNoPolicy is the "don't break the common
// case" guard. A function that declares no dependencies never runs an
// installer, so it must deploy with no egress policy wired at all — the same
// path unit tests and a fresh boot before the first policy compile take.
func TestInstallDependencies_NoDepsNeedsNoPolicy(t *testing.T) {
	cases := []struct {
		name       string
		runtime    string
		entrypoint string
		files      map[string]string
	}{
		{"node without package.json", "node", "handler.js", map[string]string{"handler.js": "module.exports={}"}},
		{"python without requirements.txt", "python", "handler.py", map[string]string{"handler.py": "def handler(e): pass"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// EgressPolicy deliberately nil: reaching for it here would mean
			// we had started an installer that has nothing to install.
			b := &Builder{DataDir: t.TempDir()}
			got, err := b.installDependencies(t.Context(), "", dir, tc.runtime, tc.entrypoint)
			if err != nil {
				t.Fatalf("dependency-free build must not need a policy: %v", err)
			}
			if got != tc.entrypoint {
				t.Errorf("entrypoint changed: want %q, got %q", tc.entrypoint, got)
			}
		})
	}
}

// TestInstallDependencies_FailsClosedWithoutPolicy is the point of the change.
// An install fetches from a package registry and runs whatever postinstall
// script arrives with it; NSTUN allows anything no rule matches, so without a
// compiled policy the only safe answer is to refuse.
func TestInstallDependencies_FailsClosedWithoutPolicy(t *testing.T) {
	deps := map[string]struct {
		runtime    string
		entrypoint string
		manifest   string
		body       string
	}{
		"node":   {"node", "handler.js", "package.json", `{"dependencies":{"lodash":"^4"}}`},
		"python": {"python", "handler.py", "requirements.txt", "requests\n"},
	}

	for name, d := range deps {
		t.Run(name+"/no policy source", func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, d.manifest), []byte(d.body), 0o644); err != nil {
				t.Fatal(err)
			}
			b := &Builder{DataDir: t.TempDir()}
			_, err := b.installDependencies(t.Context(), "", dir, d.runtime, d.entrypoint)
			if !errors.Is(err, sandbox.ErrEgressPolicyMissing) {
				t.Fatalf("want ErrEgressPolicyMissing, got %v", err)
			}
		})

		t.Run(name+"/policy unavailable", func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, d.manifest), []byte(d.body), 0o644); err != nil {
				t.Fatal(err)
			}
			boom := errors.New("no policy compiled yet")
			b := &Builder{DataDir: t.TempDir()}
			b.SetEgressPolicy(func() (string, string, error) { return "", "", boom })
			_, err := b.installDependencies(t.Context(), "", dir, d.runtime, d.entrypoint)
			if !errors.Is(err, boom) {
				t.Fatalf("the firewall's own error must reach the build log, got %v", err)
			}
		})
	}
}

// TestWithRegistryEnv covers the one thing a jailed build loses for free: the
// operator's ~/.npmrc / pip.conf, because HOME is now a throwaway directory.
// The env-var form of the same configuration (what the Docker deployment uses)
// is forwarded so a private registry keeps working.
func TestWithRegistryEnv(t *testing.T) {
	t.Setenv("NPM_CONFIG_REGISTRY", "https://registry.internal/")
	t.Setenv("npm_config_strict_ssl", "false")
	t.Setenv("PIP_INDEX_URL", "https://pypi.internal/simple")
	t.Setenv("HTTPS_PROXY", "http://proxy.internal:3128")
	t.Setenv("ORVA_ADMIN_KEY", "super-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "super-secret")

	env := withRegistryEnv(map[string]string{"PIP_DISABLE_PIP_VERSION_CHECK": "1"})

	for k, want := range map[string]string{
		"NPM_CONFIG_REGISTRY":           "https://registry.internal/",
		"npm_config_strict_ssl":         "false",
		"PIP_INDEX_URL":                 "https://pypi.internal/simple",
		"HTTPS_PROXY":                   "http://proxy.internal:3128",
		"PIP_DISABLE_PIP_VERSION_CHECK": "1",
	} {
		if env[k] != want {
			t.Errorf("env[%s] = %q, want %q", k, env[k], want)
		}
	}
	// The build jail is not a copy of orvad's environment: a daemon secret
	// that happens to live there must not be handed to a postinstall script.
	for _, k := range []string{"ORVA_ADMIN_KEY", "AWS_SECRET_ACCESS_KEY", "PATH"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s must not be forwarded into the build jail", k)
		}
	}
}

// TestWithRegistryEnv_StepWins: a value the build step sets itself takes
// precedence over the forwarded one.
func TestWithRegistryEnv_StepWins(t *testing.T) {
	t.Setenv("PIP_DISABLE_PIP_VERSION_CHECK", "0")
	if got := withRegistryEnv(map[string]string{"PIP_DISABLE_PIP_VERSION_CHECK": "1"}); got["PIP_DISABLE_PIP_VERSION_CHECK"] != "1" {
		t.Fatalf("step env must win, got %q", got["PIP_DISABLE_PIP_VERSION_CHECK"])
	}
}
