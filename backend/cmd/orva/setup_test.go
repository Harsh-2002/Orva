package main

import (
	"os"
	"path/filepath"
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
