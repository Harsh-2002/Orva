//go:build linux

// Package workloadspawn composes inert durable snapshots and leased trees.
// It does not establish current generation, provenance, or IAM approval.
package workloadspawn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/safepath"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/google/uuid"
)

var (
	ErrInvalid = errors.New("workloadspawn: invalid preparation")
	ErrUsed    = errors.New("workloadspawn: plan unavailable")
	ErrSpawn   = errors.New("workloadspawn: spawn failed")
)

// Generation is detached durable metadata, not proof of current authority.
type Generation struct {
	Token    string
	Revision uint64
	Epoch    uint64
}

// SpawnPlan shares one consumption state even when its value is copied.
type SpawnPlan struct{ state *planState }

type planState struct {
	mu       sync.Mutex
	owner    string
	artifact string
	gen      Generation
	input    *inputs
}

type inputs struct {
	fn           *database.Function
	secrets      map[string]string
	paths        *artifactfs.PreparedPaths
	preparedDone func()
}

// NewPlan transfers paths only after validating and cloning every input.
// Callers must exclusively own prepared while constructing the plan.
func NewPlan(prepared *secrets.PreparedArtifactSnapshot, paths *artifactfs.PreparedPaths) (*SpawnPlan, error) {
	if prepared == nil || prepared.Function == nil || paths == nil {
		return nil, ErrInvalid
	}
	r, fn := prepared.Artifact, prepared.Function
	if !canonicalUUID(r.OwnerID) || !canonicalUUID(r.Generation) || fn.ID != r.OwnerID ||
		r.Revision == 0 || r.Revision > math.MaxInt64 || r.Epoch == 0 || r.Epoch > math.MaxInt64 ||
		!validFunction(fn) {
		return nil, ErrInvalid
	}
	code, runtime := paths.Identities()
	if code.Kind != artifactfs.KindCode || code.Owner != fn.ID ||
		runtime.Kind != artifactfs.KindRuntime || runtime.Owner != fn.Runtime ||
		!canonicalUUID(code.Generation) || !canonicalUUID(runtime.Generation) ||
		!validDigest(code.Digest) || !validDigest(runtime.Digest) {
		return nil, ErrInvalid
	}
	env, err := cloneEnvironment(fn.EnvVars)
	if err != nil {
		return nil, err
	}
	secretMap, err := cloneEnvironment(prepared.Secrets)
	if err != nil {
		return nil, err
	}
	frozen := *fn
	frozen.EnvVars = env
	// Explicit fields keep environment values and cosmetic metadata out of the hash.
	public := struct {
		Domain                                  string
		Owner                                   string
		Generation                              Generation
		SourceCodeHash                          string
		Code, Runtime                           artifactfs.Identity
		Name, Language, Authored, Run, Network  string
		Memory, Timeout                         int64
		CPUs                                    float64
		SDK                                     bool
		MaxConcurrency, RateLimit               int
		ConcurrencyPolicy, AuthMode, Deployment string
	}{
		"orva.workloadspawn.v2", fn.ID, Generation{r.Generation, r.Revision, r.Epoch},
		fn.CodeHash, code, runtime, fn.Name, fn.Runtime, fn.Entrypoint,
		fn.RunEntrypoint, fn.NetworkMode, fn.MemoryMB, fn.TimeoutMS, fn.CPUs, fn.SDK,
		fn.MaxConcurrency, fn.RateLimitPerMin, fn.ConcurrencyPolicy, fn.AuthMode, fn.ActiveDeploymentID,
	}
	raw, err := json.Marshal(public)
	if err != nil {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256(raw)
	state := &planState{owner: fn.ID, artifact: hex.EncodeToString(sum[:]), gen: public.Generation}
	owned, err := paths.Transfer()
	if err != nil {
		return nil, ErrInvalid
	}
	state.input = &inputs{fn: &frozen, secrets: secretMap, paths: owned}
	return &SpawnPlan{state: state}, nil
}

func canonicalUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s && id.Variant() == uuid.RFC4122 &&
		id.Version() >= 1 && id.Version() <= 8
}

