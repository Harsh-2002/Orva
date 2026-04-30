package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/events"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	actorKey     contextKey = "orva_actor"
)

func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// Actor identifies the caller of an inbound request. authMiddleware
// stashes it on the request context after a successful auth lookup so
// loggerMiddleware (and any other downstream consumer) can read who
// made the call without re-running auth.
type Actor struct {
	Source string // "web" | "api" | "mcp" | "sdk" | "cron" | "internal"
	Type   string // "session" | "api_key" | "internal_token" | "anon"
	ID     string // ak_abc... | user id | calling fn name | ""
	Label  string // human label, e.g. "admin" or "ci-deployer"
}

// WithActor returns a new context carrying the actor for downstream
// readers (notably loggerMiddleware → activity log).
func WithActor(ctx context.Context, a *Actor) context.Context {
	if a == nil {
		return ctx
	}
	return context.WithValue(ctx, actorKey, a)
}

// ActorFromContext returns the actor stashed by authMiddleware, or a
// best-effort anonymous fallback if none was set.
func ActorFromContext(ctx context.Context) *Actor {
	if a, ok := ctx.Value(actorKey).(*Actor); ok && a != nil {
		return a
	}
	return &Actor{Source: "api", Type: "anon"}
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = generateRequestID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Chatty endpoints polled by the UI every few seconds. We skip INFO logging
// on 2xx responses for these, but still log 4xx/5xx so real problems surface.
var quietPaths = map[string]bool{
	"/api/v1/system/health":  true,
	"/api/v1/system/metrics": true,
	"/api/v1/auth/status":    true,
	"/api/v1/auth/me":        true,
}

func loggerMiddleware(db *database.Database, hub *events.Hub, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		duration := time.Since(start)
		durationMS := duration.Milliseconds()

		// Quiet paths: 2xx responses on chatty UI pollers stay silent. We
		// still log 4xx/5xx so real problems surface, AND we still emit
		// activity events for those errors so the operator sees them in
		// the live feed.
		quiet := quietPaths[r.URL.Path] && rec.status < 400

		if !quiet {
			slog.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", durationMS,
				"request_id", RequestID(r.Context()),
			)
		}

		// Activity emission. Skips:
		//   - the SSE endpoint itself (subscribers would generate events
		//     for themselves and recurse)
		//   - assets / web shell / health pollers when they returned 2xx
		//   - the activity list endpoint (avoid feedback when the page
		//     refreshes its history)
		if shouldSkipActivity(r.URL.Path, rec.status) {
			return
		}

		actor := ActorFromContext(r.Context())
		row := database.ActivityRow{
			TS:         time.Now().UnixMilli(),
			Source:     actor.Source,
			ActorType:  actor.Type,
			ActorID:    actor.ID,
			ActorLabel: actor.Label,
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     rec.status,
			DurationMS: durationMS,
			Summary:    summaryFor(r.Method, r.URL.Path, rec.status),
			RequestID:  RequestID(r.Context()),
		}
		if db != nil {
			db.InsertActivity(row)
		}
		if hub != nil {
			hub.Publish(events.TypeActivity, row)
		}
	})
}

// shouldSkipActivity is the filter for paths that should NEVER emit an
// activity row. The feed should show "things the operator did" — clicks
// the dashboard makes auto-loading itself, asset fetches, and chatty
// pollers add noise without insight, so they're suppressed here.
func shouldSkipActivity(path string, status int) bool {
	// Self-loop: the SSE endpoint subscribes to its own emissions.
	if path == "/api/v1/events" {
		return true
	}
	// Activity history fetches — avoid the page polling itself.
	if strings.HasPrefix(path, "/api/v1/activity") {
		return true
	}
	// SPA shell + static assets. The browser fetches lots of these on
	// boot; they're not API activity.
	if path == "/" || path == "/web" || path == "/favicon.svg" || path == "/manifest.json" || path == "/robots.txt" {
		return true
	}
	if strings.HasPrefix(path, "/web/") || strings.HasPrefix(path, "/assets/") {
		return true
	}
	// Quiet-path 2xx: matches the slog suppression so the visible log
	// and the activity feed agree.
	if status < 400 && quietPaths[path] {
		return true
	}
	// Chatty UI pollers regardless of suffix. The .json variants are
	// the dashboard's metrics tile auto-refresh; the suffix-less paths
	// the auth-status pings.
	if status < 400 && (strings.HasPrefix(path, "/api/v1/system/metrics") ||
		strings.HasPrefix(path, "/api/v1/system/health") ||
		strings.HasPrefix(path, "/api/v1/auth/")) {
		return true
	}
	return false
}

// summaryFor builds the one-line "what happened" string. Recognises a
// handful of common write shapes; falls back to "<METHOD> <path>".
func summaryFor(method, path string, status int) string {
	// /api/v1/functions[/{id}[/...]]
	if strings.HasPrefix(path, "/api/v1/functions") {
		switch {
		case method == http.MethodPost && path == "/api/v1/functions":
			return "create function"
		case method == http.MethodPost && strings.HasSuffix(path, "/deployments"):
			return "deploy " + extractFnID(path)
		case method == http.MethodDelete:
			return "delete " + extractFnID(path)
		case method == http.MethodPut:
			return "update " + extractFnID(path)
		}
	}
	// /fn/<name>  → invocation through the public proxy
	if strings.HasPrefix(path, "/fn/") {
		fn := strings.TrimPrefix(path, "/fn/")
		if i := strings.Index(fn, "/"); i >= 0 {
			fn = fn[:i]
		}
		if status >= 400 {
			return "invoke " + fn + " (err)"
		}
		return "invoke " + fn
	}
	// /api/v1/keys
	if strings.HasPrefix(path, "/api/v1/keys") {
		switch method {
		case http.MethodPost:
			return "mint api key"
		case http.MethodDelete:
			return "delete api key"
		}
	}
	// /api/v1/webhooks
	if strings.HasPrefix(path, "/api/v1/webhooks") {
		switch method {
		case http.MethodPost:
			return "create webhook"
		case http.MethodDelete:
			return "delete webhook"
		}
	}
	return method + " " + path
}

// extractFnID pulls the function id segment from /api/v1/functions/{id}/...
func extractFnID(path string) string {
	const prefix = "/api/v1/functions/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func generateRequestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// bodySizeMiddleware caps request bodies at maxBytes. When the cap is
// breached we return a structured PAYLOAD_TOO_LARGE envelope (RFC 7231
// 413) instead of stdlib's plaintext "request body too large" so clients
// get a consistent error shape across the API.
func bodySizeMiddleware(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cheap up-front check: if the client sent a Content-Length we
		// trust and it already exceeds the cap, fail before the body is
		// streamed at all.
		if r.ContentLength > maxBytes {
			respond.ErrorWithDetail(w, http.StatusRequestEntityTooLarge, respond.ErrorOpts{
				Code:      "PAYLOAD_TOO_LARGE",
				Message:   "request body exceeds the configured cap",
				RequestID: r.Header.Get("X-Request-ID"),
				Hint:      "raise cfg.Server.MaxBodyBytes or split the upload into chunks",
				Details:   map[string]any{"max_bytes": maxBytes, "got_bytes": r.ContentLength},
			})
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware sets CORS headers and handles preflight OPTIONS requests.
func corsMiddleware(origins []string, next http.Handler) http.Handler {
	allowOrigin := "*"
	if len(origins) > 0 && origins[0] != "*" {
		allowOrigin = strings.Join(origins, ", ")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-Orva-API-Key")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
