package workloadhistory

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const coordinationReader = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"state"}},"context":"execution"}],"inbound":[]}`
const coordinationEmpty = `{"schema_version":1,"allow":[],"inbound":[]}`

type coordinationFixture struct {
	*fixture
	managed  *workloadpolicy.Service
	artifact string
}

func (f *coordinationFixture) stage(t *testing.T, name, marker string) {
	t.Helper()
	tree, err := parser.ParseFile(token.NewFileSet(), "../database/"+name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	ast.Inspect(tree, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			v, _ := strconv.Unquote(lit.Value)
			if strings.Contains(v, marker) {
				schema = v
			}
		}
		return true
	})
	if schema == "" {
		t.Fatal("canonical staged schema absent")
	}
	f.exec(t, schema)
}
func coordinated(t *testing.T) *coordinationFixture {
	t.Helper()
	f := &coordinationFixture{fixture: setup(t), artifact: strings.Repeat("a", 64)}
	f.stage(t, "workload_iam_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_policies")
	evaluators := []*workloadiam.Evaluator{}
	for _, owner := range []string{f.target, f.caller} {
		row, err := f.db.NewIAMPolicy(f.ctx, owner, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
		if err != nil {
			t.Fatal(err)
		}
		e, err := row.AdmissionEvaluator()
		if err != nil {
			t.Fatal(err)
		}
		evaluators = append(evaluators, e)
	}
	f.stage(t, "workload_managed_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_inline")
	a, err := workloadiam.NewAuthority(128, evaluators)
	if err != nil {
		t.Fatal(err)
	}
	f.s.Authority = a
	f.s.Registry = f.reg
	f.s.CurrentArtifact = func(string) string { return f.artifact }
	f.a = &workloadasync.Service{DB: f.db, Authority: a, CurrentArtifact: f.s.CurrentArtifact}
	f.managed, err = workloadpolicy.NewManaged(f.db, f.reg, a, func(owner string) workloadpolicy.ArtifactState {
		return workloadpolicy.ArtifactState{OwnerID: owner, Artifact: f.artifact, Known: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s.Managed = f.managed
	return f
}
func (f *coordinationFixture) expected(t *testing.T, owners ...string) []database.IAMOwnerExpectation {
	t.Helper()
	out := []database.IAMOwnerExpectation{}
	for _, owner := range owners {
		row, err := f.db.GetIAMPolicy(f.ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, database.IAMOwnerExpectation{OwnerID: owner, Revision: row.Revision, ApprovedArtifact: row.ApprovedArtifact, CurrentArtifact: f.artifact})
	}
	return out
}
func (f *coordinationFixture) create(t *testing.T, name string) *database.IAMManagedPolicy {
	t.Helper()
	r, err := f.managed.MutateManaged(f.ctx, f.ref, database.IAMManagedMutation{Operation: "create", Name: name, PolicyJSON: []byte(coordinationReader)})
	if err != nil {
		t.Fatal(err)
	}
	return r.Policy
}
func (f *coordinationFixture) change(t *testing.T, p *database.IAMManagedPolicy, op string, owners ...string) *database.IAMManagedPolicy {
	t.Helper()
	req := database.IAMManagedMutation{Operation: op, PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Owners: f.expected(t, owners...)}
	if op == "attach" {
		req.Version = 1
	}
	r, err := f.managed.MutateManaged(f.ctx, f.ref, req)
	if err != nil {
		t.Fatal(err)
	}
	return r.Policy
}
func (f *coordinationFixture) allows(owner string) bool {
	key := "state"
	return f.s.Authority.Do(workloadiam.Context{
		Principal: workloadiam.Principal{FunctionID: owner, WorkerID: "fixture", ArtifactRevision: f.artifact},
		Phase:     workloadiam.Execution, Ready: true, Lease: workloadiam.Lease{ID: "fixture", WorkerID: "fixture", ExecutionID: ids.New(), Active: true},
	}, workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: owner, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}, func() error { return nil }) == nil
}
func (f *coordinationFixture) newOwner(t *testing.T, name string) string {
	t.Helper()
	owner := ids.New()
	if err := f.db.InsertFunction(&database.Function{ID: owner, Name: name, Runtime: "node", Status: "active", Entrypoint: "handler.js"}); err != nil {
		t.Fatal(err)
	}
	row, err := f.db.NewIAMPolicy(f.ctx, owner, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
	if err != nil {
		t.Fatal(err)
	}
	f.stage(t, "workload_managed_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_inline")
	e, err := row.AdmissionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.Authority.Publish(0, e, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestCoordinationMembershipAuditSurvivors(t *testing.T) {
	f := coordinated(t)
	p := f.change(t, f.create(t, "shared"), "attach", f.target, f.caller)
	q := f.change(t, f.create(t, "overlap"), "attach", f.caller)
	before, err := f.db.GetIAMSources(f.ctx, f.caller)
	if err != nil {
		t.Fatal(err)
	}
	oldExpected := f.expected(t, f.target)
	if !f.allows(f.target) || !f.allows(f.caller) {
		t.Fatal("fixture grant absent")
	}
	if err = f.reg.Delete(f.ctx, f.target); err != nil {
		t.Fatal(err)
	}
	current, err := f.db.GetIAMManagedPolicy(f.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.MembershipRevision != p.MembershipRevision+1 || current.DefinitionRevision != p.DefinitionRevision || current.LatestVersion != p.LatestVersion {
		t.Fatal("wrong named counters")
	}
	owners, err := f.db.ListIAMAttachedOwners(f.ctx, p.ID)
	if err != nil || len(owners) != 1 || owners[0].OwnerID != f.caller {
		t.Fatal("stale membership", err)
	}
	after, err := f.db.GetIAMSources(f.ctx, f.caller)
	if err != nil || !reflect.DeepEqual(before, after) || !f.allows(f.caller) || f.allows(f.target) {
		t.Fatal("surviving authority changed or deleted owner active", err)
	}
	other, err := f.db.GetIAMManagedPolicy(f.ctx, q.ID)
	if err != nil || *other != *q {
		t.Fatal("unrelated definition changed", err)
	}
	history, err := f.s.List(f.ctx, f.target, database.HistorySelector{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var summary []database.IAMOwnerDeletionAudit
	for _, e := range history {
		if e.Kind == "delete" {
			var root struct {
				Managed []database.IAMOwnerDeletionAudit `json:"managed_sources"`
			}
			if err = json.Unmarshal(e.Metadata, &root); err != nil {
				t.Fatal(err)
			}
			summary = root.Managed
		}
	}
	audit, err := f.db.ListIAMManagedAudit(f.ctx, p.ID)
	if err != nil || len(summary) != 1 || audit[0].Operation != "owner_delete" || audit[0].Digest != summary[0].Digest ||
		audit[0].Actor.ID != f.ref.ID || summary[0].OwnerID != f.target || summary[0].PolicyID != p.ID ||
		summary[0].MembershipBefore != p.MembershipRevision || summary[0].MembershipAfter != current.MembershipRevision {
		t.Fatal("retained UUID/audit mismatch", err)
	}
	_, err = f.managed.MutateManaged(f.ctx, f.ref, database.IAMManagedMutation{Operation: "attach", PolicyID: p.ID, MembershipRevision: current.MembershipRevision, Version: 1, Owners: oldExpected})
	if err == nil {
		t.Fatal("retired UUID reattached")
	}
	fresh := f.newOwner(t, "target")
	if f.allows(fresh) {
		t.Fatal("same name inherited source")
	}
	f.change(t, current, "attach", fresh)
	if !f.allows(fresh) {
		t.Fatal("fresh explicit grant did not work")
	}
	f.ledger(t)
}

func TestCoordinationFailureAtomicity(t *testing.T) {
	for _, mode := range []string{"capacity", "preflight", "revoked", "expired", "cancelled", "commit", "commit_revoke", "commit_expire", "missing_coordinator"} {
		t.Run(mode, func(t *testing.T) {
			f := coordinated(t)
			p := f.change(t, f.create(t, "shared"), "attach", f.target, f.caller)
			survivor, err := f.db.GetIAMSources(f.ctx, f.caller)
			if err != nil {
				t.Fatal(err)
			}
			auditBefore, err := f.db.ListIAMManagedAudit(f.ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			ctx := f.ctx
			switch mode {
			case "capacity":
				f.exec(t, `UPDATE work_history_capacity SET rows_used=100000`)
			case "preflight":
				f.exec(t, `UPDATE cron_schedules SET name=?`, strings.Repeat("x", 3000))
			case "revoked":
				f.exec(t, `DELETE FROM api_keys WHERE id=?`, f.ref.ID)
			case "expired":
				f.exec(t, `UPDATE api_keys SET expires_at=? WHERE id=?`, time.Now().UTC().Add(-time.Minute), f.ref.ID)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "commit":
				f.exec(t, `CREATE TABLE bad_coord(x TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER fail_coord AFTER UPDATE ON workload_iam_managed BEGIN INSERT INTO bad_coord VALUES('missing'); END;`)
			case "commit_revoke":
				f.exec(t, `CREATE TRIGGER fail_coord AFTER UPDATE ON workload_iam_managed BEGIN DELETE FROM api_keys; END;`)
			case "commit_expire":
				f.exec(t, `CREATE TRIGGER fail_coord AFTER UPDATE ON workload_iam_managed BEGIN UPDATE api_keys SET expires_at='2000-01-01 00:00:00'; END;`)
			case "missing_coordinator":
				f.s.Managed = nil
			}
			drained := false
			if err = f.reg.DeleteWithDrain(ctx, f.target, func() { drained = true }); err == nil {
				t.Fatal("expected refusal")
			}
			current, err := f.db.GetIAMManagedPolicy(f.ctx, p.ID)
			if err != nil || *current != *p {
				t.Fatal("failed deletion advanced membership", err)
			}
			owners, err := f.db.ListIAMAttachedOwners(f.ctx, p.ID)
			if err != nil || len(owners) != 2 {
				t.Fatal("failed deletion removed owner", err)
			}
			fn, err := f.reg.Get(f.target)
			if err != nil || fn.Status != "active" || drained || !f.allows(f.target) {
				t.Fatal("failed deletion changed admission", err)
			}
			after, err := f.db.GetIAMSources(f.ctx, f.caller)
			if err != nil || !reflect.DeepEqual(after, survivor) {
				t.Fatal("failed deletion changed survivor", err)
			}
			audit, err := f.db.ListIAMManagedAudit(f.ctx, p.ID)
			if err != nil || !reflect.DeepEqual(auditBefore, audit) {
				t.Fatal("failed deletion retained audit", err)
			}
			if f.count(t, `SELECT count(*) FROM work_history`) != 0 {
				t.Fatal("failed deletion reserved history")
			}
		})
	}
}

func TestCoordinationPartialRetryOnce(t *testing.T) {
	f := coordinated(t)
	p := f.change(t, f.create(t, "shared"), "attach", f.target, f.caller)
	f.exec(t, `CREATE TRIGGER stop_cleanup BEFORE DELETE ON jobs BEGIN SELECT RAISE(ABORT,'cleanup fault'); END;`)
	if err := f.reg.Delete(f.ctx, f.target); err == nil {
		t.Fatal("expected cleanup fault")
	}
	mid, err := f.db.GetIAMManagedPolicy(f.ctx, p.ID)
	if err != nil || mid.MembershipRevision != p.MembershipRevision+1 {
		t.Fatal("membership not removed at publication", err)
	}
	audit, err := f.db.ListIAMManagedAudit(f.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `DROP TRIGGER stop_cleanup`)
	if err = f.reg.Delete(f.ctx, f.target); err != nil {
		t.Fatal(err)
	}
	after, err := f.db.GetIAMManagedPolicy(f.ctx, p.ID)
	if err != nil || *after != *mid {
		t.Fatal("retry advanced membership twice", err)
	}
	finalAudit, err := f.db.ListIAMManagedAudit(f.ctx, p.ID)
	if err != nil || !reflect.DeepEqual(audit, finalAudit) {
		t.Fatal("retry duplicated audit", err)
	}
	f.ledger(t)
}

func TestCoordinationConcurrentManagedOperations(t *testing.T) {
	for _, op := range []string{"attach", "detach", "rollout", "revoke"} {
		t.Run(op, func(t *testing.T) {
			f := coordinated(t)
			p := f.change(t, f.create(t, "shared"), "attach", f.target, f.caller)
			q := f.change(t, f.create(t, "overlap"), "attach", f.caller)
			req := database.IAMManagedMutation{Operation: op, PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision}
			switch op {
			case "attach":
				req.Version = 1
				req.Owners = f.expected(t, f.newOwner(t, "third"))
			case "detach":
				req.Owners = f.expected(t, f.caller)
			case "rollout":
				updated, err := f.managed.MutateManaged(f.ctx, f.ref, database.IAMManagedMutation{Operation: "update", PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, PolicyJSON: []byte(coordinationEmpty)})
				if err != nil {
					t.Fatal(err)
				}
				p = updated.Policy
				req.Operation = "attach"
				req.Version = 2
				req.Owners = f.expected(t, f.caller)
			case "revoke":
				req.Owners = f.expected(t, f.target, f.caller)
			}
			start := make(chan struct{})
			mutation := make(chan error, 1)
			deletion := make(chan error, 1)
			go func() { <-start; _, err := f.managed.MutateManaged(f.ctx, f.ref, req); mutation <- err }()
			go func() { <-start; deletion <- f.reg.Delete(f.ctx, f.target) }()
			close(start)
			wait := func(ch <-chan error) error {
				t.Helper()
				select {
				case err := <-ch:
					return err
				case <-time.After(10 * time.Second):
					t.Fatal("owner/definition lock deadlock")
					return nil
				}
			}
			merr, derr := wait(mutation), wait(deletion)
			if derr != nil {
				if !errors.Is(derr, database.ErrIAMConflict) {
					t.Fatal("unexpected deletion error", derr)
				}
				if err := f.reg.Delete(f.ctx, f.target); err != nil {
					t.Fatal("explicit fresh deletion retry", err)
				}
			}
			if merr != nil && !errors.Is(merr, database.ErrIAMConflict) && !errors.Is(merr, database.ErrIAMFunctionNotFound) && !errors.Is(merr, database.ErrFunctionDeleting) && !errors.Is(merr, workloadiam.ErrDenied) {
				t.Fatal("unexpected mutation error", merr)
			}
			current, err := f.db.GetIAMManagedPolicy(f.ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := p.MembershipRevision + 1
			if merr == nil {
				want++
			}
			if current.MembershipRevision != want {
				t.Fatal("non-serial membership counter")
			}
			owners, err := f.db.ListIAMAttachedOwners(f.ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range owners {
				if o.OwnerID == f.target {
					t.Fatal("retired member remained")
				}
			}
			unrelated, err := f.db.GetIAMManagedPolicy(f.ctx, q.ID)
			if err != nil || *unrelated != *q || !f.allows(f.caller) {
				t.Fatal("surviving overlap grant changed", err)
			}
			f.ledger(t)
		})
	}
}

func TestCoordinationMembershipCapCommitBoundary(t *testing.T) {
	f := coordinated(t)
	p := f.create(t, "full")
	owners := []string{f.target, f.caller}
	for i := 2; i < database.MaxIAMPolicyOwners; i++ {
		owners = append(owners, f.newOwner(t, "owner-"+strconv.Itoa(i)))
	}
	p = f.change(t, p, "attach", owners...)
	extra := f.newOwner(t, "extra")
	attach := func() error {
		current, err := f.db.GetIAMManagedPolicy(f.ctx, p.ID)
		if err != nil {
			return err
		}
		_, err = f.managed.MutateManaged(f.ctx, f.ref, database.IAMManagedMutation{Operation: "attach", PolicyID: p.ID, MembershipRevision: current.MembershipRevision, Version: 1, Owners: f.expected(t, extra)})
		return err
	}
	if err := attach(); err == nil {
		t.Fatal("full membership accepted extra owner")
	}
	f.exec(t, `UPDATE work_history_capacity SET rows_used=100000`)
	if err := f.reg.Delete(f.ctx, f.target); !errors.Is(err, database.ErrHistoryCapacity) {
		t.Fatal("expected capacity denial", err)
	}
	if err := attach(); err == nil {
		t.Fatal("failed deletion freed membership cap")
	}
	f.exec(t, `UPDATE work_history_capacity SET rows_used=0`)
	marked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- f.reg.DeleteWithDrain(f.ctx, f.target, func() { close(marked); <-release }) }()
	select {
	case <-marked:
	case <-time.After(10 * time.Second):
		t.Fatal("deletion preparation blocked")
	}
	err := attach()
	close(release)
	if err != nil {
		t.Fatal("committed deletion did not free cap", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	attached, err := f.db.ListIAMAttachedOwners(f.ctx, p.ID)
	if err != nil || len(attached) != database.MaxIAMPolicyOwners {
		t.Fatal("wrong final owner count", err)
	}
	f.ledger(t)
}

func TestCoordinationConcurrentDeletionActorsAndCallbacks(t *testing.T) {
	f := coordinated(t)
	p := f.change(t, f.create(t, "shared"), "attach", f.target, f.caller)
	actorB := ids.New()
	if err := f.db.InsertAPIKey(&database.APIKey{ID: actorB, Name: "deletion-b", KeyHash: "fixture-b", Prefix: "fixture", Permissions: `["read","admin"]`}); err != nil {
		t.Fatal(err)
	}
	ctxB := auth.WithOperator(t.Context(), &auth.Principal{Kind: "api_key", ID: actorB})
	type outcome struct {
		owner string
		ctx   context.Context
		err   error
	}
	done := make(chan outcome, 2)
	start := make(chan struct{})
	for _, item := range []outcome{{owner: f.target, ctx: f.ctx}, {owner: f.caller, ctx: ctxB}} {
		go func(item outcome) { <-start; item.err = f.reg.Delete(item.ctx, item.owner); done <- item }(item)
	}
	close(start)
	for i := 0; i < 2; i++ {
		var item outcome
		select {
		case item = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent owner deletion blocked")
		}
		if item.err != nil {
			if !errors.Is(item.err, database.ErrIAMConflict) {
				t.Fatal(item.err)
			}
			if err := f.reg.Delete(item.ctx, item.owner); err != nil {
				t.Fatal(err)
			}
		}
	}
	current, err := f.db.GetIAMManagedPolicy(f.ctx, p.ID)
	if err != nil || current.MembershipRevision != p.MembershipRevision+2 {
		t.Fatal("concurrent deletions lost membership CAS", err)
	}
	for _, item := range []struct{ owner, actor string }{{f.target, f.ref.ID}, {f.caller, actorB}} {
		entries, err := f.s.List(f.ctx, item.owner, database.HistorySelector{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range entries {
			if e.Kind != "delete" {
				continue
			}
			var root struct {
				Actor   database.IAMActorReference       `json:"actor"`
				Sources []database.IAMOwnerDeletionAudit `json:"managed_sources"`
			}
			if json.Unmarshal(e.Metadata, &root) != nil || root.Actor.ID != item.actor || len(root.Sources) != 1 || root.Sources[0].OwnerID != item.owner {
				t.Fatal("deletion actor/identity crossed requests")
			}
			found = true
		}
		if !found {
			t.Fatal("missing retained deletion root")
		}
	}
	f.ledger(t)

	g := coordinated(t)
	q := g.change(t, g.create(t, "callback"), "attach", g.target, g.caller)
	if err = g.reg.DeleteWithDrain(g.ctx, g.target, func() {
		for _, lock := range []*sync.Mutex{g.reg.MutationLock(g.target), g.reg.PolicyMutationLock(q.ID)} {
			if !lock.TryLock() {
				t.Error("retirement callback held owner/definition lock")
			} else {
				lock.Unlock()
			}
		}
		if err := g.s.Authority.ChangeHostState(func() {}); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
}
