package sdkhost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

func snapshot(t tb, h *Host, fn string) string {
	t.Helper()
	entries, err := h.DB.KVList(fn, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s=%s;", e.Key, e.Value)
	}
	return sb.String()
}

const internalMsg = "sdk operation failed"

func TestDispatchAndIdentity(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	h := New(db)
	ctx := context.Background()
	// invoke* are implemented: without an invoker they are 501, and without a
	// live execution they are a scope violation once an invoker exists.
	for _, op := range []string{"invoke", "invoke.stream.open"} {
		if _, e := h.Handle(ctx, sandbox.SDKCaller{FunctionID: fnA}, op, nil); e == nil || e.Status != 501 || e.Code != "NOT_IMPLEMENTED" {
			t.Errorf("%s: %+v", op, e)
		}
	}
	for _, op := range []string{"invoke.stream.next", "invoke.stream.close"} {
		if _, e := h.Handle(ctx, sandbox.SDKCaller{FunctionID: fnA}, op, nil); e == nil || e.Status != 403 || e.Code != "SDK_SCOPE_VIOLATION" {
			t.Errorf("%s: %+v", op, e)
		}
	}
	for _, op := range []string{"", "kv", "kv.", "kv.nope", "KV.GET", "admin.keys", "kv.get\x00"} {
		if _, e := h.Handle(ctx, sandbox.SDKCaller{FunctionID: fnA}, op, nil); e == nil || e.Status != 400 || e.Code != "UNKNOWN_OP" {
			t.Errorf("%q: %+v", op, e)
		}
	}
	if _, e := h.Handle(ctx, sandbox.SDKCaller{}, "kv.get", json.RawMessage(`{"key":"k"}`)); e == nil || e.Status != 403 {
		t.Errorf("empty identity: %+v", e)
	}
	// module scope (Exec nil) and a live execution both work for kv.*
	if _, e := h.Handle(ctx, sandbox.SDKCaller{FunctionID: fnA}, "kv.put", json.RawMessage(`{"key":"m","value":1}`)); e != nil {
		t.Fatal(e)
	}
	exec := &sandbox.ExecInfo{ExecutionID: "e1", Lease: "l"}
	r, e := h.Handle(ctx, sandbox.SDKCaller{FunctionID: fnA, Exec: exec}, "kv.get", json.RawMessage(`{"key":"m"}`))
	if e != nil || r.(getResult).Value == nil {
		t.Fatalf("%v %v", r, e)
	}
	// a nil DB panics inside the op: it must surface as a 500, never a crash.
	if _, e := New(nil).Handle(ctx, sandbox.SDKCaller{FunctionID: fnA}, "kv.get", json.RawMessage(`{"key":"m"}`)); e == nil || e.Status != 500 {
		t.Errorf("panic not contained: %+v", e)
	}
}

type captureMetrics struct {
	mu   sync.Mutex
	seen []string
}

func (c *captureMetrics) ObserveSDKOp(op string, d time.Duration, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, fmt.Sprintf("%s:%d", op, status))
}

func TestMetrics(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	cm := &captureMetrics{}
	h := New(db, WithMetrics(cm))
	c := sandbox.SDKCaller{FunctionID: fnA}
	h.Handle(context.Background(), c, "kv.put", json.RawMessage(`{"key":"a","value":1}`))
	h.Handle(context.Background(), c, "kv.get", json.RawMessage(`{"key":"zz"}`))
	h.Handle(context.Background(), c, "whatever-attacker-chose", nil)
	h.Handle(context.Background(), c, "invoke", nil)
	want := []string{"kv.put:200", "kv.get:404", "unknown:400", "invoke:501"}
	if strings.Join(cm.seen, ",") != strings.Join(want, ",") {
		t.Fatalf("metrics %v want %v", cm.seen, want)
	}
	var puts, errs uint64
	for _, s := range h.Stats() {
		if s.Op == "kv.put" {
			puts = s.Count
		}
		if s.Op == "kv.get" {
			errs = s.Errors
		}
	}
	if puts != 1 || errs != 1 {
		t.Fatalf("stats puts=%d get errors=%d", puts, errs)
	}
}

