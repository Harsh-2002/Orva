//go:build linux

// Package artifactfs owns inert, verified filesystem generations and their leases.
package artifactfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/Harsh-2002/Orva/internal/ids"
)

var (
	ErrInvalid     = errors.New("artifactfs: invalid input or state")
	ErrIntegrity   = errors.New("artifactfs: integrity mismatch")
	ErrUnsupported = errors.New("artifactfs: unsupported tree")
	ErrCapacity    = errors.New("artifactfs: capacity exhausted")
	ErrRetired     = errors.New("artifactfs: generation retired")
	ErrClosed      = errors.New("artifactfs: store closed")
	ErrBusy        = errors.New("artifactfs: operation in progress")
	ErrIO          = errors.New("artifactfs: filesystem operation failed")
)

type Kind string

const (
	KindCode    Kind = "code"
	KindRuntime Kind = "runtime"
)

// Limits must all be positive. Bytes reserve logical file bytes, counting each
// hardlink name; they are not a filesystem quota against a misbehaving producer.
type Limits struct {
	Generations, Leases, Staging, Entries, PathBytes, LinkBytes, ManifestBytes int
	Bytes, FileBytes                                                           int64
}

func (l Limits) valid() bool {
	return l.Generations > 0 && l.Generations <= 1<<20 &&
		l.Leases > 0 && l.Leases <= 1<<20 &&
		l.Staging > 0 && l.Staging <= l.Generations &&
		l.Entries > 0 && l.Entries <= 1<<20 &&
		l.PathBytes > 0 && l.PathBytes <= 4096 &&
		l.LinkBytes > 0 && l.LinkBytes <= 4096 &&
		l.ManifestBytes > 0 && l.ManifestBytes <= 64<<20 &&
		l.Bytes > 0 && l.Bytes <= 1<<50 && l.FileBytes > 0 && l.FileBytes <= l.Bytes
}

type Identity struct {
	Generation string
	Kind       Kind
	Owner      string
	Digest     string
}

type phase uint8

const (
	staging phase = iota
	verifying
	failed
	active
	retired
	deleting
	deleted
)

type record struct {
	identity      Identity
	path          string
	reserved      int64
	phase         phase
	refs          int
	isStage       bool
	durable       persistentRecord
	manifestBytes int
	treeOwned     bool
}

type Store struct {
	mu         sync.Mutex
	root       string
	limits     Limits
	lock       *os.File
	records    map[string]*record
	bytes      int64
	stages     int
	leases     int
	closed     bool
	removeAll  func(string) error
	persistent *persistentState
}

// New accepts only an existing empty, private, euid-owned directory without
// symlink components. There is deliberately no recovery or legacy adoption.
func New(root string, limits Limits) (*Store, error) {
	return openStore(root, limits, true)
}

func openStore(root string, limits Limits, empty bool) (*Store, error) {
	if !limits.valid() || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, ErrInvalid
	}
	fd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrIO
	}
	f := os.NewFile(uintptr(fd), root)
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalid
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, ErrBusy
	}
	if err := noXattrs(root); err != nil {
		return nil, err
	}
	if empty {
		names, err := f.Readdirnames(1)
		if len(names) != 0 || (err != nil && !errors.Is(err, io.EOF)) {
			return nil, ErrInvalid
		}
	}
	ok = true
	return &Store{root: root, limits: limits, lock: f, records: make(map[string]*record), removeAll: os.RemoveAll}, nil
}

type Staging struct {
	store *Store
	rec   *record
}

