package workloadpolicy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const emptyPolicy = `{"schema_version":1,"allow":[],"inbound":[]}`

type fixture struct {
	db      *database.Database
	sql     *sql.DB
	gate    *workloadiam.Authority
	service *Service
	ref     database.OperatorCredentialReference
	owner   string
	stored  *database.IAMPolicyRecord
	state   ArtifactState
	body    string
}

func newFixture(t *testing.T, blocked bool) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	source, err := parser.ParseFile(token.NewFileSet(), "../database/workload_iam_schema.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	ast.Inspect(source, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		name, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || name.Sel.Name != "ExecContext" {
			return true
		}
		if text, ok := call.Args[1].(*ast.BasicLit); ok {
			schema, _ = strconv.Unquote(text.Value)
		}
		return true
	})
	if schema == "" {
		t.Fatal("the canonical staged schema was not found")
	}
	if _, err := conn.Exec(schema); err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, sql: conn, owner: ids.New(), ref: database.OperatorCredentialReference{Kind: "api_key", ID: ids.New()}}
	if err := db.InsertFunction(&database.Function{ID: f.owner, Name: "managed", Runtime: "node", Status: "active", Entrypoint: "handler.js"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAPIKey(&database.APIKey{ID: f.ref.ID, Name: "dummy-admin", KeyHash: "dummy-hash", Prefix: "dummy", Permissions: `["admin"]`}); err != nil {
		t.Fatal(err)
	}
	f.stored, err = db.NewIAMPolicy(context.Background(), f.owner, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
	if err != nil {
		t.Fatal(err)
	}
	f.body = fmt.Sprintf(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"state"}},"context":"execution"},{"actions":["functions.invoke"],"resource":{"function":{"id":%q}},"context":"execution"}],"inbound":[{"caller_id":%q,"triggers":["f2f"]}]}`, f.owner, f.owner)
	f.state = ArtifactState{OwnerID: f.owner, Artifact: strings.Repeat("a", 64), Known: true}
	f.stored, err = db.SaveIAMPolicy(context.Background(), database.IAMPolicyChange{
		OwnerID: f.owner, ExpectedRevision: f.stored.Revision, ExpectedApprovedArtifact: f.stored.ApprovedArtifact,
		ApprovedArtifact: f.state.Artifact, PolicyJSON: []byte(f.body), ReviewRequired: blocked, Quarantined: blocked,
		Actor: database.IAMActorReference{Kind: "system", ID: ids.New()},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := f.stored.AdmissionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	f.gate, err = workloadiam.NewAuthority(16, []*workloadiam.Evaluator{compiled})
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = New(db, registry.New(db), f.gate, func(string) ArtifactState { return f.state })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) expected() Expected {
	return Expected{Revision: f.stored.Revision, CurrentArtifact: f.state.Artifact, ApprovedArtifact: f.stored.ApprovedArtifact}
}

func (f *fixture) worker(artifact string) workloadiam.Context {
	return workloadiam.Context{
		Principal: workloadiam.Principal{FunctionID: f.owner, WorkerID: "worker", ArtifactRevision: artifact},
		Phase:     workloadiam.Execution, Ready: true,
		Lease: workloadiam.Lease{ID: "lease", WorkerID: "worker", ExecutionID: ids.New(), Active: true},
	}
}

func (f *fixture) read(artifact string) error {
	key := "state"
	return f.gate.Do(f.worker(artifact), workloadiam.Request{
		Operation: workloadiam.OpKVGet, FunctionID: f.owner,
		Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}},
	}, func() error { return nil })
}

func TestReplacePublishesAndRevokesRegisteredCalls(t *testing.T) {
	f := newFixture(t, false)
	ctx := f.worker(f.state.Artifact)
	call, err := f.gate.BeginCall(context.Background(), ctx, workloadiam.Request{
		Operation: workloadiam.OpInvoke, FunctionID: f.owner,
		Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: f.owner}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	target := ctx.Principal
	target.WorkerID = "target-worker"
	if err := call.Admit(ctx, workloadiam.TargetState{Active: true, Principal: target}); err != nil {
		t.Fatal(err)
	}
	saved, err := f.service.Replace(context.Background(), f.ref, f.owner, f.expected(), []byte(emptyPolicy))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != f.stored.Revision+1 || saved.AuthorityEpoch != f.stored.AuthorityEpoch+1 ||
		f.read(f.state.Artifact) != workloadiam.ErrDenied || call.Context().Err() == nil {
		t.Fatal("committed revocation was not published/cancelled before acknowledgement")
	}
	audit, err := f.db.ListIAMPolicyAudit(context.Background(), f.owner)
	if err != nil || audit[0].Actor != (database.IAMActorReference{Kind: f.ref.Kind, ID: f.ref.ID}) {
		t.Fatal("transaction did not derive the authenticated actor")
	}
}

func TestReviewRequiresExactCurrentArtifactAndExplicitApproval(t *testing.T) {
	f := newFixture(t, true)
	oldArtifact := f.state.Artifact
	if err := f.gate.ChangeHostState(func() { f.state.Artifact = strings.Repeat("b", 64) }); err != nil {
		t.Fatal(err)
	}
	expected := f.expected()
	saved, err := f.service.Replace(context.Background(), f.ref, f.owner, expected, []byte(f.body))
	if err != nil || !saved.Quarantined || !saved.ReviewRequired || f.read(f.state.Artifact) != workloadiam.ErrDenied {
		t.Fatalf("editing silently approved retained authority: %v", err)
	}
	f.stored = saved
	expected = f.expected()
	expected.CurrentArtifact = oldArtifact
	if saved, err := f.service.Review(context.Background(), f.ref, f.owner, expected); saved != nil || err != database.ErrIAMConflict {
		t.Fatalf("stale reviewed current artifact accepted: %v", err)
	}
	saved, err = f.service.Review(context.Background(), f.ref, f.owner, f.expected())
	if err != nil || saved.Quarantined || saved.ReviewRequired || saved.PolicyJSON != f.body {
		t.Fatalf("explicit exact review failed: %v", err)
	}
	if f.read(oldArtifact) != workloadiam.ErrDenied || f.read(f.state.Artifact) != nil {
		t.Fatal("new approval was inherited by an obsolete worker")
	}
}

func TestUnavailableArtifactAllowsOnlyCompleteRevocation(t *testing.T) {
	for _, missingObserver := range []bool{false, true} {
		t.Run(fmt.Sprint(missingObserver), func(t *testing.T) {
			f := newFixture(t, false)
			f.state = ArtifactState{}
			if missingObserver {
				f.service.observe = nil
			}
			expected := f.expected()
			if saved, err := f.service.Replace(context.Background(), f.ref, f.owner, expected, []byte(f.body)); saved != nil || err != ErrUnavailable {
				t.Fatalf("missing current artifact granted authority: %v", err)
			}
			if saved, err := f.service.Review(context.Background(), f.ref, f.owner, expected); saved != nil || err != ErrUnavailable {
				t.Fatalf("missing current artifact permitted review: %v", err)
			}
			saved, err := f.service.Replace(context.Background(), f.ref, f.owner, expected, []byte(emptyPolicy))
			if err != nil || saved.ApprovedArtifact != f.stored.ApprovedArtifact || f.read(f.stored.ApprovedArtifact) != workloadiam.ErrDenied {
				t.Fatalf("broken runtime prevented safe total revocation: %v", err)
			}
		})
	}
}

func TestPublicationFailureLeavesLiveAuthorityAndStorageUnchanged(t *testing.T) {
	for _, cause := range []string{"operator", "audit", "current", "identity"} {
		t.Run(cause, func(t *testing.T) {
			f := newFixture(t, false)
			expected := f.expected()
			call, err := f.gate.BeginCall(context.Background(), f.worker(f.state.Artifact), workloadiam.Request{
				Operation: workloadiam.OpInvoke, FunctionID: f.owner,
				Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: f.owner}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer call.Finish()
			switch cause {
			case "operator":
				if err := f.db.DeleteAPIKey(f.ref.ID); err != nil {
					t.Fatal(err)
				}
			case "audit":
				if _, err := f.sql.Exec(`CREATE TRIGGER control_fail BEFORE INSERT ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'private-audit-detail'); END`); err != nil {
					t.Fatal(err)
				}
			case "current":
				f.state.Artifact = strings.Repeat("c", 64)
			case "identity":
				f.state.OwnerID = ids.New()
			}
			saved, err := f.service.Replace(context.Background(), f.ref, f.owner, expected, []byte(emptyPolicy))
			if saved != nil || err == nil || strings.Contains(err.Error(), "private-audit-detail") {
				t.Fatalf("failed publish returned success/diagnostic: %v", err)
			}
			persisted, err := f.db.GetIAMPolicy(context.Background(), f.owner)
			if err != nil || persisted.Revision != f.stored.Revision || persisted.PolicyJSON != f.stored.PolicyJSON ||
				f.read(f.stored.ApprovedArtifact) != nil || call.Context().Err() != nil {
				t.Fatal("failed persistence/current-state check changed live authority")
			}
		})
	}
}

func TestServiceBoundsAndCancelledRequest(t *testing.T) {
	f := newFixture(t, false)
	for _, revision := range []uint64{0, ^uint64(0)} {
		expected := f.expected()
		expected.Revision = revision
		if saved, err := f.service.Replace(context.Background(), f.ref, f.owner, expected, []byte(f.body)); saved != nil || err != database.ErrIAMInvalidPolicy {
			t.Fatalf("invalid revision admitted: %v", err)
		}
	}
	for _, body := range []string{`null`, `{"schema_version":1,"allow":null,"inbound":[]}`, strings.Repeat(" ", 32769)} {
		if saved, err := f.service.Replace(context.Background(), f.ref, f.owner, f.expected(), []byte(body)); saved != nil || err != database.ErrIAMInvalidPolicy {
			t.Fatalf("invalid authorable policy admitted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if saved, err := f.service.Replace(ctx, f.ref, f.owner, f.expected(), []byte(emptyPolicy)); saved != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request admitted or lost cancellation: %v", err)
	}
	if f.read(f.state.Artifact) != nil {
		t.Fatal("invalid/cancelled input changed active authority")
	}
}

func TestServiceConcurrentCAS(t *testing.T) {
	f := newFixture(t, false)
	expected := f.expected()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range cap(results) {
		wg.Go(func() {
			_, err := f.service.Replace(context.Background(), f.ref, f.owner, expected, []byte(emptyPolicy))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, database.ErrIAMConflict) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if winners != 1 {
		t.Fatal("policy publication did not have exactly one winner")
	}
}
