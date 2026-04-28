package builder

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/internal/database"
)

func TestValidateArchive_Valid(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "handler.js"), []byte("module.exports = {}"), 0644)

	if err := ValidateArchive(dir, "node22", "handler.js"); err != nil {
		t.Errorf("expected valid archive, got: %v", err)
	}
}

func TestValidateArchive_MissingEntrypoint(t *testing.T) {
	dir := t.TempDir()

	err := ValidateArchive(dir, "node22", "handler.js")
	if err == nil {
		t.Error("expected error for missing entrypoint")
	}
}

func TestValidateArchive_ELFBinary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "handler.js"), []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}, 0644)

	err := ValidateArchive(dir, "node22", "handler.js")
	if err == nil {
		t.Error("expected ELF binary to be rejected")
	}
}

func TestValidateArchive_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "handler.js"), []byte("ok"), 0644)
	os.Symlink("/etc/passwd", filepath.Join(dir, "escape"))

	err := ValidateArchive(dir, "node22", "handler.js")
	if err == nil {
		t.Error("expected symlink escape to be rejected")
	}
}

func TestBuild_ExtractAndValidate(t *testing.T) {
	archivePath := createTestArchive(t, map[string]string{
		"handler.py": "def handler(event): return {'statusCode': 200}",
	})

	b := &Builder{DataDir: t.TempDir()}
	fn := &database.Function{
		ID:         "fn_test123",
		Name:       "test-fn",
		Runtime:    "python313",
		Entrypoint: "handler.py",
	}

	result, err := b.Build(nil, fn, archivePath)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	if result.CodeHash == "" {
		t.Error("expected non-empty code hash")
	}

	// Round-G: code lives under versions/<hash>/, not code/. Use the result's
	// VersionDir to find the adapter so we don't have to recompute the hash.
	if result.VersionDir == "" {
		t.Fatalf("expected non-empty version dir in BuildResult")
	}
	mainPy := filepath.Join(result.VersionDir, "main.py")
	if _, err := os.Stat(mainPy); err != nil {
		t.Errorf("expected main.py adapter to be created: %v", err)
	}
	// Readiness marker should be present.
	if _, err := os.Stat(filepath.Join(result.VersionDir, ".orva-ready")); err != nil {
		t.Errorf("expected .orva-ready marker in version dir: %v", err)
	}
}

func TestBuild_UnsupportedRuntime(t *testing.T) {
	archivePath := createTestArchive(t, map[string]string{
		"main.rb": "puts 'hello'",
	})

	b := &Builder{DataDir: t.TempDir()}
	fn := &database.Function{
		ID:         "fn_ruby",
		Name:       "ruby-fn",
		Runtime:    "ruby33",
		Entrypoint: "main.rb",
	}

	_, err := b.Build(nil, fn, archivePath)
	if err == nil {
		t.Error("expected error for unsupported runtime")
	}
}

func createTestArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	for name, content := range files {
		tw.WriteHeader(&tar.Header{
			Name: name,
			Size: int64(len(content)),
			Mode: 0644,
		})
		tw.Write([]byte(content))
	}

	return archivePath
}
