package servingmode

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Harsh-2002/Orva/internal/ids"
	"golang.org/x/sys/unix"
)

// RelocateToManaged is an offline layout change, not workload enrollment.
// Stop even pre-lock daemons, close SQLite and checkpoint all sidecars first.
// A failure after staging may require supervised recovery; this never guesses
// whether an existing managed directory belongs to a previous operation.
func (l *Lock) RelocateToManaged(owner, operation string) error {
	current, err := l.Read()
	if err != nil {
		return err
	}
	if current.Mode != Legacy {
		return errors.New("only a legacy layout can be relocated")
	}
	return l.relocate(Record{Version: 1, Mode: Managed, Owner: owner, Operation: operation}, func(string) error { return nil })
}

// BeginProvisioning records explicit intent for a fresh instance, not a legacy
// conversion. No database or managed routing is enabled.
func (l *Lock) BeginProvisioning() error {
	current, err := l.Read()
	if err != nil {
		return err
	}
	if current != (Record{Mode: Legacy}) {
		return errors.New("fresh managed provisioning requires an unmarked data directory")
	}
	if err := l.checkFreshLayout(); err != nil {
		return err
	}
	return l.write(Record{Version: 1, Mode: Provisioning, Owner: ids.New(), Operation: ids.New()})
}

func (l *Lock) checkFreshLayout() error {
	for _, name := range []string{dbName, "orva.db-wal", "orva.db-shm", "orva.db-journal", Managed, ".master.key", ".admin-key", ".workload-recovery"} {
		st, err := statAt(int(l.root.Fd()), name)
		if err != nil {
			return err
		}
		if st != nil {
			return fmt.Errorf("%s already exists; this operation is fresh managed provisioning only", name)
		}
	}
	dir, err := openDir(int(l.root.Fd()), "functions", "functions")
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	if names, err := dir.Readdirnames(1); len(names) != 0 || !errors.Is(err, io.EOF) {
		return errors.New("fresh managed provisioning requires an empty legacy functions directory")
	}
	return nil
}

// PublishProvisioned requires the independently verified ID of the exact
// provisioned Owner, under the same outer lock.
func (l *Lock) PublishProvisioned(owner string) error {
	current, err := l.Read()
	if err != nil {
		return err
	}
	if current.Mode != Provisioning || current.Owner != owner {
		return errors.New("no matching fresh managed provisioning")
	}
	if err := l.checkFreshLayout(); err != nil {
		return err
	}
	current.Mode = Managed
	return l.relocate(current, func(string) error { return nil })
}

// CheckProvisioning refuses legacy residue before the daemon creates an Owner.
func (l *Lock) CheckProvisioning() error {
	current, err := l.Read()
	if err != nil {
		return err
	}
	if current.Mode != Provisioning {
		return errors.New("fresh managed provisioning is not selected")
	}
	return l.checkFreshLayout()
}

func (l *Lock) relocate(r Record, step func(string) error) error {
	if _, err := l.Read(); err != nil {
		return err
	}
	return l.relocateLocked(r, step)
}

func (l *Lock) relocateLocked(r Record, step func(string) error) error {
	return l.relocateLockedWithMode(r, step, "", nil)
}

func (l *Lock) relocateLockedWithMode(r Record, step func(string) error, modePhase string, modeStep func(string) error) error {
	if !r.valid() || r.Mode != Managed {
		return errors.New("invalid managed relocation identity")
	}
	current, err := l.readMode()
	if err != nil {
		return err
	}
	if current.Mode != Legacy && !(current.Mode == Provisioning && current.Owner == r.Owner && current.Operation == r.Operation) {
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
	return l.writeWithStep(r, modePhase, modeStep)
}
