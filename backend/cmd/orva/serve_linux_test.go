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
		{servingmode.Legacy, servingmode.Legacy},
		{"malformed marker", servingmode.Legacy},
		{"dangling marker", servingmode.Legacy},
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
			case servingmode.Managed, servingmode.Provisioning, servingmode.Legacy:
				marker := fmt.Sprintf(`{"version":1,"mode":%q,"owner":%q,"operation":%q}`, tc.mode, ids.New(), ids.New())
				if err := os.WriteFile(filepath.Join(dir, servingmode.MarkerName), []byte(marker), 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed marker":
				if err := os.WriteFile(filepath.Join(dir, servingmode.MarkerName), []byte("not JSON"), 0600); err != nil {
					t.Fatal(err)
				}
			case "dangling marker":
				if err := os.Symlink("missing", filepath.Join(dir, servingmode.MarkerName)); err != nil {
					t.Fatal(err)
				}
			case "lock held":
				layout, err := servingmode.AcquireLegacy(dir)
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

func TestServeLegacyLayoutReachesSQLite(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	for _, layout := range []string{"group writable", "symlinked data", "symlinked parent"} {
		t.Run(layout, func(t *testing.T) {
			parent := t.TempDir()
			realDir := filepath.Join(parent, "data")
			if err := os.Mkdir(realDir, 0700); err != nil {
				t.Fatal(err)
			}
			configured := realDir
			switch layout {
			case "group writable":
				if err := os.Chmod(realDir, 0775); err != nil {
					t.Fatal(err)
				}
			case "symlinked data":
				configured = filepath.Join(parent, "alias")
				if err := os.Symlink(realDir, configured); err != nil {
					t.Fatal(err)
				}
			case "symlinked parent":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(parent, alias); err != nil {
					t.Fatal(err)
				}
				configured = filepath.Join(alias, "data")
			}
			// Force database.New to return without binding a listener.
			if err := os.WriteFile(filepath.Join(realDir, "orva.db"), make([]byte, 4096), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ORVA_DATA_DIR", configured)
			t.Setenv("ORVA_PPROF_ADDR", "")
			t.Setenv(servingmode.Env, servingmode.Legacy)
			if got := runServe(newServeCmd(), nil); got != 1 {
				t.Fatalf("exit status = %d, want database-open failure (1)", got)
			}
		})
	}
}
