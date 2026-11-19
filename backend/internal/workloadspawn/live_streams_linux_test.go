//go:build linux

package workloadspawn_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// liveStreamsSource adds real streaming modes to the calls fixture. The callee
// yields timed chunks through the runtime's own streaming protocol; the caller
// consumes them with the public SDK stream API and forwards each chunk to the
// public response. Errors are reduced to generic codes so hidden target names
// and messages never reach the response.
func liveStreamsSource(lang string) (string, string) {
	entry, source := liveCallsSource(lang)
	if lang == "node" {
		source = strings.Replace(source, "const {kv,invoke} = require('orva');", "const {kv,invoke,invokeStream} = require('orva');", 1)
		source = strings.Replace(source, "if(input.mode && input.mode!=='kv')",
			"if(input.mode==='stream-source') return streamSource(input,context);\n if(input.mode==='stream-call') return streamCall(input,context);\n if(input.mode && input.mode!=='kv')", 1)
		return entry, source + `
async function* streamSource(input,context){
  yield JSON.stringify({kind:'callee',context,marker:input.marker})+'\n';
  const n=Math.max(1,Math.min(Number(input.count)||5,200)), d=Math.max(0,Math.min(Number(input.delayMs)||50,1000));
  for(let i=1;i<=n;i++){ await new Promise(r=>setTimeout(r,d)); yield 'chunk-'+String(i).padStart(3,'0')+'\n'; }
}
async function* streamCall(input,context){
  let it=null;
  try{
    it=invokeStream(input.target,{mode:'stream-source',count:input.count,delayMs:input.delayMs,marker:'streams-workflow',depth:9999,trace:'author-not-authority'},{timeoutMs:30000})[Symbol.asyncIterator]();
    let r=await it.next();
    yield JSON.stringify({kind:'caller',context})+'\n';
    while(!r.done){ yield r.value; r=await it.next(); }
    yield 'end\n';
  }catch(e){ yield 'error:'+(e&&e.status===403?'IAM_DENIED':'CALL_ERROR')+'\n'; }
  finally{ if(it&&it.return) await it.return().catch(()=>{}); }
}`
	}
	source = strings.Replace(source, `    if input.get("mode") not in (None,"kv"):`,
		`    if input.get("mode")=="stream-source":
        return stream_source(input,context)
    if input.get("mode")=="stream-call":
        return stream_call(input,context)
    if input.get("mode") not in (None,"kv"):`, 1)
	source = strings.Replace(source, "from orva import invoke\n", "from orva import invoke, invoke_stream\n", 1)
	return entry, source + `
def stream_source(inp,context):
    import time as _t
    yield json.dumps({"kind":"callee","context":context,"marker":inp.get("marker")})+"\n"
    n=max(1,min(int(inp.get("count") or 5),200)); d=max(0,min(int(inp.get("delayMs") or 50),1000))/1000.0
    for i in range(1,n+1):
        _t.sleep(d)
        yield "chunk-%03d\n" % i

def stream_call(inp,context):
    gen=None
    try:
        gen=invoke_stream(inp["target"],{"mode":"stream-source","count":inp.get("count"),"delayMs":inp.get("delayMs"),"marker":"streams-workflow","depth":9999,"trace":"author-not-authority"},timeout_ms=30000)
        chunk=next(gen,None)
        yield json.dumps({"kind":"caller","context":context})+"\n"
        while chunk is not None:
            yield chunk
            chunk=next(gen,None)
        yield "end\n"
    except Exception as e:
        yield "error:"+("IAM_DENIED" if getattr(e,"status",0)==403 else "CALL_ERROR")+"\n"
    finally:
        if gen is not None:
            gen.close()
`
}

type streamOpLog struct{ NextOK, NextDenied, Close []int64 }

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

type streamLine struct {
	Text string `json:"text"`
	At   int64  `json:"at_ns"`
}

