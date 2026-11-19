package servingmode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Harsh-2002/Orva/internal/ids"
	"golang.org/x/sys/unix"
)

var errEnrollmentIncomplete = errors.New("offline enrollment incomplete; serving refused")

// Enrollment owns one non-replayable transition. Failure leaves both layouts closed.
type Enrollment struct {
	lock   *Lock
	record Record
}

func (l *Lock) BeginEnrollment(planDigest string) (*Enrollment, error) {
	current, dir, err := l.OpenData()
	if err != nil {
		return nil, err
	}
	if err := dir.Close(); err != nil {
		return nil, err
	}
	if current != (Record{Mode: Legacy}) {
		return nil, errors.New("enrollment requires an unmarked legacy data directory")
	}
	parent := int(l.root.Fd())
	for _, name := range []string{Managed, ".workload-recovery", "sealed-owner", "sealed-code", "sealed-runtime", "managed-build"} {
		if st, err := statAt(parent, name); err != nil || st != nil {
			return nil, errors.Join(fmt.Errorf("enrollment requires absent %s", name), err)
		}
	}
	for _, name := range []string{dbName, "functions"} {
		st, err := statAt(parent, name)
		if err != nil || (name == dbName && (st == nil || !ownedRegular(st))) ||
			(st != nil && name == "functions" && (st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0)) {
			return nil, errors.Join(fmt.Errorf("invalid legacy %s for enrollment", name), err)
		}
	}
	r := Record{Version: 1, Mode: Managed, Owner: ids.New(), Operation: ids.New()}
	raw, err := json.Marshal(struct {
		Record
		Plan string `json:"plan_sha256"`
	}{r, planDigest})
	if err != nil {
		return nil, err
	}
	if err := createSyncedFile(parent, enrollIntent, raw); err != nil {
		return nil, err
	}
	if err := l.root.Sync(); err != nil {
		return nil, err
	}
	return &Enrollment{lock: l, record: r}, nil
}

func (e *Enrollment) Record() Record { return e.record }

// Publish preserves the whole legacy source tree and commits the serving mode
// only after the durable tombstone can guard the interval without intent.
func (e *Enrollment) Publish() error {
	return e.publish(func(string) error { return nil })
}

func (e *Enrollment) publish(step func(string) error) error {
	parent := int(e.lock.root.Fd())
	if st, err := statAt(parent, "functions"); err != nil {
		return err
	} else if st != nil {
		archive := "legacy-functions.enrolled-" + e.record.Operation
		if err := unix.Renameat2(parent, "functions", parent, archive, unix.RENAME_NOREPLACE); err != nil {
			return err
		}
	}
	if err := e.lock.root.Sync(); err != nil {
		return err
	}
	if err := step("source_archived"); err != nil {
		return err
	}
	// The mode-phase step names the only durable boundary between a relocated
	// layout and a published one: the serving-mode record is staged and synced
	// there, and the rename follows.
	return e.lock.relocateLockedWithMode(e.record, func(stage string) error {
		if err := step(stage); err != nil || stage != "relocated" {
			return err
		}
		if err := unix.Unlinkat(parent, enrollIntent, 0); err != nil {
			return err
		}
		if err := e.lock.root.Sync(); err != nil {
			return err
		}
		return step("intent_removed")
	}, "mode_staged", step)
}
