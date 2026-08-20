package mcp

// Protocol pinning for MCP 2026-07-28 over Orva's /mcp Streamable HTTP handler.
//
// These tests live in package mcp, not package server, on purpose. Everything
// under test is owned here: NewHandler is what passes
// StreamableHTTPOptions{Stateless: true} to the SDK, and cacheScopeMiddleware is
// unexported. The server router only mounts the handler NewHandler returns, so
// exercising it through the router would add a database/registry/pool/builder
// graph without testing one extra line of protocol behaviour — and would put
// the cache-scope assertions out of reach. The harness below is the same shape
// as server_test.go's newTestServer (temp SQLite, Migrate, one seeded admin API
// key) with the router removed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// protocolVersion20260728 is the version this file pins. The SDK keeps its own
// copy unexported (mcp/shared.go), so it cannot be referenced; the MetaKey*
// constants below are exported and ARE referenced, so a rename there is a
// compile error rather than an assertion that silently stops matching.
const protocolVersion20260728 = "2026-07-28"

// Wire header names from the 2026-07-28 transport. Also unexported in the SDK
// (mcp/streamable_headers.go), hence the literals.
const (
	hdrProtocolVersion = "Mcp-Protocol-Version"
	hdrMethod          = "Mcp-Method"
	hdrSessionID       = "Mcp-Session-Id"
)

// ── harness ──────────────────────────────────────────────────────────

// newStatelessHandler returns the real /mcp handler plus an admin API key that
// authenticates against it. Deps carries only DB: tool registration never
// dereferences Registry/Builder/PoolMgr/etc., and none of the assertions here
// reach a tool handler body, so wiring the rest would be noise.
func newStatelessHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	db := newStatelessDatabase(t)

	const key = "orva_stateless_test_key_0123456789abcdef"
	seedStatelessAPIKey(t, db, key, "key_statelesstest", "stateless-test",
		`["invoke","read","write","admin"]`)

	return NewHandler(Deps{DB: db, Version: "test"}), key
}

