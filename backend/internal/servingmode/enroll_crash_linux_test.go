package servingmode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		if err := os.WriteFile(path, []byte("preserved"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "functions")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "historical"), []byte("preserved"), 0600); err != nil {
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

// enrollmentCrashPreserved checks that every durable payload still exists once,
// wherever the interrupted relocation left it.
func enrollmentCrashPreserved(t *testing.T, root string) error {
	t.Helper()
	want := map[string]int{"orva.db": 1, ".master.key": 1, ".admin-key": 1, "historical": 1}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(raw) == "preserved" {
			want[filepath.Base(path)]--
		}
		return nil
	})
	for name, n := range want {
		if n != 0 {
			t.Errorf("%s preserved %d times, want 1", name, 1-n)
		}
	}
	return err
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

			// No serving mode before the last boundary; the intent is unlinked
			// after relocation, before the record is staged.
			published := stage == ""
			intent := stage != "" && stage != "intent_removed" && stage != "mode_staged"
			if _, err := os.Lstat(filepath.Join(root, MarkerName)); (err == nil) != published {
				t.Fatalf("serving mode published=%t at %q, want %t", err == nil, stage, published)
			}
			if _, err := os.Lstat(filepath.Join(root, enrollIntent)); (err == nil) != intent {
				t.Fatalf("enrollment intent present=%t at %q, want %t", err == nil, stage, intent)
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
			} else if !intent && !strings.Contains(legacyErr.Error(), "is a directory") {
				t.Fatalf("legacy refusal at %q = %v, want the orva.db tombstone", stage, legacyErr)
			}

			// Managed serve: the same Acquire/OpenData gate runServeManaged uses.
			if published {
				t.Setenv(Env, Managed)
			}
			lock, err := Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			record, dir, err := lock.OpenData()
			if published {
				if err != nil || record.Mode != Managed || record.Owner == "" || record.Operation == "" {
					t.Fatalf("managed serve refused a published layout: %+v %v", record, err)
				}
				dir.Close()
			} else if err == nil {
				dir.Close()
				t.Fatal("managed serve opened an interrupted enrollment")
			}

			if err := enrollmentCrashPreserved(t, root); err != nil {
				t.Fatalf("lost durable data: %v", err)
			}
			archived, err := filepath.Glob(filepath.Join(root, "legacy-functions.enrolled-*"))
			if err != nil || len(archived) > 1 || (stage != "intent" && len(archived) != 1) {
				t.Fatalf("legacy source archives at %q = %v, %v", stage, archived, err)
			}
			if stage == "intent" {
				if st, err := os.Stat(filepath.Join(root, "functions", "historical")); err != nil || !st.Mode().IsRegular() {
					t.Fatalf("legacy source moved before publication: %v", err)
				}
			}
		})
	}
}
