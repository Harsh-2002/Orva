package secrets

import (
	"context"
	"crypto/cipher"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func preparedArtifactFixture(t *testing.T, schema bool) (*Manager, *sql.DB, *database.WorkloadArtifactRecord) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scratch.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	fn := &database.Function{
		ID: snapshotOwner, Name: "prepared-fixture", Runtime: "node",
		Entrypoint: "handler.js", Status: "created",
		EnvVars: map[string]string{"CONFIG": "original"},
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	manager := New(db, dir)
	if !schema {
		return manager, conn, nil
	}
	// Use canonical SQL without exporting or activating the private initializer.
	if _, err := conn.Exec(canonicalArtifactSchema(t)); err != nil {
		t.Fatal(err)
	}
	r, err := db.NewWorkloadArtifact(context.Background(), snapshotOwner, database.IAMActorReference{
		Kind: "system", ID: "018bcfe5-6800-7000-8000-000000000002",
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, conn, r
}

func canonicalArtifactSchema(t *testing.T) string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "database", "workload_artifact_schema.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var statements []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "initWorkloadArtifactSchema" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "ExecContext" {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Fatal("canonical schema SQL changed shape; update fixture extraction")
			}
			sql, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			statements = append(statements, sql)
			return true
		})
	}
	if len(statements) != 1 {
		t.Fatal("expected one canonical artifact schema statement")
	}
	return statements[0]
}

func preparedArtifactChange(r *database.WorkloadArtifactRecord) database.WorkloadArtifactChange {
	return database.WorkloadArtifactChange{
		OwnerID: r.OwnerID, ExpectedRevision: r.Revision, ExpectedEpoch: r.Epoch,
		ExpectedGeneration: r.Generation, Actor: r.Actor,
	}
}

