package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// Diff handles GET /api/v1/functions/{fn_id}/diff?from=<dep_id>&to=<dep_id>&format=json|unified.
//
// Compares the handler source + dependency manifest of two successful
// past deployments. Output shape depends on `format`:
//
//	json (default — dashboard FunctionDiff view):
//	  { "from":  {deployment_id, version, code_hash, short_hash, submitted_at, snapshot},
//	    "to":    {…same…},
//	    "files": [ {path, kind:"handler"|"manifest", before, after, added, removed} ] }
//
//	unified (CLI `orva diff`):
//	  Content-Type: text/x-diff
//	  Body: concatenation of git-style "--- a/path\n+++ b/path\n@@ …" hunks
//	  per file in the same order. Empty body when byte-identical.
//
// The slim binary stays diff-library-free by consuming the pre-computed
// unified bytes; the dashboard receives raw before/after blobs so the
// @codemirror/merge MergeView owns its own highlighting.
func (h *FunctionHandler) Diff(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	rawID := r.PathValue("fn_id")
	if rawID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function ID", reqID)
		return
	}
	fnID, ok := h.resolveFnID(rawID)
	if !ok {
		respond.Error(w, http.StatusNotFound, "FUNCTION_NOT_FOUND", "function not found", reqID)
		return
	}
	fn, err := h.Registry.Get(fnID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "FUNCTION_NOT_FOUND", "function not found", reqID)
		return
	}

	q := r.URL.Query()
	fromID := strings.TrimSpace(q.Get("from"))
	toID := strings.TrimSpace(q.Get("to"))
	if fromID == "" || toID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "both `from` and `to` query parameters are required", reqID)
		return
	}
	if fromID == toID {
		respond.ErrorWithDetail(w, http.StatusBadRequest, respond.ErrorOpts{
			Code: "VALIDATION", Message: "from and to must be different deployments",
			RequestID: reqID,
			Details:   map[string]any{"deployment_id": fromID},
		})
		return
	}
	format := strings.ToLower(strings.TrimSpace(q.Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "unified" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "format must be `json` or `unified`", reqID)
		return
	}

	fromDep, err := loadDiffDeployment(h.DB, fromID, fnID, reqID, w)
	if err != nil {
		return // response already written
	}
	toDep, err := loadDiffDeployment(h.DB, toID, fnID, reqID, w)
	if err != nil {
		return
	}

	// GC guard: either version's source tree may have been pruned.
	missing := make([]string, 0, 2)
	for _, d := range []*database.Deployment{fromDep, toDep} {
		versionDir := filepath.Join(h.DataDir, "functions", fnID, "versions", d.CodeHash)
		if _, err := os.Stat(filepath.Join(versionDir, ".orva-ready")); err != nil {
			missing = append(missing, d.CodeHash)
		}
	}
	if len(missing) > 0 {
		respond.ErrorWithDetail(w, http.StatusGone, respond.ErrorOpts{
			Code:      "VERSION_GCD",
			Message:   "one or both versions have been garbage-collected",
			RequestID: reqID,
			Hint:      "redeploy the original code, or pick a still-archived version from `details.available_hashes`",
			Details: map[string]any{
				"function_id":      fnID,
				"missing":          missing,
				"available_hashes": availableHashes(h.DataDir, fnID),
			},
		})
		return
	}

	// Load + diff every candidate file. A file that's absent from BOTH
	// sides is silently skipped; absent from ONE side flags as added/removed.
	files := make([]diffFile, 0, 2)
	for _, c := range candidateFiles(fn) {
		before, beforeFound, err := loadVersionFile(h.DataDir, fnID, fromDep.CodeHash, c.path)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to read version file: "+err.Error(), reqID)
			return
		}
		after, afterFound, err := loadVersionFile(h.DataDir, fnID, toDep.CodeHash, c.path)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to read version file: "+err.Error(), reqID)
			return
		}
		if !beforeFound && !afterFound {
			continue
		}
		files = append(files, diffFile{
			Path:    c.path,
			Kind:    c.kind,
			Before:  before,
			After:   after,
			Added:   !beforeFound && afterFound,
			Removed: beforeFound && !afterFound,
		})
	}

	if format == "unified" {
		var sb strings.Builder
		for _, f := range files {
			if f.Before == f.After {
				continue
			}
			sb.WriteString(unifiedDiff(f.Path, f.Before, f.After))
		}
		w.Header().Set("Content-Type", "text/x-diff; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sb.String()))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"from":  summariseDeployment(fromDep),
		"to":    summariseDeployment(toDep),
		"files": files,
	})
}

