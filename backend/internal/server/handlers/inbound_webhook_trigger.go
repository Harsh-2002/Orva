// Package handlers — inbound webhook trigger (v0.4 C2a).
//
// The trigger lives at POST /webhook/{id} — outside /api/v1 so external
// services (GitHub, Stripe, Slack, your own backend) can hit it without
// an Orva API key. Authentication is the HMAC signature on the request
// body itself; the inbound_webhooks row carries the secret + format.
//
// On success we synthesize the same event envelope the public /fn/
// handler builds and return whatever the function responded — so an
// inbound trigger feels indistinguishable from a public invocation
// from the user-code perspective.
package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/internal/trace"
)

// maxInboundBody caps the request body the trigger will read. nsjail
// stdin is small; we don't want a 100MB POST hanging the worker.
const maxInboundBody = 6 * 1024 * 1024 // 6 MiB

// InboundTriggerHandler serves POST /webhook/{id}. It deliberately does
// NOT bypass the bodySizeMiddleware (it lives at /webhook/, not /api/,
// so the middleware applies — but we read the raw body via io.ReadAll
// so signature verification sees exactly what the upstream sent).
type InboundTriggerHandler struct {
	DB       *database.Database
	Registry *registry.Registry
	Pool     *pool.Manager
	Metrics  *metrics.Metrics

	// PublishEvent fires an `event: execution` so the live UI sees
	// inbound-triggered runs in the same feed as HTTP invokes. Wired in
	// server.New from events.Hub.Publish; nil-safe.
	PublishEvent func(eventType string, data any)
}

