package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

const iamTestOwner = "019a8000-0000-7000-8000-000000000001"
const iamTestOther = "019a8000-0000-7000-8000-000000000002"
const iamTestActorID = "019a8000-0000-7000-8000-000000000003"
const iamTestEmpty = `{"schema_version":1,"allow":[],"inbound":[]}`
const iamTestGrant = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"private-key-selector"}},"context":"execution"}],"inbound":[]}`

var iamTestActor = IAMActorReference{Kind: "api_key", ID: iamTestActorID}

func iamTestFunction(t *testing.T, db *Database, id string, sdk bool) *Function {
	t.Helper()
	f := &Function{ID: id, Name: "iam-" + id, Runtime: "node", Entrypoint: "handler.js", SDK: sdk, Status: "created", EnvVars: map[string]string{"PRIVATE": "not-an-audit-value"}}
	if err := db.InsertFunction(f); err != nil {
		t.Fatal(err)
	}
	return f
}

func iamTestSetup(t *testing.T) (*Database, *IAMPolicyRecord) {
	t.Helper()
	db := newTestDB(t)
	iamTestFunction(t, db, iamTestOwner, false)
	if err := db.initWorkloadIAMSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, err := db.NewIAMPolicy(context.Background(), iamTestOwner, false, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	return db, r
}

func iamTestChange(r *IAMPolicyRecord, policy string) IAMPolicyChange {
	return IAMPolicyChange{OwnerID: r.OwnerID, ExpectedRevision: r.Revision, ExpectedApprovedArtifact: r.ApprovedArtifact, ApprovedArtifact: "host-config-artifact-v1", PolicyJSON: []byte(policy), Actor: iamTestActor}
}

func iamTestCount(t *testing.T, db *Database, table string) int {
	t.Helper()
	var n int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func iamTestUnchanged(t *testing.T, db *Database, want *IAMPolicyRecord, audit []IAMPolicyAudit) {
	t.Helper()
	got, err := db.GetIAMPolicy(context.Background(), want.OwnerID)
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("policy changed: got %+v, error %v", got, err)
	}
	a, err := db.ListIAMPolicyAudit(context.Background(), want.OwnerID)
	if err != nil || !reflect.DeepEqual(a, audit) {
		t.Fatalf("audit changed: got %+v, error %v", a, err)
	}
}

func TestWorkloadIAMExplicitSchemaOnly(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	assertAbsent := func() {
		t.Helper()
		var n int
		if err := db.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'workload_iam_%'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("normal New/Migrate activated IAM schema: %d, %v", n, err)
		}
	}
	assertAbsent()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	assertAbsent()
	f := iamTestFunction(t, db, iamTestOwner, true)
	before, _ := json.Marshal(f)
	if err := db.InitIAMSchemaForFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.InitIAMSchemaForFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if n := iamTestCount(t, db, "workload_iam_policies"); n != 0 {
		t.Fatalf("initializer created policies: %d", n)
	}
	got, err := db.GetFunction(f.ID)
	after, _ := json.Marshal(got)
	if err != nil || string(before) != string(after) {
		t.Fatalf("initializer modified function: %v", err)
	}
	if n := iamTestCount(t, db, "system_config"); n != 18 {
		t.Fatalf("initializer modified migration/config markers: %d", n)
	}
	if _, err := db.NewIAMPolicy(ctx, f.ID, true, iamTestActor); err != nil {
		t.Fatal(err)
	}
	if err := db.InitIAMSchemaForFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if n := iamTestCount(t, db, "workload_iam_policies"); n != 1 {
		t.Fatalf("repeat initializer changed rows: %d", n)
	}
}

func TestWorkloadIAMNewReadSave(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	iamTestFunction(t, db, iamTestOwner, false)
	if err := db.initWorkloadIAMSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetIAMPolicy(ctx, iamTestOwner); !errors.Is(err, ErrIAMPolicyNotFound) {
		t.Fatalf("missing policy: %v", err)
	}
	if _, err := db.NewIAMPolicy(ctx, iamTestOther, false, iamTestActor); !errors.Is(err, ErrIAMFunctionNotFound) {
		t.Fatalf("missing function: %v", err)
	}
	r, err := db.NewIAMPolicy(ctx, iamTestOwner, false, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 1 || r.AuthorityEpoch != 1 || r.ApprovedArtifact != IAMUnboundArtifact || r.ReviewRequired || r.Quarantined || r.PolicyJSON != iamTestEmpty || r.CreatedAt.IsZero() || !r.CreatedAt.Equal(r.UpdatedAt) {
		t.Fatalf("unexpected initial policy: %+v", r)
	}
	if _, err := db.NewIAMPolicy(ctx, iamTestOwner, true, iamTestActor); !errors.Is(err, ErrIAMConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
	change := iamTestChange(r, " \n"+iamTestGrant+" \t")
	change.ReviewRequired, change.Quarantined = true, true
	saved, err := db.SaveIAMPolicy(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 2 || saved.AuthorityEpoch != 2 || saved.PolicyJSON != iamTestGrant || !saved.ReviewRequired || !saved.Quarantined || !saved.CreatedAt.Equal(r.CreatedAt) || saved.UpdatedAt.Before(r.UpdatedAt) {
		t.Fatalf("unexpected saved policy: %+v", saved)
	}
	a, err := db.ListIAMPolicyAudit(ctx, iamTestOwner)
	if err != nil || len(a) != 2 || a[0].Actor != iamTestActor || a[0].Revision != 2 || a[0].AuthorityEpoch != 2 {
		t.Fatalf("audit: %+v, %v", a, err)
	}
	bytes, _ := json.Marshal(saved)
	digest := sha256.Sum256(bytes)
	if a[0].Digest != hex.EncodeToString(digest[:]) {
		t.Fatal("audit digest does not bind the committed canonical state")
	}
	iamTestUnchanged(t, db, saved, a)
	if _, err := db.SaveIAMPolicy(ctx, change); !errors.Is(err, ErrIAMConflict) {
		t.Fatalf("stale write: %v", err)
	}
	change = iamTestChange(saved, iamTestEmpty)
	change.ExpectedApprovedArtifact = "different-stored-approval"
	if _, err := db.SaveIAMPolicy(ctx, change); !errors.Is(err, ErrIAMConflict) {
		t.Fatalf("stored approval conflict: %v", err)
	}
	iamTestUnchanged(t, db, saved, a)
}

func TestWorkloadIAMConcurrentCAS(t *testing.T) {
	db, r := iamTestSetup(t)
	const contenders = 16
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := db.SaveIAMPolicy(context.Background(), iamTestChange(r, iamTestGrant))
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrIAMConflict) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if winners != 1 || iamTestCount(t, db, "workload_iam_audit") != 2 {
		t.Fatalf("CAS winners: %d", winners)
	}
}

func TestWorkloadIAMRejectsWithoutMutation(t *testing.T) {
	db, r := iamTestSetup(t)
	a, err := db.ListIAMPolicyAudit(context.Background(), r.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*IAMPolicyChange){
		"malformed": func(c *IAMPolicyChange) { c.PolicyJSON = []byte(`{`) },
		"unknown field": func(c *IAMPolicyChange) {
			c.PolicyJSON = []byte(`{"schema_version":1,"allow":[],"inbound":[],"admin":true}`)
		},
		"duplicate field": func(c *IAMPolicyChange) {
			c.PolicyJSON = []byte(`{"schema_version":1,"allow":[],"inbound":[],"allow":[]}`)
		},
		"null":                   func(c *IAMPolicyChange) { c.PolicyJSON = []byte(`{"schema_version":1,"allow":null,"inbound":[]}`) },
		"owner name":             func(c *IAMPolicyChange) { c.OwnerID = "function-name" },
		"owner uppercase":        func(c *IAMPolicyChange) { c.OwnerID = "019A8000-0000-7000-8000-000000000001" },
		"owner compact":          func(c *IAMPolicyChange) { c.OwnerID = "019a8000000070008000000000000001" },
		"owner nil":              func(c *IAMPolicyChange) { c.OwnerID = "00000000-0000-0000-0000-000000000000" },
		"owner bad version":      func(c *IAMPolicyChange) { c.OwnerID = "019a8000-0000-0000-8000-000000000001" },
		"revision overflow":      func(c *IAMPolicyChange) { c.ExpectedRevision = uint64(math.MaxInt64) + 1 },
		"revision uint overflow": func(c *IAMPolicyChange) { c.ExpectedRevision = math.MaxUint64 },
		"revision zero":          func(c *IAMPolicyChange) { c.ExpectedRevision = 0 },
		"missing approval":       func(c *IAMPolicyChange) { c.ApprovedArtifact = "" },
		"unbound grants":         func(c *IAMPolicyChange) { c.ApprovedArtifact = IAMUnboundArtifact },
		"unbound inbound only": func(c *IAMPolicyChange) {
			c.ApprovedArtifact = IAMUnboundArtifact
			c.PolicyJSON = []byte(fmt.Sprintf(`{"schema_version":1,"allow":[],"inbound":[{"caller_id":%q,"triggers":["f2f"]}]}`, iamTestOther))
		},
		"long approval":     func(c *IAMPolicyChange) { c.ApprovedArtifact = strings.Repeat("x", 257) },
		"bad approval UTF8": func(c *IAMPolicyChange) { c.ApprovedArtifact = string([]byte{255}) },
		"actor token":       func(c *IAMPolicyChange) { c.Actor.ID = "Bearer secret-credential" },
		"actor kind":        func(c *IAMPolicyChange) { c.Actor.Kind = "admin" },
		"foreign self cron": func(c *IAMPolicyChange) {
			c.PolicyJSON = []byte(fmt.Sprintf(`{"schema_version":1,"allow":[{"actions":["crons.upsert"],"resource":{"own_cron":{"name":"daily","target_id":%q}},"context":"execution"}],"inbound":[]}`, iamTestOther))
		},
		"foreign cron acceptance": func(c *IAMPolicyChange) {
			c.PolicyJSON = []byte(fmt.Sprintf(`{"schema_version":1,"allow":[],"inbound":[{"caller_id":%q,"triggers":["cron"]}]}`, iamTestOther))
		},
		"oversize": func(c *IAMPolicyChange) { c.PolicyJSON = []byte(strings.Repeat(" ", workloadiam.MaxPolicyBytes+1)) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := iamTestChange(r, iamTestGrant)
			mutate(&c)
			if _, err := db.SaveIAMPolicy(context.Background(), c); !errors.Is(err, ErrIAMInvalidPolicy) {
				t.Fatalf("invalid save: %v", err)
			}
			iamTestUnchanged(t, db, r, a)
		})
	}
	for _, id := range []string{"bad", strings.ToUpper(iamTestOwner), "00000000-0000-0000-0000-000000000000"} {
		if _, err := db.NewIAMPolicy(context.Background(), id, false, iamTestActor); !errors.Is(err, ErrIAMInvalidPolicy) {
			t.Fatalf("invalid new owner %q: %v", id, err)
		}
		if _, err := db.GetIAMPolicy(context.Background(), id); !errors.Is(err, ErrIAMInvalidPolicy) {
			t.Fatalf("invalid read owner %q: %v", id, err)
		}
	}
	if _, err := db.NewIAMPolicy(context.Background(), iamTestOther, false, IAMActorReference{}); !errors.Is(err, ErrIAMInvalidPolicy) {
		t.Fatalf("invalid create actor: %v", err)
	}
	missing := iamTestChange(r, iamTestEmpty)
	missing.OwnerID = iamTestOther
	if _, err := db.SaveIAMPolicy(context.Background(), missing); !errors.Is(err, ErrIAMFunctionNotFound) {
		t.Fatalf("save missing function: %v", err)
	}
	iamTestFunction(t, db, iamTestOther, false)
	if _, err := db.SaveIAMPolicy(context.Background(), missing); !errors.Is(err, ErrIAMPolicyNotFound) {
		t.Fatalf("save absent policy: %v", err)
	}
	iamTestUnchanged(t, db, r, a)
}

func TestWorkloadIAMCounterBoundaries(t *testing.T) {
	for _, counter := range []string{"revision", "authority_epoch"} {
		t.Run(counter, func(t *testing.T) {
			db, r := iamTestSetup(t)
			if _, err := db.write.Exec(`UPDATE workload_iam_policies SET `+counter+`=? WHERE function_id=?`, int64(math.MaxInt64)-1, r.OwnerID); err != nil {
				t.Fatal(err)
			}
			r, err := db.GetIAMPolicy(context.Background(), r.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			r, err = db.SaveIAMPolicy(context.Background(), iamTestChange(r, iamTestEmpty))
			if err != nil {
				t.Fatal(err)
			}
			if counter == "revision" && r.Revision != math.MaxInt64 || counter == "authority_epoch" && r.AuthorityEpoch != math.MaxInt64 {
				t.Fatalf("counter truncated: %+v", r)
			}
			a, err := db.ListIAMPolicyAudit(context.Background(), r.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.SaveIAMPolicy(context.Background(), iamTestChange(r, iamTestGrant)); !errors.Is(err, ErrIAMRevisionExhausted) {
				t.Fatalf("counter exhaustion: %v", err)
			}
			iamTestUnchanged(t, db, r, a)
			if _, err := db.write.Exec(`UPDATE workload_iam_policies SET `+counter+`=9223372036854775808 WHERE function_id=?`, r.OwnerID); err == nil {
				t.Fatal("schema allowed integer-to-REAL counter overflow")
			}
		})
	}
}

func TestWorkloadIAMEagerValidationAndCancellation(t *testing.T) {
	db, r := iamTestSetup(t)
	a, err := db.ListIAMPolicyAudit(context.Background(), r.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	// Occupy the only write connection: invalid input must never wait for it.
	conn, err := db.write.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	change := iamTestChange(r, `{"unknown":true}`)
	if _, err := db.SaveIAMPolicy(ctx, change); !errors.Is(err, ErrIAMInvalidPolicy) {
		t.Fatalf("validation waited for write connection: %v", err)
	}
	change = iamTestChange(r, iamTestGrant)
	change.ExpectedRevision = math.MaxInt64
	if _, err := db.SaveIAMPolicy(ctx, change); !errors.Is(err, ErrIAMRevisionExhausted) {
		t.Fatalf("exhausted revision waited for write connection: %v", err)
	}
	if _, err := db.NewIAMPolicy(ctx, "invalid-owner", false, iamTestActor); !errors.Is(err, ErrIAMInvalidPolicy) {
		t.Fatalf("invalid create waited for write connection: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := db.SaveIAMPolicy(cancelled, iamTestChange(r, iamTestGrant)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	iamTestUnchanged(t, db, r, a)
}

func TestWorkloadIAMInboundAndMetadataRevisions(t *testing.T) {
	db, r := iamTestSetup(t)
	ctx := context.Background()
	inbound := fmt.Sprintf(`{"schema_version":1,"allow":[],"inbound":[{"caller_id":%q,"triggers":["f2f","job"]}]}`, iamTestOther)
	change := iamTestChange(r, inbound)
	change.Actor = IAMActorReference{Kind: "session", ID: iamTestActorID}
	r, err := db.SaveIAMPolicy(ctx, change)
	if err != nil || r.PolicyJSON != inbound || r.Revision != 2 || r.AuthorityEpoch != 2 {
		t.Fatalf("explicit inbound-only approval: %+v, %v", r, err)
	}
	change = iamTestChange(r, inbound)
	change.ApprovedArtifact = "host-config-artifact-v2"
	change.ReviewRequired, change.Quarantined = true, true
	change.Actor = IAMActorReference{Kind: "oauth", ID: iamTestActorID}
	r, err = db.SaveIAMPolicy(ctx, change)
	if err != nil || r.Revision != 3 || r.AuthorityEpoch != 3 || !r.ReviewRequired || !r.Quarantined || r.ApprovedArtifact != change.ApprovedArtifact {
		t.Fatalf("combined metadata revision: %+v, %v", r, err)
	}
	a, err := db.ListIAMPolicyAudit(ctx, r.OwnerID)
	if err != nil || len(a) != 3 || a[0].Actor.Kind != "oauth" || a[1].Actor.Kind != "session" {
		t.Fatalf("actor references: %+v, %v", a, err)
	}
	iamTestUnchanged(t, db, r, a)
}

func TestWorkloadIAMInvalidStoredPolicyFailsClosed(t *testing.T) {
	db, r := iamTestSetup(t)
	ctx := context.Background()
	a, err := db.ListIAMPolicyAudit(ctx, r.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"unknown":true}`, strings.Replace(iamTestEmpty, "[]", "null", 1)} {
		if _, err := db.write.Exec(`UPDATE workload_iam_policies SET policy_json=? WHERE function_id=?`, bad, r.OwnerID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.GetIAMPolicy(ctx, r.OwnerID); !errors.Is(err, ErrIAMInvalidPolicy) {
			t.Fatalf("invalid stored policy read: %v", err)
		}
		if _, err := db.SaveIAMPolicy(ctx, iamTestChange(r, iamTestGrant)); !errors.Is(err, ErrIAMInvalidPolicy) {
			t.Fatalf("invalid stored policy overwritten: %v", err)
		}
		var stored string
		if err := db.read.QueryRow(`SELECT policy_json FROM workload_iam_policies WHERE function_id=?`, r.OwnerID).Scan(&stored); err != nil || stored != bad {
			t.Fatalf("invalid store changed: %q, %v", stored, err)
		}
		if _, err := db.write.Exec(`UPDATE workload_iam_policies SET policy_json=? WHERE function_id=?`, r.PolicyJSON, r.OwnerID); err != nil {
			t.Fatal(err)
		}
		iamTestUnchanged(t, db, r, a)
	}
}

