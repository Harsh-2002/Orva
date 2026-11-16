package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func artifactTestSetup(t *testing.T) (*Database, *WorkloadArtifactRecord) {
	t.Helper()
	db := newTestDB(t)
	iamTestFunction(t, db, iamTestOwner, true)
	if err := db.initWorkloadArtifactSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, err := db.NewWorkloadArtifact(context.Background(), iamTestOwner, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	return db, r
}

func artifactTestChange(r *WorkloadArtifactRecord) WorkloadArtifactChange {
	return WorkloadArtifactChange{
		OwnerID: r.OwnerID, ExpectedRevision: r.Revision, ExpectedEpoch: r.Epoch,
		ExpectedGeneration: r.Generation, Actor: iamTestActor,
	}
}

func artifactTestSnapshot(t *testing.T, db *Database, owner string) *WorkloadArtifactSnapshot {
	t.Helper()
	s, err := db.GetWorkloadArtifactSnapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func artifactTestUnchanged(t *testing.T, db *Database, before *WorkloadArtifactSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, artifactTestSnapshot(t, db, before.Artifact.OwnerID)) {
		t.Fatal("failed operation changed durable snapshot")
	}
}

func TestWorkloadArtifactSchemaInert(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	f := iamTestFunction(t, db, iamTestOwner, true)
	assertAbsent := func() {
		t.Helper()
		var n int
		if err := db.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'workload_artifact_%'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("normal opening/migration activated schema: %d, %v", n, err)
		}
	}
	assertAbsent()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	assertAbsent()
	if s, err := db.GetWorkloadArtifactSnapshot(ctx, f.ID); s != nil || !errors.Is(err, ErrWorkloadArtifactStorage) {
		t.Fatalf("schema-less snapshot did not fail closed: %v", err)
	}
	for range 2 {
		if err := db.initWorkloadArtifactSchema(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := iamTestCount(t, db, "workload_artifact_generations"); n != 0 {
		t.Fatal("initializer invented generations")
	}
	if s, err := db.GetWorkloadArtifactSnapshot(ctx, f.ID); s != nil || !errors.Is(err, ErrWorkloadArtifactNotFound) {
		t.Fatalf("missing generation did not deny: %v", err)
	}
	got, err := db.GetFunction(f.ID)
	if err != nil || !reflect.DeepEqual(f, got) {
		t.Fatal("initializer changed function or SDK")
	}
	if n := iamTestCount(t, db, "system_config"); n != 18 {
		t.Fatal("initializer changed boot markers")
	}
	r, err := db.NewWorkloadArtifact(ctx, f.ID, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	before := artifactTestSnapshot(t, db, f.ID)
	if err := db.initWorkloadArtifactSchema(ctx); err != nil {
		t.Fatal(err)
	}
	artifactTestUnchanged(t, db, before)
	if r.Revision != 1 || r.Epoch != 1 || !validIAMUUID(r.Generation) || r.Generation[14] != '7' ||
		r.Actor != iamTestActor || r.ChangeKind != "initialize" || !r.CreatedAt.Equal(r.UpdatedAt) {
		t.Fatalf("invalid initial metadata: %+v", r)
	}
	if _, err := db.NewWorkloadArtifact(ctx, f.ID, iamTestActor); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("duplicate creation: %v", err)
	}
	artifactTestUnchanged(t, db, before)
}

func TestWorkloadArtifactCASAndOwnership(t *testing.T) {
	ctx := context.Background()
	db, r := artifactTestSetup(t)
	iamTestFunction(t, db, iamTestOther, false)
	other, err := db.NewWorkloadArtifact(ctx, iamTestOther, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	beforeOther := artifactTestSnapshot(t, db, other.OwnerID)
	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for range contenders {
		wg.Go(func() {
			<-start
			_, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "winner", "opaque-ciphertext")
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrWorkloadArtifactConflict) {
			t.Fatalf("unexpected CAS error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("CAS winners = %d", winners)
	}
	s := artifactTestSnapshot(t, db, r.OwnerID)
	if s.Artifact.Revision != 2 || s.Artifact.Epoch != 2 || s.Artifact.Generation == r.Generation {
		t.Fatal("rotation reused generation or failed to advance counters")
	}
	if len(s.Secrets) != 1 || s.Secrets[0].Key != "winner" {
		t.Fatal("CAS did not commit exactly one secret mutation")
	}
	before := s.Clone()
	for _, change := range []WorkloadArtifactChange{
		{OwnerID: r.OwnerID, ExpectedRevision: 1, ExpectedEpoch: 2, ExpectedGeneration: s.Artifact.Generation, Actor: iamTestActor},
		{OwnerID: r.OwnerID, ExpectedRevision: 2, ExpectedEpoch: 1, ExpectedGeneration: s.Artifact.Generation, Actor: iamTestActor},
		{OwnerID: r.OwnerID, ExpectedRevision: 2, ExpectedEpoch: 2, ExpectedGeneration: r.Generation, Actor: iamTestActor},
		{OwnerID: other.OwnerID, ExpectedRevision: 1, ExpectedEpoch: 1, ExpectedGeneration: s.Artifact.Generation, Actor: iamTestActor},
	} {
		if _, err := db.UpsertWorkloadArtifactSecret(ctx, change, "PRIVATE", "opaque-ciphertext"); !errors.Is(err, ErrWorkloadArtifactConflict) {
			t.Fatalf("inexact CAS accepted: %v", err)
		}
	}
	artifactTestUnchanged(t, db, before)
	artifactTestUnchanged(t, db, beforeOther)
	for range 3 {
		next, err := db.RotateWorkloadArtifact(ctx, artifactTestChange(&s.Artifact))
		if err != nil || next.Generation == s.Artifact.Generation {
			t.Fatalf("identical-input rotation: %v", err)
		}
		s.Artifact = *next
	}
}

func TestWorkloadArtifactSecretMutationAndCopies(t *testing.T) {
	ctx := context.Background()
	db, r := artifactTestSetup(t)
	for _, key := range []string{"z-last", "a-first"} {
		next, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), key, "opaque-"+key)
		if err != nil || next.Generation == r.Generation || next.ChangeKind != "secret_upsert" {
			t.Fatalf("upsert: %v", err)
		}
		r = next
	}
	s := artifactTestSnapshot(t, db, r.OwnerID)
	if len(s.Secrets) != 2 || s.Secrets[0].Key != "a-first" || s.Secrets[1].Key != "z-last" {
		t.Fatal("snapshot did not own sorted complete rows")
	}
	clone := s.Clone()
	clone.Function.EnvVars["PRIVATE"] = "mutated"
	clone.Secrets[0].ValueEncrypted = "mutated"
	clone.Artifact.Generation = ids.New()
	if reflect.DeepEqual(s, clone) || s.Function.EnvVars["PRIVATE"] == "mutated" || s.Secrets[0].ValueEncrypted == "mutated" {
		t.Fatal("snapshot clone aliases owned data")
	}
	artifactTestUnchanged(t, db, s)
	if (*WorkloadArtifactSnapshot)(nil).Clone() != nil {
		t.Fatal("nil clone")
	}
	// Even an identical ciphertext upsert and absent-key delete rotate.
	next, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "a-first", "opaque-a-first")
	if err != nil || next.Generation == r.Generation {
		t.Fatalf("identical upsert: %v", err)
	}
	r = next
	for _, key := range []string{"a-first", "absent"} {
		next, err := db.DeleteWorkloadArtifactSecret(ctx, artifactTestChange(r), key)
		if err != nil || next.Generation == r.Generation || next.ChangeKind != "secret_delete" {
			t.Fatalf("delete: %v", err)
		}
		r = next
	}
	s = artifactTestSnapshot(t, db, r.OwnerID)
	if len(s.Secrets) != 1 || s.Secrets[0].Key != "z-last" || s.Artifact.Actor != iamTestActor {
		t.Fatal("delete rows or mutation provenance")
	}
	metadata, _ := json.Marshal(s.Artifact)
	for _, forbidden := range []string{"z-last", "opaque", "PRIVATE", "not-an-audit-value"} {
		if strings.Contains(string(metadata), forbidden) {
			t.Fatal("generation metadata leaked config/secret input")
		}
	}
}

func TestWorkloadArtifactSecretRollback(t *testing.T) {
	for _, failure := range []string{"rotate", "upsert", "delete", "commit"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			db, r := artifactTestSetup(t)
			r, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "private-key", "private-ciphertext")
			if err != nil {
				t.Fatal(err)
			}
			before := artifactTestSnapshot(t, db, r.OwnerID)
			var trigger string
			switch failure {
			case "rotate":
				trigger = `CREATE TRIGGER artifact_fail BEFORE UPDATE ON workload_artifact_generations BEGIN SELECT RAISE(ABORT,'private-ciphertext'); END`
			case "upsert":
				trigger = `CREATE TRIGGER artifact_fail BEFORE INSERT ON function_secrets BEGIN SELECT RAISE(ABORT,'private-ciphertext'); END`
			case "delete":
				trigger = `CREATE TRIGGER artifact_fail BEFORE DELETE ON function_secrets BEGIN SELECT RAISE(ABORT,'private-ciphertext'); END`
			case "commit":
				trigger = `CREATE TABLE artifact_guard (owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
				CREATE TRIGGER artifact_fail AFTER UPDATE ON function_secrets BEGIN INSERT INTO artifact_guard VALUES ('absent'); END`
			}
			if _, err := db.write.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			var result *WorkloadArtifactRecord
			if failure == "delete" {
				result, err = db.DeleteWorkloadArtifactSecret(ctx, artifactTestChange(r), "private-key")
			} else {
				result, err = db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "private-key", "new-private-ciphertext")
			}
			if result != nil || !errors.Is(err, ErrWorkloadArtifactStorage) || strings.Contains(err.Error(), "private") {
				t.Fatalf("failed write returned state or unsafe diagnostics: %v", err)
			}
			artifactTestUnchanged(t, db, before)
		})
	}
}

