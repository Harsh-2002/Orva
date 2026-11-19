// Package servingmode serializes access to an instance's durable data layout.
package servingmode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	MarkerName   = ".orva-serving-mode"
	Env          = "ORVA_SERVING_MODE"
	Legacy       = "legacy"
	Provisioning = "provisioning"
	Managed      = "managed"

	lockName     = ".workload-recovery.lock"
	managedGuard = ".orva-managed-data"
	dbName       = "orva.db"
	maxMarker    = 256
)

type Record struct {
	Version   int    `json:"version"`
	Mode      string `json:"mode"`
	Owner     string `json:"owner"`
	Operation string `json:"operation"`
}

func (r Record) valid() bool {
	owner, e1 := uuid.Parse(r.Owner)
	op, e2 := uuid.Parse(r.Operation)
	return r.Version == 1 && (r.Mode == Legacy || r.Mode == Provisioning || r.Mode == Managed) &&
		e1 == nil && owner != uuid.Nil && owner.String() == r.Owner && owner.Variant() == uuid.RFC4122 &&
		e2 == nil && op.String() == r.Operation && op.Variant() == uuid.RFC4122 && op.Version() == 7
}

// Lock is held until all database users have stopped. It is not a fence
// against filesystem administrators or already-running pre-lock binaries.
type Lock struct {
	root *os.File
	file *os.File
}

// Acquire takes the existing recovery lock before resolving any serving mode.
func Acquire(dataDir string) (*Lock, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if strings.ContainsRune(abs, '?') {
		return nil, errors.New("data directory must not contain a SQLite query delimiter")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(abs, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("open data directory: %w", err)
		}
		fd = next
	}
	root := os.NewFile(uintptr(fd), abs)
	fail := func(err error) (*Lock, error) {
		root.Close()
		return nil, err
	}
	if err := checkDir(fd); err != nil {
		return fail(err)
	}
	if st, err := statAt(fd, managedGuard); err != nil {
		return fail(err)
	} else if st != nil {
		return fail(errors.New("use the instance data directory, not its managed subdirectory"))
	}
	file, err := flock(fd, true)
	if err != nil {
		return fail(err)
	}
	return &Lock{root: root, file: file}, nil
}

// AcquireLegacy follows the configured directory path, as legacy SQLite does.
// It returns only the flock handle, not a handle that can publish managed mode.
func AcquireLegacy(dataDir string) (*os.File, error) {
	if strings.ContainsRune(dataDir, '?') {
		return nil, errors.New("data directory must not contain a SQLite query delimiter")
	}
	fd, err := unix.Open(dataDir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open legacy data directory: %w", err)
	}
	defer unix.Close(fd)
	if err := checkLegacyStart(fd); err != nil {
		return nil, err
	}
	return lockLegacy(fd)
}

func lockLegacy(fd int) (*os.File, error) {
	file, err := flock(fd, false)
	if err != nil {
		return nil, err
	}
	// A relocation could have completed after the first check but before flock.
	if err := checkLegacyStart(fd); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func checkLegacyStart(fd int) error {
	for _, name := range []string{managedGuard, MarkerName, dbName} {
		st, err := statAt(fd, name)
		if err != nil {
			return fmt.Errorf("check %s: %w", name, err)
		}
		if st == nil {
			continue
		}
		switch {
		case name == managedGuard:
			return errors.New("use the instance data directory, not its managed subdirectory")
		case name == MarkerName:
			return errors.New("legacy serve requires no .orva-serving-mode file")
		case st.Mode&unix.S_IFMT == unix.S_IFDIR:
			return errors.New("legacy SQLite path orva.db is a directory; leaving the tombstone intact")
		}
	}
	return (Record{Mode: Legacy}).CheckEnvironment()
}

func flock(fd int, strict bool) (*os.File, error) {
	lfd, err := unix.Openat(fd, lockName, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open data-directory lock: %w", err)
	}
	file := os.NewFile(uintptr(lfd), lockName)
	if strict {
		err = controlFile(lfd, 0)
	} else {
		var st unix.Stat_t
		err = unix.Fstat(lfd, &st)
		if err == nil && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1) {
			err = errors.New("invalid data-directory lock file")
		}
	}
	if err == nil {
		err = unix.Flock(lfd, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire exclusive data-directory lock: %w", err)
	}
	return file, nil
}

func (l *Lock) Close() error {
	return errors.Join(l.root.Close(), l.file.Close())
}

func checkDir(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return errors.New("data directory must be owned by Orva and not group/world writable")
	}
	return nil
}

