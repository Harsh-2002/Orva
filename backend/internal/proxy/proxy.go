package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/sandbox"
)

// redactHeaders names the HTTP headers whose values must not be persisted
// in the captured-request row that powers the dashboard's Replay button
// (v0.4 A3). Lookup is case-insensitive — the keys here are lower-case
// and callers MUST normalise the inbound header name before checking.
//
// Anything that smells like an authentication credential lives here:
// platform-issued bearer tokens, browser cookies, the operator-issued
// Orva API key, the per-process internal token used by the SDK, and
// HTTP/1.1's proxy auth. If a future header carries credentials, add
// it to this map — there is no allow-list fallback.
var redactHeaders = map[string]bool{
	"authorization":         true,
	"cookie":                true,
	"x-orva-api-key":        true,
	"x-orva-internal-token": true,
	"proxy-authorization":   true,
}

const redactedValue = "[REDACTED]"

// captureCache holds the cached value of replay_capture_enabled +
// replay_capture_max_bytes. The proxy reads system_config exactly once at
// package level and refreshes it on a long timer, trading a small window
// of staleness (≤ captureRefreshEvery) for zero per-invoke DB hits on the
// hot path. An operator who flips the toggle from the Settings page sees
// the change apply within one refresh cycle.
type captureCache struct {
	enabled  atomic.Bool
	maxBytes atomic.Int64
	loaded   atomic.Bool
}

var capCache captureCache

const captureRefreshEvery = 30 * time.Second

// LoadCaptureConfig seeds the cached replay-capture settings from the
// database and starts a background refresher. Idempotent — safe to call
// from server.New regardless of whether tests already wired a different
// proxy. Tests that don't call this fall through to "disabled" since the
// loaded flag stays false.
func LoadCaptureConfig(db *database.Database) {
	if db == nil {
		return
	}
	refresh := func() {
		enabled := db.GetSystemConfigInt("replay_capture_enabled", 1) == 1
		maxBytes := int64(db.GetSystemConfigInt("replay_capture_max_bytes", 1<<20))
		capCache.enabled.Store(enabled)
		capCache.maxBytes.Store(maxBytes)
		capCache.loaded.Store(true)
	}
	refresh()
	go func() {
		t := time.NewTicker(captureRefreshEvery)
		defer t.Stop()
		for range t.C {
			refresh()
		}
	}()
}

// captureSettings returns the cached toggle + cap. Returns (false, 0)
// when LoadCaptureConfig was never called (tests, unit benchmarks);
// callers fall back to "no capture".
func captureSettings() (bool, int64) {
	if !capCache.loaded.Load() {
		return false, 0
	}
	return capCache.enabled.Load(), capCache.maxBytes.Load()
}

// Proxy executes function code via warm pool workers and writes the response.
//
// Capture vs. streaming (v0.4 A3 + C1 interaction): the request capture
// machinery only persists the *inbound* request body for the dashboard's
// Replay button. It deliberately does not buffer the response — streaming
// handlers can emit gigabytes of chunks over a long-lived connection, and
// turning that into a SQLite blob would defeat the streaming UX. Replay
// re-invokes the function fresh anyway, so capture-of-request is enough
// to reconstruct any execution. This keeps streaming + capture mutually
// compatible by construction.
type Proxy struct {
	// Sandbox is the legacy host-wide concurrency limiter. Retained for API
	// compatibility with server.go; the real ceiling now lives inside the
	// pool manager (which also owns a reference to this limiter).
	Sandbox *sandbox.Limiter

	// Pool is the warm worker manager. When non-nil, Forward uses warm
	// workers; when nil (tests), falls back to the legacy one-shot path.
	Pool *pool.Manager

	// DB is used to persist the captured request envelope for replay
	// (v0.4 A3). Optional: when nil, capture is skipped silently. Tests
	// without a DB wired up keep working unchanged.
	DB *database.Database

	Config ProxyConfig
}

// streamMaxFallback is the wall-clock cap for a streaming response when
// the database lookup for stream_max_seconds is unavailable. Matches the
// migrations.go default so behaviour is consistent with operators who
// haven't customised the row.
const streamMaxFallback = 300 * time.Second

// ProxyConfig holds sandbox paths needed for execution.
type ProxyConfig struct {
	NsjailBin string
	RootfsDir string
}

// New creates a new Proxy.
func New() *Proxy {
	return &Proxy{}
}