func newStatelessDatabase(tb testing.TB) *database.Database {
	tb.Helper()
	db, err := database.New(filepath.Join(tb.TempDir(), "mcp-stateless.db"))
	if err != nil {
		tb.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		tb.Fatalf("migrate test db: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

func seedStatelessAPIKey(tb testing.TB, db *database.Database, plaintext, id, name, permissions string) {
	tb.Helper()
	sum := sha256.Sum256([]byte(plaintext))
	if err := db.InsertAPIKey(&database.APIKey{
		ID:          id,
		KeyHash:     hex.EncodeToString(sum[:]),
		Name:        name,
		Permissions: permissions,
	}); err != nil {
		tb.Fatalf("seed api key: %v", err)
	}
}

// rpcResponse is the JSON-RPC envelope, decoded far enough to branch on
// error-vs-result. Result stays raw so each subtest can decode it into the SDK
// result type it actually expects.
type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// decodeRPC pulls the JSON-RPC envelope out of a response body. A successful
// Streamable HTTP reply is SSE-framed ("event: message" + "data: {...}"), while
// transport-level rejections come back as bare application/json — both shapes
// are normal and both must be readable.
func decodeRPC(t *testing.T, w *httptest.ResponseRecorder) rpcResponse {
	t.Helper()

	payload := w.Body.String()
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		found := false
		for _, line := range strings.Split(payload, "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				payload, found = data, true
				break
			}
		}
		if !found {
			t.Fatalf("SSE response carried no data: frame; body=%s", truncBody(w.Body.String()))
		}
	}

	var res rpcResponse
	if err := json.Unmarshal([]byte(payload), &res); err != nil {
		t.Fatalf("decode JSON-RPC envelope: %v; body=%s", err, truncBody(w.Body.String()))
	}
	return res
}

// decodeResult decodes the JSON-RPC result into an SDK result type. Using the
// SDK's own structs (rather than ad-hoc maps) means a field rename upstream
// fails the build instead of quietly decoding to a zero value.
func decodeResult[T any](t *testing.T, res rpcResponse) *T {
	t.Helper()
	if res.Error != nil {
		t.Fatalf("expected a result, got JSON-RPC error %d: %s", res.Error.Code, res.Error.Message)
	}
	var out T
	if err := json.Unmarshal(res.Result, &out); err != nil {
		t.Fatalf("decode result into %T: %v; raw=%s", out, err, truncBody(string(res.Result)))
	}
	return &out
}

// newProtocolMeta builds the params._meta block the 2026-07-28 protocol
// requires. clientCapabilities is mandatory; clientInfo is optional. Passing
// includeCapabilities=false is how the "omitted required key" case is built,
// and it is the only difference between a well-formed request and that one.
func newProtocolMeta(includeCapabilities bool) map[string]any {
	meta := map[string]any{
		mcpsdk.MetaKeyProtocolVersion: protocolVersion20260728,
		mcpsdk.MetaKeyClientInfo: map[string]any{
			"name":    "orva-stateless-test",
			"version": "1",
		},
	}
	if includeCapabilities {
		meta[mcpsdk.MetaKeyClientCapabilities] = map[string]any{}
	}
	return meta
}

// rpcBody marshals a JSON-RPC request with the given params.
func rpcBody(t testing.TB, id int, method string, params map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(raw)
}

func truncBody(s string) string {
	const max = 700
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// ── the protocol contract ────────────────────────────────────────────

// TestStatelessMCP20260728 pins the behaviour that makes Orva reachable by
// 2026-07-28 MCP clients. The SDK serves that protocol version ONLY on a
// stateless Streamable HTTP handler; every case here fails loudly if
// StreamableHTTPOptions.Stateless is ever dropped from NewHandler.
func TestStatelessMCP20260728(t *testing.T) {
	handler, apiKey := newStatelessHandler(t)

	cases := []struct {
		name string
		// httpMethod defaults to POST when empty.
		httpMethod string
		// rpcMethod is both the JSON-RPC method and the Mcp-Method header.
		rpcMethod string
		params    map[string]any
		// legacy sends the pre-2026-07-28 shape: no protocol headers, no
		// _meta, version negotiated through the initialize handshake.
		legacy     bool
		wantStatus int
		check      func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			// The headline regression. Under the SDK's default (stateful)
			// options a 2026-07-28 request is refused outright with HTTP 400
			// -- "protocol version is only supported on stateless HTTP
			// servers" -- and no modern client can talk to Orva at all.
			name:       "tools/list without any initialize handshake",
			rpcMethod:  "tools/list",
			params:     map[string]any{"_meta": newProtocolMeta(true)},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				out := decodeResult[mcpsdk.ListToolsResult](t, decodeRPC(t, w))
				if len(out.Tools) == 0 {
					t.Fatalf("tools/list returned an EMPTY catalog over protocol %s. "+
						"A 2026-07-28 client never sends initialize, so this single call is "+
						"its whole view of Orva: an empty list means the agent believes the "+
						"instance exposes no tools at all.", protocolVersion20260728)
				}
				// A floor, not an exact count. Asserting exactly 73 would fail on
				// every legitimate tool addition, but a bare non-empty check is a
				// floor of ONE: a permission-gating regression in register*Tools
				// that collapsed the catalog to a handful would have passed it
				// silently. The admin key seeded above holds all four permissions,
				// so anything far below the real count means registration broke.
				const minAdminTools = 50
				if len(out.Tools) < minAdminTools {
					t.Errorf("tools/list returned %d tools for a key holding "+
						"invoke+read+write+admin; expected at least %d. The catalog did not "+
						"vanish, so this is a registration or permission-gating regression "+
						"rather than a transport one.", len(out.Tools), minAdminTools)
				}
			},
		},
		{
			// The catalog is built per principal: register*Tools is gated on
			// the caller's permission set and a channel token sees only its
			// own bundled functions. "public" invites any shared cache or
			// intermediary to replay one principal's answer to another, which
			// discloses a tool surface -- function names included -- that the
			// second caller was never entitled to see.
			name:       "tools/list is cache-scoped private, not public",
			rpcMethod:  "tools/list",
			params:     map[string]any{"_meta": newProtocolMeta(true)},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				out := decodeResult[mcpsdk.ListToolsResult](t, decodeRPC(t, w))
				if out.CacheScope != cacheScopePrivate {
					t.Fatalf("tools/list cacheScope = %q, want %q. The SDK's default is "+
						"\"public\", meaning any client OR INTERMEDIARY may cache this "+
						"response and serve it to somebody else. Orva's catalog is "+
						"permission-scoped and channel-specific, so two callers of the same "+
						"URL are entitled to different answers: a shared cache entry leaks "+
						"one principal's tool surface to another. cacheScopeMiddleware must "+
						"stay registered on every server built in NewHandler.",
						out.CacheScope, cacheScopePrivate)
				}
			},
		},
		{
			// server/discover is how a client learns what to speak before it
			// commits to a version. Dropping 2026-07-28 from this list makes
			// well-behaved clients negotiate down even when the transport
			// would have served the new protocol fine.
			name:       "server/discover advertises 2026-07-28",
			rpcMethod:  "server/discover",
			params:     map[string]any{"_meta": newProtocolMeta(true)},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				out := decodeResult[mcpsdk.DiscoverResult](t, decodeRPC(t, w))
				if !contains(out.SupportedVersions, protocolVersion20260728) {
					t.Fatalf("server/discover supportedVersions = %v, missing %q. "+
						"Clients that negotiate via discover will silently fall back to an "+
						"older protocol even though the stateless transport can serve this one.",
						out.SupportedVersions, protocolVersion20260728)
				}
			},
		},
		{
			// Back-compat leg. Statelessness is not allowed to cost us the
			// installed base: a pre-2026-07-28 client opens with initialize
			// and must still get a negotiated version back.
			name:       "legacy initialize still negotiates 2025-11-25",
			rpcMethod:  "initialize",
			legacy:     true,
			wantStatus: http.StatusOK,
			params: map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "legacy-client", "version": "0"},
			},
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				out := decodeResult[mcpsdk.InitializeResult](t, decodeRPC(t, w))
				if out.ProtocolVersion != "2025-11-25" {
					t.Fatalf("legacy initialize negotiated %q, want \"2025-11-25\". "+
						"Serving the new protocol must not drop the old handshake — every "+
						"already-deployed MCP client opens with exactly this call.",
						out.ProtocolVersion)
				}
			},
		},
		{
			// The historically session-minting call. Stateless mode must issue
			// no session id at all.
			name:       "no Mcp-Session-Id is issued",
			rpcMethod:  "initialize",
			legacy:     true,
			wantStatus: http.StatusOK,
			params: map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "legacy-client", "version": "0"},
			},
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				if sid := w.Header().Get(hdrSessionID); sid != "" {
					t.Fatalf("initialize issued %s: %q. A session id means the handler is "+
						"stateful again, and a stateful handler answers every 2026-07-28 "+
						"request with HTTP 400. It also re-freezes the tool catalog at the "+
						"permissions held when the session opened, so a mid-session "+
						"permission downgrade stops being re-gated.", hdrSessionID, sid)
				}
			},
		},
		{
			// clientCapabilities moved from the initialize handshake into
			// per-request _meta (SEP-2575). With no handshake left to carry
			// it, a request that omits it is not answerable — the server must
			// say so rather than guessing at capabilities or panicking on a
			// nil dereference.
			name:       "missing clientCapabilities _meta is rejected as invalid params",
			rpcMethod:  "tools/list",
			params:     map[string]any{"_meta": newProtocolMeta(false)},
			wantStatus: http.StatusBadRequest,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				res := decodeRPC(t, w)
				if res.Error == nil {
					t.Fatalf("omitting %q produced a SUCCESS response. The server silently "+
						"assumed capabilities the client never declared; a client that "+
						"cannot handle elicitation or sampling will be handed one anyway.",
						mcpsdk.MetaKeyClientCapabilities)
				}
				if res.Error.Code != jsonrpc.CodeInvalidParams {
					t.Fatalf("omitting %q returned JSON-RPC code %d (%s), want %d "+
						"(invalid params). The client needs the typed code to know its "+
						"request was malformed rather than that Orva is broken.",
						mcpsdk.MetaKeyClientCapabilities, res.Error.Code,
						res.Error.Message, jsonrpc.CodeInvalidParams)
				}
			},
		},
		{
			// Stateless servers speak POST only; the standalone SSE stream a
			// GET would open needs a session to belong to.
			name:       "GET /mcp is method not allowed",
			httpMethod: http.MethodGet,
			wantStatus: http.StatusMethodNotAllowed,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				// RFC 9110 §15.5.6 — a 405 must say what IS allowed, or a
				// client has no way to recover other than guessing.
				if allow := w.Header().Get("Allow"); allow != http.MethodPost {
					t.Errorf("405 on GET /mcp advertised Allow: %q, want %q", allow, http.MethodPost)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpMethod := tc.httpMethod
			if httpMethod == "" {
				httpMethod = http.MethodPost
			}

			var body string
			if tc.rpcMethod != "" {
				body = rpcBody(t, 1, tc.rpcMethod, tc.params)
			}

			req := httptest.NewRequest(httpMethod, "/mcp", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if !tc.legacy && tc.rpcMethod != "" {
				// The new protocol mirrors the version and the method into
				// headers so proxies can route without parsing the body; the
				// SDK rejects the request if either is missing or disagrees
				// with the JSON-RPC payload.
				req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
				req.Header.Set(hdrMethod, tc.rpcMethod)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.wantStatus, truncBody(w.Body.String()))
			}

			// Blanket tripwire: no response on a stateless handler may carry a
			// session id, whatever the case under test was doing.
			if sid := w.Header().Get(hdrSessionID); sid != "" {
				t.Errorf("response carried %s: %q — the handler is stateful again, "+
					"which makes every 2026-07-28 request fail with HTTP 400",
					hdrSessionID, sid)
			}

			if tc.check != nil {
				tc.check(t, w)
			}
		})
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// ── cache scope ──────────────────────────────────────────────────────

