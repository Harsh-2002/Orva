package servingmode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestPortableRestorePublicationFaults(t *testing.T) {
	fault := errors.New("interrupted restore")
	for _, phase := range []string{"intent", "installed:orva.db", "installed:.master.key", "installed:.admin-key",
		"staged", "exchanged", "tombstone", "relocated", "intent_removed", "mode_staged", "committed"} {
		t.Run(phase, func(t *testing.T) {
			data := t.TempDir()
			l, err := Acquire(data)
			if err != nil {
				t.Fatal(err)
			}
			r, err := l.BeginPortableRestore(ids.New())
			if err != nil {
				t.Fatal(err)
			}
			stage, err := os.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			for _, file := range []string{dbName, ".master.key", ".admin-key"} {
				if err := os.WriteFile(filepath.Join(stage.Name(), file), []byte(file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if phase != "intent" {
				err = r.Install(int(stage.Fd()), func(got string) error {
					if got == phase {
						return fault
					}
					return nil
				})
				if (phase == "committed" && err != nil) || (phase != "committed" && !errors.Is(err, fault)) {
					t.Fatalf("install: %v", err)
				}
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			if file, err := AcquireLegacy(data); err == nil {
				file.Close()
				t.Fatal("legacy startup accepted")
			}
			l, err = Acquire(data)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			record, dir, err := l.OpenData()
			if dir != nil {
				dir.Close()
			}
			if phase == "committed" {
				if err != nil || record != r.Record() {
					t.Fatalf("valid managed startup refused: %v", err)
				}
			} else {
				if _, err := os.Lstat(filepath.Join(data, MarkerName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("published mode before completion: %v", err)
				}
				if err == nil {
					t.Fatal("managed startup accepted interrupted layout")
				}
			}
			for _, file := range []string{dbName, ".master.key", ".admin-key"} {
				found := false
				for _, root := range []string{stage.Name(), data, filepath.Join(data, Managed)} {
					body, _ := os.ReadFile(filepath.Join(root, file))
					found = found || string(body) == file
				}
				if !found {
					t.Fatalf("%s was lost", file)
				}
			}
		})
	}
}
