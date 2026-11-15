package sandbox

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// workerHarness is a Worker over in-memory pipes (this test plays the adapter)
// whose SDK server is attached to a real socketpair (this test plays the
// sandbox end of that too), wired exactly as Spawn wires them.
type workerHarness struct {
	*srvHarness
	w     *Worker
	reqR  *io.PipeReader
	respW *io.PipeWriter
	calls atomic.Int32
	last  atomic.Pointer[SDKCaller]
}

func newWorkerHarness(t *testing.T, sdk bool) *workerHarness {
	t.Helper()
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	w := &Worker{
		stdin: reqW, stdout: respR, sdkEnabled: sdk,
		frames: make(chan frameRead), readStop: make(chan struct{}),
		waitDone: make(chan struct{}), errBuf: newRingBuffer(1024),
	}
	go w.readLoop()
	h := &workerHarness{w: w, reqR: reqR, respW: respW}
	h.srvHarness = newSrvHarness(t, func(ctx context.Context, c SDKCaller, op string, _ json.RawMessage) (any, *SDKError) {
		h.calls.Add(1)
		h.last.Store(&c)
		if op == "invoke" {
			<-ctx.Done()
			return nil, &SDKError{Status: 499, Code: "CANCELLED", Message: "dispatch ended"}
		}
		return map[string]int{"v": 1}, nil
	}, w.disp.Load, w.markDead)
	t.Cleanup(func() {
		w.markDead()
		_ = reqR.Close()
		_ = respW.Close()
		_ = respR.Close()
	})
	return h
}

// adapterFrames reads what the host wrote to the adapter's stdin for one
// dispatch: the frame types in order, and the lease if one was sent.
func (h *workerHarness) adapterFrames(t *testing.T, n int) (types []string, lease string) {
	t.Helper()
	for i := 0; i < n; i++ {
		raw, err := readFrame(h.reqR)
		if err != nil {
			t.Fatalf("adapter read: %v", err)
		}
		var f struct{ Type, Lease string }
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		types = append(types, f.Type)
		if f.Type == "lease" {
			lease = f.Lease
		}
	}
	return types, lease
}

func (h *workerHarness) reply(t *testing.T, frame string) {
	t.Helper()
	if err := writeFrame(h.respW, []byte(frame)); err != nil {
		t.Fatalf("adapter reply: %v", err)
	}
}

var nextTestID atomic.Uint64

func (h *workerHarness) frame(t *testing.T, op, lease string) testReply {
	t.Helper()
	id := nextTestID.Add(1)
	sendReq(t, h.client, id, op, lease, "")
	r := readReply(t, h.client)
	if r.ID != id {
		t.Fatalf("reply id %d, want %d", r.ID, id)
	}
	return r
}

func (h *workerHarness) expectAccepted(t *testing.T, lease string) {
	t.Helper()
	if r := h.frame(t, "kv.get", lease); !r.OK {
		t.Fatalf("frame with live lease refused: %+v", r)
	}
}

func (h *workerHarness) expectRefused(t *testing.T, lease string) {
	t.Helper()
	before := h.calls.Load()
	r := h.frame(t, "kv.get", lease)
	if r.OK || r.E == nil || r.E.Status != 403 || r.E.Code != "SDK_SCOPE_VIOLATION" {
		t.Fatalf("frame with revoked lease = %+v, want 403 SDK_SCOPE_VIOLATION", r)
	}
	if h.calls.Load() != before {
		t.Fatal("handler ran for a frame with a revoked lease")
	}
}

func testCtx(t *testing.T) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx, cancel
}