// cacheableResultTypes is the set cacheScopeMiddleware rewrites. It is the
// authoritative list this test compares the SDK against; adding a type here
// without adding the matching case to cachescope.go's switch fails the
// behavioural subtest below.
func cacheableResultTypes() map[string]mcpsdk.Result {
	return map[string]mcpsdk.Result{
		"ListToolsResult":             &mcpsdk.ListToolsResult{},
		"ListResourcesResult":         &mcpsdk.ListResourcesResult{},
		"ListResourceTemplatesResult": &mcpsdk.ListResourceTemplatesResult{},
		"ListPromptsResult":           &mcpsdk.ListPromptsResult{},
		"ReadResourceResult":          &mcpsdk.ReadResourceResult{},
		"DiscoverResult":              &mcpsdk.DiscoverResult{},
	}
}

// TestEveryCacheableResultIsScopedPrivate is the test cachescope.go's comment
// promises. cacheScopeMiddleware cannot use the CacheableResult interface — it
// is read-only (GetCacheScope, no setter) — so it switches on concrete types,
// and a cacheable result type the switch does not name keeps the SDK's "public"
// default and leaks across principals. Two halves: every type we claim to
// handle really is rewritten, and the SDK has not grown one we missed.
func TestEveryCacheableResultIsScopedPrivate(t *testing.T) {
	handled := cacheableResultTypes()

	t.Run("middleware rewrites every handled result type", func(t *testing.T) {
		for name, empty := range handled {
			t.Run(name, func(t *testing.T) {
				result := empty
				next := func(_ context.Context, _ string, _ mcpsdk.Request) (mcpsdk.Result, error) {
					return result, nil
				}
				got, err := cacheScopeMiddleware()(next)(t.Context(), "test/method", nil)
				if err != nil {
					t.Fatalf("middleware returned error: %v", err)
				}
				cacheable, ok := got.(mcpsdk.CacheableResult)
				if !ok {
					t.Fatalf("%s does not implement CacheableResult — it does not belong in "+
						"cacheScopeMiddleware's switch", name)
				}
				if scope := cacheable.GetCacheScope(); scope != cacheScopePrivate {
					t.Errorf("%s cacheScope = %q, want %q. cachescope.go's switch is missing "+
						"this type. Called in isolation the value is empty because the "+
						"middleware runs before the SDK's setDefaultCacheableValues; on a real "+
						"server that default lands and the result ships as \"public\", so any "+
						"shared cache may serve one principal's answer to another.",
						name, scope, cacheScopePrivate)
				}
			})
		}
	})

	// Exhaustiveness. Reflection cannot enumerate the types in a package, so
	// this reads the SDK source that `go test` already downloaded and finds
	// every exported struct embedding mcp.Cacheable. That is precisely the set
	// the middleware must cover.
	t.Run("SDK has grown no cacheable result the middleware ignores", func(t *testing.T) {
		dir := sdkPackageDir(t)
		if dir == "" {
			t.Skip("SDK source not locatable; the behavioural subtest above still runs")
		}

		sources, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(sources) == 0 {
			t.Skipf("no Go sources under %s (err=%v)", dir, err)
		}

		fset := token.NewFileSet()
		var found []string
		for _, path := range sources {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Skipf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, field := range st.Fields.List {
					// An embedded field has no names; Cacheable is embedded
					// by value in every cacheable result.
					if len(field.Names) != 0 {
						continue
					}
					if ident, ok := field.Type.(*ast.Ident); ok && ident.Name == "Cacheable" {
						found = append(found, ts.Name.Name)
					}
				}
				return true
			})
		}
		sort.Strings(found)

		if len(found) == 0 {
			t.Skipf("no types embedding Cacheable found under %s — the SDK layout changed; "+
				"re-derive this check rather than trusting a silent pass", dir)
		}

		var missing []string
		for _, name := range found {
			if _, ok := handled[name]; !ok {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("the SDK gained cacheable result type(s) %v that cacheScopeMiddleware "+
				"does not handle. Unhandled types keep the SDK's \"public\" cacheScope "+
				"default, so these results become shareable across principals. Add a case "+
				"to the switch in cachescope.go and an entry to cacheableResultTypes().",
				missing)
		}

		// The reverse direction: a type we still claim to handle that the SDK
		// has dropped or renamed would make the switch case dead code. Caught
		// at compile time by cacheableResultTypes() referencing the type, so
		// only the count needs checking here.
		if len(found) != len(handled) {
			t.Errorf("SDK exposes %d cacheable result types %v but cacheableResultTypes() "+
				"lists %d — the two lists have drifted", len(found), found, len(handled))
		}
	})
}

