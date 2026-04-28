package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Harsh-2002/Orva/internal/config"
	"github.com/spf13/cobra"
)

// Embedded adapter sources so `orva setup` works on a freshly-installed
// host without needing the source tree. Populated via a top-level generate
// step (see Makefile `adapters-embed`) which copies runtimes/*/adapter.*
// into cmd/orva/adapters/ at build time.
//
//go:embed all:adapters
var embeddedAdapters embed.FS

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Prepare the host so `orva serve` can run sandboxed functions",
	Long: `Install nsjail (if missing), grant it the capabilities it needs,
download or build the language rootfs directories, and create the data dir
layout Orva expects. Idempotent — safe to run repeatedly.

On hosts where nsjail is missing, this command uses docker to build the
rootfs trees via scripts/build-rootfs.sh.`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().String("data-dir", "", "override data directory (default: ~/.orva)")
	setupCmd.Flags().Bool("skip-rootfs", false, "do not build/fetch language rootfs (you'll need to populate it manually)")
	setupCmd.Flags().Bool("skip-nsjail", false, "do not install nsjail or run setcap")
	setupCmd.Flags().String("rootfs-url", "", "base URL for downloadable rootfs tarballs (e.g. https://github.com/Harsh-2002/Orva/releases/latest/download)")
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	cfg := config.Defaults()
	if v, _ := cmd.Flags().GetString("data-dir"); v != "" {
		cfg.Data.Dir = v
		cfg.Sandbox.RootfsDir = filepath.Join(v, "rootfs")
		cfg.Database.Path = filepath.Join(v, "orva.db")
	}
	skipRootfs, _ := cmd.Flags().GetBool("skip-rootfs")
	skipNsjail, _ := cmd.Flags().GetBool("skip-nsjail")
	rootfsURL, _ := cmd.Flags().GetString("rootfs-url")

	fmt.Println("Orva host setup")
	fmt.Println("  data dir:", cfg.Data.Dir)
	fmt.Println("  rootfs:  ", cfg.Sandbox.RootfsDir)
	fmt.Println()

	// Data dir layout.
	for _, d := range []string{
		cfg.Data.Dir,
		filepath.Join(cfg.Data.Dir, "functions"),
		cfg.Sandbox.RootfsDir,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	// nsjail.
	if !skipNsjail {
		if err := ensureNsjail(cfg.Sandbox.NsjailBin); err != nil {
			return err
		}
	} else {
		fmt.Println("[skip] nsjail install/setcap (--skip-nsjail)")
	}

	// Rootfs.
	if !skipRootfs {
		for _, rt := range []string{"node22", "node24", "python313", "python314"} {
			target := filepath.Join(cfg.Sandbox.RootfsDir, rt)
			if err := ensureRootfs(target, rt, rootfsURL); err != nil {
				return err
			}
			if err := installAdapter(target, rt); err != nil {
				return err
			}
		}
	} else {
		fmt.Println("[skip] rootfs build (--skip-rootfs)")
	}

	// First-run marker.
	marker := filepath.Join(cfg.Data.Dir, ".setup-complete")
	if err := os.WriteFile(marker, []byte("ok\n"), 0o644); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	fmt.Println()
	fmt.Println("Setup complete. Next: orva serve")
	return nil
}

func ensureNsjail(target string) error {
	if _, err := os.Stat(target); err == nil {
		fmt.Printf("[ok] nsjail present at %s\n", target)
	} else {
		// Try package manager first, then fall back to a helpful error.
		if path, err := exec.LookPath("nsjail"); err == nil {
			fmt.Printf("[ok] nsjail found on PATH at %s\n", path)
			target = path
		} else {
			fmt.Fprintln(os.Stderr, "nsjail is not installed.")
			fmt.Fprintln(os.Stderr, "Install from https://github.com/google/nsjail (or use the Orva docker image),")
			fmt.Fprintln(os.Stderr, "then re-run `orva setup`.")
			return fmt.Errorf("nsjail missing")
		}
	}

	// setcap so we can run nsjail without sudo.
	caps := "cap_sys_admin,cap_setuid,cap_setgid,cap_net_admin,cap_net_bind_service=eip"
	if hasCaps(target) {
		fmt.Println("[ok] nsjail already has required capabilities")
		return nil
	}
	fmt.Printf("[..] applying setcap %s %s (this may prompt for sudo)\n", caps, target)
	if err := runWithPossibleSudo("setcap", caps, target); err != nil {
		return fmt.Errorf("setcap: %w", err)
	}
	fmt.Println("[ok] nsjail setcap done")
	return nil
}

