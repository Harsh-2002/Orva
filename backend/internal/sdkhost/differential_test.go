package sdkhost

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Every scenario here runs through the real HTTP KV handler and through
// Host.Handle against separate fresh databases; each step must be identical.

func TestDiffRoundTrips(t *testing.T) {
	runBoth(t, func(r *rec) {
		vals := []string{`"str"`, `42`, `-1.5e3`, `null`, `true`, `[1,"a",null,{"x":[]}]`, `{"a":{"b":[1,2]},"c":"é"}`, `""`, `{}`, `[]`}
		for i, v := range vals {
			k := fmt.Sprintf("rt%d", i)
			r.do("put", fmt.Sprintf(`{"key":%q,"value":%s}`, k, v))
			r.do("get", fmt.Sprintf(`{"key":%q}`, k))
		}
		r.do("put", `{"key":"rt0","value":"overwritten"}`)
		r.do("get", `{"key":"rt0"}`)
		r.do("delete", `{"key":"rt0"}`)
		r.do("get", `{"key":"rt0"}`)
		r.do("delete", `{"key":"rt0"}`) // idempotent
		r.do("get", `{"key":"never"}`)
		r.do("put", `{"key":"novalue"}`)
		r.do("put", `{"key":"k","value":"x","ttl_seconds":-1}`)
		r.do("put", `{"key":"k","value":"x","ttl_seconds":9223372036854775807}`)
	})
}

func TestDiffTTL(t *testing.T) {
	runBoth(t, func(r *rec) {
		r.do("put", `{"key":"t","value":1,"ttl_seconds":1}`)
		r.do("get", `{"key":"t"}`)
		r.do("put", `{"key":"keep","value":1,"ttl_seconds":1}`)
		r.do("put", `{"key":"keep","value":2}`) // nil TTL preserves expiry
		r.do("get", `{"key":"keep"}`)
		r.do("put", `{"key":"clear","value":1,"ttl_seconds":1}`)
		r.do("put", `{"key":"clear","value":2,"ttl_seconds":0}`) // zero clears
		r.do("incr", `{"key":"ctr","delta":3,"ttl_seconds":1}`)
		r.do("cas", `{"key":"casttl","expected":null,"new":"a","ttl_seconds":1}`)
		r.do("batch", `{"ops":[{"op":"put","key":"bt","value":1,"ttl_seconds":1}]}`)
		r.do("list", `{}`)
		sleepPastTTL()
		r.do("get", `{"key":"t"}`)
		r.do("get", `{"key":"keep"}`)
		r.do("get", `{"key":"clear"}`)
		r.do("get", `{"key":"ctr"}`)
		r.do("batch", `{"ops":[{"op":"get","key":"bt"},{"op":"get","key":"clear"}]}`)
		r.do("list", `{}`)
		r.do("incr", `{"key":"ctr","delta":3}`) // expired counts as missing
		r.do("cas", `{"key":"casttl","expected":"a","new":"b"}`)
		r.do("cas", `{"key":"casttl","expected":null,"new":"b"}`) // expired = absent
	})
}

func TestDiffListPagination(t *testing.T) {
	runBoth(t, func(r *rec) {
		for i := 0; i < 25; i++ {
			r.do("put", fmt.Sprintf(`{"key":%q,"value":%d}`, keyName(i), i))
		}
		r.do("put", `{"key":"other/x","value":"o"}`)
		r.do("put", `{"key":"k%x","value":"pct"}`)
		for _, lim := range []string{`10`, `0`, `-5`, `5000`, `1000`, `1`, `10.5`, `1e30`, `99999999999999999999`, `null`} {
			r.do("list", `{"limit":`+lim+`}`)
		}
		r.do("list", `{}`)
		r.do("list", `{"prefix":"k","limit":7}`)
		r.do("list", `{"prefix":"%","limit":3}`) // LIKE wildcard semantics preserved
		r.do("list", `{"prefix":"nomatch"}`)
		r.do("list", `{"prefix":"other/"}`)
		r.do("list", `{"cursor":"zzz"}`)
		for _, prefix := range []string{"", "k"} {
			cursor := ""
			for page := 0; page < 20; page++ {
				args := map[string]any{"prefix": prefix, "limit": 4}
				if cursor != "" {
					args["cursor"] = cursor
				}
				x := r.do("list", jb(args))
				m, _ := x.Body.(map[string]any)
				next, _ := m["next_cursor"].(string)
				if next == "" {
					break
				}
				cursor = next
			}
		}
	})
}

