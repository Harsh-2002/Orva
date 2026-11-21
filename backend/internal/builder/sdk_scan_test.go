package builder

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceUsesOrvaSDK(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"empty", "", false},
		{"node cjs single quotes", `const { kv } = require('orva')`, true},
		{"node cjs double quotes", `const orva = require("orva");`, true},
		{"node esm single quotes", `import { kv } from 'orva'`, true},
		{"node esm double quotes", `import { kv } from "orva";`, true},
		{"python from-import", "from orva import kv\n", true},
		{"python from-import tab", "from orva\timport kv\n", true},
		{"python plain import", "import orva\nfrom datetime import datetime\n", true},
		{"unrelated comment mentioning orva", "// the orva platform deploys this\nexports.handler = async () => 'hi'", false},
		{"requires orvado not orva", `require('orvado')`, false},
		{"python from-orva-package not bare", "from orva.kv import client", true}, // legitimately imports the SDK
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SourceUsesOrvaSDK(tc.src)
			if got != tc.want {
				t.Fatalf("SourceUsesOrvaSDK(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

func TestArchiveEntrypointUsesOrvaSDK(t *testing.T) {
	const source = `const { kv } = require('orva')`
	for _, tc := range []struct {
		name, preceding, entrypoint, handler string
		want                                 bool
	}{
		{"matches configured handler", "", "src/handler.js", source, true},
		{"ignores other files", source, "src/handler.js", "exports.handler = () => 1", false},
		{"missing entrypoint", "", "missing.js", source, false},
		{"bounded archive scan", strings.Repeat(" ", 5<<20), "src/handler.js", source, false},
		{"bounded source scan", "", "src/handler.js", strings.Repeat(" ", 256<<10) + source, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "src.tar.gz")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			z := gzip.NewWriter(f)
			w := tar.NewWriter(z)
			add := func(name, body string) {
				t.Helper()
				if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			add("other.js", tc.preceding)
			add("./src/handler.js", tc.handler)
			for _, closer := range []interface{ Close() error }{w, z, f} {
				if err := closer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if got := ArchiveEntrypointUsesOrvaSDK(archive, tc.entrypoint); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
