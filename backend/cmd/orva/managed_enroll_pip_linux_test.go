package main

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrollRejectsUnsafePipRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"escape", "../evil", "cannot distinguish"},
		{"absolute", "/etc/passwd", "cannot distinguish"},
		{"malformed script path", "../../bin/../x", "cannot distinguish"},
		{"authored entrypoint", "handler.py", "cannot distinguish"},
		{"requirements manifest", "requirements.txt", "cannot distinguish"},
		{"unhashed outside metadata", "author.py", "cannot verify installed file"},
		{"symlinked package directory", "redirect/__init__.py", `output "redirect" is not a directory`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const record = "pkg.dist-info/RECORD"
			enrollmentMust(t, os.Mkdir(filepath.Join(dir, filepath.Dir(record)), 0700))
			digest := ""
			if tc.name == "symlinked package directory" {
				// A real contained file with the right hash still cannot make
				// a symlink acceptable as an installed package directory.
				enrollmentMust(t, os.Mkdir(filepath.Join(dir, "real"), 0700))
				body := []byte("package")
				enrollmentMust(t, os.WriteFile(filepath.Join(dir, "real", "__init__.py"), body, 0600))
				enrollmentMust(t, os.Symlink("real", filepath.Join(dir, "redirect")))
				h := sha256.Sum256(body)
				digest = "sha256=" + base64.RawURLEncoding.EncodeToString(h[:])
			} else if tc.name == "unhashed outside metadata" {
				enrollmentMust(t, os.WriteFile(filepath.Join(dir, tc.path), []byte("authored"), 0600))
			}
			raw := record + ",,\n" + tc.path + "," + digest + ",\n"
			enrollmentMust(t, os.WriteFile(filepath.Join(dir, record), []byte(raw), 0600))
			got, err := enrolledPipFiles(t.Context(), dir, "handler.py")
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, %v; want no projection and error containing %q", got, err, tc.want)
			}
		})
	}
}
