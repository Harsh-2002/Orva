package servingmode

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
	"golang.org/x/sys/unix"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func acquire(t *testing.T, dir string) *Lock {
	t.Helper()
	l, err := Acquire(dir)
	must(t, err)
	return l
}

func TestModeRecord(t *testing.T) {
	r := Record{Version: 1, Mode: Managed, Owner: ids.New(), Operation: ids.New()}
	raw, err := json.Marshal(r)
	must(t, err)
	text := string(raw)
	for name, input := range map[string]string{
		"whitespace": text + "\n",
		"trailing":   text + `{}`,
		"duplicate":  strings.Replace(text, `"mode":`, `"mode":"managed","mode":`, 1),
		"unknown":    strings.Replace(text, "{", `{"unexpected":true,`, 1),
		"ordering":   strings.Replace(text, `{"version":1,"mode":"managed"`, `{"mode":"managed","version":1`, 1),
		"version":    strings.Replace(text, `"version":1`, `"version":2`, 1),
		"mode":       strings.Replace(text, `"managed"`, `"other"`, 1),
		"case":       strings.Replace(text, `"mode"`, `"Mode"`, 1),
		"owner":      strings.Replace(text, r.Owner, "not-a-uuid", 1),
		"operation":  strings.Replace(text, r.Operation, "e4208ef6-c6b8-4d37-8181-ccba8c9f4d35", 1),
		"oversize":   strings.Repeat(" ", maxMarker+1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			l := acquire(t, dir)
			defer l.Close()
			must(t, os.WriteFile(filepath.Join(dir, MarkerName), []byte(input), 0600))
			if _, err := l.Read(); err == nil {
				t.Fatal("accepted invalid marker")
			}
		})
	}
	dir := t.TempDir()
	l := acquire(t, dir)
	defer l.Close()
	absent, err := l.Read()
	must(t, err)
	if absent != (Record{Mode: Legacy}) {
		t.Fatalf("absent marker: %+v", absent)
	}
	for _, mode := range []string{Legacy, Provisioning, Managed} {
		r.Mode = mode
		must(t, l.write(r))
		got, err := l.Read()
		must(t, err)
		if got != r {
			t.Fatalf("read %+v, want %+v", got, r)
		}
	}
}

func TestModeControlFileTypes(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "directory", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			l := acquire(t, dir)
			defer l.Close()
			r := Record{Version: 1, Mode: Managed, Owner: ids.New(), Operation: ids.New()}
			must(t, l.write(r))
			path, original := filepath.Join(dir, MarkerName), filepath.Join(dir, "original")
			must(t, os.Rename(path, original))
			switch kind {
			case "symlink":
				must(t, os.Symlink(original, path))
			case "hardlink":
				must(t, os.Link(original, path))
			case "fifo":
				must(t, unix.Mkfifo(path, 0600))
			case "directory":
				must(t, os.Mkdir(path, 0700))
			case "permissions":
				must(t, os.Rename(original, path))
				must(t, os.Chmod(path, 0644))
			}
			if _, err := l.Read(); err == nil {
				t.Fatal("accepted unsafe control file")
			}
		})
	}
}

func TestRelocationProcessExit(t *testing.T) {
	if point := os.Getenv("ORVA_TEST_RELOCATION_POINT"); point != "" {
		l := acquire(t, os.Getenv("ORVA_TEST_RELOCATION_DIR"))
		must(t, l.relocate(Record{Version: 1, Mode: Managed, Owner: ids.New(), Operation: ids.New()}, func(step string) error {
			if step == point {
				os.Exit(90)
			}
			return nil
		}))
		os.Exit(91)
	}
	t.Setenv(Env, Legacy)
	for _, step := range []string{"staged", "exchanged", "tombstone", "relocated", "complete"} {
		t.Run(step, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, dbName)
			inner := filepath.Join(dir, Managed)
			must(t, os.WriteFile(db, []byte("database fixture"), 0600))
			must(t, os.WriteFile(filepath.Join(dir, ".master.key"), []byte("key fixture"), 0600))
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRelocationProcessExit$")
			cmd.Env = append(os.Environ(), "ORVA_TEST_RELOCATION_POINT="+step, "ORVA_TEST_RELOCATION_DIR="+dir)
			want := 90
			if step == "complete" {
				want = 91
			}
			var ee *exec.ExitError
			raw, err := cmd.CombinedOutput()
			if !errors.As(err, &ee) || ee.ExitCode() != want {
				t.Fatalf("child exit: %v, want %d: %s", err, want, raw)
			}
			l := acquire(t, dir)
			defer l.Close()
			record, err := l.Read()
			must(t, err)
			if step == "staged" {
				must(t, l.RequireLegacy())
				assertFile(t, db, "database fixture")
				assertFile(t, filepath.Join(dir, ".master.key"), "key fixture")
			} else {
				if err := l.RequireLegacy(); err == nil {
					t.Fatal("legacy accepted after tombstone installation")
				}
				if step == "exchanged" {
					info, err := os.Lstat(db)
					must(t, err)
					if !info.IsDir() {
						t.Fatal("exchange did not immediately block the old SQLite path")
					}
				} else {
					must(t, checkTombstone(int(l.root.Fd()), dbName))
				}
				assertFile(t, filepath.Join(inner, dbName), "database fixture")
				keyRoot := inner
				if step == "exchanged" || step == "tombstone" {
					keyRoot = dir
				}
				assertFile(t, filepath.Join(keyRoot, ".master.key"), "key fixture")
			}
			if other, err := Acquire(inner); err == nil {
				other.Close()
				t.Fatal("accepted managed subdirectory as data root")
			}
			if step != "complete" {
				if record.Mode != Legacy || record.Version != 0 {
					t.Fatalf("mode was published early: %+v", record)
				}
				if step != "staged" {
					if _, f, err := l.OpenData(); err == nil {
						f.Close()
						t.Fatal("opened partially relocated layout")
					}
				}
				return
			}
			t.Setenv(Env, Managed)
			got, f, err := l.OpenData()
			must(t, err)
			defer f.Close()
			if got != record || record.Mode != Managed || f.Name() != inner {
				t.Fatalf("wrong completed layout: %+v, %s", got, f.Name())
			}
		})
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	must(t, err)
	if string(got) != want {
		t.Fatalf("%s: got %q, want %q", path, got, want)
	}
}

func TestRelocationRejectsUnsafeInputs(t *testing.T) {
	for _, name := range []string{"orva.db-wal", "orva.db-shm", "orva.db-journal", ".workload-recovery", Managed} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, dbName)
			must(t, os.WriteFile(db, []byte("database fixture"), 0600))
			must(t, os.WriteFile(filepath.Join(dir, name), nil, 0600))
			l := acquire(t, dir)
			defer l.Close()
			if err := l.RelocateToManaged(ids.New(), ids.New()); err == nil {
				t.Fatal("relocation accepted unsafe layout")
			}
			assertFile(t, db, "database fixture")
			if r, err := l.Read(); err != nil || r.Mode != Legacy {
				t.Fatalf("mode changed: %+v, %v", r, err)
			}
		})
	}
}
