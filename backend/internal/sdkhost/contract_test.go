package sdkhost

import (
	"encoding/json"
	"strings"
	"testing"
)

// The request and reply shapes below are exactly what the runtime SDKs
// (backend/runtimes/node/orva.js, backend/runtimes/python/orva.py, "kv.*" over
// the SDK channel) send and read:
//
//	kv.get    {key}                                 -> {value, expires_at?}   404 = missing
//	kv.put    {key, value, ttl_seconds?}            -> (reply body ignored by the SDK)
//	kv.delete {key}                                 -> (reply body ignored; 404 ignored)
//	kv.list   {limit, prefix?, cursor?}             -> {keys:[{key,value,expires_at?}], next_cursor?}
//	kv.batch  {ops:[{op,key,value?,ttl_seconds?}]}  -> {results:[{op,key,found,value?,expires_at?,error?}]}
//	kv.incr   {key, delta, ttl_seconds?}            -> {value}
//	kv.cas    {key, expected, new, ttl_seconds?}    -> {ok, current?}         ok:false is a 200 reply
//
// If a SDK change breaks one of these, this table must change in the same PR.
// expires_at is compared as the placeholder "<ts>".
func TestSDKContract(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	d := sdkDriver{h: New(db), fn: fnA}

	type step struct {
		op, args string
		status   int
		reply    string // success: exact JSON; error: "CODE: message"
	}
	steps := []step{
		// put / get
		{"put", `{"key":"a","value":{"n":1},"ttl_seconds":5}`, 200, `{"status":"ok","key":"a","ttl_seconds":5}`},
		{"get", `{"key":"a"}`, 200, `{"value":{"n":1},"expires_at":"<ts>"}`},
		{"put", `{"key":"b","value":"x"}`, 200, `{"status":"ok","key":"b","ttl_seconds":null}`},
		{"get", `{"key":"b"}`, 200, `{"value":"x"}`},
		{"get", `{"key":"missing"}`, 404, `NOT_FOUND: key not found`},
		{"put", `{"key":"nul","value":null}`, 200, `{"status":"ok","key":"nul","ttl_seconds":null}`},
		{"get", `{"key":"nul"}`, 200, `{"value":null}`},
		// list: SDK reads keys + next_cursor; empty next_cursor is simply absent
		{"list", `{"limit":1}`, 200, `{"keys":[{"key":"a","value":{"n":1},"expires_at":"<ts>"}],"next_cursor":"a"}`},
		{"list", `{"limit":100,"cursor":"a"}`, 200, `{"keys":[{"key":"b","value":"x"},{"key":"nul","value":null}]}`},
		{"list", `{"limit":100,"prefix":"n"}`, 200, `{"keys":[{"key":"nul","value":null}]}`},
		{"list", `{"limit":100,"prefix":"zzz"}`, 200, `{"keys":[]}`},
		// batch: results carry key/found/value, per-op order
		{"batch", `{"ops":[{"op":"get","key":"a"},{"op":"get","key":"zz"},{"op":"put","key":"c","value":1,"ttl_seconds":0},{"op":"delete","key":"b"},{"op":"delete","key":"b"}]}`, 200,
			`{"results":[{"op":"get","key":"a","found":true,"value":{"n":1},"expires_at":"<ts>"},{"op":"get","key":"zz","found":false},{"op":"put","key":"c","found":true},{"op":"delete","key":"b","found":true},{"op":"delete","key":"b","found":false}]}`},
		{"batch", `{"ops":[]}`, 200, `{"results":[]}`},
		{"batch", `{"ops":[{"op":"put","key":"atomic","value":1},{"op":"get","key":""}]}`, 400, `VALIDATION: kv: key is required`},
		{"get", `{"key":"atomic"}`, 404, `NOT_FOUND: key not found`},
		// incr
		{"incr", `{"key":"n","delta":1}`, 200, `{"value":1}`},
		{"incr", `{"key":"n","delta":4,"ttl_seconds":0}`, 200, `{"value":5}`},
		{"incr", `{"key":"n","delta":-7}`, 200, `{"value":-2}`},
		{"incr", `{"key":"a","delta":1}`, 409, `KV_INCR_FAILED: kv.incr: existing value is not an integer`},
		// cas: mismatch is ok:false (200), not an error reply
		{"cas", `{"key":"lock","expected":null,"new":"me"}`, 200, `{"ok":true}`},
		{"cas", `{"key":"lock","expected":null,"new":"you"}`, 200, `{"ok":false,"current":"me"}`},
		{"cas", `{"key":"lock","expected":"nope","new":"you"}`, 200, `{"ok":false,"current":"me"}`},
		{"cas", `{"key":"gone","expected":"x","new":"y"}`, 200, `{"ok":false}`},
		{"cas", `{"key":"lock","expected":"me","new":{"v":2},"ttl_seconds":0}`, 200, `{"ok":true}`},
		{"get", `{"key":"lock"}`, 200, `{"value":{"v":2}}`},
		// delete: idempotent
		{"delete", `{"key":"a"}`, 200, `{"status":"deleted","key":"a"}`},
		{"delete", `{"key":"a"}`, 200, `{"status":"deleted","key":"a"}`},
		{"get", `{"key":"a"}`, 404, `NOT_FOUND: key not found`},
		// validation errors (status, code and wording shared with the HTTP surface)
		{"get", `{"key":""}`, 400, `VALIDATION: kv: key is required`},
		{"put", `{"key":"k"}`, 400, `VALIDATION: kv: value must be valid JSON`},
		{"put", `{"key":"k","value":1,"ttl_seconds":-1}`, 400, `VALIDATION: kv: ttl_seconds must be >= 0`},
		{"put", `{"key":"big","value":` + jstr(64*1024+1) + `}`, 413, `VALIDATION: kv: value exceeds 64 KB`},
		{"put", `{"key":"` + strings.Repeat("k", 257) + `","value":1}`, 400, `VALIDATION: kv: key exceeds 256 characters`},
		{"cas", `{"key":"k","expected":null}`, 400, `VALIDATION: new value is required`},
		{"batch", `{"ops":[{"op":"zap","key":"k"}]}`, 400, `VALIDATION: unknown batch operation: zap`},
		// JS sends Number(limit): NaN/Infinity arrive as null, fractions as floats
		{"list", `{"limit":null}`, 200, ``},
		{"list", `{"limit":2.5}`, 200, ``},
	}
	for i, s := range steps {
		got := d.call(s.op, s.args)
		if got.Status != s.status {
			t.Errorf("#%d kv.%s %s: status %d (%s: %s), want %d", i, s.op, s.args, got.Status, got.Code, got.Message, s.status)
			continue
		}
		if s.reply == "" {
			continue
		}
		if s.status != 200 {
			if want := got.Code + ": " + got.Message; want != s.reply {
				t.Errorf("#%d kv.%s %s: error %q, want %q", i, s.op, s.args, want, s.reply)
			}
			continue
		}
		wantJSON, _ := json.Marshal(decodeAny([]byte(s.reply)))
		gotJSON, _ := json.Marshal(got.Body)
		if string(wantJSON) != string(gotJSON) {
			t.Errorf("#%d kv.%s %s:\n got  %s\n want %s", i, s.op, s.args, gotJSON, wantJSON)
		}
	}
}