func hasCaps(path string) bool {
	out, err := exec.Command("getcap", path).CombinedOutput()
	if err != nil {
		return false
	}
	// Any non-empty getcap output means capabilities are present.
	return len(out) > 0
}

func runWithPossibleSudo(cmd string, args ...string) error {
	if os.Geteuid() == 0 {
		c := exec.Command(cmd, args...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c.Run()
	}
	full := append([]string{cmd}, args...)
	c := exec.Command("sudo", full...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func ensureRootfs(target, runtime, rootfsURL string) error {
	if _, err := os.Stat(filepath.Join(target, "usr/local/bin")); err == nil {
		fmt.Printf("[ok] %s rootfs already at %s\n", runtime, target)
		return nil
	}

	// Preferred path on bare VMs: download a pre-built tarball. This avoids
	// the Docker-daemon requirement of the build-rootfs.sh script.
	if rootfsURL != "" {
		if err := fetchRootfsTarball(target, runtime, rootfsURL); err == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "[warn] tarball fetch failed (%v); falling back to local build\n", err)
		}
	}

	// Dev fallback — uses Docker to extract an official slim image.
	script := locateBuildRootfsScript()
	if script == "" {
		return fmt.Errorf("rootfs for %s missing and no --rootfs-url or scripts/build-rootfs.sh available", runtime)
	}
	fmt.Printf("[..] building %s rootfs at %s (uses docker to extract an official image)\n", runtime, target)
	c := exec.Command("bash", script, target, runtime)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// fetchRootfsTarball downloads <base>/rootfs-<runtime>-<arch>.tar.zst and
// extracts it into target. Used by the piped-curl installer on bare VMs.
func fetchRootfsTarball(target, runtime, base string) error {
	arch := goArchToRelease()
	if arch == "" {
		return fmt.Errorf("unsupported arch for rootfs download")
	}
	url := fmt.Sprintf("%s/rootfs-%s-%s.tar.zst", base, runtime, arch)
	fmt.Printf("[..] fetching %s\n", url)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	// curl → zstd -d → tar -x. Keeping it shell-pipeline-based lets us
	// avoid pulling in tar+zstd Go deps; every supported distro ships
	// `tar` in base, and install.sh pulls `zstd` into the install.
	c := exec.Command("sh", "-c",
		"set -o pipefail; curl -fsSL '"+url+"' | zstd -d | tar -xf - -C '"+target+"'")
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// goArchToRelease maps runtime.GOARCH → the release-asset architecture
// label. amd64 and arm64 are the two we publish.
func goArchToRelease() string {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return runtime.GOARCH
	default:
		return ""
	}
}

func installAdapter(rootfs, runtime string) error {
	name := ""
	switch runtime {
	case "node22", "node24":
		name = "adapter.js"
	case "python313", "python314":
		name = "adapter.py"
	default:
		return fmt.Errorf("unknown runtime: %s", runtime)
	}

	// Prefer the embedded copy so `orva setup` works on a fresh VM with no
	// source checkout; fall back to disk for development iteration where
	// you may be editing the runtime adapters live.
	data, err := embeddedAdapters.ReadFile("adapters/" + runtime + "/" + name)
	if err != nil {
		// Dev-mode fallback: look in the repo's runtimes/ tree.
		candidates := []string{"runtimes/" + runtime + "/" + name}
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(cwd, "runtimes", runtime, name))
		}
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "runtimes", runtime, name))
		}
		for _, c := range candidates {
			if b, err2 := os.ReadFile(c); err2 == nil {
				data = b
				err = nil
				break
			}
		}
		if data == nil {
			return fmt.Errorf("no embedded or on-disk adapter for %s (%s): %w", runtime, name, err)
		}
	}

	dstDir := filepath.Join(rootfs, "opt/orva")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dstDir, err)
	}
	dst := filepath.Join(dstDir, name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write adapter %s: %w", dst, err)
	}
	fmt.Printf("[ok] installed %s adapter at %s\n", runtime, dst)
	return nil
}

func locateBuildRootfsScript() string {
	candidates := []string{
		"scripts/build-rootfs.sh",
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "scripts/build-rootfs.sh"))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "scripts/build-rootfs.sh"))
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "../share/orva/build-rootfs.sh"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
