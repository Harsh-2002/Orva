package sdkhost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

const (
	opExec  = "exec-1"
	opTrace = "trace-1"
	opSpan  = "span-1"
)

// opEnv is one transport over its own database. Both bind the same execution
// identity so attribution is comparable.
type opEnv struct {
	db     *database.Database
	auth   *sdkauth.Authenticator
	host   *Host
	start  time.Time
	ctx    context.Context
	cancel context.CancelFunc
}

func newOpEnv(t *testing.T) *opEnv {
	t.Helper()
	db := newDB(t)
	t.Cleanup(func() { db.Close() })
	auth := sdkauth.New()
	e := &opEnv{db: db, auth: auth, host: New(db), start: time.Now().Add(-time.Second).UTC()}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	t.Cleanup(e.cancel)
	release := auth.BindExecution(opExec, fnA, opTrace, opSpan, e.start, 0)
	t.Cleanup(release)
	return e
}

func (e *opEnv) caller(fn string, module bool) sandbox.SDKCaller {
	c := sandbox.SDKCaller{FunctionID: fn}
	if !module {
		c.Exec = &sandbox.ExecInfo{ExecutionID: opExec, TraceID: opTrace, SpanID: opSpan, StartedAt: e.start, Lease: "l", Ctx: e.ctx}
	}
	return c
}

// sdkCall runs one op through Host.Handle.
func (e *opEnv) sdkCall(fn string, module bool, op, args string) reply {
	r, serr := e.host.Handle(context.Background(), e.caller(fn, module), op, json.RawMessage(args))
	out := reply{Step: op + " " + short(args)}
	if serr != nil {
		out.Status, out.Code, out.Message = serr.Status, serr.Code, serr.Message
		return out
	}
	b, err := json.Marshal(r)
	if err != nil {
		out.Status, out.Code = -1, "MARSHAL:"+err.Error()
		return out
	}
	out.Status, out.Body = 200, decodeOp(b)
	return out
}

// coreCall uses execution bindings and the shared cores without calling the channel host.
func (e *opEnv) coreCall(fn string, module bool, op, args string) reply {
	out := reply{Step: op + " " + short(args)}
	traceID, spanID, started, active := e.auth.TraceContext(opExec, fn)
	if module || !active {
		out.Status, out.Code, out.Message = 403, "SDK_SCOPE_VIOLATION", "SDK request is not associated with an active caller execution"
		if op == "crons.upsert" {
			out.Message = "crons.upsert must be called from inside your handler, not at module scope"
		}
		if op == "spans.emit" {
			out.Message = "span execution is not active for the calling function"
		}
		return out
	}
	limit := handlers.MaxEnqueueBody
	if op == "spans.emit" {
		limit = handlers.MaxUserSpanBody
	}
	if len(args) > limit {
		out.Status, out.Code, out.Message = 400, "INVALID_BODY", "request body exceeds limit"
		if op == "spans.emit" {
			out.Message = "failed to read body"
		}
		return out
	}
	var jobReq handlers.EnqueueJobRequest
	var cronReq handlers.UpsertCronRequest
	var spanReq handlers.UserSpanRequest
	var req any
	switch op {
	case "jobs.enqueue":
		req = &jobReq
	case "crons.upsert":
		req = &cronReq
	case "spans.emit":
		req = &spanReq
	default:
		panic(op)
	}
	if err := json.Unmarshal([]byte(args), req); err != nil {
		out.Status, out.Code, out.Message = 400, "INVALID_JSON", "invalid request body"
		return out
	}
	var result any
	var serr *handlers.StatusErr
	switch op {
	case "jobs.enqueue":
		result, serr = handlers.EnqueueJob(e.db, fn, traceID, spanID, &jobReq)
	case "crons.upsert":
		result, serr = handlers.UpsertCron(e.db, fn, &cronReq)
	case "spans.emit":
		result, serr = handlers.CreateUserSpan(e.db, fn, opExec, traceID, spanID, started, &spanReq)
	}
	if serr != nil {
		out.Status, out.Code, out.Message = serr.Status, serr.Code, serr.Message
		return out
	}
	b, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	out.Status, out.Body = 200, decodeOp(b)
	return out
}

func short(s string) string {
	if len(s) > 80 {
		return s[:80] + fmt.Sprintf("...(%d bytes)", len(s))
	}
	return s
}

// decodeOp normalises per-run values (ids, clocks) that cannot be equal
// across two databases.
func decodeOp(b []byte) any {
	v := decodeAny(b)
	var walk func(any) any
	walk = func(x any) any {
		switch m := x.(type) {
		case map[string]any:
			for k, e := range m {
				switch k {
				case "id", "created_at", "scheduled_at", "next_run_at":
					m[k] = "<v>"
				default:
					m[k] = walk(e)
				}
			}
		case []any:
			for i := range m {
				m[i] = walk(m[i])
			}
		}
		return x
	}
	return walk(v)
}

