package sandbox

import (
	"strings"
	"testing"
)

// containsArg returns true if any element in args equals s — useful for
// asserting flag presence regardless of position.
func containsArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// TestBuildArgs_NoEgressByDefault asserts that the default (and explicit
// "none") network mode does NOT add --user_net. The sandbox stays in
// nsjail's default loopback-only net namespace.
func TestBuildArgs_NoEgressByDefault(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{"unset", ""},
		{"explicit-none", "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ExecConfig{
				Language:    Python,
				CodeDir:     "/tmp/code",
				NetworkMode: tc.mode,
			}
			args := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
			if containsArg(args, "--user_net") {
				t.Fatalf("expected no --user_net with NetworkMode=%q, got: %s",
					tc.mode, strings.Join(args, " "))
			}
		})
	}
}

// TestBuildArgs_EgressAddsUsePasta asserts that NetworkMode == "egress"
// adds the --user_net flag, which lights up nsjail's userspace TCP/UDP
// stack and lets the sandbox reach external APIs.
func TestBuildArgs_EgressAddsUsePasta(t *testing.T) {
	cfg := ExecConfig{
		Language:    Node,
		CodeDir:     "/tmp/code",
		NetworkMode: "egress",
	}
	args := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js")
	if !containsArg(args, "--user_net") {
		t.Fatalf("expected --user_net in args for egress mode, got: %s",
			strings.Join(args, " "))
	}
}

// TestBuildArgs_AlwaysHasOnceMode is a smoke check that the new branch
// didn't accidentally drop the -Mo flag (one-shot mode is mandatory).
func TestBuildArgs_AlwaysHasOnceMode(t *testing.T) {
	cfg := ExecConfig{
		Language: Python,
		CodeDir:  "/tmp/code",
	}
	args := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
	if !containsArg(args, "-Mo") {
		t.Fatalf("expected -Mo in args, got: %s", strings.Join(args, " "))
	}
}

func TestApplyExecDefaultsSetsProcessLimit(t *testing.T) {
	cfg := applyExecDefaults(ExecConfig{})
	if cfg.MaxPids != 32 {
		t.Fatalf("MaxPids default: want 32, got %d", cfg.MaxPids)
	}
	if cfg.MemoryMB != 64 {
		t.Fatalf("MemoryMB default: want 64, got %d", cfg.MemoryMB)
	}
	if cfg.Timeout <= 0 {
		t.Fatalf("Timeout default must be positive, got %s", cfg.Timeout)
	}
}

func TestApplyExecDefaultsPreservesExplicitProcessLimit(t *testing.T) {
	cfg := applyExecDefaults(ExecConfig{MaxPids: 7})
	if cfg.MaxPids != 7 {
		t.Fatalf("explicit MaxPids: want 7, got %d", cfg.MaxPids)
	}
}

func TestARM64SeccompFiltersUnsupportedLegacySyscalls(t *testing.T) {
	for name := range arm64UnsupportedSyscalls {
		if syscallSupportedOnArch(name, "arm64") {
			t.Errorf("%s must be filtered on arm64", name)
		}
		if !syscallSupportedOnArch(name, "amd64") {
			t.Errorf("%s should remain available on amd64", name)
		}
	}
	if !syscallSupportedOnArch("openat", "arm64") {
		t.Fatal("openat must remain available on arm64")
	}
}
