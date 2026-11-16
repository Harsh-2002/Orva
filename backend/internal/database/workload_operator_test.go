package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
	"modernc.org/sqlite"
)

func operatorPublishSetup(t *testing.T, kind string) (*Database, OperatorCredentialReference, WorkloadEnvironmentChange) {
	t.Helper()
	db, c := environmentPublishSetup(t)
	ref := OperatorCredentialReference{Kind: kind, ID: ids.New()}
	expiry := time.Now().UTC().Add(time.Hour)
	switch kind {
	case "api_key":
		if err := db.InsertAPIKey(&APIKey{ID: ref.ID, KeyHash: "private-test-key-hash",
			Prefix: "private-test-prefix", Name: "operator", Permissions: `["admin"]`}); err != nil {
			t.Fatal(err)
		}
	case "oauth":
		userID := sessionIdentityUser(t, db)
		if err := db.InsertOAuthClient(&OAuthClient{ID: ids.New(), ClientID: "operator-client",
			ClientName: "operator", RedirectURIs: "[]", GrantTypes: "[]", ResponseTypes: "[]",
			TokenEndpointAuthMethod: "none", Scope: "read invoke write admin openid email profile"}); err != nil {
			t.Fatal(err)
		}
		ref.ExpectedResource = "https://operator.invalid/mcp"
		if err := db.InsertOAuthAccessToken(&OAuthAccessToken{ID: ref.ID, AccessTokenHash: "private-test-access-hash",
			RefreshTokenHash: "private-test-refresh-hash", ClientID: "operator-client", UserID: userID,
			Scope: "admin openid", Resource: ref.ExpectedResource, AccessExpiresAt: expiry}); err != nil {
			t.Fatal(err)
		}
	case "session":
		userID := sessionIdentityUser(t, db)
		if err := db.initSessionIdentitySchema(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.write.Exec(`INSERT INTO sessions(id,token,user_id,expires_at) VALUES(?,?,?,?)`,
			ref.ID, "private-test-session-bearer", userID, expiry); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown fixture kind")
	}
	c.Actor = IAMActorReference{}
	return db, ref, c
}

func operatorExec(t *testing.T, db *Database, query string, args ...any) {
	t.Helper()
	if _, err := db.write.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func operatorAssertDenied(t *testing.T, db *Database, ref OperatorCredentialReference, c WorkloadEnvironmentChange) {
	t.Helper()
	before := artifactTestSnapshot(t, db, c.OwnerID)
	p, audit := environmentPublishPolicy(t, db)
	result, err := db.PublishWorkloadEnvironmentAsOperator(context.Background(), ref, c)
	if result != nil || !errors.Is(err, ErrOperatorDenied) || err.Error() != ErrOperatorDenied.Error() {
		t.Fatalf("expected generic denial without partial result: %v", err)
	}
	artifactTestUnchanged(t, db, before)
	iamTestUnchanged(t, db, p, audit)
}

func TestWorkloadOperatorPublication(t *testing.T) {
	for _, kind := range []string{"api_key", "oauth", "session"} {
		t.Run(kind, func(t *testing.T) {
			db, ref, c := operatorPublishSetup(t, kind)
			operatorExec(t, db, `UPDATE workload_iam_policies SET quarantined=1`)
			old, oldAudit := environmentPublishPolicy(t, db)
			c.Actor = IAMActorReference{Kind: ref.Kind, ID: ref.ID}
			result, err := db.PublishWorkloadEnvironmentAsOperator(context.Background(), ref, c)
			if err != nil {
				t.Fatal(err)
			}
			_, audit := environmentPublishPolicy(t, db)
			if result.Function.Version != c.ExpectedFunctionVersion+1 ||
				result.Artifact.Generation == c.ExpectedGeneration || result.Artifact.Revision != c.ExpectedRevision+1 ||
				result.Artifact.Actor != c.Actor || audit[0].Actor != c.Actor || len(audit) != len(oldAudit)+1 ||
				result.Policy.Revision != old.Revision+1 || result.Policy.AuthorityEpoch != old.AuthorityEpoch+1 ||
				!result.Policy.Quarantined || !result.Policy.ReviewRequired ||
				result.Policy.ApprovedArtifact != old.ApprovedArtifact || result.Policy.PolicyJSON != old.PolicyJSON ||
				result.Function.EnvVars["CONFIG"] != c.Env["CONFIG"] {
				t.Fatal("publication failed to preserve exact atomic deny-state semantics")
			}
		})
	}
}

func TestWorkloadOperatorAPIKeyDenials(t *testing.T) {
	for _, permissions := range []string{
		`[]`, `["read","write","invoke"]`, `null`, `{}`, `"admin"`, `["admin",null]`,
		`["admin","admin"]`, `["admin","unknown"]`, `["admin","openid"]`,
		`["admin",1]`, `["admin",{}]`, `["admin",`, `["admin"] null`, `["admin","\ud800"]`,
		"[\"admin\",\"\xff\"]", strings.Repeat(" ", 4097) + `["admin"]`,
	} {
		t.Run(fmt.Sprintf("permissions-%x", []byte(permissions[:min(len(permissions), 24)])), func(t *testing.T) {
			db, ref, c := operatorPublishSetup(t, "api_key")
			operatorExec(t, db, `UPDATE api_keys SET permissions=?`, permissions)
			operatorAssertDenied(t, db, ref, c)
		})
	}
	for _, query := range []string{
		`DELETE FROM api_keys`,
		`UPDATE api_keys SET expires_at='2000-01-01 00:00:00'`,
		`UPDATE api_keys SET expires_at='private-invalid-expiry'`,
		`UPDATE api_keys SET permissions=CAST(x'ff' AS TEXT)`,
		`DROP TABLE api_keys`,
	} {
		t.Run(query, func(t *testing.T) {
			db, ref, c := operatorPublishSetup(t, "api_key")
			operatorExec(t, db, query)
			operatorAssertDenied(t, db, ref, c)
		})
	}
}

func TestWorkloadOperatorOAuthDenials(t *testing.T) {
	for _, query := range []string{
		`DELETE FROM oauth_access_tokens`,
		`UPDATE oauth_access_tokens SET revoked_at=CURRENT_TIMESTAMP`,
		`UPDATE oauth_access_tokens SET access_expires_at='2000-01-01 00:00:00'`,
		`UPDATE oauth_access_tokens SET access_expires_at='private-bad-time'`,
		`UPDATE oauth_access_tokens SET user_id=999999`,
		`DELETE FROM users`,
		`DELETE FROM oauth_clients`,
		`UPDATE oauth_clients SET revoked_at=CURRENT_TIMESTAMP`,
		`UPDATE oauth_clients SET scope='read invoke write'`,
		`UPDATE oauth_clients SET scope='admin unknown'`,
		`UPDATE oauth_access_tokens SET scope='read invoke write'`,
		`UPDATE oauth_access_tokens SET scope='admin admin'`,
		`UPDATE oauth_access_tokens SET scope='admin unknown'`,
		`UPDATE oauth_access_tokens SET scope='admin' || char(9) || 'read'`,
		`UPDATE oauth_access_tokens SET scope=CAST(x'ff' AS TEXT)`,
		`UPDATE oauth_access_tokens SET resource=NULL`,
		`UPDATE oauth_access_tokens SET resource=''`,
		`UPDATE oauth_access_tokens SET resource='https://different.invalid/mcp'`,
		`DROP TABLE oauth_clients`,
	} {
		t.Run(query, func(t *testing.T) {
			db, ref, c := operatorPublishSetup(t, "oauth")
			operatorExec(t, db, query)
			operatorAssertDenied(t, db, ref, c)
		})
	}
	for _, column := range []string{"token scope", "client scope", "resource"} {
		t.Run("oversize "+column, func(t *testing.T) {
			db, ref, c := operatorPublishSetup(t, "oauth")
			query := `UPDATE oauth_access_tokens SET scope=?`
			if column == "client scope" {
				query = `UPDATE oauth_clients SET scope=?`
			} else if column == "resource" {
				query = `UPDATE oauth_access_tokens SET resource=?`
			}
			operatorExec(t, db, query, "admin "+strings.Repeat("x", 4096))
			operatorAssertDenied(t, db, ref, c)
		})
	}
}

func TestWorkloadOperatorSessionDenials(t *testing.T) {
	for _, query := range []string{
		`DELETE FROM sessions`,
		`DELETE FROM users`,
		`UPDATE sessions SET id=NULL`,
		`UPDATE sessions SET expires_at='2000-01-01 00:00:00'`,
		`UPDATE sessions SET expires_at='private-bad-time'`,
		`DROP TABLE sessions`,
	} {
		t.Run(query, func(t *testing.T) {
			db, ref, c := operatorPublishSetup(t, "session")
			operatorExec(t, db, query)
			operatorAssertDenied(t, db, ref, c)
		})
	}
	db, c := environmentPublishSetup(t)
	c.Actor = IAMActorReference{}
	operatorAssertDenied(t, db, OperatorCredentialReference{Kind: "session", ID: ids.New()}, c)
}

func TestWorkloadOperatorReferenceAndActor(t *testing.T) {
	db, ref, base := operatorPublishSetup(t, "api_key")
	for _, kind := range []string{"system", "channel", "", "function", "API_KEY"} {
		other := ref
		other.Kind = kind
		operatorAssertDenied(t, db, other, base)
	}
	for _, id := range []string{"", "private-token-selector", strings.ToUpper(ref.ID), "00000000-0000-0000-0000-000000000000", ids.New()} {
		other := ref
		other.ID = id
		operatorAssertDenied(t, db, other, base)
	}
	for _, actor := range []IAMActorReference{
		{Kind: "system", ID: ref.ID}, {Kind: ref.Kind, ID: ids.New()}, {Kind: ref.Kind},
	} {
		c := base
		c.Actor = actor
		operatorAssertDenied(t, db, ref, c)
	}
	ref.ExpectedResource = "unexpected"
	operatorAssertDenied(t, db, ref, base)
	db, ref, base = operatorPublishSetup(t, "oauth")
	for _, resource := range []string{"", "https://different.invalid/mcp", "\xff", "x\x00y", strings.Repeat("x", 4097)} {
		other := ref
		other.ExpectedResource = resource
		operatorAssertDenied(t, db, other, base)
	}
}

func TestWorkloadOperatorRollbackAndCAS(t *testing.T) {
	for _, failure := range []string{"artifact", "function", "policy", "audit", "commit", "CAS"} {
		t.Run(failure, func(t *testing.T) {
			db, ref, c := operatorPublishSetup(t, "api_key")
			switch failure {
			case "artifact", "function", "policy", "audit":
				table := map[string]string{"artifact": "workload_artifact_generations", "function": "functions",
					"policy": "workload_iam_policies", "audit": "workload_iam_audit"}[failure]
				op := "UPDATE"
				if failure == "audit" {
					op = "INSERT"
				}
				operatorExec(t, db, fmt.Sprintf(`CREATE TRIGGER operator_failure BEFORE %s ON %s
					BEGIN SELECT RAISE(ABORT,'private-test-bearer'); END`, op, table))
			case "commit":
				operatorExec(t, db, `CREATE TABLE operator_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
					CREATE TRIGGER operator_failure AFTER INSERT ON workload_iam_audit
					BEGIN INSERT INTO operator_guard VALUES('private-test-bearer'); END`)
			case "CAS":
				c.ExpectedEpoch++
			}
			before := artifactTestSnapshot(t, db, c.OwnerID)
			p, audit := environmentPublishPolicy(t, db)
			result, err := db.PublishWorkloadEnvironmentAsOperator(context.Background(), ref, c)
			want := ErrWorkloadArtifactStorage
			if failure == "CAS" {
				want = ErrWorkloadArtifactConflict
			}
			if result != nil || !errors.Is(err, want) || strings.Contains(err.Error(), "private") {
				t.Fatalf("failure leaked partial result/diagnostics: %v", err)
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, p, audit)
		})
	}
}

func operatorAwaitWriteQueue(t *testing.T, db *Database, before int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for db.write.Stats().WaitCount <= before {
		if time.Now().After(deadline) {
			t.Fatal("publication did not queue on the write connection")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkloadOperatorQueuedRevocation(t *testing.T) {
	for _, change := range []string{"delete", "permissions", "expiry", "oauth revoke", "oauth ceiling", "session logout"} {
		t.Run(change, func(t *testing.T) {
			kind := "api_key"
			if strings.HasPrefix(change, "oauth") {
				kind = "oauth"
			} else if change == "session logout" {
				kind = "session"
			}
			db, ref, c := operatorPublishSetup(t, kind)
			before := artifactTestSnapshot(t, db, c.OwnerID)
			p, audit := environmentPublishPolicy(t, db)
			var submittedPermissions string
			if kind == "api_key" {
				if err := db.read.QueryRow(`SELECT permissions FROM api_keys WHERE id=?`, ref.ID).Scan(&submittedPermissions); err != nil ||
					!operatorAdminPermissions(submittedPermissions, false) {
					t.Fatal("submission fixture was not initially admin")
				}
			}
			tx, err := db.write.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			query := map[string]string{
				"delete": `DELETE FROM api_keys`, "permissions": `UPDATE api_keys SET permissions='["write"]'`,
				"expiry":        `UPDATE api_keys SET expires_at=?`,
				"oauth revoke":  `UPDATE oauth_access_tokens SET revoked_at=CURRENT_TIMESTAMP`,
				"oauth ceiling": `UPDATE oauth_clients SET scope='write'`, "session logout": `DELETE FROM sessions`,
			}[change]
			var args []any
			expires := time.Now().UTC().Add(100 * time.Millisecond)
			if change == "expiry" {
				args = []any{expires}
			}
			if _, err := tx.Exec(query, args...); err != nil {
				t.Fatal(err)
			}
			waitCount := db.write.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				result, err := db.PublishWorkloadEnvironmentAsOperator(context.Background(), ref, c)
				if result != nil {
					done <- errors.New("partial publication")
					return
				}
				done <- err
			}()
			operatorAwaitWriteQueue(t, db, waitCount)
			if change == "expiry" {
				time.Sleep(max(time.Duration(0), time.Until(expires)+time.Millisecond))
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, ErrOperatorDenied) {
				t.Fatalf("stale submitted provenance authorized publication: %v", err)
			}
			if kind == "api_key" && !operatorAdminPermissions(submittedPermissions, false) {
				t.Fatal("fixture lost the stale submission-time admin snapshot")
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, p, audit)
		})
	}
}

func TestWorkloadOperatorMetadataBoundaries(t *testing.T) {
	for _, permissions := range []string{`["admin"]`, `["read","invoke","write","admin"]`,
		`["admin"]` + strings.Repeat(" ", maxOperatorMetadataBytes-len(`["admin"]`))} {
		if !operatorAdminPermissions(permissions, false) {
			t.Fatal("valid API-key permission metadata denied")
		}
	}
	for _, scope := range []string{"admin", "read invoke write admin openid email profile"} {
		if !operatorAdminPermissions(scope, true) {
			t.Fatal("valid OAuth metadata denied")
		}
	}
}

func TestWorkloadOperatorExpiryBeforeCommit(t *testing.T) {
	function := "test_operator_expiry_" + strings.ReplaceAll(ids.New(), "-", "")
	var expiry time.Time
	if err := sqlite.RegisterScalarFunction(function, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		time.Sleep(max(time.Duration(0), time.Until(expiry)+time.Millisecond))
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	db, ref, c := operatorPublishSetup(t, "api_key")
	expiry = time.Now().UTC().Add(100 * time.Millisecond)
	operatorExec(t, db, `UPDATE api_keys SET expires_at=?`, expiry)
	operatorExec(t, db, fmt.Sprintf(`CREATE TRIGGER operator_expire BEFORE UPDATE ON functions BEGIN SELECT %s(); END`, function))
	operatorAssertDenied(t, db, ref, c)
}

func TestWorkloadOperatorCancellationAndPublicationFirst(t *testing.T) {
	db, ref, c := operatorPublishSetup(t, "api_key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := db.PublishWorkloadEnvironmentAsOperator(ctx, ref, c)
	if result != nil || !errors.Is(err, ErrOperatorDenied) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publication did not deny: %v", err)
	}
	result, err = db.PublishWorkloadEnvironmentAsOperator(context.Background(), ref, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAPIKey(ref.ID); err != nil {
		t.Fatal(err)
	}
	got := artifactTestSnapshot(t, db, c.OwnerID)
	if got.Artifact != result.Artifact || got.Function.Version != result.Function.Version {
		t.Fatal("later credential revocation altered already committed publication")
	}
}
