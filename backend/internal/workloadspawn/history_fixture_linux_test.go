//go:build linux

package workloadspawn_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestHistoryStageConnectionPragmas(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "stage.db")+"?"+historyStagePragmas)
	liveMust(t, err)
	defer db.Close()
	first, err := db.Conn(t.Context())
	liveMust(t, err)
	defer first.Close()
	second, err := db.Conn(t.Context())
	liveMust(t, err)
	defer second.Close()
	for _, conn := range []*sql.Conn{first, second} {
		var busy, foreignKeys int
		liveMust(t, conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busy))
		liveMust(t, conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys))
		if busy != 10000 || foreignKeys != 1 {
			t.Fatalf("staging connection pragmas: busy_timeout=%d foreign_keys=%d", busy, foreignKeys)
		}
	}
}

func TestHistoryManagedStageSeedsCanonicalPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stage.db")
	db, err := database.New(path)
	liveMust(t, err)
	defer db.Close()
	liveMust(t, db.Migrate())
	stageHistoryManaged(t, path, "workload_iam_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_policies")
	id := ids.New()
	liveMust(t, db.InsertFunction(&database.Function{ID: id, Name: "stage", Runtime: "node", Entrypoint: "handler.js", Status: "created"}))
	actor := database.IAMActorReference{Kind: "system", ID: ids.New()}
	row, err := db.NewIAMPolicy(t.Context(), id, false, actor)
	liveMust(t, err)
	row, err = db.SaveIAMPolicy(t.Context(), database.IAMPolicyChange{
		OwnerID: id, ExpectedRevision: row.Revision, ExpectedApprovedArtifact: row.ApprovedArtifact,
		ApprovedArtifact: "fixture-artifact", Actor: actor,
		PolicyJSON: []byte(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"seeded"}},"context":"execution"}],"inbound":[]}`),
	})
	liveMust(t, err)
	stageHistoryManaged(t, path, "workload_managed_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_inline")
	conn, err := sql.Open("sqlite", path+"?"+historyStagePragmas)
	liveMust(t, err)
	defer conn.Close()
	var inline string
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT policy_json FROM workload_iam_inline WHERE function_id=?", id).Scan(&inline))
	if inline != row.PolicyJSON {
		t.Fatal("managed staging did not copy the actual canonical policy")
	}
	after, err := db.GetIAMPolicy(t.Context(), id)
	liveMust(t, err)
	if after.Revision != row.Revision || after.AuthorityEpoch != row.AuthorityEpoch || after.ApprovedArtifact != row.ApprovedArtifact || after.PolicyJSON != row.PolicyJSON {
		t.Fatal("managed staging changed canonical policy authority")
	}
}
