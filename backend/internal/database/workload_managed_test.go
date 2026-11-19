package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

const managedReader = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"state"}},"context":"execution"}],"inbound":[]}`
const managedEmpty = `{"schema_version":1,"allow":[],"inbound":[]}`

func managedDB(t *testing.T, n int) (*Database, OperatorCredentialReference, []string) {
	t.Helper()
	db := newTestDB(t)
	if err := db.initWorkloadIAMSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	ref := OperatorCredentialReference{Kind: "api_key", ID: ids.New()}
	if err := db.InsertAPIKey(&APIKey{ID: ref.ID, Name: "managed-admin", KeyHash: ids.New(), Prefix: "dummy", Permissions: `["read","admin"]`}); err != nil {
		t.Fatal(err)
	}
	owners := []string{}
	for i := 0; i < n; i++ {
		id := ids.New()
		owners = append(owners, id)
		if err := db.InsertFunction(&Function{ID: id, Name: fmt.Sprintf("managed-%d", i), Runtime: "node", Status: "active", Entrypoint: "handler.js"}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewIAMPolicy(t.Context(), id, false, IAMActorReference{Kind: "system", ID: ids.New()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.initManagedIAMSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db, ref, owners
}

func managedApply(t *testing.T, db *Database, ref OperatorCredentialReference, r IAMManagedMutation) *IAMManagedPolicy {
	t.Helper()
	p, err := db.PrepareIAMManagedMutation(t.Context(), ref, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CommitIAMManagedMutation(t.Context(), ref, p); err != nil {
		t.Fatal(err)
	}
	return p.Policy()
}

func managedExpected(t *testing.T, db *Database, owners []string) []IAMOwnerExpectation {
	t.Helper()
	out := []IAMOwnerExpectation{}
	for _, id := range owners {
		row, err := db.GetIAMPolicy(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, IAMOwnerExpectation{OwnerID: id, Revision: row.Revision, ApprovedArtifact: row.ApprovedArtifact, CurrentArtifact: strings.Repeat("a", 64)})
	}
	return out
}

func managedChange(p *IAMManagedPolicy, op string, owners []IAMOwnerExpectation) IAMManagedMutation {
	r := IAMManagedMutation{Operation: op, PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Owners: owners}
	if op == "attach" {
		r.Version = 1
	}
	return r
}

func TestManagedVersionsPinsSourcesAndTerminalRevoke(t *testing.T) {
	db, ref, owners := managedDB(t, 2)
	p := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "read-own", PolicyJSON: []byte(managedReader)})
	// Inline remains a separate overlapping grant, never replaced by the union.
	r := IAMManagedMutation{Operation: "inline", PolicyJSON: []byte(managedReader), Owners: managedExpected(t, db, owners[:1])}
	managedApply(t, db, ref, r)
	p = managedApply(t, db, ref, managedChange(p, "attach", managedExpected(t, db, owners)))
	update := managedChange(p, "update", nil)
	update.PolicyJSON = []byte(managedEmpty)
	p = managedApply(t, db, ref, update)
	for _, id := range owners {
		s, err := db.GetIAMSources(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Attachments[0].Version != 1 || s.Record.PolicyJSON == managedEmpty {
			t.Fatal("new version propagated to existing pin")
		}
	}
	if _, err := db.write.Exec(`UPDATE workload_iam_managed_versions SET policy_json=? WHERE policy_id=?`, managedEmpty, p.ID); err == nil {
		t.Fatal("version was mutable")
	}
	p = managedApply(t, db, ref, managedChange(p, "detach", managedExpected(t, db, owners[:1])))
	s, err := db.GetIAMSources(t.Context(), owners[0])
	if err != nil || s.InlineJSON != managedReader || s.Record.PolicyJSON != managedReader {
		t.Fatal("detach destroyed inline overlap", err)
	}
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, managedChange(p, "delete", nil)); err != ErrIAMConflict {
		t.Fatal("delete cascaded attached authority", err)
	}
	p = managedApply(t, db, ref, managedChange(p, "revoke", managedExpected(t, db, owners[1:])))
	s, err = db.GetIAMSources(t.Context(), owners[1])
	if err != nil || s.Attachments[0].Enabled || s.Record.PolicyJSON != managedEmpty {
		t.Fatal("terminal revoke failed", err)
	}
	for _, op := range []string{"attach", "update", "revoke"} {
		r := managedChange(p, op, nil)
		if op == "attach" {
			r.Owners = managedExpected(t, db, owners[:1])
		}
		if op == "update" {
			r.PolicyJSON = []byte(managedReader)
		}
		if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, r); err == nil {
			t.Fatal("revoked definition revived", op)
		}
	}
	p = managedApply(t, db, ref, managedChange(p, "detach", managedExpected(t, db, owners[1:])))
	managedApply(t, db, ref, managedChange(p, "delete", nil))
	if _, err := db.GetIAMManagedPolicy(t.Context(), p.ID); err != ErrIAMPolicyNotFound {
		t.Fatal(err)
	}
	var audits int
	if err := db.read.QueryRow(`SELECT count(*) FROM workload_iam_managed_audit WHERE policy_id=?`, p.ID).Scan(&audits); err != nil || audits < 6 {
		t.Fatal("deletion lost audit", err, audits)
	}
}

func TestManagedBatchSourceCASAndCommitFailure(t *testing.T) {
	for _, cause := range []string{"owner", "membership", "operator", "trigger-revoke", "commit", "source"} {
		t.Run(cause, func(t *testing.T) {
			db, ref, owners := managedDB(t, 2)
			p := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "reader", PolicyJSON: []byte(managedReader)})
			request := managedChange(p, "attach", managedExpected(t, db, owners))
			plan, err := db.PrepareIAMManagedMutation(t.Context(), ref, request)
			if err != nil {
				t.Fatal(err)
			}
			switch cause {
			case "owner":
				if _, err := db.write.Exec(`UPDATE workload_iam_policies SET revision=revision+1 WHERE function_id=?`, owners[1]); err != nil {
					t.Fatal(err)
				}
			case "source":
				if _, err := db.write.Exec(`UPDATE workload_iam_inline SET policy_json=? WHERE function_id=?`, managedReader, owners[1]); err != nil {
					t.Fatal(err)
				}
			case "membership":
				if _, err := db.write.Exec(`UPDATE workload_iam_managed SET membership_revision=membership_revision+1 WHERE id=?`, p.ID); err != nil {
					t.Fatal(err)
				}
			case "operator":
				if _, err := db.write.Exec(`DELETE FROM api_keys WHERE id=?`, ref.ID); err != nil {
					t.Fatal(err)
				}
			case "trigger-revoke":
				_, err = db.write.Exec(fmt.Sprintf(`CREATE TRIGGER managed_revoke BEFORE INSERT ON workload_iam_attachments BEGIN DELETE FROM api_keys WHERE id='%s'; END`, ref.ID))
				if err != nil {
					t.Fatal(err)
				}
			case "commit":
				_, err = db.write.Exec(`CREATE TABLE managed_deferred(n INTEGER REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER managed_bad_commit AFTER INSERT ON workload_iam_attachments BEGIN INSERT INTO managed_deferred VALUES(42); END;`)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := db.GetIAMPolicy(t.Context(), owners[0])
			err = db.CommitIAMManagedMutation(t.Context(), ref, plan)
			if err == nil {
				t.Fatal("failed transaction committed")
			}
			after, _ := db.GetIAMPolicy(t.Context(), owners[0])
			var attached, audit int
			_ = db.read.QueryRow(`SELECT count(*) FROM workload_iam_attachments`).Scan(&attached)
			_ = db.read.QueryRow(`SELECT count(*) FROM workload_iam_audit WHERE function_id=?`, owners[0]).Scan(&audit)
			if *before != *after || attached != 0 || audit != 1 {
				t.Fatalf("partial commit: attached=%d audit=%d", attached, audit)
			}
		})
	}
}

func TestManagedBoundsAndStrictDocuments(t *testing.T) {
	db, ref, owners := managedDB(t, 26)
	p := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "reader", PolicyJSON: []byte(managedReader)})
	for _, body := range []string{
		strings.Replace(managedReader, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		strings.Replace(managedReader, `"schema_version":1`, `"schema_version":1,"actor":"host"`, 1),
		managedReader + " {}",
		strings.Replace(managedReader, "state", string([]byte{0xff}), 1),
	} {
		if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, IAMManagedMutation{Operation: "create", Name: "invalid", PolicyJSON: []byte(body)}); err == nil {
			t.Fatal("invalid definition accepted")
		}
	}
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, managedChange(p, "attach", managedExpected(t, db, owners))); err != ErrIAMInvalidPolicy {
		t.Fatal("26-owner request accepted", err)
	}
	p = managedApply(t, db, ref, managedChange(p, "attach", managedExpected(t, db, owners[:25])))
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, managedChange(p, "attach", managedExpected(t, db, owners[25:]))); err != ErrIAMInvalidPolicy {
		t.Fatal("cross-version distinct-owner cap exceeded", err)
	}
	// A rollout at the exact total-owner cap is an update, not a 26th attachment.
	p = managedApply(t, db, ref, managedChange(p, "attach", managedExpected(t, db, owners[:1])))
	for i := 1; i < 8; i++ {
		q := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: fmt.Sprintf("policy-%d", i), PolicyJSON: []byte(managedReader)})
		managedApply(t, db, ref, managedChange(q, "attach", managedExpected(t, db, owners[:1])))
	}
	q := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "ninth", PolicyJSON: []byte(managedReader)})
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, managedChange(q, "attach", managedExpected(t, db, owners[:1]))); err != ErrIAMInvalidPolicy {
		t.Fatal("ninth policy accepted", err)
	}
	duplicate := managedExpected(t, db, owners[:1])
	duplicate = append(duplicate, duplicate[0])
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, managedChange(p, "detach", duplicate)); err != ErrIAMInvalidPolicy {
		t.Fatal("duplicate owner accepted", err)
	}
	// JSON revisions are decimal strings, not potentially lossy JS numbers.
	raw, _ := json.Marshal(p)
	if !strings.Contains(string(raw), `"definition_revision":"1"`) {
		t.Fatal(string(raw))
	}
}

func TestManagedInitializationPreservesInlineAndRemainsInert(t *testing.T) {
	db, ref, owners := managedDB(t, 1)
	if _, err := db.write.Exec(`DELETE FROM workload_iam_inline WHERE function_id=?`, owners[0]); err != nil {
		t.Fatal(err)
	}
	row, _ := db.GetIAMPolicy(t.Context(), owners[0])
	_, err := db.SaveIAMPolicy(t.Context(), IAMPolicyChange{OwnerID: owners[0], ExpectedRevision: row.Revision, ExpectedApprovedArtifact: row.ApprovedArtifact,
		ApprovedArtifact: strings.Repeat("b", 64), PolicyJSON: []byte(managedReader), Actor: IAMActorReference{Kind: ref.Kind, ID: ref.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.initManagedIAMSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.initManagedIAMSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	s, err := db.GetIAMSources(t.Context(), owners[0])
	if err != nil || s.InlineJSON != managedReader {
		t.Fatal("migration emptied existing inline", err)
	}
	// Normal boot migration does not create the managed schema.
	fresh := newTestDB(t)
	var count int
	if err := fresh.read.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='workload_iam_managed'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("managed schema boot-activated", err)
	}
}

func TestManagedPreparedPlanOwnsInputsAndFinalCredentialCheck(t *testing.T) {
	db, ref, owners := managedDB(t, 1)
	request := IAMManagedMutation{Operation: "create", Name: "reader", PolicyJSON: []byte(managedReader)}
	plan, err := db.PrepareIAMManagedMutation(context.Background(), ref, request)
	if err != nil {
		t.Fatal(err)
	}
	request.PolicyJSON[0] = '['
	preview := plan.Policy()
	preview.Name = "tampered"
	if err := db.CommitIAMManagedMutation(t.Context(), ref, plan); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetIAMManagedPolicy(t.Context(), plan.Policy().ID)
	if err != nil || p.Name != "reader" {
		t.Fatal(err)
	}
	attach := managedChange(p, "attach", managedExpected(t, db, owners))
	next, err := db.PrepareIAMManagedMutation(t.Context(), ref, attach)
	if err != nil {
		t.Fatal(err)
	}
	attach.Owners[0].OwnerID = ids.New()
	copies := next.Publications()
	copies[0].After.PolicyJSON = managedEmpty
	if err := db.CommitIAMManagedMutation(t.Context(), ref, next); err != nil {
		t.Fatal(err)
	}
	s, err := db.GetIAMSources(t.Context(), owners[0])
	if err != nil || s.Record.PolicyJSON != managedReader {
		t.Fatal("caller mutated plan", err)
	}
	if err := db.CommitIAMManagedMutation(t.Context(), ref, next); !errors.Is(err, ErrIAMConflict) {
		t.Fatal("plan replay succeeded", err)
	}
	e, err := s.Record.AdmissionEvaluator()
	if err != nil || e.Metadata().OwnerID != owners[0] {
		t.Fatal(err)
	}
}
