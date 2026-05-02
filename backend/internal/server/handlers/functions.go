package handlers

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// errVersionGCd is returned by Rollback when the target version directory
// has been pruned by the GC. errmap.go maps it to HTTP 410 VERSION_GCD.
var errVersionGCd = errors.New("requested version has been garbage-collected")

// resolveFnID accepts either an ID (fn_xxx) or a friendly name and
// returns the canonical function ID. Mirrors the helper used by the KV /
// fixtures / inbound-webhook handlers so /functions/{name}/... and
// /functions/{id}/... behave identically across the API surface.
func (h *FunctionHandler) resolveFnID(idOrName string) (string, bool) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return "", false
	}
	if strings.HasPrefix(idOrName, "fn_") {
		if _, err := h.Registry.Get(idOrName); err == nil {
			return idOrName, true
		}
	}
	if fn, err := h.DB.GetFunctionByName(idOrName); err == nil && fn != nil {
		return fn.ID, true
	}
	return "", false
}

// FunctionHandler handles function CRUD and deploy operations.
type FunctionHandler struct {
	Registry   *registry.Registry
	Builder    *builder.Builder
	DB         *database.Database
	Metrics    *metrics.Metrics
	DataDir    string
	BuildQueue *builder.Queue // async build pipeline; nil fallback = synchronous legacy path
	// PoolRefresh is called by the deploy path after a successful build so
	// the warm pool drops stale workers. Nil = no-op (dev / tests).
	PoolRefresh func(fnID string)
	// PoolDrain is called on delete to kill workers and remove the pool entry
	// so the function no longer appears in metrics. Nil = no-op (dev / tests).
	PoolDrain func(fnID string)
	// FnLock returns a per-function mutex shared with the build queue, so
	// Rollback serializes against any in-flight deploy of the same fn.
	// Wired in server.New from pool.Manager.FunctionLock.
	FnLock func(fnID string) *sync.Mutex
}

// createFunctionRequest is the body for creating a function.
type createFunctionRequest struct {
	Name              string            `json:"name"`
	Runtime           string            `json:"runtime"`
	Entrypoint        string            `json:"entrypoint"`
	TimeoutMS         int64             `json:"timeout_ms"`
	MemoryMB          int64             `json:"memory_mb"`
	CPUs              float64           `json:"cpus"`
	EnvVars           map[string]string `json:"env_vars"`
	NetworkMode       string            `json:"network_mode"`
	MaxConcurrency    int               `json:"max_concurrency"`
	ConcurrencyPolicy string            `json:"concurrency_policy"`
	AuthMode          string            `json:"auth_mode"`
	RateLimitPerMin   int               `json:"rate_limit_per_min"`
}

// updateFunctionRequest is the body for updating a function.
type updateFunctionRequest struct {
	Name              *string            `json:"name"`
	Entrypoint        *string            `json:"entrypoint"`
	TimeoutMS         *int64             `json:"timeout_ms"`
	MemoryMB          *int64             `json:"memory_mb"`
	CPUs              *float64           `json:"cpus"`
	EnvVars           *map[string]string `json:"env_vars"`
	NetworkMode       *string            `json:"network_mode"`
	MaxConcurrency    *int               `json:"max_concurrency"`
	ConcurrencyPolicy *string            `json:"concurrency_policy"`
	AuthMode          *string            `json:"auth_mode"`
	RateLimitPerMin   *int               `json:"rate_limit_per_min"`
	Status            *string            `json:"status"`
}

// userSettableStatus is the whitelist of status values an operator may set
// via PUT /api/v1/functions/{id}. Internal lifecycle states (created, queued,
// building, error) are managed by the deploy pipeline and rejected here so
// clients can't put a function into an invalid state.
var userSettableStatus = map[string]bool{
	"active":   true,
	"inactive": true,
}

var validRuntimes = map[string]bool{
	"node22":    true,
	"node24":    true,
	"python313": true,
	"python314": true,
}

// runtimeIsNode / runtimeIsPython centralise the "what kind of runtime is
// this string" decision so handler switches don't need to be updated
// every time we bump language versions.
func runtimeIsNode(r string) bool   { return r == "node22" || r == "node24" }
func runtimeIsPython(r string) bool { return r == "python313" || r == "python314" }

