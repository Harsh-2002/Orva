package backup

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// RecoveryMarkerName is host-owned control metadata, never archive content.
const RecoveryMarkerName = ".workload-recovery"

func recoveryPathAllowed(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == RecoveryMarkerName {
			return false
		}
	}
	p := strings.Split(rel, "/")
	if rel == "orva.db" || rel == "manifest.json" || rel == "keys" ||
		rel == "keys/master.key" || rel == "keys/admin.key" || rel == "functions" {
		return true
	}
	if len(p) >= 2 && p[0] == "functions" {
		u, err := uuid.Parse(p[1])
		return err == nil && u.String() == p[1]
	}
	return false
}

func validateRecoveryStage(stage, dataDir string) error {
	if err := filepath.WalkDir(stage, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil || rel == "." {
			return err
		}
		if !recoveryPathAllowed(filepath.ToSlash(rel)) || d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: reserved or unsupported recovery path", ErrBadArchive)
		}
		return nil
	}); err != nil {
		return err
	}
	// Manifest paths are untrusted too; never hash outside the staging tree.
	// Their validation happens before readAndVerifyManifest (see extraction).
	for _, name := range []string{"functions", "orva.db"} {
		st, err := os.Lstat(filepath.Join(dataDir, name))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore: symlinked live destination")
		}
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "functions"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore: symlinked live function")
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(stage, "orva.db")+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,COALESCE(code_hash,'') FROM functions`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return err
		}
		u, err := uuid.Parse(id)
		if err != nil || u.String() != id || (hash != "" && !isVersionHash(hash)) {
			return fmt.Errorf("%w: unsafe restored function identity", ErrBadArchive)
		}
	}
	return rows.Err()
}