func TestTTLExpiresAtIsRelativeToNow(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	d := sdkDriver{h: New(db), fn: fnA}
	must(t, d.call("put", `{"key":"t","value":1,"ttl_seconds":100}`), 200)
	// raw (un-normalized) check through the host itself
	r, e := d.h.Handle(context.Background(), sandbox.SDKCaller{FunctionID: fnA}, "kv.get", json.RawMessage(`{"key":"t"}`))
	if e != nil {
		t.Fatal(e)
	}
	exp := *r.(getResult).ExpiresAt
	ts, err := time.Parse("2006-01-02T15:04:05Z", exp)
	if err != nil {
		t.Fatal(err)
	}
	if delta := time.Until(ts); delta < 97*time.Second || delta > 101*time.Second {
		t.Fatalf("expires_at %s is %v away", exp, delta)
	}
}

func TestIsolationBetweenFunctions(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	h := New(db)
	b := sdkDriver{h: h, fn: fnB}
	a := sdkDriver{h: h, fn: fnA}
	for _, k := range []string{"secret", "fn-a/x", "b-only", "../secret"} {
		must(t, b.call("put", jb(map[string]any{"key": k, "value": "B-" + k})), 200)
	}
	must(t, b.call("incr", `{"key":"ctr","delta":7}`), 200)
	before := snapshot(t, h, fnB)

	// A reads nothing of B's, whatever it spells.
	for _, k := range []string{"secret", "b-only", "../secret", "fn-b/secret", "fn-b", "ctr"} {
		if r := a.call("get", jb(map[string]any{"key": k})); r.Status != 404 {
			t.Errorf("A saw B's key %q: %+v", k, r)
		}
	}
	for _, prefix := range []string{"", "fn-b", "fn-b/", "%", "_", "..", "'; --", "s%", "b_only"} {
		r := a.call("list", jb(map[string]any{"prefix": prefix, "limit": 1000}))
		if r.Status != 200 {
			t.Fatalf("%+v", r)
		}
		if ks := r.Body.(map[string]any)["keys"].([]any); len(ks) != 0 {
			t.Errorf("A listed B's keys with prefix %q: %v", prefix, ks)
		}
	}
	// A cannot name B via any field: identity fields are unknown fields.
	for _, args := range []string{
		`{"key":"secret","function_id":"fn-b"}`, `{"key":"secret","fn":"fn-b"}`,
		`{"key":"secret","fnId":"fn-b","value":1}`, `{"function_id":"fn-b","key":"secret","value":"pwn"}`,
		`{"key":"secret","value":"pwn","namespace":"fn-b"}`,
	} {
		for _, op := range []string{"get", "put", "delete", "incr", "cas"} {
			if r := a.call(op, args); r.Status != 400 || r.Code != "INVALID_JSON" {
				t.Errorf("%s %s: %+v", op, args, r)
			}
		}
	}
	// A writes/deletes/mutates keys that look like B's: they land in A only.
	must(t, a.call("put", `{"key":"secret","value":"A"}`), 200)
	must(t, a.call("delete", `{"key":"secret"}`), 200)
	must(t, a.call("incr", `{"key":"ctr","delta":100}`), 200)
	must(t, a.call("batch", `{"ops":[{"op":"delete","key":"secret"},{"op":"delete","key":"../secret"},{"op":"put","key":"fn-b/secret","value":1}]}`), 200)
	if r := a.call("cas", `{"key":"secret","expected":"B-secret","new":"pwn"}`); r.Status != 200 || r.Body.(map[string]any)["ok"] != false {
		t.Errorf("A cas against B's value succeeded: %+v", r)
	}
	if after := snapshot(t, h, fnB); after != before {
		t.Fatalf("B's namespace changed:\n before %s\n after  %s", before, after)
	}
	if r := b.call("incr", `{"key":"ctr","delta":0}`); r.Body.(map[string]any)["value"].(json.Number).String() != "7" {
		t.Errorf("B counter %+v", r)
	}
}

func TestConcurrentIncr(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	d := sdkDriver{h: New(db), fn: fnA}
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := d.call("incr", `{"key":"c"}`); r.Status != 200 {
				bad.Add(1)
				t.Errorf("%+v", r)
			}
		}()
	}
	wg.Wait()
	r := must(t, d.call("get", `{"key":"c"}`), 200)
	if v := bodyMap(t, r)["value"]; fmt.Sprint(v) != "50" {
		t.Fatalf("counter=%v (failed=%d)", v, bad.Load())
	}
}