// Create handles POST /api/v1/functions.
func (h *FunctionHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	var req createFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}

	// Validate required fields.
	if req.Name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	if req.Runtime == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "runtime is required", reqID)
		return
	}
	if !validRuntimes[req.Runtime] {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", fmt.Sprintf("unsupported runtime: %s", req.Runtime), reqID)
		return
	}
	if !database.ValidNetworkMode(req.NetworkMode) {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", fmt.Sprintf("invalid network_mode: %s (allowed: none, egress)", req.NetworkMode), reqID)
		return
	}
	if !database.ValidConcurrencyPolicy(req.ConcurrencyPolicy) {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", fmt.Sprintf("invalid concurrency_policy: %s (allowed: queue, reject)", req.ConcurrencyPolicy), reqID)
		return
	}
	if req.MaxConcurrency < 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "max_concurrency must be >= 0 (0 = unlimited)", reqID)
		return
	}
	if !database.ValidAuthMode(req.AuthMode) {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			fmt.Sprintf("invalid auth_mode: %s (allowed: none, platform_key, signed)", req.AuthMode), reqID)
		return
	}
	if req.RateLimitPerMin < 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "rate_limit_per_min must be >= 0 (0 = unlimited)", reqID)
		return
	}

	// Apply defaults.
	if req.Entrypoint == "" {
		switch {
		case runtimeIsNode(req.Runtime):
			req.Entrypoint = "handler.js"
		case runtimeIsPython(req.Runtime):
			req.Entrypoint = "handler.py"
		}
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = 30000
	}
	if req.MemoryMB <= 0 {
		req.MemoryMB = 64
	}
	if req.CPUs <= 0 {
		req.CPUs = 0.5
	}
	if req.NetworkMode == "" {
		// Default = isolated net namespace, loopback only. Functions opt
		// into outbound network access by setting "egress" via the
		// editor's Settings modal or this API.
		req.NetworkMode = database.NetworkModeNone
	}
	if req.ConcurrencyPolicy == "" {
		req.ConcurrencyPolicy = database.ConcurrencyPolicyQueue
	}
	if req.AuthMode == "" {
		req.AuthMode = database.AuthModeNone
	}
	if req.EnvVars == nil {
		req.EnvVars = make(map[string]string)
	}

	// Generate function ID.
	suffix, err := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 12)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to generate ID", reqID)
		return
	}
	fnID := "fn_" + suffix

	fn := &database.Function{
		ID:                fnID,
		Name:              req.Name,
		Runtime:           req.Runtime,
		Entrypoint:        req.Entrypoint,
		TimeoutMS:         req.TimeoutMS,
		MemoryMB:          req.MemoryMB,
		CPUs:              req.CPUs,
		EnvVars:           req.EnvVars,
		NetworkMode:       req.NetworkMode,
		MaxConcurrency:    req.MaxConcurrency,
		ConcurrencyPolicy: req.ConcurrencyPolicy,
		AuthMode:          req.AuthMode,
		RateLimitPerMin:   req.RateLimitPerMin,
		Status:            "created",
		Version:           1,
	}

	if err := h.Registry.Set(fn); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			respond.Error(w, http.StatusConflict, "CONFLICT", "function name already exists", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create function", reqID)
		return
	}

	respond.JSON(w, http.StatusCreated, fn)
}

// List handles GET /api/v1/functions.
func (h *FunctionHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	params := database.ListFunctionsParams{
		Status:  r.URL.Query().Get("status"),
		Runtime: r.URL.Query().Get("runtime"),
	}

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			params.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			params.Offset = n
		}
	}

	result, err := h.Registry.List(params)
	if err != nil {
		slog.Error("list functions failed", "error", err, "request_id", reqID)
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list functions: "+err.Error(), reqID)
		return
	}

	respond.JSON(w, http.StatusOK, result)
}

// Get handles GET /api/v1/functions/{fn_id}.
func (h *FunctionHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := extractPathParam(r.URL.Path, "/api/v1/functions/")
	if fnID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}

	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	respond.JSON(w, http.StatusOK, fn)
}

