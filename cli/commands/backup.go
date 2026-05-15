package commands

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// backupCmd surfaces the dashboard's Settings → Backup & Restore card
// to the terminal. Both subcommands hit the same authenticated REST
// endpoints the dashboard uses, so the behavior is byte-faithful:
// `orva backup download` produces a tarball restorable via either
// `orva backup restore` or the dashboard, and vice versa.
var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Download or restore a point-in-time snapshot",
	Long: `Download a complete snapshot of the running Orva instance, or restore one.

A snapshot is a single gzip-tar containing the SQLite database, every
deployed function version, the secrets master key, and the bootstrap
admin API key — exactly what's behind the Settings → Backup & Restore
card in the dashboard.`,
}

var backupDownloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download a snapshot to a local file",
	Long: `Download a point-in-time snapshot from the connected Orva instance.

The default output filename matches the dashboard's: orva-backup-<RFC3339>.tar.gz
in the current directory. Override with --output / -o.`,
	RunE: runBackupDownload,
}

var backupRestoreCmd = &cobra.Command{
	Use:   "restore [archive]",
	Short: "Restore a snapshot into the connected Orva instance",
	Long: `Restore a snapshot previously produced by ` + "`orva backup download`" + `
(or the dashboard's "Download backup" button).

The server stops accepting new requests, verifies every file's sha256
against the archive's manifest, performs an atomic swap of the live
database + keys + function code trees, then exits so the supervisor
(systemd / docker restart: unless-stopped) reopens the new files. The
caller's connection is reset mid-stream — that's expected. Wait ~5s
and reconnect.`,
	Args: cobra.ExactArgs(1),
	RunE: runBackupRestore,
}

func init() {
	backupDownloadCmd.Flags().StringP("output", "o", "",
		"output path (default: orva-backup-<RFC3339>.tar.gz in cwd)")
	backupRestoreCmd.Flags().Bool("yes", false,
		"skip the confirmation prompt (required for non-interactive use)")

	backupCmd.AddCommand(backupDownloadCmd, backupRestoreCmd)
}

func runBackupDownload(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	out, _ := cmd.Flags().GetString("output")
	if out == "" {
		stamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
		out = fmt.Sprintf("orva-backup-%s.tar.gz", stamp)
	}
	// Refuse to clobber — an existing backup at the target path probably
	// means the operator typed the wrong name. They can `rm` and retry.
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists; pick a different path or remove it first", out)
	}

	resp, err := client.Get("/api/v1/backup")
	if err != nil {
		return fmt.Errorf("backup request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("backup failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return fmt.Errorf("write archive: %w", err)
	}

	abs, _ := filepath.Abs(out)
	fmt.Printf("Saved %s (%s)\n", abs, formatBytes(n))
	return nil
}

func runBackupRestore(cmd *cobra.Command, args []string) error {
	archivePath := args[0]
	st, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	if st.Size() == 0 {
		return fmt.Errorf("archive is empty")
	}

	confirmed, _ := cmd.Flags().GetBool("yes")
	if !confirmed {
		fmt.Fprintf(os.Stderr,
			"This will overwrite the live database, function code, "+
				"secrets master key, and admin key.\n"+
				"The orvad process will exit so its supervisor can reopen "+
				"the restored files.\n\n"+
				"Archive: %s (%s)\n\n"+
				"Re-run with --yes to proceed.\n",
			archivePath, formatBytes(st.Size()))
		return fmt.Errorf("confirmation required")
	}

	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	// Build the multipart body in-memory. Restore archives are big
	// (potentially hundreds of MB if a function bundle includes
	// node_modules) but the Go HTTP layer streams the body, so we
	// pipe through io.Pipe to avoid loading the whole file at once.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	go func() {
		defer pw.Close()
		defer mw.Close()
		part, err := mw.CreateFormFile("archive", filepath.Base(archivePath))
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, f); err != nil {
			pw.CloseWithError(err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost,
		client.BaseURL+"/api/v1/restore?confirm=1", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if client.APIKey != "" {
		req.Header.Set("X-Orva-API-Key", client.APIKey)
	}

	resp, err := client.HTTP.Do(req)
	if err != nil {
		// The server `os.Exit`s about 1s after responding 200, so a
		// connection-reset from the client side AFTER receiving the
		// body is the expected happy path. Surface it as a hint.
		return fmt.Errorf("restore request failed: %w (note: connection "+
			"resets after success are expected — the server is restarting)", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("restore failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	fmt.Println(string(bytes.TrimSpace(body)))
	fmt.Fprintln(os.Stderr, "\nServer is restarting to load the restored files. Wait ~5s, then reconnect.")
	return nil
}

// formatBytes is a tiny pretty-printer for archive sizes.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
