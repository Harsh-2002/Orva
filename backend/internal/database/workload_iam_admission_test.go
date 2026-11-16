package database

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func TestWorkloadIAMAdmissionSnapshotsWithholdUnapprovedAuthority(t *testing.T) {
	db, r := iamTestSetup(t)
	doc := fmt.Sprintf(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"private-key-selector"}},"context":"execution"}],"inbound":[{"caller_id":%q,"triggers":["f2f"]}]}`, iamTestOther)
	key := "private-key-selector"
	req := workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: iamTestOwner, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}
	ctx := workloadiam.Context{
		Principal: workloadiam.Principal{FunctionID: iamTestOwner, WorkerID: "worker", ArtifactRevision: "host-config-artifact-v1"},
		Phase:     workloadiam.Execution, Ready: true,
		Lease: workloadiam.Lease{ID: "lease", WorkerID: "worker", ExecutionID: iamTestOther, Active: true},
	}
	for _, flags := range []struct{ review, quarantine bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("review=%v/quarantine=%v", flags.review, flags.quarantine), func(t *testing.T) {
			change := iamTestChange(r, doc)
			change.ReviewRequired, change.Quarantined = flags.review, flags.quarantine
			var err error
			r, err = db.SaveIAMPolicy(context.Background(), change)
			if err != nil {
				t.Fatal(err)
			}
			e, err := r.AdmissionEvaluator()
			if err != nil || e.Metadata() != r.Metadata {
				t.Fatalf("snapshot lost current revision: evaluator=%v error=%v", e, err)
			}
			want := !flags.review && !flags.quarantine
			if e.Allows(ctx, req) != want || e.Accepts(iamTestOther, workloadiam.F2F) != want {
				t.Fatalf("unapproved authority admitted (want active=%v)", want)
			}
			stored, err := db.GetIAMPolicy(context.Background(), r.OwnerID)
			if err != nil || stored.PolicyJSON != r.PolicyJSON || stored.Revision != r.Revision {
				t.Fatalf("compiling changed retained policy: record=%v error=%v", stored, err)
			}
		})
	}
}

func TestWorkloadIAMAdmissionSnapshotRejectsMalformedRetainedState(t *testing.T) {
	_, r := iamTestSetup(t)
	for _, bad := range []*IAMPolicyRecord{nil, {}, {Metadata: r.Metadata, AuthorityEpoch: math.MaxUint64, PolicyJSON: r.PolicyJSON}} {
		if _, err := bad.AdmissionEvaluator(); err == nil {
			t.Fatal("invalid record compiled")
		}
	}
	r.Quarantined = true
	r.PolicyJSON = `{"schema_version":1,"allow":null,"inbound":[]}`
	if _, err := r.AdmissionEvaluator(); err == nil {
		t.Fatal("quarantine masked malformed retained policy")
	}
}
