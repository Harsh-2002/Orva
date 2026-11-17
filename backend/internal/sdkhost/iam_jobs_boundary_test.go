package sdkhost

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func TestIAMJobsHostileOriginLeaseAndReplacement(t *testing.T) {
	db := iamDB(t)
	if err := db.InitAsyncSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	caller, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: iamOwner, Revision: 1, ApprovedArtifact: "reviewed"},
		workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{{
			Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.JobsEnqueue},
			Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: iamOther}},
		}}, Inbound: []workloadiam.Acceptance{}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: iamOther, Revision: 1, ApprovedArtifact: "reviewed"},
		workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{{
			CallerID: iamOwner, Triggers: []workloadiam.Trigger{workloadiam.Job},
		}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := workloadiam.NewAuthority(16, []*workloadiam.Evaluator{caller, target})
	if err != nil {
		t.Fatal(err)
	}
	var replaced atomic.Bool
	service := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(owner string) string {
		if owner == iamOwner && replaced.Load() {
			return "replacement"
		}
		return "reviewed"
	}}
	h := New(db, WithIAM(a), WithAsyncIAM(service), WithIAMCurrentArtifact(func(owner, artifact string) bool {
		return artifact == service.CurrentArtifact(owner)
	}))
	f, boot := spawnIAMWorker(t, h, a)
	args := `{"function_id":"` + iamOther + `","payload":{},"idempotency_key":"boundary"}`
	count := func(want int) {
		t.Helper()
		rows, err := db.ListJobs("", "", 100)
		if err != nil || len(rows) != want {
			t.Fatalf("denied SDK request changed jobs: count=%d want=%d err=%v", len(rows), want, err)
		}
	}
	iamCall(t, h, boot.ctx, boot.caller, "jobs.enqueue", args, 403)
	count(0)
	f.ready(t, boot)
	c, finish := f.execution(t)
	for _, field := range []string{
		`"origin_kind":"operator"`, `"creator_id":"` + iamOther + `"`,
		`"operator_id":"` + iamOwner + `"`, `"enqueued_by_function_id":"` + iamOther + `"`,
		`"trace_id":"author-controlled"`, `"lease":"forged"`,
	} {
		t.Run(field, func(t *testing.T) {
			iamCall(t, h, c.ctx, c.caller, "jobs.enqueue", args[:len(args)-1]+","+field+"}", 400)
			count(0)
		})
	}
	for _, forged := range []sandbox.SDKCaller{
		{FunctionID: iamOwner},
		{FunctionID: iamOwner, Exec: &sandbox.ExecInfo{ExecutionID: iamExecution, Lease: "forged"}},
	} {
		iamCall(t, h, c.ctx, forged, "jobs.enqueue", args, 403)
		count(0)
	}
	wrong := c.caller
	wrong.FunctionID = iamOther
	iamCall(t, h, c.ctx, wrong, "jobs.enqueue", args, 403)
	count(0)
	expired, cancel := context.WithCancel(c.ctx)
	cancel()
	iamCall(t, h, expired, c.caller, "jobs.enqueue", args, 403)
	count(0)
	replaced.Store(true)
	iamCall(t, h, c.ctx, c.caller, "jobs.enqueue", args, 403)
	count(0)
	replaced.Store(false)
	result := iamCall(t, h, c.ctx, c.caller, "jobs.enqueue", args, 200)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		OriginKind string `json:"origin_kind"`
		CreatorID  string `json:"creator_id"`
	}
	if err := json.Unmarshal(raw, &provenance); err != nil || provenance.OriginKind != "function" || provenance.CreatorID != iamOwner {
		t.Fatal("host-owned provenance not preserved", err)
	}
	count(1)
	finish()
	iamCall(t, h, c.ctx, c.caller, "jobs.enqueue", args, 403)
	next, end := f.execution(t)
	iamCall(t, h, c.ctx, c.caller, "jobs.enqueue", args, 403)
	count(1)
	end()
	iamCall(t, h, next.ctx, next.caller, "jobs.enqueue", args, 403)
	count(1)
}