func TestDiffBatch(t *testing.T) {
	runBoth(t, func(r *rec) {
		r.do("put", `{"key":"exist","value":"v"}`)
		r.do("batch", `{"ops":[]}`)
		r.do("batch", `{}`)
		r.do("batch", `{"ops":[{"op":"put","key":"a","value":1},{"op":"get","key":"a"},{"op":"get","key":"exist"},{"op":"delete","key":"exist"},{"op":"delete","key":"exist"},{"op":"get","key":"missing"}]}`)
		r.do("get", `{"key":"a"}`)
		// atomic failure: a validation error in a later op writes nothing.
		r.do("batch", `{"ops":[{"op":"put","key":"p1","value":1},{"op":"put","key":"p2"}]}`)
		r.do("get", `{"key":"p1"}`)
		r.do("batch", `{"ops":[{"op":"put","key":"p1","value":1},{"op":"nope","key":"p2"}]}`)
		r.do("get", `{"key":"p1"}`)
		r.do("batch", `{"ops":[{"op":"put","key":"p1","value":1},{"op":"get","key":""}]}`)
		r.do("batch", `{"ops":[{"op":"put","key":"p1","value":1,"ttl_seconds":-3}]}`)
		r.do("batch", `{"ops":[{"op":"put","key":"p1","value":`+jstr(64*1024+1)+`}]}`)
		r.do("get", `{"key":"p1"}`)
		var big []string
		for i := 0; i < 100; i++ {
			big = append(big, fmt.Sprintf(`{"op":"put","key":"b%d","value":%d}`, i, i))
		}
		r.do("batch", `{"ops":[`+strings.Join(big, ",")+`]}`)
		r.do("list", `{"prefix":"b","limit":1000}`)
		big = append(big, `{"op":"put","key":"b100","value":100}`)
		r.do("batch", `{"ops":[`+strings.Join(big, ",")+`]}`)
		r.do("get", `{"key":"b100"}`)
	})
}

func TestDiffIncr(t *testing.T) {
	runBoth(t, func(r *rec) {
		r.do("incr", `{"key":"c"}`)
		r.do("incr", `{"key":"c","delta":5}`)
		r.do("incr", `{"key":"c","delta":-20}`)
		r.do("incr", `{"key":"c","delta":0}`)
		r.do("get", `{"key":"c"}`)
		r.do("put", `{"key":"s","value":"7"}`)
		r.do("incr", `{"key":"s","delta":1}`)
		r.do("put", `{"key":"n","value":41}`)
		r.do("incr", `{"key":"n"}`)
		for i, v := range []string{`"abc"`, `1.5`, `null`, `[1]`, `{"a":1}`, `true`, `""`} {
			k := fmt.Sprintf("bad%d", i)
			r.do("put", fmt.Sprintf(`{"key":%q,"value":%s}`, k, v))
			r.do("incr", fmt.Sprintf(`{"key":%q}`, k))
			r.do("get", fmt.Sprintf(`{"key":%q}`, k))
		}
		r.do("incr", `{"key":"c","ttl_seconds":-1}`)
		r.do("incr", `{"key":"","delta":1}`)
		r.do("incr", `{"key":"big","delta":9223372036854775807}`)
		r.do("incr", `{"key":"big","delta":1}`) // wraps identically
	})
}

func TestDiffCAS(t *testing.T) {
	runBoth(t, func(r *rec) {
		r.do("cas", `{"key":"c","expected":null,"new":1}`)
		r.do("cas", `{"key":"c","expected":null,"new":2}`) // exists -> mismatch with current
		r.do("cas", `{"key":"c","new":2}`)                 // omitted expected == null
		r.do("cas", `{"key":"c","expected":1,"new":{"x":[1]}}`)
		r.do("cas", `{"key":"c","expected":1,"new":3}`)
		r.do("get", `{"key":"c"}`)
		r.do("cas", `{"key":"missing","expected":"x","new":1}`) // absent + non-null expected
		r.do("cas", `{"key":"c","expected":{"x":[1]},"new":null}`)
		r.do("get", `{"key":"c"}`)
		r.do("cas", `{"key":"c","expected":null,"new":"after-null"}`)
		r.do("cas", `{"key":"c","expected":"after-null"}`)
		r.do("cas", `{"key":"c","expected":"after-null","new":`+jstr(64*1024+1)+`}`)
		r.do("cas", `{"key":"c","expected":"after-null","new":"ok","ttl_seconds":-1}`)
		r.do("cas", `{"key":"","expected":null,"new":1}`)
		r.do("cas", `{"key":"c","expected":`+jstr(64*1024+1)+`,"new":1}`)
	})
}

func TestDiffSizesAndKeys(t *testing.T) {
	runBoth(t, func(r *rec) {
		r.do("put", `{"key":"max","value":`+jstr(64*1024)+`}`)
		r.do("get", `{"key":"max"}`)
		r.do("put", `{"key":"over","value":`+jstr(64*1024+1)+`}`)
		r.do("get", `{"key":"over"}`)
		keys := []string{
			strings.Repeat("a", 256), strings.Repeat("a", 257),
			strings.Repeat("é", 256), strings.Repeat("é", 257), strings.Repeat("日", 256),
			"a/b/c", "/lead", "trail/", "sp ace", "100%", "a_b", "q?x=1&y=2", "#frag", `quo"te`, `back\slash`,
			"'; DROP TABLE kv_store;--", "fn-b", "fn-b/secret", "../fn-b/secret", "..", ".", "🔥emoji", "tab\tkey",
			"nul\u0000byte", " ", "A", "a", "Z",
		}
		for _, k := range keys {
			kb, _ := json.Marshal(k)
			r.do("put", `{"key":`+string(kb)+`,"value":"v"}`)
			r.do("get", `{"key":`+string(kb)+`}`)
			r.do("incr", `{"key":`+string(kb)+`}`)
			r.do("delete", `{"key":`+string(kb)+`}`)
			r.do("get", `{"key":`+string(kb)+`}`)
		}
		r.do("put", `{"key":"","value":1}`)
		r.do("get", `{"key":""}`)
		r.do("delete", `{"key":""}`)
		r.do("list", `{"limit":1000}`)
	})
}
