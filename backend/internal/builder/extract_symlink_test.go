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

// TestExtractPreservesContainedSymlinks — extraction had no case for
// TypeSymlink at all, so links were silently dropped: the archive was
// accepted minus its links, and the symlink-escape branch in the validator
// was unreachable because no symlink could exist in the extracted tree. The
// CLI now archives symlinks properly, so dropping them would lose them.
func TestExtractPreservesContainedSymlinks(t *testing.T) {
	archive := tarGz(t, func(tw *tar.Writer) {
		writeReg(t, tw, "shared.js", "module.exports = 1;\n")
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeSymlink, Name: "lib.js", Linkname: "shared.js", Mode: 0o777,
		}); err != nil {
			t.Fatal(err)
		}
	})

	dest := t.TempDir()
	if err := extractTarGz(archive, dest); err != nil {
		t.Fatalf("extract failed on a contained symlink: %v", err)
	}

	target, err := os.Readlink(filepath.Join(dest, "lib.js"))
	if err != nil {
		t.Fatalf("symlink was not recreated: %v", err)
	}
	if target != "shared.js" {
		t.Errorf("symlink target = %q, want shared.js", target)
	}
}

// TestExtractRejectsEscapingSymlink — the containment check has to be real
// now that links are actually created.
func TestExtractRejectsEscapingSymlink(t *testing.T) {
	for _, link := range []string{"../../../etc/passwd", "/etc/passwd"} {
		t.Run(link, func(t *testing.T) {
			archive := tarGz(t, func(tw *tar.Writer) {
				if err := tw.WriteHeader(&tar.Header{
					Typeflag: tar.TypeSymlink, Name: "evil.js", Linkname: link, Mode: 0o777,
				}); err != nil {
					t.Fatal(err)
				}
			})
			err := extractTarGz(archive, t.TempDir())
			if err == nil {
				t.Fatalf("symlink to %q was accepted", link)
			}
			if !strings.Contains(err.Error(), "symlink") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestExtractRejectsHardLinks — a hard link can reference a file outside
// the archive entirely and no deploy needs one. Refuse rather than drop.
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
