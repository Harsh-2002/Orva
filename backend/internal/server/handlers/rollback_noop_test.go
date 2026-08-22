package handlers

import (
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// Rollback used to refuse whenever the target deployment's code hash matched
// the running one, on the theory that it was a no-op. It is not: rollback
// restores the deployment's SNAPSHOT (env vars, memory, cpus, timeout, network
// and auth mode, rate limit, concurrency) as well as its code.
//
// Redeploying unchanged code is the most ordinary thing an operator does — a
// settings tweak, a CI re-run, a retry after a transient failure. Each one
// writes a new deployment row with a new version and the same code hash. Every
// historical row on such a function therefore matched the running hash, and
// rollback was refused across the board with "this version is already active",
// while the dashboard happily offered the button (it decides "active" by
// version number, not by hash, so the two disagreed).
//
// The discriminating case is sameCodeDifferentSettings: it is the state the old
// guard called a no-op and the user could never roll back to.
func TestRollbackIsNoOpOnlyWhenNothingWouldChange(t *testing.T) {
	base := func() *database.Function {
		return &database.Function{
			CodeHash:          "aaaa",
			EnvVars:           map[string]string{"LOG_LEVEL": "info"},
			MemoryMB:          128,
			CPUs:              0.5,
			TimeoutMS:         30000,
			NetworkMode:       "none",
			AuthMode:          "none",
			RateLimitPerMin:   0,
			MaxConcurrency:    0,
			ConcurrencyPolicy: "queue",
		}
	}
	// A snapshot that matches base() exactly.
	matching := func() *database.DeploymentSnapshot {
		return database.SnapshotFromFunction(base())
	}

	tests := []struct {
		name     string
		hash     string
		snapshot func() *database.DeploymentSnapshot
		wantNoOp bool
		why      string
	}{
		{
			name: "differentCode", hash: "bbbb", snapshot: matching, wantNoOp: false,
			why: "different code is always a real change",
		},
		{
			name: "sameCodeSameSettings", hash: "aaaa", snapshot: matching, wantNoOp: true,
			why: "nothing at all would change",
		},
		{
			name: "sameCodeDifferentMemory", hash: "aaaa", wantNoOp: false,
			why: "rollback restores the memory limit, so this changes the function",
			snapshot: func() *database.DeploymentSnapshot {
				s := matching()
				s.MemoryMB = 256
				return s
			},
		},
		{
			name: "sameCodeDifferentEnvValue", hash: "aaaa", wantNoOp: false,
			why: "rollback restores env vars",
			snapshot: func() *database.DeploymentSnapshot {
				s := matching()
				s.EnvVars = map[string]string{"LOG_LEVEL": "debug"}
				return s
			},
		},
		{
			name: "sameCodeExtraEnvKey", hash: "aaaa", wantNoOp: false,
			why: "an added env var is a change even when every other field matches",
			snapshot: func() *database.DeploymentSnapshot {
				s := matching()
				s.EnvVars["FEATURE_X"] = "1"
				return s
			},
		},
		{
			name: "sameCodeDifferentAuthMode", hash: "aaaa", wantNoOp: false,
			why: "auth mode is part of the restored state and is security-relevant",
			snapshot: func() *database.DeploymentSnapshot {
				s := matching()
				s.AuthMode = "platform_key"
				return s
			},
		},
		{
			name: "sameCodeNoSnapshot", hash: "aaaa", snapshot: func() *database.DeploymentSnapshot { return nil },
			wantNoOp: true,
			why:      "a legacy row carries no settings, so a matching hash is the whole story",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rollbackIsNoOp(tc.hash, tc.snapshot(), base())
			if got != tc.wantNoOp {
				t.Errorf("rollbackIsNoOp() = %v, want %v: %s", got, tc.wantNoOp, tc.why)
			}
		})
	}
}

// The guard is only as good as the equality behind it, and "we do not know"
// must never read as "they match".
func TestSnapshotEqualTreatsNilAsUnknown(t *testing.T) {
	populated := &database.DeploymentSnapshot{EnvVars: map[string]string{}, MemoryMB: 128}
	var nilSnap *database.DeploymentSnapshot

	if nilSnap.Equal(populated) {
		t.Error("a nil snapshot compared equal to a populated one; unknown settings would read as matching settings")
	}
	if populated.Equal(nilSnap) {
		t.Error("a populated snapshot compared equal to nil")
	}
	if !nilSnap.Equal(nil) {
		t.Error("two nil snapshots should compare equal")
	}
}

// EnvVars is the one reference field on the snapshot, so it is the one that can
// alias or compare wrongly.
func TestSnapshotEqualComparesEnvContents(t *testing.T) {
	a := &database.DeploymentSnapshot{EnvVars: map[string]string{"A": "1", "B": "2"}}
	b := &database.DeploymentSnapshot{EnvVars: map[string]string{"B": "2", "A": "1"}}
	if !a.Equal(b) {
		t.Error("same env pairs in a different insertion order compared unequal")
	}

	c := &database.DeploymentSnapshot{EnvVars: map[string]string{"A": "1"}}
	if a.Equal(c) {
		t.Error("a snapshot with an extra env key compared equal to one without it")
	}

	empty := &database.DeploymentSnapshot{EnvVars: map[string]string{}}
	nilEnv := &database.DeploymentSnapshot{}
	if !empty.Equal(nilEnv) {
		t.Error("an empty env map and a nil env map describe the same state and should compare equal")
	}
}
