package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

// orvaRepo is the GitHub repo to query for releases. Overridable for tests.
var orvaRepo = "Harsh-2002/Orva"

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
		Filters: []string{"^orva-cli-"},
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

	if check {
		if latest.LessOrEqual(current) {
			fmt.Fprintf(out, "orva %s is up to date (latest: %s)\n", Version, latestStr)
			return nil
		}
		fmt.Fprintf(out, "orva %s is available (current: %s)\n", latestStr, Version)
		return nil
	}

	if !force && latest.LessOrEqual(current) {
		fmt.Fprintf(out, "orva %s is already the latest.\n", Version)
		return nil
	}

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}

	if err := assertWritable(exe); err != nil {
		return fmt.Errorf("%w\nhint: re-run with `sudo orva upgrade` if the binary lives in a system path like /usr/local/bin", err)
	}

	fmt.Fprintf(out, "Upgrading orva %s -> %s ...\n", Version, latestStr)
	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}

	fmt.Fprintf(out, "Upgraded to orva %s\n", latestStr)
	return nil
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
