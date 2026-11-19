//go:build linux

// Package workloadrecovery is an explicit admission-closed recovery fixture.
// Normal serving, boot migration and the public restore handler do not use it.
package workloadrecovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

var ErrClosed = errors.New("recovery requires closed admission and trusted quiescence")
var ErrIncomplete = errors.New("restore installation incomplete; admission remains closed")

type marker struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	State     string `json:"state"`
}

// Host callbacks must stop/drain all admissions, workers, schedulers and builds,
// close live DB handles, and establish any required external process fence.
// They run outside all IAM/DB locks; a marker is not evidence of kernel fencing.
type Host struct {
	Quiesce         func(context.Context) error
	AdmissionClosed func() bool
}

type Coordinator struct {
	dir   string
	root  *os.File
	lock  *os.File
	host  Host
	mu    sync.Mutex
	fault func(string) error
	sync  func(string, func() error) error
}

// Open requires an existing host-owned data directory with no linked ancestors.
// The exclusive file lock serializes only cooperating recovery coordinators.
func Open(dataDir string, host Host) (*Coordinator, error) {
	if host.Quiesce == nil || host.AdmissionClosed == nil {
		return nil, ErrClosed
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(abs, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	root := os.NewFile(uintptr(fd), abs)
	fail := func(e error) (*Coordinator, error) { root.Close(); return nil, e }
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fail(err)
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return fail(errors.New("recovery directory must be owned and not group/world writable"))
	}
	lfd, err := unix.Openat(fd, backup.RecoveryMarkerName+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fail(err)
	}
	lock := os.NewFile(uintptr(lfd), "recovery lock")
	if err := regularOwned(lfd, 0); err != nil {
		lock.Close()
		return fail(err)
	}
	if err := unix.Flock(lfd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return fail(err)
	}
	return &Coordinator{dir: abs, root: root, lock: lock, host: host}, nil
}

func regularOwned(fd int, max int64) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) ||
		st.Mode&0077 != 0 || (max > 0 && st.Size > max) {
		return errors.New("invalid recovery control file")
	}
	return nil
}

func (c *Coordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.root == nil {
		return nil
	}
	e := c.lock.Close()
	if err := c.root.Close(); e == nil {
		e = err
	}
	c.root = nil
	return e
}

func (c *Coordinator) step(s string) error {
	if c.fault != nil {
		return c.fault(s)
	}
	return nil
}

func (c *Coordinator) durableSync(point string, action func() error) error {
	if c.sync != nil {
		return c.sync(point, action)
	}
	return action()
}

func (c *Coordinator) checkDBPaths() error {
	for _, name := range []string{"orva.db", "orva.db-wal", "orva.db-shm"} {
		var st unix.Stat_t
		err := unix.Fstatat(int(c.root.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) {
			return errors.New("unsafe recovery database path")
		}
	}
	return nil
}

func checkManagedSourceDirectory(root *os.File) error {
	fd, err := unix.Openat(int(root.Fd()), "functions", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return database.ErrManagedOwnerInvalid
	}
	dir := os.NewFile(uintptr(fd), "functions")
	defer dir.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return database.ErrManagedOwnerInvalid
	}
	names, err := dir.Readdirnames(1)
	if len(names) != 0 {
		return database.ErrManagedOwnerLegacy
	}
	if !errors.Is(err, io.EOF) {
		return database.ErrManagedOwnerInvalid
	}
	return nil
}

func (c *Coordinator) quiesce(ctx context.Context) error {
	if c.root == nil {
		return ErrClosed
	}
	if err := c.host.Quiesce(ctx); err != nil {
		return fmt.Errorf("quiesce: %w", err)
	}
	if !c.host.AdmissionClosed() {
		return ErrClosed
	}
	return c.step("quiesced")
}

func (c *Coordinator) readMarker() (*marker, error) {
	fd, err := unix.Openat(int(c.root.Fd()), backup.RecoveryMarkerName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "recovery marker")
	defer f.Close()
	if err := regularOwned(fd, 256); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil {
		return nil, err
	}
	var m marker
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errors.New("malformed recovery marker")
	}
	u, err := uuid.Parse(m.Operation)
	if err != nil || u.String() != m.Operation || u.Version() != 7 || m.Version != 1 ||
		(m.State != "staging" && m.State != "installing" && m.State != "ready") {
		return nil, errors.New("invalid recovery marker")
	}
	canonical, _ := json.Marshal(m)
	if !bytes.Equal(raw, canonical) {
		return nil, errors.New("noncanonical recovery marker")
	}
	return &m, nil
}

