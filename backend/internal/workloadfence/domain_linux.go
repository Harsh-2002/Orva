//go:build linux

// Package workloadfence owns an inert, persistent cgroup-v2 process epoch.
package workloadfence

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	ErrInvalid     = errors.New("workload fence: invalid state")
	ErrClosed      = errors.New("workload fence: entry closed")
	ErrBusy        = errors.New("workload fence: owner busy")
	ErrIntegrity   = errors.New("workload fence: identity mismatch")
	ErrUnsupported = errors.New("workload fence: requested controllers unavailable")
	ErrCapacity    = errors.New("workload fence: nested domain capacity exhausted")
)

const cgroup2Magic = 0x63677270

type objectID struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type record struct {
	Version int      `json:"version"`
	Epoch   string   `json:"epoch"`
	Boot    string   `json:"boot"`
	Root    objectID `json:"root"`
	Parent  objectID `json:"parent"`
	Group   objectID `json:"group"`
	Closed  bool     `json:"closed"`
	Lineage lineage  `json:"lineage,omitzero"`
}

// Domain handles share state even when copied. No cgroup descriptor is exported.
type Domain struct{ s *state }

type state struct {
	gate         chan struct{}
	root         *os.File
	group        *os.File
	supervisors  *os.File
	jails        *os.File
	controllers  uint8
	parent       string
	path         string
	rec          record
	recovering   bool
	closed       atomic.Bool
	disposed     bool
	persistFault func(string) error
	budget       *budgetState
	parentState  *state
	children     map[*state]struct{}
	depth        int
	descendants  int // instance-root accounting, protected by its gate
	groupCreated bool
	deleting     bool
	reclaimed    bool
}

// Proof is minted only after durable entry closure and kernel populated=0.
// It does not authorize artifact activation, reclamation, or IAM admission.
type Proof struct {
	s   *state
	rec record
}

// Create requires an empty private metadata root and an already delegated,
// host-trusted cgroup-v2 parent. It never moves this process or edits ancestors.
func Create(root, parent string) (*Domain, error) {
	return create(root, parent, nil)
}

func create(root, parent string, budget *budgetState) (*Domain, error) {
	s, err := openOwnerWithBudget(root, parent, budget)
	if err != nil {
		return nil, err
	}
	return initialize(s)
}

