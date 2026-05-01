package handlers

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Harsh-2002/Orva/internal/backup"
	"github.com/Harsh-2002/Orva/internal/config"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
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

	filename := fmt.Sprintf("orva-backup-%s.tar.gz", time.Now().UTC().Format(time.RFC3339))
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
// On success the response body is `{"status":"restored","next":"reload"}`
// — the operator is expected to restart the orvad process so all open
// *sql.DB handles re-open against the new file.
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
		respond.Error(w, http.StatusInternalServerError, "RESTORE_FAILED", err.Error(), "")
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"status": "restored",
		"next":   "reload",
	})
}