func (c *Coordinator) writeMarker(m *marker) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	name := backup.RecoveryMarkerName + ".tmp-" + ids.New()
	fd, err := unix.Openat(int(c.root.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(c.root.Fd()), name, 0)
	f := os.NewFile(uintptr(fd), "new recovery marker")
	_, err = f.Write(raw)
	if err == nil {
		err = c.durableSync("marker_file_"+m.State, f.Sync)
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err := unix.Renameat(int(c.root.Fd()), name, int(c.root.Fd()), backup.RecoveryMarkerName); err != nil {
		return err
	}
	if err := c.durableSync("marker_dir_"+m.State, c.root.Sync); err != nil {
		return err
	}
	return c.step("marker_" + m.State)
}

// PrepareManual must run while the instance is stopped, before manual copying.
// CompleteManual is a separate explicit acknowledgement of a complete install.
func (c *Coordinator) PrepareManual(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.quiesce(ctx); err != nil {
		return "", err
	}
	m, err := c.readMarker()
	if err != nil {
		return "", err
	}
	if m != nil {
		return m.Operation, ErrIncomplete
	}
	m = &marker{Version: 1, Operation: ids.New(), State: "installing"}
	return m.Operation, c.writeMarker(m)
}

func (c *Coordinator) CompleteManual(ctx context.Context, operation string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.quiesce(ctx); err != nil {
		return err
	}
	m, err := c.readMarker()
	if err != nil {
		return err
	}
	if m == nil || m.Operation != operation || m.State != "installing" {
		return ErrIncomplete
	}
	if err := c.syncInstalled(); err != nil {
		return err
	}
	m.State = "ready"
	return c.writeMarker(m)
}

// Restore drives real backup staging/promotion. Failed or interrupted installs
// retain a non-ready marker; only an explicit retry can replace that state.
func (c *Coordinator) Restore(ctx context.Context, r io.Reader) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.quiesce(ctx); err != nil {
		return "", err
	}
	m, err := c.readMarker()
	if err != nil {
		return "", err
	}
	if m != nil && m.State == "ready" {
		return m.Operation, ErrIncomplete
	}
	if m == nil {
		m = &marker{Version: 1, Operation: ids.New(), State: "staging"}
		if err := c.writeMarker(m); err != nil {
			return m.Operation, err
		}
	}
	err = backup.RestoreFromGuarded(r, c.dir, func() error {
		if !c.host.AdmissionClosed() {
			return ErrClosed
		}
		if err := c.step("staged"); err != nil {
			return err
		}
		m.State = "installing"
		return c.writeMarker(m)
	})
	if err != nil {
		return m.Operation, err
	}
	if err := c.step("installed"); err != nil {
		return m.Operation, err
	}
	if err := c.syncInstalled(); err != nil {
		return m.Operation, err
	}
	m.State = "ready"
	return m.Operation, c.writeMarker(m)
}

// RestoreBound rejects foreign or unbound archives before promotion. It can
// replace an installed ready restore only when that restore cannot start with
// this verified Owner. It never clears the marker without a replacement.
func (c *Coordinator) RestoreBound(ctx context.Context, owner string, r io.Reader) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.quiesce(ctx); err != nil {
		return "", err
	}
	m, err := c.readMarker()
	if err != nil {
		return "", err
	}
	if err := checkManagedSourceDirectory(c.root); err != nil {
		return "", err
	}
	if err := c.checkDBPaths(); err != nil {
		return "", err
	}
	live := filepath.Join(c.dir, "orva.db")
	if m != nil && m.State == "ready" {
		err := database.RequireManagedOwnerBinding(ctx, live, owner)
		if !errors.Is(err, database.ErrManagedOwnerMismatch) && !errors.Is(err, database.ErrManagedOwnerLegacy) {
			return m.Operation, ErrIncomplete
		}
	} else if err := database.InspectManagedOwnerBinding(ctx, live, owner); err != nil {
		return "", err
	}
	next := &marker{Version: 1, Operation: ids.New(), State: "installing"}
	if err := backup.RestoreSealedFromGuardedDB(r, c.dir, func(path string) error {
		if err := database.RequireManagedOwnerBinding(ctx, path, owner); err != nil {
			return err
		}
		if !c.host.AdmissionClosed() {
			return ErrClosed
		}
		if err := c.step("staged"); err != nil {
			return err
		}
		return c.writeMarker(next)
	}); err != nil {
		return next.Operation, err
	}
	if err := c.step("installed"); err != nil {
		return next.Operation, err
	}
	if err := c.syncInstalled(); err != nil {
		return next.Operation, err
	}
	next.State = "ready"
	return next.Operation, c.writeMarker(next)
}

// Start supplies the opened DB only after explicit migration/quarantine and
// independent current-artifact reconstruction. It never restores approvals or
// rebinds an old boot's Factory. Callbacks must fail with admission still closed.
func (c *Coordinator) Start(ctx context.Context, reconstruct func(context.Context, *database.Database) error,
	start func(context.Context, *database.Database) error) (*database.Database, database.RecoveryResult, error) {
	return c.start(ctx, "", reconstruct, start)
}

