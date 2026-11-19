//go:build linux

package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/google/uuid"
)

var (
	ErrOutputCapacity = errors.New("builder: managed output capacity exhausted")
	ErrOutputPending  = errors.New("builder: managed output cleanup pending")
	ErrOutputProfile  = errors.New("builder: unsupported managed output profile")
)

// OutputConfig is host-only provisioning, not an author or policy input.
type OutputConfig struct {
	CodeStore                                     *artifactfs.Store
	Budget                                        *workloadfence.Budget
	ParentDomain                                  *workloadfence.Domain
	Parent, MetadataRoot                          string
	Limits                                        artifactfs.Limits
	ReservedBytes, MaxReservedBytes, ArchiveBytes int64
	MaxBuilds                                     int
	BuildTimeout, CleanupTimeout                  time.Duration
}

// OutputOwner copies share finite active/quarantine capacity. Retain until Close succeeds.
type OutputOwner struct{ s *outputState }
type outputState struct {
	mu     sync.Mutex
	cfg    OutputConfig
	root   *os.File
	closed bool
	jobs   map[*outputJob]bool // true only while an explicit cleanup retry owns the job
}
type OutputUsage struct {
	Jobs, Pending int
	ReservedBytes int64
}
type outputJob struct {
	owner                              *outputState
	stage                              *artifactfs.Staging
	path, metadata, parent, generation string
	source                             string
	domain                             *workloadfence.Domain
	proof                              *workloadfence.Proof
	runtime                            *artifactfs.Lease
	done                               chan error
	finished                           bool
	pending                            bool // protected by owner.mu
}

func WithManagedOutput(owner *OutputOwner) Option {
	return func(b *Builder) { b.managedOutputEnabled, b.outputOwner = true, owner }
}

func NewOutputOwner(cfg OutputConfig) (*OutputOwner, error) {
	typed := cfg.ParentDomain != nil
	if (typed && (cfg.Budget != nil || cfg.Parent != "")) ||
		(!typed && (cfg.Budget == nil || cfg.Parent == "")) {
		return nil, artifactfs.ErrInvalid
	}
	if cfg.CodeStore == nil || cfg.MaxBuilds <= 0 || cfg.MaxBuilds > 1024 ||
		cfg.ReservedBytes <= 0 || cfg.ReservedBytes > cfg.Limits.Bytes ||
		cfg.MaxReservedBytes < cfg.ReservedBytes || cfg.MaxReservedBytes > 1<<40 ||
		cfg.ArchiveBytes <= 0 || cfg.ArchiveBytes > 1<<30 ||
		cfg.BuildTimeout <= 0 || cfg.BuildTimeout > time.Hour ||
		cfg.CleanupTimeout <= 0 || cfg.CleanupTimeout > time.Minute {
		return nil, artifactfs.ErrInvalid
	}
	if _, err := artifactfs.Digest(artifactfs.Manifest{Version: 1, Entries: []artifactfs.Entry{{Path: ".", Type: artifactfs.TypeDirectory, Mode: 0700}}}, cfg.Limits); err != nil {
		return nil, err
	}
	paths := []string{cfg.MetadataRoot}
	if !typed {
		paths = append(paths, cfg.Parent)
	}
	for _, path := range paths {
		if err := outputPrivatePath(path); err != nil {
			return nil, err
		}
	}
	if !typed {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(cfg.Parent, &fs); err != nil || fs.Type != 0x63677270 {
			return nil, workloadfence.ErrInvalid
		}
	} else {
		parent := *cfg.ParentDomain
		cfg.ParentDomain = &parent
	}
	root, err := os.Open(cfg.MetadataRoot)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			root.Close()
		}
	}()
	info, err := root.Stat()
	if err != nil || info.Mode() != os.ModeDir|0700 {
		return nil, artifactfs.ErrInvalid
	}
	if err := syscall.Flock(int(root.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, err
	}
	names, err := root.Readdirnames(1)
	if len(names) != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return nil, artifactfs.ErrInvalid
	}
	ok = true
	cfg.Parent, cfg.MetadataRoot = strings.Clone(cfg.Parent), strings.Clone(cfg.MetadataRoot)
	return &OutputOwner{s: &outputState{cfg: cfg, root: root, jobs: make(map[*outputJob]bool)}}, nil
}

func outputPrivatePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return artifactfs.ErrInvalid
	}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return artifactfs.ErrInvalid
		}
		if p == path {
			st := info.Sys().(*syscall.Stat_t)
			if st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
				return artifactfs.ErrInvalid
			}
		}
		if p == "/" {
			return nil
		}
	}
}

func (o *OutputOwner) Usage() OutputUsage {
	if o == nil || o.s == nil {
		return OutputUsage{}
	}
	s := o.s
	s.mu.Lock()
	defer s.mu.Unlock()
	u := OutputUsage{Jobs: len(s.jobs), ReservedBytes: int64(len(s.jobs)) * s.cfg.ReservedBytes}
	for j := range s.jobs {
		if j.pending {
			u.Pending++
		}
	}
	return u
}
func (o *OutputOwner) Close() error {
	if o == nil || o.s == nil {
		return artifactfs.ErrInvalid
	}
	s := o.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if len(s.jobs) != 0 {
		return ErrOutputPending
	}
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return err
}
func (s *outputState) reserve() (*outputJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, artifactfs.ErrClosed
	}
	if len(s.jobs) >= s.cfg.MaxBuilds || int64(len(s.jobs)) >= s.cfg.MaxReservedBytes/s.cfg.ReservedBytes {
		return nil, ErrOutputCapacity
	}
	j := &outputJob{owner: s, finished: true}
	s.jobs[j] = false
	return j, nil
}
func (j *outputJob) pendingCleanup() {
	s := j.owner
	s.mu.Lock()
	j.pending = true
	s.jobs[j] = false
	s.mu.Unlock()
}
func (j *outputJob) releaseSlot() {
	s := j.owner
	s.mu.Lock()
	delete(s.jobs, j)
	s.mu.Unlock()
}

// RetryCleanup never promotes quarantined output, even if its original build succeeded.
func (o *OutputOwner) RetryCleanup(ctx context.Context) error {
	if o == nil || o.s == nil || ctx == nil {
		return artifactfs.ErrInvalid
	}
	s := o.s
	s.mu.Lock()
	var jobs []*outputJob
	for j, busy := range s.jobs {
		if j.pending && !busy {
			s.jobs[j] = true
			jobs = append(jobs, j)
		}
	}
	s.mu.Unlock()
	var result error
	for _, j := range jobs {
		if err := j.dispose(ctx); err != nil {
			j.pendingCleanup()
			result = errors.Join(result, err)
		} else {
			j.releaseSlot()
		}
	}
	return result
}

