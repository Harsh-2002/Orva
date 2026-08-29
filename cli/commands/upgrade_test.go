package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestUpgradeAction covers the install decision truth table.
func TestUpgradeAction(t *testing.T) {
	cases := []struct {
		versionNewer, rebuilt, force, want bool
	}{
		{false, false, false, false}, // up to date
		{true, false, false, true},   // newer tag
		{false, true, false, true},   // same tag, rebuilt
		{false, false, true, true},   // forced
		{true, true, true, true},
	}
	for _, c := range cases {
		if got := upgradeAction(c.versionNewer, c.rebuilt, c.force); got != c.want {
			t.Errorf("upgradeAction(newer=%v,rebuilt=%v,force=%v) = %v, want %v",
				c.versionNewer, c.rebuilt, c.force, got, c.want)
		}
	}
}

// TestLessOrEqual guards the version-compare semantics AND the dev-build
// no-panic contract (the old code panicked on semver.MustParse("dev")).
func TestLessOrEqual(t *testing.T) {
	cases := []struct {
		latestTag, current string
		want               bool // latest <= current (i.e. NOT strictly newer)
	}{
		{"v2026.08.05", "2026.08.05", true},  // equal
		{"v2026.08.06", "2026.08.05", false}, // latest newer
		{"v2026.08.04", "2026.08.05", true},  // latest older
		{"v2026.08.05", "dev", false},        // dev current -> latest is "newer", offer upgrade (no panic)
		{"nonsense", "2026.08.05", true},      // unparseable latest -> not newer
	}
	for _, c := range cases {
		if got := lessOrEqual(c.latestTag, c.current); got != c.want {
			t.Errorf("lessOrEqual(%q,%q) = %v, want %v", c.latestTag, c.current, got, c.want)
		}
	}
}