// loadDiffDeployment fetches a deployment row by id and validates it
// belongs to the supplied function and is in `succeeded` status. On
// failure it writes the error response and returns a non-nil error so
// the caller can short-circuit.
func loadDiffDeployment(db *database.Database, depID, fnID, reqID string, w http.ResponseWriter) (*database.Deployment, error) {
	dep, err := db.GetDeployment(depID)
	if err != nil {
		respond.ErrorWithDetail(w, http.StatusNotFound, respond.ErrorOpts{
			Code: "VERSION_NOT_FOUND", Message: "deployment not found",
			RequestID: reqID,
			Details:   map[string]any{"requested_deployment_id": depID},
		})
		return nil, err
	}
	if dep.FunctionID != fnID {
		respond.ErrorWithDetail(w, http.StatusBadRequest, respond.ErrorOpts{
			Code: "VALIDATION", Message: "deployment belongs to a different function",
			RequestID: reqID,
			Details: map[string]any{
				"deployment_id": depID, "deployment_function_id": dep.FunctionID, "function_id": fnID,
			},
		})
		return nil, errors.New("wrong function")
	}
	if dep.Status != "succeeded" {
		respond.ErrorWithDetail(w, http.StatusBadRequest, respond.ErrorOpts{
			Code: "VALIDATION", Message: "can only diff succeeded deployments",
			RequestID: reqID,
			Details:   map[string]any{"deployment_id": depID, "status": dep.Status},
		})
		return nil, errors.New("not succeeded")
	}
	if dep.CodeHash == "" {
		respond.ErrorWithDetail(w, http.StatusBadRequest, respond.ErrorOpts{
			Code: "VALIDATION", Message: "deployment has no code hash recorded",
			RequestID: reqID,
			Details:   map[string]any{"deployment_id": depID},
		})
		return nil, errors.New("no code hash")
	}
	return dep, nil
}

// diffFile is one entry in the JSON response's `files` array.
type diffFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"` // "handler" | "manifest"
	Before  string `json:"before"`
	After   string `json:"after"`
	Added   bool   `json:"added"`
	Removed bool   `json:"removed"`
}

// summariseDeployment builds the compact deployment-summary projection
// embedded in the json response. Mirrors the fields the dashboard
// already shows for a deployment row, plus the full snapshot so the
// MetadataPanel can call describeSnapshotDiff without a second request.
func summariseDeployment(d *database.Deployment) map[string]any {
	out := map[string]any{
		"deployment_id": d.ID,
		"version":       d.Version,
		"code_hash":     d.CodeHash,
		"short_hash":    short(d.CodeHash),
		"submitted_at":  d.SubmittedAt,
		"source":        d.Source,
	}
	if d.Snapshot != nil {
		out["snapshot"] = d.Snapshot
	}
	return out
}

type candidate struct {
	path string // relative to versions/<hash>/
	kind string // "handler" | "manifest"
}

// candidateFiles is the per-runtime list of files we attempt to diff.
// We skip dependency trees (node_modules, __pycache__, venv) — they bloat
// the diff with churn that only reflects install determinism.
//
// TypeScript heuristic: if fn.Entrypoint was rewritten by the deploy
// pipeline to "dist/handler.js" (post-tsc), fall back to "handler.ts" so
// the diff shows the source the operator actually edited, not the
// compiled output (which drifts on every build).
func candidateFiles(fn *database.Function) []candidate {
	switch {
	case runtimeIsNode(fn.Runtime):
		ep := fn.Entrypoint
		if strings.HasPrefix(ep, "dist/") {
			ep = "handler.ts"
		}
		if ep == "" {
			ep = "handler.js"
		}
		return []candidate{{ep, "handler"}, {"package.json", "manifest"}}
	case runtimeIsPython(fn.Runtime):
		ep := fn.Entrypoint
		if ep == "" {
			ep = "handler.py"
		}
		return []candidate{{ep, "handler"}, {"requirements.txt", "manifest"}}
	default:
		return nil
	}
}

// loadVersionFile reads one file from an on-disk version tree. A
// not-exist returns (found=false, err=nil) so the caller can mark the
// file as added/removed depending on which side it lives on. Other I/O
// errors propagate.
//
// `relPath` is filepath.Clean-guarded against "../" escape — defensive
// against future call sites that might pass operator input here. Today
// only candidateFiles supplies the path and it's always safe.
func loadVersionFile(dataDir, fnID, codeHash, relPath string) (content string, found bool, err error) {
	versionDir := filepath.Join(dataDir, "functions", fnID, "versions", codeHash)
	clean := filepath.Clean("/" + relPath) // anchor to "/" so ".." collapses
	full := filepath.Join(versionDir, clean)
	if !strings.HasPrefix(full, versionDir+string(os.PathSeparator)) && full != versionDir {
		return "", false, fmt.Errorf("path escapes version dir: %q", relPath)
	}
	b, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// unifiedDiff produces git-compatible unified-diff bytes for one file
// using the myers algorithm. Context fixed at the gotextdiff default (3
// lines, matching git). Returns the empty string when before == after.
func unifiedDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	edits := myers.ComputeEdits(span.URIFromPath(path), before, after)
	u := gotextdiff.ToUnified("a/"+path, "b/"+path, before, edits)
	return fmt.Sprint(u)
}