func TestWorkloadIAMAuditAtomicityRetention(t *testing.T) {
	ctx := context.Background()
	db, r := iamTestSetup(t)
	a, err := db.ListIAMPolicyAudit(ctx, r.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`CREATE TRIGGER iam_test_fail_audit BEFORE INSERT ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'test audit failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveIAMPolicy(ctx, iamTestChange(r, iamTestGrant)); err == nil {
		t.Fatal("save succeeded without audit")
	}
	iamTestUnchanged(t, db, r, a)
	iamTestFunction(t, db, iamTestOther, false)
	if _, err := db.NewIAMPolicy(ctx, iamTestOther, false, iamTestActor); err == nil {
		t.Fatal("create succeeded without audit")
	}
	if _, err := db.GetIAMPolicy(ctx, iamTestOther); !errors.Is(err, ErrIAMPolicyNotFound) {
		t.Fatalf("failed creation left a policy: %v", err)
	}
	if _, err := db.write.Exec(`DROP TRIGGER iam_test_fail_audit`); err != nil {
		t.Fatal(err)
	}
	for range IAMAuditRetention + 5 {
		r, err = db.SaveIAMPolicy(ctx, iamTestChange(r, iamTestGrant))
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err = db.ListIAMPolicyAudit(ctx, r.OwnerID)
	if err != nil || len(a) != IAMAuditRetention || a[0].Revision != r.Revision || a[len(a)-1].Revision != r.Revision-IAMAuditRetention+1 || iamTestCount(t, db, "workload_iam_audit") != IAMAuditRetention {
		t.Fatalf("audit retention: %+v, %v", a, err)
	}
	previous := append([]IAMPolicyAudit(nil), a...)
	if _, err := db.write.Exec(`UPDATE workload_iam_audit SET actor_id=? WHERE function_id=?`, iamTestOther, r.OwnerID); err == nil {
		t.Fatal("committed audit metadata could be rewritten")
	}
	iamTestUnchanged(t, db, r, previous)
	encoded, _ := json.Marshal(a)
	for _, forbidden := range []string{"private-key-selector", "not-an-audit-value", iamTestGrant, r.ApprovedArtifact} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("raw policy/secret/artifact in audit: %q", forbidden)
		}
	}
	if _, err := db.write.Exec(`CREATE TRIGGER iam_test_fail_trim BEFORE DELETE ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'test retention failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveIAMPolicy(ctx, iamTestChange(r, iamTestEmpty)); err == nil {
		t.Fatal("save succeeded despite retention failure")
	}
	iamTestUnchanged(t, db, r, previous)
}