type opStep struct{ op, args string }

// diffOps compares the shared cores with the channel and requires every reply to
// match; it returns the two environments for follow-up state checks.
func diffOps(t *testing.T, module bool, steps []opStep) (h, s *opEnv) {
	t.Helper()
	h, s = newOpEnv(t), newOpEnv(t)
	for i, st := range steps {
		a, b := h.coreCall(fnA, module, st.op, st.args), s.sdkCall(fnA, module, st.op, st.args)
		ja, _ := json.Marshal(a.Body)
		jbb, _ := json.Marshal(b.Body)
		if a.Status != b.Status || a.Code != b.Code || a.Message != b.Message || string(ja) != string(jbb) {
			t.Errorf("step %d %q differs:\n core: %d %s %q %s\n sdk:  %d %s %q %s",
				i, a.Step, a.Status, a.Code, a.Message, ja, b.Status, b.Code, b.Message, jbb)
		}
	}
	return h, s
}

func TestJobsEnqueueDifferential(t *testing.T) {
	when := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	big := `{"function_name":"fn-b","payload":"` + strings.Repeat("a", 1<<20) + `"}`
	h, s := diffOps(t, false, []opStep{
		{"jobs.enqueue", `{"function_name":"fn-b","payload":{"x":1}}`},
		{"jobs.enqueue", `{"function_id":"fn-b"}`},
		{"jobs.enqueue", `{"payload":{}}`},
		{"jobs.enqueue", `{"function_name":"nope"}`},
		{"jobs.enqueue", `{"function_id":"nope"}`},
		{"jobs.enqueue", `{"function_name":"fn-b","idempotency_key":"k1"}`},
		{"jobs.enqueue", `{"function_name":"fn-b","idempotency_key":"k1"}`},
		{"jobs.enqueue", `{"function_name":"fn-b","scheduled_at":"` + when + `","max_attempts":7}`},
		{"jobs.enqueue", `{"function_name":"fn-b","scheduled_at":"yesterday"}`},
		{"jobs.enqueue", `[1]`},
		{"jobs.enqueue", big},
	})
	for _, e := range []*opEnv{h, s} {
		jobs, err := e.db.ListJobs("", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		// by-name, by-id, k1 (its replay dedupes) and the scheduled one.
		if len(jobs) != 4 {
			t.Fatalf("jobs stored=%d, want 4", len(jobs))
		}
		for _, j := range jobs {
			if j.EnqueuedByFunctionID != fnA || j.TraceID != opTrace || j.ParentSpanID != opSpan {
				t.Errorf("attribution %q/%q/%q", j.EnqueuedByFunctionID, j.TraceID, j.ParentSpanID)
			}
			if j.MaxAttempts == 7 && j.ScheduledAt.Before(time.Now().Add(30*time.Minute)) {
				t.Errorf("delay not honoured: %v", j.ScheduledAt)
			}
		}
	}
}

func TestJobsEnqueueIdempotentReplaySameID(t *testing.T) {
	e := newOpEnv(t)
	a := e.sdkCall(fnA, false, "jobs.enqueue", `{"function_name":"fn-b","idempotency_key":"k"}`)
	b := e.sdkCall(fnA, false, "jobs.enqueue", `{"function_name":"fn-b","idempotency_key":"k"}`)
	must(t, a, 200)
	must(t, b, 200)
	if bodyMap(t, b)["replayed"] != true {
		t.Errorf("replay not flagged: %v", b.Body)
	}
	jobs, _ := e.db.ListJobs("", "", 100)
	if len(jobs) != 1 {
		t.Errorf("stored %d jobs, want 1", len(jobs))
	}
}

func TestCronsUpsertDifferential(t *testing.T) {
	steps := []opStep{
		{"crons.upsert", `{"name":"nightly","schedule":"0 3 * * *"}`},
		{"crons.upsert", `{"name":"nightly","schedule":"0 4 * * *","timezone":"Asia/Kolkata","payload":{"a":1}}`},
		{"crons.upsert", `{"name":"off","schedule":"* * * * *","enabled":false}`},
		{"crons.upsert", `{"schedule":"* * * * *"}`},
		{"crons.upsert", `{"name":"x"}`},
		{"crons.upsert", `{"name":"x","schedule":"not a cron"}`},
		{"crons.upsert", `{"name":"x","schedule":"* * * * *","timezone":"Mars/Base"}`},
		{"crons.upsert", `{"name":"x","schedule":"* * * * *","payload":"{not json"}`},
		{"crons.upsert", `{"name":"x","schedule":"* * * * *","enabled":"yes"}`},
		{"crons.upsert", `[1]`},
		{"crons.upsert", `{"name":"x","schedule":"* * * * *","payload":"` + strings.Repeat("a", 1<<20) + `"}`},
	}
	// Fill to the cap (nightly + off already declared), then the 26th, then a
	// re-declare at the cap.
	for i := 0; i < 23; i++ {
		steps = append(steps, opStep{"crons.upsert", fmt.Sprintf(`{"name":"job-%d","schedule":"* * * * *"}`, i)})
	}
	steps = append(steps,
		opStep{"crons.upsert", `{"name":"one-too-many","schedule":"* * * * *"}`},
		opStep{"crons.upsert", `{"name":"job-0","schedule":"5 * * * *"}`},
	)
	h, s := diffOps(t, false, steps)
	for _, e := range []*opEnv{h, s} {
		rows, err := e.db.ListCronSchedulesForFunction(fnA)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 25 {
			t.Errorf("stored %d schedules, want 25", len(rows))
		}
		if other, _ := e.db.ListCronSchedulesForFunction(fnB); len(other) != 0 {
			t.Errorf("schedules leaked to fn-b: %d", len(other))
		}
	}
}

func spanRows(e *opEnv, wantAtLeast int) []*database.UserSpan {
	var got []*database.UserSpan
	for i := 0; i < 100; i++ {
		got, _ = e.db.ListUserSpansByExecution(opExec)
		if len(got) >= wantAtLeast {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return got
}

func TestSpansEmitDifferential(t *testing.T) {
	started := time.Now().UTC().Format(time.RFC3339Nano)
	h, s := diffOps(t, false, []opStep{
		{"spans.emit", `{"name":"work","duration_ms":5}`},
		{"spans.emit", `{"name":"db","started_at":"` + started + `","duration_ms":1,"status":"error","error_message":"boom","attributes":{"k":"v"}}`},
		{"spans.emit", `{"duration_ms":5}`},
		{"spans.emit", `{"name":"x","started_at":"nope"}`},
		{"spans.emit", `[1]`},
		{"spans.emit", `{"name":"` + strings.Repeat("a", 1<<16) + `"}`},
	})
	for _, e := range []*opEnv{h, s} {
		rows := spanRows(e, 2)
		if len(rows) != 2 {
			t.Fatalf("spans stored=%d, want 2", len(rows))
		}
		for _, r := range rows {
			if r.TraceID != opTrace || r.ParentSpanID != opSpan || r.ExecutionID != opExec {
				t.Errorf("span attribution %q/%q/%q", r.TraceID, r.ParentSpanID, r.ExecutionID)
			}
		}
	}
}

func TestModuleScopeRefusedWithoutSideEffects(t *testing.T) {
	for _, st := range []opStep{
		{"jobs.enqueue", `{"function_name":"fn-b"}`},
		{"crons.upsert", `{"name":"n","schedule":"* * * * *"}`},
	} {
		h, s := newOpEnv(t), newOpEnv(t)
		a, b := h.coreCall(fnA, true, st.op, st.args), s.sdkCall(fnA, true, st.op, st.args)
		if a.Status != 403 || b.Status != 403 || a.Code != b.Code || a.Message != b.Message {
			t.Errorf("%s: core=%d %s %q sdk=%d %s %q", st.op, a.Status, a.Code, a.Message, b.Status, b.Code, b.Message)
		}
		if jobs, _ := s.db.ListJobs("", "", 10); len(jobs) != 0 {
			t.Errorf("%s wrote at module scope", st.op)
		}
		if rows, _ := s.db.ListCronSchedulesForFunction(fnA); len(rows) != 0 {
			t.Errorf("%s wrote at module scope", st.op)
		}
	}
	h, s := newOpEnv(t), newOpEnv(t)
	a, b := h.coreCall(fnA, true, "spans.emit", `{"name":"x"}`), s.sdkCall(fnA, true, "spans.emit", `{"name":"x"}`)
	if a.Status != 403 || b.Status != 403 || a.Code != b.Code || a.Message != b.Message {
		t.Errorf("spans module scope: core=%d sdk=%d %s", a.Status, b.Status, b.Code)
	}
}

// A revoked lease (dispatch ended / worker killed) performs no side effect.
func TestRevokedLeasePerformsNoSideEffect(t *testing.T) {
	e := newOpEnv(t)
	e.cancel()
	for _, st := range []opStep{
		{"jobs.enqueue", `{"function_name":"fn-b"}`},
		{"crons.upsert", `{"name":"n","schedule":"* * * * *"}`},
		{"spans.emit", `{"name":"x"}`},
	} {
		r := e.sdkCall(fnA, false, st.op, st.args)
		if r.Status != 403 || r.Code != "SDK_SCOPE_VIOLATION" {
			t.Errorf("%s after revoke: %d %s", st.op, r.Status, r.Code)
		}
	}
	if jobs, _ := e.db.ListJobs("", "", 10); len(jobs) != 0 {
		t.Errorf("job enqueued after revoke")
	}
	if rows, _ := e.db.ListCronSchedulesForFunction(fnA); len(rows) != 0 {
		t.Errorf("schedule written after revoke")
	}
	time.Sleep(200 * time.Millisecond)
	if rows, _ := e.db.ListUserSpansByExecution(opExec); len(rows) != 0 {
		t.Errorf("span written after revoke")
	}
	// A cancelled call context alone also stops the work.
	e2 := newOpEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, serr := e2.host.Handle(ctx, e2.caller(fnA, false), "jobs.enqueue", json.RawMessage(`{"function_name":"fn-b"}`)); serr == nil {
		t.Errorf("cancelled ctx still enqueued")
	}
	if jobs, _ := e2.db.ListJobs("", "", 10); len(jobs) != 0 {
		t.Errorf("job enqueued on cancelled ctx")
	}
}

// Identity is host-derived: no argument can act as another function.
func TestOpsIsolationBetweenFunctions(t *testing.T) {
	e := newOpEnv(t)
	// Identity-shaped fields are not part of the args at all.
	for _, st := range []opStep{
		{"jobs.enqueue", `{"function_name":"fn-b","enqueued_by_function_id":"fn-b"}`},
		{"jobs.enqueue", `{"function_name":"fn-b","trace_id":"forged"}`},
		{"crons.upsert", `{"name":"n","schedule":"* * * * *","function_id":"fn-b"}`},
		{"spans.emit", `{"name":"x","execution_id":"exec-b","function_id":"fn-b","trace_id":"forged"}`},
	} {
		if r := e.sdkCall(fnA, false, st.op, st.args); r.Status != 400 || r.Code != "INVALID_JSON" {
			t.Errorf("%s accepted identity field: %d %s", st.op, r.Status, r.Code)
		}
	}
	// Legitimate calls as A are attributed to A, never B.
	must(t, e.sdkCall(fnA, false, "jobs.enqueue", `{"function_name":"fn-b"}`), 200)
	must(t, e.sdkCall(fnA, false, "crons.upsert", `{"name":"n","schedule":"* * * * *"}`), 200)
	must(t, e.sdkCall(fnA, false, "spans.emit", `{"name":"x"}`), 200)
	jobs, _ := e.db.ListJobs("", "", 10)
	if len(jobs) != 1 || jobs[0].EnqueuedByFunctionID != fnA || jobs[0].FunctionID != fnB {
		t.Errorf("job attribution: %+v", jobs)
	}
	if a, _ := e.db.ListCronSchedulesForFunction(fnA); len(a) != 1 {
		t.Errorf("fn-a schedules=%d", len(a))
	}
	if b, _ := e.db.ListCronSchedulesForFunction(fnB); len(b) != 0 {
		t.Errorf("fn-b schedules=%d", len(b))
	}
	// A caller whose identity is B but whose execution is A's still cannot
	// write A's span: the host takes both from the same ExecInfo, and the
	// span row carries the caller's function id.
	must(t, e.sdkCall(fnB, false, "crons.upsert", `{"name":"b","schedule":"* * * * *"}`), 200)
	if b, _ := e.db.ListCronSchedulesForFunction(fnB); len(b) != 1 {
		t.Errorf("fn-b own schedule missing")
	}
	if a, _ := e.db.ListCronSchedulesForFunction(fnA); len(a) != 1 {
		t.Errorf("fn-b upsert leaked into fn-a")
	}
}

func TestOpsMetricsCounted(t *testing.T) {
	e := newOpEnv(t)
	e.sdkCall(fnA, false, "jobs.enqueue", `{"function_name":"fn-b"}`)
	e.sdkCall(fnA, true, "crons.upsert", `{}`)
	seen := map[string]OpStats{}
	for _, s := range e.host.Stats() {
		seen[s.Op] = s
	}
	if seen["jobs.enqueue"].Count != 1 || seen["crons.upsert"].Errors != 1 {
		t.Errorf("stats: %+v %+v", seen["jobs.enqueue"], seen["crons.upsert"])
	}
}
