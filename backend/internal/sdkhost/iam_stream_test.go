package sdkhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type iamStreamFixture struct {
	e      *invEnv
	h      *Host
	a      *workloadiam.Authority
	c      iamCaptured
	target *sandbox.Worker
	table  *streamTable
}

func newIAMStreamFixture(t *testing.T, capacity int) *iamStreamFixture {
	t.Helper()
	e := newInvEnv(t, &database.Function{ID: iamOwner, Name: "iam-0"}, &database.Function{ID: iamOther, Name: "iam-1"})
	a, err := workloadiam.NewAuthority(capacity, []*workloadiam.Evaluator{
		invokeIAMEvaluator(t, iamOwner, iamOther, 1, true, false),
		invokeIAMEvaluator(t, iamOther, iamOther, 1, false, true),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := New(e.db, WithIAM(a), WithInvoker(e.inv), WithIAMTargetState(invokeIAMTarget))
	f, boot := spawnIAMWorker(t, h, a)
	f.ready(t, boot)
	c, finish := f.execution(t)
	t.Cleanup(finish)
	table, serr := sessionTable(c.ctx)
	if serr != nil {
		t.Fatal(serr)
	}
	t.Cleanup(table.Closed)
	return &iamStreamFixture{e, h, a, c, spawnIAMInvokeTarget(t, iamOther, "reviewed", sandbox.Node), table}
}

func (f *iamStreamFixture) begin(t *testing.T) *workloadiam.Admission {
	t.Helper()
	_, call, e := f.h.beginIAMCall(f.c.ctx, f.c.caller, json.RawMessage(`{"name":"iam-1"}`), workloadiam.OpStreamOpen)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(call.Finish)
	if e := f.h.admitIAMInvoke(f.c.ctx, f.c.caller, *f.c.caller.Exec, call, iamOther, f.target); e != nil {
		t.Fatal(e)
	}
	return call
}

// Serving pools have no attested artifacts yet. Exercise lifetime/handoff with
// a separately admitted host-owned test worker, not a fabricated pool identity.
func (f *iamStreamFixture) open(t *testing.T, body string) (string, *chStream) {
	t.Helper()
	call := f.begin(t)
	fn, ie := f.e.inv.ResolveCallee("iam-1")
	if ie != nil {
		t.Fatal(ie)
	}
	s, ie := f.e.inv.OpenStream(call.Context(), handlers.F2FCaller{FunctionID: iamOwner}, fn, []byte(body), "")
	if ie != nil {
		t.Fatal(ie)
	}
	return f.publish(t, call, s)
}

func (f *iamStreamFixture) publish(t *testing.T, call *workloadiam.Admission, s *handlers.F2FStream) (string, *chStream) {
	t.Helper()
	r, e := f.h.publishIAMStream(f.c.ctx, f.c.caller, f.table, s, &iamStream{call: call, worker: f.target, targetID: iamOther})
	if e != nil {
		t.Fatal(e)
	}
	var head struct {
		Handle string            `json:"handle"`
		Status int               `json:"status"`
		Header map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(r.(json.RawMessage), &head); err != nil || head.Handle == "" || head.Status == 0 || head.Header == nil {
		t.Fatalf("head: %s %v", r, err)
	}
	return head.Handle, f.table.lookup(head.Handle, f.c.caller.Exec.Lease)
}

func (f *iamStreamFixture) next(t *testing.T, handle string, status int) map[string]any {
	t.Helper()
	r := iamCall(t, f.h, f.c.ctx, f.c.caller, "invoke.stream.next", `{"handle":"`+handle+`"}`, status)
	if r == nil {
		return nil
	}
	return r.(map[string]any)
}

func (f *iamStreamFixture) revoke(t *testing.T) {
	t.Helper()
	done := make(chan error, 1)
	candidate := invokeIAMEvaluator(t, iamOther, iamOther, 2, false, false)
	go func() { done <- f.a.Publish(1, candidate, func() error { return nil }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation blocked on stream I/O or locks")
	}
}

func TestIAMStreamFramesBufferedAndChunkBound(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	for _, body := range []string{`{"mode":"stream"}`, `{"mode":"big"}`, `{"mode":"echo","echo":7}`} {
		handle, _ := f.open(t, body)
		var got strings.Builder
		for {
			m := f.next(t, handle, 200)
			if m["done"] == true {
				break
			}
			b, err := base64.StdEncoding.DecodeString(m["chunk_b64"].(string))
			if err != nil || len(b) > workloadiam.MaxHandoffBytes {
				t.Fatalf("unbounded/invalid reply: %d %v", len(b), err)
			}
			got.Write(b)
		}
		switch {
		case strings.Contains(body, `"big"`):
			if got.String() != strings.Repeat("z", 600000) {
				t.Fatal("big frame lost bytes")
			}
		case strings.Contains(body, `"echo"`):
			if !strings.Contains(got.String(), `"echo": 7`) {
				t.Fatal("buffered response lost", got.String())
			}
		default:
			if got.String() != "aaabbbccc" {
				t.Fatal("stream response lost", got.String())
			}
		}
		f.e.waitBusy(t, 0)
		for range 2 {
			iamCall(t, f.h, f.c.ctx, f.c.caller, "invoke.stream.close", `{"handle":"`+handle+`"}`, 200)
		}
		// Finishing a stream returns its sole registration slot.
		call := f.begin(t)
		call.Finish()
	}
}

func TestIAMStreamRevokeBlockedNextAndOwnedClose(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	handle, _ := f.open(t, `{"mode":"slowstream","chunks":[]}`)
	observed := make(chan struct{}, 1)
	f.h.iamTargetState = func(id string, w *sandbox.Worker) workloadiam.TargetState {
		select {
		case observed <- struct{}{}:
		default:
		}
		return invokeIAMTarget(id, w)
	}
	done := make(chan *sandbox.SDKError, 1)
	go func() {
		_, e := f.h.Handle(f.c.ctx, f.c.caller, "invoke.stream.next", json.RawMessage(`{"handle":"`+handle+`"}`))
		done <- e
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("next did not observe host state")
	}
	f.revoke(t)
	select {
	case e := <-done:
		if e == nil || e.Code != "IAM_DENIED" || e.Message != "SDK operation denied" {
			t.Fatal("revoked next disclosed frame/error", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("next not cancelled")
	}
	f.e.waitBusy(t, 0)
	for range 2 {
		iamCall(t, f.h, f.c.ctx, f.c.caller, "invoke.stream.close", `{"handle":"`+handle+`"}`, 200)
	}
	if err := f.a.Publish(2, invokeIAMEvaluator(t, iamOther, iamOther, 3, false, true), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.next(t, handle, 403)
	call := f.begin(t)
	call.Finish()
}

func TestIAMStreamHandoffDoesNotAcquireSessionOrStreamLocks(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	_, cs := f.open(t, `{"mode":"echo"}`)
	f.table.mu.Lock()
	cs.mu.Lock()
	defer f.table.mu.Unlock()
	defer cs.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := f.h.handoffIAMStream(f.c.ctx, f.c.caller, cs, []byte("frame"))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("handoff acquired session or stream lock")
	}
}

func TestIAMStreamGateContainsOnlyObservedBoundedCopy(t *testing.T) {
	source, err := os.ReadFile("iam_stream.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "iam_stream.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	handoffs := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandoffFrameObserved" {
			return true
		}
		handoffs++
		transfer, ok := call.Args[2].(*ast.FuncLit)
		if !ok || len(transfer.Body.List) != 2 {
			t.Fatal("handoff transfer must only copy into reserved storage and return")
		}
		copyStmt, ok := transfer.Body.List[0].(*ast.ExprStmt)
		if !ok {
			t.Fatal("handoff transfer does more than a bounded copy")
		}
		copyCall, ok := copyStmt.X.(*ast.CallExpr)
		if !ok {
			t.Fatal("handoff transfer is not a copy")
		}
		name, ok := copyCall.Fun.(*ast.Ident)
		if !ok || name.Name != "copy" {
			t.Fatal("handoff transfer is not a copy")
		}
		if _, ok := transfer.Body.List[1].(*ast.ReturnStmt); !ok {
			t.Fatal("handoff transfer does not immediately return")
		}
		// Acquisition, encoding, table access and teardown must never migrate
		// into either gate-held callback, even when a fixture's I/O is fast.
		for _, arg := range []ast.Expr{call.Args[0], call.Args[2]} {
			ast.Inspect(arg, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				s, ok := c.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch s.Sel.Name {
				case "NextFrame", "EncodeToString", "Finish", "finish", "retire", "lookup", "Lock", "RLock", "Marshal":
					t.Errorf("%s moved under the IAM gate", s.Sel.Name)
				}
				return true
			})
		}
		return true
	})
	if handoffs != 1 {
		t.Fatalf("expected one shared observed handoff, got %d", handoffs)
	}
}

func TestIAMStreamCleanupDoesNotWaitForIAMGate(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	handle, _ := f.open(t, `{"mode":"slowstream","chunks":[]}`)
	locked, unlock, released := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		released <- f.a.ChangeHostState(func() {
			close(locked)
			<-unlock
		})
	}()
	<-locked
	defer func() {
		close(unlock)
		if err := <-released; err != nil {
			t.Error(err)
		}
	}()
	done := make(chan *sandbox.SDKError, 1)
	go func() {
		_, e := f.h.Handle(f.c.ctx, f.c.caller, "invoke.stream.close", json.RawMessage(`{"handle":"`+handle+`"}`))
		done <- e
	}()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("owned close/teardown waited for IAM gate")
	}
	f.e.waitBusy(t, 0)
}

func TestIAMStreamCancellationCallbackOutsideGate(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	_, cs := f.open(t, `{"mode":"slowstream","chunks":[]}`)
	// The cancellation callback cannot retire its handle yet. Publication must
	// nevertheless acknowledge, leaving teardown outside the authorization gate.
	func() {
		f.table.mu.Lock()
		defer f.table.mu.Unlock()
		f.revoke(t)
	}()
	select {
	case <-cs.iam.call.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("registration was not cancelled")
	}
	f.e.waitBusy(t, 0)
}

func TestIAMStreamFreshTargetAndLeaseAtEveryHandoff(t *testing.T) {
	for _, change := range []string{"inactive", "artifact", "worker", "stale lease", "caller ended", "sdk context"} {
		t.Run(change, func(t *testing.T) {
			f := newIAMStreamFixture(t, 1)
			var active atomic.Bool
			var identity atomic.Int32
			active.Store(true)
			f.h.iamTargetState = func(id string, w *sandbox.Worker) workloadiam.TargetState {
				s := invokeIAMTarget(id, w)
				s.Active = active.Load()
				switch identity.Load() {
				case 1:
					s.Principal.ArtifactRevision = "changed"
				case 2:
					s.Principal.WorkerID = "changed"
				}
				return s
			}
			handle, _ := f.open(t, `{"mode":"big"}`)
			f.next(t, handle, 200)
			ctx, caller := f.c.ctx, f.c.caller
			switch change {
			case "inactive":
				_ = f.a.ChangeHostState(func() { active.Store(false) })
			case "artifact":
				_ = f.a.ChangeHostState(func() { identity.Store(1) })
			case "worker":
				_ = f.a.ChangeHostState(func() { identity.Store(2) })
			case "stale lease":
				exec := *caller.Exec
				exec.Lease = "stale"
				caller.Exec = &exec
			case "caller ended":
				// A mismatched captured execution must not spend this handle.
				exec := *caller.Exec
				exec.ExecutionID = "another-execution"
				caller.Exec = &exec
			case "sdk context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			iamCall(t, f.h, ctx, caller, "invoke.stream.next", `{"handle":"`+handle+`"}`, 403)
			iamCall(t, f.h, f.c.ctx, f.c.caller, "invoke.stream.close", `{"handle":"`+handle+`"}`, 200)
			f.e.waitBusy(t, 0)
		})
	}
}

func TestIAMStreamRevokeBeforeHeadHandoff(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	call := f.begin(t)
	f.revoke(t)
	r, e := f.h.publishIAMStream(f.c.ctx, f.c.caller, f.table, &handlers.F2FStream{
		StatusCode: 200, Headers: map[string]string{"secret": "not-handed-off"}, Body: "private",
	}, &iamStream{call: call, worker: f.target, targetID: iamOther})
	if r != nil || e == nil || e.Code != "IAM_DENIED" {
		t.Fatal("revoked head handed off", r, e)
	}
}

func TestIAMStreamSessionCleanupAndCapacity(t *testing.T) {
	f := newIAMStreamFixture(t, 16)
	// Buffered responses still retain registration until drained or closed.
	var handles []string
	for range maxStreamsPerChan {
		handle, _ := f.open(t, `{"mode":"echo"}`)
		handles = append(handles, handle)
	}
	iamCall(t, f.h, f.c.ctx, f.c.caller, "invoke.stream.open", `{"name":"iam-1"}`, 429)
	f.table.LeaseRevoked(f.c.caller.Exec.Lease)
	f.e.waitBusy(t, 0)
	for _, handle := range handles {
		f.next(t, handle, 403)
	}
	f.table.mu.Lock()
	leaked := len(f.table.m) != 0 || len(f.table.waiting) != 0 || f.table.opening != 0
	f.table.mu.Unlock()
	if leaked {
		t.Fatal("session cleanup leaked handles or openings")
	}
	var calls []*workloadiam.Admission
	for range 16 {
		calls = append(calls, f.begin(t))
	}
	iamCall(t, f.h, f.c.ctx, f.c.caller, "invoke.stream.open", `{"name":"iam-1"}`, 403)
	for _, call := range calls {
		call.Finish()
	}
	// Channel close cancels opening registrations outside the session lock.
	_, call, e := f.h.beginIAMCall(f.c.ctx, f.c.caller, json.RawMessage(`{"name":"iam-1"}`), workloadiam.OpStreamOpen)
	if e != nil {
		t.Fatal(e)
	}
	f.table.mu.Lock()
	f.table.waiting = map[*workloadiam.Admission]string{call: f.c.caller.Exec.Lease}
	f.table.mu.Unlock()
	f.table.Closed()
	if call.Context().Err() == nil {
		t.Fatal("channel close did not cancel opening stream")
	}
}

func TestIAMStreamPoolArtifactsStillDeny(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	for range 2 {
		iamCall(t, f.h, f.c.ctx, f.c.caller, "invoke.stream.open", `{"name":"iam-1"}`, 403)
	}
	f.e.waitBusy(t, 0)
	f.table.mu.Lock()
	defer f.table.mu.Unlock()
	if len(f.table.m) != 0 || len(f.table.waiting) != 0 || f.table.opening != 0 {
		t.Fatal("denied opens leaked resources")
	}
}

func TestIAMStreamRevokeDuringRealPoolWait(t *testing.T) {
	for _, revoke := range []string{"policy", "lease", "channel", "SDK request"} {
		t.Run(revoke, func(t *testing.T) {
			f := newIAMStreamFixture(t, 1)
			if err := f.e.db.UpsertPoolConfig(&database.PoolConfig{FunctionID: iamOther, MaxWarm: 1, IdleTTLS: 60, ScaleToZero: true}); err != nil {
				t.Fatal(err)
			}
			acq, err := f.e.pm.Acquire(f.c.ctx, iamOther)
			if err != nil {
				t.Fatal(err)
			}
			defer f.e.pm.Release(acq, nil)
			sdkCtx, cancelSDK := context.WithCancel(f.c.ctx)
			defer cancelSDK()
			done := make(chan *sandbox.SDKError, 1)
			go func() {
				_, e := f.h.Handle(sdkCtx, f.c.caller, "invoke.stream.open", json.RawMessage(`{"name":"iam-1"}`))
				done <- e
			}()
			deadline := time.Now().Add(time.Second)
			for {
				f.table.mu.Lock()
				waiting := len(f.table.waiting) == 1 && f.table.opening == 1
				f.table.mu.Unlock()
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("stream not registered before acquisition")
				}
				time.Sleep(time.Millisecond)
			}
			switch revoke {
			case "policy":
				f.revoke(t)
			case "lease":
				f.table.LeaseRevoked(f.c.caller.Exec.Lease)
			case "channel":
				f.table.Closed()
			case "SDK request":
				cancelSDK()
			}
			select {
			case e := <-done:
				if e == nil || e.Code != "IAM_DENIED" || e.Message != "SDK operation denied" {
					t.Fatal("wait disclosed target state", e)
				}
			case <-time.After(time.Second):
				t.Fatal("opening wait not cancelled")
			}
			f.table.mu.Lock()
			leaked := len(f.table.waiting) != 0 || len(f.table.m) != 0 || f.table.opening != 0
			f.table.mu.Unlock()
			if leaked || acq.Worker.Served.Load() != 0 {
				t.Fatal("revoked wait leaked resources or dispatched")
			}
		})
	}
}

func TestIAMStreamResponseHeadWaitOutsideGate(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	call := f.begin(t)
	fn, ie := f.e.inv.ResolveCallee("iam-1")
	if ie != nil {
		t.Fatal(ie)
	}
	dispatched := make(chan struct{})
	done := make(chan *handlers.InvokeError, 1)
	go func() {
		s, e := f.e.inv.OpenStreamWithAdmission(call.Context(), handlers.F2FCaller{FunctionID: iamOwner}, fn,
			[]byte(`{"mode":"sleep","s":30}`), "", func(*sandbox.Worker) *handlers.InvokeError {
				close(dispatched)
				return nil
			})
		if s != nil {
			s.Finish(context.Canceled)
		}
		done <- e
	}()
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("acquired-worker hook not reached")
	}
	f.revoke(t)
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("revoked response-head wait succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("response-head wait outlived registration")
	}
	f.e.waitBusy(t, 0)
}

func TestIAMStreamOversizedHeadAndConcurrentNext(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	call := f.begin(t)
	r, e := f.h.publishIAMStream(f.c.ctx, f.c.caller, f.table,
		&handlers.F2FStream{StatusCode: 200, Headers: map[string]string{"large": strings.Repeat("x", workloadiam.MaxHandoffBytes)}},
		&iamStream{call: call, worker: f.target, targetID: iamOther})
	if r != nil || e == nil || e.Code != "IAM_DENIED" {
		t.Fatal("oversized response head accepted", e)
	}
	// No admission slot or table entry survives an oversized head.
	handle, cs := f.open(t, `{"mode":"echo"}`)
	cs.iam.nextBusy.Store(true)
	f.next(t, handle, 403)
	cs.iam.nextBusy.Store(false)
	f.next(t, handle, 200)
	f.next(t, handle, 200)
}
