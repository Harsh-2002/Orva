package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

const (
	fnA = "fn-a"
	fnB = "fn-b"
)

type tb interface {
	Helper()
	Fatal(args ...any)
	TempDir() string
}

func newDB(t tb) *database.Database {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "sdkhost.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fnA, fnB} {
		err := db.InsertFunction(&database.Function{
			ID: id, Name: id, Runtime: "node", Entrypoint: "handler.js", TimeoutMS: 30000,
			MemoryMB: 64, CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// reply is the observable outcome of one operation, identical for both transports.
type reply struct {
	Step    string
	Status  int
	Code    string
	Message string
	Body    any
}

type driver interface {
	call(op, args string) reply
}

// normalize replaces wall-clock fields, which cannot be equal across two runs.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if k == "expires_at" {
				if _, ok := e.(string); ok {
					x[k] = "<ts>"
					continue
				}
			}
			x[k] = normalize(e)
		}
		return x
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
	}
	return v
}

func decodeAny(b []byte) any {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "undecodable:" + string(b)
	}
	return normalize(v)
}

// ---- SDK transport ----

type sdkDriver struct {
	h  *Host
	fn string
}

func (d sdkDriver) call(op, args string) reply {
	r, e := d.h.Handle(context.Background(), sandbox.SDKCaller{FunctionID: d.fn}, "kv."+op, json.RawMessage(args))
	out := reply{Step: op + " " + args}
	if e != nil {
		out.Status, out.Code, out.Message = e.Status, e.Code, e.Message
		return out
	}
	b, err := json.Marshal(r)
	if err != nil {
		out.Status, out.Code = -1, "MARSHAL:"+err.Error()
		return out
	}
	out.Status = 200
	out.Body = decodeAny(b)
	return out
}

// ---- HTTP transport: the real handler, a minted credential ----

type httpDriver struct {
	h    *handlers.KVHandler
	auth *sdkauth.Authenticator
	fn   string
}

func newHTTPDriver(db *database.Database, fn string) httpDriver {
	a := sdkauth.New([]byte("secret"))
	return httpDriver{h: &handlers.KVHandler{DB: db, SDKAuth: a}, auth: a, fn: fn}
}

func (d httpDriver) call(op, args string) reply {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		panic("test args must be a JSON object: " + args)
	}
	var key string
	if raw, ok := m["key"]; ok {
		_ = json.Unmarshal(raw, &key)
		delete(m, "key")
	}
	rest, _ := json.Marshal(m)
	var req *http.Request
	var fn func(http.ResponseWriter, *http.Request)
	switch op {
	case "get":
		req, fn = httptest.NewRequest("GET", "/x", nil), d.h.Get
	case "delete":
		req, fn = httptest.NewRequest("DELETE", "/x", nil), d.h.Delete
	case "put":
		req, fn = httptest.NewRequest("PUT", "/x", bytes.NewReader(rest)), d.h.Put
	case "incr":
		req, fn = httptest.NewRequest("POST", "/x", bytes.NewReader(rest)), d.h.Incr
	case "cas":
		req, fn = httptest.NewRequest("POST", "/x", bytes.NewReader(rest)), d.h.CAS
	case "batch":
		req, fn = httptest.NewRequest("POST", "/x", bytes.NewReader([]byte(args))), d.h.Batch
	case "list":
		q := url.Values{}
		for _, k := range []string{"prefix", "cursor"} {
			if raw, ok := m[k]; ok {
				var s string
				_ = json.Unmarshal(raw, &s)
				q.Set(k, s)
			}
		}
		if raw, ok := m["limit"]; ok {
			q.Set("limit", string(raw))
		}
		req, fn = httptest.NewRequest("GET", "/x?"+q.Encode(), nil), d.h.List
	default:
		panic("op " + op)
	}
	req.SetPathValue("fn_id", d.fn)
	req.SetPathValue("key", key)
	tok, _ := d.auth.Mint(d.fn)
	req.Header.Set("X-Orva-Internal-Token", tok)
	w := httptest.NewRecorder()
	fn(w, req)
	out := reply{Step: op + " " + args, Status: w.Code}
	body := decodeAny(w.Body.Bytes())
	if w.Code >= 400 {
		if m, ok := body.(map[string]any); ok {
			if e, ok := m["error"].(map[string]any); ok {
				out.Code, _ = e["code"].(string)
				out.Message, _ = e["message"].(string)
			}
		}
		return out
	}
	out.Body = body
	return out
}

// rec records every reply of a scenario.
type rec struct {
	d   driver
	log []reply
}

func (r *rec) do(op, args string) reply {
	x := r.d.call(op, args)
	r.log = append(r.log, x)
	return x
}

func jb(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func jstr(n int) string { return `"` + strings.Repeat("a", n-2) + `"` }

// runBoth runs one scenario through the HTTP handler and through Host.Handle on
// two fresh databases and requires every step to be observably identical.
func runBoth(t *testing.T, scenario func(r *rec)) {
	t.Helper()
	dbH, dbS := newDB(t), newDB(t)
	t.Cleanup(func() { dbH.Close(); dbS.Close() })
	rh := &rec{d: newHTTPDriver(dbH, fnA)}
	rs := &rec{d: sdkDriver{h: New(dbS), fn: fnA}}
	scenario(rh)
	scenario(rs)
	if len(rh.log) != len(rs.log) {
		t.Fatalf("step count differs http=%d sdk=%d", len(rh.log), len(rs.log))
	}
	for i := range rh.log {
		a, b := rh.log[i], rs.log[i]
		ja, _ := json.Marshal(a.Body)
		jbb, _ := json.Marshal(b.Body)
		if a.Status != b.Status || a.Code != b.Code || a.Message != b.Message || string(ja) != string(jbb) {
			t.Errorf("step %d %q differs:\n http: %d %s %q %s\n sdk:  %d %s %q %s",
				i, a.Step, a.Status, a.Code, a.Message, ja, b.Status, b.Code, b.Message, jbb)
		}
	}
}

func sleepPastTTL() { time.Sleep(1200 * time.Millisecond) }

func must(t *testing.T, r reply, status int) reply {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: status=%d code=%s msg=%s want %d", r.Step, r.Status, r.Code, r.Message, status)
	}
	return r
}

func bodyMap(t *testing.T, r reply) map[string]any {
	t.Helper()
	m, ok := r.Body.(map[string]any)
	if !ok {
		t.Fatalf("%s: body %T not an object", r.Step, r.Body)
	}
	return m
}

func keyName(i int) string { return fmt.Sprintf("k%02d", i) }
