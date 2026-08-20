package commands

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateArchiveHandlesSymlinks — filepath.Walk uses Lstat, so a symlink
// arrives as a symlink; FileInfoHeader then emits a zero-size TypeSymlink
// header and the old code fell through to Open+Copy, writing the target's
// content into it. tar rejected that with "write too long" and the whole
// deploy aborted. A lib.js -> shared.js link is ordinary in a JS project.
func TestCreateArchiveHandlesSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shared.js"),
		[]byte("module.exports = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared.js", filepath.Join(dir, "lib.js")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	archive, err := createArchive(dir)
	if archive != "" {
		defer os.Remove(archive)
	}
	if err != nil {
		t.Fatalf("createArchive failed on a tree containing a symlink: %v", err)
	}

	names, links := readArchive(t, archive)
	if _, ok := names["shared.js"]; !ok {
		t.Error("regular file missing from the archive")
	}
	// DEREFERENCED, not archived as a link: the builder refuses link entries,
	// because recreating one from an untrusted archive lets an early link
	// redirect a later write outside the extraction root, and lexical
	// containment cannot see through that chain.
	if len(links) != 0 {
		t.Errorf("archive carries link entries the builder will refuse: %v", links)
	}
	if got := names["lib.js"]; got != "module.exports = 1;\n" {
		t.Errorf("lib.js = %q, want the target's contents", got)
	}
}

// TestCreateArchiveRejectsDirectorySymlinks — following one could duplicate a
// whole tree, or loop forever on a cycle. Fail with a message that says what
// to do rather than silently omitting the path.
func TestCreateArchiveRejectsDirectorySymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	archive, err := createArchive(dir)
	if archive != "" {
		defer os.Remove(archive)
	}
	if err == nil {
		t.Fatal("a symlink to a directory was packed")
	}
	if !strings.Contains(err.Error(), "symlink to a directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestCreateArchiveReportsBrokenSymlinks — a dangling link used to produce a
// confusing tar error; it should name the file.
func TestCreateArchiveReportsBrokenSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("nope.js", filepath.Join(dir, "dangling.js")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	archive, err := createArchive(dir)
	if archive != "" {
		defer os.Remove(archive)
	}
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Errorf("broken symlink not reported clearly: %v", err)
	}
}

// TestCreateArchiveStillPacksRegularFiles guards the fix against dropping
// ordinary content.
func TestCreateArchiveStillPacksRegularFiles(t *testing.T) {
	dir := t.TempDir()
	const body = "exports.handler = async () => ({ok:true});\n"
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "util.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	archive, err := createArchive(dir)
	if archive != "" {
		defer os.Remove(archive)
	}
	if err != nil {
		t.Fatal(err)
	}
	names, _ := readArchive(t, archive)
	if names["handler.js"] != body {
		t.Errorf("handler.js content = %q, want %q", names["handler.js"], body)
	}
	if _, ok := names[filepath.Join("sub", "util.js")]; !ok {
		t.Error("nested file missing from the archive")
	}
}

func readArchive(t *testing.T, path string) (files map[string]string, links map[string]string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	files, links = map[string]string{}, map[string]string{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading archive: %v", err)
		}
		switch h.Typeflag {
		case tar.TypeSymlink:
			links[h.Name] = h.Linkname
		case tar.TypeReg:
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			files[h.Name] = string(b)
		}
	}
	return files, links
}
