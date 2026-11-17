package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func sourceDocument(t *testing.T, n int, key string, callers int) []byte {
	t.Helper()
	p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}}
	for range n {
		p.Allow = append(p.Allow, workloadiam.Statement{Actions: []workloadiam.Action{workloadiam.KVGet}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}, Context: workloadiam.Execution})
	}
	for range callers {
		p.Inbound = append(p.Inbound, workloadiam.Acceptance{CallerID: ids.New(), Triggers: []workloadiam.Trigger{workloadiam.F2F}})
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestManagedAggregateLimitsApplyAcrossSources(t *testing.T) {
	for _, bound := range []string{"statements", "bytes", "targets"} {
		t.Run(bound, func(t *testing.T) {
			db, ref, owners := managedDB(t, 1)
			var inline, part []byte
			switch bound {
			case "statements":
				inline = sourceDocument(t, 63, "state", 0)
				part = sourceDocument(t, 1, "state", 0)
			case "bytes":
				inline = sourceDocument(t, 20, strings.Repeat("界", 256), 0)
				part = sourceDocument(t, 10, strings.Repeat("界", 256), 0)
			case "targets":
				inline = sourceDocument(t, 0, "", 255)
				part = sourceDocument(t, 0, "", 1)
			}
			managedApply(t, db, ref, IAMManagedMutation{Operation: "inline", PolicyJSON: inline, Owners: managedExpected(t, db, owners)})
			first := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "first", PolicyJSON: part})
			first = managedApply(t, db, ref, managedChange(first, "attach", managedExpected(t, db, owners)))
			before, _ := db.GetIAMSources(t.Context(), owners[0])
			if bound == "bytes" && len(before.Record.PolicyJSON) > workloadiam.MaxPolicyBytes {
				t.Fatal("boundary fixture overlarge")
			}
			second := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "second", PolicyJSON: part})
			if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, managedChange(second, "attach", managedExpected(t, db, owners))); !errors.Is(err, ErrIAMInvalidPolicy) {
				t.Fatalf("aggregate %s bound ignored: %v", bound, err)
			}
			after, _ := db.GetIAMSources(t.Context(), owners[0])
			if !sameSourceState(*before, *after) {
				t.Fatal("over-limit preparation changed source state")
			}
			_ = first
		})
	}
}

func TestManagedOwnerCapSpansVersionsAndRevokeRequiresCompleteSet(t *testing.T) {
	db, ref, owners := managedDB(t, 26)
	p := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "reader", PolicyJSON: []byte(managedReader)})
	p = managedApply(t, db, ref, managedChange(p, "attach", managedExpected(t, db, owners[:12])))
	update := managedChange(p, "update", nil)
	update.PolicyJSON = []byte(managedReader)
	p = managedApply(t, db, ref, update)
	attach := managedChange(p, "attach", managedExpected(t, db, owners[12:25]))
	attach.Version = 2
	p = managedApply(t, db, ref, attach)
	attach = managedChange(p, "attach", managedExpected(t, db, owners[25:]))
	attach.Version = 2
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, attach); err != ErrIAMInvalidPolicy {
		t.Fatal("26th owner across pins accepted", err)
	}
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, managedChange(p, "revoke", managedExpected(t, db, owners[:24]))); err != ErrIAMConflict {
		t.Fatal("incomplete revoke set accepted", err)
	}
	before, err := db.ListIAMAttachedOwners(t.Context(), p.ID)
	if err != nil || len(before) != 25 {
		t.Fatal(err)
	}
	p = managedApply(t, db, ref, managedChange(p, "revoke", managedExpected(t, db, owners[:25])))
	after, err := db.ListIAMAttachedOwners(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range after {
		if a.Enabled || a.Version != before[i].Version {
			t.Fatal("revoke lost pinned version attribution")
		}
	}
	history, err := db.ListIAMManagedAudit(t.Context(), p.ID)
	if err != nil || history[0].Operation != "revoke" || history[0].Actor.ID != ref.ID {
		t.Fatal("managed history missing authoritative actor", err)
	}
}

func TestManagedLiveSessionAndReadOnlyChecks(t *testing.T) {
	db, ref, owners := managedDB(t, 1)
	p := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "reader", PolicyJSON: []byte(managedReader)})
	reader := OperatorCredentialReference{Kind: "api_key", ID: ids.New()}
	if err := db.InsertAPIKey(&APIKey{ID: reader.ID, KeyHash: ids.New(), Name: "reader", Prefix: "dummy", Permissions: `["read"]`}); err != nil {
		t.Fatal(err)
	}
	r := managedChange(p, "attach", managedExpected(t, db, owners))
	if _, err := db.PrepareIAMManagedMutation(t.Context(), reader, r); err != ErrOperatorDenied {
		t.Fatal("reader mutated", err)
	}
	if err := db.initSessionIdentitySchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	user := sessionIdentityUser(t, db)
	session := OperatorCredentialReference{Kind: "session", ID: ids.New()}
	if _, err := db.write.Exec(`INSERT INTO sessions(id,token,user_id,expires_at) VALUES(?,?,?,?)`, session.ID, "dummy-session", user, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	plan, err := db.PrepareIAMManagedMutation(t.Context(), session, r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.write.Exec(`CREATE TRIGGER managed_session_revoke BEFORE INSERT ON workload_iam_attachments BEGIN DELETE FROM sessions; END;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CommitIAMManagedMutation(t.Context(), session, plan); err != ErrOperatorDenied {
		t.Fatal("session logout inside transaction accepted", err)
	}
	attachments, err := db.ListIAMAttachedOwners(t.Context(), p.ID)
	if err != nil || len(attachments) != 0 {
		t.Fatal("revoked session partially committed", err)
	}
}

func TestManagedCountersAndOwnerBoundSelectorsDeny(t *testing.T) {
	db, ref, owners := managedDB(t, 1)
	cron := fmt.Sprintf(`{"schema_version":1,"allow":[{"actions":["crons.upsert"],"resource":{"own_cron":{"name":"x","target_id":%q}},"context":"execution"}],"inbound":[]}`, owners[0])
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, IAMManagedMutation{Operation: "create", Name: "cron", PolicyJSON: []byte(cron)}); err != ErrIAMInvalidPolicy {
		t.Fatal("managed cron silently bound to creator", err)
	}
	p := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "reader", PolicyJSON: []byte(managedReader)})
	_, err := db.write.Exec(`UPDATE workload_iam_managed SET definition_revision=9223372036854775807 WHERE id=?`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, _ = db.GetIAMManagedPolicy(t.Context(), p.ID)
	update := managedChange(p, "update", nil)
	update.PolicyJSON = []byte(managedEmpty)
	if _, err := db.PrepareIAMManagedMutation(t.Context(), ref, update); err != ErrIAMRevisionExhausted {
		t.Fatal("definition counter overflow accepted", err)
	}
}
