package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestPortableRestoreProducerFaultsKeepIntent(t *testing.T) {
	production := []string{"owner_provisioned", "runtime_sealed", "code_sealed",
		"database_committed", "associations_verified", "database_checkpointed"}
	fault := errors.New("interrupted producer")
	for _, phase := range append(production, "owner_closed") {
		t.Run(phase, func(t *testing.T) {
			data := t.TempDir()
			lock, err := servingmode.Acquire(data)
			mustManagedBoot(t, err)
			intent, err := lock.BeginPortableRestore(ids.New())
			mustManagedBoot(t, err)
			stage, err := os.MkdirTemp(data, ".orva-portable-stage-")
			mustManagedBoot(t, err)
			file := filepath.Join(stage, "orva.db")
			mustManagedBoot(t, os.WriteFile(file, []byte("staged database"), 0600))
			step := func(at string) error {
				if at == phase {
					return fault
				}
				return nil
			}
			err = publishPortableRestore(t.Context(), intent, stage, step, func() error {
				for _, at := range production {
					if err := step(at); err != nil {
						return err
					}
				}
				return nil
			})
			if !errors.Is(err, fault) {
				t.Fatalf("restore interrupted at %s: %v", phase, err)
			}
			mustManagedBoot(t, lock.Close())
			if data, err := os.ReadFile(file); err != nil || string(data) != "staged database" {
				t.Fatal("staged evidence was lost")
			}
			t.Setenv("ORVA_DATA_DIR", data)
			for _, mode := range []string{servingmode.Legacy, servingmode.Managed} {
				t.Setenv(servingmode.Env, mode)
				if code := runServe(newServeCmd(), nil); code != exitConfig {
					t.Fatalf("%s startup exit %d, want %d", mode, code, exitConfig)
				}
			}
		})
	}
}
