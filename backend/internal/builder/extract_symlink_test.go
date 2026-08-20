package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, write func(*tar.Writer)) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeReg(t *testing.T, tw *tar.Writer, name, body string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Size: int64(len(body)), Mode: 0o644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

// TestExtractRefusesSymlinkEntries — link entries are refused, not
// recreated and not silently dropped.
//
// The original code had no case for them at all, so an archive was accepted
// minus its links. Recreating them after a lexical containment check was
// worse: CodeQL flagged it (go/unsafe-unzip-symlink) and was right to. A
// lexical check cannot see through a chain -- extract "a -> ." and a later
// "a/../../evil" resolves through the first link at write time, landing
// outside the root that string manipulation said was safe.
//
// `orva deploy` dereferences symlinks when packing, so ordinary projects
// never produce a link entry here.
func TestExtractRefusesSymlinkEntries(t *testing.T) {
	for _, link := range []string{"shared.js", "../../../etc/passwd", "/etc/passwd", "."} {
		t.Run(link, func(t *testing.T) {
			archive := tarGz(t, func(tw *tar.Writer) {
				writeReg(t, tw, "shared.js", "module.exports = 1;\n")
				if err := tw.WriteHeader(&tar.Header{
					Typeflag: tar.TypeSymlink, Name: "lib.js", Linkname: link, Mode: 0o777,
				}); err != nil {
					t.Fatal(err)
				}
			})
			dest := t.TempDir()
			err := extractTarGz(archive, dest)
			if err == nil {
				t.Fatalf("symlink -> %q was accepted", link)
			}
			if !strings.Contains(err.Error(), "symlink") {
				t.Errorf("unexpected error: %v", err)
			}
			// And nothing was created for it.
			if _, lerr := os.Lstat(filepath.Join(dest, "lib.js")); lerr == nil {
				t.Error("a link was created despite the refusal")
			}
		})
	}
}

// TestExtractRejectsHardLinks — a hard link can reference a file outside the
// archive entirely.
func TestExtractRejectsHardLinks(t *testing.T) {
	archive := tarGz(t, func(tw *tar.Writer) {
		writeReg(t, tw, "a.js", "x")
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeLink, Name: "b.js", Linkname: "a.js", Mode: 0o644,
		}); err != nil {
			t.Fatal(err)
		}
	})
	if err := extractTarGz(archive, t.TempDir()); err == nil {
		t.Error("hard link was accepted")
	}
}

// TestExtractStillHandlesOrdinaryArchives — the refusal must not break a
// normal deploy.
func TestExtractStillHandlesOrdinaryArchives(t *testing.T) {
	const body = "exports.handler = async () => ({ok:true});\n"
	archive := tarGz(t, func(tw *tar.Writer) {
		writeReg(t, tw, "handler.js", body)
		writeReg(t, tw, "sub/util.js", "x")
	})
	dest := t.TempDir()
	if err := extractTarGz(archive, dest); err != nil {
		t.Fatalf("ordinary archive rejected: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "handler.js"))
	if err != nil || string(got) != body {
		t.Errorf("handler.js = %q, %v", string(got), err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "util.js")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
}