func prepareArtifact(t *testing.T, m *Manager) *PreparedArtifactSnapshot {
	t.Helper()
	p, err := m.PrepareArtifactSnapshot(context.Background(), snapshotOwner, snapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPrepareArtifactSnapshotOwnershipAndRotation(t *testing.T) {
	m, _, r := preparedArtifactFixture(t, true)
	first := prepareArtifact(t, m)
	if first.Artifact != *r || first.Function.ID != snapshotOwner ||
		first.Function.EnvVars["CONFIG"] != "original" || first.Secrets == nil || len(first.Secrets) != 0 {
		t.Fatal("incomplete prepared snapshot")
	}
	if _, err := os.Stat(m.keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty preparation loaded a master key")
	}
	rotated, err := m.db.RotateWorkloadArtifact(context.Background(), preparedArtifactChange(r))
	if err != nil {
		t.Fatal(err)
	}
	second := prepareArtifact(t, m)
	if second.Artifact != *rotated || second.Artifact.Generation == first.Artifact.Generation ||
		!reflect.DeepEqual(second.Function, first.Function) {
		t.Fatal("rotation lost exact metadata or changed function")
	}
	encrypted, err := m.encrypt("private-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := m.db.UpsertWorkloadArtifactSecret(context.Background(), preparedArtifactChange(rotated), "PRIVATE_SELECTOR", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := m.db.GetWorkloadArtifactSnapshot(context.Background(), snapshotOwner)
	if err != nil {
		t.Fatal(err)
	}
	before := raw.Clone()
	third := prepareArtifact(t, m)
	if third.Artifact != *updated || third.Artifact.Generation == second.Artifact.Generation ||
		!reflect.DeepEqual(third.Secrets, map[string]string{"PRIVATE_SELECTOR": "private-plaintext"}) {
		t.Fatal("secret mutation and generation did not compose")
	}
	third.Function.EnvVars["CONFIG"] = "changed"
	third.Secrets["PRIVATE_SELECTOR"] = "changed"
	third.Artifact.Generation = "changed"
	raw.Function.EnvVars["CONFIG"] = "source-changed"
	raw.Secrets[0].ValueEncrypted = "source-changed"
	fourth := prepareArtifact(t, m)
	if fourth.Artifact != *updated || !reflect.DeepEqual(fourth.Function, before.Function) ||
		fourth.Secrets["PRIVATE_SELECTOR"] != "private-plaintext" ||
		second.Function.EnvVars["CONFIG"] != "original" || len(second.Secrets) != 0 {
		t.Fatal("result, source, durable state or prior preparation aliased")
	}
	after, err := m.db.GetWorkloadArtifactSnapshot(context.Background(), snapshotOwner)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("preparation mutated durable inputs")
	}
	deleted, err := m.db.DeleteWorkloadArtifactSecret(context.Background(), preparedArtifactChange(updated), "PRIVATE_SELECTOR")
	if err != nil {
		t.Fatal(err)
	}
	last := prepareArtifact(t, m)
	if last.Artifact != *deleted || len(last.Secrets) != 0 || fourth.Secrets["PRIVATE_SELECTOR"] != "private-plaintext" {
		t.Fatal("deletion changed an owned prior preparation")
	}
}

func TestPrepareArtifactSnapshotTransactionalComposition(t *testing.T) {
	m, conn, r := preparedArtifactFixture(t, true)
	ctx := context.Background()
	// Scratch-only publication fixture changes all three inputs in one commit.
	publish := func(n int) error {
		encrypted, err := m.encrypt(fmt.Sprint(n))
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE workload_artifact_generations SET revision=?,epoch=?,generation=?,change_kind='rotate' WHERE function_id=?`,
			n, n, ids.New(), r.OwnerID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE functions SET env_vars=? WHERE id=?`, fmt.Sprintf(`{"marker":"%d"}`, n), r.OwnerID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO function_secrets(function_id,key,value_encrypted) VALUES(?,?,?)
ON CONFLICT(function_id,key) DO UPDATE SET value_encrypted=excluded.value_encrypted`, r.OwnerID, "marker", encrypted); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := publish(1); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 2; i <= 30; i++ {
			if err := publish(i); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var inconsistent bool
	for range 60 {
		p, err := m.PrepareArtifactSnapshot(ctx, snapshotOwner, snapshotLimits())
		if err != nil {
			<-done
			t.Fatal(err)
		}
		marker := fmt.Sprint(p.Artifact.Revision)
		inconsistent = inconsistent || p.Artifact.Epoch != p.Artifact.Revision ||
			p.Function.EnvVars["marker"] != marker || p.Secrets["marker"] != marker
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if inconsistent {
		t.Fatal("prepared inputs mixed committed generations")
	}
}

func TestPrepareArtifactSnapshotMutationRollback(t *testing.T) {
	m, conn, r := preparedArtifactFixture(t, true)
	before := prepareArtifact(t, m)
	if _, err := conn.Exec(`CREATE TRIGGER reject_secret BEFORE INSERT ON function_secrets
BEGIN SELECT RAISE(ABORT,'private-trigger-diagnostic'); END`); err != nil {
		t.Fatal(err)
	}
	encrypted, err := m.encrypt("private-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	failed, err := m.db.UpsertWorkloadArtifactSecret(context.Background(), preparedArtifactChange(r), "PRIVATE_SELECTOR", encrypted)
	if failed != nil || err == nil {
		t.Fatal("failed secret write published a record")
	}
	after := prepareArtifact(t, m)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preparation exposed partially rolled-back mutation")
	}
}

func TestPrepareArtifactSnapshotFailsWithoutPartialResult(t *testing.T) {
	for _, kind := range []string{
		"schema absent", "metadata absent", "metadata corrupt", "invalid owner",
		"malformed env", "corrupt secret", "limits count", "limits bytes", "deleting", "closed",
	} {
		t.Run(kind, func(t *testing.T) {
			m, conn, _ := preparedArtifactFixture(t, kind != "schema absent")
			owner, limits := snapshotOwner, snapshotLimits()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := conn.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "metadata absent":
				exec(`DELETE FROM workload_artifact_generations`)
			case "metadata corrupt":
				exec(`UPDATE workload_artifact_generations SET generation=?`, strings.Repeat("x", 36))
			case "invalid owner":
				owner = "private-invalid-owner"
			case "malformed env":
				exec(`UPDATE functions SET env_vars=?`, `{"PRIVATE_SELECTOR":null}`)
			case "corrupt secret", "limits count", "limits bytes":
				encrypted, err := m.encrypt("private-plaintext")
				if err != nil {
					t.Fatal(err)
				}
				if err := m.db.UpsertSecret(snapshotOwner, "A_VALID", encrypted); err != nil {
					t.Fatal(err)
				}
				if kind == "corrupt secret" {
					if err := m.db.UpsertSecret(snapshotOwner, "Z_PRIVATE_SELECTOR", "private-ciphertext"); err != nil {
						t.Fatal(err)
					}
				} else if kind == "limits count" {
					limits.MaxSecrets = 0
				} else {
					limits.MaxTotalBytes = 1
				}
			case "deleting":
				exec(`UPDATE functions SET status='deleting'`)
			case "closed":
				if err := m.db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			p, err := m.PrepareArtifactSnapshot(context.Background(), owner, limits)
			if p != nil || err != errInvalidArtifactSnapshot {
				t.Fatal("failure exposed a partial result or non-generic diagnostic")
			}
		})
	}
	for _, m := range []*Manager{nil, New(nil, t.TempDir())} {
		if p, err := m.PrepareArtifactSnapshot(context.Background(), snapshotOwner, snapshotLimits()); p != nil || err != errInvalidArtifactSnapshot {
			t.Fatal("unavailable database did not fail closed")
		}
	}
}

type cancelAfterArtifactDecrypt struct {
	cipher.AEAD
	cancel context.CancelFunc
}

func (a cancelAfterArtifactDecrypt) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	value, err := a.AEAD.Open(dst, nonce, ciphertext, additionalData)
	a.cancel()
	return value, err
}

func TestPrepareArtifactSnapshotCancellation(t *testing.T) {
	m, _, _ := preparedArtifactFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := m.PrepareArtifactSnapshot(ctx, snapshotOwner, snapshotLimits()); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("already canceled preparation returned data")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	encrypted, err := m.encrypt("private-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.db.UpsertSecret(snapshotOwner, "PRIVATE_SELECTOR", encrypted); err != nil {
		t.Fatal(err)
	}
	m.aead = cancelAfterArtifactDecrypt{AEAD: m.aead, cancel: cancel}
	if p, err := m.PrepareArtifactSnapshot(ctx, snapshotOwner, snapshotLimits()); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation during decryption returned prepared data")
	}
}
