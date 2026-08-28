package commands

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/creativeprojects/go-selfupdate/update"
	"github.com/spf13/cobra"
)

// orvaRepo is the GitHub repo to query for releases. Overridable for tests.
var orvaRepo = "Harsh-2002/Orva"

// githubAPIBase is the GitHub REST API base. A var so tests can point it at an
// httptest server (mirrors the orvaRepo override pattern).
var githubAPIBase = "https://api.github.com"

// ServerBuild is set by the server binary's main(); the slim CLI leaves it
// false. The server registers `upgrade` too, so without this the command
// installs the slim CLI over the server and `orva serve` ceases to exist.
var ServerBuild bool

// upgradeAssetName is the EXACT release-asset name for the running platform:
// "orva-cli-linux-amd64" / "orva-cli-windows-amd64.exe" for the slim CLI, and
// the separate "orva-linux-<arch>" server asset for a server build.
//
// This is stronger than the old "^orva-cli-<os>-<arch>" regex filter: matching
// the full os-arch token exactly means a wrong-OS asset (e.g. a darwin Mach-O
// binary on a linux host) can never be selected. Releases upload the build
// matrix in parallel, so asset order is non-deterministic; an exact-name match
// removes the "exec format error after a successful upgrade" class entirely.
func upgradeAssetName(goos, goarch string) string {
	if ServerBuild {
		return fmt.Sprintf("orva-%s-%s", goos, goarch)
	}
	name := fmt.Sprintf("orva-cli-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// checkUpgradePlatform rejects a server-build upgrade off linux. Releases
// publish the server binary for linux only, and the one thing this must never
// do is quietly settle for the CLI asset.
func checkUpgradePlatform(server bool, goos string) error {
	if server && goos != "linux" {
		return fmt.Errorf("no server release asset for %s: the Orva server is published for linux only", goos)
	}
	return nil
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade orva to the latest GitHub release",
	Long: `Download the latest orva release from GitHub, verify its SHA-256
against checksums.txt, and atomically replace the running binary. A CLI build
fetches the CLI asset and a server build fetches the server asset; neither is
ever installed over the other.

On a server this replaces the binary only. Restart the service afterwards, and
re-run install.sh instead when the runtime adapters, rootfs or the service unit
also need refreshing.

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
	if err := checkUpgradePlatform(ServerBuild, runtime.GOOS); err != nil {
		return err
	}

	ctx := cmd.Context()
	latest, found, err := detectLatest(ctx, orvaRepo)
	if err != nil {
		return fmt.Errorf("check latest release: %w", err)
	}
	if !found {
		return fmt.Errorf("no matching release asset %q for %s/%s in %s",
			upgradeAssetName(runtime.GOOS, runtime.GOARCH), runtime.GOOS, runtime.GOARCH, orvaRepo)
	}

	current := strings.TrimPrefix(Version, "v")
	latestStr := displayVersion(latest.Tag)
	versionNewer := !lessOrEqual(latest.Tag, current)

	// Resolve the running binary up front: we need it both to hash (staleness
	// check) and to replace. Follow symlinks so a bare-metal install
	// (/usr/local/bin/orva -> /opt/orva/bin/orva) replaces the real target,
	// not the symlink.
	exe, err := executablePath()
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
	if err := downloadVerifyApply(ctx, latest, exe); err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}

	fmt.Fprintf(out, "Upgraded to orva %s\n", latestStr)
	if ServerBuild {
		fmt.Fprintf(out, "Restart to apply: systemctl restart orva (OpenRC: rc-service orva restart)\n")
		fmt.Fprintf(out, "This replaced the binary only; re-run install.sh to also refresh adapters, rootfs and the unit.\n")
	}
	return nil
}

// releaseInfo is the resolved latest release for the running platform. It
// replaces go-selfupdate's *Release (ChecksumsURL was its ValidationAssetURL).
type releaseInfo struct {
	Tag          string // raw tag, e.g. "v2026.08.05"
	AssetName    string // "orva-cli-linux-amd64"
	AssetURL     string // browser_download_url of the platform asset
	ChecksumsURL string // browser_download_url of checksums.txt
}

// ghRelease / ghAsset decode the subset of the GitHub "releases/latest" payload
// we need.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// detectLatest resolves the latest release for repo (owner/name) and the asset
// matching this platform. Mirrors scripts/install.sh's resolution: it hits the
// GitHub REST API, honors GITHUB_TOKEN/GH_TOKEN for the authenticated rate
// limit, and selects the platform asset by EXACT name. Returns (info, found,
// err); found is false when the release has no asset for this os/arch.
func detectLatest(ctx context.Context, repo string) (*releaseInfo, bool, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, false, fmt.Errorf("invalid repo %q (want owner/name)", repo)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(githubAPIBase, "/"), owner, name)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "orva-cli")
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, false, fmt.Errorf("github API rate-limited (status %d); set GITHUB_TOKEN to raise the limit", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("github API returned status %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rel); err != nil {
		return nil, false, fmt.Errorf("decode release JSON: %w", err)
	}

	wantAsset := upgradeAssetName(runtime.GOOS, runtime.GOARCH)
	info := &releaseInfo{Tag: rel.TagName}
	for _, a := range rel.Assets {
		switch a.Name {
		case wantAsset:
			info.AssetName = a.Name
			info.AssetURL = a.URL
		case "checksums.txt":
			info.ChecksumsURL = a.URL
		}
	}
	if info.AssetName == "" || info.AssetURL == "" {
		return nil, false, nil
	}
	return info, true, nil
}

func githubToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

// semverRe extracts a dotted three-part version from a tag (e.g. "v2026.08.05"
// -> "2026.08.05"), matching go-selfupdate's tag parsing.
var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// parseSemver pulls a semver out of a tag string. ok is false when none is
// present (e.g. a "dev" build).
func parseSemver(tag string) (*semver.Version, bool) {
	m := semverRe.FindString(tag)
	if m == "" {
		return nil, false
	}
	v, err := semver.NewVersion(m)
	if err != nil {
		return nil, false
	}
	return v, true
}

// lessOrEqual reports whether the latest tag is <= the current version — i.e.
// NOT a strictly-newer release. Mirrors go-selfupdate's Release.LessOrEqual but
// never panics: an unparseable current (a "dev" build) is treated as older than
// any real release (so an upgrade is offered), and an unparseable latest is
// treated as not-newer.
func lessOrEqual(latestTag, current string) bool {
	lv, ok := parseSemver(latestTag)
	if !ok {
		return true // can't parse latest → don't claim it's newer
	}
	cv, ok := parseSemver(current)
	if !ok {
		return false // current is "dev"/unparseable → latest is newer, offer it
	}
	return !lv.GreaterThan(cv)
}

// displayVersion renders a tag for user-facing messages, normalized to its
// semver form when possible (matches the old latest.Version() output), else the
// raw tag with a leading "v" stripped.
func displayVersion(tag string) string {
	if v, ok := parseSemver(tag); ok {
		return v.String()
	}
	return strings.TrimPrefix(tag, "v")
}

// executablePath returns the real path of the running binary, resolving
// symlinks so a bare-metal install (/usr/local/bin/orva -> /opt/orva/bin/orva)
// upgrades the real target rather than clobbering the symlink with a file.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// downloadVerifyApply fetches the platform asset, verifies its SHA-256 against
// the published checksums.txt BEFORE any mutation, then atomically replaces the
// running binary via update.Apply (which handles the Unix atomic swap and the
// Windows rename-and-hide). The verify-before-swap ordering guarantees a
// tampered or truncated download can never reach the target path.
func downloadVerifyApply(ctx context.Context, rel *releaseInfo, exe string) error {
	data, err := fetchAsset(ctx, rel.AssetURL)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}

	sum := sha256.Sum256(data)
	want, err := remoteAssetSHA(ctx, rel.ChecksumsURL, rel.AssetName)
	if err != nil {
		return fmt.Errorf("fetch checksums: %w", err)
	}
	if want == "" {
		return fmt.Errorf("no checksum published for %s", rel.AssetName)
	}
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return fmt.Errorf("checksum mismatch for %s: refusing to install", rel.AssetName)
	}

	// Checksum is re-verified inside Apply against the exact bytes it writes.
	return update.Apply(bytes.NewReader(data), update.Options{
		TargetPath: exe,
		Checksum:   sum[:],
	})
}

// fetchAsset downloads a release asset. It uses the default client so it follows
// GitHub's redirect to the pre-signed asset host (which strips cross-host auth);
// no Authorization header is attached to the asset GET.
func fetchAsset(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "orva-cli")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	// Cap the read defensively; CLI binaries are tens of MB.
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

// upgradeAction is the pure decision: do we install? A genuinely newer tag, a
// same-tag rebuild (checksum changed), or --force all trigger an install.
func upgradeAction(versionNewer, rebuilt, force bool) bool {
	return force || versionNewer || rebuilt
}

// remoteBuildDiffers reports whether the latest release's published asset for
// this platform has a different checksum than the running binary. The second
// return (known) is false when we can't determine it — no checksums asset, the
// asset isn't listed, or any network/parse error — so callers fall back to the
// version-only decision rather than guessing. Best-effort: never fatal.
func remoteBuildDiffers(ctx context.Context, latest *releaseInfo, exePath string) (differs, known bool) {
	if latest == nil || latest.ChecksumsURL == "" || latest.AssetName == "" {
		return false, false
	}
	localSHA, err := fileSHA256(exePath)
	if err != nil {
		return false, false
	}
	remoteSHA, err := remoteAssetSHA(ctx, latest.ChecksumsURL, latest.AssetName)
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
	req.Header.Set("User-Agent", "orva-cli")
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
// the running .exe is locked, but update.Apply handles that case via a
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
	// allow the upgrade attempt — update.Apply's swap path handles it.
	return nil
}