func TestWorkloadIAMRevokeReopen(t *testing.T) {
	ctx := context.Background()
	db, r := iamTestSetup(t)
	path := db.Path()
	r, err := db.SaveIAMPolicy(ctx, iamTestChange(r, iamTestGrant))
	if err != nil {
		t.Fatal(err)
	}
	change := iamTestChange(r, iamTestEmpty)
	change.ApprovedArtifact = IAMUnboundArtifact
	r, err = db.SaveIAMPolicy(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.ListIAMPolicyAudit(ctx, r.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	if err := reopened.initWorkloadIAMSchema(ctx); err != nil {
		t.Fatal(err)
	}
	iamTestUnchanged(t, reopened, r, a)
	if r.PolicyJSON != iamTestEmpty || r.Revision != 3 || r.AuthorityEpoch != 3 || r.ApprovedArtifact != IAMUnboundArtifact {
		t.Fatalf("revocation lost: %+v", r)
	}
}

func TestWorkloadIAMForeignKeysAndDeletion(t *testing.T) {
	ctx := context.Background()
	db, r := iamTestSetup(t)
	if _, err := db.write.Exec(`INSERT INTO workload_iam_policies SELECT ?,revision,authority_epoch,approved_artifact,review_required,quarantined,policy_json,created_at,updated_at FROM workload_iam_policies WHERE function_id=?`, iamTestOther, r.OwnerID); err == nil {
		t.Fatal("orphan current policy accepted")
	}
	if _, err := db.write.Exec(`INSERT INTO workload_iam_audit SELECT ?,revision,authority_epoch,digest,actor_kind,actor_id,changed_at FROM workload_iam_audit WHERE function_id=?`, iamTestOther, r.OwnerID); err == nil {
		t.Fatal("orphan audit accepted")
	}
	if err := db.DeleteFunction(ctx, r.OwnerID); err != nil {
		t.Fatal(err)
	}
	if iamTestCount(t, db, "workload_iam_policies") != 0 || iamTestCount(t, db, "workload_iam_audit") != 0 {
		t.Fatal("function deletion left IAM state")
	}
	if _, err := db.GetIAMPolicy(ctx, r.OwnerID); !errors.Is(err, ErrIAMFunctionNotFound) {
		t.Fatalf("deleted function read: %v", err)
	}
	if _, err := db.SaveIAMPolicy(ctx, iamTestChange(r, iamTestGrant)); !errors.Is(err, ErrIAMFunctionNotFound) {
		t.Fatalf("deleted function save: %v", err)
	}
	iamTestFunction(t, db, iamTestOther, false)
	if _, err := db.NewIAMPolicy(ctx, iamTestOther, false, iamTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`DELETE FROM workload_iam_policies WHERE function_id=?`, iamTestOther); err != nil {
		t.Fatal(err)
	}
	if iamTestCount(t, db, "workload_iam_audit") != 0 {
		t.Fatal("current policy deletion left audit state")
	}
	if _, err := db.write.Exec(`UPDATE functions SET status=? WHERE id=?`, FunctionStatusDeleting, iamTestOther); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewIAMPolicy(ctx, iamTestOther, false, iamTestActor); !errors.Is(err, ErrFunctionDeleting) {
		t.Fatalf("deleting function acquired policy: %v", err)
	}
	var table string
	var rowid sql.NullInt64
	var parent string
	var fk int
	if err := db.read.QueryRow(`PRAGMA foreign_key_check`).Scan(&table, &rowid, &parent, &fk); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign key violation: %v", err)
	}
}

func TestWorkloadIAMLegacyPreflightAndSnapshotIsolation(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	changes := func() int64 {
		t.Helper()
		var n int64
		if err := db.write.QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := changes()
	entries, err := db.IAMLegacySDKPreflight(ctx)
	if err != nil || len(entries) != 0 || changes() != before {
		t.Fatalf("fresh preflight mutated DB: %+v, %v", entries, err)
	}
	for i, id := range []string{iamTestOwner, iamTestOther} {
		fn := iamTestFunction(t, db, id, i == 0)
		snap := SnapshotFromFunction(fn)
		d := &Deployment{ID: fmt.Sprintf("iam-deploy-%d", i), FunctionID: id, Version: 1, Status: "succeeded"}
		if err := db.InsertDeployment(d); err != nil {
			t.Fatal(err)
		}
		if err := db.SetDeploymentSnapshot(d.ID, snap); err != nil {
			t.Fatal(err)
		}
	}
	before = changes()
	entries, err = db.IAMLegacySDKPreflight(ctx)
	if err != nil || len(entries) != 2 || !entries[0].SDK || !entries[0].ReviewRequired || entries[1].SDK || entries[1].ReviewRequired || changes() != before {
		t.Fatalf("legacy preflight: %+v, %v", entries, err)
	}
	var schemaCount int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'workload_iam_%'`).Scan(&schemaCount); err != nil || schemaCount != 0 {
		t.Fatalf("preflight activated IAM schema: %d, %v", schemaCount, err)
	}
	if err := db.initWorkloadIAMSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{iamTestOwner, iamTestOther} {
		fnBefore, err := db.GetFunction(id)
		if err != nil {
			t.Fatal(err)
		}
		dBefore, err := db.GetDeployment(fmt.Sprintf("iam-deploy-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		r, err := db.NewIAMPolicy(ctx, id, i == 0, iamTestActor)
		if err != nil {
			t.Fatal(err)
		}
		if r.PolicyJSON != iamTestEmpty || r.ReviewRequired != (i == 0) {
			t.Fatalf("legacy initialization translated SDK: %+v", r)
		}
		r, err = db.SaveIAMPolicy(ctx, iamTestChange(r, iamTestGrant))
		if err != nil {
			t.Fatal(err)
		}
		fnAfter, err := db.GetFunction(id)
		if err != nil || !reflect.DeepEqual(fnBefore, fnAfter) {
			t.Fatalf("IAM modified function/SDK/defaults: %v", err)
		}
		dAfter, err := db.GetDeployment(dBefore.ID)
		if err != nil || !reflect.DeepEqual(dBefore, dAfter) || !dAfter.Snapshot.Equal(SnapshotFromFunction(fnAfter)) {
			t.Fatalf("IAM modified source snapshot: %v", err)
		}
		// Exercise both legacy snapshot boolean values through existing persistence.
		fnAfter.SDK = dBefore.Snapshot.SDK
		if err := db.UpdateFunction(fnAfter); err != nil {
			t.Fatal(err)
		}
		policyAfter, err := db.GetIAMPolicy(ctx, id)
		if err != nil || !reflect.DeepEqual(r, policyAfter) {
			t.Fatalf("legacy snapshot config write modified policy: %v", err)
		}
	}
	before = changes()
	entries, err = db.IAMLegacySDKPreflight(ctx)
	if err != nil || len(entries) != 2 || changes() != before || iamTestCount(t, db, "workload_iam_policies") != 2 || iamTestCount(t, db, "workload_iam_audit") != 4 {
		t.Fatalf("populated preflight mutated rows: %+v, %v", entries, err)
	}
}