func (s *Store) Begin(ctx context.Context, reservedBytes int64) (*Staging, error) {
	if s.persistent != nil {
		return s.beginPersistent(ctx, reservedBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if reservedBytes <= 0 || reservedBytes > s.limits.Bytes {
		s.mu.Unlock()
		return nil, ErrInvalid
	}
	if len(s.records) >= s.limits.Generations || s.stages >= s.limits.Staging || reservedBytes > s.limits.Bytes-s.bytes {
		s.mu.Unlock()
		return nil, ErrCapacity
	}
	id := ids.New()
	r := &record{identity: Identity{Generation: id}, path: filepath.Join(s.root, "staging-"+id), reserved: reservedBytes, phase: verifying, isStage: true}
	s.records[id], s.bytes, s.stages = r, s.bytes+reservedBytes, s.stages+1
	s.mu.Unlock()
	err := os.Mkdir(r.path, 0700)
	s.mu.Lock()
	if err != nil {
		// Mkdir did not create anything: never remove an unexpected collision.
		delete(s.records, id)
		s.bytes -= reservedBytes
		s.stages--
		s.mu.Unlock()
		return nil, ErrIO
	}
	r.phase = staging
	closed := s.closed
	s.mu.Unlock()
	st := &Staging{store: s, rec: r}
	if err := ctx.Err(); err != nil {
		_ = st.Abort()
		return st, err
	}
	if closed {
		_ = st.Abort()
		return st, ErrClosed
	}
	return st, nil
}

// Path is producer access, not artifact authority. Seal permanently revokes it.
func (st *Staging) Path() (string, error) {
	if st == nil || st.store == nil || st.rec == nil {
		return "", ErrInvalid
	}
	st.store.mu.Lock()
	defer st.store.mu.Unlock()
	if st.rec.phase != staging {
		return "", ErrInvalid
	}
	if st.store.closed {
		return "", ErrClosed
	}
	return st.rec.path, nil
}

func validOwner(kind Kind, owner string) bool {
	switch kind {
	case KindCode:
		if len(owner) != 36 || !ids.IsUUID(owner) {
			return false
		}
		for _, c := range owner {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || c == '-') {
				return false
			}
		}
		return owner[14] >= '1' && owner[14] <= '8' &&
			(owner[19] == '8' || owner[19] == '9' || owner[19] == 'a' || owner[19] == 'b')
	case KindRuntime:
		return owner == "node" || owner == "python"
	}
	return false
}

func (st *Staging) Seal(ctx context.Context, kind Kind, owner string, manifest Manifest, expectedDigest string) (string, error) {
	if st == nil || st.store == nil || st.rec == nil {
		return "", ErrInvalid
	}
	if st.store.persistent != nil {
		return st.sealPersistent(ctx, kind, owner, manifest, expectedDigest)
	}
	s, r := st.store, st.rec
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", ErrClosed
	}
	if r.phase != staging {
		s.mu.Unlock()
		return "", ErrInvalid
	}
	r.phase = verifying
	s.mu.Unlock()
	err := ctx.Err()
	var digest string
	var owned Manifest
	if err == nil {
		if !validOwner(kind, owner) {
			err = ErrInvalid
		} else {
			owned, digest, err = canonical(manifest, s.limits)
		}
	}
	if err == nil && (!validDigest(expectedDigest) || digest != expectedDigest) {
		err = ErrIntegrity
	}
	if err == nil {
		err = verify(ctx, r.path, kind, owned, s.limits, r.reserved)
	}
	if err == nil {
		err = ctx.Err()
	}
	destination := filepath.Join(s.root, "generation-"+r.identity.Generation)
	if err == nil {
		// The exclusive writer never creates/reuses a generation destination.
		if _, e := os.Lstat(destination); !errors.Is(e, os.ErrNotExist) {
			err = ErrIntegrity
		} else if e = os.Rename(r.path, destination); e != nil {
			err = ErrIO
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		r.path = destination
		r.isStage = false
		s.stages--
		r.identity.Kind, r.identity.Owner, r.identity.Digest = kind, owner, digest
		if s.closed || ctx.Err() != nil {
			r.phase = retired
			if ctx.Err() != nil {
				return r.identity.Generation, ctx.Err()
			}
			return r.identity.Generation, ErrClosed
		}
		r.phase = active
		return r.identity.Generation, nil
	}
	r.phase = failed
	return "", err
}

// Abort is also the bounded explicit cleanup retry after a failed Seal.
func (st *Staging) Abort() error {
	if st == nil || st.store == nil || st.rec == nil {
		return ErrInvalid
	}
	return st.store.remove(st.rec, true)
}

type Lease struct {
	store *Store
	rec   *record
	once  *sync.Once
	path  string
	id    Identity
}

func (s *Store) Acquire(generation string) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.persistent != nil && s.persistent.recovered {
		return nil, ErrRetired
	}
	r := s.records[generation]
	if r == nil {
		return nil, ErrInvalid
	}
	if r.phase != active {
		return nil, ErrRetired
	}
	if s.leases >= s.limits.Leases {
		return nil, ErrCapacity
	}
	r.refs++
	s.leases++
	return &Lease{store: s, rec: r, once: new(sync.Once), path: r.path, id: r.identity}, nil
}

