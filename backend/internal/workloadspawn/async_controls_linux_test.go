//go:build linux

package workloadspawn_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	orvamcp "github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/internal/ids"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type asyncControls struct {
	server *httptest.Server
	keys   map[string]string
}

func newAsyncControls(t *testing.T, db *database.Database, reg *registry.Registry, s *workloadasync.Service) *asyncControls {
	t.Helper()
	c := &asyncControls{keys: map[string]string{}}
	for role, perms := range map[string]string{"admin": `["read","admin"]`, "reader": `["read"]`, "writer": `["read","write"]`, "writeonly": `["write"]`} {
		raw := "orva_" + strings.ReplaceAll(ids.New(), "-", "") + strings.ReplaceAll(ids.New(), "-", "")
		hash := sha256.Sum256([]byte(raw))
		liveMust(t, db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "async-" + role, KeyHash: hex.EncodeToString(hash[:]), Prefix: "fixture", Permissions: perms}))
		c.keys[role] = raw
	}
	h := &handlers.JobsHandler{DB: db, Registry: reg, AsyncEnabled: true, AsyncIAM: s}
	rest := http.NewServeMux()
	rest.HandleFunc("GET /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		evidence := os.Getenv("ORVA_ASYNC_EVIDENCE")
		if os.Getenv("ORVA_ASYNC_BROWSER_HOLD") == "1" && evidence != "" {
			if err := os.Remove(filepath.Join(evidence, "hold-next-list")); err == nil {
				rec := httptest.NewRecorder()
				h.List(rec, r)
				liveMust(t, os.WriteFile(filepath.Join(evidence, "list-held"), []byte("ready"), 0600))
				deadline := time.Now().Add(30 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(filepath.Join(evidence, "release-list")); err == nil {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				for key, values := range rec.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(rec.Code)
				_, _ = w.Write(rec.Body.Bytes())
				return
			}
		}
		h.List(w, r)
	})
	rest.HandleFunc("GET /api/v1/jobs/{id}", h.Get)
	rest.HandleFunc("POST /api/v1/jobs", h.Enqueue)
	rest.HandleFunc("POST /api/v1/jobs/{id}/resume", h.Resume)
	rest.HandleFunc("POST /api/v1/jobs/{id}/retry", h.Retry)
	rest.HandleFunc("DELETE /api/v1/jobs/{id}", h.Delete)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Orva-API-Key")
		if raw == "" {
			raw = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		hash := sha256.Sum256([]byte(raw))
		key, err := db.GetAPIKeyByHash(hex.EncodeToString(hash[:]))
		if err != nil {
			w.WriteHeader(401)
			return
		}
		perms := auth.PermSet{}
		for _, p := range key.PermissionsList() {
			perms[p] = true
		}
		if r.Method == "GET" && !perms.Has("read") {
			w.WriteHeader(403)
			return
		}
		p := &auth.Principal{Kind: auth.KindAPIKey, ID: key.ID, Perms: perms}
		rest.ServeHTTP(w, r.WithContext(auth.WithOperator(r.Context(), p)))
	}))
	mux.Handle("/mcp", orvamcp.NewHandler(orvamcp.Deps{DB: db, Registry: reg, AsyncEnabled: true, AsyncIAM: s}))
	c.server = httptest.NewServer(mux)
	t.Cleanup(c.server.Close)
	return c
}

