//go:build linux

package workloadspawn

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
)

var (
	ErrFactoryCapacity = errors.New("workloadspawn: prepared capacity exhausted")
	ErrFactoryRetired  = errors.New("workloadspawn: factory retired")
)

// FactoryLimits bound one frozen snapshot and pending/starting plans, not workers.
type FactoryLimits struct {
	MaxPreparedPlans                   int
	MaxPreparedBytes, MaxSnapshotBytes int64
}

func (l FactoryLimits) valid() bool {
	return l.MaxPreparedPlans > 0 && l.MaxPreparedPlans <= 1<<16 &&
		l.MaxPreparedBytes > 0 && l.MaxPreparedBytes <= 1<<30 &&
		l.MaxSnapshotBytes > 0 && l.MaxSnapshotBytes <= 256<<20
}

// ResourceFacts come from exactly the function configuration used by every plan.
type ResourceFacts struct {
	MemoryMB                        int64
	CPUs                            float64
	Timeout                         time.Duration
	MaxConcurrency, RateLimitPerMin int
	ConcurrencyPolicy, NetworkMode  string
	SDK                             bool
}

type PreparedUsage struct {
	Plans            int
	Bytes, PlanBytes int64
}

// Factory copies share retirement, anchors and prepared-capacity accounting.
type Factory struct{ state *factoryState }

type factoryState struct {
	mu                      sync.Mutex
	retired                 atomic.Bool
	base                    *secrets.PreparedArtifactSnapshot
	anchor                  *artifactfs.PreparedPaths
	codeStore, runtimeStore *artifactfs.Store
	code, runtime           artifactfs.Identity
	owner, artifact         string
	gen                     Generation
	resources               ResourceFacts
	limits                  FactoryLimits
	planBytes               int64
	plans                   int
	bytes                   int64
}

