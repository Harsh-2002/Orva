package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestServeRefusesBeforeOpeningDatabase(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	for _, tc := range []struct{ mode, env string }{
		{"tombstone", servingmode.Legacy},
		{servingmode.Managed, servingmode.Managed},
		{servingmode.Provisioning, servingmode.Provisioning},
		{"absent", servingmode.Managed},
		{"absent", ""},
		{"lock held", servingmode.Legacy},
	} {
		t.Run(fmt.Sprintf("layout=%s/env=%q", tc.mode, tc.env), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("ORVA_DATA_DIR", dir)
			t.Setenv(servingmode.Env, tc.env)
			db := filepath.Join(dir, "orva.db")
			switch tc.mode {
			case "tombstone":
				if err := os.Mkdir(db, 0500); err != nil {
					t.Fatal(err)
				}
			case servingmode.Managed, servingmode.Provisioning:
				marker := fmt.Sprintf(`{"version":1,"mode":%q,"owner":%q,"operation":%q}`, tc.mode, ids.New(), ids.New())
				if err := os.WriteFile(filepath.Join(dir, servingmode.MarkerName), []byte(marker), 0600); err != nil {
					t.Fatal(err)
				}
			case "lock held":
				layout, err := servingmode.Acquire(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer layout.Close()
			}
			if got := runServe(newServeCmd(), nil); got != 78 {
				t.Fatalf("exit status = %d, want 78", got)
			}
			if tc.mode != "tombstone" {
				if _, err := os.Lstat(db); !os.IsNotExist(err) {
					t.Fatalf("legacy SQLite path touched: %v", err)
				}
			}
		})
	}
}
