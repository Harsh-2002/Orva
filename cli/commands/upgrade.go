package commands

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

// orvaRepo is the GitHub repo to query for releases. Overridable for tests.
var orvaRepo = "Harsh-2002/Orva"

// upgradeAssetFilter pins go-selfupdate's asset match to the exact
// orva-cli-<os>-<arch> release artifact for the running platform.
//
// A loose "^orva-cli-" filter let go-selfupdate fall back to matching on
// arch alone, so on a linux/amd64 host it could pick orva-cli-darwin-amd64
// (a Mach-O binary) whenever that asset happened to sort first — releases
// upload the build matrix in parallel, so asset order is non-deterministic.
// The result was an intermittent "exec format error" after a "successful"
// upgrade. Anchoring to the full os-arch token removes the ambiguity; the
// trailing .exe on Windows assets is still matched by the prefix.
func upgradeAssetFilter(goos, goarch string) string {
	return fmt.Sprintf("^orva-cli-%s-%s", goos, goarch)
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade orva to the latest GitHub release",
	Long: `Download the latest orva CLI release from GitHub, verify its SHA-256
against checksums.txt, and atomically replace the running binary.

If the install location is not writable by the current user, the command
exits non-zero with a hint to re-run under sudo. It never silently elevates.

Set ORVA_UPGRADE_REPO=<owner>/<name> to point at a fork.`,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().Bool("check", false, "check for a newer release without downloading")
	upgradeCmd.Flags().Bool("force", false, "reinstall the latest release even if it matches the current version")
}

func runUpgrade(cmd *cobra.Command, _ []string) error {
	check, _ := cmd.Flags().GetBool("check")
	force, _ := cmd.Flags().GetBool("force")
	out := cmd.OutOrStdout()

	if env := os.Getenv("ORVA_UPGRADE_REPO"); env != "" {
		orvaRepo = env
	}

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("init github source: %w", err)
	}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
		Validator: &selfupdate.ChecksumValidator{
			UniqueFilename: "checksums.txt",
		},
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Filters: []string{upgradeAssetFilter(runtime.GOOS, runtime.GOARCH)},
	})
	if err != nil {
		return fmt.Errorf("init updater: %w", err)
	}

	ctx := cmd.Context()
	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(orvaRepo))
	if err != nil {
		return fmt.Errorf("check latest release: %w", err)
	}
	if !found {
		return fmt.Errorf("no matching release asset for %s/%s in %s", runtime.GOOS, runtime.GOARCH, orvaRepo)
	}

	current := strings.TrimPrefix(Version, "v")
	latestStr := latest.Version()
	versionNewer := !latest.LessOrEqual(current)

	// Resolve the running binary up front: we need it both to hash (staleness
	// check) and to replace.
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}

	// Date-based tags (vYYYY.MM.DD) get re-cut the same day, so an equal tag can
	// still point at a different published binary. When the tag isn't strictly
	// newer, compare the running binary's checksum against the published one for
	// this platform; a mismatch means a fresh build under the same tag.
	stale, known := false, false
	if !versionNewer {
		stale, known = remoteBuildDiffers(ctx, latest, exe)
	}
	rebuilt := known && stale // same tag, different published build
	do := upgradeAction(versionNewer, rebuilt, force)

	if check {
		switch {
		case versionNewer:
			fmt.Fprintf(out, "orva %s is available (current: %s)\n", latestStr, Version)
		case rebuilt:
			fmt.Fprintf(out, "orva %s has a newer build available (same version, rebuilt) — run `orva upgrade`\n", Version)
		default:
			fmt.Fprintf(out, "orva %s is up to date (latest: %s)\n", Version, latestStr)
		}
		return nil
	}

	if !do {
		fmt.Fprintf(out, "orva %s is already the latest.\n", Version)
		return nil
	}

	if err := assertWritable(exe); err != nil {
		return fmt.Errorf("%w\nhint: re-run with `sudo orva upgrade` if the binary lives in a system path like /usr/local/bin", err)
	}

	if rebuilt && !versionNewer {
		fmt.Fprintf(out, "Reinstalling orva %s (new build) ...\n", latestStr)
	} else {
		fmt.Fprintf(out, "Upgrading orva %s -> %s ...\n", Version, latestStr)
	}
	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}

	fmt.Fprintf(out, "Upgraded to orva %s\n", latestStr)
	return nil
}

// upgradeAction is the pure decision: do we install? A genuinely newer tag, a
// same-tag rebuild (checksum changed), or --force all trigger an install.
func upgradeAction(versionNewer, rebuilt, force bool) bool {
	return force || versionNewer || rebuilt
}

// remoteBuildDiffers reports whether the latest release's published asset for
// this platform has a different checksum than the running binary. The second
// return (known) is false when we can't determine it — no validation asset, the
// asset isn't listed, or any network/parse error — so callers fall back to the
// version-only decision rather than guessing. Best-effort: never fatal.
func remoteBuildDiffers(ctx context.Context, latest *selfupdate.Release, exePath string) (differs, known bool) {
	if latest == nil || latest.ValidationAssetURL == "" || latest.AssetName == "" {
		return false, false
	}
	localSHA, err := fileSHA256(exePath)
	if err != nil {
		return false, false
	}
	remoteSHA, err := remoteAssetSHA(ctx, latest.ValidationAssetURL, latest.AssetName)
	if err != nil || remoteSHA == "" {
		return false, false
	}
	return !strings.EqualFold(localSHA, remoteSHA), true
}

// fileSHA256 returns the hex-encoded SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// remoteAssetSHA fetches a sha256sum-format checksums file and returns the hash
// recorded for assetName (empty string if not present). Lines look like
// "<hex>␠␠<name>"; the name may carry a leading "*" (binary-mode marker).
func remoteAssetSHA(ctx context.Context, checksumsURL, assetName string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch checksums: status %d", resp.StatusCode)
	}
	return parseChecksums(resp.Body, assetName), nil
}

// parseChecksums scans a sha256sum-format stream for assetName's hash.
func parseChecksums(r io.Reader, assetName string) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName {
			return fields[0]
		}
	}
	return ""
}

// assertWritable returns an error if the current user cannot replace the
// file at path. On Unix-likes this is an open-for-write probe; on Windows
// the running .exe is locked, but go-selfupdate handles that case via a
// rename-and-hide dance, so we accept any non-permission error.
func assertWritable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		_ = f.Close()
		return nil
	}
	var perr *fs.PathError
	if errors.As(err, &perr) {
		if errors.Is(perr.Err, fs.ErrPermission) {
			return fmt.Errorf("install location not writable: %s", path)
		}
	}
	// Windows running-exe lock manifests as ERROR_SHARING_VIOLATION (32);
	// allow the upgrade attempt — go-selfupdate's swap path handles it.
	return nil
}
