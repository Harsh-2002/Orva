package pool

import (
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// getRemainingTimeInMillis() / ctx.timeoutMs read ORVA_TIMEOUT_MS from the
// worker environment. The proxy used to write it into a per-request map that
// never reached a spawn, so the value user code saw was always the adapter's
// 30000 default no matter how the function was configured. It is a
// per-function property, so it belongs here — a warm worker outlives the
// request that created it and cannot be re-envd per call.
func TestBuildEnvCarriesTimeout(t *testing.T) {
	env := buildEnv(&database.Function{
		ID: "fn-1", Name: "sample", MemoryMB: 128, TimeoutMS: 300000,
	})
	if got := env["ORVA_TIMEOUT_MS"]; got != "300000" {
		t.Errorf("ORVA_TIMEOUT_MS = %q, want %q", got, "300000")
	}
}

// Mirrors the <=0 fallback in InvokeHandler so the sandbox and the enforced
// deadline agree on what "unset" means.
func TestBuildEnvTimeoutFallback(t *testing.T) {
	for _, ms := range []int64{0, -1} {
		env := buildEnv(&database.Function{ID: "fn-1", Name: "sample", TimeoutMS: ms})
		if got := env["ORVA_TIMEOUT_MS"]; got != "30000" {
			t.Errorf("TimeoutMS=%d → ORVA_TIMEOUT_MS = %q, want %q", ms, got, "30000")
		}
	}
}

// User-supplied env vars must not be able to override the platform's own.
func TestBuildEnvPlatformValuesWin(t *testing.T) {
	env := buildEnv(&database.Function{
		ID: "fn-1", Name: "sample", TimeoutMS: 5000,
		EnvVars: map[string]string{"ORVA_TIMEOUT_MS": "999999"},
	})
	if got := env["ORVA_TIMEOUT_MS"]; got != "5000" {
		t.Errorf("ORVA_TIMEOUT_MS = %q, want the function's configured 5000", got)
	}
}