// sdkPackageDir resolves the on-disk source directory of the MCP SDK's mcp
// package via the module cache. Returns "" when it cannot be determined (no go
// tool on PATH, vendored build, trimmed environment) — the caller skips.
func sdkPackageDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}",
		"github.com/modelcontextprotocol/go-sdk").Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return ""
	}
	return filepath.Join(root, "mcp")
}

// TestChannelPathIsAlsoCacheScopedPrivate closes a coverage gap that a reviewer
// found the hard way.
//
// NewHandler builds TWO servers — an operator one and a channel one — and each
// registers cacheScopeMiddleware separately. Every other test here authenticates
// with an operator API key, and TestEveryCacheableResultIsScopedPrivate exercises
// the middleware function in isolation, so removing the middleware from ONLY the
// channel branch left the whole Go suite green. It was caught by the Python E2E
// module, which runs in CI but not in `make test`.
//
// The channel branch is the worse one to lose. A channel token's catalog is the
// most tightly scoped surface Orva exposes — one invoke-only tool per bundled
// function, and nothing else — so a "public" cacheScope there invites an
// intermediary to serve one tenant's function names to another tenant.
//
// The channel is created with no bound functions on purpose: registerChannelTools
// returns early on an empty FunctionIDs list, which keeps Registry out of the
// picture, and an empty tool list still carries a cacheScope. The middleware
// registration is what is under test, not the catalog contents.
func TestChannelPathIsAlsoCacheScopedPrivate(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "mcp-channel.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	const token = "orva_chn_0123456789abcdef0123456789abcdef"
	sum := sha256.Sum256([]byte(token))
	if err := db.InsertChannel(&database.Channel{
		ID:          "chan_cachescope",
		Name:        "cachescope-test",
		TokenHash:   hex.EncodeToString(sum[:]),
		TokenPrefix: token[:16],
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	handler := NewHandler(Deps{DB: db, Version: "test"})

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(rpcBody(t, 1, "tools/list",
			map[string]any{"_meta": newProtocolMeta(true)})))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
	req.Header.Set(hdrMethod, "tools/list")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("channel tools/list status = %d, want 200; body=%s",
			w.Code, truncBody(w.Body.String()))
	}
	out := decodeResult[mcpsdk.ListToolsResult](t, decodeRPC(t, w))
	if out.CacheScope != cacheScopePrivate {
		t.Fatalf("channel tools/list cacheScope = %q, want %q. cacheScopeMiddleware is "+
			"registered separately on the channel server in NewHandler and has been "+
			"dropped from that branch. A channel catalog is the most tightly scoped "+
			"surface Orva has, so a shareable cache entry here leaks one tenant's "+
			"function names to another.", out.CacheScope, cacheScopePrivate)
	}
}