// StartBound refuses legacy or foreign instance state before opening a writer.
// The owner ID must come from the independently verified sealed-store Owner.
func (c *Coordinator) StartBound(ctx context.Context, owner string, reconstruct, start func(context.Context, *database.Database) error) (*database.Database, database.RecoveryResult, error) {
	if owner == "" {
		return nil, database.RecoveryResult{}, database.ErrManagedOwnerInvalid
	}
	return c.start(ctx, owner, reconstruct, start)
}

func (c *Coordinator) start(ctx context.Context, owner string, reconstruct, start func(context.Context, *database.Database) error) (*database.Database, database.RecoveryResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result database.RecoveryResult
	if reconstruct == nil || start == nil {
		return nil, result, ErrClosed
	}
	if err := c.quiesce(ctx); err != nil {
		return nil, result, err
	}
	// Read before opening SQLite, migration, or any possible dispatch callback.
	m, err := c.readMarker()
	if err != nil {
		return nil, result, err
	}
	if m != nil && m.State != "ready" {
		return nil, result, ErrIncomplete
	}
	if err := c.checkDBPaths(); err != nil {
		return nil, result, err
	}
	operation := ""
	if m != nil {
		operation = m.Operation
	}
	path := filepath.Join(c.dir, "orva.db")
	if owner != "" {
		if err := checkManagedSourceDirectory(c.root); err != nil {
			return nil, result, err
		}
		inspect := database.InspectManagedOwnerBinding
		if m != nil {
			inspect = database.RequireManagedOwnerBinding
		}
		if err := inspect(ctx, path, owner); err != nil {
			return nil, result, err
		}
	}
	db, err := database.New(path)
	if err != nil {
		return nil, result, err
	}
	fail := func(e error) (*database.Database, database.RecoveryResult, error) {
		db.Close()
		return nil, result, e
	}
	if err := db.Migrate(); err != nil {
		return fail(err)
	}
	if owner != "" {
		if err := db.BindManagedOwner(ctx, owner); err != nil {
			return fail(err)
		}
	}
	if err := c.step("before_quarantine"); err != nil {
		return fail(err)
	}
	result, err = db.ApplyRecoveryForFixture(ctx, operation)
	if err != nil {
		return fail(err)
	}
	if err := c.step("committed"); err != nil {
		return fail(err)
	}
	if err := c.durableSync("quarantine_dir", c.root.Sync); err != nil {
		return fail(err)
	}
	if m != nil {
		if err := c.step("before_remove"); err != nil {
			return fail(err)
		}
		if err := unix.Unlinkat(int(c.root.Fd()), backup.RecoveryMarkerName, 0); err != nil {
			return fail(err)
		}
		if err := c.step("removed"); err != nil {
			return fail(err)
		}
		if err := c.durableSync("removal_dir", c.root.Sync); err != nil {
			return fail(err)
		}
		if err := c.step("removal_synced"); err != nil {
			return fail(err)
		}
	}
	if err := reconstruct(ctx, db); err != nil {
		return fail(err)
	}
	if !c.host.AdmissionClosed() {
		return fail(ErrClosed)
	}
	if err := start(ctx, db); err != nil {
		_ = c.host.Quiesce(ctx)
		return fail(err)
	}
	return db, result, nil
}

func (c *Coordinator) syncInstalled() error {
	fd := int(c.root.Fd())
	for _, name := range []string{"orva.db", ".master.key", ".admin-key"} {
		f, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && name != "orva.db" {
			continue
		}
		if err != nil {
			return err
		}
		var st unix.Stat_t
		err = unix.Fstat(f, &st)
		if err == nil && st.Mode&unix.S_IFMT != unix.S_IFREG {
			err = ErrIncomplete
		}
		if err == nil {
			err = c.durableSync("installed_file_"+name, func() error { return unix.Fsync(f) })
		}
		unix.Close(f)
		if err != nil {
			return err
		}
	}
	budget := 200000
	if err := syncTree(fd, "functions", &budget); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return c.durableSync("installed_dir", c.root.Sync)
}

func syncTree(parent int, name string, budget *int) error {
	*budget--
	if *budget < 0 {
		return errors.New("recovery filesystem capacity exceeded")
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if name == "current" && errors.Is(err, unix.ELOOP) {
			return nil
		}
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		for {
			entries, err := f.ReadDir(128)
			for _, entry := range entries {
				if e := syncTree(fd, entry.Name(), budget); e != nil {
					return e
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
	} else if !st.Mode().IsRegular() {
		return ErrIncomplete
	}
	return f.Sync()
}
