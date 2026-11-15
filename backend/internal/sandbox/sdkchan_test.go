package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// ── sandbox-side test client ────────────────────────────────────────────────

type testReply struct {
	ID uint64          `json:"id"`
	OK bool            `json:"ok"`
	R  json.RawMessage `json:"r"`
	E  *SDKError       `json:"e"`
}

func sendRaw(t *testing.T, c net.Conn, payload []byte) {
	t.Helper()
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(encodeSDKFrame(payload)); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func sendReq(t *testing.T, c net.Conn, id uint64, op, lease, args string) {
	t.Helper()
	m := map[string]any{"id": id, "op": op}
	if lease != "" {
		m["lease"] = lease
	}
	if args != "" {
		m["a"] = json.RawMessage(args)
	}
	b, _ := json.Marshal(m)
	sendRaw(t, c, b)
}

func readReply(t *testing.T, c net.Conn) testReply {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	f, err := readSDKFrame(c)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	var r testReply
	if err := json.Unmarshal(f, &r); err != nil {
		t.Fatalf("decode reply %q: %v", f, err)
	}
	return r
}

func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := readSDKFrame(c); err == nil {
		t.Fatal("expected the connection to be closed, got a frame")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("connection still open (read timed out)")
	}
}

func expectSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// ── sdkServer harness over a real socketpair ───────────────────────────────

type srvHarness struct {
	srv       *sdkServer
	client    net.Conn
	violation chan struct{}
	killed    chan struct{}
}

func newSrvHarness(t *testing.T, handler SDKHandler, state func() *dispatchState, violation func()) *srvHarness {
	t.Helper()
	host, child, err := newSDKPair()
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.FileConn(child)
	_ = child.Close()
	if err != nil {
		t.Fatal(err)
	}
	h := &srvHarness{client: client, violation: make(chan struct{}, 1), killed: make(chan struct{}, 1)}
	if state == nil {
		state = func() *dispatchState { return nil }
	}
	h.srv = &sdkServer{
		conn: host, functionID: "fn-host", handler: handler, state: state,
		violation: func() {
			if violation != nil {
				violation()
			}
			h.violation <- struct{}{}
		},
		kill: func() { h.killed <- struct{}{} },
	}
	done := make(chan struct{})
	go func() { h.srv.serve(); close(done) }()
	t.Cleanup(func() {
		_ = client.Close()
		_ = host.Close()
		<-done
	})
	return h
}

func okHandler(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
	return map[string]int{"v": 1}, nil
}

func TestSDKRoundTripAndOutOfOrder(t *testing.T) {
	first := make(chan struct{})
	h := newSrvHarness(t, func(ctx context.Context, c SDKCaller, op string, args json.RawMessage) (any, *SDKError) {
		var a struct{ N int }
		_ = json.Unmarshal(args, &a)
		if a.N == 1 {
			<-first // reply 1 only after reply 2 has been produced
			return "one", nil
		}
		defer close(first)
		return "two", nil
	}, nil, nil)
	sendReq(t, h.client, 1, "kv.get", "", `{"n":1}`)
	sendReq(t, h.client, 2, "kv.get", "", `{"n":2}`)
	a, b := readReply(t, h.client), readReply(t, h.client)
	if a.ID != 2 || b.ID != 1 || !a.OK || !b.OK || string(a.R) != `"two"` || string(b.R) != `"one"` {
		t.Fatalf("replies = %+v %+v, want id 2 then id 1", a, b)
	}
}

func TestSDKErrorReplyShapeAndNilResult(t *testing.T) {
	h := newSrvHarness(t, func(_ context.Context, _ SDKCaller, op string, _ json.RawMessage) (any, *SDKError) {
		if op == "kv.delete" {
			return nil, nil
		}
		return nil, &SDKError{Status: 404, Code: "NOT_FOUND", Message: "nope"}
	}, nil, nil)
	sendReq(t, h.client, 1, "kv.get", "", "")
	if r := readReply(t, h.client); r.OK || r.E == nil || r.E.Status != 404 || r.E.Code != "NOT_FOUND" {
		t.Fatalf("error reply = %+v", r)
	}
	sendReq(t, h.client, 2, "kv.delete", "", "")
	if r := readReply(t, h.client); !r.OK || string(r.R) != "{}" {
		t.Fatalf("nil result reply = %+v %s", r, r.R)
	}
}