// Update handles PUT /api/v1/functions/{fn_id}.
func (h *FunctionHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rawID := extractPathParam(r.URL.Path, "/api/v1/functions/")
	if rawID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}
	fnID, ok := h.resolveFnID(rawID)
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	var req updateFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}

	// Validate mutables before any mutation, so we don't half-apply.
	if req.NetworkMode != nil && !database.ValidNetworkMode(*req.NetworkMode) {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			fmt.Sprintf("invalid network_mode: %s (allowed: none, egress)", *req.NetworkMode), reqID)
		return
	}
	if req.ConcurrencyPolicy != nil && !database.ValidConcurrencyPolicy(*req.ConcurrencyPolicy) {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			fmt.Sprintf("invalid concurrency_policy: %s (allowed: queue, reject)", *req.ConcurrencyPolicy), reqID)
		return
	}
	if req.MaxConcurrency != nil && *req.MaxConcurrency < 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "max_concurrency must be >= 0 (0 = unlimited)", reqID)
		return
	}
	if req.AuthMode != nil && !database.ValidAuthMode(*req.AuthMode) {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			fmt.Sprintf("invalid auth_mode: %s (allowed: none, platform_key, signed)", *req.AuthMode), reqID)
		return
	}
	if req.RateLimitPerMin != nil && *req.RateLimitPerMin < 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "rate_limit_per_min must be >= 0 (0 = unlimited)", reqID)
		return
	}
	if req.Status != nil && !userSettableStatus[*req.Status] {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "status must be one of: active, inactive", reqID)
		return
	}

	// Track whether anything that affects the spawn config changed — if
	// so, we drain the warm pool so the next invoke re-spawns with the
	// new config (memory, CPU, env vars, and now network_mode).
	spawnConfigChanged := false

	// Apply partial updates.
	if req.Name != nil {
		fn.Name = *req.Name
	}
	if req.Entrypoint != nil {
		fn.Entrypoint = *req.Entrypoint
	}
	if req.TimeoutMS != nil {
		fn.TimeoutMS = *req.TimeoutMS
		spawnConfigChanged = true
	}
	if req.MemoryMB != nil {
		fn.MemoryMB = *req.MemoryMB
		spawnConfigChanged = true
	}
	if req.CPUs != nil {
		fn.CPUs = *req.CPUs
		spawnConfigChanged = true
	}
	if req.EnvVars != nil {
		fn.EnvVars = *req.EnvVars
		spawnConfigChanged = true
	}
	if req.NetworkMode != nil {
		newMode := *req.NetworkMode
		if newMode == "" {
			newMode = database.NetworkModeNone
		}
		if newMode != fn.NetworkMode {
			fn.NetworkMode = newMode
			spawnConfigChanged = true
		}
	}
	if req.MaxConcurrency != nil && *req.MaxConcurrency != fn.MaxConcurrency {
		fn.MaxConcurrency = *req.MaxConcurrency
		spawnConfigChanged = true
	}
	if req.ConcurrencyPolicy != nil {
		newPolicy := *req.ConcurrencyPolicy
		if newPolicy == "" {
			newPolicy = database.ConcurrencyPolicyQueue
		}
		if newPolicy != fn.ConcurrencyPolicy {
			fn.ConcurrencyPolicy = newPolicy
			spawnConfigChanged = true
		}
	}
	if req.AuthMode != nil {
		newMode := *req.AuthMode
		if newMode == "" {
			newMode = database.AuthModeNone
		}
		// auth_mode does not affect the spawn config — it gates *requests*,
		// not the sandbox process — so no pool drain is required when it
		// flips. Updating the field is enough; the invoke handler reads it
		// fresh from the registry on every call.
		fn.AuthMode = newMode
	}
	if req.RateLimitPerMin != nil {
		fn.RateLimitPerMin = *req.RateLimitPerMin
	}
	if req.Status != nil {
		fn.Status = *req.Status
	}
	fn.Version++

	if err := h.Registry.Set(fn); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to update function", reqID)
		return
	}

	// Drain warm workers so the next invoke picks up the new spawn config.
	if spawnConfigChanged && h.PoolRefresh != nil {
		h.PoolRefresh(fn.ID)
	}

	respond.JSON(w, http.StatusOK, fn)
}

// Delete handles DELETE /api/v1/functions/{fn_id}.
func (h *FunctionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rawID := extractPathParam(r.URL.Path, "/api/v1/functions/")
	if rawID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}
	fnID, ok := h.resolveFnID(rawID)
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	if err := h.Registry.Delete(fnID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to delete function", reqID)
		return
	}
	if h.PoolDrain != nil {
		h.PoolDrain(fnID)
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": fnID})
}