// ServeHTTP routes /webhook/{id} → function. The path is fixed:
// /webhook/iwh_xxxxxxxxxxxx with no sub-paths.
func (h *InboundTriggerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		respond.Error(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
			"inbound webhook accepts POST only", reqID)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/webhook/")
	if id == "" || strings.Contains(id, "/") {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "inbound webhook not found", reqID)
		return
	}

	hook, err := h.DB.GetInboundWebhook(id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "inbound webhook not found", reqID)
		return
	}
	if !hook.Active {
		respond.Error(w, http.StatusGone, "INACTIVE", "inbound webhook is disabled", reqID)
		return
	}

	// Read the raw body up to the cap. Signature verification needs the
	// EXACT bytes the upstream sent; do not let json.Decode rewrite or
	// json-canonicalize the bytes here.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInboundBody+1))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	if int64(len(body)) > maxInboundBody {
		respond.Error(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
			"inbound webhook body exceeds cap", reqID)
		return
	}

	// Verify signature. On mismatch we record an audit row and return
	// 401 — never log the secret value, never leak it via error message.
	if err := verifyInboundSignature(r, body, hook); err != nil {
		summary := "inbound webhook signature mismatch: " + err.Error()
		h.DB.InsertActivity(database.ActivityRow{
			TS:         time.Now().UnixMilli(),
			Source:     "webhook",
			ActorType:  "anon",
			ActorID:    hook.ID,
			ActorLabel: hook.Name,
			Method:     "POST",
			Path:       r.URL.Path,
			Status:     http.StatusUnauthorized,
			Summary:    summary,
			RequestID:  reqID,
		})
		respond.Error(w, http.StatusUnauthorized, "SIGNATURE_INVALID",
			"signature verification failed", reqID)
		return
	}

	// Resolve the target function. The FK CASCADE on inbound_webhooks
	// makes a "hook exists, function gone" race vanishingly rare, but
	// handle it gracefully if the registry doesn't yet know about a
	// freshly-created function.
	fn, err := h.Registry.Get(hook.FunctionID)
	if err != nil {
		respond.Error(w, http.StatusServiceUnavailable, "FUNCTION_UNAVAILABLE",
			"target function not available", reqID)
		return
	}
	if fn.Status != "active" {
		respond.Error(w, http.StatusServiceUnavailable, "NOT_ACTIVE",
			"target function is not active", reqID)
		return
	}

	timeout := time.Duration(fn.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	acq, err := h.Pool.Acquire(ctx, fn.ID)
	if err != nil {
		respond.Error(w, http.StatusServiceUnavailable, "POOL_ERROR",
			"pool acquire failed", reqID)
		return
	}
	var reqErr error
	defer func() { h.Pool.Release(fn.ID, acq.Worker, reqErr) }()

	// Generate execution id up-front so the activity + execution rows
	// stay correlated. Format mirrors invoke / cron / job.
	execSuffix, _ := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 12)
	execID := "exec_" + execSuffix
	startedAt := time.Now()

	// Inbound webhook is always a root span. The middleware already
	// generated a trace_id for the incoming HTTP request — we reuse it
	// so external callers correlating on X-Trace-Id see the same id.
	traceID := trace.TraceID(r.Context())
	if traceID == "" {
		traceID = trace.NewTraceID()
	}
	spanID := trace.NewSpanID()

	// Build the event envelope. We forward the original headers so user
	// code (e.g. a GitHub handler that wants X-GitHub-Event) can branch
	// on them, plus stamp our own x-orva-* tags so a function can tell
	// it was triggered by an inbound webhook vs a regular HTTP call.
	headers := flattenHeaders(r.Header)
	headers["x-orva-trigger"] = "inbound_webhook"
	headers["x-orva-inbound-webhook-id"] = hook.ID
	headers["x-orva-execution-id"] = execID
	headers["x-orva-function-id"] = fn.ID
	headers["x-orva-trace-id"] = traceID
	headers["x-orva-span-id"] = spanID

	event := map[string]any{
		"method":  "POST",
		"path":    "/",
		"headers": headers,
		"body":    string(body),
	}
	eventJSON, _ := json.Marshal(event)

	respJSON, stderr, err := acq.Worker.Dispatch(ctx, eventJSON)
	durationMS := time.Since(startedAt).Milliseconds()
	if err != nil {
		reqErr = err
		errMsg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "function timed out"
		}
		// Record an execution row tagged with status=error so the
		// dashboard's invocations list shows the failure.
		h.DB.AsyncInsertExecutionFinal(
			&database.Execution{
				ID: execID, FunctionID: fn.ID, Status: "error",
				TraceID: traceID, SpanID: spanID, Trigger: "inbound",
				StartedAt: startedAt,
			},
			durationMS, 0, errMsg, 0,
		)
		if h.Metrics != nil {
			h.Metrics.Baselines.FinalizeExecution(h.DB, execID, fn.ID, "error", false, durationMS)
		}
		if len(stderr) > 0 {
			h.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
				ExecutionID: execID, Stderr: string(stderr),
			})
		}
		h.publish(execID, fn, "error", 0, durationMS)
		respond.Error(w, http.StatusBadGateway, "INVOKE_FAILED", errMsg, reqID)
		return
	}

	// Parse the worker response so we can mirror its statusCode + body
	// back to the upstream. The frame shape is the same one InvokeHandler
	// reads via the proxy.
	var fnResp struct {
		StatusCode int               `json:"statusCode"`
		Headers    map[string]string `json:"headers"`
		Body       string            `json:"body"`
	}
	_ = json.Unmarshal(respJSON, &fnResp)
	statusCode := fnResp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	execStatus := "success"
	if statusCode >= 500 {
		execStatus = "error"
	}
	h.DB.AsyncInsertExecutionFinal(
		&database.Execution{
			ID: execID, FunctionID: fn.ID, Status: execStatus,
			TraceID: traceID, SpanID: spanID, Trigger: "inbound",
			StartedAt: startedAt,
		},
		durationMS, statusCode, "", len(fnResp.Body),
	)
	if h.Metrics != nil {
		h.Metrics.Baselines.FinalizeExecution(h.DB, execID, fn.ID, execStatus, false, durationMS)
	}
	if len(stderr) > 0 {
		h.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
			ExecutionID: execID, Stderr: string(stderr),
		})
	}
	h.publish(execID, fn, execStatus, statusCode, durationMS)

	// Pass response back. We only forward Content-Type from the user's
	// headers map (keeping the contract narrow and predictable); the
	// rest are the platform's own.
	if ct := fnResp.Headers["content-type"]; ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Orva-Execution-ID", execID)
	w.WriteHeader(statusCode)
	_, _ = io.WriteString(w, fnResp.Body)
}

func (h *InboundTriggerHandler) publish(execID string, fn *database.Function, status string, statusCode int, durationMS int64) {
	if h.PublishEvent == nil {
		return
	}
	now := time.Now().UTC()
	h.PublishEvent("execution", map[string]any{
		"id":            execID,
		"function_id":   fn.ID,
		"function_name": fn.Name,
		"status":        status,
		"status_code":   statusCode,
		"duration_ms":   durationMS,
		"started_at":    now.Format(time.RFC3339Nano),
		"finished_at":   now.Format(time.RFC3339Nano),
		"trigger":       "inbound_webhook",
	})
}

// flattenHeaders converts http.Header (multimap) into the simple map the
// adapter contract expects. Multi-value headers are joined with ", " —
// matches RFC 9110 §5.2.
func flattenHeaders(in http.Header) map[string]string {
	out := make(map[string]string, len(in))
	for k, vs := range in {
		out[strings.ToLower(k)] = strings.Join(vs, ", ")
	}
	return out
}

