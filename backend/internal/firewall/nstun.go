package firewall

// Publication of compiled egress policies as nsjail --config files.
//
// Each generation is written to its own immutable file. That immutability is
// the correctness guarantee for warm workers: a worker captures a concrete
// path at spawn, and because that file is never rewritten, a policy change
// mid-spawn cannot alter what the in-flight worker is running under. Pools are
// retired separately so the *next* spawn picks up the new generation.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// generationsKept bounds stale files at daemon startup, before workers can
// spawn. Runtime publication cannot safely delete an older generation: a
// worker may have captured its path but not yet opened it in nsjail.
const generationsKept = 5

// PolicyDir is where compiled policy generations live, alongside the generated
// resolv.conf and hosts file.
func PolicyDir(dataDir string) string {
	return filepath.Join(dataDir, "firewall", "policy")
}

func policyPath(dataDir, gen string) string {
	return filepath.Join(PolicyDir(dataDir), "egress-"+gen+".cfg")
}

// currentLinkPath is an operator convenience only — never handed to nsjail.
// Workers always get a concrete generation path so the file backing a running
// worker cannot change underneath it.
func currentLinkPath(dataDir string) string {
	return filepath.Join(PolicyDir(dataDir), "current")
}

// publish writes the rendered policy for gen and retargets the `current`
// symlink. Writing is temp+rename so a spawning worker never observes a
// partially written config. Mode is 0600: the policy is not secret, but it
// describes the operator's security posture and nothing needs to read it but
// orvad and nsjail.
//
// Re-publishing an existing generation is a no-op on the file (same content by
// construction, since the generation IS the content hash) but still refreshes
// the symlink.
func publish(dataDir, gen string, rendered []byte) (string, error) {
	dir := PolicyDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create policy dir: %w", err)
	}

	target := policyPath(dataDir, gen)
	if _, err := os.Stat(target); err != nil {
		tmp := target + ".tmp"
		if err := os.WriteFile(tmp, rendered, 0o600); err != nil {
			return "", fmt.Errorf("write policy: %w", err)
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("publish policy: %w", err)
		}
	}

	// Symlink retarget must also be atomic, or an operator inspecting
	// `current` could momentarily find nothing there.
	link := currentLinkPath(dataDir)
	tmpLink := link + ".tmp"
	_ = os.Remove(tmpLink)
	if err := os.Symlink(filepath.Base(target), tmpLink); err == nil {
		_ = os.Rename(tmpLink, link)
	}

	return target, nil
}

// gcGenerations runs only before the first policy is published at startup.
// Every file from the prior process is safe to discard; active process
// generations remain immutable and available for delayed worker spawns.
func gcGenerations(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	current, _ := os.Readlink(filepath.Join(dir, "current"))
	current = filepath.Base(current)

	type gen struct {
		name string
		mod  int64
	}
	var gens []gen
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == current ||
			!strings.HasPrefix(n, "egress-") || !strings.HasSuffix(n, ".cfg") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		gens = append(gens, gen{name: n, mod: info.ModTime().UnixNano()})
	}

	budget := generationsKept
	if current != "." && current != "" {
		budget--
	}
	if len(gens) <= budget {
		return
	}
	sort.Slice(gens, func(i, j int) bool {
		if gens[i].mod != gens[j].mod {
			return gens[i].mod > gens[j].mod
		}
		return gens[i].name < gens[j].name // deterministic tiebreak
	})
	for _, g := range gens[budget:] {
		_ = os.Remove(filepath.Join(dir, g.name))
	}
}
