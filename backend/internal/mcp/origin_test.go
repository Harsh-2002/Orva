package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Origin gate on /mcp. Note the net security delta is 401 -> 403: /mcp has
// no ambient credential (auth is header-only, no cookie is read anywhere in
// this package, and Allow-Credentials is never set), so a request the gate
// rejects was already going to be rejected by the auth gate. It exists so an
// operator who narrowed ORVA_CORS_ORIGINS gets that narrowing enforced on
// /mcp too, and so the code stops contradicting its own comment.
//
// The 200 rows are guard rows — they pass either way, and are here to prove
// the gate does not break the default or non-browser clients.
func TestMCPOriginGate(t *testing.T) {
	db := newStatelessDatabase(t)
	seedStatelessAPIKey(t, db, "orva_origin_test", "key-origin", "origin", `["read"]`)

	narrowed := []string{"https://orva.example", "https://ops.example"}

	cases := []struct {
		name    string
		allowed []string
		origin  string // "" means the header is not sent at all
		want    int
	}{
		{"default config allows a hostile origin", nil, "https://evil.example", http.StatusOK},
		{"explicit wildcard allows any origin", []string{"*"}, "https://evil.example", http.StatusOK},
		// The load-bearing row: nearly every MCP client is not a browser and
		// sends no Origin. Rejecting those would break the whole surface.
		{"narrowed still allows a request with no Origin", narrowed, "", http.StatusOK},
		{"narrowed allows a listed origin", narrowed, "https://orva.example", http.StatusOK},
		{"narrowed allows the second listed origin", narrowed, "https://ops.example", http.StatusOK},
		{"narrowed rejects an unlisted origin", narrowed, "https://evil.example", http.StatusForbidden},
		// Case is not normalized by corsMiddleware either; pin the behavior.
		{"origin match is case-sensitive", narrowed, "https://ORVA.example", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(Deps{DB: db, AllowedOrigins: tc.allowed})

			req := httptest.NewRequest(http.MethodPost, "/mcp",
				strings.NewReader(rpcBody(t, 1, "tools/list", map[string]any{"_meta": newProtocolMeta(true)})))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Authorization", "Bearer orva_origin_test")
			req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
			req.Header.Set(hdrMethod, "tools/list")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}

			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if tc.want == http.StatusForbidden {
				if w.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403 for origin %q", w.Code, tc.origin)
				}
				return
			}
			if w.Code == http.StatusForbidden {
				t.Fatalf("status = 403 for origin %q, want the request allowed through the gate", tc.origin)
			}
		})
	}
}

// The gate must not be made host-relative: under DNS rebinding the attacker
// controls Host as well as Origin, so trusting Host would defeat the point.
func TestMCPOriginGateIgnoresRequestHost(t *testing.T) {
	db := newStatelessDatabase(t)
	seedStatelessAPIKey(t, db, "orva_origin_host", "key-origin-host", "origin-host", `["read"]`)

	h := NewHandler(Deps{DB: db, AllowedOrigins: []string{"https://orva.example"}})
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(rpcBody(t, 1, "tools/list", map[string]any{"_meta": newProtocolMeta(true)})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer orva_origin_host")
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "evil.example"

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — Host must not launder a rejected Origin", w.Code)
	}
}

// The origin gate runs before authentication, so a rejected origin gets 403
// rather than 401 even with no credential at all.
func TestMCPOriginGatePrecedesAuth(t *testing.T) {
	db := newStatelessDatabase(t)
	h := NewHandler(Deps{DB: db, AllowedOrigins: []string{"https://orva.example"}})

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(rpcBody(t, 1, "tools/list", map[string]any{"_meta": newProtocolMeta(true)})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", "https://evil.example")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 before the auth gate returns 401", w.Code)
	}
}