func (c *asyncControls) request(t *testing.T, role, method, path string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		liveMust(t, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.server.URL+path, bytes.NewReader(raw))
	liveMust(t, err)
	req.Header.Set("X-Orva-API-Key", c.keys[role])
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	liveMust(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	liveMust(t, err)
	return resp.StatusCode, out
}

func (c *asyncControls) cli(t *testing.T, role, id, action string, wantSuccess bool) {
	t.Helper()
	bin := os.Getenv("ORVA_ASYNC_CLI")
	if bin == "" {
		t.Fatal("real CLI binary required for async fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--endpoint", c.server.URL, "-o", "json", "jobs", action, id)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "NO_COLOR=1", "ORVA_API_KEY=" + c.keys[role]}
	out, err := cmd.Output()
	if (err == nil) != wantSuccess {
		t.Fatalf("actual CLI %s role=%s success=%v, wanted %v", action, role, err == nil, wantSuccess)
	}
	if wantSuccess && !json.Valid(out) {
		t.Fatal("CLI stdout is not JSON")
	}
}

type asyncRPC struct {
	Error  json.RawMessage `json:"error"`
	Result struct {
		Tools []struct {
			Name         string          `json:"name"`
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

func (c *asyncControls) rpc(t *testing.T, role, method string, params any) asyncRPC {
	t.Helper()
	p, ok := params.(map[string]any)
	if !ok {
		t.Fatal("MCP params must be an object")
	}
	p["_meta"] = map[string]any{
		mcpsdk.MetaKeyProtocolVersion:    "2026-07-28",
		mcpsdk.MetaKeyClientCapabilities: map[string]any{},
		mcpsdk.MetaKeyClientInfo:         map[string]any{"name": "async-fixture", "version": "1"},
	}
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": p})
	liveMust(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.server.URL+"/mcp", bytes.NewReader(raw))
	liveMust(t, err)
	req.Header.Set("Authorization", "Bearer "+c.keys[role])
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", method)
	if p, ok := params.(map[string]any); ok {
		if name, ok := p["name"].(string); ok {
			req.Header.Set("Mcp-Name", name)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	liveMust(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	liveMust(t, err)
	if resp.StatusCode != 200 {
		var failure struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if resp.StatusCode == 400 && role != "admin" && method == "tools/call" && failure.Error.Code == -32602 && failure.Error.Message == fmt.Sprintf("unknown tool %q", p["name"]) {
			var denied asyncRPC
			liveMust(t, json.Unmarshal(data, &denied))
			return denied
		}
		t.Fatalf("external MCP %s role=%s HTTP %d code=%d message=%s", method, role, resp.StatusCode, failure.Error.Code, failure.Error.Message)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "data: ") {
				data = []byte(strings.TrimPrefix(line, "data: "))
				break
			}
		}
	}
	var result asyncRPC
	liveMust(t, json.Unmarshal(data, &result))
	return result
}

func TestAsyncManagementProtocol(t *testing.T) {
	db, err := database.New(t.TempDir() + "/protocol.db")
	liveMust(t, err)
	defer db.Close()
	liveMust(t, db.Migrate())
	c := newAsyncControls(t, db, registry.New(db), &workloadasync.Service{DB: db})
	_ = c.rpc(t, "reader", "tools/list", map[string]any{})
	reply := c.rpc(t, "reader", "tools/call", map[string]any{"name": "resume_job", "arguments": map[string]string{"id": ids.New()}})
	if len(reply.Error) == 0 && !reply.Result.IsError {
		t.Fatal("reader resume succeeded")
	}
}

func (c *asyncControls) denyChecks(t *testing.T, j *database.AsyncJob) {
	t.Helper()
	for _, role := range []string{"reader", "writer", "writeonly"} {
		status, raw := c.request(t, role, "GET", "/api/v1/jobs", nil)
		var list struct {
			CanManage     bool `json:"can_manage"`
			AsyncControls bool `json:"async_controls"`
		}
		if role != "writeonly" {
			liveMust(t, json.Unmarshal(raw, &list))
		}
		if role == "writeonly" && status != 403 {
			t.Fatal("write-only principal could read jobs")
		}
		if role != "writeonly" && (status != 200 || list.CanManage || !list.AsyncControls) {
			t.Fatal("read projection exposed admin controls")
		}
		status, _ = c.request(t, role, "POST", "/api/v1/jobs/"+j.ID+"/resume", nil)
		if status != 403 {
			t.Fatalf("%s REST resume status %d", role, status)
		}
		c.cli(t, role, j.ID, "resume", false)
		catalog := c.rpc(t, role, "tools/list", map[string]any{})
		for _, tool := range catalog.Result.Tools {
			switch tool.Name {
			case "resume_job", "retry_job", "enqueue_job", "delete_job":
				t.Fatalf("%s catalog exposed %s", role, tool.Name)
			}
		}
		reply := c.rpc(t, role, "tools/call", map[string]any{"name": "resume_job", "arguments": map[string]string{"id": j.ID}})
		if len(reply.Error) == 0 && !reply.Result.IsError {
			t.Fatal("external MCP read/write resume succeeded")
		}
	}
}
