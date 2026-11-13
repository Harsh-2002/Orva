package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateArchivePrebundle(t *testing.T) {
	for _, language := range []string{"js", "ts"} {
		t.Run(language, func(t *testing.T) {
			root := t.TempDir()
			want := map[string][]byte{}
			for _, name := range []string{"dist/handler.js", "dist/handler.js.map"} {
				data, err := os.ReadFile(filepath.Join("..", "..", "test", "fixtures", "node-prebundle", language, "upload", name))
				if err != nil {
					t.Fatal(err)
				}
				want[name] = data
			}
			files := map[string][]byte{}
			for name, data := range want {
				files[name] = data
			}
			files["node_modules/esbuild/bin/esbuild"] = []byte("author tool must not ship")
			files[".tools/esbuild"] = []byte("hidden author tool must not ship")
			for name, data := range files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if got := detectTSEntrypoint(root); got != "" {
				t.Fatalf("JavaScript upload detected as TypeScript: %q", got)
			}
			archive, err := createArchive(root)
			if archive != "" {
				defer os.Remove(archive)
			}
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(archive)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			gz, err := gzip.NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			defer gz.Close()
			tr := tar.NewReader(gz)
			seen := map[string]bool{}
			for {
				hdr, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if hdr.Typeflag == tar.TypeDir {
					continue
				}
				name := filepath.ToSlash(hdr.Name)
				data, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				expected, ok := want[name]
				if !ok || !bytes.Equal(data, expected) || seen[name] {
					t.Fatalf("unexpected, changed or duplicate archive member %q", name)
				}
				seen[name] = true
			}
			if len(seen) != len(want) {
				t.Fatalf("archive members = %v, want bundle and map", seen)
			}
		})
	}
}