func TestWorkloadArtifactValidationAndExhaustion(t *testing.T) {
	ctx := context.Background()
	db, r := artifactTestSetup(t)
	before := artifactTestSnapshot(t, db, r.OwnerID)
	tests := []struct {
		name string
		edit func(*WorkloadArtifactChange)
	}{
		{"owner", func(c *WorkloadArtifactChange) { c.OwnerID = "not-uuid" }},
		{"owner uppercase", func(c *WorkloadArtifactChange) { c.OwnerID = strings.ToUpper(c.OwnerID) }},
		{"generation", func(c *WorkloadArtifactChange) { c.ExpectedGeneration = "not-uuid" }},
		{"generation uppercase", func(c *WorkloadArtifactChange) { c.ExpectedGeneration = strings.ToUpper(c.ExpectedGeneration) }},
		{"nil generation", func(c *WorkloadArtifactChange) { c.ExpectedGeneration = "00000000-0000-0000-0000-000000000000" }},
		{"revision zero", func(c *WorkloadArtifactChange) { c.ExpectedRevision = 0 }},
		{"revision overflow", func(c *WorkloadArtifactChange) { c.ExpectedRevision = math.MaxInt64 + 1 }},
		{"epoch zero", func(c *WorkloadArtifactChange) { c.ExpectedEpoch = 0 }},
		{"epoch overflow", func(c *WorkloadArtifactChange) { c.ExpectedEpoch = math.MaxUint64 }},
		{"actor token", func(c *WorkloadArtifactChange) { c.Actor.ID = "never-print-this-token" }},
		{"actor kind", func(c *WorkloadArtifactChange) { c.Actor.Kind = "unknown" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := artifactTestChange(r)
			tt.edit(&c)
			if result, err := db.RotateWorkloadArtifact(ctx, c); result != nil || !errors.Is(err, ErrWorkloadArtifactInvalid) {
				t.Fatalf("invalid metadata: %v", err)
			}
			artifactTestUnchanged(t, db, before)
		})
	}
	for _, actor := range []IAMActorReference{{Kind: "system", ID: "token"}, {Kind: "bad", ID: iamTestActorID}} {
		if _, err := db.NewWorkloadArtifact(ctx, iamTestOther, actor); !errors.Is(err, ErrWorkloadArtifactInvalid) {
			t.Fatalf("invalid initial actor: %v", err)
		}
	}
	if _, err := db.NewWorkloadArtifact(ctx, "bad", iamTestActor); !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatalf("invalid initial owner: %v", err)
	}
	if _, err := db.NewWorkloadArtifact(ctx, iamTestOther, iamTestActor); !errors.Is(err, ErrIAMFunctionNotFound) {
		t.Fatalf("missing function: %v", err)
	}
	c := artifactTestChange(r)
	c.OwnerID = iamTestOther
	if _, err := db.RotateWorkloadArtifact(ctx, c); !errors.Is(err, ErrIAMFunctionNotFound) {
		t.Fatalf("missing rotate function: %v", err)
	}
	if _, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "", "ciphertext"); !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatal("empty key accepted")
	}
	if _, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "key", ""); !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatal("empty ciphertext accepted")
	}
	if _, err := db.DeleteWorkloadArtifactSecret(ctx, artifactTestChange(r), ""); !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatal("empty delete key accepted")
	}
	artifactTestUnchanged(t, db, before)
	for _, column := range []string{"revision", "epoch"} {
		if _, err := db.write.Exec(`UPDATE workload_artifact_generations SET revision=1,epoch=1,`+column+`=?`, int64(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
		s := artifactTestSnapshot(t, db, r.OwnerID)
		if result, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(&s.Artifact), "key", "ciphertext"); result != nil || !errors.Is(err, ErrWorkloadArtifactExhausted) {
			t.Fatalf("%s exhausted: %v", column, err)
		}
		artifactTestUnchanged(t, db, s)
	}
	for _, column := range []string{"revision", "epoch"} {
		if _, err := db.write.Exec(`UPDATE workload_artifact_generations SET ` + column + `=9223372036854775808`); err == nil {
			t.Fatalf("schema accepted unsigned overflow in %s", column)
		}
	}
	if _, err := db.write.Exec(`UPDATE workload_artifact_generations SET revision=?,epoch=?`, int64(math.MaxInt64-1), int64(math.MaxInt64-1)); err != nil {
		t.Fatal(err)
	}
	s := artifactTestSnapshot(t, db, r.OwnerID)
	last, err := db.RotateWorkloadArtifact(ctx, artifactTestChange(&s.Artifact))
	if err != nil || last.Revision != math.MaxInt64 || last.Epoch != math.MaxInt64 || last.Generation == s.Artifact.Generation {
		t.Fatalf("last signed increment failed: %v", err)
	}
}