// GetSource handles GET /api/v1/functions/{fn_id}/source.
// Returns the deployed source code and dependency file content so the UI
// can pre-populate the editor with the actual code rather than a template.
func (h *FunctionHandler) GetSource(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rawID := r.PathValue("fn_id")
	if rawID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}
	fnID, ok := h.resolveFnID(rawID)
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	// Don't gate on fn.Status: a function may sit in 'error' (last build
	// failed) or 'created' (never deployed) yet still have a working
	// 'current' symlink from a previous successful deploy. The right
	// signal is "is the file readable" — handled below. Gating here
	// caused the editor to fall back to boilerplate for any function not
	// strictly 'active', even when its source was sitting on disk.

	// Mini-git layout: the live code is at functions/<id>/current/, a
	// symlink retargeted on each successful deploy / rollback. The old
	// flat "code/" directory was removed in Round G.
	codeDir := h.DataDir + "/functions/" + fn.ID + "/current"

	// Read handler source.
	entrypoint := fn.Entrypoint
	if entrypoint == "" {
		switch {
		case runtimeIsNode(fn.Runtime):
			entrypoint = "handler.js"
		case runtimeIsPython(fn.Runtime):
			entrypoint = "handler.py"
		}
	}
	src, err := os.ReadFile(codeDir + "/" + entrypoint)
	if err != nil {
		// Code dir doesn't exist yet (not deployed).
		respond.JSON(w, http.StatusOK, map[string]string{"code": "", "dependencies": ""})
		return
	}

	// Read deps file if present.
	deps := ""
	var depsFile string
	switch {
	case runtimeIsNode(fn.Runtime):
		depsFile = codeDir + "/package.json"
	case runtimeIsPython(fn.Runtime):
		depsFile = codeDir + "/requirements.txt"
	}
	if depsFile != "" {
		if d, err := os.ReadFile(depsFile); err == nil {
			deps = string(d)
		}
	}

	respond.JSON(w, http.StatusOK, map[string]string{
		"code":         string(src),
		"dependencies": deps,
	})
}

// Deploy handles POST /api/v1/functions/{fn_id}/deploy.
// Accepts a multipart-form upload with the code tarball under the "code"
// field. Returns 202 Accepted + {deployment_id, status: "queued"} — build
// runs asynchronously; client polls /deployments/{id}.
func (h *FunctionHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	rawID := extractDeployFnID(r.URL.Path)
	if rawID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}
	fnID, ok := h.resolveFnID(rawID)
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "expected multipart form with code archive", reqID)
		return
	}
	file, _, err := r.FormFile("code")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing 'code' file in form", reqID)
		return
	}
	defer file.Close()

	tarballPath, err := h.stashUpload(file, "orva-deploy-*.tar.gz")
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}

	h.enqueueOrBuildSync(w, r, fn, tarballPath, reqID)
}

// DeployInline handles POST /api/v1/functions/{fn_id}/deploy-inline.
// Accepts JSON {"code": "...", "filename": "handler.js"} — no file upload needed.
func (h *FunctionHandler) DeployInline(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rawID := r.PathValue("fn_id")
	if rawID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}
	fnID, ok := h.resolveFnID(rawID)
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	var req struct {
		Code         string `json:"code"`
		Filename     string `json:"filename"`
		Dependencies string `json:"dependencies"` // requirements.txt or package.json content
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if req.Code == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "code is required", reqID)
		return
	}
	if req.Filename == "" {
		switch {
		case runtimeIsNode(fn.Runtime):
			req.Filename = "handler.js"
		case runtimeIsPython(fn.Runtime):
			req.Filename = "handler.py"
		default:
			req.Filename = fn.Entrypoint
		}
	}

	// Determine the deps filename for this runtime.
	depsFilename := ""
	if req.Dependencies != "" {
		switch {
		case runtimeIsNode(fn.Runtime):
			depsFilename = "package.json"
		case runtimeIsPython(fn.Runtime):
			depsFilename = "requirements.txt"
		}
	}

	// Build a tarball in /tmp so the Queue worker can consume it later.
	tmpFile, err := createTempFile("orva-inline-*.tar.gz")
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create temp file", reqID)
		return
	}
	gw := gzip.NewWriter(tmpFile)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: req.Filename, Size: int64(len(req.Code)), Mode: 0644})
	tw.Write([]byte(req.Code))
	if depsFilename != "" {
		tw.WriteHeader(&tar.Header{Name: depsFilename, Size: int64(len(req.Dependencies)), Mode: 0644})
		tw.Write([]byte(req.Dependencies))
	}
	tw.Close()
	gw.Close()
	tmpFile.Close()

	h.enqueueOrBuildSync(w, r, fn, tmpFile.Name(), reqID)
}

