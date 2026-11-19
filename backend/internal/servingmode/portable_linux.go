package servingmode

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Harsh-2002/Orva/internal/ids"
	"golang.org/x/sys/unix"
)

var errPortableIncomplete = errors.New("offline portable restore incomplete; serving refused")

type PortableRestore struct {
	lock   *Lock
	record Record
}

func (l *Lock) CheckPortableRestore() (Record, error) {
	current, err := l.Read()
	if err != nil {
		return Record{}, err
	}
	if current.Mode != Legacy && current.Mode != Provisioning {
		return Record{}, errors.New("portable restore requires a fresh or provisioning directory")
	}
	if err := l.checkFreshLayout(); err != nil {
		return Record{}, err
	}
	// A provisioning marker reserves an identity, but an already opened
	// Owner may have stores from a prior operation. Never adopt that state.
	for _, name := range []string{"sealed-owner", "sealed-code", "sealed-runtime", "managed-build"} {
		if st, err := statAt(int(l.root.Fd()), name); err != nil {
			return Record{}, err
		} else if st != nil {
			return Record{}, fmt.Errorf("portable restore requires absent %s (Owner not yet initialized)", name)
		}
	}
	return current, nil
}

func (l *Lock) BeginPortableRestore(prior string) (*PortableRestore, error) {
	if l.file == nil {
		return nil, errors.New("portable restore requires an exclusive layout lock")
	}
	r, err := l.CheckPortableRestore()
	if err != nil {
		return nil, err
	}
	if r.Mode == Legacy {
		r = Record{Version: 1, Owner: ids.New(), Operation: ids.New()}
	}
	r.Mode = Managed
	if r.Owner == prior {
		return nil, errors.New("portable restore requires a different Owner; use same-host restore")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if err := createSyncedFile(int(l.root.Fd()), portableIntent, raw); err != nil {
		return nil, err
	}
	if err := l.root.Sync(); err != nil {
		return nil, err
	}
	return &PortableRestore{l, r}, nil
}

func (r *PortableRestore) Record() Record { return r.record }

// Install moves only new files into the guarded layout, never replacing an
// existing file. The intent is removed only after the legacy tombstone exists.
func (r *PortableRestore) Install(stageFD int, step func(string) error) error {
	parent := int(r.lock.root.Fd())
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if st, err := statAt(stageFD, dbName+suffix); err != nil || st != nil {
			return errors.Join(errors.New("staged SQLite sidecars are not checkpointed"), err)
		}
	}
	for _, file := range []struct{ from, to string }{
		{"orva.db", "orva.db"}, {".master.key", ".master.key"}, {".admin-key", ".admin-key"},
	} {
		st, err := statAt(stageFD, file.from)
		if err != nil {
			return err
		}
		if st == nil && file.from != "orva.db" {
			continue
		}
		if st == nil || !ownedRegular(st) || st.Mode&0777 != 0600 {
			return fmt.Errorf("unsafe staged %s", file.from)
		}
		if err := unix.Renameat2(stageFD, file.from, parent, file.to, unix.RENAME_NOREPLACE); err != nil {
			return err
		}
		if err := r.lock.root.Sync(); err != nil {
			return err
		}
		if err := step("installed:" + file.to); err != nil {
			return err
		}
	}
	if err := unix.Fsync(stageFD); err != nil {
		return err
	}
	return r.lock.relocateLockedWithMode(r.record, func(phase string) error {
		if err := step(phase); err != nil || phase != "relocated" {
			return err
		}
		if err := unix.Unlinkat(parent, portableIntent, 0); err != nil {
			return err
		}
		if err := r.lock.root.Sync(); err != nil {
			return err
		}
		return step("intent_removed")
	}, "mode_staged", step)
}