func TestWorkloadArtifactMissingDeletingAndCorruptSnapshot(t *testing.T) {
	ctx := context.Background()
	db, r := artifactTestSetup(t)
	iamTestFunction(t, db, iamTestOther, true)
	if result, err := db.UpsertWorkloadArtifactSecret(ctx, WorkloadArtifactChange{
		OwnerID: iamTestOther, ExpectedRevision: 1, ExpectedEpoch: 1, ExpectedGeneration: ids.New(), Actor: iamTestActor,
	}, "key", "ciphertext"); result != nil || !errors.Is(err, ErrWorkloadArtifactNotFound) {
		t.Fatalf("mutation inferred missing generation: %v", err)
	}
	if result, err := db.GetWorkloadArtifactSnapshot(ctx, "bad"); result != nil || !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatalf("noncanonical snapshot owner: %v", err)
	}
	if _, err := db.write.Exec(`UPDATE functions SET status='deleting' WHERE id=?`, r.OwnerID); err != nil {
		t.Fatal(err)
	}
	if s, err := db.GetWorkloadArtifactSnapshot(ctx, r.OwnerID); s != nil || !errors.Is(err, ErrFunctionDeleting) {
		t.Fatalf("deleting snapshot: %v", err)
	}
	if _, err := db.RotateWorkloadArtifact(ctx, artifactTestChange(r)); !errors.Is(err, ErrFunctionDeleting) {
		t.Fatalf("deleting rotate: %v", err)
	}
	if _, err := db.write.Exec(`UPDATE functions SET status='created',env_vars='{"PRIVATE":7}' WHERE id=?`, r.OwnerID); err != nil {
		t.Fatal(err)
	}
	if s, err := db.GetWorkloadArtifactSnapshot(ctx, r.OwnerID); s != nil || !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatalf("corrupt env was skipped/partial: %v", err)
	}
	if _, err := db.write.Exec(`UPDATE functions SET env_vars='{}' WHERE id=?`, r.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSecret(r.OwnerID, "corrupt", ""); err != nil {
		t.Fatal(err)
	}
	if s, err := db.GetWorkloadArtifactSnapshot(ctx, r.OwnerID); s != nil || !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatalf("corrupt secret was skipped/partial: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if s, err := db.GetWorkloadArtifactSnapshot(cancelled, r.OwnerID); s != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled snapshot returned data: %v", err)
	}
}