func TestLeaseFrameWrittenBeforeRequest(t *testing.T) {
	for _, mode := range []string{"Dispatch", "DispatchEx"} {
		t.Run(mode, func(t *testing.T) {
			h := newWorkerHarness(t, true)
			ctx, _ := testCtx(t)
			done := make(chan error, 1)
			go func() {
				if mode == "Dispatch" {
					_, _, err := h.w.Dispatch(ctx, []byte(`{}`))
					done <- err
				} else {
					_, err := h.w.DispatchEx(ctx, []byte(`{}`))
					done <- err
				}
			}()
			types, lease := h.adapterFrames(t, 2)
			if len(types) != 2 || types[0] != "lease" || types[1] != "request" || len(lease) != 32 {
				t.Fatalf("stdin frames = %v lease=%q, want [lease request] with a 32-hex lease", types, lease)
			}
			h.expectAccepted(t, lease)
			h.reply(t, `{"type":"response","statusCode":200}`)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNoLeaseFrameForChannellessWorker(t *testing.T) {
	h := newWorkerHarness(t, false)
	ctx, _ := testCtx(t)
	for i := 0; i < 2; i++ {
		done := make(chan error, 1)
		go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); done <- err }()
		types, _ := h.adapterFrames(t, 1)
		if len(types) != 1 || types[0] != "request" {
			t.Fatalf("dispatch %d: first frame = %v, want only a request", i, types)
		}
		if h.w.disp.Load() != nil {
			t.Fatal("channel-less worker published dispatch state")
		}
		h.reply(t, `{"type":"response","statusCode":200}`)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestLeaseCarriesExecutionIdentityFromContext(t *testing.T) {
	h := newWorkerHarness(t, true)
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	ctx, _ := testCtx(t)
	ctx = WithExecution(ctx, ExecInfo{ExecutionID: "exec-1", TraceID: "tr", SpanID: "sp", Depth: 3, Ctx: parent})
	done := make(chan error, 1)
	go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); done <- err }()
	_, lease := h.adapterFrames(t, 2)
	h.expectAccepted(t, lease)
	c := h.last.Load()
	if c.Exec == nil || c.Exec.ExecutionID != "exec-1" || c.Exec.TraceID != "tr" || c.Exec.Depth != 3 || c.Exec.Lease != lease {
		t.Fatalf("caller.Exec = %+v", c.Exec)
	}
	// The handler ctx derives from info.Ctx: cancelling it cancels the call.
	cancelParent()
	select {
	case <-c.Exec.Ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Exec.Ctx does not derive from ExecInfo.Ctx")
	}
	h.reply(t, `{"type":"response","statusCode":200}`)
	<-done
}

func TestBlockedHandlerCancelledWhenDispatchEnds(t *testing.T) {
	h := newWorkerHarness(t, true)
	ctx, _ := testCtx(t)
	done := make(chan error, 1)
	go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); done <- err }()
	_, lease := h.adapterFrames(t, 2)
	// "invoke" blocks in the handler until the dispatch ends.
	id := nextTestID.Add(1)
	sendReq(t, h.client, id, "invoke", lease, "")
	for deadline := time.Now().Add(5 * time.Second); h.calls.Load() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("handler never started")
		}
		time.Sleep(time.Millisecond)
	}
	h.reply(t, `{"type":"response","statusCode":200}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r := readReply(t, h.client)
	if r.ID != id || r.OK || r.E == nil || r.E.Code != "CANCELLED" {
		t.Fatalf("blocked call reply = %+v, want it released by the end of the dispatch", r)
	}
}

// TestLeaseRevokedOnEveryExitPath drives each way a dispatch can end and
// asserts the lease was live during it and refused (handler untouched) after.
func TestLeaseRevokedOnEveryExitPath(t *testing.T) {
	type drive func(t *testing.T, h *workerHarness) (lease string)

	// start runs Dispatch in a goroutine and returns once the adapter has the
	// lease and the frame was accepted while the dispatch is live.
	startDispatch := func(t *testing.T, h *workerHarness, ctx context.Context) (string, chan error) {
		errc := make(chan error, 1)
		go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); errc <- err }()
		_, lease := h.adapterFrames(t, 2)
		h.expectAccepted(t, lease)
		return lease, errc
	}
	startEx := func(t *testing.T, h *workerHarness, ctx context.Context) (string, chan *DispatchResult, chan error) {
		resc, errc := make(chan *DispatchResult, 1), make(chan error, 1)
		go func() {
			r, err := h.w.DispatchEx(ctx, []byte(`{}`))
			resc <- r
			errc <- err
		}()
		_, lease := h.adapterFrames(t, 2)
		h.expectAccepted(t, lease)
		return lease, resc, errc
	}

	cases := map[string]drive{
		"dispatch normal response": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, errc := startDispatch(t, h, ctx)
			h.reply(t, `{"type":"response","statusCode":200}`)
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
			return lease
		},
		"dispatchEx normal response": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, resc, errc := startEx(t, h, ctx)
			h.reply(t, `{"type":"response","statusCode":200}`)
			<-resc
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
			return lease
		},
		"non-fatal adapter error": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, errc := startDispatch(t, h, ctx)
			h.reply(t, `{"type":"error","message":"handler threw"}`)
			if err := <-errc; err == nil {
				t.Fatal("want error")
			}
			return lease
		},
		"fatal adapter error": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, errc := startDispatch(t, h, ctx)
			h.reply(t, `{"type":"error","fatal":true,"message":"dead"}`)
			if err := <-errc; err == nil {
				t.Fatal("want error")
			}
			return lease
		},
		"invalid response frame": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, errc := startDispatch(t, h, ctx)
			h.reply(t, `{"type":"bogus"}`)
			if err := <-errc; err == nil {
				t.Fatal("want error")
			}
			return lease
		},
		"timeout": func(t *testing.T, h *workerHarness) string {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			lease, errc := startDispatch(t, h, ctx)
			if err := <-errc; err != context.DeadlineExceeded {
				t.Fatalf("err = %v, want deadline exceeded", err)
			}
			return lease
		},
		"ctx cancel": func(t *testing.T, h *workerHarness) string {
			ctx, cancel := context.WithCancel(context.Background())
			lease, errc := startDispatch(t, h, ctx)
			cancel()
			if err := <-errc; err != context.Canceled {
				t.Fatalf("err = %v, want canceled", err)
			}
			return lease
		},
		"stream end": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, resc, errc := startEx(t, h, ctx)
			h.reply(t, `{"type":"response_start","statusCode":200}`)
			res := <-resc
			if err := <-errc; err != nil || !res.Streaming {
				t.Fatalf("stream start: %v %+v", err, res)
			}
			// The lease must stay valid for the whole stream.
			h.expectAccepted(t, lease)
			go h.replyBG(t, `{"type":"chunk","data":"aGk="}`)
			if kind, _, err := res.NextFrame(ctx); err != nil || kind != "chunk" {
				t.Fatalf("chunk: %q %v", kind, err)
			}
			h.expectAccepted(t, lease)
			go h.replyBG(t, `{"type":"response_end"}`)
			if kind, _, err := res.NextFrame(ctx); err != nil || kind != "end" {
				t.Fatalf("end: %q %v", kind, err)
			}
			return lease
		},
		"stream client disconnect": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, resc, errc := startEx(t, h, ctx)
			h.reply(t, `{"type":"response_start","statusCode":200}`)
			res := <-resc
			<-errc
			streamCtx, cancel := context.WithCancel(ctx)
			cancel() // the HTTP client went away mid-stream
			if _, _, err := res.NextFrame(streamCtx); err == nil {
				t.Fatal("want error")
			}
			return lease
		},
		"stream adapter error": func(t *testing.T, h *workerHarness) string {
			ctx, _ := testCtx(t)
			lease, resc, errc := startEx(t, h, ctx)
			h.reply(t, `{"type":"response_start","statusCode":200}`)
			res := <-resc
			<-errc
			go h.replyBG(t, `{"type":"error","message":"mid-stream"}`)
			if _, _, err := res.NextFrame(ctx); err == nil {
				t.Fatal("want error")
			}
			return lease
		},
		"kill": func(t *testing.T, h *workerHarness) string {
			ctx, cancel := testCtx(t)
			defer cancel()
			lease, errc := startDispatch(t, h, ctx)
			_ = h.w.Kill()
			cancel()
			<-errc
			return lease
		},
		"markDead": func(t *testing.T, h *workerHarness) string {
			ctx, cancel := testCtx(t)
			defer cancel()
			lease, errc := startDispatch(t, h, ctx)
			h.w.markDead()
			cancel()
			<-errc
			return lease
		},
		// The reaper goroutine in Spawn calls revokeLease once the process is
		// gone (it needs a real nsjail to exercise end to end).
		"reaper": func(t *testing.T, h *workerHarness) string {
			ctx, cancel := testCtx(t)
			defer cancel()
			lease, errc := startDispatch(t, h, ctx)
			h.w.dead.Store(true)
			h.w.revokeLease()
			cancel()
			<-errc
			return lease
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			h := newWorkerHarness(t, true)
			lease := run(t, h)
			if lease == "" {
				t.Fatal("no lease observed")
			}
			if h.w.disp.Load() != nil {
				t.Fatal("dispatch state still published after the path completed")
			}
			h.expectRefused(t, lease)
			// An empty lease (module scope) is still allowed after the dispatch.
			h.expectAccepted(t, "")
		})
	}
}

func TestOldLeaseRefusedAfterNewDispatchStarts(t *testing.T) {
	h := newWorkerHarness(t, true)
	ctx, _ := testCtx(t)

	done := make(chan error, 1)
	go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); done <- err }()
	_, lease1 := h.adapterFrames(t, 2)
	h.reply(t, `{"type":"response","statusCode":200}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); done <- err }()
	_, lease2 := h.adapterFrames(t, 2)
	if lease1 == lease2 {
		t.Fatal("two dispatches received the same lease")
	}
	h.expectRefused(t, lease1)
	h.expectAccepted(t, lease2)
	h.reply(t, `{"type":"response","statusCode":200}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.expectRefused(t, lease2)
}

// replyBG is reply for use from a goroutine (Errorf, never Fatalf).
func (h *workerHarness) replyBG(t *testing.T, frame string) {
	if err := writeFrame(h.respW, []byte(frame)); err != nil {
		t.Errorf("adapter reply: %v", err)
	}
}