// request is the JSON structure piped to the adapter's stdin.
type request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Result is returned from Forward so the caller can record stderr
// regardless of whether the adapter succeeded or the sandbox itself failed.
type Result struct {
	StatusCode   int
	ResponseSize int
	Stdout       []byte // kept for API compat; pool path leaves this empty
	Stderr       []byte
	TimedOut     bool
	Wrote        bool // whether we already wrote an HTTP response to w
	ColdStart    bool // true iff the Acquire spawned a fresh worker
}

// Forward serializes the HTTP request, acquires a warm worker from the
// pool, dispatches the request frame, and writes the response back on
// success. On failure it returns an error and does NOT write to w — the
// caller is responsible for emitting a JSON error envelope.
func (p *Proxy) Forward(
	w http.ResponseWriter,
	r *http.Request,
	codeDir string,
	language sandbox.Language,
	fnID, execID string,
	timeoutMS int64,
	memoryMB int,
	cpus float64,
	env map[string]string,
	seccompPolicy string,
	stripPrefix string, // strip this from r.URL.Path before passing to the function
	coldStart bool, // ignored; populated from pool Acquire result
	startTime time.Time,
) (*Result, error) {
	_ = codeDir
	_ = cpus
	_ = seccompPolicy
	_ = coldStart

	// Serialize the HTTP request into JSON for the adapter.
	body, _ := io.ReadAll(r.Body)

	// Compute the path the function sees.
	path := r.URL.Path
	if stripPrefix != "" && strings.HasPrefix(path, stripPrefix) {
		path = strings.TrimPrefix(path, stripPrefix)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	} else {
		// Direct /fn/{short_id} invocation. Strip the /fn/<short_id> prefix
		// so the function sees the sub-path (or "/" for the root).
		shortID := strings.TrimPrefix(fnID, "fn_")
		prefix := "/fn/" + shortID
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			if path == "" {
				path = "/"
			}
		}
	}

	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if !strings.EqualFold(k, "X-Orva-API-Key") {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	headers["x-orva-function-id"] = fnID
	headers["x-orva-execution-id"] = execID
	headers["x-orva-timeout-ms"] = strconv.FormatInt(timeoutMS, 10)
	// v0.4 C1: streaming feature flag. Operator-tunable via system_config;
	// when off the adapters treat generators as buffered single-frame
	// responses (back-compat fallback). Default on.
	streamingOn := 1
	streamKeepaliveS := 15
	if p.DB != nil {
		streamingOn = p.DB.GetSystemConfigInt("streaming_enabled", 1)
		streamKeepaliveS = p.DB.GetSystemConfigInt("stream_keepalive_seconds", 15)
	}
	headers["x-orva-streaming-enabled"] = strconv.Itoa(streamingOn)
	headers["x-orva-stream-keepalive-seconds"] = strconv.Itoa(streamKeepaliveS)

	// v0.4 A3: capture the inbound request for the dashboard's Replay
	// button. We piggy-back on the body bytes already in memory and on
	// the cached toggle so the hot path pays at most a small JSON marshal
	// + a non-blocking channel send.
	//
	// Cache-staleness tradeoff: replay_capture_enabled is read from
	// system_config every captureRefreshEvery (30s). Operators flipping
	// the toggle from the Settings page see it apply within one cycle —
	// vs. a SELECT-per-invoke that would cost ~50µs at p99. The cap is
	// likewise cached so the operator can shrink the maximum body size
	// without redeploying.
	if p.DB != nil {
		if enabled, maxBytes := captureSettings(); enabled {
			p.captureRequest(execID, r.Method, path, r.Header, body, maxBytes)
		}
	}

	reqJSON, _ := json.Marshal(request{
		Method:  r.Method,
		Path:    path,
		Headers: headers,
		Body:    string(body),
	})

	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	if env == nil {
		env = map[string]string{}
	}
	env["ORVA_EXECUTION_ID"] = execID
	env["ORVA_TIMEOUT_MS"] = strconv.FormatInt(timeoutMS, 10)
	env["ORVA_MEMORY_MB"] = strconv.Itoa(memoryMB)

	if p.Pool == nil {
		// Tests and tooling without a pool wired up: fail fast with a clear
		// message. The serve path always wires the pool.
		return &Result{}, fmt.Errorf("pool manager not configured")
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	acq, err := p.Pool.Acquire(ctx, fnID)
	if err != nil {
		return &Result{}, fmt.Errorf("pool acquire: %w", err)
	}
	// reqErr is the error we pass back to Release — determines whether the
	// worker gets returned to the pool or killed.
	var reqErr error
	defer func() { p.Pool.Release(fnID, acq.Worker, reqErr) }()

	dispatchStart := time.Now()
	dres, err := acq.Worker.DispatchEx(ctx, reqJSON)
	// Feed the per-fn EWMA so the autoscaler can compute Little's-Law floor.
	// (Streaming responses inflate this number — the dispatch "duration"
	// includes the entire stream wall-clock. The autoscaler treats this as
	// signal regardless; see pool.go's note near recordAcquire.)
	p.Pool.RecordLatency(fnID, time.Since(dispatchStart))
	var stderr []byte
	if dres != nil {
		stderr = dres.Stderr()
	}
	result := &Result{Stderr: stderr, ColdStart: acq.ColdStart}
	if err != nil {
		reqErr = err
		if ctx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
			// Wrap so the central error mapper can detect this via
			// errors.Is and emit 504 TIMEOUT instead of 503 SANDBOX_ERROR.
			return result, fmt.Errorf("function timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
		return result, fmt.Errorf("dispatch: %w", err)
	}

	coldStartStr := "false"
	if acq.ColdStart {
		coldStartStr = "true"
	}
	for k, v := range dres.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("X-Orva-Execution-ID", execID)
	w.Header().Set("X-Orva-Cold-Start", coldStartStr)

	sc := dres.StatusCode
	if sc == 0 {
		sc = 200
	}

	if !dres.Streaming {
		// Historical single-write path — preserves byte-for-byte parity
		// with pre-C1 behaviour for non-streaming handlers. The duration
		// header is the full wall-clock from request entry to body emit,
		// which on a buffered handler equals total response time.
		w.Header().Set("X-Orva-Duration-MS", strconv.FormatInt(time.Since(startTime).Milliseconds(), 10))
		w.WriteHeader(sc)
		n, _ := w.Write([]byte(dres.Body))
		result.StatusCode = sc
		result.ResponseSize = n
		result.Wrote = true
		return result, nil
	}

	// ── Streaming path (v0.4 C1) ─────────────────────────────────────
	//
	// Wire shape: adapter sent {"type":"response_start", ...}; we now loop
	// reading {"type":"chunk", ...} frames until a terminal
	// {"type":"response_end"} arrives. Each chunk is written + flushed so
	// the HTTP client sees TTFB = time-to-first-yield.
	//
	// Backpressure: the adapter blocks on its stdout write when this side
	// stops reading (kernel pipe buffer fills). The kernel pipe buffer
	// gives us ~64KiB of headroom before the handler's yield blocks —
	// good enough that a slow consumer slows the handler instead of OOM.
	//
	// HTTP/2 caveat: Flusher is not guaranteed on every ResponseWriter.
	// Standard library serves HTTP/1.1 keep-alive connections through a
	// chunked-transfer writer that DOES implement http.Flusher; HTTP/2
	// streams behave similarly via http2.responseWriter. If for some
	// reason the wrapped writer doesn't implement Flusher (custom
	// middleware, unusual TLS termination), we still write — the data
	// just gets buffered until the stream closes. We log nothing here
	// because the path runs per-request.

	// v0.4 polish: echo the streaming-config knobs as RESPONSE headers so
	// operators inspecting the wire (curl -v, load balancers, tcpdump) can
	// tell at a glance whether a given response was delivered as a stream
	// and what keepalive cadence was in effect. We reuse the values already
	// computed above for the request-side hint to the adapter.
	w.Header().Set("x-orva-streaming-enabled", strconv.Itoa(streamingOn))
	w.Header().Set("x-orva-stream-keepalive-seconds", strconv.Itoa(streamKeepaliveS))

	// v0.4 polish: rename Duration→Ttfb on streaming responses.
	//
	// On a streaming response, response headers are flushed at the moment
	// the FIRST chunk arrives — before the stream is finished. So a
	// "duration" header written here can only ever measure time-to-first-
	// byte, not total wall-clock. Naming it X-Orva-Duration-Ms is
	// misleading: a 30s stream would advertise "duration: 4ms".
	//
	// Picked Option B: rename the header to X-Orva-Ttfb-Ms so the wire
	// label matches what's measured. Total streaming duration is recorded
	// on the executions row by Forward's caller (server/invoke.go) and is
	// available via the executions REST/UI surfaces. Non-streaming
	// responses keep X-Orva-Duration-Ms unchanged for back-compat.
	w.Header().Set("X-Orva-Ttfb-Ms", strconv.FormatInt(time.Since(startTime).Milliseconds(), 10))

	flusher, _ := w.(http.Flusher)
	w.WriteHeader(sc)
	if flusher != nil {
		flusher.Flush() // push headers immediately so TTFB is measurable
	}

	// Hard cap — operator-tunable via system_config.stream_max_seconds.
	// This is a wall-clock fence: even if the handler keeps yielding, we
	// close the connection and let the pool reap the worker. Without this
	// a runaway generator could hold a worker for the entire lease.
	streamCap := streamMaxFallback
	if p.DB != nil {
		if secs := p.DB.GetSystemConfigInt("stream_max_seconds", 300); secs > 0 {
			streamCap = time.Duration(secs) * time.Second
		}
	}
	streamCtx, streamCancel := context.WithTimeout(r.Context(), streamCap)
	defer streamCancel()

	totalBytes := 0
	for {
		kind, data, err := dres.NextFrame(streamCtx)
		if err != nil {
			reqErr = err
			result.StatusCode = sc
			result.ResponseSize = totalBytes
			// Headers + status already flew. Nothing useful to write
			// back — return the error so the caller can record the
			// execution row as failed but DO NOT try to emit a JSON
			// envelope (Wrote=true tells invoke.go we're done).
			result.Wrote = true
			result.Stderr = dres.Stderr()
			return result, fmt.Errorf("stream: %w", err)
		}
		if kind == "end" {
			break
		}
		// kind == "chunk"
		if len(data) == 0 {
			// Heartbeat — nothing to push to the wire. Flushing without
			// data is a no-op for HTTP/1.1 chunked, but we still flush to
			// be safe against intermediate buffering.
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		n, werr := w.Write(data)
		totalBytes += n
		if werr != nil {
			// Client disconnected. Cancel the parent ctx so the worker's
			// next stdin/stdout op unblocks and the pool reaps it. We
			// return the disconnect as the request error so Release
			// kills the worker (mid-stream is unrecoverable).
			reqErr = werr
			streamCancel()
			result.StatusCode = sc
			result.ResponseSize = totalBytes
			result.Wrote = true
			result.Stderr = dres.Stderr()
			return result, fmt.Errorf("stream write: %w", werr)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	result.StatusCode = sc
	result.ResponseSize = totalBytes
	result.Wrote = true
	result.Stderr = dres.Stderr()
	return result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}

// captureRequest builds the captured-request envelope for the dashboard's
// Replay button (v0.4 A3) and queues it via the async writer. Sensitive
// header values (see redactHeaders) are replaced with "[REDACTED]" before
// the JSON serialise — the on-disk row never contains a credential.
//
// Bodies larger than maxBytes are cut to maxBytes with truncated=1 so
// the replay handler can refuse them (replay would be inaccurate).
//
// The function never returns an error — capture is best-effort and any
// failure must not affect the in-flight invocation.
func (p *Proxy) captureRequest(execID, method, path string, hdr http.Header, body []byte, maxBytes int64) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1MiB safety floor
	}

	redacted := make(map[string]string, len(hdr))
	for k, v := range hdr {
		if len(v) == 0 {
			continue
		}
		lower := strings.ToLower(k)
		if redactHeaders[lower] {
			redacted[lower] = redactedValue
			continue
		}
		redacted[lower] = v[0]
	}
	headersJSON, err := json.Marshal(redacted)
	if err != nil {
		// JSON of a string-string map can only fail on memory pressure;
		// give up rather than corrupt the row.
		return
	}

	truncated := false
	captured := body
	if int64(len(body)) > maxBytes {
		captured = body[:maxBytes]
		truncated = true
	}

	// Copy the slice — body's backing array is owned by the request and
	// we're handing this off to a different goroutine (the async writer).
	bodyCopy := make([]byte, len(captured))
	copy(bodyCopy, captured)

	p.DB.AsyncInsertExecutionRequest(&database.ExecutionRequest{
		ExecutionID: execID,
		Method:      method,
		Path:        path,
		HeadersJSON: string(headersJSON),
		Body:        bodyCopy,
		Truncated:   truncated,
		CapturedAt:  time.Now().UnixMilli(),
	})
}
