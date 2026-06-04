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
	"strings"
	"testing"

	"github.com/creativeprojects/go-selfupdate"
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
		rel := &selfupdate.Release{ValidationAssetURL: srv.URL, AssetName: asset}
		differs, known := remoteBuildDiffers(context.Background(), rel, exe)
		if !known || differs {
			t.Errorf("equal sha: differs=%v known=%v, want differs=false known=true", differs, known)
		}
	})

	t.Run("different -> stale, known", func(t *testing.T) {
		srv := serve("0000000000000000000000000000000000000000000000000000000000000000  " + asset + "\n")
		defer srv.Close()
		rel := &selfupdate.Release{ValidationAssetURL: srv.URL, AssetName: asset}
		differs, known := remoteBuildDiffers(context.Background(), rel, exe)
		if !known || !differs {
			t.Errorf("different sha: differs=%v known=%v, want differs=true known=true", differs, known)
		}
	})

	t.Run("asset absent -> unknown", func(t *testing.T) {
		srv := serve(localSHA + "  some-other-asset\n")
		defer srv.Close()
		rel := &selfupdate.Release{ValidationAssetURL: srv.URL, AssetName: asset}
		_, known := remoteBuildDiffers(context.Background(), rel, exe)
		if known {
			t.Error("absent asset: want known=false")
		}
	})

	t.Run("no validation url -> unknown", func(t *testing.T) {
		rel := &selfupdate.Release{AssetName: asset}
		_, known := remoteBuildDiffers(context.Background(), rel, exe)
		if known {
			t.Error("no validation url: want known=false")
		}
	})
}
