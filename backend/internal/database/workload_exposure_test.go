package database

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func exposureSetup(t *testing.T, kind string) (*Database, OperatorCredentialReference, string) {
	t.Helper()
	db, ref, p := configurationPlanSetup(t, kind)
	if err := db.initWorkloadExposureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner := p.before.Artifact.OwnerID
	operatorExec(t, db, `UPDATE workload_iam_policies SET policy_json=?,review_required=0,quarantined=0`, iamTestEmpty)
	operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=?`, iamTestEmpty)
	return db, ref, owner
}
func exposureExpected(t *testing.T, db *Database, owners ...string) map[string]ExposureOwnerExpected {
	t.Helper()
	out := map[string]ExposureOwnerExpected{}
	for _, id := range owners {
		s, err := db.GetIAMSources(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		out[id] = ExposureOwnerExpected{s.Record.Revision, s.Record.AuthorityEpoch}
	}
	return out
}
func exposurePrepare(t *testing.T, db *Database, c ExposureMutation) *ExposurePlan {
	t.Helper()
	p, err := db.PrepareExposure(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func exposureGrant(t *testing.T, db *Database, owner, kind string) {
	t.Helper()
	body := iamTestEmpty
	switch kind {
	case "outbound":
		body = iamTestGrant
	case "inbound":
		body = `{"schema_version":1,"allow":[],"inbound":[{"caller_id":"` + ids.New() + `","triggers":["f2f"]}]}`
	case "quarantine":
		operatorExec(t, db, `UPDATE workload_iam_policies SET quarantined=1 WHERE function_id=?`, owner)
	case "review":
		operatorExec(t, db, `UPDATE workload_iam_policies SET review_required=1 WHERE function_id=?`, owner)
	case "retained":
		id := ids.New()
		operatorExec(t, db, `INSERT INTO workload_iam_managed VALUES(?,'retained',1,1,1,1)`, id)
		operatorExec(t, db, `INSERT INTO workload_iam_managed_versions VALUES(?,1,?,'system',?,'2026-11-18T00:00:00Z')`, id, iamTestGrant, ids.New())
		operatorExec(t, db, `INSERT INTO workload_iam_attachments VALUES(?,?,1,0)`, owner, id)
	case "attached":
		id := ids.New()
		operatorExec(t, db, `INSERT INTO workload_iam_managed VALUES(?,'attached',1,1,1,0)`, id)
		operatorExec(t, db, `INSERT INTO workload_iam_managed_versions VALUES(?,1,?,'system',?,'2026-11-18T00:00:00Z')`, id, iamTestGrant, ids.New())
		operatorExec(t, db, `INSERT INTO workload_iam_attachments VALUES(?,?,1,1)`, owner, id)
		body = iamTestGrant
	}
	operatorExec(t, db, `UPDATE workload_iam_policies SET policy_json=? WHERE function_id=?`, body, owner)
	if kind != "attached" {
		operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=? WHERE function_id=?`, body, owner)
	}
}
func exposureNoWrites(t *testing.T, db *Database) {
	t.Helper()
	for _, table := range []string{"routes", "inbound_webhooks", "workload_exposure_versions", "workload_exposure_audit"} {
		var n int
		if err := db.read.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("unexpected durable exposure effects", table, n, err)
		}
	}
}
func TestExposureSourceAwarePrivilege(t *testing.T) {
	for _, kind := range []string{"empty", "outbound", "inbound", "attached", "retained", "quarantine", "review"} {
		for _, admin := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "-admin", false: "-write"}[admin], func(t *testing.T) {
				db, ref, owner := exposureSetup(t, "api_key")
				exposureGrant(t, db, owner, kind)
				perms := `["write"]`
				if admin {
					perms = `["admin"]`
				}
				operatorExec(t, db, `UPDATE api_keys SET permissions=?`, perms)
				before, err := db.GetIAMSources(t.Context(), owner)
				if err != nil {
					t.Fatal(err)
				}
				p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/exposure", FunctionID: owner, Expected: exposureExpected(t, db, owner)})
				_, err = db.CommitExposure(t.Context(), ref, p)
				if !admin && kind != "empty" {
					if !errors.Is(err, ErrOperatorDenied) {
						t.Fatalf("protected WRITE not denied: %v", err)
					}
					exposureNoWrites(t, db)
				} else if err != nil {
					t.Fatal("authorized writer denied", err)
				}
				after, err := db.GetIAMSources(t.Context(), owner)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("exposure changed policy sources/epoch/approval")
				}
			})
		}
	}
}

