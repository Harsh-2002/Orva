package sdkhost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
)

func TestCronSDKExplicitNilAndAuthorClaimsDeny(t *testing.T) {
	db := iamDB(t)
	a := iamAuthority(t)
	service := &workloadasync.Service{DB: db, Authority: a}
	caller := sandbox.SDKCaller{FunctionID: iamOwner, Exec: &sandbox.ExecInfo{ExecutionID: iamExecution, Lease: "untrusted-test-claim"}}
	for _, h := range []*Host{New(db, WithCronIAM(nil)), New(db, WithIAM(a)), New(db, WithIAM(a), WithCronIAM(nil))} {
		iamCall(t, h, context.Background(), caller, "crons.upsert", `{"name":"daily","schedule":"@daily"}`, 403)
	}
	h := New(db, WithIAM(a), WithCronIAM(service))
	for _, claim := range []string{`"origin_kind":"operator"`, `"operator_id":"admin"`, `"creator_id":"other"`, `"function_id":"other"`, `"authority_state":"active"`, `"config_revision":"1"`} {
		_, err := h.Handle(t.Context(), caller, "crons.upsert", json.RawMessage(`{"name":"daily","schedule":"@daily",`+claim+`}`))
		if err == nil || err.Status != 400 {
			t.Fatal("author identity field was not rejected", claim)
		}
	}
	iamCall(t, h, t.Context(), caller, "crons.upsert", `{"name":"daily","schedule":"@daily"}`, 403)
	rows, err := db.ListAllCronSchedules()
	if err != nil || len(rows) != 0 {
		t.Fatal("forged lease wrote schedule", err)
	}
}
func TestCronSDKPayloadKeepsLegacyStringSemantics(t *testing.T) {
	for _, test := range []struct {
		in   any
		want string
		bad  bool
	}{
		{nil, "{}", false}, {` {"ok":true} `, `{"ok":true}`, false}, {"", "{}", false},
		{map[string]any{"a": 1}, `{"a":1}`, false}, {"not json", "", true},
	} {
		raw, err := cronPayload(test.in)
		if (err != nil) != test.bad || (!test.bad && string(raw) != test.want) {
			t.Fatal("payload changed shape", test.in, err)
		}
	}
}
