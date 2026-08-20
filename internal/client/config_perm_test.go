package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveConfigSecuresAnExistingWorldReadableFile — os.WriteFile's mode
// argument only applies at creation, so a config that already existed at
// 0644 kept 0644 and `orva login` wrote the API key into a world-readable
// file while reporting success. The docs and the installer both promise
// mode 0600 unconditionally. Copying a config to make a second profile
// (`cp` creates under umask) is the ordinary way to land here.
func TestSaveConfigSecuresAnExistingWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("endpoint: http://old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ORVA_CONFIG", path)
	cfg := &CLIConfig{Endpoint: "https://orva.example", APIKey: "orva_secret_key"}
	if err := SaveCLIConfig(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("config mode = %04o, want 0600 - the API key is readable by other users", mode)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Error("config was written empty")
	}
}

func TestSaveConfigCreatesAt0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.yaml")
	t.Setenv("ORVA_CONFIG", path)
	if err := SaveCLIConfig(&CLIConfig{Endpoint: "https://x", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("new config mode = %04o, want 0600", mode)
	}
}