func TestConcurrentCASExactlyOneWinner(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	d := sdkDriver{h: New(db), fn: fnA}
	var wg sync.WaitGroup
	var wins atomic.Int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := d.call("cas", jb(map[string]any{"key": "lock", "expected": nil, "new": i}))
			if r.Status == 200 && r.Body.(map[string]any)["ok"] == true {
				wins.Add(1)
			} else if r.Status != 200 {
				t.Errorf("%+v", r)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("winners=%d", wins.Load())
	}
}

func TestConcurrentPutGetDelete(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	h := New(db)
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			d := sdkDriver{h: h, fn: []string{fnA, fnB}[g%2]}
			for i := 0; i < 20; i++ {
				k := fmt.Sprintf("k%d", i%5)
				for _, c := range []struct{ op, args string }{
					{"put", jb(map[string]any{"key": k, "value": g})},
					{"get", jb(map[string]any{"key": k})},
					{"list", `{"limit":10}`},
					{"delete", jb(map[string]any{"key": k})},
					{"batch", jb(map[string]any{"ops": []any{map[string]any{"op": "put", "key": k, "value": 1}, map[string]any{"op": "get", "key": k}}})},
				} {
					if r := d.call(c.op, c.args); r.Status != 200 && r.Status != 404 {
						t.Errorf("%+v", r)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestStrictDecoding(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	h := New(db)
	c := sandbox.SDKCaller{FunctionID: fnA}
	nested := strings.Repeat("[", 200000) + strings.Repeat("]", 200000)
	big4 := `{"key":"k","value":"` + strings.Repeat("a", 4<<20) + `"}`
	cases := []struct {
		name, op, args string
		status         int
		code           string
	}{
		{"unknown field", "kv.put", `{"key":"k","value":1,"x":1}`, 400, "INVALID_JSON"},
		{"key wrong type", "kv.get", `{"key":5}`, 400, "INVALID_JSON"},
		{"key null is empty", "kv.get", `{"key":null}`, 400, "VALIDATION"},
		{"ttl wrong type", "kv.put", `{"key":"k","value":1,"ttl_seconds":"5"}`, 400, "INVALID_JSON"},
		{"ttl float", "kv.put", `{"key":"k","value":1,"ttl_seconds":1.5}`, 400, "INVALID_JSON"},
		{"ttl huge", "kv.put", `{"key":"k","value":1,"ttl_seconds":99999999999999999999999}`, 400, "INVALID_JSON"},
		{"ttl exp", "kv.put", `{"key":"k","value":1,"ttl_seconds":1e999}`, 400, "INVALID_JSON"},
		{"delta huge", "kv.incr", `{"key":"k","delta":99999999999999999999}`, 400, "INVALID_JSON"},
		{"delta float", "kv.incr", `{"key":"k","delta":1.5}`, 400, "INVALID_JSON"},
		{"limit bool", "kv.list", `{"limit":true}`, 400, "INVALID_JSON"},
		{"limit object", "kv.list", `{"limit":{}}`, 400, "INVALID_JSON"},
		{"limit string ok", "kv.list", `{"limit":"10"}`, 200, ""},
		{"limit fractional falls back like HTTP", "kv.list", `{"limit":10.5}`, 200, ""},
		{"limit huge falls back like HTTP", "kv.list", `{"limit":1e30}`, 200, ""},
		{"limit null (JS NaN)", "kv.list", `{"limit":null}`, 200, ""},
		{"truncated", "kv.put", `{"key":"k","value":`, 400, "INVALID_JSON"},
		{"truncated string", "kv.get", `{"key":"abc`, 400, "INVALID_JSON"},
		{"trailing garbage", "kv.get", `{"key":"k"} x`, 400, "INVALID_JSON"},
		{"two objects", "kv.get", `{"key":"k"}{"key":"k"}`, 400, "INVALID_JSON"},
		{"array args", "kv.get", `[1,2]`, 400, "INVALID_JSON"},
		{"string args", "kv.get", `"k"`, 400, "INVALID_JSON"},
		{"number args", "kv.list", `5`, 400, "INVALID_JSON"},
		{"not json", "kv.get", `\x00\xff`, 400, "INVALID_JSON"},
		{"invalid utf8 json", "kv.get", "{\"key\":\"\xff\xfe\"}", 404, ""}, // decoder substitutes U+FFFD; the key is then valid UTF-8 and simply absent
		{"json bomb", "kv.put", `{"key":"k","value":` + nested + `}`, 400, ""},
		{"bomb in batch", "kv.batch", `{"ops":[{"op":"put","key":"k","value":` + nested + `}]}`, 400, ""},
		{"4MiB args", "kv.put", big4, 400, "INVALID_BODY"},
		{"4MiB+1 get", "kv.get", strings.Repeat(" ", 4<<20+1), 400, "INVALID_BODY"},
		{"batch ops wrong type", "kv.batch", `{"ops":"x"}`, 400, "INVALID_JSON"},
		{"batch op unknown field", "kv.batch", `{"ops":[{"op":"get","key":"k","z":1}]}`, 400, "INVALID_JSON"},
		{"cas expected invalid via raw", "kv.cas", `{"key":"k","expected":{"a":,},"new":1}`, 400, "INVALID_JSON"},
		{"empty args get", "kv.get", ``, 400, "VALIDATION"},
		{"null args get", "kv.get", `null`, 400, "VALIDATION"},
		{"empty args list ok", "kv.list", ``, 200, ""},
		{"whitespace args list ok", "kv.list", "  {}\n", 200, ""},
		{"empty args incr needs key", "kv.incr", ``, 400, "VALIDATION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, e := h.Handle(context.Background(), c, tc.op, json.RawMessage(tc.args))
			if e != nil && e.Message == internalMsg {
				t.Fatalf("panic contained: %+v", e)
			}
			got, code := 200, ""
			if e != nil {
				got, code = e.Status, e.Code
			}
			if got != tc.status || (tc.code != "" && code != tc.code) {
				t.Fatalf("status=%d code=%s want %d %s (%+v)", got, code, tc.status, tc.code, e)
			}
			if tc.status == 400 && tc.code == "" && code != "INVALID_JSON" && code != "INVALID_BODY" {
				t.Fatalf("code=%s", code)
			}
		})
	}
	if s := snapshot(t, h, fnA); strings.Contains(s, "bomb") {
		t.Fatalf("rejected write leaked: %s", s)
	}
}

func FuzzHandleKV(f *testing.F) {
	db := newDB(f)
	f.Cleanup(func() { db.Close() })
	h := New(db)
	for _, k := range []string{"secret", "../secret", "fn-b/x"} {
		if err := db.KVPut(fnB, k, []byte(`"B"`), nil); err != nil {
			f.Fatal(err)
		}
	}
	if err := db.KVPut(fnB, "ctr", []byte(`5`), nil); err != nil {
		f.Fatal(err)
	}
	before := snapshot(f, h, fnB)
	ops := []string{"kv.get", "kv.put", "kv.delete", "kv.list", "kv.batch", "kv.incr", "kv.cas", "invoke", "nope", ""}
	seeds := []string{
		`{"key":"k"}`, `{"key":"secret","value":1}`, `{"key":"../secret","value":{"a":[1]},"ttl_seconds":5}`,
		`{"prefix":"fn-b","limit":3}`, `{"ops":[{"op":"put","key":"fn-b/x","value":1},{"op":"delete","key":"secret"}]}`,
		`{"key":"ctr","delta":3}`, `{"key":"secret","expected":"B","new":"pwn"}`, `{"function_id":"fn-b","key":"secret"}`,
		`{"key":"` + strings.Repeat("é", 300) + `"}`, `[[[[[[`, `null`, ``, `{"key":"k","ttl_seconds":-1}`,
	}
	for i, s := range seeds {
		f.Add(ops[i%len(ops)], []byte(s))
		f.Add("kv.put", []byte(s))
	}
	f.Fuzz(func(t *testing.T, op string, args []byte) {
		if len(args) > 1<<17 {
			return
		}
		res, e := h.Handle(context.Background(), sandbox.SDKCaller{FunctionID: fnA}, op, json.RawMessage(args))
		if e != nil {
			if e.Message == internalMsg {
				t.Fatalf("panic in %q %q", op, args)
			}
			if e.Status < 400 || e.Status > 599 || e.Code == "" {
				t.Fatalf("bad error %+v", e)
			}
		} else if _, err := json.Marshal(res); err != nil {
			t.Fatalf("unmarshalable result: %v", err)
		}
		if after := snapshot(t, h, fnB); after != before {
			t.Fatalf("function B's namespace changed by %q %q", op, args)
		}
	})
}