func TestExposurePreparedWriterLosesAuthority(t *testing.T) {
	for _, kind := range []string{"inbound", "attached", "retained", "review", "epoch"} {
		for _, admin := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "-admin", false: "-write"}[admin], func(t *testing.T) {
				db, ref, owner := exposureSetup(t, "api_key")
				if !admin {
					operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
				}
				p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/queued", FunctionID: owner, Expected: exposureExpected(t, db, owner)})
				if kind == "epoch" {
					operatorExec(t, db, `UPDATE workload_iam_policies SET authority_epoch=authority_epoch+2`)
				} else {
					exposureGrant(t, db, owner, kind)
				}
				_, err := db.CommitExposure(t.Context(), ref, p)
				want := ErrOperatorDenied
				if admin {
					want = ErrExposureConflict
				}
				if !errors.Is(err, want) {
					t.Fatalf("prepared authority reused: %v", err)
				}
				exposureNoWrites(t, db)
			})
		}
	}
}

func TestExposureLiveCredentialInMutationTransaction(t *testing.T) {
	for kind, queries := range map[string][]string{
		"api_key": {`DELETE FROM api_keys`, `UPDATE api_keys SET expires_at='2000-01-01 00:00:00'`, `UPDATE api_keys SET permissions='["write"]'`},
		"session": {`DELETE FROM sessions`, `DELETE FROM users`, `UPDATE sessions SET expires_at='2000-01-01 00:00:00'`},
		"oauth":   {`UPDATE oauth_access_tokens SET revoked_at=CURRENT_TIMESTAMP`, `UPDATE oauth_clients SET revoked_at=CURRENT_TIMESTAMP`, `UPDATE oauth_access_tokens SET access_expires_at='2000-01-01 00:00:00'`, `UPDATE oauth_clients SET scope='read write'`, `UPDATE oauth_access_tokens SET resource='https://wrong.invalid/mcp'`},
	} {
		for i, query := range queries {
			t.Run(kind+string(rune('a'+i)), func(t *testing.T) {
				db, ref, owner := exposureSetup(t, kind)
				exposureGrant(t, db, owner, "inbound")
				p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/revoked", FunctionID: owner, Expected: exposureExpected(t, db, owner)})
				operatorExec(t, db, query)
				if _, err := db.CommitExposure(t.Context(), ref, p); !errors.Is(err, ErrOperatorDenied) {
					t.Fatalf("revoked/expired credential accepted: %v", err)
				}
				exposureNoWrites(t, db)
			})
		}
	}
	t.Run("permission-recheck-before-commit", func(t *testing.T) {
		db, ref, owner := exposureSetup(t, "api_key")
		exposureGrant(t, db, owner, "inbound")
		p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/final-check", FunctionID: owner, Expected: exposureExpected(t, db, owner)})
		operatorExec(t, db, `CREATE TRIGGER revoke_exposure_actor AFTER INSERT ON workload_exposure_audit BEGIN UPDATE api_keys SET permissions='[]'; END`)
		if _, err := db.CommitExposure(t.Context(), ref, p); !errors.Is(err, ErrOperatorDenied) {
			t.Fatal("missing final credential check", err)
		}
		exposureNoWrites(t, db)
	})
}

func exposureSecondOwner(t *testing.T, db *Database, owner string) string {
	t.Helper()
	fn, err := db.GetFunction(owner)
	if err != nil {
		t.Fatal(err)
	}
	fn = fn.Clone()
	fn.ID, fn.Name = ids.New(), "second"
	if err = db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	p, err := db.NewIAMPolicy(t.Context(), fn.ID, false, IAMActorReference{Kind: "system", ID: ids.New()})
	if err != nil {
		t.Fatal(err)
	}
	operatorExec(t, db, `UPDATE workload_iam_policies SET approved_artifact=? WHERE function_id=?`, strings.Repeat("a", 64), fn.ID)
	operatorExec(t, db, `INSERT INTO workload_iam_inline VALUES(?,?)`, fn.ID, p.PolicyJSON)
	return fn.ID
}

