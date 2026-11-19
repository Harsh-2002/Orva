package servingmode

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// The child re-execs this test binary so an interrupted enrollment is a real
// process death: no deferred close, no in-process rollback, and nothing but the
// kernel releases the lock. Same pattern as artifactfs.TestOwnerPreparedCrashHelper.
const (
	enrollCrashStageEnv = "ORVA_ENROLL_CRASH_STAGE"
	enrollCrashDataEnv  = "ORVA_ENROLL_CRASH_DATA_DIR"
	enrollCrashExit     = 97
)

// enrollmentCrashStages are the durable boundaries of the publication half in
// the order the code reaches them. "" is an uninterrupted run.
//
// The intent is unlinked after "relocated" and before the serving-mode record is
// staged, so "intent_removed" and "mode_staged" are the interval the orva.db
// tombstone alone guards.
var enrollmentCrashStages = []string{
	"intent", "source_archived", "staged", "exchanged", "tombstone",
	"relocated", "intent_removed", "mode_staged", "",
}

func TestEnrollmentCrashHelper(t *testing.T) {
	root, child := os.LookupEnv(enrollCrashDataEnv)
	if !child {
		return
	}
	enrollmentCrashChild(root, os.Getenv(enrollCrashStageEnv))
}

// enrollmentCrashChild performs the real publication and dies at one boundary.
func enrollmentCrashChild(root, stage string) {
	lock, err := AcquireEnrollment(root)
	if err != nil {
		os.Exit(1)
	}
	e, err := lock.BeginEnrollment("plan")
	if err != nil {
		os.Exit(1)
	}
	fmt.Println(e.Record().Operation)
	if stage == "intent" {
		os.Exit(enrollCrashExit)
	}
	if err := e.publish(func(s string) error {
		if s == stage {
			os.Exit(enrollCrashExit)
		}
		return nil
	}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// enrollmentCrashFixture builds the legacy layout an enrollment converts: a
// private database, both key files and a source tree with history in it.
func enrollmentCrashFixture(t *testing.T) string {
	t.Helper()
	t.Setenv(Env, Legacy)
	root := t.TempDir()
	for _, name := range []string{dbName, ".master.key", ".admin-key"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("preserved:"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "functions")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "historical"), []byte("preserved:historical"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := AcquireLegacy(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func enrollmentCrashPreserved(t *testing.T, root, stage, operation string) {
	t.Helper()
	archived, exchanged, relocated := true, true, true
	switch stage {
	case "intent":
		archived = false
		fallthrough
	case "source_archived", "staged":
		exchanged = false
		fallthrough
	case "exchanged", "tombstone":
		relocated = false
	}
	source := "functions"
	var wantArchives []string
	if archived {
		source = "legacy-functions.enrolled-" + operation
		wantArchives = []string{filepath.Join(root, source)}
		if _, err := os.Lstat(filepath.Join(root, "functions")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unarchived functions at %q: %v; want absent", stage, err)
		}
	}
	if archives, err := filepath.Glob(filepath.Join(root, "legacy-functions.enrolled-*")); err != nil || !slices.Equal(archives, wantArchives) {
		t.Errorf("source archives at %q = %v, %v; want %v", stage, archives, err, wantArchives)
	}
	if stage == "intent" || stage == "source_archived" {
		if _, err := os.Lstat(filepath.Join(root, Managed)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("managed directory at %q: %v; want absent", stage, err)
		}
	}
	dbDir, keyDir := "", ""
	if exchanged {
		dbDir = Managed
		mode := os.ModeDir | 0500
		if stage == "exchanged" {
			mode = os.ModeDir | 0700
		}
		st, err := os.Lstat(filepath.Join(root, dbName))
		if err != nil || st.Mode() != mode {
			t.Errorf("root SQLite tombstone at %q = %v, %v; want mode %v", stage, st, err, mode)
		}
	} else if stage == "staged" {
		st, err := os.Lstat(filepath.Join(root, Managed, dbName))
		if err != nil || st.Mode() != os.ModeDir|0700 {
			t.Errorf("staged SQLite tombstone = %v, %v; want a 0700 directory", st, err)
		}
	}
	if relocated {
		keyDir = Managed
	}
	want := map[string]string{
		filepath.Join(dbDir, dbName):         dbName,
		filepath.Join(keyDir, ".master.key"): ".master.key",
		filepath.Join(keyDir, ".admin-key"):  ".admin-key",
		filepath.Join(source, "historical"):  "historical",
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(raw), "preserved:") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if name, ok := want[rel]; !ok || string(raw) != "preserved:"+name || info.Mode() != 0600 {
			t.Errorf("unexpected fixture payload at %q in stage %q", rel, stage)
		}
		delete(want, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path := range want {
		t.Errorf("missing fixture payload %q at stage %q", path, stage)
	}
}

// TestEnrollmentPublicationProcessKill kills a real enrollment process at every
// durable boundary and checks the two invariants an operator depends on: both
// serving paths refuse the layout, and nothing was deleted. The final case is
// the completion control, where the layout must be usable.
func TestEnrollmentPublicationProcessKill(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range enrollmentCrashStages {
		t.Run("stop_at_"+stage, func(t *testing.T) {
			root := enrollmentCrashFixture(t)
			cmd := exec.Command(bin, "-test.run=^TestEnrollmentCrashHelper$", "-test.timeout=2m")
			cmd.Env = append(os.Environ(), enrollCrashStageEnv+"="+stage, enrollCrashDataEnv+"="+root)
			out, err := cmd.CombinedOutput()
			var status *exec.ExitError
			switch {
			case stage == "" && err != nil:
				t.Fatalf("uninterrupted publication failed: %v\n%s", err, out)
			case stage != "" && (!errors.As(err, &status) || status.ExitCode() != enrollCrashExit):
				t.Fatalf("child did not stop at %q: %v\n%s", stage, err, out)
			}
			operation := strings.TrimSpace(string(out))
			if !ids.IsUUID(operation) {
				t.Fatalf("invalid child enrollment operation: %q", operation)
			}

			// No serving mode before the last boundary; the intent is unlinked
			// after relocation, before the record is staged.
			published := stage == ""
			intent := stage != "" && stage != "intent_removed" && stage != "mode_staged"
			for name, present := range map[string]bool{MarkerName: published, enrollIntent: intent} {
				_, err := os.Lstat(filepath.Join(root, name))
				if (present && err != nil) || (!present && !errors.Is(err, os.ErrNotExist)) {
					t.Fatalf("stat %s at %q: %v; want present=%t", name, stage, err, present)
				}
			}
			if stage == "mode_staged" {
				if draft, err := filepath.Glob(filepath.Join(root, MarkerName+".tmp-*")); err != nil || len(draft) != 1 {
					t.Fatalf("staged serving-mode draft = %v, %v", draft, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, lockName)); err != nil {
				t.Fatalf("lock file removed: %v", err)
			}

			// Legacy serve: the intent, or the tombstone that replaces it, closes
			// this path. A legacy layout that looked complete here would be the
			// defect: its orva.db has already become the managed tombstone.
			const tombstoneRefusal = "legacy SQLite path orva.db is a directory; leaving the tombstone intact"
			legacy, legacyErr := AcquireLegacy(root)
			if published {
				// The published marker is what closes legacy serve for good.
				if legacyErr == nil || !strings.Contains(legacyErr.Error(), MarkerName) {
					if legacyErr == nil {
						legacy.Close()
					}
					t.Fatalf("legacy serve accepted a published layout: %v", legacyErr)
				}
			} else if legacyErr == nil {
				legacy.Close()
				t.Fatal("legacy serve opened an interrupted enrollment")
			} else if intent && !errors.Is(legacyErr, errEnrollmentIncomplete) {
				t.Fatalf("legacy refusal at %q = %v, want the enrollment intent", stage, legacyErr)
			} else if !intent && legacyErr.Error() != tombstoneRefusal {
				t.Fatalf("legacy refusal at %q = %v, want the orva.db tombstone", stage, legacyErr)
			}

			// Managed serve: the same Acquire/OpenData gate runServeManaged uses.
			if published {
				t.Setenv(Env, Managed)
			} else {
				t.Setenv(Env, Legacy)
			}
			lock, err := Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			record, dir, err := lock.OpenData()
			if published {
				if err != nil || record.Mode != Managed || record.Owner == "" || record.Operation != operation {
					t.Fatalf("managed serve refused a published layout: %+v %v", record, err)
				}
				dir.Close()
			} else if err == nil {
				dir.Close()
				t.Fatal("managed serve opened an interrupted enrollment")
			} else if (intent && !errors.Is(err, errEnrollmentIncomplete)) || (!intent && err.Error() != tombstoneRefusal) {
				t.Fatalf("OpenData refused for the wrong reason at %q: %v", stage, err)
			}

			enrollmentCrashPreserved(t, root, stage, operation)
		})
	}
}
