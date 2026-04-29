package handlers

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/proxy"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// InvokeHandler handles function invocation requests.
type InvokeHandler struct {
	Registry       *registry.Registry
	Proxy          *proxy.Proxy
	DB             *database.Database
	Metrics        *metrics.Metrics
	Secrets        *secrets.Manager
	DataDir        string
	DefaultSeccomp string // Global default seccomp policy name

	// PublishEvent is fired after every invocation so the SSE event hub can
	// stream execution rows to live UI clients (Dashboard recent invocations
	// + InvocationsLog). Best-effort; non-blocking. Wired in server.New from
	// events.Hub.Publish.
	PublishEvent func(eventType string, data any)

	// rateLimiter is initialized lazily on first request so existing tests
	// that build the handler with zero-value fields keep working. Keyed by
	// (fn_id, client_ip).
	rateLimiterOnce sync.Once
	rateLimiter     *rateLimiter
}

// ServeHTTP is the hot path for function invocation. It accepts two path
// shapes:
//   - /fn/{id}/... — direct invoke (id is the short form without "fn_" prefix)
//   - any custom route previously registered via /api/v1/routes that maps
//     a user-chosen path (e.g. /webhooks/stripe) to a function_id
func (h *InvokeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	var fnID string
	// Prefix we should strip from the incoming request path before handing
	// it to the sandboxed function. For /fn/{id} this is handled inside
	// proxy.Forward; for custom routes we compute the strip here based on
	// whether the match was exact or a `/prefix/*` match.
	var stripPrefix string

	// Check custom routes first. MatchRoute tries exact match, then the
	// longest `/prefix/*` match.
	if route, matched, err := h.DB.MatchRoute(r.URL.Path); err == nil && route != nil {
		if route.Methods != "*" && route.Methods != "" {
			allowed := false
			for _, m := range strings.Split(route.Methods, ",") {
				if strings.EqualFold(strings.TrimSpace(m), r.Method) {
					allowed = true
					break
				}
			}
			if !allowed {
				respond.Error(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
					"method not allowed for this route", reqID)
				return
			}
		}
		fnID = route.FunctionID
		// For exact-match routes the function sees "/" as its path. For
		// prefix routes it sees the sub-path after the prefix so its
		// internal router (FastAPI/Express/etc.) can dispatch.
		if strings.HasSuffix(route.Path, "/*") {
			stripPrefix = matched // e.g. "/shortener/"
		} else {
			stripPrefix = matched // full path → function sees "/"
		}
	} else {
		// Fall back to /fn/{id} extraction.
		fnID = extractFnID(r.URL.Path)
	}

	if fnID == "" {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "no function for this path", reqID)
		return
	}

	// Registry lookup.
	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	// Status gate:
	// - "active": proceed normally.
	// - "building" AND a prior code directory exists: serve with the old
	//   code (zero-downtime redeploy). This is the Heroku/Vercel model.
	// - "building" with no prior code, or any other non-active status:
	//   respond 503 with Retry-After so clients can poll.
	if fn.Status != "active" {
		if fn.Status == "building" && hasPriorCode(h.DataDir, fn.ID) {
			// Fall through — the warm pool still has workers bound to the
			// old code dir. Fresh deploys won't hit this branch because
			// there's no prior code yet.
		} else {
			status := http.StatusConflict
			code := "NOT_ACTIVE"
			msg := fmt.Sprintf("function status is %q, must be active", fn.Status)
			if fn.Status == "building" {
				status = http.StatusServiceUnavailable
				code = "BUILDING"
				msg = "deployment in progress"
				w.Header().Set("Retry-After", "5")
			}
			respond.Error(w, status, code, msg, reqID)
			return
		}
	}

	// Per-function auth gate. auth_mode='none' (default) is a no-op and
	// keeps the public-by-default contract; 'platform_key' and 'signed' are
	// opt-in. authorizeInvoke writes the error response itself on failure.
	if code := h.authorizeInvoke(w, r, fn); code != "" {
		return
	}

	// Per-function rate limit. perMin == 0 short-circuits inside Allow.
	if fn.RateLimitPerMin > 0 {
		h.rateLimiterOnce.Do(func() { h.rateLimiter = newRateLimiter() })
		if !h.rateLimiter.Allow(fn.ID, clientIP(r), fn.RateLimitPerMin) {
			w.Header().Set("Retry-After", "60")
			respond.Error(w, http.StatusTooManyRequests, "RATE_LIMITED",
				fmt.Sprintf("rate limit exceeded (%d req/min per IP)", fn.RateLimitPerMin), reqID)
			return
		}
	}

	// Generate execution ID.
	execSuffix, err := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 12)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to generate execution ID", reqID)
		return
	}
	execID := "exec_" + execSuffix

	// Track active requests.
	h.Metrics.ActiveRequests.Add(1)
	defer h.Metrics.ActiveRequests.Add(-1)

	start := time.Now()

	// Set context timeout from function config.
	timeoutMS := fn.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 30000
	}

	// Resolve the code directory for this function.
	// The deploy handler stores code at DataDir/functions/<fn_id>/code/
	codeDir := h.DataDir + "/functions/" + fn.ID + "/code"

	// Determine language.
	lang := sandbox.Language(fn.Runtime)

	// Build seccomp policy for this function.
	seccompPolicy := sandbox.BuildSeccompPolicy(h.DefaultSeccomp, nil, nil)

	// Build env: function's env_vars plus decrypted secrets. Secrets win on
	// key collision (so an operator can override a public env var).
	env := map[string]string{}
	for k, v := range fn.EnvVars {
		env[k] = v
	}
	if h.Secrets != nil {
		if secretMap, err := h.Secrets.GetForFunction(fn.ID); err == nil {
			for k, v := range secretMap {
				env[k] = v
			}
		}
	}

	// Forward request through nsjail sandbox.
	result, err := h.Proxy.Forward(
		w, r, codeDir, lang,
		fnID, execID, timeoutMS,
		int(fn.MemoryMB), fn.CPUs,
		env,
		seccompPolicy,
		stripPrefix,
		true, start,
	)

	duration := time.Since(start)
	h.Metrics.RecordDuration(duration)
	if result != nil {
		h.Metrics.RecordInvocation(result.ColdStart)
	} else {
		h.Metrics.RecordInvocation(true)
	}

	if err != nil {
		errMsg := err.Error()
		if len(errMsg) > 1024 {
			errMsg = errMsg[:1024] + "…(truncated)"
		}
		statusCode := 0
		if result != nil {
			statusCode = result.StatusCode
		}
		coldStart := result != nil && result.ColdStart
		h.DB.AsyncInsertExecutionFinal(
			&database.Execution{ID: execID, FunctionID: fn.ID, Status: "error", ColdStart: coldStart},
			duration.Milliseconds(), statusCode, errMsg, 0,
		)
		if result != nil && len(result.Stderr) > 0 {
			h.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
				ExecutionID: execID,
				Stderr:      string(result.Stderr),
			})
		}
		h.publishExecution(execID, fn, "error", statusCode, duration.Milliseconds(), 0, coldStart)

		// Map the failure to an HTTP status via the central error taxonomy.
		// proxy.Forward only writes to w on success — so on error we always
		// emit our own envelope.
		if result == nil || !result.Wrote {
			status, opts := invokeError(err, fn, reqID)
			// Override message with the truncated err string for ops parity.
			if opts.Message == "" || opts.Code == "SANDBOX_ERROR" {
				opts.Message = errMsg
			}
			respond.ErrorWithDetail(w, status, opts)
		}
		return
	}

	execStatus := "success"
	if result.StatusCode >= 500 {
		execStatus = "error"
	}
	h.DB.AsyncInsertExecutionFinal(
		&database.Execution{ID: execID, FunctionID: fn.ID, Status: execStatus, ColdStart: result.ColdStart},
		duration.Milliseconds(), result.StatusCode, "", result.ResponseSize,
	)
	// Persist stderr after the execution row so the FK constraint is satisfied.
	if len(result.Stderr) > 0 {
		h.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
			ExecutionID: execID,
			Stderr:      string(result.Stderr),
		})
	}
	h.publishExecution(execID, fn, execStatus, result.StatusCode, duration.Milliseconds(), result.ResponseSize, result.ColdStart)
}

