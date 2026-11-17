package workloadpolicy

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const sourceReader = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"state"}},"context":"execution"}],"inbound":[]}`

type managedFixture struct {
	*fixture
	other  string
	states map[string]ArtifactState
}

func managedFixtureForTest(t *testing.T, blocked bool) *managedFixture {
	t.Helper()
	f := newFixture(t, blocked)
	other := ids.New()
	if err := f.db.InsertFunction(&database.Function{ID: other, Name: "managed-second", Runtime: "python", Status: "active", Entrypoint: "handler.py"}); err != nil {
		t.Fatal(err)
	}
	row, err := f.db.NewIAMPolicy(t.Context(), other, blocked, database.IAMActorReference{Kind: "system", ID: ids.New()})
	if err != nil {
		t.Fatal(err)
	}
	e, err := row.AdmissionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.gate.Publish(0, e, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "../database/workload_managed_schema.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	ast.Inspect(file, func(n ast.Node) bool {
		v, ok := n.(*ast.ValueSpec)
		if ok && len(v.Names) == 1 && v.Names[0].Name == "managedIAMSchema" {
			lit := v.Values[0].(*ast.BasicLit)
			schema, _ = strconv.Unquote(lit.Value)
		}
		return true
	})
	if schema == "" {
		t.Fatal("canonical managed schema absent")
	}
	if _, err := f.sql.Exec(schema); err != nil {
		t.Fatal(err)
	}
	m := &managedFixture{fixture: f, other: other, states: map[string]ArtifactState{
		f.owner: f.state, other: {OwnerID: other, Artifact: strings.Repeat("b", 64), Known: true},
	}}
	f.service, err = NewManaged(f.db, registry.New(f.db), f.gate, func(id string) ArtifactState { return m.states[id] })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *managedFixture) expectations(t *testing.T, owners ...string) []database.IAMOwnerExpectation {
	t.Helper()
	out := []database.IAMOwnerExpectation{}
	for _, id := range owners {
		row, err := f.db.GetIAMPolicy(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, database.IAMOwnerExpectation{OwnerID: id, Revision: row.Revision, ApprovedArtifact: row.ApprovedArtifact, CurrentArtifact: f.states[id].Artifact})
	}
	return out
}

func (f *managedFixture) change(t *testing.T, p *database.IAMManagedPolicy, op string, owners ...string) *ManagedResult {
	t.Helper()
	r := database.IAMManagedMutation{Operation: op, PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Owners: f.expectations(t, owners...)}
	if op == "attach" {
		r.Version = 1
	}
	result, err := f.service.MutateManaged(t.Context(), f.ref, r)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f *managedFixture) create(t *testing.T, name, body string) *database.IAMManagedPolicy {
	t.Helper()
	result, err := f.service.MutateManaged(t.Context(), f.ref, database.IAMManagedMutation{Operation: "create", Name: name, PolicyJSON: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	return result.Policy
}

func (f *managedFixture) allows(id, artifact string, phase workloadiam.Phase) bool {
	c := f.worker(artifact)
	c.Principal.FunctionID = id
	if phase == workloadiam.Bootstrap {
		c.Ready = false
		c.Phase = phase
		c.Lease = workloadiam.Lease{}
	}
	key := "state"
	return f.gate.Do(c, workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: id, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}, func() error { return nil }) == nil
}

func TestManagedServiceTwoOwnerPinnedRolloutAndOverlap(t *testing.T) {
	f := managedFixtureForTest(t, false)
	p := f.create(t, "reader", sourceReader)
	q := f.create(t, "overlapping-reader", sourceReader)
	p = f.change(t, p, "attach", f.owner, f.other).Policy
	q = f.change(t, q, "attach", f.owner, f.other).Policy
	for _, owner := range []string{f.owner, f.other} {
		if !f.allows(owner, f.states[owner].Artifact, workloadiam.Execution) || f.allows(owner, f.states[owner].Artifact, workloadiam.Bootstrap) {
			t.Fatal("own read/execution separation failed")
		}
	}
	// Version creation changes neither attachment pins nor owner revision/epoch.
	before, _ := f.db.GetIAMPolicy(t.Context(), f.other)
	result, err := f.service.MutateManaged(t.Context(), f.ref, database.IAMManagedMutation{Operation: "update", PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, PolicyJSON: []byte(emptyPolicy)})
	if err != nil {
		t.Fatal(err)
	}
	p = result.Policy
	after, _ := f.db.GetIAMPolicy(t.Context(), f.other)
	if *before != *after || !f.allows(f.other, f.states[f.other].Artifact, workloadiam.Execution) {
		t.Fatal("version update autopropagated")
	}
	// Explicit rollout of v2 removes only that contribution.
	_, err = f.service.MutateManaged(t.Context(), f.ref, database.IAMManagedMutation{Operation: "attach", PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Version: 2, Owners: f.expectations(t, f.owner, f.other)})
	if err != nil {
		t.Fatal(err)
	}
	q = f.change(t, q, "revoke", f.owner, f.other).Policy
	if f.allows(f.other, f.states[f.other].Artifact, workloadiam.Execution) || !f.allows(f.owner, f.states[f.owner].Artifact, workloadiam.Execution) {
		t.Fatal("revoke lost inline residual or kept revoked managed grant")
	}
	first, err := f.db.GetIAMSources(t.Context(), f.owner)
	if err != nil || first.InlineJSON != f.body || len(first.Attachments) != 2 {
		t.Fatal("source attribution lost", err)
	}
	for _, owner := range []string{f.owner, f.other} {
		audit, err := f.db.ListIAMPolicyAudit(t.Context(), owner)
		if err != nil || audit[0].Actor.ID != f.ref.ID {
			t.Fatal("actor/audit parity", err)
		}
	}
}

func TestManagedServiceBatchArtifactConflictIsAllOrNone(t *testing.T) {
	f := managedFixtureForTest(t, false)
	p := f.create(t, "reader", sourceReader)
	request := database.IAMManagedMutation{Operation: "attach", PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Version: 1, Owners: f.expectations(t, f.owner, f.other)}
	beforeA, _ := f.db.GetIAMPolicy(t.Context(), f.owner)
	beforeB, _ := f.db.GetIAMPolicy(t.Context(), f.other)
	if err := f.gate.ChangeHostState(func() {
		state := f.states[f.other]
		state.Artifact = strings.Repeat("c", 64)
		f.states[f.other] = state
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.MutateManaged(t.Context(), f.ref, request); result != nil || err != database.ErrIAMConflict {
		t.Fatal("stale one-owner artifact committed batch", err)
	}
	afterA, _ := f.db.GetIAMPolicy(t.Context(), f.owner)
	afterB, _ := f.db.GetIAMPolicy(t.Context(), f.other)
	if *beforeA != *afterA || *beforeB != *afterB {
		t.Fatal("partial durable owner mutation")
	}
	unchanged, _ := f.db.GetIAMManagedPolicy(t.Context(), p.ID)
	if *unchanged != *p || f.allows(f.other, strings.Repeat("c", 64), workloadiam.Execution) {
		t.Fatal("partial memory or membership publication")
	}
}

func TestManagedServiceSourceReductionWithUnknownArtifacts(t *testing.T) {
	f := managedFixtureForTest(t, true)
	p := f.create(t, "reader", sourceReader)
	p = f.change(t, p, "attach", f.owner, f.other).Policy
	before, _ := f.db.GetIAMPolicy(t.Context(), f.owner)
	if err := f.gate.ChangeHostState(func() { f.states[f.owner] = ArtifactState{}; f.states[f.other] = ArtifactState{} }); err != nil {
		t.Fatal(err)
	}
	p = f.change(t, p, "revoke", f.owner, f.other).Policy
	after, _ := f.db.GetIAMPolicy(t.Context(), f.owner)
	if after.ApprovedArtifact != before.ApprovedArtifact || !after.Quarantined || !after.ReviewRequired || after.PolicyJSON != f.body {
		t.Fatal("reduction rebound or approved retained inline")
	}
	if f.allows(f.owner, before.ApprovedArtifact, workloadiam.Execution) {
		t.Fatal("quarantine activated")
	}
	expect := f.expectations(t, f.owner)[0]
	expected := Expected{Revision: expect.Revision, ApprovedArtifact: expect.ApprovedArtifact, CurrentArtifact: expect.CurrentArtifact}
	if _, err := f.service.Review(t.Context(), f.ref, f.owner, expected); err == nil {
		t.Fatal("unknown artifact reviewed")
	}
	p = f.change(t, p, "detach", f.owner, f.other).Policy
	f.change(t, p, "delete")
}

func TestManagedServiceDeferredCommitFailureDoesNotPublishOrCancel(t *testing.T) {
	f := managedFixtureForTest(t, false)
	p := f.create(t, "reader", sourceReader)
	call, err := f.gate.BeginCall(t.Context(), f.worker(f.state.Artifact), workloadiam.Request{Operation: workloadiam.OpInvoke, FunctionID: f.owner, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: f.owner}}})
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	before, _ := f.db.GetIAMPolicy(t.Context(), f.owner)
	_, err = f.sql.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE managed_failure(n TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER managed_commit_failure AFTER INSERT ON workload_iam_attachments BEGIN INSERT INTO managed_failure VALUES('missing'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	request := database.IAMManagedMutation{Operation: "attach", PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Version: 1, Owners: f.expectations(t, f.owner, f.other)}
	if _, err := f.service.MutateManaged(t.Context(), f.ref, request); err == nil {
		t.Fatal("deferred COMMIT unexpectedly succeeded")
	}
	after, _ := f.db.GetIAMPolicy(t.Context(), f.owner)
	if *before != *after || call.Context().Err() != nil || !f.allows(f.owner, f.state.Artifact, workloadiam.Execution) {
		t.Fatal("failed COMMIT published/cancelled")
	}
}

func TestManagedServiceReplaceChangesOnlyInlineAndReviewExactAggregate(t *testing.T) {
	f := managedFixtureForTest(t, true)
	p := f.create(t, "reader", sourceReader)
	p = f.change(t, p, "attach", f.owner).Policy
	row, _ := f.db.GetIAMPolicy(t.Context(), f.owner)
	expected := Expected{Revision: row.Revision, ApprovedArtifact: row.ApprovedArtifact, CurrentArtifact: f.state.Artifact}
	row, err := f.service.Replace(t.Context(), f.ref, f.owner, expected, []byte(emptyPolicy))
	if err != nil || !row.Quarantined || !row.ReviewRequired {
		t.Fatal(err)
	}
	source, err := f.db.GetIAMSources(t.Context(), f.owner)
	if err != nil || source.InlineJSON != emptyPolicy || source.Record.PolicyJSON != sourceReader {
		t.Fatal("inline save overwrote attached contribution", err)
	}
	expected.Revision = row.Revision
	row, err = f.service.Review(t.Context(), f.ref, f.owner, expected)
	if err != nil || row.Quarantined || row.ReviewRequired || !f.allows(f.owner, f.state.Artifact, workloadiam.Execution) {
		t.Fatal("review did not approve exact aggregate", err)
	}
	f.change(t, p, "detach", f.owner)
	if f.allows(f.owner, f.state.Artifact, workloadiam.Execution) {
		t.Fatal("detach left authority")
	}
	// The legacy adapter must not render the effective union as inline authoring.
	adapter := &HTTPAdapter{Service: f.service, Authenticate: func(*http.Request) (*auth.Principal, error) { return nil, nil }}
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, httptest.NewRequest("GET", "/unused", nil))
	if w.Code != 503 {
		t.Fatal("legacy adapter exposed source-unaware managed editing")
	}
}

func TestManagedServiceConcurrentBatchesAndLiveOperatorRevocation(t *testing.T) {
	f := managedFixtureForTest(t, false)
	p := f.create(t, "reader", sourceReader)
	r := database.IAMManagedMutation{Operation: "attach", PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Version: 1, Owners: f.expectations(t, f.other, f.owner)}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := f.service.MutateManaged(context.Background(), f.ref, r); results <- err })
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, database.ErrIAMConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	p, _ = f.db.GetIAMManagedPolicy(t.Context(), p.ID)
	_, err := f.sql.Exec(`CREATE TRIGGER managed_remove_operator BEFORE UPDATE ON workload_iam_policies BEGIN DELETE FROM api_keys; END;`)
	if err != nil {
		t.Fatal(err)
	}
	r = database.IAMManagedMutation{Operation: "detach", PolicyID: p.ID, DefinitionRevision: p.DefinitionRevision, MembershipRevision: p.MembershipRevision, Owners: f.expectations(t, f.owner, f.other)}
	if _, err := f.service.MutateManaged(t.Context(), f.ref, r); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatal("revoked-at-commit operator published", err)
	}
	if !f.allows(f.other, f.states[f.other].Artifact, workloadiam.Execution) {
		t.Fatal("failed operator commit removed authority")
	}
}

func TestManagedServiceCancellationCallbackCanReenterOwnerLocks(t *testing.T) {
	f := managedFixtureForTest(t, false)
	p := f.create(t, "reader", sourceReader)
	call, err := f.gate.BeginCall(t.Context(), f.worker(f.state.Artifact), workloadiam.Request{Operation: workloadiam.OpInvoke, FunctionID: f.owner, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: f.owner}}})
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	done := make(chan struct{})
	stop := context.AfterFunc(call.Context(), func() {
		// Any callback may inspect the same owner's current state.
		lock := f.service.registry.MutationLock(f.owner)
		lock.Lock()
		lock.Unlock()
		_ = f.gate.ChangeHostState(func() {})
		close(done)
	})
	defer stop()
	f.change(t, p, "attach", f.owner, f.other)
	if call.Context().Err() == nil {
		t.Fatal("not cancelled before ACK")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("callback deadlocked on host locks")
	}
}