func TestSDKTooManyInflight(t *testing.T) {
	release := make(chan struct{})
	var entered atomic.Int32
	h := newSrvHarness(t, func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
		entered.Add(1)
		<-release
		return "ok", nil
	}, nil, nil)
	for i := uint64(1); i <= MaxSDKInflight; i++ {
		sendReq(t, h.client, i, "kv.get", "", "")
	}
	for deadline := time.Now().Add(5 * time.Second); entered.Load() < MaxSDKInflight; {
		if time.Now().After(deadline) {
			t.Fatalf("only %d handlers started", entered.Load())
		}
		time.Sleep(time.Millisecond)
	}
	sendReq(t, h.client, 33, "kv.get", "", "")
	r := readReply(t, h.client)
	if r.ID != 33 || r.OK || r.E == nil || r.E.Status != 429 || r.E.Code != "TOO_MANY_INFLIGHT" {
		t.Fatalf("33rd reply = %+v, want immediate 429 TOO_MANY_INFLIGHT", r)
	}
	close(release)
	for i := 0; i < MaxSDKInflight; i++ {
		if r := readReply(t, h.client); !r.OK {
			t.Fatalf("reply %d = %+v", i, r)
		}
	}
	// Capacity is released: another request is served.
	sendReq(t, h.client, 34, "kv.get", "", "")
	if r := readReply(t, h.client); !r.OK || r.ID != 34 {
		t.Fatalf("after drain = %+v", r)
	}
}

func TestSDKUnknownOpKeepsConnectionOpen(t *testing.T) {
	var calls atomic.Int32
	h := newSrvHarness(t, func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
		calls.Add(1)
		return "ok", nil
	}, nil, nil)
	for i, op := range []string{"nope", "", "KV.GET", "kv.get "} {
		sendReq(t, h.client, uint64(i+1), op, "", "")
		r := readReply(t, h.client)
		if r.OK || r.E == nil || r.E.Status != 400 || r.E.Code != "UNKNOWN_OP" {
			t.Fatalf("op %q reply = %+v", op, r)
		}
	}
	sendReq(t, h.client, 9, "kv.get", "", "")
	if r := readReply(t, h.client); !r.OK {
		t.Fatalf("connection unusable after unknown op: %+v", r)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", calls.Load())
	}
}

func TestSDKProtocolViolationClosesAndMarksUnhealthy(t *testing.T) {
	hdr := func(n uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, n); return b }
	cases := []struct {
		name string
		raw  []byte
	}{
		{"zero length", hdr(0)},
		{"oversized length", hdr(MaxSDKFrame + 1)},
		{"max uint32 length", hdr(0xFFFFFFFF)},
		{"invalid json", encodeSDKFrame([]byte(`{"id":1,`))},
		{"not json", encodeSDKFrame([]byte(`hello`))},
		{"array", encodeSDKFrame([]byte(`[1]`))},
		{"null", encodeSDKFrame([]byte(`null`))},
		{"string", encodeSDKFrame([]byte(`"x"`))},
		{"missing id", encodeSDKFrame([]byte(`{"op":"kv.get"}`))},
		{"zero id", encodeSDKFrame([]byte(`{"id":0,"op":"kv.get"}`))},
		{"negative id", encodeSDKFrame([]byte(`{"id":-1,"op":"kv.get"}`))},
		{"string id", encodeSDKFrame([]byte(`{"id":"1","op":"kv.get"}`))},
		{"fractional id", encodeSDKFrame([]byte(`{"id":1.5,"op":"kv.get"}`))},
		{"op wrong type", encodeSDKFrame([]byte(`{"id":1,"op":7}`))},
		{"lease wrong type", encodeSDKFrame([]byte(`{"id":1,"op":"kv.get","lease":3}`))},
		{"args array", encodeSDKFrame([]byte(`{"id":1,"op":"kv.get","a":[1]}`))},
		{"trailing data", encodeSDKFrame([]byte(`{"id":1,"op":"kv.get"} {}`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var unhealthy atomic.Bool
			h := newSrvHarness(t, func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
				calls.Add(1)
				return "ok", nil
			}, nil, func() { unhealthy.Store(true) })
			if _, err := h.client.Write(tc.raw); err != nil {
				t.Fatal(err)
			}
			expectClosed(t, h.client)
			expectSignal(t, h.violation, "worker to be marked unhealthy")
			if !unhealthy.Load() || calls.Load() != 0 {
				t.Fatalf("unhealthy=%v calls=%d", unhealthy.Load(), calls.Load())
			}
		})
	}
}