func initialize(s *state) (result *Domain, err error) {
	ok := false
	defer func() {
		if !ok {
			if s.parentState != nil && s.groupCreated {
				s.closed.Store(true)
				result = &Domain{s}
			} else {
				s.dispose()
			}
		}
	}()
	names, err := s.root.Readdirnames(1)
	if err != io.EOF || len(names) != 0 {
		return nil, ErrInvalid
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	s.rec.Version = 1
	if s.parentState != nil {
		s.rec.Version = 2
		s.rec.Lineage = s.parentState.childLineage()
	}
	s.rec.Epoch = hex.EncodeToString(nonce[:])
	s.rec.Boot, err = bootID()
	if err != nil {
		return nil, err
	}
	s.rec.Root, err = identity(s.root)
	if err != nil {
		return nil, err
	}
	p, err := openCgroup(s.parent)
	if err != nil {
		return nil, err
	}
	s.rec.Parent, err = identity(p)
	p.Close()
	if err != nil {
		return nil, err
	}
	s.path = filepath.Join(s.parent, "epoch-"+s.rec.Epoch)
	if err := os.Mkdir(s.path, 0o700); err != nil {
		return nil, fmt.Errorf("create epoch: %w", err)
	}
	s.groupCreated = true
	s.group, err = openCgroup(s.path)
	if err != nil {
		return nil, err
	}
	s.rec.Group, err = identity(s.group)
	if err != nil {
		return nil, err
	}
	if err := prepareTree(s.path); err != nil {
		return nil, err
	}
	s.supervisors, err = openCgroup(filepath.Join(s.path, "supervisors"))
	if err != nil {
		return nil, err
	}
	s.jails, err = openCgroup(filepath.Join(s.path, "jails"))
	if err != nil {
		return nil, err
	}
	// No process can enter through this package before the association is durable.
	if err := s.persist(s.rec); err != nil {
		return nil, err
	}
	ok = true
	return &Domain{s}, nil
}

// OpenRecovering verifies the exact same-boot epoch. Reopened domains never
// allow Start, even when the crashed owner's record had entry open.
func OpenRecovering(root, parent string) (*Domain, error) {
	s, err := openOwner(root, parent)
	if err != nil {
		return nil, err
	}
	s.closed.Store(true)
	s.recovering = true
	ok := false
	defer func() {
		if !ok {
			s.dispose()
		}
	}()
	names, err := s.root.Readdirnames(2)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(names) != 1 || names[0] != "epoch.json" {
		return nil, ErrInvalid
	}
	s.rec, err = readRecord(filepath.Join(root, "epoch.json"))
	if err != nil {
		return nil, err
	}
	if s.rec.Version != 1 {
		return nil, ErrUnsupported
	}
	s.path = filepath.Join(parent, "epoch-"+s.rec.Epoch)
	s.group, err = openCgroup(s.path)
	if err != nil {
		return nil, err
	}
	if err := s.verifyIdentity(); err != nil {
		return nil, err
	}
	if err := s.verifyRecord(); err != nil {
		return nil, err
	}
	ok = true
	return &Domain{s}, nil
}

// Start enrolls the HOST command with CLONE_INTO_CGROUP, before exec. Callers
// exclusively own cmd during this call and must not migrate its descendants
// outside the epoch. A supplied JailParent is revalidated under the entry gate.
// Run outside IAM/pool locks. There is no post-Start PID-write fallback.
func (d *Domain) Start(ctx context.Context, cmd *exec.Cmd, parent ...*JailParent) error {
	if d == nil || d.s == nil || cmd == nil || ctx == nil {
		return ErrInvalid
	}
	if len(parent) > 1 {
		return ErrInvalid
	}
	s := d.s
	unlock, err := s.enterLineage(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	if s.disposed || s.closed.Load() || s.recovering {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.verifyLineage(); err != nil {
		s.closed.Store(true)
		return err
	}
	if len(parent) == 1 {
		if err := s.checkJailParent(parent[0]); err != nil {
			return err
		}
	}
	old := cmd.SysProcAttr
	var attr syscall.SysProcAttr
	if old != nil {
		attr = *old
		if old.UseCgroupFD {
			return ErrInvalid
		}
	}
	attr.UseCgroupFD = true
	attr.CgroupFD = int(s.supervisors.Fd())
	cmd.SysProcAttr = &attr
	defer func() { cmd.SysProcAttr = old }()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("clone into epoch: %w", err)
	}
	return nil
}

// Fence permanently closes entry before killing the entire epoch, including
// descendants in nested cgroups. Cancellation or I/O failure returns no proof;
// entry stays closed. Interrupted temporary metadata requires explicit host
// investigation; it is never automatically discarded or overwritten.
func (d *Domain) Fence(ctx context.Context) (*Proof, error) {
	if d == nil || d.s == nil || ctx == nil {
		return nil, ErrInvalid
	}
	s := d.s
	s.closed.Store(true)
	unlock, err := s.enterLineage(ctx, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if s.disposed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.verifyLineage(); err != nil {
		return nil, err
	}
	closed := s.rec
	closed.Closed = true
	if err := s.persist(closed); err != nil {
		return nil, err
	}
	s.rec = closed
	if err := writeControl(s.path, "cgroup.kill", "1"); err != nil {
		return nil, err
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		empty, err := unpopulated(s.path)
		if err != nil {
			return nil, err
		}
		if empty {
			if err := s.verifyLineage(); err != nil {
				return nil, err
			}
			return &Proof{s: s, rec: s.rec}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

// Check verifies an opaque proof against its still-owned, closed domain and
// fresh kernel emptiness. A detached record or caller string is not a proof.
func (d *Domain) Check(ctx context.Context, p *Proof) error {
	if d == nil || d.s == nil || p == nil || p.s != d.s || ctx == nil {
		return ErrInvalid
	}
	s := d.s
	unlock, err := s.enterLineage(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	if s.disposed || !s.closed.Load() || !s.rec.Closed || p.rec != s.rec {
		return ErrClosed
	}
	if err := s.verifyLineage(); err != nil {
		return err
	}
	durable, err := readRecord(filepath.Join(s.root.Name(), "epoch.json"))
	if err != nil {
		return err
	}
	if durable != s.rec {
		return ErrIntegrity
	}
	empty, err := unpopulated(s.path)
	if err != nil {
		return err
	}
	if !empty {
		return ErrIntegrity
	}
	return nil
}

// Close denies entry and relinquishes descriptors/flock, but does not kill,
// delete, or prove anything about surviving descendants or persisted data.
func (d *Domain) Close() error {
	if d == nil || d.s == nil {
		return ErrInvalid
	}
	s := d.s
	s.closed.Store(true)
	unlock, err := s.enterLineage(context.Background(), false)
	if err != nil {
		return err
	}
	defer unlock()
	if s.disposed {
		return nil
	}
	if len(s.children) != 0 || s.parentState != nil {
		return ErrBusy
	}
	s.dispose()
	return nil
}

func (s *state) enter(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.gate:
		return nil
	}
}

func (s *state) leave() { s.gate <- struct{}{} }

func (s *state) dispose() {
	if s.disposed {
		return
	}
	s.disposed = true
	for _, f := range []*os.File{s.jails, s.supervisors, s.group, s.root} {
		if f != nil {
			f.Close()
		}
	}
	if s.budget != nil {
		s.budget.release()
	}
	if s.parentState != nil {
		delete(s.parentState.children, s)
		s.instance().descendants--
	}
}

func openOwner(root, parent string) (*state, error) {
	return openOwnerWithBudget(root, parent, nil)
}

func openOwnerWithBudget(root, parent string, budget *budgetState) (*state, error) {
	if err := noLinks(root); err != nil {
		return nil, err
	}
	if err := noLinks(parent); err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if owned {
			budget.release()
		}
	}()
	if budget != nil {
		if err := budget.retain(parent); err != nil {
			return nil, err
		}
		owned = true
	} else {
		membership, err := os.ReadFile("/proc/self/cgroup")
		if err != nil {
			return nil, err
		}
		if err := validateParent(parent, string(membership)); err != nil {
			return nil, err
		}
	}
	s, err := openMetadata(root, parent)
	if err != nil {
		return nil, err
	}
	s.budget = budget
	owned = false
	return s, nil
}

func openMetadata(root, parent string) (*state, error) {
	if err := noLinks(root); err != nil {
		return nil, err
	}
	f, err := openDir(root)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	stat := st.Sys().(*syscall.Stat_t)
	if stat.Uid != uint32(os.Geteuid()) || st.Mode() != os.ModeDir|0o700 {
		f.Close()
		return nil, ErrInvalid
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	s := &state{root: f, parent: parent, gate: make(chan struct{}, 1)}
	s.leave()
	return s, nil
}

func validateParent(parent, membership string) error {
	var base string
	for _, line := range strings.Split(strings.TrimSpace(membership), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || parts[0] != "0" || parts[1] != "" {
			continue
		}
		rel := parts[2]
		if base != "" || !filepath.IsAbs(rel) || filepath.Clean(rel) != rel || rel == "/" {
			return ErrInvalid
		}
		base = "/sys/fs/cgroup" + rel
	}
	if base == "" || !filepath.IsAbs(parent) || filepath.Clean(parent) != parent {
		return ErrInvalid
	}
	rel, err := filepath.Rel(base, parent)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ErrInvalid
	}
	return nil
}

func openDir(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openCgroup(path string) (*os.File, error) {
	if err := noLinks(path); err != nil {
		return nil, err
	}
	f, err := openDir(path)
	if err != nil {
		return nil, err
	}
	var fs syscall.Statfs_t
	st, statErr := f.Stat()
	if err := syscall.Fstatfs(int(f.Fd()), &fs); err != nil || fs.Type != cgroup2Magic || statErr != nil {
		f.Close()
		return nil, ErrInvalid
	}
	sys := st.Sys().(*syscall.Stat_t)
	if sys.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0o022 != 0 {
		f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}

func noLinks(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInvalid
	}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if p == "/" {
			return nil
		}
	}
}

func identity(f *os.File) (objectID, error) {
	st, err := f.Stat()
	if err != nil {
		return objectID{}, err
	}
	s := st.Sys().(*syscall.Stat_t)
	return objectID{Device: uint64(s.Dev), Inode: s.Ino}, nil
}

func bootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !validBootID(id) {
		return "", ErrInvalid
	}
	return id, nil
}

func validBootID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	raw := strings.ReplaceAll(id, "-", "")
	if _, err := hex.DecodeString(raw); err != nil || len(raw) != 32 || strings.ToLower(id) != id {
		return false
	}
	return true
}

func prepareTree(path string) error {
	data, err := os.ReadFile(filepath.Join(path, "cgroup.type"))
	if err != nil || strings.TrimSpace(string(data)) != "domain" {
		return ErrInvalid
	}
	for _, child := range []string{"supervisors", "jails"} {
		if err := os.Mkdir(filepath.Join(path, child), 0o700); err != nil {
			return err
		}
	}
	empty, err := unpopulated(path)
	if err != nil || !empty {
		return ErrInvalid
	}
	if data, err := os.ReadFile(filepath.Join(path, "cgroup.subtree_control")); err != nil || len(strings.TrimSpace(string(data))) != 0 {
		return ErrInvalid
	}
	if data, err := os.ReadFile(filepath.Join(path, "jails", "cgroup.subtree_control")); err != nil || len(strings.TrimSpace(string(data))) != 0 {
		return ErrInvalid
	}
	if data, err := os.ReadFile(filepath.Join(path, "supervisors", "cgroup.subtree_control")); err != nil || len(strings.TrimSpace(string(data))) != 0 {
		return ErrInvalid
	}
	if err := noLinks(path); err != nil {
		return err
	}
	for _, name := range []string{"cgroup.kill", "cgroup.events"} {
		flags := os.O_RDONLY
		if name == "cgroup.kill" {
			flags = os.O_WRONLY
		}
		f, err := os.OpenFile(filepath.Join(path, name), flags, 0)
		if err != nil {
			return err
		}
		f.Close()
	}
	return nil
}

func writeControl(path, name, value string) error {
	f, err := os.OpenFile(filepath.Join(path, name), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, value)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func unpopulated(path string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
	if err != nil {
		return false, err
	}
	seen := false
	empty := false
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return false, ErrInvalid
		}
		if fields[0] == "populated" {
			if seen || (fields[1] != "0" && fields[1] != "1") {
				return false, ErrInvalid
			}
			seen, empty = true, fields[1] == "0"
		}
	}
	if !seen {
		return false, ErrInvalid
	}
	return empty, nil
}

func (s *state) verifyIdentity() error {
	if s.parentState != nil {
		p := s.parentState
		if s.parent != filepath.Join(p.path, "jails") || s.rec.Lineage != p.childLineage() || p.jails == nil {
			return ErrIntegrity
		}
		current, err := openCgroup(s.parent)
		if err != nil {
			return err
		}
		id, err := identity(current)
		current.Close()
		if err != nil {
			return err
		}
		pinned, err := identity(p.jails)
		if err != nil || id != pinned {
			return ErrIntegrity
		}
	} else if s.budget != nil {
		if err := s.budget.verify(s.parent); err != nil {
			return err
		}
	} else {
		membership, err := os.ReadFile("/proc/self/cgroup")
		if err != nil {
			return err
		}
		if err := validateParent(s.parent, string(membership)); err != nil {
			return err
		}
	}
	boot, err := bootID()
	if err != nil {
		return err
	}
	root, err := identity(s.root)
	if err != nil {
		return err
	}
	if err := noLinks(s.root.Name()); err != nil {
		return err
	}
	currentRoot, err := openDir(s.root.Name())
	if err != nil {
		return err
	}
	defer currentRoot.Close()
	st, err := currentRoot.Stat()
	if err != nil {
		return err
	}
	if st.Mode() != os.ModeDir|0o700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ErrInvalid
	}
	rid, err := identity(currentRoot)
	if err != nil {
		return err
	}
	parent, err := openCgroup(s.parent)
	if err != nil {
		return err
	}
	defer parent.Close()
	pid, err := identity(parent)
	if err != nil {
		return err
	}
	current, err := openCgroup(s.path)
	if err != nil {
		return err
	}
	defer current.Close()
	gid, err := identity(current)
	if err != nil {
		return err
	}
	pinned, err := identity(s.group)
	if err != nil {
		return err
	}
	if s.parentState != nil {
		for _, f := range []*os.File{parent, current} {
			id, err := descriptorMountID(f)
			if err != nil {
				return err
			}
			expected, err := descriptorMountID(s.parentState.jails)
			if err != nil {
				return err
			}
			if id != expected {
				return ErrIntegrity
			}
		}
	}
	if root != s.rec.Root || rid != root || pid != s.rec.Parent || gid != s.rec.Group || pinned != gid || boot != s.rec.Boot {
		return ErrIntegrity
	}
	return nil
}

func (s *state) verifyRecord() error {
	// Reopen rather than reuse the directory's enumeration offset.
	dir, err := openDir(s.root.Name())
	if err != nil {
		return err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(2)
	if err != nil && err != io.EOF {
		return err
	}
	if len(names) != 1 || names[0] != "epoch.json" {
		return ErrInvalid
	}
	durable, err := readRecord(filepath.Join(s.root.Name(), "epoch.json"))
	if err != nil {
		return err
	}
	expected := s.rec
	// A failed directory fsync may leave the closed rename visible. Only this
	// monotonic transition is retryable; persist still fsyncs it again.
	if durable.Closed && s.closed.Load() {
		expected.Closed = true
	}
	if durable != expected {
		return ErrIntegrity
	}
	return nil
}

func (s *state) persist(r record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(s.root.Name(), "epoch.json.tmp")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = s.persistenceStep("file-sync", f.Sync)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := s.persistenceStep("rename", func() error {
		return os.Rename(path, filepath.Join(s.root.Name(), "epoch.json"))
	}); err != nil {
		return err
	}
	return s.persistenceStep("directory-sync", s.root.Sync)
}

func (s *state) persistenceStep(name string, op func() error) error {
	if s.persistFault != nil {
		if err := s.persistFault(name); err != nil {
			return err
		}
	}
	return op()
}

func readRecord(path string) (record, error) {
	var r record
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return r, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return r, err
	}
	sys := st.Sys().(*syscall.Stat_t)
	if st.Mode() != 0o600 || sys.Uid != uint32(os.Geteuid()) || sys.Nlink != 1 || st.Size() > 4096 {
		return r, ErrInvalid
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return r, ErrInvalid
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, ErrInvalid
	}
	canonical, err := json.Marshal(r)
	if err != nil || !bytes.Equal(b, canonical) || (r.Version != 1 && r.Version != 2) || len(r.Epoch) != 32 || r.Epoch != strings.ToLower(r.Epoch) {
		return r, ErrInvalid
	}
	if _, err := hex.DecodeString(r.Epoch); err != nil {
		return r, ErrInvalid
	}
	if !validBootID(r.Boot) || r.Root.Inode == 0 || r.Parent.Inode == 0 || r.Group.Inode == 0 {
		return r, ErrInvalid
	}
	if (r.Version == 1 && r.Lineage != (lineage{})) || (r.Version == 2 && !r.Lineage.valid(r.Epoch)) {
		return r, ErrInvalid
	}
	return r, nil
}