func TestWorkloadArtifactSnapshotConcurrentConsistency(t *testing.T) {
	ctx := context.Background()
	db, r := artifactTestSetup(t)
	// The future publisher can share the helper's transaction with config/rows.
	publish := func(r *WorkloadArtifactRecord) (*WorkloadArtifactRecord, error) {
		db.lifecycleMu.RLock()
		defer db.lifecycleMu.RUnlock()
		tx, err := db.write.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		next, err := rotateWorkloadArtifactTx(ctx, tx, artifactTestChange(r), "rotate")
		if err != nil {
			return nil, err
		}
		marker := fmt.Sprint(next.Revision)
		env, _ := json.Marshal(map[string]string{"marker": marker})
		if _, err := tx.ExecContext(ctx, `UPDATE functions SET env_vars=?,code_hash=?,run_entrypoint=? WHERE id=?`, string(env), marker, marker, r.OwnerID); err != nil {
			return nil, err
		}
		for _, key := range []string{"a", "z"} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO function_secrets(function_id,key,value_encrypted) VALUES(?,?,?)
				ON CONFLICT(function_id,key) DO UPDATE SET value_encrypted=excluded.value_encrypted`, r.OwnerID, key, marker); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return next, nil
	}
	var err error
	r, err = publish(r)
	if err != nil {
		t.Fatal(err)
	}
	// Pin an old WAL snapshot, commit new inputs, then read all old inputs.
	oldTx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer oldTx.Rollback()
	old, err := readWorkloadArtifactTx(ctx, oldTx, r.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	r, err = publish(r)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := readWorkloadArtifactSnapshotTx(ctx, oldTx, r.OwnerID)
	if err != nil || pinned.Artifact.Generation != old.Generation || pinned.Function.EnvVars["marker"] != fmt.Sprint(old.Revision) ||
		len(pinned.Secrets) != 2 || pinned.Secrets[1].ValueEncrypted != fmt.Sprint(old.Revision) {
		t.Fatalf("snapshot mixed old generation/new inputs: %v", err)
	}
	oldTx.Rollback()
	done := make(chan error, 1)
	go func() {
		current := r
		for range 60 {
			current, err = publish(current)
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 120 {
		s := artifactTestSnapshot(t, db, r.OwnerID)
		marker := fmt.Sprint(s.Artifact.Revision)
		if s.Function.EnvVars["marker"] != marker || s.Function.CodeHash != marker || s.Function.RunEntrypoint != marker ||
			len(s.Secrets) != 2 || s.Secrets[0].ValueEncrypted != marker || s.Secrets[1].ValueEncrypted != marker {
			t.Fatal("concurrent snapshot mixed committed generations")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	before := artifactTestSnapshot(t, db, r.OwnerID)
	for range 5 {
		artifactTestUnchanged(t, db, before)
	}
}

func TestWorkloadArtifactTransactionHelperDoesNotCommit(t *testing.T) {
	ctx := context.Background()
	db, r := artifactTestSetup(t)
	before := artifactTestSnapshot(t, db, r.OwnerID)
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	next, err := rotateWorkloadArtifactTx(ctx, tx, artifactTestChange(r), "rotate")
	if err != nil || next.Generation == r.Generation {
		t.Fatalf("prepare rotation: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE functions SET code_hash='uncommitted' WHERE id=?`, r.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO function_secrets(function_id,key,value_encrypted) VALUES(?,?,?)`, r.OwnerID, "key", "uncommitted"); err != nil {
		t.Fatal(err)
	}
	artifactTestUnchanged(t, db, before)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	artifactTestUnchanged(t, db, before)
}

func TestWorkloadArtifactDeleteRecreateAndReopen(t *testing.T) {
	ctx := context.Background()
	db, r := artifactTestSetup(t)
	if _, err := db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "key", "ciphertext"); err != nil {
		t.Fatal(err)
	}
	// Delete/recreate the very same UUID to exercise the strongest ABA case.
	if err := db.DeleteFunction(ctx, r.OwnerID); err != nil {
		t.Fatal(err)
	}
	if n := iamTestCount(t, db, "workload_artifact_generations"); n != 0 {
		t.Fatal("generation did not cascade on deletion")
	}
	iamTestFunction(t, db, r.OwnerID, false)
	if s, err := db.GetWorkloadArtifactSnapshot(ctx, r.OwnerID); s != nil || !errors.Is(err, ErrWorkloadArtifactNotFound) {
		t.Fatalf("recreation inherited generation: %v", err)
	}
	recreated, err := db.NewWorkloadArtifact(ctx, r.OwnerID, iamTestActor)
	if err != nil || recreated.Generation == r.Generation || recreated.Revision != 1 {
		t.Fatalf("recreation reused identity: %v", err)
	}
	if _, err := db.RotateWorkloadArtifact(ctx, artifactTestChange(r)); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("old CAS survived recreation: %v", err)
	}
	if len(artifactTestSnapshot(t, db, r.OwnerID).Secrets) != 0 {
		t.Fatal("recreation inherited secrets")
	}

	path := filepath.Join(t.TempDir(), "reopen.db")
	durable, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := durable.Migrate(); err != nil {
		t.Fatal(err)
	}
	iamTestFunction(t, durable, iamTestOther, false)
	if err := durable.initWorkloadArtifactSchema(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := durable.NewWorkloadArtifact(ctx, iamTestOther, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	if err := durable.Close(); err != nil {
		t.Fatal(err)
	}
	durable, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer durable.Close()
	if err := durable.Migrate(); err != nil {
		t.Fatal(err)
	}
	if got := artifactTestSnapshot(t, durable, iamTestOther); got.Artifact != *stored {
		t.Fatal("normal reopen changed durable generation")
	}
}

func TestWorkloadArtifactLegacyAndPolicyUnchanged(t *testing.T) {
	ctx := context.Background()
	db, policy := iamTestSetup(t)
	fn, err := db.GetFunction(iamTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := db.ListIAMPolicyAudit(ctx, iamTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.initWorkloadArtifactSchema(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := db.NewWorkloadArtifact(ctx, iamTestOwner, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	r, err = db.UpsertWorkloadArtifactSecret(ctx, artifactTestChange(r), "key", "ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteWorkloadArtifactSecret(ctx, artifactTestChange(r), "key"); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetFunction(fn.ID)
	if err != nil || !reflect.DeepEqual(fn, got) {
		t.Fatal("new storage changed legacy function/SDK")
	}
	iamTestUnchanged(t, db, policy, audit)
	before := artifactTestSnapshot(t, db, iamTestOwner)
	if err := db.UpsertSecret(iamTestOwner, "legacy", "legacy-ciphertext"); err != nil {
		t.Fatal(err)
	}
	gotSnapshot := artifactTestSnapshot(t, db, iamTestOwner)
	if gotSnapshot.Artifact != before.Artifact {
		t.Fatal("legacy CRUD silently acquired generation semantics")
	}
	if err := db.DeleteSecret(iamTestOwner, "legacy"); err != nil {
		t.Fatal(err)
	}
	artifactTestUnchanged(t, db, before)
}
