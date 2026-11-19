package servingmode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnrollmentFaultsAndPublication(t *testing.T) {
	for _, fault := range []string{"intent", "source_archived", "staged", "exchanged", "tombstone", "relocated", "intent_removed", ""} {
		t.Run("stop_"+fault, func(t *testing.T) {
			t.Setenv(Env, Legacy)
			root := t.TempDir()
			db := filepath.Join(root, dbName)
			source := filepath.Join(root, "functions")
			for _, path := range []string{db, filepath.Join(root, ".master.key")} {
				if err := os.WriteFile(path, []byte("preserved"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "historical"), []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := AcquireEnrollment(root); err == nil {
				t.Fatal("dry-run lock must not create a new file")
			}
			legacy, err := AcquireLegacy(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := AcquireEnrollment(root); err == nil {
				t.Fatal("enrollment acquired a live daemon lock")
			}
			legacy.Close()
			lock, err := AcquireEnrollment(root)
			if err != nil {
				t.Fatal(err)
			}
			e, err := lock.BeginEnrollment("plan")
			if err != nil {
				t.Fatal(err)
			}
			interrupted := errors.New("interrupted")
			if fault != "intent" {
				err = e.publish(func(stage string) error {
					if stage == fault {
						return interrupted
					}
					return nil
				})
				if (fault != "" && !errors.Is(err, interrupted)) || (fault == "" && err != nil) {
					t.Fatalf("publish: %v", err)
				}
				source = filepath.Join(root, "legacy-functions.enrolled-"+e.Record().Operation)
			}
			lock.Close()
			if _, err := AcquireLegacy(root); err == nil {
				t.Fatal("legacy opened after enrollment intent")
			}
			lock, err = Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			t.Setenv(Env, Managed)
			record, dir, err := lock.OpenData()
			if fault == "" {
				if err != nil || record != e.Record() {
					t.Fatalf("open published layout: %v", err)
				}
				dir.Close()
			} else if err == nil {
				dir.Close()
				t.Fatal("managed opened an incomplete layout")
			}
			for _, path := range []string{dbName, ".master.key"} {
				legacy, _ := os.ReadFile(filepath.Join(root, path))
				managed, _ := os.ReadFile(filepath.Join(root, Managed, path))
				if string(legacy) != "preserved" && string(managed) != "preserved" {
					t.Fatalf("lost %s", path)
				}
			}
			if archive, err := os.ReadFile(filepath.Join(source, "historical")); err != nil || string(archive) != "preserved" {
				t.Fatalf("lost legacy source: %v", err)
			}
		})
	}
}