// enqueueOrBuildSync submits the build job. If BuildQueue is wired, returns
// 202 + deployment_id immediately; otherwise falls back to synchronous
// build (legacy path for tests). The tarball at tarballPath is consumed by
// the Queue and removed when the build completes.
func (h *FunctionHandler) enqueueOrBuildSync(w http.ResponseWriter, r *http.Request, fn *database.Function, tarballPath, reqID string) {
	// Always insert a deployments row so clients can observe state.
	depSuffix, err := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 16)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to generate deployment id", reqID)
		removeTempFile(tarballPath)
		return
	}
	deploymentID := "dep_" + depSuffix
	dep := &database.Deployment{
		ID:         deploymentID,
		FunctionID: fn.ID,
		Version:    int64(fn.Version + 1),
		Status:     "queued",
		Phase:      "queued",
	}
	if err := h.DB.InsertDeployment(dep); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to record deployment", reqID)
		removeTempFile(tarballPath)
		return
	}

	// Async path: enqueue and return 202.
	if h.BuildQueue != nil {
		err := h.BuildQueue.Submit(builder.BuildJob{
			DeploymentID: deploymentID,
			FunctionID:   fn.ID,
			TarballPath:  tarballPath,
			SubmittedAt:  time.Now(),
			OnComplete: func(fnID string, success bool) {
				removeTempFile(tarballPath)
				if success && h.PoolRefresh != nil {
					h.PoolRefresh(fnID)
				}
				h.Metrics.RecordBuild(!success)
			},
		})
		if err != nil {
			depth := 0
			if h.BuildQueue != nil {
				depth = h.BuildQueue.QueuedDepth()
			}
			_ = h.DB.FinishDeployment(deploymentID, "failed", err.Error(), 0)
			removeTempFile(tarballPath)
			status, opts := deployError(err, reqID, depth)
			respond.ErrorWithDetail(w, status, opts)
			return
		}
		w.Header().Set("Location", "/api/v1/deployments/"+deploymentID)
		respond.JSON(w, http.StatusAccepted, map[string]any{
			"deployment_id": deploymentID,
			"status":        "queued",
			"function_id":   fn.ID,
		})
		return
	}

	// Fallback synchronous path — used by tests and any env without the
	// Queue wired. Same observable shape as before Phase 9 landed.
	defer removeTempFile(tarballPath)
	// Status flips during the synchronous build path are silent for the
	// same reason as the queue path — covered by deployment.* events.
	fn.Status = "building"
	h.Registry.SetSilent(fn)
	result, buildErr := h.Builder.Build(r.Context(), fn, tarballPath)
	h.Metrics.RecordBuild(buildErr != nil)
	if buildErr != nil {
		fn.Status = "error"
		h.Registry.SetSilent(fn)
		_ = h.DB.FinishDeployment(deploymentID, "failed", buildErr.Error(), 0)
		respond.Error(w, http.StatusInternalServerError, "BUILD_ERROR", "build failed: "+buildErr.Error(), reqID)
		return
	}
	fn.Image = result.ImageTag
	fn.ImageSize = result.ImageSize
	fn.CodeHash = result.CodeHash
	fn.Status = "active"
	fn.Version++
	h.Registry.SetSilent(fn)
	// See queue.go for rationale — capture the function's full state so
	// rollback restores env + spawn config alongside the code.
	_ = h.DB.SetDeploymentSnapshot(deploymentID, database.SnapshotFromFunction(fn))
	_ = h.DB.FinishDeployment(deploymentID, "succeeded", "", result.Duration.Milliseconds())
	if h.PoolRefresh != nil {
		h.PoolRefresh(fn.ID)
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"status":        "deployed",
		"deployment_id": deploymentID,
		"code_hash":     result.CodeHash,
		"duration":      result.Duration.String(),
		"function":      fn,
	})
}

// stashUpload saves the request body (a tar.gz upload) to a temp file and
// returns its path. Caller is responsible for eventually removing the file.
func (h *FunctionHandler) stashUpload(r io.Reader, pattern string) (string, error) {
	tmp, err := createTempFile(pattern)
	if err != nil {
		return "", fmt.Errorf("tempfile: %w", err)
	}
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("save upload: %w", err)
	}
	tmp.Close()
	return tmp.Name(), nil
}

