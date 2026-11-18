package workloadrecovery

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestStartupOrderReconstructionFailureKeepsAdmissionClosed(t *testing.T) {
	f, archive := seed(t, true)
	c := coordinator(t, f.dir)
	_, err := c.Restore(t.Context(), bytes.NewReader(archive))
	must(t, err)
	// seed already failed the archived build. Add unfinished work to the
	// restored fixture so a failure observed during reconstruction is fresh.
	func() {
		db := openDB(t, f.dir)
		defer func() { must(t, db.Close()) }()
		f.deployment = ids.New()
		_, err := db.WriteDB().Exec(`INSERT INTO deployments(id,function_id,version,status) VALUES(?,?,2,'building')`,
			f.deployment, f.owners[0])
		must(t, err)
		var status string
		must(t, db.ReadDB().QueryRow(`SELECT status FROM deployments WHERE id=?`, f.deployment).Scan(&status))
		if status != "building" {
			t.Fatal("fixture does not contain unfinished work before startup")
		}
	}()

	missingProof := errors.New("fixture has no current artifact ownership proof")
	reconstructed := 0
	for range 2 {
		db, _, err := c.Start(t.Context(), func(_ context.Context, db *database.Database) error {
			reconstructed++
			assertRetained(t, db, f, f.dir, true)
			return missingProof
		}, func(context.Context, *database.Database) error {
			t.Fatal("startup callback ran without successful reconstruction")
			return nil
		})
		if db != nil {
			_ = db.Close()
			t.Fatal("failed startup returned a database handle")
		}
		if !errors.Is(err, missingProof) || !c.host.AdmissionClosed() {
			t.Fatalf("startup failure did not preserve closed admission: %v", err)
		}
	}
	if reconstructed != 2 {
		t.Fatalf("reconstruction was not rechecked on retry: %d attempts", reconstructed)
	}
}