func serveModernRPC(t testing.TB, handler http.Handler, apiKey, host, method string, params map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(rpcBody(t, 1, method, params)))
	if host == "" {
		host = "orva.test"
	}
	req.Host = host
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
	req.Header.Set(hdrMethod, method)
	if method == "tools/call" {
		if name, _ := params["name"].(string); name != "" {
			req.Header.Set("Mcp-Name", name)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func listedToolNames(t *testing.T, w *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, want 200; body=%s", w.Code, truncBody(w.Body.String()))
	}
	out := decodeResult[mcpsdk.ListToolsResult](t, decodeRPC(t, w))
	names := make(map[string]bool, len(out.Tools))
	for _, tool := range out.Tools {
		names[tool.Name] = true
	}
	return names
}

func decodeStructuredToolResult[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d, want 200; body=%s", w.Code, truncBody(w.Body.String()))
	}
	out := decodeResult[mcpsdk.CallToolResult](t, decodeRPC(t, w))
	raw, err := json.Marshal(out.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structuredContent: %v", err)
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode structuredContent into %T: %v; raw=%s", result, err, truncBody(string(raw)))
	}
	return result
}

func TestOperatorServerCacheReusesPermissionVariants(t *testing.T) {
	var cache operatorServerCache
	var builds atomic.Int32
	build := func() *mcpsdk.Server {
		builds.Add(1)
		return mcpsdk.NewServer(&mcpsdk.Implementation{Name: "cache-test", Version: "1"}, nil)
	}

	readA := cache.get(permSet{permRead: true}, build)
	readB := cache.get(permSet{permRead: true}, build)
	admin := cache.get(permSet{permRead: true, permAdmin: true}, build)
	if readA != readB {
		t.Fatal("identical permission sets returned different cached servers")
	}
	if readA == admin {
		t.Fatal("different permission surfaces shared one cached server")
	}
	if got := builds.Load(); got != 2 {
		t.Fatalf("server builds = %d, want one per permission variant (2)", got)
	}

	var concurrent operatorServerCache
	builds.Store(0)
	const callers = 64
	servers := make(chan *mcpsdk.Server, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			servers <- concurrent.get(permSet{permInvoke: true, permRead: true}, build)
		}()
	}
	wg.Wait()
	close(servers)
	var first *mcpsdk.Server
	for server := range servers {
		if first == nil {
			first = server
		} else if server != first {
			t.Fatal("concurrent cache lookup returned more than one server")
		}
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("concurrent server builds = %d, want 1", got)
	}
}

