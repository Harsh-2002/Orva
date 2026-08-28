package main

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasRequiredNsjailCaps(t *testing.T) {
	complete := "/usr/local/bin/nsjail cap_net_admin,cap_net_bind_service,cap_setgid,cap_setuid,cap_sys_admin=eip\n"
	if !hasRequiredNsjailCaps(complete) {
		t.Fatal("complete nsjail capability set was rejected")
	}

	for _, incomplete := range []string{
		"",
		"/usr/local/bin/nsjail cap_sys_admin=eip\n",
		"/usr/local/bin/nsjail cap_net_admin,cap_net_bind_service,cap_setgid,cap_setuid,cap_sys_admin=ep\n",
	} {
		if hasRequiredNsjailCaps(incomplete) {
			t.Fatalf("incomplete nsjail capability set was accepted: %q", incomplete)
		}
	}
}

func TestRuntimeCompleteRequiresExecutable(t *testing.T) {
	root := t.TempDir()
	entrypoint := filepath.Join(root, "usr/local/bin/node")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("node"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !runtimeComplete(entrypoint) {
		t.Fatal("executable runtime was rejected")
	}
	if err := os.Chmod(entrypoint, 0o644); err != nil {
		t.Fatal(err)
	}
	if runtimeComplete(entrypoint) {
		t.Fatal("non-executable runtime entrypoint was accepted")
	}
}

// TestEmbeddedNodeSDKEntrypointsExist pins package.json's main/types against
// the files actually embedded. A `main` that misses does not fail loudly —
// Node falls back to index.js under DEP0128 — so nothing else would notice.
func TestEmbeddedNodeSDKEntrypointsExist(t *testing.T) {
	raw, err := embeddedAdapters.ReadFile("adapters/node/package.json")
	if err != nil {
		t.Fatalf("read embedded node package.json: %v", err)
	}
	var pkg struct {
		Main  string `json:"main"`
		Types string `json:"types"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("parse embedded node package.json: %v", err)
	}
	for field, rel := range map[string]string{"main": pkg.Main, "types": pkg.Types} {
		if rel == "" {
			t.Errorf("package.json %q is empty", field)
			continue
		}
		name := strings.TrimPrefix(rel, "./")
		if _, err := embeddedAdapters.ReadFile("adapters/node/" + name); err != nil {
			t.Errorf("package.json %q = %q but adapters/node/%s is not embedded: %v", field, rel, name, err)
		}
	}
}

// TestDockerfileShipsTheSDKUnderItsRealNames keeps the image's rootfs stages
// in step with installSDK. The image once renamed orva.js to index.js and
// hand-wrote a package.json that had gone five SDK versions stale.
func TestDockerfileShipsTheSDKUnderItsRealNames(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	for rt, files := range sdkFiles {
		copied := map[string]string{} // in-image path -> source basename
		for _, line := range lines {
			f := strings.Fields(line)
			if len(f) != 3 || f[0] != "COPY" {
				continue
			}
			if !strings.HasPrefix(f[1], "backend/runtimes/"+rt+"/") {
				continue
			}
			copied[f[2]] = path.Base(f[1])
		}
		for _, name := range files {
			dst := "/" + sdkDestDir[rt] + "/" + name
			src, ok := copied[dst]
			if !ok {
				t.Errorf("%s: Dockerfile copies nothing to %s", rt, dst)
				continue
			}
			if src != name {
				t.Errorf("%s: %s is copied from %s — renaming breaks package.json main/types", rt, dst, src)
			}
		}
	}
}