func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func validEntrypoint(s string) bool {
	if !validText(s, 4096) || strings.Contains(s, `\`) || safepath.Validate(s) != nil {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func validFunction(f *database.Function) bool {
	return f.Status == "active" && f.Version > 0 && validDigest(f.CodeHash) &&
		(f.Runtime == "node" || f.Runtime == "python") &&
		f.Name != "" && validText(f.Name, 256) &&
		validEntrypoint(f.Entrypoint) && (f.RunEntrypoint == "" || validEntrypoint(f.RunEntrypoint)) &&
		f.MemoryMB > 0 && f.MemoryMB <= math.MaxInt64/(3<<20) && f.MemoryMB <= int64(math.MaxInt) &&
		f.TimeoutMS > 0 && f.TimeoutMS <= math.MaxInt64/int64(time.Millisecond)-5000 &&
		!math.IsNaN(f.CPUs) && !math.IsInf(f.CPUs, 0) && f.CPUs >= 0 && f.CPUs <= float64(math.MaxInt32)/1000 &&
		database.ValidNetworkMode(f.NetworkMode) && database.ValidAuthMode(f.AuthMode) &&
		database.ValidConcurrencyPolicy(f.ConcurrencyPolicy) && f.MaxConcurrency >= 0 && f.RateLimitPerMin >= 0 &&
		validText(f.ActiveDeploymentID, 256)
}

func cloneEnvironment(source map[string]string) (map[string]string, error) {
	if len(source) > secrets.MaxIAMSecretSnapshotCount {
		return nil, ErrInvalid
	}
	out := make(map[string]string, len(source))
	budget := secrets.MaxIAMSecretSnapshotBytes
	for key, value := range source {
		if key == "" || !validText(key, budget) || strings.Contains(key, "=") || len(key) > budget {
			return nil, ErrInvalid
		}
		budget -= len(key)
		if !validText(value, budget) || len(value) > budget {
			return nil, ErrInvalid
		}
		budget -= len(value)
		out[key] = value
	}
	return out, nil
}

func (p *SpawnPlan) Owner() string {
	if p == nil || p.state == nil {
		return ""
	}
	return p.state.owner
}

func (p *SpawnPlan) ArtifactRevision() string {
	if p == nil || p.state == nil {
		return ""
	}
	return p.state.artifact
}

func (p *SpawnPlan) Generation() Generation {
	if p == nil || p.state == nil {
		return Generation{}
	}
	return p.state.gen
}

func (p *SpawnPlan) String() string   { return "workloadspawn.SpawnPlan(" + p.ArtifactRevision() + ")" }
func (p *SpawnPlan) GoString() string { return p.String() }

func (p *SpawnPlan) take() *inputs {
	if p == nil || p.state == nil {
		return nil
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	in := s.input
	s.input = nil
	return in
}

// Close releases an unused plan. It cannot release a concurrently spawned worker.
func (p *SpawnPlan) Close() {
	if in := p.take(); in != nil {
		in.paths.Release()
		if in.preparedDone != nil {
			in.preparedDone()
		}
	}
}

// Spawn consumes the plan, even on cancellation or failure. Template is trusted
// host configuration; its seccomp policy must already suit the frozen network mode.
func (p *SpawnPlan) Spawn(ctx context.Context, template sandbox.ExecConfig) (*sandbox.Worker, error) {
	in := p.take()
	if in == nil {
		return nil, ErrUsed
	}
	if in.preparedDone != nil {
		defer in.preparedDone()
	}
	defer in.paths.Release()
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f := in.fn
	cfg := template
	cfg.FunctionID, cfg.ArtifactRevision = f.ID, p.ArtifactRevision()
	cfg.VerifiedPaths, cfg.CodeDir, cfg.RootfsDir = in.paths, "", ""
	cfg.Language, cfg.Stdin = sandbox.Language(f.Runtime), nil
	cfg.MemoryMB, cfg.MaxCPUs = int(f.MemoryMB), f.CPUs
	cfg.Timeout, cfg.NetworkMode = time.Duration(f.TimeoutMS)*time.Millisecond, f.NetworkMode
	cfg.Env = in.fn.EnvVars
	for key, value := range in.secrets {
		cfg.Env[key] = value
	}
	for key := range cfg.Env {
		switch strings.ToUpper(key) {
		case "ORVA_INTERNAL_TOKEN", "ORVA_API_BASE", "ORVA_SDK_FD", "ORVA_SDK_PROTO":
			delete(cfg.Env, key)
		}
	}
	cfg.Env["ORVA_FUNCTION_ID"], cfg.Env["ORVA_FUNCTION_NAME"] = f.ID, f.Name
	cfg.Env["ORVA_MEMORY_MB"], cfg.Env["ORVA_TIMEOUT_MS"] = fmt.Sprint(f.MemoryMB), fmt.Sprint(f.TimeoutMS)
	cfg.Env["ORVA_ENTRYPOINT"] = f.Entrypoint
	if f.RunEntrypoint != "" {
		cfg.Env["ORVA_ENTRYPOINT"] = f.RunEntrypoint
	}
	if !f.SDK {
		cfg.SDKHandler = nil
	} else if cfg.SDKHandler == nil {
		return nil, ErrSpawn
	}
	worker, err := sandbox.Spawn(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrSpawn
	}
	if err := ctx.Err(); err != nil {
		_ = worker.Kill()
		return nil, err
	}
	return worker, nil
}
