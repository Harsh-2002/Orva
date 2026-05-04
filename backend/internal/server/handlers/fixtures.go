// Per-function "Postman-style" request fixtures (v0.4 B3). Backs the
// editor's Test pane Saved popover and the test_function_with_fixture
// MCP tool. Same auth model as the rest of /api/v1/functions/<id>/*:
// the dashboard's session cookie or any API key with read/write.
//
// Validation is intentionally narrow — fixtures are operator-curated
// presets, not user input passed through to a function — so we trust
// the headers map but cap body size to keep the SQLite row small.
package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// fixtureBodyCap matches the KV operator surface: 64 KB body + a small
// envelope. Test fixtures are typically tiny (<1 KB); the cap is there to
// keep a runaway client from filling the row table with megabyte
// payloads. Bodies past the cap return 413.
const fixtureBodyCap = 64 * 1024
const fixtureRequestCap = 1 << 20 // 1 MB envelope, generous over the 64 KB body cap

// FixtureHandler exposes /api/v1/functions/{fn_id}/fixtures[/{name}].
type FixtureHandler struct {
	DB       *database.Database
	Registry *registry.Registry
}

// resolveFnID accepts either a UUID id or a friendly name and returns
// the canonical function id. Mirrors the KV operator helper.
func (h *FixtureHandler) resolveFnID(idOrName string) (string, bool) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return "", false
	}
	if ids.IsUUID(idOrName) {
		if _, err := h.Registry.Get(idOrName); err == nil {
			return idOrName, true
		}
	}
	if fn, err := h.DB.GetFunctionByName(idOrName); err == nil && fn != nil {
		return fn.ID, true
	}
	return "", false
}

// fixtureRequest is the shape of POST/PUT bodies. Headers is decoded as
// an object so the dashboard can send {"X-Foo":"bar"} directly. Body is
// a raw string — JSON callers wrap with json.stringify on the front end.
type fixtureRequest struct {
	Name    string            `json:"name"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// fixtureView is the public per-row shape. Headers is decoded back from
// the stored JSON so the dashboard doesn't have to re-parse a string.
type fixtureView struct {
	ID         string            `json:"id"`
	FunctionID string            `json:"function_id"`
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
}

func toFixtureView(f *database.Fixture) fixtureView {
	hdrs := map[string]string{}
	if f.HeadersJSON != "" {
		_ = json.Unmarshal([]byte(f.HeadersJSON), &hdrs)
	}
	return fixtureView{
		ID:         f.ID,
		FunctionID: f.FunctionID,
		Name:       f.Name,
		Method:     f.Method,
		Path:       f.Path,
		Headers:    hdrs,
		Body:       string(f.Body),
		CreatedAt:  f.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:  f.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// validMethods is the closed allowlist of HTTP methods the fixture form
// can store. We mirror what the editor's <select> exposes so the backend
// can't be talked into persisting "TRACE" or arbitrary verbs that would
// fail at invoke time anyway.
var validMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// validateAndNormalise mutates req in place — uppercases method, defaults
// path to "/", trims name. Returns an error suitable for a 400 response.
func validateAndNormalise(req *fixtureRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if len(req.Name) > 128 {
		return errors.New("name must be <= 128 chars")
	}
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	if req.Method == "" {
		req.Method = "POST"
	}
	if !validMethods[req.Method] {
		return errors.New("method must be one of GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS")
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		req.Path = "/"
	}
	if !strings.HasPrefix(req.Path, "/") {
		req.Path = "/" + req.Path
	}
	if len(req.Body) > fixtureBodyCap {
		return errors.New("body exceeds 64 KB cap")
	}
	if req.Headers == nil {
		req.Headers = map[string]string{}
	}
	return nil
}

// List handles GET /api/v1/functions/{fn_id}/fixtures.
func (h *FixtureHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	rows, err := h.DB.ListFixtures(fnID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "fixture list failed: "+err.Error(), reqID)
		return
	}
	out := make([]fixtureView, 0, len(rows))
	for _, f := range rows {
		out = append(out, toFixtureView(f))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"fixtures": out})
}

// Get handles GET /api/v1/functions/{fn_id}/fixtures/{name}.
func (h *FixtureHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	row, err := h.DB.GetFixtureByName(fnID, name)
	if errors.Is(err, database.ErrFixtureNotFound) {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "fixture not found", reqID)
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "fixture get failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, toFixtureView(row))
}

// Create handles POST /api/v1/functions/{fn_id}/fixtures. Returns 409
// when (function_id, name) already exists — callers that want upsert
// should use PUT /fixtures/{name} instead.
func (h *FixtureHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, fixtureRequestCap))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req fixtureRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if err := validateAndNormalise(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	headersJSON, _ := json.Marshal(req.Headers)
	row := &database.Fixture{
		FunctionID:  fnID,
		Name:        req.Name,
		Method:      req.Method,
		Path:        req.Path,
		HeadersJSON: string(headersJSON),
		Body:        []byte(req.Body),
	}
	if err := h.DB.InsertFixture(row); err != nil {
		// SQLite UNIQUE violation surfaces as a generic constraint error;
		// detect it via the substring rather than coupling to a sqlite-
		// specific error type.
		if strings.Contains(err.Error(), "UNIQUE") {
			respond.Error(w, http.StatusConflict, "CONFLICT", "fixture name already exists for this function", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "fixture insert failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusCreated, toFixtureView(row))
}

// Upsert handles PUT /api/v1/functions/{fn_id}/fixtures/{name}. The
// {name} path segment is authoritative; any `name` field in the body is
// ignored (the URL wins). Idempotent: 200 whether the row existed or not.
func (h *FixtureHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, fixtureRequestCap))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req fixtureRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	// Force the URL name to win — the body's `name` field is advisory.
	req.Name = name
	if err := validateAndNormalise(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	headersJSON, _ := json.Marshal(req.Headers)
	row := &database.Fixture{
		FunctionID:  fnID,
		Name:        req.Name,
		Method:      req.Method,
		Path:        req.Path,
		HeadersJSON: string(headersJSON),
		Body:        []byte(req.Body),
	}
	saved, err := h.DB.UpsertFixture(row)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "fixture upsert failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, toFixtureView(saved))
}

// Delete handles DELETE /api/v1/functions/{fn_id}/fixtures/{name}.
// Idempotent — 204 whether or not the row existed.
func (h *FixtureHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	if err := h.DB.DeleteFixture(fnID, name); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "fixture delete failed: "+err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