// NewFactory validates with NewPlan, then owns frozen strings/maps and an anchor
// pair. Input must be exclusively owned during construction, not concurrently mutated.
func NewFactory(ctx context.Context, prepared *secrets.PreparedArtifactSnapshot,
	codeStore *artifactfs.Store, codeGeneration string, runtimeStore *artifactfs.Store, runtimeGeneration string,
	limits FactoryLimits,
) (*Factory, error) {
	if ctx == nil || !limits.valid() {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cost, err := snapshotCost(prepared, limits.MaxSnapshotBytes)
	if err != nil {
		return nil, err
	}
	if cost > limits.MaxPreparedBytes {
		return nil, ErrFactoryCapacity
	}
	paths, err := artifactfs.PreparePaths(codeStore, codeGeneration, runtimeStore, runtimeGeneration)
	if err != nil {
		return nil, err
	}
	defer paths.Release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := NewPlan(prepared, paths)
	if err != nil {
		return nil, err
	}
	defer plan.Close()
	in := plan.take()
	keep := false
	defer func() {
		if !keep {
			in.paths.Release()
		}
	}()
	fn := ownFunction(in.fn)
	base := &secrets.PreparedArtifactSnapshot{
		Function: fn, Secrets: ownEnvironment(in.secrets),
		Artifact: database.WorkloadArtifactRecord{OwnerID: fn.ID, Generation: strings.Clone(plan.Generation().Token),
			Revision: plan.Generation().Revision, Epoch: plan.Generation().Epoch},
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	code, runtime := in.paths.Identities()
	s := &factoryState{
		base: base, anchor: in.paths, codeStore: codeStore, runtimeStore: runtimeStore,
		code: code, runtime: runtime, owner: fn.ID, artifact: plan.ArtifactRevision(),
		gen: Generation{base.Artifact.Generation, base.Artifact.Revision, base.Artifact.Epoch},
		resources: ResourceFacts{MemoryMB: fn.MemoryMB, CPUs: fn.CPUs, Timeout: time.Duration(fn.TimeoutMS) * time.Millisecond,
			MaxConcurrency: fn.MaxConcurrency, RateLimitPerMin: fn.RateLimitPerMin,
			ConcurrencyPolicy: fn.ConcurrencyPolicy, NetworkMode: fn.NetworkMode, SDK: fn.SDK},
		limits: limits, planBytes: cost,
	}
	keep = true
	return &Factory{state: s}, nil
}

// Prepare reserves before acquiring or cloning. A winning preparation may
// overlap retirement; it grants ownership, not permission to import or execute.
func (f *Factory) Prepare(ctx context.Context) (*SpawnPlan, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f == nil || f.state == nil {
		return nil, ErrFactoryRetired
	}
	s := f.state
	s.mu.Lock()
	if s.retired.Load() {
		s.mu.Unlock()
		return nil, ErrFactoryRetired
	}
	if s.plans >= s.limits.MaxPreparedPlans || s.planBytes > s.limits.MaxPreparedBytes-s.bytes {
		s.mu.Unlock()
		return nil, ErrFactoryCapacity
	}
	s.plans++
	s.bytes += s.planBytes
	base := s.base
	s.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			s.releasePrepared()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths, err := artifactfs.PreparePaths(s.codeStore, s.code.Generation, s.runtimeStore, s.runtime.Generation)
	if err != nil {
		return nil, err
	}
	defer paths.Release()
	plan, err := NewPlan(base, paths)
	if err != nil {
		return nil, err
	}
	plan.state.input.preparedDone = s.releasePrepared
	reserved = false
	ctxErr := ctx.Err()
	s.mu.Lock()
	retired := s.retired.Load()
	s.mu.Unlock()
	if ctxErr != nil || retired {
		plan.Close()
		if ctxErr != nil {
			return nil, ctxErr
		}
		return nil, ErrFactoryRetired
	}
	return plan, nil
}

func (s *factoryState) releasePrepared() {
	s.mu.Lock()
	s.plans--
	s.bytes -= s.planBytes
	s.mu.Unlock()
}

// Retire does not revoke issued plans or workers. Release runs outside the
// factory mutex; callers must also stay outside IAM, pool and registry locks.
func (f *Factory) Retire() {
	if f == nil || f.state == nil {
		return
	}
	s := f.state
	s.mu.Lock()
	s.retired.Store(true)
	anchor := s.anchor
	s.anchor, s.base = nil, nil
	s.mu.Unlock()
	anchor.Release()
}

func (f *Factory) Close() { f.Retire() }

// Retired is an atomic observation, never a current-generation/IAM decision.
func (f *Factory) Retired() bool { return f == nil || f.state == nil || f.state.retired.Load() }

func (f *Factory) Resources() ResourceFacts {
	if f == nil || f.state == nil {
		return ResourceFacts{}
	}
	return f.state.resources
}

func (f *Factory) Owner() string {
	if f == nil || f.state == nil {
		return ""
	}
	return f.state.owner
}

func (f *Factory) ArtifactRevision() string {
	if f == nil || f.state == nil {
		return ""
	}
	return f.state.artifact
}

func (f *Factory) Generation() Generation {
	if f == nil || f.state == nil {
		return Generation{}
	}
	return f.state.gen
}

// MatchesSnapshot runs outside IAM/pool locks and compares frozen owned inputs.
func (f *Factory) MatchesSnapshot(p *secrets.PreparedArtifactSnapshot) bool {
	if f == nil || f.state == nil || p == nil {
		return false
	}
	s := f.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.retired.Load() && s.base != nil &&
		s.gen == (Generation{p.Artifact.Generation, p.Artifact.Revision, p.Artifact.Epoch}) &&
		reflect.DeepEqual(s.base.Function, p.Function) && reflect.DeepEqual(s.base.Secrets, p.Secrets)
}

func (f *Factory) Identities() (artifactfs.Identity, artifactfs.Identity) {
	if f == nil || f.state == nil {
		return artifactfs.Identity{}, artifactfs.Identity{}
	}
	return f.state.code, f.state.runtime
}

func (f *Factory) UsesStores(code, runtime *artifactfs.Store) bool {
	return f != nil && f.state != nil && f.state.codeStore == code && f.state.runtimeStore == runtime
}

// Usage takes the factory mutex; it must not be used by a gate-held observer.
func (f *Factory) Usage() PreparedUsage {
	if f == nil || f.state == nil {
		return PreparedUsage{}
	}
	s := f.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return PreparedUsage{Plans: s.plans, Bytes: s.bytes, PlanBytes: s.planBytes}
}

func (f *Factory) String() string   { return "workloadspawn.Factory(" + f.ArtifactRevision() + ")" }
func (f *Factory) GoString() string { return f.String() }

func functionStrings(f *database.Function) [13]string {
	return [13]string{f.ID, f.Name, f.Description, f.Runtime, f.Entrypoint, f.Image, f.NetworkMode,
		f.ConcurrencyPolicy, f.AuthMode, f.CodeHash, f.RunEntrypoint, f.Status, f.ActiveDeploymentID}
}

func snapshotCost(p *secrets.PreparedArtifactSnapshot, limit int64) (int64, error) {
	if p == nil || p.Function == nil {
		return 0, ErrInvalid
	}
	cost := int64(4096)
	add := func(text string) bool {
		if int64(len(text)) > (limit-cost)/8 {
			return false
		}
		cost += 8 * int64(len(text))
		return true
	}
	if cost > limit || !add(p.Artifact.OwnerID) || !add(p.Artifact.Generation) {
		return 0, ErrFactoryCapacity
	}
	for _, text := range functionStrings(p.Function) {
		if !add(text) {
			return 0, ErrFactoryCapacity
		}
	}
	for _, env := range []map[string]string{p.Function.EnvVars, p.Secrets} {
		if len(env) > secrets.MaxIAMSecretSnapshotCount {
			return 0, ErrInvalid
		}
		for key, value := range env {
			if limit-cost < 512 {
				return 0, ErrFactoryCapacity
			}
			cost += 512
			if !add(key) || !add(value) {
				return 0, ErrFactoryCapacity
			}
		}
	}
	return cost, nil
}

func ownFunction(source *database.Function) *database.Function {
	f := *source
	text := functionStrings(source)
	for i := range text {
		text[i] = strings.Clone(text[i])
	}
	f.ID, f.Name, f.Description, f.Runtime, f.Entrypoint, f.Image, f.NetworkMode = text[0], text[1], text[2], text[3], text[4], text[5], text[6]
	f.ConcurrencyPolicy, f.AuthMode, f.CodeHash, f.RunEntrypoint, f.Status, f.ActiveDeploymentID = text[7], text[8], text[9], text[10], text[11], text[12]
	f.EnvVars = ownEnvironment(source.EnvVars)
	return &f
}

func ownEnvironment(source map[string]string) map[string]string {
	owned := make(map[string]string, len(source))
	for key, value := range source {
		owned[strings.Clone(key)] = strings.Clone(value)
	}
	return owned
}
