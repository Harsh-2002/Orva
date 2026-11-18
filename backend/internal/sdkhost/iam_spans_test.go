package sdkhost

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func TestIAMSpansHostEnvelopeAndRevocation(t *testing.T) {
	db := iamDB(t)
	grant := workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.SpansEmit},
		Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}}
	a := iamAuthority(t, grant)
	var current atomic.Bool
	current.Store(true)
	h := New(db, WithIAM(a), WithIAMCurrentArtifact(func(string, string) bool { return current.Load() }))
	w, boot := spawnIAMWorker(t, h, a)
	args := `{"name":"owned","duration_ms":1,"attributes":{"synthetic":true}}`
	iamCall(t, h, boot.ctx, boot.caller, "spans.emit", args, 403)
	w.ready(t, boot)
	done := make(chan error, 1)
	go func() {
		_, _, err := w.w.Dispatch(sandbox.WithExecution(w.ctx, sandbox.ExecInfo{
			ExecutionID: iamExecution, TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), StartedAt: time.Now().UTC(),
		}), []byte(`{}`))
		done <- err
	}()
	call := w.next(t)
	iamCall(t, h, call.ctx, call.caller, "kv.get", `{"key":"x"}`, 403)
	iamCall(t, h, call.ctx, call.caller, "kv.list", `{"prefix":""}`, 403)
	iamCall(t, New(db, WithIAM(a)), call.ctx, call.caller, "spans.emit", args, 403)
	iamCall(t, New(db, WithIAM(a), WithIAMCurrentArtifact(nil)), call.ctx, call.caller, "spans.emit", args, 403)
	for _, bad := range []string{
		`{"name":"x","execution_id":"foreign"}`, `{"name":"x","lease":"forged"}`,
		`{"name":"x","duration_ms":-1}`, `{"name":"x","status":"other"}`, `{"name":3}`,
	} {
		iamCall(t, h, call.ctx, call.caller, "spans.emit", bad, 400)
	}
	forged := call.caller
	forged.FunctionID = iamOther
	iamCall(t, h, call.ctx, forged, "spans.emit", args, 403)
	forged = call.caller
	forged.Exec = &sandbox.ExecInfo{ExecutionID: iamOther, TraceID: "author", SpanID: "author", Lease: "forged"}
	raw := json.RawMessage(args)
	result, serr := h.Handle(call.ctx, forged, "spans.emit", raw)
	if serr != nil || result.(map[string]string)["status"] != "queued" {
		t.Fatal("span admission failed", serr)
	}
	for i := range raw {
		raw[i] = ' '
	}
	if err := a.Publish(1, iamEvaluator(t, 2, "reviewed"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	iamCall(t, h, call.ctx, call.caller, "spans.emit", args, 403)
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows, err := db.ListUserSpansByExecution(iamExecution)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 {
			if rows[0].TraceID != strings.Repeat("a", 32) || rows[0].ParentSpanID != strings.Repeat("b", 16) || rows[0].Attributes != `{"synthetic":true}` {
				t.Fatal("accepted envelope lost original host identity/owned attributes")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accepted span did not persist")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.Publish(2, iamEvaluator(t, 3, "reviewed", grant), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := a.ChangeHostState(func() { current.Store(false) }); err != nil {
		t.Fatal(err)
	}
	iamCall(t, h, call.ctx, call.caller, "spans.emit", args, 403)
	call.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	iamCall(t, h, call.ctx, call.caller, "spans.emit", args, 403)
}

func TestIAMSpansAdmissionSerializesRevoke(t *testing.T) {
	db := iamDB(t)
	grant := workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.SpansEmit},
		Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}}
	a := iamAuthority(t, grant)
	entered, release := make(chan struct{}), make(chan struct{})
	var pause atomic.Bool
	h := New(db, WithIAM(a), WithIAMCurrentArtifact(func(string, string) bool {
		if pause.Load() {
			close(entered)
			<-release
		}
		return true
	}))
	w, boot := spawnIAMWorker(t, h, a)
	w.ready(t, boot)
	done := make(chan error, 1)
	go func() {
		_, _, err := w.w.Dispatch(sandbox.WithExecution(w.ctx, sandbox.ExecInfo{ExecutionID: iamExecution,
			TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), StartedAt: time.Now()}), []byte(`{}`))
		done <- err
	}()
	c := w.next(t)
	pause.Store(true)
	emitted := make(chan *sandbox.SDKError, 1)
	go func() {
		_, err := h.Handle(c.ctx, c.caller, "spans.emit", json.RawMessage(`{"name":"before"}`))
		emitted <- err
	}()
	<-entered
	revoked := make(chan error, 1)
	go func() { revoked <- a.Publish(1, iamEvaluator(t, 2, "reviewed"), func() error { return nil }) }()
	select {
	case <-revoked:
		t.Fatal("revoke crossed emission admission")
	case <-time.After(25 * time.Millisecond):
	}
	pause.Store(false)
	close(release)
	if err := <-emitted; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	iamCall(t, h, c.ctx, c.caller, "spans.emit", `{"name":"after"}`, 403)
	c.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
