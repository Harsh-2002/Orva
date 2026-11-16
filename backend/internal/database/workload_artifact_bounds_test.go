package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestWorkloadArtifactSnapshotBounds(t *testing.T) {
	for _, kind := range []string{"count", "ciphertext", "environment", "invalid_key"} {
		t.Run(kind, func(t *testing.T) {
			db, r := artifactTestSetup(t)
			switch kind {
			case "count":
				tx, err := db.write.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for i := 0; i <= MaxWorkloadArtifactSecrets; i++ {
					if _, err := tx.Exec(`INSERT INTO function_secrets(function_id,key,value_encrypted) VALUES(?,?,?)`, r.OwnerID, fmt.Sprintf("k%d", i), "ciphertext"); err != nil {
						t.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			case "ciphertext":
				if err := db.UpsertSecret(r.OwnerID, "k", strings.Repeat("x", MaxWorkloadArtifactCiphertextBytes)); err != nil {
					t.Fatal(err)
				}
			case "environment":
				if _, err := db.write.Exec(`UPDATE functions SET env_vars=? WHERE id=?`, `{"k":"`+strings.Repeat("x", MaxWorkloadArtifactEnvBytes)+`"}`, r.OwnerID); err != nil {
					t.Fatal(err)
				}
			case "invalid_key":
				if err := db.UpsertSecret(r.OwnerID, "bad\x00key", "ciphertext"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := db.GetWorkloadArtifactSnapshot(context.Background(), r.OwnerID)
			if got != nil || !errors.Is(err, ErrWorkloadArtifactInvalid) {
				t.Fatalf("invalid snapshot returned: present=%v error=%v", got != nil, err)
			}
		})
	}
}
