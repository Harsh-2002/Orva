package sdkhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func invokeIAMEvaluator(t *testing.T, owner, target string, revision uint64, outbound, inbound bool) *workloadiam.Evaluator {
	t.Helper()
	p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}}
	if outbound {
		p.Allow = append(p.Allow, workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.FunctionsInvoke}, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: target}}})
	}
	if inbound {
		p.Inbound = append(p.Inbound, workloadiam.Acceptance{CallerID: iamOwner, Triggers: []workloadiam.Trigger{workloadiam.F2F}})
	}
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: owner, Revision: revision, ApprovedArtifact: "reviewed"}, p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func invokeIAMAuthority(t *testing.T, outbound, inbound bool) *workloadiam.Authority {
	t.Helper()
	a, err := workloadiam.NewAuthority(1, []*workloadiam.Evaluator{
		invokeIAMEvaluator(t, iamOwner, iamOther, 1, outbound, false),
		invokeIAMEvaluator(t, iamOther, iamOther, 1, false, inbound),
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// This provider is test-only; production still has no active/generation source.
func invokeIAMTarget(id string, worker *sandbox.Worker) workloadiam.TargetState {
	s, _ := worker.WorkloadState()
	return workloadiam.TargetState{Active: true, Principal: workloadiam.Principal{
		FunctionID: id, WorkerID: s.Identity.WorkerID, ArtifactRevision: s.Identity.ArtifactRevision,
	}}
}

func TestIAMInvokeTargetJail(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--iam-invoke-target") {
		return
	}
	if iamWriteFrame(os.Stdout, []byte(`{"type":"ready"}`)) != nil {
		os.Exit(10)
	}
	for {
		if _, err := iamReadFrame(os.Stdin); err != nil {
			os.Exit(0)
		}
		if iamWriteFrame(os.Stdout, []byte(`{"type":"response","statusCode":200}`)) != nil {
			os.Exit(11)
		}
	}
}

func spawnIAMInvokeTarget(t *testing.T, id, artifact string, language sandbox.Language) *sandbox.Worker {
	t.Helper()
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		"node/usr/local/bin/node": "", "node/opt/orva/adapter.js": "//\n",
		"python/usr/local/bin/python3": "", "python/opt/orva/adapter.py": "#\n",
		"jail": fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestIAMInvokeTargetJail$ -- --iam-invoke-target \"$@\"\n", exe),
	} {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	exited := make(chan struct{})
	w, err := sandbox.Spawn(ctx, sandbox.ExecConfig{FunctionID: id, ArtifactRevision: artifact, Language: language, RootfsDir: root, CodeDir: root, NsjailBin: filepath.Join(root, "jail"), OnExit: func() { close(exited) }})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = w.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("target fake jail not reaped")
		}
	})
	if err := w.AwaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestIAMInvokeBeginDenialsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		outbound, inbound, provider bool
	}{
		{"missing outbound", false, true, true}, {"missing inbound", true, false, true}, {"missing provider", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := iamDB(t)
			a := invokeIAMAuthority(t, tc.outbound, tc.inbound)
			h := New(db, WithIAM(a), WithInvoker(&handlers.InternalInvokeHandler{DB: db}))
			if tc.provider {
				WithIAMTargetState(invokeIAMTarget)(h)
			}
			f, boot := spawnIAMWorker(t, h, a)
			f.ready(t, boot)
			c, finish := f.execution(t)
			defer finish()
			iamCall(t, h, c.ctx, c.caller, "invoke", `{"name":"iam-1"}`, 403)
		})
	}
	db := iamDB(t)
	a := invokeIAMAuthority(t, true, true)
	h := New(db, WithIAM(a), WithIAMTargetState(invokeIAMTarget), WithInvoker(&handlers.InternalInvokeHandler{DB: db}))
	f, boot := spawnIAMWorker(t, h, a)
	f.ready(t, boot)
	c, finish := f.execution(t)
	defer finish()
	for _, name := range []string{"missing", "iam-1"} {
		if name == "iam-1" {
			_, err := db.WriteDB().Exec(`UPDATE functions SET status='inactive' WHERE id=?`, iamOther)
			if err != nil {
				t.Fatal(err)
			}
		}
		iamCall(t, h, c.ctx, c.caller, "invoke", `{"name":"`+name+`"}`, 403)
	}
	for _, args := range []string{`{`, `{"name":"missing","worker_id":"forged"}`, `{"name":"missing","body":"` + strings.Repeat("x", maxInvokeBody) + `"}`} {
		iamCall(t, h, c.ctx, c.caller, "invoke", args, 400)
	}
	for _, op := range []string{"invoke.stream.open", "invoke.stream.next"} {
		iamCall(t, h, c.ctx, c.caller, op, `{"name":"iam-1"}`, 403)
	}
}

