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

// TestCheckRootfsVersion covers the binary/rootfs coupling guard. Every
// stamped runtime must match: a current Node rootfs must not hide a stale
// Python one.
func TestCheckRootfsVersion(t *testing.T) {
	writeStamp := func(t *testing.T, root, runtime, version string) {
		t.Helper()
		path := filepath.Join(root, "rootfs", runtime, ".orva-rootfs-version")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(version+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("unstamped is allowed", func(t *testing.T) {
		if err := checkRootfsVersion(t.TempDir(), "v2026.10.25"); err != nil {
			t.Fatalf("unstamped rootfs must not block an upgrade: %v", err)
		}
	})

	t.Run("matching runtimes pass", func(t *testing.T) {
		dir := t.TempDir()
		writeStamp(t, dir, "node", "v2026.10.25")
		writeStamp(t, dir, "python", "v2026.10.25")
		if err := checkRootfsVersion(dir, "v2026.10.25"); err != nil {
			t.Fatalf("matching rootfs must pass: %v", err)
		}
	})

	t.Run("stale node refuses", func(t *testing.T) {
		dir := t.TempDir()
		writeStamp(t, dir, "node", "v2026.09.04")
		err := checkRootfsVersion(dir, "v2026.10.25")
		if err == nil {
			t.Fatal("stale rootfs must refuse the binary-only upgrade")
		}
		if !strings.Contains(err.Error(), "node is v2026.09.04") || !strings.Contains(err.Error(), "install.sh") {
			t.Fatalf("refusal must name the stale runtime, its version and the remedy, got: %v", err)
		}
	})

	t.Run("stale python refuses even when node is current", func(t *testing.T) {
		dir := t.TempDir()
		writeStamp(t, dir, "node", "v2026.10.25")
		writeStamp(t, dir, "python", "v2026.09.04")
		err := checkRootfsVersion(dir, "v2026.10.25")
		if err == nil {
			t.Fatal("a stale python rootfs must refuse even when node matches")
		}
		if !strings.Contains(err.Error(), "python is v2026.09.04") {
			t.Fatalf("refusal must name python, got: %v", err)
		}
		if strings.Contains(err.Error(), "node is ") {
			t.Fatalf("a current node rootfs must not be named as stale, got: %v", err)
		}
	})

	t.Run("both stale names both", func(t *testing.T) {
		dir := t.TempDir()
		writeStamp(t, dir, "node", "v2026.09.04")
		writeStamp(t, dir, "python", "v2026.08.01")
		err := checkRootfsVersion(dir, "v2026.10.25")
		if err == nil {
			t.Fatal("both stale must refuse")
		}
		for _, want := range []string{"node is v2026.09.04", "python is v2026.08.01"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal must name %q, got: %v", want, err)
			}
		}
	})

	t.Run("one stamped runtime is enough to refuse", func(t *testing.T) {
		dir := t.TempDir()
		writeStamp(t, dir, "python", "v2026.09.04")
		if err := checkRootfsVersion(dir, "v2026.10.25"); err == nil {
			t.Fatal("a python-only stamp must also block")
		}
	})

	t.Run("empty target is allowed", func(t *testing.T) {
		dir := t.TempDir()
		writeStamp(t, dir, "node", "v2026.09.04")
		if err := checkRootfsVersion(dir, ""); err != nil {
			t.Fatalf("empty target version must not block: %v", err)
		}
	})
}

// TestResolveDataDir pins the precedence the service and the installer share:
// flag, environment, installer record, unit file, default. A bare `orva
// upgrade` has none of the unit's environment, which is why the record exists.
func TestResolveDataDir(t *testing.T) {
	prevRecord, prevUnits := installRecordPath, serviceUnitPaths
	t.Cleanup(func() { installRecordPath, serviceUnitPaths = prevRecord, prevUnits })

	setup := func(t *testing.T) (record, unit string) {
		t.Helper()
		dir := t.TempDir()
		record = filepath.Join(dir, "install.conf")
		unit = filepath.Join(dir, "orva.service")
		installRecordPath = record
		serviceUnitPaths = []string{unit}
		return record, unit
	}
	write := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("default when nothing is configured", func(t *testing.T) {
		setup(t)
		t.Setenv("ORVA_DATA_DIR", "")
		if got := resolveDataDir(""); got != "/var/lib/orva" {
			t.Errorf("resolveDataDir() = %q, want /var/lib/orva", got)
		}
	})

	t.Run("unit file is a fallback for installs without a record", func(t *testing.T) {
		_, unit := setup(t)
		write(t, unit, "[Service]\nEnvironment=ORVA_DATA_DIR=/srv/from-unit\n")
		t.Setenv("ORVA_DATA_DIR", "")
		if got := resolveDataDir(""); got != "/srv/from-unit" {
			t.Errorf("resolveDataDir() = %q, want /srv/from-unit", got)
		}
	})

	t.Run("openrc export is parsed", func(t *testing.T) {
		_, unit := setup(t)
		write(t, unit, "export ORVA_DATA_DIR=\"/srv/openrc\"\n")
		t.Setenv("ORVA_DATA_DIR", "")
		if got := resolveDataDir(""); got != "/srv/openrc" {
			t.Errorf("resolveDataDir() = %q, want /srv/openrc", got)
		}
	})

	t.Run("installer record wins over the unit", func(t *testing.T) {
		record, unit := setup(t)
		write(t, record, "# written by install.sh\nDATA_DIR=/srv/from-record\n")
		write(t, unit, "Environment=ORVA_DATA_DIR=/srv/from-unit\n")
		t.Setenv("ORVA_DATA_DIR", "")
		if got := resolveDataDir(""); got != "/srv/from-record" {
			t.Errorf("resolveDataDir() = %q, want /srv/from-record", got)
		}
	})

	t.Run("environment wins over the record", func(t *testing.T) {
		record, _ := setup(t)
		write(t, record, "DATA_DIR=/srv/from-record\n")
		t.Setenv("ORVA_DATA_DIR", "/srv/from-env")
		if got := resolveDataDir(""); got != "/srv/from-env" {
			t.Errorf("resolveDataDir() = %q, want /srv/from-env", got)
		}
	})

	t.Run("flag wins over everything", func(t *testing.T) {
		record, _ := setup(t)
		write(t, record, "DATA_DIR=/srv/from-record\n")
		t.Setenv("ORVA_DATA_DIR", "/srv/from-env")
		if got := resolveDataDir("/srv/from-flag"); got != "/srv/from-flag" {
			t.Errorf("resolveDataDir() = %q, want /srv/from-flag", got)
		}
	})

	t.Run("malformed record falls through", func(t *testing.T) {
		record, _ := setup(t)
		write(t, record, "this is not a key value line\n")
		t.Setenv("ORVA_DATA_DIR", "")
		if got := resolveDataDir(""); got != "/var/lib/orva" {
			t.Errorf("resolveDataDir() = %q, want the default", got)
		}
	})

	t.Run("quoted and spaced values are cleaned", func(t *testing.T) {
		record, _ := setup(t)
		write(t, record, "DATA_DIR = \"/srv/quoted\"\n")
		t.Setenv("ORVA_DATA_DIR", "")
		if got := resolveDataDir(""); got != "/srv/quoted" {
			t.Errorf("resolveDataDir() = %q, want /srv/quoted", got)
		}
	})
}

// TestGuardFindsCustomDataDir reproduces the blind spot this record exists for:
// a bare `orva upgrade` has no ORVA_DATA_DIR, so only the installer record can
// point the guard at a custom data directory.
func TestGuardFindsCustomDataDir(t *testing.T) {
	prevRecord, prevUnits := installRecordPath, serviceUnitPaths
	t.Cleanup(func() { installRecordPath, serviceUnitPaths = prevRecord, prevUnits })

	dataDir := t.TempDir()
	for _, rt := range []string{"node", "python"} {
		dir := filepath.Join(dataDir, "rootfs", rt)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rt, version string) {
		t.Helper()
		p := filepath.Join(dataDir, "rootfs", rt, ".orva-rootfs-version")
		if err := os.WriteFile(p, []byte(version+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("node", "v2026.10.25")
	write("python", "v2026.09.04")

	record := filepath.Join(t.TempDir(), "install.conf")
	if err := os.WriteFile(record, []byte("DATA_DIR="+dataDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installRecordPath = record
	serviceUnitPaths = nil
	t.Setenv("ORVA_DATA_DIR", "")

	err := checkRootfsVersion(resolveDataDir(""), "v2026.10.25")
	if err == nil {
		t.Fatal("guard must find a custom data dir through the installer record and refuse the stale python rootfs")
	}
	if !strings.Contains(err.Error(), "python is v2026.09.04") {
		t.Fatalf("refusal must name the stale python rootfs, got: %v", err)
	}
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
		{"nonsense", "2026.08.05", true},     // unparseable latest -> not newer
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
