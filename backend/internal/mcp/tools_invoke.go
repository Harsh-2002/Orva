package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/Harsh-2002/Orva/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/trace"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var osStat = os.Stat

// ─── invoke_function ───────────────────────────────────────────────

type InvokeFunctionInput struct {
	FunctionID string            `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
	Method     string            `json:"method" jsonschema:"REQUIRED — HTTP method (uppercase). GET for read endpoints, POST for write/create, PUT/PATCH for updates, DELETE for removals. Set explicitly because a silent default would invoke a GET-shaped function with POST and trigger 404/405 the agent then misdiagnoses."`
	Path       string            `json:"path,omitempty" jsonschema:"sub-path passed to the handler as event.path, default /"`
	Headers    map[string]string `json:"headers,omitempty" jsonschema:"request headers (lowercased on the way in)"`
	Body       any               `json:"body,omitempty" jsonschema:"request body — pass an object to send JSON, or a string for raw"`
	TimeoutMS  int64             `json:"timeout_ms,omitempty" jsonschema:"override the function's configured timeout for this one call"`
}

type InvokeFunctionOutput struct {
	StatusCode  int               `json:"status_code"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	DurationMS  int64             `json:"duration_ms"`
	ColdStart   bool              `json:"cold_start"`
	ExecutionID string            `json:"execution_id"`
	Stderr      string            `json:"stderr,omitempty"`
	// OrvaHint surfaces a known-shape diagnostic when the handler failed
	// in a way the platform recognises. Currently set when the invocation
	// crashed with a network-shaped error AND the function's network_mode
	// is "none", which is the most common reason an orva-SDK-using
	// handler fails after deploy. Empty when no hint applies.
	OrvaHint string `json:"orva_hint,omitempty" jsonschema:"diagnostic hint when the handler failed in a known-shape way (e.g. network_mode=none blocking the orva SDK)"`
}

// ─── list_executions ───────────────────────────────────────────────