// scratchStreamWriter forwards and flushes every write unchanged while recording
// bounded line timestamps. It is fixture-only and never decides authorization.
type scratchStreamWriter struct {
	http.ResponseWriter
	lookup func(string) (sandbox.WorkloadIdentity, bool)
	mu     sync.Mutex
	status int
	buf    []byte
	lines  []streamLine
	bytes  int
}

func (w *scratchStreamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *scratchStreamWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *scratchStreamWriter) WriteHeader(code int) {
	w.mu.Lock()
	first := w.status == 0
	if first {
		w.status = code
	}
	w.mu.Unlock()
	if first {
		if id, ok := w.lookup(w.Header().Get("X-Orva-Execution-ID")); ok {
			w.Header().Set("X-Scratch-Worker", id.WorkerID)
			w.Header().Set("X-Scratch-Artifact", id.ArtifactRevision)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *scratchStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	started := w.status != 0
	w.mu.Unlock()
	if !started {
		w.WriteHeader(http.StatusOK)
	}
	now := time.Now().UnixNano()
	w.mu.Lock()
	w.bytes += len(p)
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		if len(w.lines) < 400 {
			text := string(w.buf[:i])
			if len(text) > 512 {
				text = text[:512]
			}
			w.lines = append(w.lines, streamLine{Text: text, At: now})
		}
		w.buf = w.buf[i+1:]
	}
	w.mu.Unlock()
	return w.ResponseWriter.Write(p)
}

type streamHooks struct {
	call         func(string, string, string, any, int) map[string]any
	keys, owners map[string]string
	events       func() []any
	targetWorker func(owner string) *sandbox.Worker
	db           *database.Database
}

type streamRun struct {
	mu     sync.Mutex
	Status int
	Header http.Header
	Lines  []streamLine
	Done   chan struct{}
	Err    error
	cancel context.CancelFunc
}

func (r *streamRun) lines() []streamLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]streamLine(nil), r.Lines...)
}
func (r *streamRun) chunkCount() int {
	n := 0
	for _, l := range r.lines() {
		if strings.HasPrefix(l.Text, "chunk-") {
			n++
		}
	}
	return n
}

func (h *streamHooks) start(t *testing.T, caller string, body map[string]any) *streamRun {
	t.Helper()
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	req, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:8443/fn/"+caller+"/stream", strings.NewReader(string(raw)))
	liveMust(t, err)
	req.Header.Set("X-Orva-API-Key", h.keys["admin"])
	run := &streamRun{Done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(run.Done)
		defer cancel()
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			run.Err = err
			return
		}
		defer resp.Body.Close()
		run.mu.Lock()
		run.Status, run.Header = resp.StatusCode, resp.Header
		run.mu.Unlock()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			run.mu.Lock()
			run.Lines = append(run.Lines, streamLine{Text: sc.Text(), At: time.Now().UnixNano()})
			run.mu.Unlock()
		}
		run.Err = sc.Err()
	}()
	return run
}

func (h *streamHooks) wait(t *testing.T, run *streamRun, within time.Duration) {
	t.Helper()
	select {
	case <-run.Done:
	case <-time.After(within):
		run.cancel()
		t.Fatalf("stream did not finish within %s; lines=%v", within, run.lines())
	}
}

func (h *streamHooks) waitChunks(t *testing.T, run *streamRun, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if run.chunkCount() >= n {
			return
		}
		select {
		case <-run.Done:
			t.Fatalf("stream ended before %d chunks: %v", n, run.lines())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %d chunks", n)
}

func lastText(lines []streamLine) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1].Text
}

func headContext(t *testing.T, lines []streamLine, kind string) map[string]any {
	t.Helper()
	for _, l := range lines {
		if strings.HasPrefix(l.Text, "{") {
			var v map[string]any
			if json.Unmarshal([]byte(l.Text), &v) == nil && v["kind"] == kind {
				c, _ := v["context"].(map[string]any)
				return c
			}
		}
	}
	return nil
}

