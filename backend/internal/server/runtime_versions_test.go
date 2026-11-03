package server

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// The function runtime versions are stated in the rootfs recipes, the CI parity
// pin, the docs and the dashboard as well as in Go. sandbox.NodeMajor and
// sandbox.PythonMinor are the source of truth; this fails when a file that states
// a runtime version disagrees, which is what a half-finished bump looks like.

// Node and Python version mentions in prose, tables, badges and image tags.
var (
	nodeMention   = regexp.MustCompile(`(?i)\bnode(?:\.js)?[ -](\d{2})\b`)
	pythonMention = regexp.MustCompile(`(?i)\bpython[ -](3\.\d{1,2})\b`)
	nodeImage     = regexp.MustCompile(`\bnode:(\d{2})-(?:slim|alpine)\b`)
	pythonImage   = regexp.MustCompile(`\bpython:(3\.\d{1,2})-slim\b`)
)

// Files whose version statements describe the runtimes functions run on. Records
// of past measurements (CHANGELOG, CAPACITY, Kata/gVisor runs, TESTING.md) and the
// toolchain pages (CONTRACT, CLAUDE) are left out on purpose.
var liveRuntimeFiles = []string{
	"README.md", "PRODUCT.md", "DESIGN.md",
	"docs/reference.md", "docs/RUNTIMES.md", "docs/ARCHITECTURE.md", "docs/OPERATIONS.md",
	"scripts/build-rootfs.sh",
	"frontend/src/utils/runtime.js", "frontend/src/utils/aiPrompts.js",
	"frontend/src/views/Editor.vue", "frontend/src/views/Docs.vue",
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestRuntimeVersionsAgree(t *testing.T) {
	node, python := sandbox.NodeMajor, sandbox.PythonMinor
	var bad []string
	checked := 0
	check := func(file string, text string, re *regexp.Regexp, want, what string) {
		for i, line := range strings.Split(text, "\n") {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				checked++
				if m[1] != want {
					bad = append(bad, fmt.Sprintf("%s:%d says %s %s, want %s: %s", file, i+1, what, m[1], want, strings.TrimSpace(line)))
				}
			}
		}
	}

	for _, f := range liveRuntimeFiles {
		text := repoFile(t, f)
		check(f, text, nodeMention, node, "Node")
		check(f, text, pythonMention, python, "Python")
		check(f, text, nodeImage, node, "node image")
		check(f, text, pythonImage, python, "python image")
	}

	// A scan that matches nothing would pass vacuously.
	if checked < 25 {
		t.Fatalf("only %d version mentions were inspected; the patterns or the file list no longer match the repo", checked)
	}

	// The Dockerfile has a third Node stage, the dashboard build, which follows
	// the LTS line independently; only the runtime stages must match.
	docker := repoFile(t, "Dockerfile")
	runtimeStages := regexp.MustCompile(`(?m)^FROM (node:\d{2}-slim|python:3\.\d+-slim) AS (node-strip|rootfs-node|rootfs-python)\b`)
	stages := runtimeStages.FindAllStringSubmatch(docker, -1)
	if len(stages) != 3 {
		t.Fatalf("expected the node-strip, rootfs-node and rootfs-python stages in the Dockerfile, found %d", len(stages))
	}
	for _, s := range stages {
		want := "node:" + node + "-slim"
		if s[2] == "rootfs-python" {
			want = "python:" + python + "-slim"
		}
		if s[1] != want {
			bad = append(bad, fmt.Sprintf("Dockerfile stage %s uses %s, want %s", s[2], s[1], want))
		}
	}
	if !strings.Contains(docker, "/usr/local/lib/python3.*") {
		bad = append(bad, "Dockerfile prunes a hard-coded python3.NN library path; use the python3.* glob so a bump cannot leave it stale")
	}

	// CI runs the stdlib-only E2E harness on the same Python the runtime carries.
	ci := repoFile(t, ".github/workflows/ci.yml")
	if m := regexp.MustCompile(`python-version:\s*"([\d.]+)"`).FindStringSubmatch(ci); m == nil || m[1] != python {
		got := "none"
		if m != nil {
			got = m[1]
		}
		bad = append(bad, fmt.Sprintf(".github/workflows/ci.yml python-version is %s, want %s", got, python))
	}

	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("runtime versions disagree with sandbox.NodeMajor=%s / sandbox.PythonMinor=%s:\n  %s",
			node, python, strings.Join(bad, "\n  "))
	}
}
