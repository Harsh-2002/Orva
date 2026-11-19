package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
	"modernc.org/sqlite"
)

func operatorPolicySetup(t *testing.T, kind string) (*Database, OperatorCredentialReference, IAMPolicyChange) {
	t.Helper()
	db, ref, _ := operatorPublishSetup(t, kind)
	old, _ := environmentPublishPolicy(t, db)
	change := iamTestChange(old, iamTestEmpty)
	change.Actor = IAMActorReference{}
	return db, ref, change
}

func assertOperatorPolicyDenied(t *testing.T, db *Database, ref OperatorCredentialReference, change IAMPolicyChange) {
	t.Helper()
	old, audit := environmentPublishPolicy(t, db)
	result, err := db.SaveIAMPolicyAsOperator(context.Background(), ref, change)
	if result != nil || err != ErrOperatorDenied {
		t.Fatalf("expected generic operator denial, got result=%v error=%v", result != nil, err)
	}
	iamTestUnchanged(t, db, old, audit)
}

func TestIAMOperatorPolicySave(t *testing.T) {
	for _, kind := range []string{"api_key", "oauth", "session"} {
		t.Run(kind, func(t *testing.T) {
			db, ref, change := operatorPolicySetup(t, kind)
			old, audit := environmentPublishPolicy(t, db)
			artifact := artifactTestSnapshot(t, db, change.OwnerID)
			saved, err := db.SaveIAMPolicyAsOperator(context.Background(), ref, change)
			if err != nil {
				t.Fatal(err)
			}
			_, history := environmentPublishPolicy(t, db)
			if saved.Revision != old.Revision+1 || saved.AuthorityEpoch != old.AuthorityEpoch+1 ||
				saved.PolicyJSON != iamTestEmpty || len(history) != len(audit)+1 ||
				history[0].Actor != (IAMActorReference{Kind: ref.Kind, ID: ref.ID}) {
				t.Fatal("save lost revision, epoch, policy or derived actor")
			}
			artifactTestUnchanged(t, db, artifact)
			if result, err := db.SaveIAMPolicyAsOperator(context.Background(), ref, change); result != nil || err != ErrIAMConflict {
				t.Fatalf("stale policy save: %v", err)
			}
			iamTestUnchanged(t, db, saved, history)
		})
	}
}

