package respond

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Error writes a JSON error response with the standard envelope. Existing
// callers stay unchanged — backward-compatible 4-arg signature.
func Error(w http.ResponseWriter, status int, code, message, requestID string) {
	ErrorWithDetail(w, status, ErrorOpts{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	})
}

// ErrorOpts is the rich error envelope emitted by ErrorWithDetail. All
// fields except Code/Message are optional; zero-value fields are omitted
// from the wire response.
//
// `RetryAfterS` automatically becomes the `Retry-After` HTTP header so
// stock clients (curl, browsers) get the standard signal even if they
// don't parse the body.
type ErrorOpts struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	RequestID   string         `json:"request_id,omitempty"`
	Hint        string         `json:"hint,omitempty"`
	RetryAfterS int            `json:"retry_after_s,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// ErrorWithDetail emits the rich envelope. Use from the central error
// mapper; thin wrappers in the call site keep using respond.Error.
func ErrorWithDetail(w http.ResponseWriter, status int, opts ErrorOpts) {
	w.Header().Set("Content-Type", "application/json")
	if opts.RetryAfterS > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(opts.RetryAfterS))
	}
	w.WriteHeader(status)

	body := map[string]any{
		"code":    opts.Code,
		"message": opts.Message,
	}
	if opts.RequestID != "" {
		body["request_id"] = opts.RequestID
	}
	if opts.Hint != "" {
		body["hint"] = opts.Hint
	}
	if opts.RetryAfterS > 0 {
		body["retry_after_s"] = opts.RetryAfterS
	}
	if len(opts.Details) > 0 {
		body["details"] = opts.Details
	}
	json.NewEncoder(w).Encode(map[string]any{"error": body})
}
