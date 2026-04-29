package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/sandbox"
)

// Proxy executes function code via warm pool workers and writes the response.
type Proxy struct {
	// Sandbox is the legacy host-wide concurrency limiter. Retained for API
	// compatibility with server.go; the real ceiling now lives inside the
	// pool manager (which also owns a reference to this limiter).
	Sandbox *sandbox.Limiter

	// Pool is the warm worker manager. When non-nil, Forward uses warm
	// workers; when nil (tests), falls back to the legacy one-shot path.
	Pool *pool.Manager

	Config ProxyConfig
}

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

// response is the JSON structure the adapter writes to stdout.
type response struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
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
	respJSON, stderr, err := acq.Worker.Dispatch(ctx, reqJSON)
	// Feed the per-fn EWMA so the autoscaler can compute Little's-Law floor.
	p.Pool.RecordLatency(fnID, time.Since(dispatchStart))
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

	var resp response
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		reqErr = err
		return result, fmt.Errorf("adapter returned invalid response: %w", err)
	}

	coldStartStr := "false"
	if acq.ColdStart {
		coldStartStr = "true"
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("X-Orva-Execution-ID", execID)
	w.Header().Set("X-Orva-Cold-Start", coldStartStr)
	w.Header().Set("X-Orva-Duration-MS", strconv.FormatInt(time.Since(startTime).Milliseconds(), 10))

	sc := resp.StatusCode
	if sc == 0 {
		sc = 200
	}
	w.WriteHeader(sc)
	n, _ := w.Write([]byte(resp.Body))

	result.StatusCode = sc
	result.ResponseSize = n
	result.Wrote = true
	return result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}