// verifyInboundSignature dispatches to the correct verifier per the
// inbound_webhook row's signature_format. Returns nil on success, a
// short reason on failure (NOT including the secret).
func verifyInboundSignature(r *http.Request, body []byte, hook *database.InboundWebhook) error {
	switch hook.SignatureFormat {
	case "hmac_sha256_hex":
		return verifyHMACHex(r.Header.Get(hook.SignatureHeader), body, hook.Secret)
	case "hmac_sha256_base64":
		return verifyHMACBase64(r.Header.Get(hook.SignatureHeader), body, hook.Secret)
	case "github":
		return verifyGitHub(r.Header.Get(hook.SignatureHeader), body, hook.Secret)
	case "stripe":
		return verifyStripe(r.Header.Get(hook.SignatureHeader), body, hook.Secret)
	case "slack":
		return verifySlack(
			r.Header.Get(hook.SignatureHeader),
			r.Header.Get("X-Slack-Request-Timestamp"),
			body, hook.Secret,
		)
	default:
		return errors.New("unknown signature_format")
	}
}

func verifyHMACHex(provided string, body []byte, secret string) error {
	provided = strings.TrimSpace(provided)
	if provided == "" {
		return errors.New("missing signature header")
	}
	expected := computeHMACHex(secret, body)
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return errors.New("hex digest mismatch")
	}
	return nil
}

func verifyHMACBase64(provided string, body []byte, secret string) error {
	provided = strings.TrimSpace(provided)
	if provided == "" {
		return errors.New("missing signature header")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return errors.New("base64 digest mismatch")
	}
	return nil
}

// verifyGitHub expects the value "sha256=<hex>" in the configured
// header (default X-Hub-Signature-256). GitHub computes
// HMAC-SHA256(secret, raw_body).
func verifyGitHub(provided string, body []byte, secret string) error {
	provided = strings.TrimSpace(provided)
	if provided == "" {
		return errors.New("missing signature header")
	}
	if !strings.HasPrefix(provided, "sha256=") {
		return errors.New("expected sha256=<hex>")
	}
	digest := strings.TrimPrefix(provided, "sha256=")
	expected := computeHMACHex(secret, body)
	if !hmac.Equal([]byte(digest), []byte(expected)) {
		return errors.New("github digest mismatch")
	}
	return nil
}

// verifyStripe parses Stripe-Signature ("t=<ts>,v1=<hex>[,v1=<hex>...]")
// and confirms HMAC-SHA256(secret, "<ts>.<body>") matches one of the v1
// digests. Replay window: ±5 minutes.
func verifyStripe(provided string, body []byte, secret string) error {
	provided = strings.TrimSpace(provided)
	if provided == "" {
		return errors.New("missing signature header")
	}
	var ts string
	var v1s []string
	for _, part := range strings.Split(provided, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch strings.TrimSpace(kv[0]) {
		case "t":
			ts = strings.TrimSpace(kv[1])
		case "v1":
			v1s = append(v1s, strings.TrimSpace(kv[1]))
		}
	}
	if ts == "" || len(v1s) == 0 {
		return errors.New("malformed Stripe-Signature")
	}
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("invalid timestamp")
	}
	if !withinReplayWindow(tsInt) {
		return errors.New("timestamp outside replay window")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	for _, v := range v1s {
		if hmac.Equal([]byte(v), []byte(expected)) {
			return nil
		}
	}
	return errors.New("stripe v1 mismatch")
}

// verifySlack matches Slack's "v0=<hex>" signing convention. The signed
// string is "v0:<ts>:<body>"; ts comes from X-Slack-Request-Timestamp.
// 5-minute replay window per Slack's docs.
func verifySlack(provided, ts string, body []byte, secret string) error {
	provided = strings.TrimSpace(provided)
	if provided == "" {
		return errors.New("missing signature header")
	}
	if ts == "" {
		return errors.New("missing X-Slack-Request-Timestamp")
	}
	tsInt, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return errors.New("invalid timestamp")
	}
	if !withinReplayWindow(tsInt) {
		return errors.New("timestamp outside replay window")
	}
	if !strings.HasPrefix(provided, "v0=") {
		return errors.New("expected v0=<hex>")
	}
	digest := strings.TrimPrefix(provided, "v0=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:"))
	mac.Write([]byte(ts))
	mac.Write([]byte(":"))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(digest), []byte(expected)) {
		return errors.New("slack v0 mismatch")
	}
	return nil
}

// withinReplayWindow returns true when ts (unix seconds) is within
// 5 minutes of now. Both directions, so a slightly-fast upstream clock
// doesn't fail open immediately.
func withinReplayWindow(ts int64) bool {
	now := time.Now().Unix()
	delta := now - ts
	if delta < 0 {
		delta = -delta
	}
	return delta <= 5*60
}

func computeHMACHex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