func TestPermissionDowngradeSelectsCachedRestrictedServerImmediately(t *testing.T) {
	db := newStatelessDatabase(t)
	const key = "orva_permission_downgrade_0123456789abcdef"
	seedStatelessAPIKey(t, db, key, "key_downgrade", "downgrade",
		`["invoke","read","write","admin"]`)
	handler := NewHandler(Deps{DB: db, Version: "test"})
	params := map[string]any{"_meta": newProtocolMeta(true)}

	before := listedToolNames(t, serveModernRPC(t, handler, key, "orva.test", "tools/list", params))
	if !before["create_function"] || !before["list_functions"] {
		t.Fatalf("admin catalog missing expected tools: create=%v list=%v", before["create_function"], before["list_functions"])
	}

	if err := db.DeleteAPIKey("key_downgrade"); err != nil {
		t.Fatalf("delete key before permission downgrade: %v", err)
	}
	seedStatelessAPIKey(t, db, key, "key_downgrade", "downgrade", `["read"]`)

	after := listedToolNames(t, serveModernRPC(t, handler, key, "orva.test", "tools/list", params))
	if !after["list_functions"] {
		t.Fatal("read-only catalog lost list_functions after downgrade")
	}
	if after["create_function"] {
		t.Fatal("create_function remained visible after immediate write-permission downgrade")
	}
	if len(after) >= len(before) {
		t.Fatalf("downgrade did not reduce catalog: before=%d after=%d", len(before), len(after))
	}
}

