package sdkhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

// fakeAdapter plays nsjail + the language adapter: the pool spawns it exactly
// like a real worker (real Spawn, real frames), and the request body selects
// the behaviour, so the whole host path runs without namespaces or root.
const fakeAdapter = `#!/usr/bin/env python3
import sys, json, struct, time, base64
def rd():
    h = sys.stdin.buffer.read(4)
    if len(h) < 4: sys.exit(0)
    return json.loads(sys.stdin.buffer.read(struct.unpack('>I', h)[0]))
def wr(o):
    b = json.dumps(o).encode()
    sys.stdout.buffer.write(struct.pack('>I', len(b)) + b); sys.stdout.buffer.flush()
wr({"type": "ready"})
while True:
    f = rd()
    if f.get("type") != "request": continue
    ev = f["event"]; h = ev.get("headers", {})
    try: cmd = json.loads(ev.get("body") or "{}")
    except Exception: cmd = {}
    if not isinstance(cmd, dict): cmd = {"echo": cmd}
    m = cmd.get("mode", "echo")
    if m == "echo":
        wr({"type": "response", "statusCode": 200, "headers": {"x-k": "v"},
            "body": json.dumps({"depth": h.get("x-orva-call-depth"), "trace": h.get("x-orva-trace-id"), "echo": cmd.get("echo")})})
    elif m == "fail":
        wr({"type": "error", "message": "boom"})
    elif m == "sleep":
        time.sleep(cmd.get("s", 10)); wr({"type": "response", "statusCode": 200, "body": "late"})
    elif m in ("stream", "slowstream", "big"):
        wr({"type": "response_start", "statusCode": 201, "headers": {"x-s": "1"}})
        if m == "big":
            wr({"type": "chunk", "data": base64.b64encode(b"z" * 600000).decode()})
        else:
            for c in cmd.get("chunks", ["aaa", "bbb", "ccc"]):
                wr({"type": "chunk", "data": base64.b64encode(c.encode()).decode()})
        if m == "slowstream":
            time.sleep(60)
        wr({"type": "response_end"})
`

type invEnv struct {
	db   *database.Database
	pm   *pool.Manager
	auth *sdkauth.Authenticator
	inv  *handlers.InternalInvokeHandler
	host *Host
}

