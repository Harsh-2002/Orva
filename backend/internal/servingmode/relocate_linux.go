package servingmode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// RelocateToManaged is an offline layout change, not workload enrollment.
// Stop even pre-lock daemons, close SQLite and checkpoint all sidecars first.
// A failure after staging may require supervised recovery; this never guesses
// whether an existing managed directory belongs to a previous operation.
func (l *Lock) RelocateToManaged(owner, operation string) error {
	return l.relocate(Record{Version: 1, Mode: Managed, Owner: owner, Operation: operation}, func(string) error { return nil })
}

func (l *Lock) relocate(r Record, step func(string) error) error {
	if !r.valid() || r.Mode != Managed {
		return errors.New("invalid managed relocation identity")
	}
	current, err := l.Read()
	if err != nil {
		return err
	}
	if current.Mode != Legacy {
		return errors.New("only a legacy layout can be relocated")
	}
	parent := int(l.root.Fd())
	for _, name := range []string{"orva.db-wal", "orva.db-shm", "orva.db-journal", ".workload-recovery"} {
		st, err := statAt(parent, name)
		if err != nil {
			return err
		}
		if st != nil {
			return fmt.Errorf("%s is present; complete SQLite checkpoint or recovery before relocation", name)
		}
	}
	names := []string{dbName, ".master.key", ".admin-key"}
	present := make(map[string]bool, len(names))
	for _, name := range names {
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return fmt.Errorf("open legacy %s: %w", name, err)
		}
		f := os.NewFile(uintptr(fd), name)
		var st unix.Stat_t
		err = unix.Fstat(fd, &st)
		if err == nil && (!ownedRegular(&st) || st.Mode&0022 != 0) {
			err = errors.New("not a privately writable, owned regular file")
		}
		if err == nil {
			err = f.Sync()
		}
		if err := errors.Join(err, f.Close()); err != nil {
			return fmt.Errorf("validate legacy %s: %w", name, err)
		}
		present[name] = true
	}
	if !present[dbName] && (present[".master.key"] || present[".admin-key"]) {
		return errors.New("legacy keys exist without orva.db")
	}

	if st, err := statAt(parent, Managed); err != nil {
		return err
	} else if st != nil {
		return errors.New("managed directory already exists")
	}
	stage := ".orva-managed-stage-" + r.Operation
	if err := unix.Mkdirat(parent, stage, 0700); err != nil {
		return fmt.Errorf("stage managed directory: %w", err)
	}
	dir, err := openDir(parent, stage, filepath.Join(l.root.Name(), stage))
	if err != nil {
		return err
	}
	defer dir.Close()
	inner := int(dir.Fd())
	if err := createSyncedFile(inner, managedGuard, nil); err != nil {
		return err
	}
	if err := unix.Mkdirat(inner, dbName, 0700); err != nil {
		return err
	}
	tombstone, err := openDir(inner, dbName, dbName)
	if err != nil {
		return err
	}
	defer tombstone.Close()
	syncLayout := func() error {
		if err := dir.Sync(); err != nil {
			return err
		}
		return l.root.Sync()
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if err := unix.Renameat2(parent, stage, parent, Managed, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("install guarded managed directory: %w", err)
	}
	if err := l.root.Sync(); err != nil {
		return err
	}
	if err := step("staged"); err != nil {
		return err
	}
	flags := uint(unix.RENAME_NOREPLACE)
	if present[dbName] {
		// Old binaries ignore flock: there must be no vacant legacy DB path.
		flags = unix.RENAME_EXCHANGE
	}
	if err := unix.Renameat2(inner, dbName, parent, dbName, flags); err != nil {
		return fmt.Errorf("install legacy DB tombstone: %w", err)
	}
	if err := step("exchanged"); err != nil {
		return err
	}
	// Linux needs write access to update ".." during the cross-directory rename.
	if err := tombstone.Chmod(0500); err != nil {
		return fmt.Errorf("set legacy DB tombstone permissions: %w", err)
	}
	if err := tombstone.Sync(); err != nil {
		return fmt.Errorf("sync legacy DB tombstone: %w", err)
	}
	if err := syncLayout(); err != nil {
		return err
	}
	if err := step("tombstone"); err != nil {
		return err
	}
	for _, name := range names[1:] {
		if present[name] {
			if err := unix.Renameat2(parent, name, inner, name, unix.RENAME_NOREPLACE); err != nil {
				return fmt.Errorf("relocate %s: %w", name, err)
			}
		}
	}
	if err := syncLayout(); err != nil {
		return err
	}
	if err := step("relocated"); err != nil {
		return err
	}
	return l.write(r)
}
