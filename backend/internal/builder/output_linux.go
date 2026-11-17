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
	if cfg.CodeStore == nil || cfg.Budget == nil || cfg.MaxBuilds <= 0 || cfg.MaxBuilds > 1024 ||
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
	for _, path := range []string{cfg.Parent, cfg.MetadataRoot} {
		if err := outputPrivatePath(path); err != nil {
			return nil, err
		}
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(cfg.Parent, &fs); err != nil || fs.Type != 0x63677270 {
		return nil, workloadfence.ErrInvalid
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
	return j.releaseDomain()
}
func (j *outputJob) releaseDomain() error {
	// All descendants are proven dead before removal. Only this fresh parent is owned.
	if j.domain != nil {
		if err := j.domain.Close(); err != nil {
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
	if len(fn.ID) != 36 || len(fn.Entrypoint) > j.owner.cfg.Limits.PathBytes ||
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
	j.parent, err = os.MkdirTemp(j.owner.cfg.Parent, "build-")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(j.parent, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600); err != nil {
		return nil, err
	}
	j.domain, err = workloadfence.CreateUnderBudget(j.owner.cfg.Budget, j.metadata, j.parent)
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
	if err = validateOutputProfile(fn, j.path); err != nil {
		return nil, err
	}
	j.done = make(chan error, 1)
	j.finished = false
	go func() { j.done <- b.checkSyntax(ctx, j.path, fn.Runtime, fn.Entrypoint) }()
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
	manifest, digest, err := j.produce(ctx)
	if err != nil {
		return nil, err
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
	if err = j.releaseDomain(); err != nil {
		return nil, err
	}
	return &BuildResult{Duration: time.Since(start), CodeHash: versionHash(sourceHash, resultABI), Entrypoint: fn.Entrypoint,
		RuntimeArtifact: runtimeID, RuntimeABI: resultABI, CodeArtifact: codeID, SourceArchiveSHA256: sourceHash, AuthoredEntrypoint: fn.Entrypoint}, nil
}

func validateOutputProfile(fn *database.Function, path string) error {
	if !outputPath(fn.Entrypoint, 4096) || fn.Entrypoint == "." {
		return ErrOutputProfile
	}
	ext := filepath.Ext(fn.Entrypoint)
	if (fn.Runtime == "node" && ext != ".js" && ext != ".cjs" && ext != ".mjs") || (fn.Runtime == "python" && ext != ".py") {
		return ErrOutputProfile
	}
	for _, name := range []string{"package.json", "requirements.txt", "tsconfig.json"} {
		if _, err := os.Lstat(filepath.Join(path, name)); !errors.Is(err, os.ErrNotExist) {
			return ErrOutputProfile
		}
	}
	return ValidateArchive(path, fn.Runtime, fn.Entrypoint)
}