func newInvEnv(t *testing.T, extra ...*database.Function) *invEnv {
	t.Helper()
	db := newDB(t)
	t.Cleanup(func() { _ = db.Close() })
	for _, f := range extra {
		f.Runtime, f.Entrypoint, f.MemoryMB, f.CPUs, f.NetworkMode = "node", "handler.js", 64, 0.5, "none"
		f.EnvVars = map[string]string{}
		if f.TimeoutMS == 0 {
			f.TimeoutMS = 30000
		}
		if f.Status == "" {
			f.Status = "active"
		}
		if err := db.InsertFunction(f); err != nil {
			t.Fatal(err)
		}
	}
	tmp := t.TempDir()
	rootfs := filepath.Join(tmp, "rootfs")
	for _, d := range []string{"node/usr/local/bin", "node/opt/orva"} {
		if err := os.MkdirAll(filepath.Join(rootfs, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(rootfs, "node/usr/local/bin/node"), []byte("#!/bin/sh\n"), 0o755)
	_ = os.WriteFile(filepath.Join(rootfs, "node/opt/orva/adapter.js"), []byte("//\n"), 0o644)
	nsjail := filepath.Join(tmp, "fake-nsjail")
	if err := os.WriteFile(nsjail, []byte(fakeAdapter), 0o755); err != nil {
		t.Skipf("cannot write fake adapter: %v", err)
	}
	pm := pool.NewManager(pool.ManagerConfig{DefaultMin: 0, DefaultIdleTTL: time.Minute, DefaultMaxUses: 1000, ReapInterval: time.Hour},
		pool.SandboxTemplate{NsjailBin: nsjail, RootfsDir: rootfs, DataDir: tmp, DefaultMaxPids: 32},
		db, registry.New(db), sandbox.NewLimiter(50))
	t.Cleanup(func() { _ = pm.Shutdown(context.Background()) })
	auth := sdkauth.New([]byte("secret"))
	inv := &handlers.InternalInvokeHandler{DB: db, Pool: pm, SDKAuth: auth}
	return &invEnv{db: db, pm: pm, auth: auth, inv: inv, host: New(db, WithInvoker(inv))}
}

func (e *invEnv) busy() int64 {
	var n int64
	for _, s := range e.pm.Stats() {
		n += s.Busy
	}
	return n
}

func (e *invEnv) waitBusy(t *testing.T, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.busy() != want {
		if time.Now().After(deadline) {
			t.Fatalf("pool busy=%d, want %d (leaked or never acquired worker)", e.busy(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *invEnv) exec(ctx context.Context, depth int, lease string) sandbox.SDKCaller {
	return sandbox.SDKCaller{FunctionID: fnA, Exec: &sandbox.ExecInfo{
		ExecutionID: "e-" + lease, TraceID: "trace1", SpanID: "span1", Depth: depth, Lease: lease, Ctx: ctx}}
}

// httpCall posts body to the HTTP F2F handler as the SDK would.
func (e *invEnv) httpCall(t *testing.T, name, body string, depth int) (int, string) {
	t.Helper()
	tok, rel := e.auth.Mint(fnA)
	defer rel()
	execID := fmt.Sprintf("http-%d", time.Now().UnixNano())
	defer e.auth.BindExecution(execID, fnA, "trace1", "span1", time.Now(), depth)()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/_internal/invoke/"+name, bytes.NewBufferString(body))
	req.SetPathValue("name", name)
	req.Header.Set("X-Orva-Internal-Token", tok)
	req.Header.Set("X-Orva-Execution-Id", execID)
	w := httptest.NewRecorder()
	e.inv.Invoke(w, req)
	return w.Code, w.Body.String()
}

func (e *invEnv) chanCall(name, body string, depth int) (int, string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, _ := json.Marshal(map[string]any{"name": name, "body": json.RawMessage(body)})
	r, serr := e.host.Handle(ctx, e.exec(ctx, depth, "L"), "invoke", a)
	if serr != nil {
		return serr.Status, serr.Code
	}
	b, _ := json.Marshal(r)
	return 200, string(b)
}

func httpCode(body string) string {
	var m struct {
		Code  string
		Error struct{ Code string }
	}
	_ = json.Unmarshal([]byte(body), &m)
	if m.Error.Code != "" {
		return m.Error.Code
	}
	return m.Code
}

func TestInvokeDifferentialAgainstHTTP(t *testing.T) {
	e := newInvEnv(t,
		&database.Function{ID: "fn-off", Name: "fn-off", Status: "inactive"},
		&database.Function{ID: "fn-slow", Name: "fn-slow", TimeoutMS: 300})
	cases := []struct {
		name, callee, body string
		depth              int
		status             int
		code               string
	}{
		{"success", fnB, `{"mode":"echo","echo":7}`, 0, 200, ""},
		{"not found", "nope", `{}`, 0, 404, "NOT_FOUND"},
		{"inactive", "fn-off", `{}`, 0, 409, "NOT_ACTIVE"},
		{"depth cap", fnB, `{"mode":"echo"}`, 8, 507, "MAX_CALL_DEPTH"},
		{"last allowed depth", fnB, `{"mode":"echo"}`, 7, 200, ""},
		{"callee error", fnB, `{"mode":"fail"}`, 0, 502, "INVOKE_FAILED"},
		{"timeout", "fn-slow", `{"mode":"sleep","s":5}`, 0, 502, "INVOKE_FAILED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hs, hb := e.httpCall(t, c.callee, c.body, c.depth)
			cs, cb := e.chanCall(c.callee, c.body, c.depth)
			if hs != c.status || cs != c.status {
				t.Fatalf("http=%d (%s) channel=%d (%s), want %d", hs, hb, cs, cb, c.status)
			}
			if c.status == 200 {
				var h, ch any
				_ = json.Unmarshal([]byte(hb), &h)
				_ = json.Unmarshal([]byte(cb), &ch)
				if !reflect.DeepEqual(h, ch) {
					t.Fatalf("envelope differs:\nhttp:    %s\nchannel: %s", hb, cb)
				}
				return
			}
			if httpCode(hb) != c.code || cb != c.code {
				t.Fatalf("http code %q channel code %q, want %q", httpCode(hb), cb, c.code)
			}
		})
	}
	e.waitBusy(t, 0)
	if _, s := e.chanCall(fnB, `{"mode":"echo"}`, 3); !strings.Contains(s, `\"depth\": \"4\"`) {
		t.Fatalf("callee depth must be caller depth+1, got %s", s)
	}
}

func TestInvokeNeedsExecutionAndInvoker(t *testing.T) {
	e := newInvEnv(t)
	_, serr := e.host.Handle(context.Background(), sandbox.SDKCaller{FunctionID: fnA}, "invoke", json.RawMessage(`{"name":"`+fnB+`"}`))
	if serr == nil || serr.Status != 403 || serr.Code != "SDK_SCOPE_VIOLATION" {
		t.Fatalf("module-scope invoke = %+v", serr)
	}
	// An absent body is {}; identity arguments are rejected, not trusted.
	ctx := context.Background()
	r, serr := e.host.Handle(ctx, e.exec(ctx, 0, "L"), "invoke", json.RawMessage(`{"name":"`+fnB+`","depth":0,"trace_id":"x"}`))
	if serr == nil || serr.Status != 400 || serr.Code != "INVALID_JSON" {
		t.Fatalf("identity arguments accepted: %v %+v", r, serr)
	}
	r, serr = e.host.Handle(ctx, e.exec(ctx, 0, "L"), "invoke", json.RawMessage(`{"name":"`+fnB+`"}`))
	if serr != nil || !strings.Contains(string(r.(json.RawMessage)), `\"trace\": \"trace1\"`) {
		t.Fatalf("%v %+v", r, serr)
	}
	if _, serr := New(e.db).Handle(ctx, e.exec(ctx, 0, "L"), "invoke", json.RawMessage(`{"name":"x"}`)); serr == nil || serr.Status != 501 {
		t.Fatalf("no invoker = %+v", serr)
	}
}

func TestInvokeRevokedCallerCancelsCalleeAndReleasesWorker(t *testing.T) {
	e := newInvEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *sandbox.SDKError, 1)
	go func() {
		_, serr := e.host.Handle(ctx, e.exec(ctx, 0, "L"), "invoke", json.RawMessage(`{"name":"`+fnB+`","body":{"mode":"sleep","s":30}}`))
		done <- serr
	}()
	e.waitBusy(t, 1)
	cancel() // what the sandbox does when the caller's dispatch ends
	select {
	case serr := <-done:
		if serr == nil {
			t.Fatal("cancelled invoke succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callee not cancelled")
	}
	e.waitBusy(t, 0)
}

// ---- streams ----

type streamHarness struct {
	e    *invEnv
	sess *sandbox.SDKSession
	ctx  context.Context
}

func newStreamHarness(t *testing.T, e *invEnv) *streamHarness {
	t.Helper()
	sess := &sandbox.SDKSession{}
	return &streamHarness{e: e, sess: sess, ctx: sandbox.WithSDKSession(context.Background(), sess)}
}

func (s *streamHarness) call(lease string, ctx context.Context, op, args string) (map[string]any, *sandbox.SDKError) {
	r, serr := s.e.host.Handle(sandbox.WithSDKSession(ctx, s.sess), s.e.exec(ctx, 0, lease), op, json.RawMessage(args))
	if serr != nil {
		return nil, serr
	}
	b, _ := json.Marshal(r)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m, nil
}

func (s *streamHarness) open(t *testing.T, ctx context.Context, lease, fn, body string) string {
	t.Helper()
	m, serr := s.call(lease, ctx, "invoke.stream.open", `{"name":"`+fn+`","body":`+body+`}`)
	if serr != nil {
		t.Fatalf("open: %+v", serr)
	}
	return m["handle"].(string)
}

func (s *streamHarness) revoke(lease string) {
	s.sess.State(func() sandbox.SDKSessionState { return newStreamTable() }).LeaseRevoked(lease)
}

func TestStreamHappyPathChunkingAndIdempotentClose(t *testing.T) {
	e := newInvEnv(t)
	s := newStreamHarness(t, e)
	ctx := context.Background()
	m, serr := s.call("L", ctx, "invoke.stream.open", `{"name":"`+fnB+`","body":{"mode":"stream"}}`)
	if serr != nil || m["status"].(float64) != 201 || m["headers"].(map[string]any)["x-s"] != "1" {
		t.Fatalf("open = %v %+v", m, serr)
	}
	h := m["handle"].(string)
	var got []string
	for i := 0; i < 10; i++ {
		n, serr := s.call("L", ctx, "invoke.stream.next", `{"handle":"`+h+`"}`)
		if serr != nil {
			t.Fatal(serr)
		}
		if n["done"].(bool) {
			break
		}
		b, _ := base64.StdEncoding.DecodeString(n["chunk_b64"].(string))
		got = append(got, string(b))
	}
	if strings.Join(got, ",") != "aaa,bbb,ccc" {
		t.Fatalf("chunks %v", got)
	}
	e.waitBusy(t, 0)
	for i := 0; i < 2; i++ {
		if _, serr := s.call("L", ctx, "invoke.stream.close", `{"handle":"`+h+`"}`); serr != nil {
			t.Fatalf("close #%d: %+v", i, serr)
		}
	}
	// a big chunk is split at 256 KiB
	h = s.open(t, ctx, "L", fnB, `{"mode":"big"}`)
	var sizes []int
	for {
		n, serr := s.call("L", ctx, "invoke.stream.next", `{"handle":"`+h+`"}`)
		if serr != nil {
			t.Fatal(serr)
		}
		if n["done"].(bool) {
			break
		}
		b, _ := base64.StdEncoding.DecodeString(n["chunk_b64"].(string))
		sizes = append(sizes, len(b))
	}
	if !reflect.DeepEqual(sizes, []int{262144, 262144, 75712}) {
		t.Fatalf("sizes %v", sizes)
	}
	e.waitBusy(t, 0)
}

func TestStreamEarlyCloseReleasesWorker(t *testing.T) {
	e := newInvEnv(t)
	s := newStreamHarness(t, e)
	ctx := context.Background()
	h := s.open(t, ctx, "L", fnB, `{"mode":"slowstream"}`)
	e.waitBusy(t, 1)
	if _, serr := s.call("L", ctx, "invoke.stream.next", `{"handle":"`+h+`"}`); serr != nil {
		t.Fatal(serr)
	}
	if _, serr := s.call("L", ctx, "invoke.stream.close", `{"handle":"`+h+`"}`); serr != nil {
		t.Fatal(serr)
	}
	e.waitBusy(t, 0)
	if _, serr := s.call("L", ctx, "invoke.stream.next", `{"handle":"`+h+`"}`); serr == nil || serr.Status != 404 {
		t.Fatalf("next after close = %+v", serr)
	}
}

func TestStreamLimitLeaseRevokeCloseAndForeignHandle(t *testing.T) {
	var fns []*database.Function
	for i := 0; i < 8; i++ {
		fns = append(fns, &database.Function{ID: fmt.Sprintf("s%d", i), Name: fmt.Sprintf("s%d", i)})
	}
	e := newInvEnv(t, fns...)
	s := newStreamHarness(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var handles []string
	for i := 0; i < 8; i++ {
		handles = append(handles, s.open(t, ctx, "L", fmt.Sprintf("s%d", i), `{"mode":"slowstream"}`))
	}
	e.waitBusy(t, 8)
	if _, serr := s.call("L", ctx, "invoke.stream.open", `{"name":"`+fnB+`","body":{"mode":"slowstream"}}`); serr == nil || serr.Status != 429 || serr.Code != "TOO_MANY_STREAMS" {
		t.Fatalf("9th open = %+v", serr)
	}
	// a handle from another lease is NOT_FOUND for next and close
	for _, op := range []string{"invoke.stream.next", "invoke.stream.close"} {
		if _, serr := s.call("OTHER", ctx, op, `{"handle":"`+handles[0]+`"}`); serr == nil || serr.Status != 404 {
			t.Fatalf("foreign %s = %+v", op, serr)
		}
	}
	e.waitBusy(t, 8) // the foreign close did not touch it
	cancel()
	s.revoke("L")
	e.waitBusy(t, 0)
	if _, serr := s.call("L", context.Background(), "invoke.stream.next", `{"handle":"`+handles[0]+`"}`); serr == nil || serr.Status != 404 {
		t.Fatalf("next after revoke = %+v", serr)
	}
	// channel close tears down streams of a still-live lease too
	h := s.open(t, context.Background(), "L2", fnB, `{"mode":"slowstream"}`)
	_ = h
	e.waitBusy(t, 1)
	s.sess.State(func() sandbox.SDKSessionState { return newStreamTable() }).Closed()
	e.waitBusy(t, 0)
}
