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

	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
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

func loggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		duration := time.Since(start)

		if quietPaths[r.URL.Path] && rec.status < 400 {
			return
		}

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", duration.Milliseconds(),
			"request_id", RequestID(r.Context()),
		)
	})
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