// publishExecution fires an `event: execution` to the SSE hub so the live
// UI panels (Dashboard recent invocations, InvocationsLog) prepend the
// new row without polling. The payload mirrors the Execution shape the
// list endpoint already returns.
func (h *InvokeHandler) publishExecution(id string, fn *database.Function, status string, statusCode int, durationMS int64, responseSize int, coldStart bool) {
	if h.PublishEvent == nil {
		return
	}
	now := time.Now().UTC()
	h.PublishEvent("execution", map[string]any{
		"id":            id,
		"function_id":   fn.ID,
		"function_name": fn.Name,
		"status":        status,
		"status_code":   statusCode,
		"duration_ms":   durationMS,
		"response_size": responseSize,
		"cold_start":    coldStart,
		"started_at":    now.Format(time.RFC3339Nano),
		"finished_at":   now.Format(time.RFC3339Nano),
	})
}

// hasPriorCode returns true if the function has a built code directory from
// a previous successful deploy. Used during redeploys so live traffic keeps
// hitting the old code until the new build succeeds.
func hasPriorCode(dataDir, fnID string) bool {
	if dataDir == "" {
		return false
	}
	st, err := os.Stat(dataDir + "/functions/" + fnID + "/code")
	return err == nil && st.IsDir()
}

// extractFnID pulls the function ID from paths like /fn/{id} or /fn/{id}/sub/path.
// The URL uses the short form without the "fn_" prefix; this function adds it back.
func extractFnID(path string) string {
	const prefix = "/fn/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	var shortID string
	if idx := strings.Index(remainder, "/"); idx >= 0 {
		shortID = remainder[:idx]
	} else {
		shortID = remainder
	}
	if shortID == "" {
		return ""
	}
	return "fn_" + shortID
}