// streamEvent waits for the fixture's after-response record of one execution.
func (h *streamHooks) streamEvent(t *testing.T, execution string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range h.events() {
			if m, ok := e.(map[string]any); ok && m["kind"] == "stream" && m["execution"] == execution {
				return m
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no stream evidence for %s", execution)
	return nil
}

func (h *streamHooks) ack(t *testing.T, owner string) int64 {
	t.Helper()
	var at int64
	for _, e := range h.events() {
		if m, ok := e.(map[string]any); ok && m["kind"] == "policy_ack" && m["owner"] == owner && m["status"] == 200 {
			if v, ok := m["acked_ns"].(int64); ok {
				at = v
			}
		}
	}
	if at == 0 {
		t.Fatalf("no acknowledged policy publication for %s", owner)
	}
	return at
}

func rowsOf(m map[string]any) []*database.Execution {
	rows, _ := m["children"].([]*database.Execution)
	return rows
}

func liveStreamsChecks(t *testing.T, h *streamHooks) {
	t.Helper()
	set := func(id string, allow, inbound []any) {
		p := "/api/v1/functions/" + id + "/permissions"
		s := h.call("GET", p, h.keys["admin"], nil, 200)
		host := s["host"].(map[string]any)
		h.call("PUT", p, h.keys["admin"], map[string]any{
			"expected": map[string]any{"revision": host["revision"], "current_artifact": host["currentArtifact"], "approved_artifact": host["approvedArtifact"]},
			"policy":   map[string]any{"schema_version": 1, "allow": allow, "inbound": inbound}}, 200)
	}
	empty := []any{}
	for _, pair := range [][2]string{{"node", "python"}, {"python", "node"}} {
		caller, callee := h.owners[pair[0]], h.owners[pair[1]]
		out := []any{map[string]any{"actions": []string{"functions.invoke"}, "context": "execution", "resource": map[string]any{"function": map[string]string{"id": callee}}}}
		in := []any{map[string]any{"caller_id": caller, "triggers": []string{"f2f"}}}
		request := func(count, delay int) map[string]any {
			return map[string]any{"mode": "stream-call", "target": "scratch-" + pair[1], "count": count, "delayMs": delay}
		}
		deniedRun := func(label string) {
			t.Helper()
			run := h.start(t, caller, request(5, 50))
			h.wait(t, run, 20*time.Second)
			lines := run.lines()
			if run.Err != nil || run.Status != 200 || lastText(lines) != "error:IAM_DENIED" || run.chunkCount() != 0 || headContext(t, lines, "callee") != nil {
				t.Fatalf("%s %s->%s: expected generic denial with no callee output, got status=%d err=%v lines=%v", label, pair[0], pair[1], run.Status, run.Err, lines)
			}
			for _, l := range lines {
				if strings.Contains(l.Text, "scratch-") || strings.Contains(strings.ToLower(l.Text), "not found") {
					t.Fatalf("%s leaked target metadata: %q", label, l.Text)
				}
			}
			ev := h.streamEvent(t, run.Header.Get("X-Orva-Execution-ID"))
			if kids := rowsOf(ev); len(kids) != 1 {
				t.Fatalf("%s: denied stream produced callee execution rows: %d", label, len(kids)-1)
			}
		}
		allowedRun := func(label string) *streamRun {
			t.Helper()
			run := h.start(t, caller, request(5, 50))
			h.wait(t, run, 30*time.Second)
			lines := run.lines()
			if run.Err != nil || run.Status != 200 || run.chunkCount() != 5 || lastText(lines) != "end" {
				t.Fatalf("%s %s->%s: expected five genuine chunks and end, got status=%d err=%v lines=%v", label, pair[0], pair[1], run.Status, run.Err, lines)
			}
			c, p := headContext(t, lines, "callee"), headContext(t, lines, "caller")
			if c == nil || p == nil || c["functionID"] != callee || c["depth"] != "1" || c["trigger"] != "f2f" || c["credentialAbsent"] != true ||
				c["trace"] != p["trace"] || c["trace"] == "" || c["trace"] == "author-not-authority" || c["execution"] == p["execution"] {
				t.Fatalf("%s: callee context did not come from host dispatch: %v %v", label, c, p)
			}
			return run
		}
		cut := func(label, revoked string, count, delay int) {
			t.Helper()
			run := h.start(t, caller, request(count, delay))
			h.waitChunks(t, run, 3)
			old := h.targetWorker(callee)
			set(revoked, empty, empty)
			h.wait(t, run, 8*time.Second)
			ackNS := h.ack(t, revoked)
			lines := run.lines()
			if run.Err != nil || lastText(lines) != "error:IAM_DENIED" || run.chunkCount() >= count {
				t.Fatalf("%s %s->%s: in-flight revoke did not cut the stream: err=%v chunks=%d last=%q", label, pair[0], pair[1], run.Err, run.chunkCount(), lastText(lines))
			}
			after, sawError := 0, false
			for _, l := range lines {
				if sawError {
					t.Fatalf("%s: line after the denial marker: %q", label, l.Text)
				}
				if l.Text == "error:IAM_DENIED" {
					sawError = true
				} else if strings.HasPrefix(l.Text, "chunk-") && l.At > ackNS {
					after++
				}
			}
			// Bytes already handed off before the publication can still reach the
			// client; they are admitted data, not a bypass. A bounded few only.
			if after > 2 {
				t.Fatalf("%s: %d chunks reached the client after the revocation was acknowledged", label, after)
			}
			ev := h.streamEvent(t, run.Header.Get("X-Orva-Execution-ID"))
			for _, a := range ev["next_ok_at"].([]int64) {
				if a > ackNS {
					t.Fatalf("%s: a fresh frame handoff started after acknowledged revocation and was admitted", label)
				}
			}
			var child *database.Execution
			for _, r := range rowsOf(ev) {
				if r.FunctionID == callee {
					child = r
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for child == nil && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
				kids, _ := h.db.ListByTraceID(rowsParentTrace(ev))
				for _, r := range kids {
					if r.FunctionID == callee {
						child = r
					}
				}
			}
			if child == nil || child.Status != "error" || child.Trigger != "f2f" || child.ParentFunctionID != caller ||
				!strings.Contains(strings.ToLower(child.ErrorMessage), "cancel") {
				t.Fatalf("%s: callee execution was not recorded as cancelled: %+v", label, child)
			}
			if old != nil {
				dead := false
				for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
					if s, ok := old.WorkloadState(); !ok || !s.Alive {
						dead = true
						break
					}
				}
				if !dead {
					t.Fatalf("%s: callee worker stayed alive after its lease was cancelled", label)
				}
			}
			t.Logf("%s %s->%s: cut after %d of %d chunks; %d already-admitted chunk(s) after acknowledgement; callee cancelled", label, pair[0], pair[1], run.chunkCount(), count, after)
		}

		set(caller, out, empty)
		set(callee, empty, empty)
		deniedRun("outbound-only")
		set(caller, empty, empty)
		set(callee, empty, in)
		deniedRun("inbound-only")
		set(caller, out, empty)
		allowedRun("both sides")
		cut("caller permission revoked", caller, 40, 300)
		set(caller, out, empty)
		allowedRun("restored")
		cut("callee acceptance revoked", callee, 40, 300)
		set(callee, empty, in)
		hidden := h.start(t, caller, map[string]any{"mode": "stream-call", "target": "not-visible-or-existing", "count": 3, "delayMs": 10})
		h.wait(t, hidden, 20*time.Second)
		if lastText(hidden.lines()) != "error:IAM_DENIED" || len(hidden.lines()) != 1 {
			t.Fatalf("hidden target did not deny generically: %v", hidden.lines())
		}
		set(caller, empty, empty)
		set(callee, empty, empty)
		t.Logf("PASS actual %s->%s streams: one-sided deny without callee output; both allow genuine chunks; caller revoke and callee acceptance revoke each cut an in-flight stream; hidden target generic", pair[0], pair[1])
	}
}

func rowsParentTrace(m map[string]any) string {
	for _, r := range rowsOf(m) {
		if r.Trigger == "http" {
			return r.TraceID
		}
	}
	return ""
}