func TestIAMOperatorPolicyDenials(t *testing.T) {
	cases := []struct{ name, kind, query string }{
		{"write-only", "api_key", `UPDATE api_keys SET permissions='["read","write","invoke"]'`},
		{"deleted-key", "api_key", `DELETE FROM api_keys`},
		{"expired-key", "api_key", `UPDATE api_keys SET expires_at='2000-01-01 00:00:00'`},
		{"malformed-permissions", "api_key", `UPDATE api_keys SET permissions='["admin",null]'`},
		{"revoked-oauth", "oauth", `UPDATE oauth_access_tokens SET revoked_at=CURRENT_TIMESTAMP`},
		{"revoked-client", "oauth", `UPDATE oauth_clients SET revoked_at=CURRENT_TIMESTAMP`},
		{"client-scope", "oauth", `UPDATE oauth_clients SET scope='read write'`},
		{"audience", "oauth", `UPDATE oauth_access_tokens SET resource='https://other.invalid/mcp'`},
		{"logout", "session", `DELETE FROM sessions`},
		{"missing-user", "session", `DELETE FROM users`},
		{"expired-session", "session", `UPDATE sessions SET expires_at='2000-01-01 00:00:00'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, ref, change := operatorPolicySetup(t, tc.kind)
			operatorExec(t, db, tc.query)
			assertOperatorPolicyDenied(t, db, ref, change)
		})
	}
	for _, kind := range []string{"channel", "system", "", "session"} {
		t.Run("untrusted-reference-"+kind, func(t *testing.T) {
			db, ref, change := operatorPolicySetup(t, "api_key")
			ref.Kind = kind
			assertOperatorPolicyDenied(t, db, ref, change)
		})
	}
	t.Run("forged-actor", func(t *testing.T) {
		db, ref, change := operatorPolicySetup(t, "api_key")
		change.Actor = iamTestActor
		assertOperatorPolicyDenied(t, db, ref, change)
	})
}

func TestIAMOperatorPolicyQueuedRevocation(t *testing.T) {
	for _, kind := range []string{"api_key", "oauth", "session"} {
		t.Run(kind, func(t *testing.T) {
			db, ref, change := operatorPolicySetup(t, kind)
			old, audit := environmentPublishPolicy(t, db)
			tx, err := db.write.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			query := map[string]string{
				"api_key": `UPDATE api_keys SET permissions='["write"]'`,
				"oauth":   `UPDATE oauth_clients SET revoked_at=CURRENT_TIMESTAMP`,
				"session": `DELETE FROM sessions`,
			}[kind]
			if _, err := tx.Exec(query); err != nil {
				t.Fatal(err)
			}
			before := db.write.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				result, err := db.SaveIAMPolicyAsOperator(context.Background(), ref, change)
				if result != nil {
					done <- errors.New("partial result from revoked save")
					return
				}
				done <- err
			}()
			operatorAwaitWriteQueue(t, db, before)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != ErrOperatorDenied {
				t.Fatalf("queued stale authority: %v", err)
			}
			iamTestUnchanged(t, db, old, audit)
		})
	}
}

func TestIAMOperatorPolicyAtomicity(t *testing.T) {
	for _, failure := range []string{"audit", "revoke-during-save", "cancel-during-save", "expire-during-save"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			name := "test_policy_" + strings.ReplaceAll(ids.New(), "-", "")
			var expiry time.Time
			if failure == "cancel-during-save" || failure == "expire-during-save" {
				if err := sqlite.RegisterScalarFunction(name, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
					if failure == "cancel-during-save" {
						cancel()
					} else {
						time.Sleep(max(time.Duration(0), time.Until(expiry)+time.Millisecond))
					}
					return nil, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			db, ref, change := operatorPolicySetup(t, "api_key")
			old, audit := environmentPublishPolicy(t, db)
			switch failure {
			case "audit":
				operatorExec(t, db, `CREATE TRIGGER policy_fail BEFORE INSERT ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'private-diagnostic'); END`)
			case "revoke-during-save":
				operatorExec(t, db, `CREATE TRIGGER policy_revoke AFTER UPDATE ON workload_iam_policies BEGIN DELETE FROM api_keys; END`)
			default:
				expiry = time.Now().UTC().Add(100 * time.Millisecond)
				if failure == "expire-during-save" {
					operatorExec(t, db, `UPDATE api_keys SET expires_at=?`, expiry)
				}
				operatorExec(t, db, fmt.Sprintf(`CREATE TRIGGER policy_interrupt BEFORE UPDATE ON workload_iam_policies BEGIN SELECT %s(); END`, name))
			}
			result, err := db.SaveIAMPolicyAsOperator(ctx, ref, change)
			if result != nil || err == nil || strings.Contains(err.Error(), "private-diagnostic") {
				t.Fatalf("failed transaction leaked result/diagnostic: %v", err)
			}
			if failure != "audit" && !errors.Is(err, ErrOperatorDenied) {
				t.Fatalf("revocation/expiry/cancellation lost denial sentinel: %v", err)
			}
			if failure == "cancel-during-save" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
			iamTestUnchanged(t, db, old, audit)
		})
	}
}

func TestIAMOperatorPolicyConcurrentCAS(t *testing.T) {
	db, ref, change := operatorPolicySetup(t, "api_key")
	old, audit := environmentPublishPolicy(t, db)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range cap(results) {
		wg.Go(func() {
			_, err := db.SaveIAMPolicyAsOperator(context.Background(), ref, change)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if err != ErrIAMConflict {
			t.Fatalf("unexpected concurrent save: %v", err)
		}
	}
	saved, history := environmentPublishPolicy(t, db)
	if success != 1 || saved.Revision != old.Revision+1 || saved.AuthorityEpoch != old.AuthorityEpoch+1 || len(history) != len(audit)+1 {
		t.Fatal("CAS did not have exactly one policy/audit winner")
	}
}
