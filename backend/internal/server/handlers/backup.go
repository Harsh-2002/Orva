package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/config"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// BackupHandler exposes operator-facing snapshot + restore endpoints. The
// admin permission gate is handled by middleware_auth.go (see the
// requiredPermission map there). These handlers assume the request has
// already been authenticated and authorized.
type BackupHandler struct {
	DB  *database.Database
	Cfg *config.Config
}

// Download handles GET /api/v1/backup. Streams a gzip tar of the data
// dir to the client. Steps:
//
//  1. VACUUM INTO a tempfile under cfg.Data.Dir (so the snapshot lives on
//     the same filesystem as the live DB — keeps the rename inside
//     ArchiveTo cheap and avoids EXDEV on cross-FS temp dirs).
//  2. Stream a gzip-tar response containing orva.db (the snapshot) and
//     the entire functions/ tree.
//  3. Remove the tempfile on the way out.
//
// Content-Disposition uses an RFC3339 timestamp so multiple backups in
// the same browser session get unique filenames.
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil || h.Cfg == nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "backup handler not wired", "")
		return
	}

	tmp, err := os.CreateTemp(h.Cfg.Data.Dir, "snapshot-*.db")
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "BACKUP_FAILED", "create snapshot tempfile: "+err.Error(), "")
		return
	}
	tmpPath := tmp.Name()
	// SQLite refuses to clobber, so close + delete the empty tempfile
	// before VACUUM INTO writes the real snapshot in its place.
	tmp.Close()
	_ = os.Remove(tmpPath)
	defer os.Remove(tmpPath)

	if err := backup.SnapshotDB(h.DB.WriteDB(), tmpPath); err != nil {
		respond.Error(w, http.StatusInternalServerError, "BACKUP_FAILED", "snapshot: "+err.Error(), "")
		return
	}

	// RFC3339 timestamps include `:` (e.g. 2026-05-01T12:34:56Z) which is
	// illegal on Windows / SMB filesystems. Replace `:` with `-` so the
	// filename is portable across the dashboard's likely download targets.
	stamp := strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339), ":", "-")
	filename := fmt.Sprintf("orva-backup-%s.tar.gz", stamp)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// Disable any intermediate buffering proxies might do.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if err := backup.ArchiveTo(w, h.Cfg.Data.Dir, tmpPath); err != nil {
		// Headers are already flushed, so we can't switch to a JSON
		// error response. Best we can do is log via the response
		// writer if it's still open. The client will see a truncated
		// gzip stream and gunzip will complain — that's the right
		// signal.
		return
	}
}

// Restore handles POST /api/v1/restore?confirm=1. Body is a multipart
// form with the archive in the `archive` field.
//
// We require ?confirm=1 because a successful restore moves the live DB
// aside and replaces it; an accidental fetch from a misconfigured
// dashboard shouldn't be able to nuke production. The frontend modal
// provides the confirm signal explicitly.
//
// After a successful swap the handler **exits the process** so the
// supervisor (systemd / docker `restart: unless-stopped`) restarts
// orvad against the new files. Without this, the in-process *sql.DB
// handle keeps reading/writing the moved-aside orva.db.before-restore-*
// file and writes get silently lost on the next restart. The exit
// happens in a goroutine on a short timer so the HTTP response has time
// to flush back to the client first.
func (h *BackupHandler) Restore(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil || h.Cfg == nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "backup handler not wired", "")
		return
	}
	if r.URL.Query().Get("confirm") != "1" {
		respond.Error(w, http.StatusBadRequest, "CONFIRM_REQUIRED", "restore overwrites the live database; pass ?confirm=1", "")
		return
	}

	// Caps multipart memory at 32 MiB; larger uploads spill to disk.
	// The bodySizeMiddleware is bypassed here — restore archives are
	// expected to exceed the API's 6 MB JSON cap. We rely on the
	// admin-only auth gate as the abuse control.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_MULTIPART", "parse multipart: "+err.Error(), "")
		return
	}
	file, _, err := r.FormFile("archive")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "MISSING_ARCHIVE", "expected multipart field 'archive'", "")
		return
	}
	defer file.Close()

	if err := backup.RestoreFrom(file, h.Cfg.Data.Dir); err != nil {
		// Bad gzip / non-orva tarball / corrupt-db / wrong format
		// version / sha256 mismatch are the *client's* problem — surface
		// them as 400 so callers don't page on a malformed upload.
		// Anything else (disk I/O, rename failure) stays 500.
		if errors.Is(err, backup.ErrBadArchive) {
			respond.Error(w, http.StatusBadRequest, "BAD_ARCHIVE", err.Error(), "")
			return
		}
		if errors.Is(err, backup.ErrIncompatibleFormat) {
			respond.Error(w, http.StatusBadRequest, "INCOMPATIBLE_FORMAT", err.Error(), "")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "RESTORE_FAILED", err.Error(), "")
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"status": "restored",
		"next":   "reload",
		"hint":   "server is restarting to pick up the restored files; reload in a few seconds",
	})

	// Restart cleanly so the supervisor reopens the new DB. The 1s
	// timer is enough for the response to flush over a local socket;
	// remote browsers will already have received it by the time the
	// connection drops because we close the response above.
	go func() {
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
}
