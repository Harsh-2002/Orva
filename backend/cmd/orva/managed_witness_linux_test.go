package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupManagedWitness(t *testing.T) {
	root := t.TempDir()
	data, backup := filepath.Join(root, "data"), filepath.Join(root, "backup")
	for _, dir := range []string{data, backup} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ORVA_DATA_DIR", data)
	t.Setenv(managedBackupEnv, backup)
	run := func(path string) error {
		cmd := newSetupCmd()
		cmd.SetArgs([]string{"--managed-witness", path})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		return cmd.Execute()
	}
	for _, dir := range []string{data, backup} {
		if err := run(filepath.Join(dir, "private", "witness")); err == nil {
			t.Fatal("created a witness inside a backup root")
		}
		if _, err := os.Stat(filepath.Join(dir, "private")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid setup touched backup root: %v", err)
		}
	}
	path := filepath.Join(root, "host", "witness")
	if err := run(path); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{filepath.Dir(path): os.ModeDir | 0700, path: 0600} {
		info, err := os.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != mode {
			t.Fatalf("mode of %s = %o, want %o", name, info.Mode(), mode)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) != 65 || raw[64] != '\n' {
		t.Fatal("invalid witness framing", err)
	}
	key, err := hex.DecodeString(string(raw[:64]))
	if err != nil || bytes.Equal(key, make([]byte, 32)) {
		t.Fatal("invalid witness encoding")
	}
	if err := run(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("repeated setup = %v, want existing-file refusal", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("repeated setup changed the witness", err)
	}
	if _, err := os.Stat(filepath.Join(data, ".setup-complete")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("witness-only setup ran normal setup: %v", err)
	}
}