func TestIAMInvokeObservedAdmission(t *testing.T) {
	for _, language := range []sandbox.Language{sandbox.Node, sandbox.Python} {
		t.Run(string(language), func(t *testing.T) {
			db := iamDB(t)
			a := invokeIAMAuthority(t, true, true)
			var active atomic.Bool
			active.Store(true)
			h := New(db, WithIAM(a), WithInvoker(&handlers.InternalInvokeHandler{DB: db}), WithIAMTargetState(func(id string, w *sandbox.Worker) workloadiam.TargetState {
				s := invokeIAMTarget(id, w)
				s.Active = active.Load()
				return s
			}))
			f, boot := spawnIAMWorker(t, h, a)
			f.ready(t, boot)
			c, finish := f.execution(t)
			defer finish()
			target := spawnIAMInvokeTarget(t, iamOther, "reviewed", language)
			stale := spawnIAMInvokeTarget(t, iamOther, "old", language)
			wrong := spawnIAMInvokeTarget(t, iamOwner, "reviewed", language)
			begin := func() *workloadiam.Admission {
				t.Helper()
				_, call, err := h.beginIAMInvoke(c.ctx, c.caller, json.RawMessage(`{"name":"iam-1"}`))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(call.Finish)
				return call
			}
			admit := func(call *workloadiam.Admission, w *sandbox.Worker) *handlers.InvokeError {
				return h.admitIAMInvoke(c.ctx, c.caller, *c.caller.Exec, call, iamOther, w)
			}
			for name, w := range map[string]*sandbox.Worker{"old artifact": stale, "wrong function": wrong, "unowned": {}, "nil": nil} {
				t.Run(name, func(t *testing.T) {
					call := begin()
					defer call.Finish()
					if e := admit(call, w); e == nil || e.Status != 403 {
						t.Fatalf("admitted %s: %v", name, e)
					}
				})
			}
			call := begin()
			if err := a.ChangeHostState(func() { active.Store(false) }); err != nil {
				t.Fatal(err)
			}
			if admit(call, target) == nil {
				t.Fatal("inactive target admitted")
			}
			call.Finish()
			if err := a.ChangeHostState(func() { active.Store(true) }); err != nil {
				t.Fatal(err)
			}
			call = begin()
			if e := admit(call, target); e != nil {
				t.Fatal(e)
			}
			if err := a.Publish(1, invokeIAMEvaluator(t, iamOther, iamOther, 2, false, false), func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if call.Context().Err() == nil {
				t.Fatal("revocation did not cancel callee context")
			}
			call.Finish()
			if err := a.Publish(2, invokeIAMEvaluator(t, iamOther, iamOther, 3, false, true), func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			call = begin()
			if err := a.Publish(3, invokeIAMEvaluator(t, iamOther, iamOther, 4, false, true), func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if admit(call, target) == nil {
				t.Fatal("policy publication during wait admitted")
			}
			call.Finish()
			call = begin()
			call.Finish()
		})
	}
}

func TestIAMInvokePoolArtifactFailsClosed(t *testing.T) {
	e := newInvEnv(t, &database.Function{ID: iamOwner, Name: "iam-0"}, &database.Function{ID: iamOther, Name: "iam-1"})
	a := invokeIAMAuthority(t, true, true)
	h := New(e.db, WithIAM(a), WithInvoker(e.inv), WithIAMTargetState(invokeIAMTarget))
	f, boot := spawnIAMWorker(t, h, a)
	f.ready(t, boot)
	c, finish := f.execution(t)
	defer finish()
	for range 2 {
		iamCall(t, h, c.ctx, c.caller, "invoke", `{"name":"iam-1"}`, 403)
	}
	e.waitBusy(t, 0)
	if err := e.pm.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	iamCall(t, h, c.ctx, c.caller, "invoke", `{"name":"iam-1"}`, 403)
}

func TestIAMInvokeOriginalLeaseCancellationAndCapacity(t *testing.T) {
	db := iamDB(t)
	a := invokeIAMAuthority(t, true, true)
	h := New(db, WithIAM(a), WithIAMTargetState(invokeIAMTarget), WithInvoker(&handlers.InternalInvokeHandler{DB: db}))
	f, boot := spawnIAMWorker(t, h, a)
	f.ready(t, boot)
	c, finish := f.execution(t)
	_, call, e := h.beginIAMInvoke(c.ctx, c.caller, json.RawMessage(`{"name":"iam-1"}`))
	if e != nil {
		t.Fatal(e)
	}
	defer call.Finish()
	iamCall(t, h, c.ctx, c.caller, "invoke", `{"name":"iam-1"}`, 403)
	finish()
	select {
	case <-call.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("waiting admission outlived original execution")
	}
	call.Finish()
	next, finishNext := f.execution(t)
	defer finishNext()
	iamCall(t, h, c.ctx, c.caller, "invoke", `{"name":"iam-1"}`, 403)
	_, fresh, e := h.beginIAMInvoke(next.ctx, next.caller, json.RawMessage(`{"name":"iam-1"}`))
	if e != nil {
		t.Fatal("cancelled wait leaked registration capacity", e)
	}
	defer fresh.Finish()
	target := spawnIAMInvokeTarget(t, iamOther, "reviewed", sandbox.Node)
	dead, cancel := context.WithCancel(next.ctx)
	cancel()
	if h.admitIAMInvoke(dead, next.caller, *next.caller.Exec, fresh, iamOther, target) == nil {
		t.Fatal("cancelled SDK context admitted")
	}
}

func TestIAMInvokeSelfAdmissionDoesNotTakeWorkerMutex(t *testing.T) {
	db := iamDB(t)
	a, err := workloadiam.NewAuthority(1, []*workloadiam.Evaluator{invokeIAMEvaluator(t, iamOwner, iamOwner, 1, true, true)})
	if err != nil {
		t.Fatal(err)
	}
	h := New(db, WithIAM(a), WithIAMTargetState(invokeIAMTarget), WithInvoker(&handlers.InternalInvokeHandler{DB: db}))
	f, boot := spawnIAMWorker(t, h, a)
	f.ready(t, boot)
	c, finish := f.execution(t)
	defer finish()
	_, call, e := h.beginIAMInvoke(c.ctx, c.caller, json.RawMessage(`{"name":"iam-0"}`))
	if e != nil {
		t.Fatal(e)
	}
	defer call.Finish()
	done := make(chan *handlers.InvokeError, 1)
	go func() { done <- h.admitIAMInvoke(c.ctx, c.caller, *c.caller.Exec, call, iamOwner, f.w) }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("self-admission blocked on caller dispatch mutex")
	}
}
