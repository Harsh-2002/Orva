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

	gonanoid "github.com/matoous/go-nanoid/v2"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/sandbox"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var osStat = os.Stat

// ─── invoke_function ───────────────────────────────────────────────

type InvokeFunctionInput struct {
	FunctionID string            `json:"function_id" jsonschema:"function id (fn_...) or name"`
	Method     string            `json:"method,omitempty" jsonschema:"HTTP method, default POST"`
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

// ─── registration ──────────────────────────────────────────────────

func registerInvokeTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permInvoke, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "invoke_function",
				Description: "Call a function and return its response. Pass `body` as either an object (sent as JSON) or a string. Returns status_code, headers, body, plus execution_id you can pass to get_execution_logs if you want stderr. Bypasses the function's auth_mode (the agent's MCP bearer is already trusted).",
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
		method = "POST"
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

	// Construct the synthetic HTTP request the proxy expects. We point
	// it at /api/v1/invoke/<id><path> so Proxy.Forward's path-strip
	// logic matches the same shape it does for real REST callers.
	fullPath := "/api/v1/invoke/" + fn.ID
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
	execSuffix, _ := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 12)
	execID := "exec_" + execSuffix

	// Track active requests for /system/metrics.json's active gauge.
	if deps.Metrics != nil {
		deps.Metrics.ActiveRequests.Add(1)
		defer deps.Metrics.ActiveRequests.Add(-1)
	}

	codeDir := deps.DataDir + "/functions/" + fn.ID + "/code"
	lang := sandbox.Language(fn.Runtime)
	seccompPolicy := sandbox.BuildSeccompPolicy("", nil, nil)

	start := time.Now()
	reqWithCtx := req.WithContext(ctx)
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
			&database.Execution{ID: execID, FunctionID: fn.ID, Status: execStatus, ColdStart: coldStart},
			duration.Milliseconds(), rec.Code, errMsg, len(out.Body),
		)
		if result != nil && len(result.Stderr) > 0 {
			deps.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
				ExecutionID: execID,
				Stderr:      string(result.Stderr),
			})
		}
	}

	if ferr != nil && rec.Code == 0 {
		// Forward never wrote a response — surface the error explicitly.
		return nil, out, fmt.Errorf("invoke failed: %s", errMsg)
	}
	return nil, out, nil
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