type ListExecutionsInput struct {
	FunctionID string `json:"function_id,omitempty" jsonschema:"filter to one function (id or name)"`
	Status     string `json:"status,omitempty" jsonschema:"success or error"`
	Since      string `json:"since,omitempty" jsonschema:"ISO8601 lower bound on started_at"`
	Until      string `json:"until,omitempty" jsonschema:"ISO8601 upper bound on started_at"`
	Search     string `json:"search,omitempty" jsonschema:"substring match against error_message"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}

type ExecutionView struct {
	ID           string     `json:"id"`
	FunctionID   string     `json:"function_id"`
	Status       string     `json:"status"`
	StatusCode   int        `json:"status_code,omitempty"`
	ColdStart    bool       `json:"cold_start"`
	DurationMS   int64      `json:"duration_ms,omitempty"`
	ResponseSize int        `json:"response_size,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

func toExecutionView(e *database.Execution) ExecutionView {
	if e == nil {
		return ExecutionView{}
	}
	v := ExecutionView{
		ID: e.ID, FunctionID: e.FunctionID, Status: e.Status,
		ColdStart: e.ColdStart, ErrorMessage: e.ErrorMessage,
		StartedAt: e.StartedAt, FinishedAt: e.FinishedAt,
	}
	if e.DurationMS != nil {
		v.DurationMS = *e.DurationMS
	}
	if e.StatusCode != nil {
		v.StatusCode = *e.StatusCode
	}
	if e.ResponseSize != nil {
		v.ResponseSize = *e.ResponseSize
	}
	return v
}

type ListExecutionsOutput struct {
	Executions []ExecutionView `json:"executions"`
	Total      int             `json:"total"`
	Limit      int             `json:"limit"`
	Offset     int             `json:"offset"`
}

// ─── get_execution / get_execution_logs ────────────────────────────

type GetExecutionInput struct {
	ExecutionID string `json:"execution_id"`
}

type GetExecutionLogsOutput struct {
	ExecutionID string `json:"execution_id"`
	Stderr      string `json:"stderr"`
}

// ─── delete_execution / bulk_delete_executions ─────────────────────

type DeleteExecutionInput struct {
	ExecutionID string `json:"execution_id"`
	Confirm     bool   `json:"confirm"`
}

type BulkDeleteExecutionsInput struct {
	IDs     []string `json:"ids" jsonschema:"max 1000 ids"`
	Confirm bool     `json:"confirm"`
}

type BulkDeleteOutput struct {
	Deleted int `json:"deleted"`
	Failed  int `json:"failed"`
}

// ─── test_function_with_fixture ────────────────────────────────────

// FixtureOverride is the optional shallow-merge override for
// test_function_with_fixture. Each field, when non-zero/non-nil, replaces
// the matching fixture field for this one call only — the saved fixture
// row is never mutated.
type FixtureOverride struct {
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"merged onto the fixture's headers; key collisions: override wins"`
	Body    string            `json:"body,omitempty"    jsonschema:"raw replacement body; if set, fully replaces the fixture's body"`
}

type TestFunctionWithFixtureInput struct {
	FunctionID  string          `json:"function_id" jsonschema:"function id (UUID) or name owning the fixture"`
	FixtureName string          `json:"fixture_name" jsonschema:"name of a previously-saved fixture for this function"`
	Override    FixtureOverride `json:"override,omitempty" jsonschema:"optional per-call overrides; shallow-merge onto the fixture (override wins)"`
	TimeoutMS   int64           `json:"timeout_ms,omitempty"`
}

// FixtureApplied is the audit trail returned alongside the invoke result
// so agents can confirm which knobs were swapped from the saved fixture.
type FixtureApplied struct {
	Name             string   `json:"name"`
	AppliedOverrides []string `json:"applied_overrides"`
}

type TestFunctionWithFixtureOutput struct {
	InvokeFunctionOutput
	Fixture FixtureApplied `json:"fixture"`
}

// ─── registration ──────────────────────────────────────────────────

func registerInvokeTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permInvoke, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name: "invoke_function",
				Description: "Call a function and return its response. `method` is REQUIRED — pick GET for read endpoints, POST for create/write, PUT/PATCH for updates, DELETE for removals (no silent default; an invocation that uses the wrong verb usually returns 404/405 which is hard to debug). Pass `body` as either an object (sent as JSON) or a string. Returns status_code, headers, body, plus execution_id you can pass to get_execution_logs if you want stderr. Bypasses the function's auth_mode (the agent's MCP bearer is already trusted). When the handler crashes with a network-shaped error (ENETUNREACH / fetch failed / OrvaUnavailableError) on a function with network_mode=none, the response includes an `orva_hint` telling you exactly what to fix.",
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrFalse(),
					OpenWorldHint:   ptrTrue(), // function may call external APIs
				},
			},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in InvokeFunctionInput) (*mcpsdk.CallToolResult, InvokeFunctionOutput, error) {
				return invokeFunction(ctx, deps, in)
			},
		)
	})

	gatedAdd(perms, permInvoke, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "test_function_with_fixture",
				Description: "Invoke a function using a previously-saved fixture as the request envelope. Applies optional shallow-merge overrides (override wins on key collision). Returns the same shape as invoke_function plus a `fixture` block listing which overrides were applied. Use list_fixtures to see what's saved.",
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrFalse(),
					OpenWorldHint:   ptrTrue(),
				},
			},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in TestFunctionWithFixtureInput) (*mcpsdk.CallToolResult, TestFunctionWithFixtureOutput, error) {
				return testFunctionWithFixture(ctx, deps, in)
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_executions",
				Description: "List recent invocations across all functions or filtered to one. Useful for an agent debugging why a function is failing — combine with get_execution_logs to see stderr.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListExecutionsInput) (*mcpsdk.CallToolResult, ListExecutionsOutput, error) {
				params := database.ListExecutionsParams{
					Status: in.Status, Since: in.Since, Until: in.Until,
					Search: in.Search, Limit: in.Limit, Offset: in.Offset,
				}
				if in.FunctionID != "" {
					fn, err := resolveFunction(deps, in.FunctionID)
					if err != nil {
						return nil, ListExecutionsOutput{}, err
					}
					params.FunctionID = fn.ID
				}
				if params.Limit <= 0 {
					params.Limit = 50
				}
				if params.Limit > 200 {
					params.Limit = 200
				}
				res, err := deps.DB.ListExecutions(params)
				if err != nil {
					return nil, ListExecutionsOutput{}, err
				}
				out := ListExecutionsOutput{Total: res.Total, Limit: params.Limit, Offset: params.Offset}
				for _, e := range res.Executions {
					out.Executions = append(out.Executions, toExecutionView(e))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_execution",
				Description: "Fetch one execution by id. Returns status, status_code, duration_ms, cold_start, and any error_message.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetExecutionInput) (*mcpsdk.CallToolResult, ExecutionView, error) {
				e, err := deps.DB.GetExecution(in.ExecutionID)
				if err != nil {
					return nil, ExecutionView{}, fmt.Errorf("execution not found: %s", in.ExecutionID)
				}
				return nil, toExecutionView(e), nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_execution_logs",
				Description: "Read the captured stderr from one execution. Stdout was already returned as the response body to whoever invoked. Returns empty string if the function logged nothing to stderr.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetExecutionInput) (*mcpsdk.CallToolResult, GetExecutionLogsOutput, error) {
				log, err := deps.DB.GetExecutionLog(in.ExecutionID)
				if err != nil {
					return nil, GetExecutionLogsOutput{ExecutionID: in.ExecutionID}, nil
				}
				return nil, GetExecutionLogsOutput{ExecutionID: log.ExecutionID, Stderr: log.Stderr}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_execution",
				Description: "Permanently delete one execution row and its captured stderr. Pass confirm=true.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteExecutionInput) (*mcpsdk.CallToolResult, DeletedOutput, error) {
				if !in.Confirm {
					return nil, DeletedOutput{}, errors.New("delete refused: pass confirm=true")
				}
				if err := deps.DB.DeleteExecution(in.ExecutionID); err != nil {
					return nil, DeletedOutput{}, err
				}
				return nil, DeletedOutput{DeletedID: in.ExecutionID}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "bulk_delete_executions",
				Description: "Delete multiple execution rows. Max 1000 ids per call. Pass confirm=true. Returns counts of deleted vs failed.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in BulkDeleteExecutionsInput) (*mcpsdk.CallToolResult, BulkDeleteOutput, error) {
				if !in.Confirm {
					return nil, BulkDeleteOutput{}, errors.New("delete refused: pass confirm=true")
				}
				if len(in.IDs) > 1000 {
					return nil, BulkDeleteOutput{}, errors.New("too many ids (max 1000 per call)")
				}
				out := BulkDeleteOutput{}
				for _, id := range in.IDs {
					if err := deps.DB.DeleteExecution(id); err != nil {
						out.Failed++
					} else {
						out.Deleted++
					}
				}
				return nil, out, nil
			},
		)
	})
}

// ─── invoke implementation ─────────────────────────────────────────

// invokeFunction runs a function via the proxy/sandbox path,
// bypassing per-function auth_mode/rate_limit (the MCP caller is
// already authenticated as a trusted operator). Captures the
// response in-process, persists the execution row, and returns the
// body + headers + status to the agent.
func invokeFunction(ctx context.Context, deps Deps, in InvokeFunctionInput) (*mcpsdk.CallToolResult, InvokeFunctionOutput, error) {
	fn, err := resolveFunction(deps, in.FunctionID)
	if err != nil {
		return nil, InvokeFunctionOutput{}, err
	}
	if fn.Status != "active" {
		// match the REST handler's "building + prior code" carve-out: if
		// the function is in the middle of a redeploy and a prior built
		// code dir exists, allow the call to use the old code.
		if !(fn.Status == "building" && hasPriorCode(deps.DataDir, fn.ID)) {
			return nil, InvokeFunctionOutput{}, fmt.Errorf("function status is %q, must be active", fn.Status)
		}
	}

	// Build env: function env_vars + decrypted secrets (secrets win).
	env := map[string]string{}
	for k, v := range fn.EnvVars {
		env[k] = v
	}
	if deps.Secrets != nil {
		if secretMap, err := deps.Secrets.GetForFunction(fn.ID); err == nil {
			for k, v := range secretMap {
				env[k] = v
			}
		}
	}

	timeoutMS := in.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = fn.TimeoutMS
	}
	if timeoutMS <= 0 {
		timeoutMS = 30000
	}

	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		return nil, InvokeFunctionOutput{}, errors.New(
			"method is required: pass an HTTP verb (GET, POST, PUT, " +
				"PATCH, DELETE) — defaulting silently to POST hides bugs " +
				"when the handler is GET-shaped and returns 404/405.",
		)
	}
	path := in.Path
	if path == "" {
		path = "/"
	}

	// Coerce body to a string. Accept any JSON-serialisable.
	var bodyStr string
	switch b := in.Body.(type) {
	case nil:
		bodyStr = ""
	case string:
		bodyStr = b
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return nil, InvokeFunctionOutput{}, fmt.Errorf("body marshal: %w", err)
		}
		bodyStr = string(buf)
	}

	// Construct the synthetic HTTP request the proxy expects. With
	// UUIDv7 IDs, the URL form and the DB form are identical — no
	// prefix stripping or re-adding.
	fullPath := "/fn/" + fn.ID
	if path != "" && path != "/" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		fullPath += path
	}

	req := httptest.NewRequest(method, fullPath, strings.NewReader(bodyStr))
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" && bodyStr != "" {
		// best effort: assume JSON since most agent payloads are JSON
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()

	// Generate execution id.
	execID := ids.New()

	// MCP-rooted invocation gets a fresh trace. The proxy reads trace
	// context off the request — we stamp it via WithContext so spans
	// land with the right metadata.
	traceID := trace.NewTraceID()
	spanID := trace.NewSpanID()
	tCtx := trace.WithTraceID(ctx, traceID)
	tCtx = trace.WithSpanID(tCtx, spanID)
	tCtx = trace.WithTrigger(tCtx, "mcp")

	// Track active requests for /system/metrics.json's active gauge.
	if deps.Metrics != nil {
		deps.Metrics.ActiveRequests.Add(1)
		defer deps.Metrics.ActiveRequests.Add(-1)
	}

	codeDir := deps.DataDir + "/functions/" + fn.ID + "/code"
	lang := sandbox.Language(fn.Runtime)
	seccompPolicy := sandbox.BuildSeccompPolicy("", nil, nil)

	start := time.Now()
	reqWithCtx := req.WithContext(tCtx)
	result, ferr := deps.Proxy.Forward(
		rec, reqWithCtx, codeDir, lang,
		fn.ID, execID, timeoutMS,
		int(fn.MemoryMB), fn.CPUs,
		env, seccompPolicy,
		"", true, start,
	)
	duration := time.Since(start)
	if deps.Metrics != nil {
		deps.Metrics.RecordDuration(duration)
		coldStart := result != nil && result.ColdStart
		deps.Metrics.RecordInvocation(coldStart)
	}

	// Build the output regardless of error — even a failed invoke is
	// useful info for the agent (it gets to see status_code, stderr).
	out := InvokeFunctionOutput{
		StatusCode:  rec.Code,
		Headers:     flattenHeaders(rec.Header()),
		Body:        rec.Body.String(),
		DurationMS:  duration.Milliseconds(),
		ExecutionID: execID,
	}
	if result != nil {
		out.ColdStart = result.ColdStart
		if len(result.Stderr) > 0 {
			out.Stderr = string(result.Stderr)
		}
	}

	// Persist execution row first, then stderr log — FK on execution_logs
	// requires the parent executions row to exist before the log is inserted.
	execStatus := "success"
	if ferr != nil || rec.Code >= 500 {
		execStatus = "error"
	}
	errMsg := ""
	if ferr != nil {
		errMsg = ferr.Error()
		if len(errMsg) > 1024 {
			errMsg = errMsg[:1024] + "…(truncated)"
		}
	}
	if deps.DB != nil {
		coldStart := result != nil && result.ColdStart
		deps.DB.AsyncInsertExecutionFinal(
			&database.Execution{
				ID: execID, FunctionID: fn.ID, Status: execStatus, ColdStart: coldStart,
				TraceID: traceID, SpanID: spanID, Trigger: "mcp",
				StartedAt: start,
			},
			duration.Milliseconds(), rec.Code, errMsg, len(out.Body),
		)
		if deps.Metrics != nil {
			deps.Metrics.Baselines.FinalizeExecution(deps.DB, execID, fn.ID, execStatus, coldStart, duration.Milliseconds())
		}
		if result != nil && len(result.Stderr) > 0 {
			deps.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
				ExecutionID: execID,
				Stderr:      string(result.Stderr),
			})
		}
	}

	// Annotate known-shape failures so the agent doesn't have to guess at
	// the cause. The most common foot-gun: a function created with
	// network_mode="none" tries to call orva.kv / orva.invoke / orva.jobs
	// or hit an external URL, and the handler crashes with ENETUNREACH /
	// ECONNREFUSED / "fetch failed" / OrvaUnavailableError. The agent has
	// no way to map that to "the platform is blocking my SDK call" — it
	// looks like a generic runtime crash. Stamp a hint on the output so
	// the next step is obvious.
	if hint := networkErrorHint(fn, errMsg, out.Stderr); hint != "" {
		out.OrvaHint = hint
	}

	if ferr != nil && rec.Code == 0 {
		// Forward never wrote a response — surface the error explicitly.
		return nil, out, fmt.Errorf("invoke failed: %s", errMsg)
	}
	return nil, out, nil
}

// networkErrorHint returns a non-empty hint when the function failed in a
// way that's almost certainly explained by network_mode="none" blocking
// the orva SDK or outbound HTTPS. The matching is intentionally broad
// (substring sniff on errMsg + stderr) — false positives are cheap (the
// hint is informational), false negatives are expensive (the agent goes
// down the wrong debugging path).
func networkErrorHint(fn *database.Function, errMsg, stderr string) string {
	if fn == nil || fn.NetworkMode != database.NetworkModeNone {
		return ""
	}
	combined := errMsg + " " + stderr
	if !(strings.Contains(combined, "ENETUNREACH") ||
		strings.Contains(combined, "ECONNREFUSED") ||
		strings.Contains(combined, "fetch failed") ||
		strings.Contains(combined, "OrvaUnavailableError")) {
		return ""
	}
	return "this function's network_mode is 'none', so the sandbox has " +
		"loopback only — orva.kv / orva.invoke / orva.jobs (which reach " +
		"orvad over the bridge network) and any external HTTPS will fail " +
		"with ENETUNREACH. Run update_function with network_mode='egress' " +
		"to enable them; the next invocation will be a cold start as the " +
		"warm pool drains."
}

// flattenHeaders collapses each header to a single value (the first).
// Agents almost never need multi-value semantics on the response side,
// and a flat map[string]string keeps the schema simple.
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			out[k] = vals[0]
		}
	}
	return out
}

// hasPriorCode mirrors the helper inside the REST invoke handler:
// returns true if the function has a built code directory we can
// keep serving while a new build is in flight.
func hasPriorCode(dataDir, fnID string) bool {
	codeDir := dataDir + "/functions/" + fnID + "/code"
	if _, err := osStat(codeDir); err == nil {
		return true
	}
	return false
}

// testFunctionWithFixture loads a fixture, applies the caller's overrides
// (shallow-merge: override wins), and routes through invokeFunction so
// the execution shows up in the same activity / executions tables as a
// regular invoke. The saved fixture row is never mutated.
func testFunctionWithFixture(ctx context.Context, deps Deps, in TestFunctionWithFixtureInput) (*mcpsdk.CallToolResult, TestFunctionWithFixtureOutput, error) {
	fn, err := resolveFunction(deps, in.FunctionID)
	if err != nil {
		return nil, TestFunctionWithFixtureOutput{}, err
	}
	name := strings.TrimSpace(in.FixtureName)
	if name == "" {
		return nil, TestFunctionWithFixtureOutput{}, errors.New("fixture_name is required")
	}
	fix, err := deps.DB.GetFixtureByName(fn.ID, name)
	if errors.Is(err, database.ErrFixtureNotFound) {
		return nil, TestFunctionWithFixtureOutput{}, fmt.Errorf("fixture not found: %s", name)
	}
	if err != nil {
		return nil, TestFunctionWithFixtureOutput{}, err
	}

	// Decode stored headers, then merge per-call overrides.
	headers := map[string]string{}
	if fix.HeadersJSON != "" {
		_ = json.Unmarshal([]byte(fix.HeadersJSON), &headers)
	}

	method := fix.Method
	path := fix.Path
	body := string(fix.Body)
	applied := []string{}
	if v := strings.TrimSpace(in.Override.Method); v != "" {
		method = strings.ToUpper(v)
		applied = append(applied, "method")
	}
	if v := strings.TrimSpace(in.Override.Path); v != "" {
		path = v
		applied = append(applied, "path")
	}
	if len(in.Override.Headers) > 0 {
		for k, v := range in.Override.Headers {
			headers[k] = v
		}
		applied = append(applied, "headers")
	}
	// Body override is "set or skip"; we treat the empty string as "no
	// override" so an agent that wants to clear the body can omit the
	// field. This matches how shallow-merge works for headers/path.
	if in.Override.Body != "" {
		body = in.Override.Body
		applied = append(applied, "body")
	}

	invokeIn := InvokeFunctionInput{
		FunctionID: fn.ID,
		Method:     method,
		Path:       path,
		Headers:    headers,
		Body:       body,
		TimeoutMS:  in.TimeoutMS,
	}
	_, invokeOut, ferr := invokeFunction(ctx, deps, invokeIn)
	out := TestFunctionWithFixtureOutput{
		InvokeFunctionOutput: invokeOut,
		Fixture: FixtureApplied{
			Name:             fix.Name,
			AppliedOverrides: applied,
		},
	}
	return nil, out, ferr
}
