package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
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

// dbDriver is an independent reference over the shared database cores, not Host.Handle.
type dbDriver struct {
	db *database.Database
	fn string
}

func (d dbDriver) call(op, args string) reply {
	out := reply{Step: op + " " + args, Status: 200}
	reject := func(status int, code, message string) reply {
		out.Status, out.Code, out.Message = status, code, message
		return out
	}
	var a struct {
		Key, Prefix, Cursor string
		Value, New, Limit   json.RawMessage
		Expected            *json.RawMessage
		TTLSeconds          *int `json:"ttl_seconds"`
		Delta               int64
		Ops                 []struct {
			Op, Key    string
			Value      json.RawMessage
			TTLSeconds *int `json:"ttl_seconds"`
		}
	}
	a.Delta = 1
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return reject(400, "INVALID_JSON", "invalid request body")
	}
	if op != "batch" && op != "list" {
		if err := database.ValidateKVKey(a.Key); err != nil {
			return reject(400, "VALIDATION", err.Error())
		}
	}
	var result any
	var err error
	switch op {
	case "put":
		err = d.db.KVPut(d.fn, a.Key, a.Value, a.TTLSeconds)
		if err != nil && len(a.Value) > database.KVMaxValueBytes {
			return reject(413, "VALIDATION", err.Error())
		}
		result = map[string]any{"status": "ok", "key": a.Key, "ttl_seconds": a.TTLSeconds}
	case "get":
		var entry *database.KVEntry
		entry, err = d.db.KVGet(d.fn, a.Key)
		if errors.Is(err, database.ErrKVNotFound) {
			return reject(404, "NOT_FOUND", "key not found")
		}
		if err == nil {
			result = referenceEntry(entry, false)
		}
	case "delete":
		err = d.db.KVDelete(d.fn, a.Key)
		result = map[string]any{"status": "deleted", "key": a.Key}
	case "list":
		limit := 100
		if n, e := strconv.Atoi(strings.Trim(string(a.Limit), `"`)); e == nil {
			limit = n
		}
		var page *database.KVListPage
		page, err = d.db.KVListWithCursor(d.fn, a.Prefix, a.Cursor, limit)
		if err == nil {
			rows := make([]any, 0, len(page.Entries))
			for _, entry := range page.Entries {
				rows = append(rows, referenceEntry(entry, true))
			}
			body := map[string]any{"keys": rows}
			if page.NextCursor != "" {
				body["next_cursor"] = page.NextCursor
			}
			result = body
		}
	case "incr":
		var next int64
		next, err = d.db.KVIncr(d.fn, a.Key, a.Delta, a.TTLSeconds)
		if err != nil && strings.Contains(err.Error(), "not an integer") {
			return reject(409, "KV_INCR_FAILED", err.Error())
		}
		result = map[string]any{"value": next}
	case "cas":
		if len(a.New) == 0 {
			return reject(400, "VALIDATION", "new value is required")
		}
		if e := database.ValidateKVValue(a.New); e != nil {
			return reject(400, "VALIDATION", e.Error())
		}
		if e := database.ValidateKVTTL(a.TTLSeconds); e != nil {
			return reject(400, "VALIDATION", e.Error())
		}
		var expected []byte
		if a.Expected != nil {
			expected = []byte(*a.Expected)
			if e := database.ValidateKVValue(expected); e != nil {
				return reject(400, "VALIDATION", "expected value: "+e.Error())
			}
		}
		var ok bool
		var current []byte
		ok, current, err = d.db.KVCAS(d.fn, a.Key, expected, a.New, a.TTLSeconds)
		body := map[string]any{"ok": ok}
		if !ok && current != nil {
			body["current"] = json.RawMessage(current)
		}
		result = body
	case "batch":
		if len(a.Ops) > 100 {
			return reject(400, "VALIDATION", "batch capped at 100 ops")
		}
		ops := make([]database.KVBatchOp, len(a.Ops))
		for i, row := range a.Ops {
			if e := database.ValidateKVKey(row.Key); e != nil {
				return reject(400, "VALIDATION", e.Error())
			}
			if row.Op != "put" && row.Op != "get" && row.Op != "delete" {
				return reject(400, "VALIDATION", "unknown batch operation: "+row.Op)
			}
			ops[i] = database.KVBatchOp{Op: row.Op, Key: row.Key, Value: row.Value, TTLSeconds: row.TTLSeconds}
		}
		var rows []database.KVBatchResult
		rows, err = d.db.KVBatch(d.fn, ops)
		wire := make([]any, 0, len(rows))
		for _, row := range rows {
			entry := map[string]any{"op": row.Op, "key": row.Key, "found": row.Found}
			if row.Value != nil {
				entry["value"] = json.RawMessage(row.Value)
			}
			if row.ExpiresAt != nil {
				entry["expires_at"] = row.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			if row.Err != "" {
				entry["error"] = row.Err
			}
			wire = append(wire, entry)
		}
		result = map[string]any{"results": wire}
	default:
		panic("op " + op)
	}
	if err != nil {
		return reject(400, "VALIDATION", err.Error())
	}
	b, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	out.Body = decodeAny(b)
	return out
}

func referenceEntry(entry *database.KVEntry, withKey bool) map[string]any {
	out := map[string]any{"value": json.RawMessage(entry.Value)}
	if withKey {
		out["key"] = entry.Key
	}
	if entry.ExpiresAt != nil {
		out["expires_at"] = entry.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	}
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

// runBoth runs one scenario through the database cores and through Host.Handle on
// two fresh databases and requires every step to be observably identical.
func runBoth(t *testing.T, scenario func(r *rec)) {
	t.Helper()
	dbH, dbS := newDB(t), newDB(t)
	t.Cleanup(func() { dbH.Close(); dbS.Close() })
	rh := &rec{d: dbDriver{db: dbH, fn: fnA}}
	rs := &rec{d: sdkDriver{h: New(dbS), fn: fnA}}
	scenario(rh)
	scenario(rs)
	if len(rh.log) != len(rs.log) {
		t.Fatalf("step count differs core=%d sdk=%d", len(rh.log), len(rs.log))
	}
	for i := range rh.log {
		a, b := rh.log[i], rs.log[i]
		ja, _ := json.Marshal(a.Body)
		jbb, _ := json.Marshal(b.Body)
		if a.Status != b.Status || a.Code != b.Code || a.Message != b.Message || string(ja) != string(jbb) {
			t.Errorf("step %d %q differs:\n core: %d %s %q %s\n sdk:  %d %s %q %s",
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
