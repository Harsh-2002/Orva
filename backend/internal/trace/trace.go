// Package trace provides minimal distributed-trace context plumbing for
// Orva: ID generation, context wiring, and W3C traceparent parsing.
//
// The model is intentionally lighter than full OpenTelemetry: each
// execution row IS a span, so we never instantiate Span objects in
// memory — we just pass IDs through context and headers.
//
//	trace_id      → 32 hex chars, prefix "tr_"  (one per top-level chain)
//	span_id       → 16 hex chars, prefix "sp_"  (one per execution)
//	parent_span_id→ span_id of the caller (empty on roots)
//	trigger       → http | cron | job | f2f | webhook | inbound | replay
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

// Context-key type. Distinct underlying type means our keys never collide
// with arbitrary string keys other code might stash in context.
type ctxKey int

const (
	traceIDKey ctxKey = iota
	spanIDKey
	parentSpanIDKey
	triggerKey
)

// NewTraceID returns a fresh top-level trace identifier. The "tr_" prefix
// distinguishes it from span IDs at a glance in logs.
func NewTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "tr_" + hex.EncodeToString(b[:])
}

// NewSpanID returns a fresh per-execution span identifier.
func NewSpanID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "sp_" + hex.EncodeToString(b[:])
}

// WithTraceID stamps the given trace ID onto the context.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceID returns the trace ID from context or "" if absent.
func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey).(string)
	return v
}

// WithSpanID stamps the current span ID onto the context.
func WithSpanID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, spanIDKey, id)
}

// SpanID returns the current span ID from context or "" if absent.
func SpanID(ctx context.Context) string {
	v, _ := ctx.Value(spanIDKey).(string)
	return v
}

// WithParentSpanID stamps the parent span ID onto the context.
func WithParentSpanID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, parentSpanIDKey, id)
}

// ParentSpanID returns the parent span ID from context or "" if absent.
func ParentSpanID(ctx context.Context) string {
	v, _ := ctx.Value(parentSpanIDKey).(string)
	return v
}

// WithTrigger stamps the trigger label onto the context.
func WithTrigger(ctx context.Context, t string) context.Context {
	return context.WithValue(ctx, triggerKey, t)
}

// Trigger returns the current trigger label from context or "" if absent.
func Trigger(ctx context.Context) string {
	v, _ := ctx.Value(triggerKey).(string)
	return v
}

// FromHTTPRequest extracts a trace context from request headers, honoring
// W3C traceparent (preferred) and Orva's own X-Orva-Trace-Id /
// X-Orva-Span-Id pair (used by the SDK for F2F + job-pickup paths).
// Returns (traceID, parentSpanID); both empty when no trace header exists,
// in which case the caller should generate a fresh trace ID.
//
// The "Trace-Id" return value is the *parent's* trace ID — the caller
// extends it with a fresh span ID before recording the span.
func FromHTTPRequest(r *http.Request) (traceID, parentSpanID string) {
	if v := r.Header.Get("Traceparent"); v != "" {
		if t, p, ok := parseTraceparent(v); ok {
			return "tr_" + t, "sp_" + p
		}
	}
	// Orva-native (no prefix collision with W3C).
	if v := r.Header.Get("X-Orva-Trace-Id"); v != "" {
		traceID = v
	}
	if v := r.Header.Get("X-Orva-Span-Id"); v != "" {
		parentSpanID = v
	}
	return traceID, parentSpanID
}

// parseTraceparent parses a W3C trace-context header of the form
// "00-<32hex>-<16hex>-<flags>". Returns the trace_id and parent_span_id
// (both as raw hex, no Orva prefix). All fields must be lowercase hex of
// the exact width the spec mandates; anything else returns ok=false so
// the caller falls back to native generation.
func parseTraceparent(v string) (traceID, parentSpanID string, ok bool) {
	parts := strings.Split(strings.TrimSpace(v), "-")
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] != "00" {
		return "", "", false
	}
	if !isHex(parts[1], 32) || !isHex(parts[2], 16) {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
