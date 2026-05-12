// Package ids is the canonical identifier generator for Orva storage IDs.
//
// All non-cryptographic identifiers — function IDs, deployment IDs,
// execution IDs, OAuth client/token storage IDs, etc. — come from
// this package. They are UUIDv7 (RFC 9562 §5.7), canonical 36-char
// dashed form. UUIDv7's leading 48 bits are unix-millis-since-epoch,
// so generated IDs are time-sortable: ORDER BY id ≈ ORDER BY created_at,
// and B-tree indexes append at the right edge instead of taking a
// random-write hit on every insert.
//
// Do NOT use this package for:
//
//   - Bearer tokens, refresh tokens, OAuth authorization codes,
//     OAuth client secrets, API key plaintexts. UUIDv7 leaks ~48 bits
//     of timestamp; an attacker who learns the issue time narrows
//     brute-force search dramatically. Those values stay 256-bit
//     crypto/rand. See backend/internal/oauth/crypto.go for the
//     cryptographic generators.
//
//   - W3C trace and span IDs. Those are externally constrained to
//     32-hex / 16-hex by the traceparent header spec; UUIDv7 breaks
//     interop. See backend/internal/trace/trace.go.
//
//   - users.id and other INTEGER PKs. UUID buys nothing for them.
package ids

import "github.com/google/uuid"

// New returns a fresh UUIDv7 in canonical 36-char dashed form, e.g.
// "01939a6e-3a4f-7b8c-9d2e-f1a2b3c4d5e6". Panics on entropy
// exhaustion — Go's crypto/rand only fails when /dev/urandom is
// unreachable, which means the box is already dead.
func New() string {
	u, err := uuid.NewV7()
	if err != nil {
		panic("ids: NewV7: " + err.Error())
	}
	return u.String()
}

// IsUUID returns true if s parses as ANY UUID version (v1-v8). Used
// by ID-vs-name resolvers across the codebase to decide whether an
// inbound argument is a function ID (uuid) or a function name (anything
// else). Cheap — UUID parsing is a length check + hex validation.
func IsUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