func TestExposureRouteBothOwnersAndExactMapping(t *testing.T) {
	for _, protected := range []string{"old", "new", "both", "none"} {
		for _, admin := range []bool{false, true} {
			t.Run(protected+map[bool]string{true: "-admin", false: "-write"}[admin], func(t *testing.T) {
				db, ref, old := exposureSetup(t, "api_key")
				next := exposureSecondOwner(t, db, old)
				if protected == "old" || protected == "both" {
					exposureGrant(t, db, old, "inbound")
				}
				if protected == "new" || protected == "both" {
					exposureGrant(t, db, next, "inbound")
				}
				if err := db.UpsertRoute("/binding", old, "GET"); err != nil {
					t.Fatal(err)
				}
				if !admin {
					operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
				}
				p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/binding", FunctionID: next, Methods: "POST", Expected: exposureExpected(t, db, old, next)})
				if !reflect.DeepEqual(p.Owners(), func() []string {
					if old < next {
						return []string{old, next}
					}
					return []string{next, old}
				}()) {
					t.Fatal("owners not sorted")
				}
				_, err := db.CommitExposure(t.Context(), ref, p)
				r, e := db.GetRoute("/binding")
				if e != nil {
					t.Fatal(e)
				}
				if !admin && protected != "none" {
					if !errors.Is(err, ErrOperatorDenied) || r.FunctionID != old || r.Methods != "GET" {
						t.Fatal("privileged route stolen", err)
					}
				} else if err != nil || r.FunctionID != next || r.Methods != "POST" {
					t.Fatal("authorized rebind failed", err)
				}
			})
		}
	}
	t.Run("mapping-changed-after-prepare", func(t *testing.T) {
		db, ref, old := exposureSetup(t, "api_key")
		next := exposureSecondOwner(t, db, old)
		if err := db.UpsertRoute("/race", old, "GET"); err != nil {
			t.Fatal(err)
		}
		p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/race", FunctionID: next, Expected: exposureExpected(t, db, old, next)})
		if err := db.UpsertRoute("/race", next, "POST"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CommitExposure(t.Context(), ref, p); !errors.Is(err, ErrExposureConflict) {
			t.Fatal("mapping race accepted", err)
		}
		r, _ := db.GetRoute("/race")
		if r.FunctionID != next || r.Methods != "POST" {
			t.Fatal("racing mapping overwritten")
		}
	})
}

func TestExposureAtomicRollbackDeleteCASAndAudit(t *testing.T) {
	for _, failure := range []string{"commit", "audit", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			db, ref, owner := exposureSetup(t, "api_key")
			p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/atomic", FunctionID: owner, Expected: exposureExpected(t, db, owner)})
			switch failure {
			case "commit":
				operatorExec(t, db, `CREATE TABLE exposure_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER exposure_fail AFTER INSERT ON workload_exposure_audit BEGIN INSERT INTO exposure_guard VALUES('absent'); END`)
			case "audit":
				operatorExec(t, db, `CREATE TRIGGER exposure_fail BEFORE INSERT ON workload_exposure_audit BEGIN SELECT RAISE(ABORT,'private-canary'); END`)
			}
			ctx := t.Context()
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if row, err := db.CommitExposure(ctx, ref, p); err == nil || row != nil {
				t.Fatal("failure committed")
			}
			exposureNoWrites(t, db)
		})
	}
	t.Run("delete-owner-authorization-and-tombstone", func(t *testing.T) {
		db, ref, owner := exposureSetup(t, "api_key")
		if err := db.UpsertRoute("/delete", owner, "GET"); err != nil {
			t.Fatal(err)
		}
		exposureGrant(t, db, owner, "inbound")
		p := exposurePrepare(t, db, ExposureMutation{Operation: "route_delete", Key: "/delete", Expected: exposureExpected(t, db, owner)})
		operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
		if _, err := db.CommitExposure(t.Context(), ref, p); !errors.Is(err, ErrOperatorDenied) {
			t.Fatal("delete did not authorize existing owner", err)
		}
		operatorExec(t, db, `UPDATE api_keys SET permissions='["admin"]'`)
		if _, err := db.CommitExposure(t.Context(), ref, p); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CommitExposure(t.Context(), ref, p); !errors.Is(err, ErrExposureConflict) {
			t.Fatal("stale delete accepted", err)
		}
		recreate := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/delete", FunctionID: owner, Version: 1, Expected: exposureExpected(t, db, owner)})
		if _, err := db.CommitExposure(t.Context(), ref, recreate); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CommitExposure(t.Context(), ref, p); !errors.Is(err, ErrExposureConflict) {
			t.Fatal("ABA delete accepted", err)
		}
	})
	t.Run("missing-deleting-and-literal-uuid", func(t *testing.T) {
		for _, change := range []string{"missing", "deleting", "source"} {
			t.Run(change, func(t *testing.T) {
				db, ref, owner := exposureSetup(t, "api_key")
				p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/missing", FunctionID: owner, Expected: exposureExpected(t, db, owner)})
				if change == "missing" {
					operatorExec(t, db, `DELETE FROM functions WHERE id=?`, owner)
				}
				if change == "deleting" {
					operatorExec(t, db, `UPDATE functions SET status='deleting'`)
				}
				if change == "source" {
					operatorExec(t, db, `DELETE FROM workload_iam_inline`)
				}
				if _, err := db.CommitExposure(t.Context(), ref, p); err == nil {
					t.Fatal("missing owner/state accepted")
				}
			})
		}
	})
}

