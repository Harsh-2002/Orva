//go:build linux

package artifactfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func persistentStore(t *testing.T, limits Limits) *Store {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := NewPersistent(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s.lock != nil {
			_ = s.lock.Close()
		}
	})
	return s
}

// Simulate loss of the owning process, not a supported lifecycle fence.
func crashStore(t *testing.T, s *Store) {
	t.Helper()
	if err := s.lock.Close(); err != nil {
		t.Fatal(err)
	}
	s.lock = nil
}

func recoverStore(t *testing.T, s *Store) *Store {
	t.Helper()
	crashStore(t, s)
	r, err := OpenRecovering(context.Background(), s.root, s.limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("recovered close: %v", err)
		}
	})
	return r
}

func persistentStage(t *testing.T, s *Store) (*Staging, Manifest, string) {
	t.Helper()
	st, p := begin(t, s, 100)
	write(t, filepath.Join(p, "handler"), "hello")
	m := fixtureManifest(t, p)
	d, err := Digest(m, s.limits)
	if err != nil {
		t.Fatal(err)
	}
	return st, m, d
}

func diskRecord(t *testing.T, s *Store, id string) persistentRecord {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.persistent.control, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := decodeRecord(raw, id, s.limits)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func assertQuarantined(t *testing.T, s *Store, id string) {
	t.Helper()
	if l, err := s.Acquire(id); err == nil || l != nil {
		t.Fatal("recovery granted a lease")
	}
	if _, err := s.Begin(context.Background(), 1); err == nil {
		t.Fatal("recovery allowed new staging")
	}
	if err := s.Retire(id); err == nil {
		t.Fatal("recovery allowed retirement mutation")
	}
	if err := s.Reclaim(id); err == nil {
		t.Fatal("recovery allowed reclamation")
	}
	if err := (&Staging{s, s.records[id]}).Abort(); err == nil {
		t.Fatal("recovery allowed staging cleanup")
	}
	if pair, err := PreparePaths(s, id, s, id); err == nil || pair != nil || s.leases != 0 {
		t.Fatal("recovery allowed path preparation or leaked refs")
	}
}

func TestPersistentFailedSealInterruptedAbortRecovers(t *testing.T) {
	s := persistentStore(t, testLimits())
	st, m, _ := persistentStage(t, s)
	for i := range m.Entries {
		if m.Entries[i].Type == TypeFile {
			m.Entries[i].Size = 101 // Reservation is 100; actual handler is five bytes.
		}
	}
	digest, err := Digest(m, s.limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Seal(context.Background(), KindCode, ids.New(), m, digest); err != ErrIntegrity {
		t.Fatalf("invalid manifest was not rejected: %v", err)
	}
	s.removeAll = func(string) error { return syscall.EACCES }
	if err := st.Abort(); err != ErrIO {
		t.Fatalf("interrupted cleanup: %v", err)
	}
	if s.bytes != 100 || s.stages != 1 {
		t.Fatal("failed cleanup lost reservations")
	}
	d := diskRecord(t, s, st.rec.identity.Generation)
	if d.Phase != "delete_intent" || d.Kind != "" || len(d.Manifest.Entries) != 0 {
		t.Fatal("cleanup persisted an unverified manifest")
	}
	recovered := recoverStore(t, s)
	entries := recovered.SnapshotRecovery()
	if len(entries) != 1 || entries[0].Phase != "delete_intent" || entries[0].ReservedBytes != 100 || !entries[0].StagingPresent {
		t.Fatal("interrupted cleanup was not retained for inspection")
	}
	assertQuarantined(t, recovered, st.rec.identity.Generation)
}

func TestPersistentLifetimeAndMetadataSeparation(t *testing.T) {
	s := persistentStore(t, testLimits())
	if _, err := New(s.root, s.limits); err != ErrBusy {
		t.Fatalf("second constructor did not lock: %v", err)
	}
	st, p := begin(t, s, 100)
	if d := diskRecord(t, s, st.rec.identity.Generation); d.Phase != "staging" || d.Reserved != 100 {
		t.Fatal(d)
	}
	write(t, filepath.Join(p, "handler"), "hello")
	if err := os.Link(filepath.Join(p, "handler"), filepath.Join(p, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(p, "handler"), 0751|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	m := fixtureManifest(t, p)
	before, _ := os.Stat(filepath.Join(p, "handler"))
	realSync := s.persistent.ops.sync
	var events []string
	s.persistent.ops.sync = func(f *os.File) error {
		if !s.mu.TryLock() {
			t.Fatal("fsync under store mutex")
		}
		s.mu.Unlock()
		events = append(events, f.Name())
		return realSync(f)
	}
	id, err := sealManifest(t, st, KindCode, ids.New(), m)
	if err != nil {
		t.Fatal(err)
	}
	d := diskRecord(t, s, id)
	if d.Phase != "committed" || len(d.Manifest.Entries) != 3 {
		t.Fatal("control metadata entered manifest or commit missing")
	}
	if s.bytes != 100 || s.stages != 0 || s.persistent.manifestBytes == 0 {
		t.Fatal("incorrect owned accounting")
	}
	l, err := s.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(l.Path(), "handler"))
	if !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Fatal("sealing rewrote actual metadata")
	}
	if err := s.Retire(id); err != nil || diskRecord(t, s, id).Phase != "retired" {
		t.Fatal(err)
	}
	if _, err := s.Acquire(id); err != ErrRetired {
		t.Fatal(err)
	}
	if err := s.Reclaim(id); err != ErrBusy {
		t.Fatal("live old worker lease was not preserved")
	}
	if err := s.Close(); err != ErrBusy {
		t.Fatal(err)
	}
	if _, err := OpenRecovering(context.Background(), s.root, s.limits); err != ErrBusy {
		t.Fatal("live busy Close released process lock")
	}
	l.Release()
	if err := s.Reclaim(id); err != nil {
		t.Fatal(err)
	}
	if len(s.records) != 0 || s.bytes != 0 || s.stages != 0 || s.persistent.manifestBytes != 0 {
		t.Fatal("cleanup did not return capacity")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := New(s.root, s.limits); err != ErrInvalid {
		t.Fatal("New adopted persistent layout")
	}
	if _, err := NewPersistent(s.root, s.limits); err != ErrInvalid {
		t.Fatal("initializer adopted existing state")
	}
	if len(events) < 8 {
		t.Fatal("missing durability operations")
	}
}

func TestPersistentRecoveryCommittedIsNotAuthority(t *testing.T) {
	s := persistentStore(t, testLimits())
	id := sealed(t, s, KindCode, ids.New())
	lease, err := s.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	oldWorker, err := os.Open(filepath.Join(lease.Path(), "handler"))
	if err != nil {
		t.Fatal(err)
	}
	defer oldWorker.Close()
	r := recoverStore(t, s)
	assertQuarantined(t, r, id)
	catalog := r.SnapshotRecovery()
	if len(catalog) != 1 || catalog[0].Phase != "committed" || !catalog[0].GenerationPresent ||
		catalog[0].StagingPresent || r.bytes != 100 {
		t.Fatal(catalog)
	}
	catalog[0].Manifest.Entries[0].Mode = 0
	catalog[0].Identity.Owner = "changed"
	if next := r.SnapshotRecovery(); next[0].Identity.Owner == "changed" || next[0].Manifest.Entries[0].Mode == 0 {
		t.Fatal("inspection catalog aliases internal records")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5)
	if _, err := oldWorker.ReadAt(data, 0); err != nil || string(data) != "hello" {
		t.Fatal("recovery touched a mocked surviving old worker's content")
	}
	if _, err := os.Stat(lease.Path()); err != nil {
		t.Fatal("recovery removed old worker tree")
	}
	lease.Release()
	again, err := OpenRecovering(context.Background(), s.root, s.limits)
	if err != nil {
		t.Fatal("read-only Close did not release flock", err)
	}
	assertQuarantined(t, again, id)
	_ = again.Close()
}

func TestPersistentCrashBoundaries(t *testing.T) {
	for _, boundary := range []string{
		"intent_rename", "generation_rename", "root_sync", "commit_write",
		"commit_file_sync", "commit_rename", "commit_control_sync", "cancel_after_rename", "close_after_rename",
	} {
		t.Run(boundary, func(t *testing.T) {
			s := persistentStore(t, testLimits())
			st, m, digest := persistentStage(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops := s.persistent.ops
			intent, renamed, commit := false, false, false
			s.persistent.ops.write = func(f *os.File, data []byte) (int, error) {
				commit = bytes.Contains(data, []byte(`"phase":"committed"`))
				if commit && boundary == "commit_write" {
					return 0, errors.New("must not leak a secret filesystem diagnostic")
				}
				return ops.write(f, data)
			}
			s.persistent.ops.rename = func(from, to string) error {
				if strings.HasPrefix(filepath.Base(to), "generation-") {
					if boundary == "generation_rename" {
						return errors.New("private rename")
					}
					err := ops.rename(from, to)
					renamed = err == nil
					return err
				}
				if strings.HasSuffix(to, ".json") {
					if !intent && boundary == "intent_rename" {
						return errors.New("private intent")
					}
					if commit && boundary == "commit_rename" {
						return errors.New("private commit")
					}
					intent = true
				}
				return ops.rename(from, to)
			}
			s.persistent.ops.sync = func(f *os.File) error {
				if renamed && f.Name() == s.root {
					switch boundary {
					case "root_sync":
						return errors.New("private root sync")
					case "cancel_after_rename":
						cancel()
					case "close_after_rename":
						if err := s.Close(); err != ErrBusy {
							t.Fatal(err)
						}
					}
				}
				if commit && ((boundary == "commit_file_sync" && strings.HasSuffix(f.Name(), ".tmp")) ||
					(boundary == "commit_control_sync" && f.Name() == s.persistent.control)) {
					return errors.New("private metadata sync")
				}
				return ops.sync(f)
			}
			id, err := st.Seal(ctx, KindCode, ids.New(), m, digest)
			want := ErrIO
			if boundary == "cancel_after_rename" {
				want = context.Canceled
			} else if boundary == "close_after_rename" {
				want = ErrClosed
			}
			if !errors.Is(err, want) {
				t.Fatalf("Seal returned %q, want %q", err, want)
			}
			if renamed {
				if id == "" || st.rec.isStage || s.stages != 0 || st.rec.path != filepath.Join(s.root, "generation-"+id) {
					t.Fatal("lost cleanup identity/path/accounting after rename")
				}
				if _, err := s.Acquire(id); err == nil || s.leases != 0 {
					t.Fatal("partial publication acquired")
				}
			} else if id != "" || !st.rec.isStage {
				t.Fatal("stage lost before rename")
			}
			r := recoverStore(t, s)
			entries := r.SnapshotRecovery()
			if len(entries) != 1 || entries[0].GenerationPresent != renamed || r.bytes != 100 {
				t.Fatal(entries)
			}
			wantPhase := "publish_intent"
			if boundary == "intent_rename" {
				wantPhase = "staging"
			} else if boundary == "commit_control_sync" {
				wantPhase = "committed"
			}
			if entries[0].Phase != wantPhase {
				t.Fatalf("phase %s, want %s", entries[0].Phase, wantPhase)
			}
			assertQuarantined(t, r, entries[0].Identity.Generation)
		})
	}
}

func TestPersistentBeginReservationAndUnknownStageQuarantine(t *testing.T) {
	for _, afterMkdir := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_mkdir", true: "after_mkdir"}[afterMkdir], func(t *testing.T) {
			s := persistentStore(t, testLimits())
			ops := s.persistent.ops
			s.persistent.ops.sync = func(f *os.File) error {
				if (!afterMkdir && f.Name() == s.persistent.control) || (afterMkdir && f.Name() == s.root) {
					return errors.New("sync")
				}
				return ops.sync(f)
			}
			st, err := s.Begin(context.Background(), 123)
			if err != ErrIO || st == nil || s.bytes != 123 || s.stages != 1 {
				t.Fatal("lost durable reservation/cleanup handle", err)
			}
			if afterMkdir {
				// Recovery must not read/traverse incomplete producer contents.
				if err := syscall.Mkfifo(filepath.Join(st.rec.path, "unsupported"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := recoverStore(t, s)
			e := r.SnapshotRecovery()
			if len(e) != 1 || e[0].StagingPresent != afterMkdir || e[0].ReservedBytes != 123 {
				t.Fatal(e)
			}
			assertQuarantined(t, r, e[0].Identity.Generation)
		})
	}
}

func TestPersistentCleanupFailuresRetainReservation(t *testing.T) {
	for _, where := range []string{"delete_intent", "tree", "root_sync", "record_remove", "control_sync"} {
		t.Run(where, func(t *testing.T) {
			s := persistentStore(t, testLimits())
			id := sealed(t, s, KindCode, ids.New())
			if err := s.Retire(id); err != nil {
				t.Fatal(err)
			}
			ops, removeTree := s.persistent.ops, s.removeAll
			s.persistent.ops.write = func(f *os.File, data []byte) (int, error) {
				if where == "delete_intent" {
					return 0, errors.New("private write")
				}
				return ops.write(f, data)
			}
			s.removeAll = func(path string) error {
				if where == "tree" {
					_ = os.Remove(filepath.Join(path, "handler"))
					return errors.New("private partial removal")
				}
				return removeTree(path)
			}
			s.persistent.ops.remove = func(path string) error {
				if where == "record_remove" && strings.HasSuffix(path, ".json") {
					return errors.New("private unlink")
				}
				return ops.remove(path)
			}
			s.persistent.ops.sync = func(f *os.File) error {
				if (where == "root_sync" && f.Name() == s.root) ||
					(where == "control_sync" && f.Name() == s.persistent.control) {
					return errors.New("private sync")
				}
				return ops.sync(f)
			}
			if err := s.Reclaim(id); err != ErrIO {
				t.Fatal(err)
			}
			if s.bytes != 100 || len(s.records) != 1 || s.records[id].phase != retired {
				t.Fatal("cleanup failure returned reserved capacity")
			}
			if _, err := s.Acquire(id); err != ErrRetired {
				t.Fatal("partial deletion became acquirable")
			}
			s.persistent.ops, s.removeAll = ops, removeTree
			if err := s.Reclaim(id); err != nil || s.bytes != 0 || len(s.records) != 0 {
				t.Fatal("explicit cleanup retry failed", err)
			}
		})
	}
}

func TestPersistentDeleteIntentCrashNeverResumesCleanup(t *testing.T) {
	s := persistentStore(t, testLimits())
	id := sealed(t, s, KindCode, ids.New())
	_ = s.Retire(id)
	s.removeAll = func(path string) error {
		_ = os.Remove(filepath.Join(path, "handler"))
		return ErrIO
	}
	if err := s.Reclaim(id); err != ErrIO {
		t.Fatal(err)
	}
	r := recoverStore(t, s)
	if r.SnapshotRecovery()[0].Phase != "delete_intent" {
		t.Fatal("deletion intent not durable")
	}
	assertQuarantined(t, r, id)
	if _, err := os.Stat(filepath.Join(s.root, "generation-"+id)); err != nil {
		t.Fatal("recovery auto-cleaned partial deletion")
	}
}

func TestPersistentAbortAndRetireFailures(t *testing.T) {
	s := persistentStore(t, testLimits())
	st, _, _ := persistentStage(t, s)
	ops := s.persistent.ops
	s.persistent.ops.write = func(*os.File, []byte) (int, error) { return 0, errors.New("sensitive error") }
	if err := st.Abort(); err != ErrIO || s.bytes != 100 || s.stages != 1 {
		t.Fatal(err)
	}
	s.persistent.ops = ops
	if err := st.Abort(); err != nil || s.bytes != 0 || s.stages != 0 {
		t.Fatal(err)
	}
	id := sealed(t, s, KindCode, ids.New())
	s.persistent.ops.write = func(*os.File, []byte) (int, error) { return 0, errors.New("sensitive error") }
	if err := s.Retire(id); err != ErrIO {
		t.Fatal(err)
	}
	if _, err := s.Acquire(id); err != ErrRetired {
		t.Fatal("failed retirement retained acquisition")
	}
	s.persistent.ops = ops
	if err := s.Retire(id); err != nil || diskRecord(t, s, id).Phase != "retired" {
		t.Fatal(err)
	}
}

func TestPersistentRecoveryTamperingDenies(t *testing.T) {
	for _, tamper := range []string{
		"unknown_root", "unknown_control", "missing_tree", "content", "manifest", "record_link",
		"record_hardlink", "record_mode", "control_link", "control_mode", "root_xattr", "record_xattr",
		"tree_xattr", "generation_link", "format", "oversize",
	} {
		t.Run(tamper, func(t *testing.T) {
			s := persistentStore(t, testLimits())
			id := sealed(t, s, KindCode, ids.New())
			recordPath := filepath.Join(s.persistent.control, id+".json")
			tree := filepath.Join(s.root, "generation-"+id)
			switch tamper {
			case "unknown_root":
				write(t, filepath.Join(s.root, "not-owned"), "unknown")
			case "unknown_control":
				write(t, filepath.Join(s.persistent.control, "unknown"), "unknown")
			case "missing_tree":
				if err := os.RemoveAll(tree); err != nil {
					t.Fatal(err)
				}
			case "content":
				write(t, filepath.Join(tree, "handler"), "other")
			case "manifest":
				d := diskRecord(t, s, id)
				d.Manifest.Entries[0].Mode ^= 1
				raw, _ := json.Marshal(d)
				if err := os.WriteFile(recordPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "record_link":
				raw, _ := os.ReadFile(recordPath)
				target := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
				_ = os.Remove(recordPath)
				if err := os.Symlink(target, recordPath); err != nil {
					t.Fatal(err)
				}
			case "record_hardlink":
				if err := os.Link(recordPath, filepath.Join(t.TempDir(), "alias")); err != nil {
					t.Fatal(err)
				}
			case "record_mode":
				_ = os.Chmod(recordPath, 0640)
			case "control_link":
				target := filepath.Join(t.TempDir(), "moved")
				_ = os.Rename(s.persistent.control, target)
				_ = os.Symlink(target, s.persistent.control)
			case "control_mode":
				_ = os.Chmod(s.persistent.control, 0750)
			case "root_xattr", "record_xattr", "tree_xattr":
				target := s.root
				if tamper == "record_xattr" {
					target = recordPath
				} else if tamper == "tree_xattr" {
					target = tree
				}
				if err := syscall.Setxattr(target, "user.artifactfs-test", []byte("x"), 0); err != nil {
					t.Skipf("filesystem cannot create xattr fixture: %v", err)
				}
			case "generation_link":
				target := filepath.Join(t.TempDir(), "moved")
				_ = os.Rename(tree, target)
				_ = os.Symlink(target, tree)
			case "format":
				_ = os.WriteFile(filepath.Join(s.persistent.control, "format.json"), []byte(`{"version":2}`), 0600)
			case "oversize":
				_ = os.Truncate(recordPath, int64(s.limits.ManifestBytes+recordOverhead+1))
			}
			crashStore(t, s)
			r, err := OpenRecovering(context.Background(), s.root, s.limits)
			if err == nil {
				_ = r.Close()
				t.Fatal("recovery accepted tampering")
			}
			if tamper == "unknown_root" {
				if _, err := os.Stat(filepath.Join(s.root, "not-owned")); err != nil {
					t.Fatal("recovery deleted unknown content")
				}
			}
		})
	}
}

func TestPersistentStrictRecordParser(t *testing.T) {
	s := persistentStore(t, testLimits())
	id := sealed(t, s, KindCode, ids.New())
	d := diskRecord(t, s, id)
	raw, _ := json.Marshal(d)
	for _, variant := range []struct {
		name string
		data []byte
	}{
		{"duplicate", bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)},
		{"unknown", append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"secret":"no"}`)...)},
		{"null", bytes.Replace(raw, []byte(`"owner":"`+d.Owner+`"`), []byte(`"owner":null`), 1)},
		{"null_entries", bytes.Replace(raw, []byte(`"entries":[`), []byte(`"entries":[null,`), 1)},
		{"invalid_utf8", bytes.Replace(raw, []byte(d.Owner), []byte{0xff}, 1)},
		{"invalid_escape_surrogate", bytes.Replace(raw, []byte(d.Owner), []byte(`\ud800`), 1)},
		{"whitespace", append([]byte(" "), raw...)},
		{"trailing", append(append([]byte{}, raw...), []byte(`{}`)...)},
		{"wrong_id", bytes.Replace(raw, []byte(id), []byte(ids.New()), 1)},
		{"wrong_phase", bytes.Replace(raw, []byte(`"committed"`), []byte(`"active"`), 1)},
		{"negative", bytes.Replace(raw, []byte(`"reserved":100`), []byte(`"reserved":-1`), 1)},
		{"overflow", bytes.Replace(raw, []byte(`"reserved":100`), []byte(`"reserved":9223372036854775808`), 1)},
		{"unknown_manifest", bytes.Replace(raw, []byte(`"manifest":{`), []byte(`"manifest":{"other":1,`), 1)},
		{"duplicate_entry", bytes.Replace(raw, []byte(`"path":"."`), []byte(`"path":".","path":"."`), 1)},
		{"unbounded_depth", bytes.Replace(raw, []byte(`"entries":[`), []byte(`"entries":[[[[[[[[[[[[`), 1)},
	} {
		t.Run(variant.name, func(t *testing.T) {
			if _, _, err := decodeRecord(variant.data, id, s.limits); err == nil {
				t.Fatal("accepted noncanonical record")
			}
		})
	}
	tiny := s.limits
	tiny.Entries = 1
	if _, _, err := decodeRecord(raw, id, tiny); err == nil {
		t.Fatal("decoded entries beyond allocation bound")
	}
	if _, _, err := decodeRecord(bytes.Repeat([]byte(" "), s.limits.ManifestBytes+recordOverhead+1), id, s.limits); err != ErrCapacity {
		t.Fatal("record byte bound not enforced")
	}
}

func TestPersistentAggregateBoundsAndCancellation(t *testing.T) {
	s := persistentStore(t, testLimits())
	_ = sealed(t, s, KindCode, ids.New())
	_ = sealed(t, s, KindCode, ids.New())
	_ = sealed(t, s, KindCode, ids.New())
	_ = sealed(t, s, KindCode, ids.New())
	crashStore(t, s)
	for _, which := range []string{"bytes", "generations", "manifest"} {
		limits := s.limits
		switch which {
		case "bytes":
			limits.Bytes, limits.FileBytes = 150, 150
		case "generations":
			limits.Generations, limits.Staging = 1, 1
		case "manifest":
			limits.ManifestBytes = 1000
		}
		r, err := OpenRecovering(context.Background(), s.root, limits)
		if err == nil {
			_ = r.Close()
			t.Fatalf("accepted aggregate %s overflow", which)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenRecovering(ctx, s.root, s.limits); err != context.Canceled {
		t.Fatal(err)
	}
	live := persistentStore(t, testLimits())
	if st, err := live.Begin(ctx, 1); st != nil || err != context.Canceled || live.bytes != 0 {
		t.Fatal("cancelled Begin reserved capacity")
	}
	st, m, digest := persistentStage(t, live)
	if _, err := st.Seal(ctx, KindCode, ids.New(), m, digest); err != context.Canceled {
		t.Fatal(err)
	}
	if err := st.Abort(); err != nil || live.bytes != 0 {
		t.Fatal(err)
	}
}

func TestPersistentPendingCanonicalMetadata(t *testing.T) {
	s := persistentStore(t, testLimits())
	st, m, digest := persistentStage(t, s)
	ops := s.persistent.ops
	s.persistent.ops.rename = func(from, to string) error {
		if strings.HasPrefix(filepath.Base(to), "generation-") {
			return ErrIO
		}
		return ops.rename(from, to)
	}
	_, _ = st.Seal(context.Background(), KindCode, ids.New(), m, digest)
	d := diskRecord(t, s, st.rec.identity.Generation)
	d.Phase = "committed"
	raw, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(s.persistent.control, d.Generation+".json.tmp"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	r := recoverStore(t, s)
	if e := r.SnapshotRecovery(); len(e) != 1 || !e[0].PendingMetadata || e[0].Phase != "publish_intent" {
		t.Fatal(e)
	}
	assertQuarantined(t, r, d.Generation)
}

func TestPersistentConcurrentLifecycle(t *testing.T) {
	s := persistentStore(t, testLimits())
	id := sealed(t, s, KindCode, ids.New())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 20; j++ {
				l, err := s.Acquire(id)
				if err == nil {
					l.Release()
				} else if err != ErrRetired {
					t.Errorf("acquire: %v", err)
				}
			}
		})
	}
	if err := s.Retire(id); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := s.Reclaim(id); err != nil || s.leases != 0 || s.bytes != 0 {
		t.Fatal(err)
	}
}

func TestPersistentPublicationProtocolOrdering(t *testing.T) {
	s := persistentStore(t, testLimits())
	st, m, digest := persistentStage(t, s)
	ops := s.persistent.ops
	var events []string
	s.persistent.ops.write = func(f *os.File, data []byte) (int, error) {
		d := persistentRecord{}
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatal(err)
		}
		events = append(events, "write:"+d.Phase)
		return ops.write(f, data)
	}
	s.persistent.ops.sync = func(f *os.File) error {
		event := "sync:tree"
		switch f.Name() {
		case s.root:
			event = "sync:root"
		case s.persistent.control:
			event = "sync:control"
		default:
			if strings.HasSuffix(f.Name(), ".json.tmp") {
				event = "sync:record"
			}
		}
		events = append(events, event)
		return ops.sync(f)
	}
	s.persistent.ops.rename = func(from, to string) error {
		event := "rename:record"
		if strings.HasPrefix(filepath.Base(to), "generation-") {
			event = "rename:tree"
			if l, err := s.Acquire(st.rec.identity.Generation); err == nil || l != nil {
				t.Fatal("acquired before durable commit")
			}
		}
		events = append(events, event)
		return ops.rename(from, to)
	}
	_, err := st.Seal(context.Background(), KindCode, ids.New(), m, digest)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"sync:tree", "sync:tree",
		"write:publish_intent", "sync:record", "rename:record", "sync:control",
		"rename:tree", "sync:root",
		"write:committed", "sync:record", "rename:record", "sync:control",
	}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("protocol events\n%v\nwant\n%v", events, want)
	}
}

func TestPersistentShortWritePendingCleanupRetry(t *testing.T) {
	s := persistentStore(t, testLimits())
	ops := s.persistent.ops
	s.persistent.ops.write = func(f *os.File, data []byte) (int, error) {
		n := len(data) / 2
		return f.Write(data[:n])
	}
	s.persistent.ops.remove = func(string) error { return errors.New("private cleanup failure") }
	st, err := s.Begin(context.Background(), 100)
	if err != ErrIO || st == nil || s.bytes != 100 || st.rec.treeOwned {
		t.Fatal("short write did not retain reservation", err)
	}
	if _, err := os.Stat(st.rec.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging created before durable reservation")
	}
	s.persistent.ops = ops
	if err := st.Abort(); err != nil || s.bytes != 0 || len(s.persistent.pending) != 0 {
		t.Fatal("owned failed temporary record not retryable", err)
	}
}

func TestPersistentCancellationCleanupAndLateCommit(t *testing.T) {
	t.Run("begin_cleanup_failure", func(t *testing.T) {
		s := persistentStore(t, testLimits())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ops := s.persistent.ops
		s.persistent.ops.sync = func(f *os.File) error {
			if f.Name() == s.persistent.control {
				cancel()
			}
			return ops.sync(f)
		}
		remove := s.removeAll
		s.removeAll = func(string) error { return ErrIO }
		st, err := s.Begin(ctx, 100)
		if err != context.Canceled || st == nil || s.bytes != 100 || s.stages != 1 {
			t.Fatal("cancelled Begin lost cleanup handle", err)
		}
		s.removeAll, s.persistent.ops = remove, ops
		if err := st.Abort(); err != nil || s.bytes != 0 {
			t.Fatal(err)
		}
	})
	t.Run("during_commit_sync", func(t *testing.T) {
		s := persistentStore(t, testLimits())
		st, m, digest := persistentStage(t, s)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ops := s.persistent.ops
		committing := false
		s.persistent.ops.write = func(f *os.File, raw []byte) (int, error) {
			committing = bytes.Contains(raw, []byte(`"phase":"committed"`))
			return ops.write(f, raw)
		}
		s.persistent.ops.sync = func(f *os.File) error {
			if committing && f.Name() == s.persistent.control {
				cancel()
			}
			return ops.sync(f)
		}
		id, err := st.Seal(ctx, KindCode, ids.New(), m, digest)
		if err != context.Canceled || id == "" || st.rec.phase != retired {
			t.Fatal("late cancellation activated or lost generation", err)
		}
		r := recoverStore(t, s)
		assertQuarantined(t, r, id)
		if r.SnapshotRecovery()[0].Phase != "committed" {
			t.Fatal("lost actual commit evidence")
		}
	})
}

func TestPersistentLiveAggregateManifestBudget(t *testing.T) {
	limits := testLimits()
	limits.ManifestBytes = 1000
	s := persistentStore(t, limits)
	st, m, digest := persistentStage(t, s)
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	// UID/GID widths change encoded size; fill the actual byte budget, not a fixed count.
	count := limits.ManifestBytes / len(encoded)
	if count < 1 {
		t.Fatal("fixture cannot fit one manifest")
	}
	if _, err := st.Seal(context.Background(), KindCode, ids.New(), m, digest); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < count; i++ {
		_ = sealed(t, s, KindCode, ids.New())
	}
	if s.persistent.manifestBytes != count*len(encoded) {
		t.Fatal("live manifest byte accounting differs from fixture encoding")
	}
	st, m, digest = persistentStage(t, s)
	if _, err := st.Seal(context.Background(), KindCode, ids.New(), m, digest); err != ErrCapacity {
		t.Fatalf("aggregate manifest budget not reserved: %v", err)
	}
	if err := st.Abort(); err != nil || s.stages != 0 || s.bytes != int64(count)*100 {
		t.Fatal("failed aggregate reservation leaked", err)
	}
}

func TestPersistentMkdirCollisionNeverOwned(t *testing.T) {
	s := persistentStore(t, testLimits())
	ops := s.persistent.ops
	var unexpected string
	s.persistent.ops.write = func(f *os.File, raw []byte) (int, error) {
		var d persistentRecord
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		if d.Phase == "staging" {
			unexpected = filepath.Join(s.root, "staging-"+d.Generation)
			if err := os.Mkdir(unexpected, 0700); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(unexpected, "not-owned"), "preserve")
		}
		return ops.write(f, raw)
	}
	st, err := s.Begin(context.Background(), 100)
	if err != ErrIO || st == nil || st.rec.treeOwned {
		t.Fatal("unexpected collision became store-owned", err)
	}
	s.persistent.ops = ops
	if err := st.Abort(); err != ErrIO || s.bytes != 100 {
		t.Fatal("cleanup forgot unowned collision", err)
	}
	if _, err := os.Stat(filepath.Join(unexpected, "not-owned")); err != nil {
		t.Fatal("cleanup removed unexpected tree")
	}
}