// TestUpgradeAssetName pins the exact os-arch asset name (incl. the Windows
// .exe suffix) so a wrong-OS asset can never be selected.
func TestUpgradeAssetName(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "orva-cli-linux-amd64"},
		{"darwin", "arm64", "orva-cli-darwin-arm64"},
		{"windows", "amd64", "orva-cli-windows-amd64.exe"},
	}
	for _, c := range cases {
		if got := upgradeAssetName(c.goos, c.goarch); got != c.want {
			t.Errorf("upgradeAssetName(%q,%q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

// TestUpgradeAssetNameServerBuild pins the server-build asset. The server
// binary registers `upgrade` too, and installing the slim CLI over it deletes
// `orva serve`, which leaves systemd unable to start the host again.
func TestUpgradeAssetNameServerBuild(t *testing.T) {
	prev := ServerBuild
	t.Cleanup(func() { ServerBuild = prev })
	ServerBuild = true
	cases := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "orva-linux-amd64"},
		{"linux", "arm64", "orva-linux-arm64"},
	}
	for _, c := range cases {
		if got := upgradeAssetName(c.goos, c.goarch); got != c.want {
			t.Errorf("server upgradeAssetName(%q,%q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

// TestCheckUpgradePlatform pins the fail-loudly rule: server assets exist for
// linux only, and every other platform must error rather than reach the
// CLI-asset path.
func TestCheckUpgradePlatform(t *testing.T) {
	cases := []struct {
		server  bool
		goos    string
		wantErr bool
	}{
		{false, "darwin", false},
		{false, "windows", false},
		{true, "linux", false},
		{true, "darwin", true},
		{true, "windows", true},
	}
	for _, c := range cases {
		err := checkUpgradePlatform(c.server, c.goos)
		if (err != nil) != c.wantErr {
			t.Errorf("checkUpgradePlatform(%v,%q) err = %v, wantErr %v", c.server, c.goos, err, c.wantErr)
		}
	}
}

// TestDetectLatestServerBuild resolves the server asset out of a release that
// publishes both matrices — the CLI asset for the same platform must lose.
func TestDetectLatestServerBuild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("server builds are published for linux only")
	}
	prev := ServerBuild
	t.Cleanup(func() { ServerBuild = prev })
	ServerBuild = true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
			"tag_name": "v2026.08.09",
			"assets": [
				{"name": "orva-cli-linux-%s", "browser_download_url": "http://dl/CLI"},
				{"name": "orva-linux-%s", "browser_download_url": "http://dl/SERVER"},
				{"name": "checksums.txt", "browser_download_url": "http://dl/checksums"}
			]
		}`, runtime.GOARCH, runtime.GOARCH)
	}))
	defer srv.Close()

	old := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = old })

	rel, found, err := detectLatest(context.Background(), "acme/orva")
	if err != nil || !found {
		t.Fatalf("detectLatest: found=%v err=%v", found, err)
	}
	if want := "orva-linux-" + runtime.GOARCH; rel.AssetName != want {
		t.Errorf("asset = %q, want %q", rel.AssetName, want)
	}
	if rel.AssetURL != "http://dl/SERVER" {
		t.Errorf("asset url = %q, want the server asset", rel.AssetURL)
	}
}

// TestFileSHA256 checks the running-binary hasher against a known digest.
func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bin")
	content := []byte("orva test binary contents")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(p)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}
	if want := sha256Hex(content); got != want {
		t.Errorf("fileSHA256 = %s, want %s", got, want)
	}
	if _, err := fileSHA256(filepath.Join(dir, "missing")); err == nil {
		t.Error("expected error hashing a missing file")
	}
}

// TestParseChecksums covers both line formats and the missing-asset case.
func TestParseChecksums(t *testing.T) {
	blob := "aaaa  orva-cli-linux-arm64\n" +
		"bbbb  orva-cli-linux-amd64\n" + // two-space (sha256sum text mode)
		"cccc *orva-cli-darwin-amd64\n" // leading * (binary mode)
	cases := []struct{ asset, want string }{
		{"orva-cli-linux-amd64", "bbbb"},
		{"orva-cli-darwin-amd64", "cccc"},
		{"orva-cli-windows-amd64.exe", ""}, // absent
	}
	for _, c := range cases {
		if got := parseChecksums(strings.NewReader(blob), c.asset); got != c.want {
			t.Errorf("parseChecksums(%q) = %q, want %q", c.asset, got, c.want)
		}
	}
}

// TestRemoteAssetSHA fetches + parses a checksums file over HTTP.
func TestRemoteAssetSHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "deadbeef  orva-cli-linux-amd64\n")
	}))
	defer srv.Close()

	got, err := remoteAssetSHA(context.Background(), srv.URL, "orva-cli-linux-amd64")
	if err != nil {
		t.Fatalf("remoteAssetSHA: %v", err)
	}
	if got != "deadbeef" {
		t.Errorf("remoteAssetSHA = %q, want deadbeef", got)
	}

	// Non-200 surfaces an error.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer bad.Close()
	if _, err := remoteAssetSHA(context.Background(), bad.URL, "x"); err == nil {
		t.Error("expected error on non-200 checksums fetch")
	}
}

// TestRemoteBuildDiffers exercises the staleness decision end to end against an
// httptest checksums server and a temp "binary".
func TestRemoteBuildDiffers(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "orva")
	content := []byte("the running binary")
	if err := os.WriteFile(exe, content, 0o755); err != nil {
		t.Fatal(err)
	}
	localSHA := sha256Hex(content)
	const asset = "orva-cli-linux-amd64"

	serve := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
	}

	t.Run("equal -> not stale, known", func(t *testing.T) {
		srv := serve(localSHA + "  " + asset + "\n")
		defer srv.Close()
		rel := &releaseInfo{ChecksumsURL: srv.URL, AssetName: asset}
		differs, known := remoteBuildDiffers(context.Background(), rel, exe)
		if !known || differs {
			t.Errorf("equal sha: differs=%v known=%v, want differs=false known=true", differs, known)
		}
	})

	t.Run("different -> stale, known", func(t *testing.T) {
		srv := serve("0000000000000000000000000000000000000000000000000000000000000000  " + asset + "\n")
		defer srv.Close()
		rel := &releaseInfo{ChecksumsURL: srv.URL, AssetName: asset}
		differs, known := remoteBuildDiffers(context.Background(), rel, exe)
		if !known || !differs {
			t.Errorf("different sha: differs=%v known=%v, want differs=true known=true", differs, known)
		}
	})

	t.Run("asset absent -> unknown", func(t *testing.T) {
		srv := serve(localSHA + "  some-other-asset\n")
		defer srv.Close()
		rel := &releaseInfo{ChecksumsURL: srv.URL, AssetName: asset}
		_, known := remoteBuildDiffers(context.Background(), rel, exe)
		if known {
			t.Error("absent asset: want known=false")
		}
	})

	t.Run("no checksums url -> unknown", func(t *testing.T) {
		rel := &releaseInfo{AssetName: asset}
		_, known := remoteBuildDiffers(context.Background(), rel, exe)
		if known {
			t.Error("no checksums url: want known=false")
		}
	})
}

// TestDetectLatest resolves the platform asset from a canned GitHub
// releases/latest payload, and confirms wrong-OS assets are ignored.
func TestDetectLatest(t *testing.T) {
	wantAsset := upgradeAssetName(runtime.GOOS, runtime.GOARCH)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/orva/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		// A full matrix plus checksums.txt; only the running platform's asset
		// should be selected.
		fmt.Fprintf(w, `{
			"tag_name": "v2026.08.09",
			"assets": [
				{"name": "orva-cli-linux-amd64", "browser_download_url": "http://dl/linux-amd64"},
				{"name": "orva-cli-linux-arm64", "browser_download_url": "http://dl/linux-arm64"},
				{"name": "orva-cli-darwin-amd64", "browser_download_url": "http://dl/darwin-amd64"},
				{"name": "orva-cli-darwin-arm64", "browser_download_url": "http://dl/darwin-arm64"},
				{"name": "orva-cli-windows-amd64.exe", "browser_download_url": "http://dl/win-amd64"},
				{"name": "orva-cli-windows-arm64.exe", "browser_download_url": "http://dl/win-arm64"},
				{"name": %q, "browser_download_url": "http://dl/THIS"},
				{"name": "checksums.txt", "browser_download_url": "http://dl/checksums"}
			]
		}`, wantAsset)
	})
	mux.HandleFunc("/repos/acme/empty/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		// No asset for any real platform.
		fmt.Fprint(w, `{"tag_name":"v1.0.0","assets":[{"name":"README.md","browser_download_url":"http://dl/readme"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	old := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = old })

	rel, found, err := detectLatest(context.Background(), "acme/orva")
	if err != nil {
		t.Fatalf("detectLatest: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if rel.Tag != "v2026.08.09" {
		t.Errorf("tag = %q, want v2026.08.09", rel.Tag)
	}
	if rel.AssetName != wantAsset {
		t.Errorf("asset = %q, want %q", rel.AssetName, wantAsset)
	}
	if rel.AssetURL != "http://dl/THIS" {
		t.Errorf("asset url = %q, want the exact-name match", rel.AssetURL)
	}
	if rel.ChecksumsURL != "http://dl/checksums" {
		t.Errorf("checksums url = %q, want http://dl/checksums", rel.ChecksumsURL)
	}

	// A release with no asset for this platform → found=false, no error.
	if _, found, err := detectLatest(context.Background(), "acme/empty"); err != nil || found {
		t.Errorf("empty release: found=%v err=%v, want found=false err=nil", found, err)
	}
}

// TestDownloadVerifyApply is the core security test: verify-before-swap, a
// tampered checksum leaves the target intact, and a symlinked target updates
// the real file.
func TestDownloadVerifyApply(t *testing.T) {
	const asset = "orva-cli-test"
	newBytes := []byte("the NEW binary bytes v2\n")
	goodSum := sha256Hex(newBytes)

	// One server serves the asset and a checksums.txt; the checksum body is
	// swapped per subtest.
	var checksumsBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(newBytes) })
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, checksumsBody) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rel := &releaseInfo{AssetName: asset, AssetURL: srv.URL + "/asset", ChecksumsURL: srv.URL + "/checksums"}

	t.Run("good checksum swaps the binary", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "orva")
		if err := os.WriteFile(exe, []byte("OLD binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		checksumsBody = goodSum + "  " + asset + "\n"
		if err := downloadVerifyApply(context.Background(), rel, exe); err != nil {
			t.Fatalf("downloadVerifyApply: %v", err)
		}
		got, _ := os.ReadFile(exe)
		if string(got) != string(newBytes) {
			t.Errorf("target not swapped: got %q", got)
		}
		assertNoSwapLeftovers(t, dir)
	})

	t.Run("bad checksum leaves target intact", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "orva")
		if err := os.WriteFile(exe, []byte("OLD binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		checksumsBody = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef00  " + asset + "\n"
		if err := downloadVerifyApply(context.Background(), rel, exe); err == nil {
			t.Fatal("expected a checksum-mismatch error")
		}
		got, _ := os.ReadFile(exe)
		if string(got) != "OLD binary" {
			t.Errorf("target must be untouched on checksum mismatch, got %q", got)
		}
		assertNoSwapLeftovers(t, dir)
	})

	t.Run("resolved symlink updates the real file, leaves the link", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink semantics differ on Windows")
		}
		dir := t.TempDir()
		real := filepath.Join(dir, "orva-real")
		link := filepath.Join(dir, "orva")
		if err := os.WriteFile(real, []byte("OLD binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		// runUpgrade resolves the symlink via executablePath() BEFORE the swap
		// (update.Apply does not resolve symlinks — it would overwrite the link
		// itself). Mirror that: resolve, then apply on the real target.
		resolved, err := filepath.EvalSymlinks(link)
		if err != nil {
			t.Fatal(err)
		}
		if resolved != real {
			t.Fatalf("EvalSymlinks(link) = %q, want %q", resolved, real)
		}
		checksumsBody = goodSum + "  " + asset + "\n"
		if err := downloadVerifyApply(context.Background(), rel, resolved); err != nil {
			t.Fatalf("downloadVerifyApply on resolved target: %v", err)
		}
		got, _ := os.ReadFile(real)
		if string(got) != string(newBytes) {
			t.Errorf("real target not updated: got %q", got)
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("symlink should still be a symlink after upgrade (err=%v)", err)
		}
	})
}

// assertNoSwapLeftovers fails if update.Apply left a temp/.old/.new artifact.
func assertNoSwapLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".new") || strings.HasSuffix(n, ".old") || strings.HasPrefix(n, ".orva") {
			t.Errorf("unexpected swap leftover: %s", n)
		}
	}
}