func TestExposureInboundCRUDImmutableOwnerSecret(t *testing.T) {
	db, ref, owner := exposureSetup(t, "api_key")
	name := "signed trigger"
	c := ExposureMutation{Operation: "inbound_create", Key: ids.New(), FunctionID: owner, Expected: exposureExpected(t, db, owner), Name: &name}
	p := exposurePrepare(t, db, c)
	row, err := db.CommitExposure(t.Context(), ref, p)
	if err != nil || row == nil || len(row.Secret) != 64 {
		t.Fatal("create failed", err)
	}
	original := *row
	no := false
	format := "github"
	c.Operation, c.Version, c.SignatureFormat, c.Active = "inbound_update", 1, &format, &no
	p = exposurePrepare(t, db, c)
	row, err = db.CommitExposure(t.Context(), ref, p)
	if err != nil || row.Secret != original.Secret || row.FunctionID != original.FunctionID || row.Active || row.SignatureHeader != DefaultSignatureHeader(format) {
		t.Fatal("update changed owner/secret or lost editable fields", err)
	}
	c.Operation, c.Version = "inbound_delete", 2
	p = exposurePrepare(t, db, c)
	if _, err = db.CommitExposure(t.Context(), ref, p); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CommitExposure(t.Context(), ref, p); !errors.Is(err, ErrExposureConflict) {
		t.Fatal("stale webhook deletion accepted", err)
	}
	var count int
	if err = db.read.QueryRow(`SELECT count(*) FROM workload_exposure_audit WHERE function_id=? AND actor_kind=? AND actor_id=?`, owner, ref.Kind, ref.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal("audit actor missing", err)
	}
}

func TestExposureAuditBoundAndLiteralOwners(t *testing.T) {
	db, ref, owner := exposureSetup(t, "api_key")
	for i := uint64(0); i < 40; i++ {
		p := exposurePrepare(t, db, ExposureMutation{Operation: "route_set", Key: "/audit", FunctionID: owner, Version: i, Expected: exposureExpected(t, db, owner)})
		if _, err := db.CommitExposure(t.Context(), ref, p); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.read.QueryRow(`SELECT count(*) FROM workload_exposure_audit`).Scan(&n); err != nil || n != 32 {
		t.Fatal("audit not bounded", n, err)
	}
	ghost := ids.New()
	fn, err := db.GetFunction(owner)
	if err != nil {
		t.Fatal(err)
	}
	fn = fn.Clone()
	fn.ID, fn.Name = ids.New(), ghost
	if err = db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	_, err = db.PrepareExposure(t.Context(), ExposureMutation{Operation: "route_set", Key: "/literal", FunctionID: ghost, Expected: map[string]ExposureOwnerExpected{ghost: {PolicyRevision: 1, AuthorityEpoch: 1}}})
	if !errors.Is(err, ErrExposureUnavailable) {
		t.Fatal("missing literal UUID adopted a function name", err)
	}
}