func (l *Lease) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *Lease) Identity() Identity {
	if l == nil {
		return Identity{}
	}
	return l.id
}

func (l *Lease) Release() {
	if l == nil || l.store == nil {
		return
	}
	l.once.Do(func() {
		l.store.mu.Lock()
		l.rec.refs--
		l.store.leases--
		l.store.mu.Unlock()
	})
}

func (s *Store) Retire(generation string) error {
	if s.persistent != nil {
		return s.retirePersistent(generation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[generation]
	if r == nil {
		return ErrInvalid
	}
	if r.phase == active {
		r.phase = retired
		return nil
	}
	if r.phase == retired || r.phase == deleting {
		return nil
	}
	return ErrInvalid
}

func (s *Store) Reclaim(generation string) error {
	s.mu.Lock()
	r := s.records[generation]
	s.mu.Unlock()
	if r == nil {
		return ErrInvalid
	}
	return s.remove(r, false)
}

func (s *Store) remove(r *record, abort bool) error {
	if s.persistent != nil {
		return s.removePersistent(r, abort)
	}
	s.mu.Lock()
	if r.phase == deleted {
		s.mu.Unlock()
		return nil
	}
	if r.phase == deleting || r.phase == verifying || r.refs != 0 {
		s.mu.Unlock()
		return ErrBusy
	}
	if (abort && (!r.isStage || (r.phase != staging && r.phase != failed))) || (!abort && (r.isStage || r.phase != retired)) {
		s.mu.Unlock()
		return ErrInvalid
	}
	r.phase = deleting
	p := r.path
	s.mu.Unlock()
	err := s.removeAll(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if r.isStage {
			r.phase = failed
		} else {
			r.phase = retired
		}
		return ErrIO
	}
	r.phase = deleted
	delete(s.records, r.identity.Generation)
	s.bytes -= r.reserved
	if r.isStage {
		s.stages--
	}
	return nil
}

// Close prevents new work but preserves trees and outstanding leases. It keeps
// the directory lock until the caller explicitly cleans all owned records.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if len(s.records) != 0 && (s.persistent == nil || !s.persistent.recovered) {
		return ErrBusy
	}
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	if err != nil {
		return ErrIO
	}
	return nil
}

type PreparedPaths struct {
	state *preparedPathsState
}

type preparedPathsState struct {
	mu            sync.Mutex
	code, runtime *Lease
}

func PreparePaths(codeStore *Store, codeGeneration string, runtimeStore *Store, runtimeGeneration string) (*PreparedPaths, error) {
	if codeStore == nil || runtimeStore == nil {
		return nil, ErrInvalid
	}
	code, err := codeStore.Acquire(codeGeneration)
	if err != nil {
		return nil, err
	}
	runtime, err := runtimeStore.Acquire(runtimeGeneration)
	if err != nil {
		code.Release()
		return nil, err
	}
	if code.Identity().Kind != KindCode || runtime.Identity().Kind != KindRuntime {
		code.Release()
		runtime.Release()
		return nil, ErrInvalid
	}
	return &PreparedPaths{state: &preparedPathsState{code: code, runtime: runtime}}, nil
}

func (p *PreparedPaths) Paths() (code, runtime string) {
	if p == nil || p.state == nil {
		return "", ""
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	return p.state.code.Path(), p.state.runtime.Path()
}

// Identities returns detached store-minted bindings, not policy approval.
func (p *PreparedPaths) Identities() (code, runtime Identity) {
	if p == nil || p.state == nil {
		return Identity{}, Identity{}
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	return p.state.code.Identity(), p.state.runtime.Identity()
}

// Transfer consumes this handle and all its copies as one ownership operation.
// A deferred Release on the old handle cannot release the returned pair.
func (p *PreparedPaths) Transfer() (*PreparedPaths, error) {
	if p == nil || p.state == nil {
		return nil, ErrInvalid
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == nil || s.runtime == nil {
		return nil, ErrInvalid
	}
	next := &PreparedPaths{state: &preparedPathsState{code: s.code, runtime: s.runtime}}
	s.code, s.runtime = nil, nil
	return next, nil
}

func (p *PreparedPaths) Release() {
	if p == nil || p.state == nil {
		return
	}
	s := p.state
	s.mu.Lock()
	code, runtime := s.code, s.runtime
	s.code, s.runtime = nil, nil
	s.mu.Unlock()
	code.Release()
	runtime.Release()
}