func TestSDKClientEOFIsNotAViolation(t *testing.T) {
	h := newSrvHarness(t, okHandler, nil, nil)
	_ = h.client.Close()
	select {
	case <-h.violation:
		t.Fatal("a clean close must not mark the worker unhealthy")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSDKStaleLeaseRefusedWithoutCallingHandler(t *testing.T) {
	var calls atomic.Int32
	var st atomic.Pointer[dispatchState]
	h := newSrvHarness(t, func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
		calls.Add(1)
		return "ok", nil
	}, st.Load, nil)
	// No current dispatch at all.
	sendReq(t, h.client, 1, "kv.get", "some-lease", "")
	// A current dispatch with a different lease.
	st.Store(newDispatchState(context.Background()))
	sendReq(t, h.client, 2, "invoke", "wrong", "")
	for _, want := range []uint64{1, 2} {
		r := readReply(t, h.client)
		if r.ID != want || r.OK || r.E == nil || r.E.Status != 403 || r.E.Code != "SDK_SCOPE_VIOLATION" {
			t.Fatalf("reply = %+v, want 403 SDK_SCOPE_VIOLATION for id %d", r, want)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("handler ran %d times for refused frames", calls.Load())
	}
	// The connection stays open: a refused lease is not a protocol violation.
	sendReq(t, h.client, 3, "kv.get", "", "")
	if r := readReply(t, h.client); !r.OK {
		t.Fatalf("connection unusable after refusal: %+v", r)
	}
}

func TestSDKCallerAttribution(t *testing.T) {
	got := make(chan SDKCaller, 4)
	var st atomic.Pointer[dispatchState]
	h := newSrvHarness(t, func(_ context.Context, c SDKCaller, _ string, _ json.RawMessage) (any, *SDKError) {
		got <- c
		return "ok", nil
	}, st.Load, nil)
	// Empty lease: module scope, Exec nil even while a dispatch is running.
	ctx := WithExecution(context.Background(), ExecInfo{ExecutionID: "e1", TraceID: "t1", Depth: 2})
	d := newDispatchState(ctx)
	st.Store(d)
	sendReq(t, h.client, 1, "kv.get", "", "")
	readReply(t, h.client)
	c := <-got
	if c.Exec != nil || c.FunctionID != "fn-host" {
		t.Fatalf("empty lease caller = %+v, want Exec nil and host function id", c)
	}
	// Valid lease: Exec is the host's record, never anything from the frame.
	sendReq(t, h.client, 2, "kv.get", d.info.Lease, `{"function_id":"evil","execution_id":"evil"}`)
	readReply(t, h.client)
	c = <-got
	if c.Exec == nil || c.Exec.ExecutionID != "e1" || c.Exec.TraceID != "t1" || c.Exec.Depth != 2 ||
		c.FunctionID != "fn-host" || c.Exec.Lease != d.info.Lease || c.Exec.Ctx == nil {
		t.Fatalf("leased caller = %+v exec=%+v", c, c.Exec)
	}
}

func TestSDKHandlerPanicRecovered(t *testing.T) {
	h := newSrvHarness(t, func(_ context.Context, _ SDKCaller, op string, _ json.RawMessage) (any, *SDKError) {
		if op == "kv.put" {
			panic("boom")
		}
		return "ok", nil
	}, nil, nil)
	sendReq(t, h.client, 1, "kv.put", "", "")
	r := readReply(t, h.client)
	if r.OK || r.E == nil || r.E.Status != 500 || r.E.Code != "INTERNAL" || bytes.Contains([]byte(r.E.Message), []byte("boom")) {
		t.Fatalf("panic reply = %+v", r)
	}
	sendReq(t, h.client, 2, "kv.get", "", "")
	if r := readReply(t, h.client); !r.OK || r.ID != 2 {
		t.Fatalf("loop did not continue after panic: %+v", r)
	}
}

func TestSDKUnencodableResultIs500(t *testing.T) {
	h := newSrvHarness(t, func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
		return make(chan int), nil
	}, nil, nil)
	sendReq(t, h.client, 1, "kv.get", "", "")
	if r := readReply(t, h.client); r.OK || r.E == nil || r.E.Status != 500 {
		t.Fatalf("reply = %+v", r)
	}
}

func TestSDKOversizedResultIsAnErrorNotAClose(t *testing.T) {
	big := string(bytes.Repeat([]byte("a"), MaxSDKFrame))
	h := newSrvHarness(t, func(_ context.Context, _ SDKCaller, op string, _ json.RawMessage) (any, *SDKError) {
		if op == "kv.list" {
			return big, nil
		}
		return "ok", nil
	}, nil, nil)
	sendReq(t, h.client, 1, "kv.list", "", "")
	if r := readReply(t, h.client); r.OK || r.E == nil || r.E.Code != "RESULT_TOO_LARGE" {
		t.Fatalf("reply = %+v", r)
	}
	sendReq(t, h.client, 2, "kv.get", "", "")
	if r := readReply(t, h.client); !r.OK {
		t.Fatalf("connection unusable: %+v", r)
	}
}

func TestSDKWriteDeadlineKillsWorker(t *testing.T) {
	// net.Pipe is synchronous: a reply write blocks until the peer reads, and
	// this peer never does, which is exactly a sandbox that stopped reading.
	client, host := net.Pipe()
	defer client.Close()
	killed := make(chan struct{}, 1)
	srv := &sdkServer{
		conn: host, functionID: "fn", handler: okHandler,
		state: func() *dispatchState { return nil },
		kill:  func() { killed <- struct{}{} }, writeTimeout: 50 * time.Millisecond,
	}
	done := make(chan struct{})
	go func() { srv.serve(); close(done) }()
	sendReq(t, client, 1, "kv.get", "", "")
	expectSignal(t, killed, "worker kill after write deadline")
	expectSignal(t, done, "serve loop to exit")
}

func TestSDKBlockedHandlerCtxCancelledOnRevoke(t *testing.T) {
	var st atomic.Pointer[dispatchState]
	entered := make(chan struct{})
	ended := make(chan error, 1)
	h := newSrvHarness(t, func(ctx context.Context, _ SDKCaller, _ string, _ json.RawMessage) (any, *SDKError) {
		close(entered)
		<-ctx.Done()
		ended <- ctx.Err()
		return nil, &SDKError{Status: 499, Code: "CANCELLED", Message: "dispatch ended"}
	}, st.Load, nil)
	d := newDispatchState(context.Background())
	st.Store(d)
	sendReq(t, h.client, 1, "invoke", d.info.Lease, "")
	expectSignal(t, entered, "handler start")
	st.Store(nil)
	d.cancel() // what revokeLease does
	select {
	case err := <-ended:
		if err != context.Canceled {
			t.Fatalf("ctx err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler ctx was not cancelled")
	}
}

func TestSDKConnCtxCancelledWhenChannelCloses(t *testing.T) {
	entered := make(chan struct{})
	ended := make(chan struct{})
	h := newSrvHarness(t, func(ctx context.Context, _ SDKCaller, _ string, _ json.RawMessage) (any, *SDKError) {
		close(entered)
		<-ctx.Done()
		close(ended)
		return nil, &SDKError{Status: 499, Code: "CANCELLED"}
	}, nil, nil)
	sendReq(t, h.client, 1, "kv.get", "", "")
	expectSignal(t, entered, "handler start")
	_ = h.client.Close() // worker died: module-scope work must stop
	expectSignal(t, ended, "module-scope handler ctx cancel")
}

// ── frame reader / decoder fuzz ────────────────────────────────────────────

// FuzzSDKFrame feeds arbitrary bytes to the frame reader and the strict
// decoder: no panic, and an accepted frame is always within MaxSDKFrame.
func FuzzSDKFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add(encodeSDKFrame([]byte(`{"id":1,"op":"kv.get","lease":"x","a":{"k":"v"}}`)))
	f.Add(encodeSDKFrame([]byte(`[]`)))
	f.Add(encodeSDKFrame([]byte(`{"id":1,"op":"kv.get"} trailing`)))
	f.Add([]byte(`{"id":1}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if payload, err := readSDKFrame(bytes.NewReader(data)); err == nil {
			if len(payload) < 1 || len(payload) > MaxSDKFrame || len(payload) > len(data)-4 {
				t.Fatalf("frame length %d inconsistent with input of %d bytes", len(payload), len(data))
			}
		}
		// The decoder must also stand alone on arbitrary bytes.
		req, err := decodeSDKRequest(data)
		if err == nil {
			if req.ID < 1 || len(req.Args) == 0 || req.Args[0] != '{' {
				t.Fatalf("accepted an invalid request: %+v", req)
			}
		}
		// Whatever the stream looks like, reading terminates without a panic.
		r := bytes.NewReader(data)
		for i := 0; i < 8; i++ {
			if _, err := readSDKFrame(r); err != nil {
				if err == io.EOF {
					break
				}
				break
			}
		}
	})
}