func controlFile(fd int, max int64) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if !ownedRegular(&st) || st.Mode&0077 != 0 || (max > 0 && st.Size > max) {
		return errors.New("invalid serving-mode control file")
	}
	return nil
}

func (l *Lock) Read() (Record, error) {
	fd, err := unix.Openat(int(l.root.Fd()), MarkerName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return Record{Mode: Legacy}, nil
	}
	if err != nil {
		return Record{}, fmt.Errorf("open %s: %w", MarkerName, err)
	}
	f := os.NewFile(uintptr(fd), MarkerName)
	defer f.Close()
	if err := controlFile(fd, maxMarker); err != nil {
		return Record{}, fmt.Errorf("read %s: %w", MarkerName, err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxMarker+1))
	if err != nil {
		return Record{}, fmt.Errorf("read %s: %w", MarkerName, err)
	}
	var r Record
	if len(raw) <= maxMarker && json.Unmarshal(raw, &r) == nil && r.valid() {
		canonical, _ := json.Marshal(r)
		if bytes.Equal(raw, canonical) {
			return r, nil
		}
	}
	return Record{}, errors.New("invalid or noncanonical .orva-serving-mode")
}

func (r Record) CheckEnvironment() error {
	if v, set := os.LookupEnv(Env); set && v != r.Mode {
		return fmt.Errorf("%s must match durable mode %q (it cannot change the mode)", Env, r.Mode)
	}
	return nil
}

func (l *Lock) checkLegacyDB() error {
	st, err := statAt(int(l.root.Fd()), dbName)
	if err != nil {
		return err
	}
	if st != nil && st.Mode&unix.S_IFMT == unix.S_IFDIR {
		return errors.New("legacy SQLite path orva.db is a directory; leaving the tombstone intact")
	}
	if st != nil && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1) {
		return errors.New("unsafe legacy SQLite path orva.db")
	}
	return nil
}

// OpenData returns the selected layout and a separately owned directory handle.
func (l *Lock) OpenData() (Record, *os.File, error) {
	r, err := l.Read()
	if err != nil {
		return Record{}, nil, err
	}
	if err := r.CheckEnvironment(); err != nil {
		return Record{}, nil, err
	}
	fd, name := int(l.root.Fd()), "."
	switch r.Mode {
	case Legacy:
		if err := l.checkLegacyDB(); err != nil {
			return Record{}, nil, err
		}
	case Managed:
		if err := checkTombstone(fd, dbName); err != nil {
			return Record{}, nil, err
		}
		name = Managed
	default:
		return Record{}, nil, errors.New("serving-mode provisioning is incomplete")
	}
	dir, err := openDir(fd, name, filepath.Join(l.root.Name(), name))
	if err != nil {
		return Record{}, nil, err
	}
	if r.Mode == Managed {
		st, err := statAt(int(dir.Fd()), managedGuard)
		if err != nil || st == nil || !ownedRegular(st) || st.Size != 0 || st.Mode&07777 != 0600 {
			dir.Close()
			return Record{}, nil, errors.New("missing or invalid managed data-directory guard")
		}
	}
	return r, dir, nil
}

func openDir(parent int, name, path string) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	if err := checkDir(fd); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func statAt(fd int, name string) (*unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	return &st, err
}

func ownedRegular(st *unix.Stat_t) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Nlink == 1 && st.Uid == uint32(os.Geteuid())
}

func checkTombstone(parent int, name string) error {
	f, err := openDir(parent, name, name)
	if err != nil {
		return fmt.Errorf("open managed tombstone: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Mode() != os.ModeDir|0500 {
		return errors.New("managed orva.db tombstone must have mode 0500")
	}
	if _, err := f.Readdirnames(1); err != io.EOF {
		return errors.New("managed orva.db tombstone must be empty")
	}
	return nil
}

func (l *Lock) write(r Record) error {
	if !r.valid() {
		return errors.New("invalid serving-mode record")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	parent := int(l.root.Fd())
	tmp := MarkerName + ".tmp-" + ids.New()
	defer unix.Unlinkat(parent, tmp, 0)
	if err := createSyncedFile(parent, tmp, raw); err != nil {
		return fmt.Errorf("stage serving-mode record: %w", err)
	}
	if err := unix.Renameat(parent, tmp, parent, MarkerName); err != nil {
		return fmt.Errorf("publish serving-mode record: %w", err)
	}
	return l.root.Sync()
}

func createSyncedFile(parent int, name string, data []byte) error {
	fd, err := unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