func TestCachedServerUsesRequestOriginForFunctionAndDocs(t *testing.T) {
	db := newStatelessDatabase(t)
	const key = "orva_origin_context_0123456789abcdef"
	seedStatelessAPIKey(t, db, key, "key_origin", "origin", `["read"]`)
	reg := registry.New(db)
	fn := &database.Function{
		ID: "018f0000-0000-7000-8000-000000000001", Name: "origin-context",
		Runtime: "node", Entrypoint: "handler.js", TimeoutMS: 5000,
		MemoryMB: 64, CPUs: 0.25, NetworkMode: database.NetworkModeNone,
		ConcurrencyPolicy: database.ConcurrencyPolicyQueue, AuthMode: database.AuthModeNone,
		Version: 1, Status: "active",
	}
	if err := reg.Set(fn); err != nil {
		t.Fatalf("seed function: %v", err)
	}
	handler := NewHandler(Deps{DB: db, Registry: reg, Version: "test"})

	call := func(host, name string, args map[string]any) *httptest.ResponseRecorder {
		return serveModernRPC(t, handler, key, host, "tools/call", map[string]any{
			"_meta": newProtocolMeta(true), "name": name, "arguments": args,
		})
	}
	for _, host := range []string{"alpha.example", "beta.example"} {
		function := decodeStructuredToolResult[FunctionView](t,
			call(host, "get_function", map[string]any{"function_id": fn.ID}))
		wantInvoke := "https://" + host + "/fn/" + fn.ID
		if function.InvokeURL != wantInvoke {
			t.Errorf("get_function on %s returned invoke_url %q, want %q", host, function.InvokeURL, wantInvoke)
		}

		docs := decodeStructuredToolResult[GetOrvaDocsOutput](t,
			call(host, "get_orva_docs", map[string]any{}))
		wantOrigin := "https://" + host
		if docs.Origin != wantOrigin {
			t.Errorf("get_orva_docs on %s returned origin %q, want %q", host, docs.Origin, wantOrigin)
		}
		if !strings.Contains(docs.Markdown, wantOrigin) {
			t.Errorf("get_orva_docs markdown on %s did not substitute request origin", host)
		}
	}
}

func TestCachedServerAttributesActivityToEachRequestPrincipal(t *testing.T) {
	db := newStatelessDatabase(t)
	const keyA = "orva_activity_actor_a_0123456789abcdef"
	const keyB = "orva_activity_actor_b_0123456789abcdef"
	seedStatelessAPIKey(t, db, keyA, "key_actor_a", "actor-a", `["read"]`)
	seedStatelessAPIKey(t, db, keyB, "key_actor_b", "actor-b", `["read"]`)
	handler := NewHandler(Deps{DB: db, Version: "test"})
	params := map[string]any{
		"_meta": newProtocolMeta(true), "name": "get_orva_docs", "arguments": map[string]any{},
	}
	for _, key := range []string{keyA, keyB} {
		w := serveModernRPC(t, handler, key, "orva.test", "tools/call", params)
		if w.Code != http.StatusOK {
			t.Fatalf("tools/call status = %d; body=%s", w.Code, truncBody(w.Body.String()))
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	seen := map[string]string{}
	for time.Now().Before(deadline) {
		rows, _, _, err := db.ListActivity(database.ActivityFilter{Source: "mcp", Limit: 20})
		if err != nil {
			t.Fatalf("list activity: %v", err)
		}
		for _, row := range rows {
			if row.ActorID == "key_actor_a" || row.ActorID == "key_actor_b" {
				seen[row.ActorID] = row.ActorLabel
			}
		}
		if len(seen) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if seen["key_actor_a"] != "actor-a" || seen["key_actor_b"] != "actor-b" {
		t.Fatalf("cached server activity attribution = %v, want both request principals", seen)
	}
}

func TestCachedOperatorServerHandlesConcurrentRequests(t *testing.T) {
	handler, key := newStatelessHandler(t)
	body := rpcBody(t, 1, "tools/list", map[string]any{"_meta": newProtocolMeta(true)})
	const requests = 64
	errCh := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
			req.Header.Set(hdrMethod, "tools/list")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				errCh <- fmt.Errorf("status %d: %s", w.Code, truncBody(w.Body.String()))
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func BenchmarkStatelessToolsListWarm(b *testing.B) {
	db := newStatelessDatabase(b)
	const key = "orva_benchmark_key_0123456789abcdef"
	seedStatelessAPIKey(b, db, key, "key_benchmark", "benchmark",
		`["invoke","read","write","admin"]`)
	handler := NewHandler(Deps{DB: db, Version: "bench"})
	body := rpcBody(b, 1, "tools/list", map[string]any{"_meta": newProtocolMeta(true)})
	run := func() int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
		req.Header.Set(hdrMethod, "tools/list")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	if code := run(); code != http.StatusOK {
		b.Fatalf("warmup status = %d, want 200", code)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if code := run(); code != http.StatusOK {
			b.Fatalf("status = %d, want 200", code)
		}
	}
}