// extractPathParam extracts the first path segment after the given prefix.
// For example, extractPathParam("/api/v1/functions/fn_abc/deploy", "/api/v1/functions/") returns "fn_abc".
func extractPathParam(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(remainder, "/"); idx >= 0 {
		return remainder[:idx]
	}
	return remainder
}

// extractDeployFnID extracts the function ID from a deploy path like
// /api/v1/functions/{fn_id}/deploy.
func extractDeployFnID(path string) string {
	const prefix = "/api/v1/functions/"
	const suffix = "/deploy"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	mid := strings.TrimPrefix(path, prefix)
	mid = strings.TrimSuffix(mid, suffix)
	if mid == "" || strings.Contains(mid, "/") {
		return ""
	}
	return mid
}

// rollbackRequest is the body for POST /api/v1/functions/{fn_id}/rollback.
// Either DeploymentID or CodeHash is sufficient; if both are present the
// deployment_id wins (it disambiguates same-hash deploys).
type rollbackRequest struct {
	DeploymentID string `json:"deployment_id"`
	CodeHash     string `json:"code_hash"`
}

// Rollback retargets the function's `current` symlink to a prior version.
// It does NOT go through the build queue: there's no tarball to extract,
// no deps to install — only a symlink retarget + DB write + pool drain.
// Should complete in <50 ms. The per-fn mutex (shared with the queue)
// serializes this against any deploy in flight.
func (h *FunctionHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rawID := r.PathValue("fn_id")
	if rawID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}
	fnID, ok := h.resolveFnID(rawID)
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	var req rollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if req.DeploymentID == "" && req.CodeHash == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "deployment_id or code_hash is required", reqID)
		return
	}

	// Serialize against deploys on the same fn.
	if h.FnLock != nil {
		lk := h.FnLock(fnID)
		lk.Lock()
		defer lk.Unlock()
	}

	started := time.Now()

	// Resolve target hash + the snapshot that was active when that hash
	// last shipped. Two callsites: rollback-by-deployment-id (canonical)
	// and rollback-by-code-hash (looks up the most recent succeeded
	// deployment with that hash so we can still restore env + settings).
	var (
		targetHash       = req.CodeHash
		parentDeployment *string
		targetSnapshot   *database.DeploymentSnapshot
	)
	if req.DeploymentID != "" {
		dep, err := h.DB.GetDeployment(req.DeploymentID)
		if err != nil {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "deployment not found", reqID)
			return
		}
		if dep.FunctionID != fnID {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "deployment belongs to a different function", reqID)
			return
		}
		if dep.Status != "succeeded" {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "cannot rollback to a non-succeeded deployment", reqID)
			return
		}
		targetHash = dep.CodeHash
		parent := dep.ID
		parentDeployment = &parent
		targetSnapshot = dep.Snapshot
	}
	if targetHash == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "target deployment has no recorded code_hash", reqID)
		return
	}
	// code_hash-only rollback: find the snapshot from the most recent
	// succeeded deployment that produced this hash. Best-effort — if no
	// deployment row carries a snapshot (e.g. legacy data), rollback
	// degrades to the old behaviour of "code only".
	if targetSnapshot == nil {
		if dep, err := h.DB.FindLatestSucceededByHash(fnID, targetHash); err == nil {
			targetSnapshot = dep.Snapshot
		}
	}

	// Refuse rollback to the version already serving — it's a no-op the
	// caller almost certainly didn't intend.
	if targetHash == fn.CodeHash {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "this version is already active", reqID)
		return
	}

	// Verify the version directory still exists (GC may have pruned it).
	versionDir := filepath.Join(h.DataDir, "functions", fnID, "versions", targetHash)
	if _, err := os.Stat(filepath.Join(versionDir, ".orva-ready")); err != nil {
		// Build the list of available hashes so the client can offer a
		// useful retry target.
		available := availableHashes(h.DataDir, fnID)
		respond.ErrorWithDetail(w, http.StatusGone, respond.ErrorOpts{
			Code:      "VERSION_GCD",
			Message:   fmt.Sprintf("version %s has been garbage-collected", short(targetHash)),
			RequestID: reqID,
			Hint:      "redeploy the original code, or rollback to one of the still-archived hashes",
			Details: map[string]any{
				"function_id":      fnID,
				"requested_hash":   targetHash,
				"available_hashes": available,
			},
		})
		return
	}

	// Insert the new deployment row up-front so the audit trail records
	// the attempt even if activation fails after this point.
	depID := "dep_" + nanoidShort()
	depRow := &database.Deployment{
		ID:                 depID,
		FunctionID:         fnID,
		Version:            int64(fn.Version) + 1,
		Status:             "queued",
		Phase:              "activate",
		CodeHash:           targetHash,
		Source:             "rollback",
		ParentDeploymentID: parentDeployment,
	}
	if err := h.DB.InsertDeployment(depRow); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to record rollback", reqID)
		return
	}

	// Atomic symlink retarget.
	if err := builder.ActivateVersion(h.DataDir, fnID, targetHash); err != nil {
		_ = h.DB.FinishDeployment(depID, "failed", err.Error(), time.Since(started).Milliseconds())
		slog.Error("rollback activate failed", "fn", fnID, "hash", targetHash, "err", err)
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to activate version", reqID)
		return
	}

	// Update function row: bump version, swap code_hash, mark active,
	// AND restore the snapshot (env vars + spawn config) that was active
	// when this code last shipped. Secrets are deliberately not touched —
	// they rotate independently and should always reflect current values.
	fn.Version++
	fn.CodeHash = targetHash
	fn.Status = "active"
	if targetSnapshot != nil {
		fn.EnvVars = targetSnapshot.EnvVars
		fn.MemoryMB = targetSnapshot.MemoryMB
		fn.CPUs = targetSnapshot.CPUs
		fn.TimeoutMS = targetSnapshot.TimeoutMS
		fn.NetworkMode = targetSnapshot.NetworkMode
		fn.AuthMode = targetSnapshot.AuthMode
		fn.RateLimitPerMin = targetSnapshot.RateLimitPerMin
		fn.MaxConcurrency = targetSnapshot.MaxConcurrency
		fn.ConcurrencyPolicy = targetSnapshot.ConcurrencyPolicy
	}
	if err := h.Registry.Set(fn); err != nil {
		// Symlink already retargeted — registry update is the only thing
		// that didn't stick. Mark the deployment failed so the operator
		// notices, but the function is still serving the rolled-back code.
		_ = h.DB.FinishDeployment(depID, "failed", err.Error(), time.Since(started).Milliseconds())
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "rollback applied but registry update failed", reqID)
		return
	}

	// Drain warm workers so the next invoke picks up the rolled-back code.
	if h.PoolRefresh != nil {
		h.PoolRefresh(fnID)
	}

	// Stamp the new rollback deployment with the same snapshot, so a future
	// rollback that targets *this* row (a rollback of a rollback) restores
	// the same env + spawn state.
	_ = h.DB.SetDeploymentSnapshot(depID, database.SnapshotFromFunction(fn))

	dur := time.Since(started).Milliseconds()
	_ = h.DB.FinishRollbackDeployment(depID, dur)
	slog.Info("rollback complete", "fn", fnID, "hash", targetHash[:12], "dur_ms", dur)

	dep, err := h.DB.GetDeployment(depID)
	if err != nil {
		// Shouldn't happen — we just inserted. Return the bare ID as a fallback.
		respond.JSON(w, http.StatusOK, map[string]any{"deployment_id": depID, "status": "succeeded", "code_hash": targetHash})
		return
	}
	respond.JSON(w, http.StatusOK, dep)
}

// availableHashes lists the version hashes still on disk for a function.
// Used to populate VERSION_GCD details so clients can retry against a
// surviving version.
func availableHashes(dataDir, fnID string) []string {
	entries, err := os.ReadDir(filepath.Join(dataDir, "functions", fnID, "versions"))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip in-progress scratch dirs (suffix ".tmp.<rand>").
		name := e.Name()
		if strings.Contains(name, ".tmp.") {
			continue
		}
		// Verify the readiness marker so we never advertise a half-built dir.
		if _, err := os.Stat(filepath.Join(dataDir, "functions", fnID, "versions", name, ".orva-ready")); err != nil {
			continue
		}
		out = append(out, name)
	}
	return out
}

// short returns the first 12 chars of a hash for human-readable error
// messages.
func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// nanoidShort returns a 16-char nanoid suitable for deployment IDs.
func nanoidShort() string {
	id, err := gonanoid.New(16)
	if err != nil {
		// Pathological. Fall back to time-based to avoid panicking the
		// handler — the caller will still see a unique-enough ID.
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return id
}