func (j *outputJob) fence(ctx context.Context) error {
	if j.domain == nil {
		return nil
	} // No Start capability was issued if Create failed.
	proof, err := j.domain.Fence(ctx)
	if err != nil {
		return err
	}
	if err := j.domain.Check(ctx, proof); err != nil {
		return err
	}
	j.proof = proof
	if !j.finished {
		select {
		case <-j.done:
			j.finished = true
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (j *outputJob) dispose(ctx context.Context) error {
	cleanup, cancel := context.WithTimeout(ctx, j.owner.cfg.CleanupTimeout)
	defer cancel()
	if err := j.fence(cleanup); err != nil {
		return fmt.Errorf("%w: %w", ErrOutputPending, err)
	}
	if j.generation != "" {
		if err := j.owner.cfg.CodeStore.Retire(j.generation); err != nil {
			return err
		}
		if err := j.owner.cfg.CodeStore.Reclaim(j.generation); err != nil {
			return err
		}
		j.generation = ""
	} else if j.stage != nil {
		if err := j.stage.Abort(); err != nil {
			return err
		}
		j.stage = nil
	}
	return j.releaseDomain(cleanup)
}
func (j *outputJob) releaseDomain(ctx context.Context) error {
	// All descendants are proven dead before removal. Only this fresh parent is owned.
	if j.domain != nil {
		var err error
		if parent := j.owner.cfg.ParentDomain; parent != nil {
			err = parent.ReclaimChild(ctx, j.domain, j.proof)
		} else {
			err = j.domain.Close()
		}
		if err != nil {
			return err
		}
		j.domain = nil
	}
	if j.parent != "" {
		if err := removeOutputGroups(j.parent, 0); err != nil {
			return err
		}
		j.parent = ""
	}
	if j.metadata != "" {
		if err := os.RemoveAll(j.metadata); err != nil {
			return err
		}
		j.metadata = ""
	}
	if j.source != "" {
		if err := os.Remove(j.source); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		j.source = ""
	}
	if j.runtime != nil {
		j.runtime.Release()
		j.runtime = nil
	}
	return nil
}
func removeOutputGroups(path string, depth int) error {
	if depth > 8 {
		return artifactfs.ErrCapacity
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		entries, err := f.ReadDir(64)
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				return artifactfs.ErrIntegrity
			}
			if e.IsDir() {
				if err := removeOutputGroups(filepath.Join(path, e.Name()), depth+1); err != nil {
					return err
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	return os.Remove(path)
}

func (b *Builder) buildManaged(ctx context.Context, fn *database.Function, archive string) (result *BuildResult, err error) {
	if ctx == nil || fn == nil || b.outputOwner == nil || b.outputOwner.s == nil || !b.runtimeBindingEnabled || b.BuildFunc != nil || b.checkSyntaxHook != nil {
		return nil, artifactfs.ErrInvalid
	}
	jobBuilder := *b
	b = &jobBuilder
	j, err := b.outputOwner.s.reserve()
	if err != nil {
		return nil, err
	}
	defer func() {
		if result != nil {
			j.releaseSlot()
			return
		}
		if cleanupErr := j.dispose(context.Background()); cleanupErr != nil {
			j.pendingCleanup()
			err = errors.Join(err, fmt.Errorf("%w: %w", ErrOutputPending, cleanupErr))
		} else {
			j.releaseSlot()
		}
	}()
	id, idErr := uuid.Parse(fn.ID)
	if idErr != nil || id.String() != fn.ID || id.Variant() != uuid.RFC4122 || id.Version() < 1 || id.Version() > 8 ||
		len(fn.Entrypoint) > j.owner.cfg.Limits.PathBytes ||
		(fn.Runtime != "node" && fn.Runtime != "python") {
		return nil, artifactfs.ErrInvalid
	}
	fn = &database.Function{ID: strings.Clone(fn.ID), Runtime: strings.Clone(fn.Runtime), Entrypoint: strings.Clone(fn.Entrypoint)}
	ctx, cancel := context.WithTimeout(ctx, j.owner.cfg.BuildTimeout)
	defer cancel()
	start := time.Now()
	var resultABI string
	j.runtime, resultABI, err = b.captureRuntime(ctx, fn)
	if err != nil {
		return nil, err
	}
	runtimeID := j.runtime.Identity()
	if b.buildDomain != nil {
		return nil, errors.New("managed output rejects provider-supplied domain")
	}
	j.metadata, err = os.MkdirTemp(j.owner.cfg.MetadataRoot, "build-")
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(j.metadata, 0700); err != nil {
		return nil, err
	}
	if parent := j.owner.cfg.ParentDomain; parent != nil {
		j.domain, err = parent.NewChild(ctx, j.metadata)
	} else {
		j.parent, err = os.MkdirTemp(j.owner.cfg.Parent, "build-")
		if err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(j.parent, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600); err != nil {
			return nil, err
		}
		j.domain, err = workloadfence.CreateUnderBudget(j.owner.cfg.Budget, j.metadata, j.parent)
	}
	if err != nil {
		return nil, err
	}
	b.buildDomain = j.domain
	j.stage, err = j.owner.cfg.CodeStore.Begin(ctx, j.owner.cfg.ReservedBytes)
	if err != nil {
		return nil, err
	}
	j.path, err = j.stage.Path()
	if err != nil {
		return nil, err
	}
	sourceHash, err := j.extract(ctx, archive)
	if err != nil {
		return nil, err
	}
	profile, err := managedProfile(fn, j.path)
	if err != nil {
		return nil, err
	}
	j.done = make(chan error, 1)
	j.finished = false
	go func() {
		j.done <- b.runManagedSteps(ctx, fn, j.path, profile)
	}()
	select {
	case err = <-j.done:
		j.finished = true
	case <-ctx.Done():
		err = ctx.Err()
	}
	cleanup, cancelCleanup := context.WithTimeout(context.Background(), j.owner.cfg.CleanupTimeout)
	fenceErr := j.fence(cleanup)
	cancelCleanup()
	if fenceErr != nil {
		return nil, errors.Join(err, fenceErr)
	}
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err := j.attachSource(ctx, sourceHash); err != nil {
		return nil, fmt.Errorf("attach original build source: %w", err)
	}
	manifest, digest, err := j.produce(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{fn.Entrypoint, profile.entrypoint} {
		found := false
		for _, e := range manifest.Entries {
			if e.Path == name && e.Type == artifactfs.TypeFile {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("managed build entrypoint %s is not a regular file", name)
		}
	}
	j.generation, err = j.stage.Seal(ctx, artifactfs.KindCode, fn.ID, manifest, digest)
	if err != nil {
		return nil, err
	}
	lease, err := j.owner.cfg.CodeStore.Acquire(j.generation)
	if err != nil {
		return nil, err
	}
	codeID := lease.Identity()
	lease.Release()
	release, cancelRelease := context.WithTimeout(context.Background(), j.owner.cfg.CleanupTimeout)
	defer cancelRelease()
	if err = j.releaseDomain(release); err != nil {
		return nil, err
	}
	if profile.compile {
		logTSCCompiled(b, profile.entrypoint)
	}
	return &BuildResult{Duration: time.Since(start), CodeHash: versionHash(sourceHash, resultABI), Entrypoint: profile.entrypoint,
		RuntimeArtifact: runtimeID, RuntimeABI: resultABI, CodeArtifact: codeID, SourceArchiveSHA256: sourceHash, AuthoredEntrypoint: fn.Entrypoint}, nil
}

type managedBuildProfile struct {
	entrypoint       string
	install, compile bool
}

func managedProfile(fn *database.Function, path string) (p managedBuildProfile, err error) {
	if !outputPath(fn.Entrypoint, 4096) || fn.Entrypoint == "." {
		return p, ErrOutputProfile
	}
	ext := filepath.Ext(fn.Entrypoint)
	if (fn.Runtime == "node" && ext != ".js" && ext != ".cjs" && ext != ".mjs" && ext != ".ts") ||
		(fn.Runtime == "python" && ext != ".py") {
		return p, ErrOutputProfile
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return p, err
	}
	defer root.Close()
	info, err := root.Lstat(fn.Entrypoint)
	if err != nil || !info.Mode().IsRegular() {
		return p, fmt.Errorf("%w: authored entrypoint must be a regular file", ErrOutputProfile)
	}
	p.entrypoint = fn.Entrypoint
	for _, name := range []string{"package.json", "requirements.txt", "tsconfig.json"} {
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || (name == "requirements.txt") != (fn.Runtime == "python") {
			return p, fmt.Errorf("%w: %s must be a regular manifest for the selected runtime", ErrOutputProfile, name)
		}
		if name == "tsconfig.json" {
			p.compile = true
		} else {
			p.install = true
		}
	}
	if p.compile {
		_, p.entrypoint, err = ManagedTypeScriptOutput(root, fn.Entrypoint)
		if err != nil {
			return p, err
		}
	} else if ext == ".ts" {
		return p, fmt.Errorf("%w: TypeScript requires tsconfig.json", ErrOutputProfile)
	}
	return p, ValidateArchive(path, fn.Runtime, fn.Entrypoint)
}

// ManagedTypeScriptOutput derives the same compiler target before building or
// projecting authored files from a legacy TypeScript deployment.
func ManagedTypeScriptOutput(root *os.Root, entrypoint string) (dir, entry string, err error) {
	if err := verifyTypeScriptDeclared(filepath.Join(root.Name(), "package.json")); err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrOutputProfile, err)
	}
	dir = readTSConfigOutDir(root)
	return dir, compiledEntrypoint(entrypoint, dir), nil
}

func (b *Builder) runManagedSteps(ctx context.Context, fn *database.Function, path string, profile managedBuildProfile) error {
	if err := b.checkSyntax(ctx, path, fn.Runtime, fn.Entrypoint); err != nil {
		return err
	}
	if profile.install {
		install := b.installNPM
		if fn.Runtime == "python" {
			install = b.installPip
		}
		if err := install(ctx, fn.ID, path); err != nil {
			return err
		}
	}
	if profile.compile {
		return b.compileTypeScript(ctx, fn.ID, path)
	}
	return nil
}
