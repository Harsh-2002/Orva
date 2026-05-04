package mcp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── deploy_function_inline ────────────────────────────────────────

type DeployInlineInput struct {
	FunctionID   string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
	Code         string `json:"code" jsonschema:"full source code for the handler file (handler.py or handler.js)"`
	Filename     string `json:"filename,omitempty" jsonschema:"override the entrypoint filename — defaults to handler.{py|js}"`
	Dependencies string `json:"dependencies,omitempty" jsonschema:"contents of requirements.txt (Python) or package.json (Node) — leave empty if no third-party deps"`
	Wait         bool   `json:"wait" jsonschema:"REQUIRED — true blocks until the build terminates (succeeded or failed) and the response carries the final state; false returns immediately with status='queued' and the agent must poll get_deployment / wait_deployment. Chat agents that intend to invoke right after should pass true; long-running agents that fan out parallel deploys may pass false. No silent default — racing invoke against a still-queued build is the bug we're avoiding."`
}

type DeployInlineOutput struct {
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	FunctionID   string `json:"function_id"`
	CodeHash     string `json:"code_hash,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	Phase        string `json:"phase,omitempty"`
	Error        string `json:"error,omitempty"`
	// Warning is non-empty when the platform detected something likely
	// wrong with this deploy that the agent should know before invoking.
	// Currently emitted when the source imports the orva SDK but the
	// function's network_mode is "none" (every SDK call would fail at
	// runtime with ENETUNREACH).
	Warning string `json:"warning,omitempty" jsonschema:"non-fatal advisory about likely runtime issues — read this before invoking"`
}

// ─── rollback_function ─────────────────────────────────────────────

type RollbackFunctionInput struct {
	FunctionID   string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"target deployment id (preferred — restores the env+config snapshot from that deployment)"`
	CodeHash     string `json:"code_hash,omitempty" jsonschema:"alternative to deployment_id — content-addressed hash of a prior version"`
	Confirm      bool   `json:"confirm" jsonschema:"must be true — rollback changes what's serving traffic"`
}

type RollbackFunctionOutput struct {
	DeploymentID string `json:"deployment_id"`
	CodeHash     string `json:"code_hash"`
	DurationMS   int64  `json:"duration_ms"`
}

// ─── get_deployment ────────────────────────────────────────────────

type GetDeploymentInput struct {
	DeploymentID string `json:"deployment_id"`
}

type DeploymentView struct {
	ID          string     `json:"id"`
	FunctionID  string     `json:"function_id"`
	Version     int64      `json:"version"`
	Status      string     `json:"status"`
	Phase       string     `json:"phase"`
	CodeHash    string     `json:"code_hash,omitempty"`
	Source      string     `json:"source,omitempty"`
	DurationMS  int64      `json:"duration_ms,omitempty"`
	Error       string     `json:"error,omitempty"`
	SubmittedAt time.Time  `json:"submitted_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

func toDeploymentView(d *database.Deployment) DeploymentView {
	if d == nil {
		return DeploymentView{}
	}
	var dur int64
	if d.DurationMS != nil {
		dur = *d.DurationMS
	}
	return DeploymentView{
		ID: d.ID, FunctionID: d.FunctionID, Version: d.Version,
		Status: d.Status, Phase: d.Phase, CodeHash: d.CodeHash,
		Source: d.Source, DurationMS: dur, Error: d.ErrorMessage,
		SubmittedAt: d.SubmittedAt, FinishedAt: d.FinishedAt,
	}
}

// ─── get_deployment_logs ───────────────────────────────────────────

type GetDeploymentLogsInput struct {
	DeploymentID string `json:"deployment_id"`
	From         int64  `json:"from,omitempty" jsonschema:"return only lines with seq > from (default 0 = from start)"`
	Limit        int    `json:"limit,omitempty" jsonschema:"max lines to return, default 200, max 2000"`
}

type DeploymentLogLine struct {
	Seq    int64     `json:"seq"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

type GetDeploymentLogsOutput struct {
	Logs []DeploymentLogLine `json:"logs"`
}

// ─── list_function_deployments ─────────────────────────────────────

type ListFunctionDeploymentsInput struct {
	FunctionID string `json:"function_id"`
	Limit      int    `json:"limit,omitempty"`
}

type ListFunctionDeploymentsOutput struct {
	Deployments []DeploymentView `json:"deployments"`
}

// ─── wait_deployment ───────────────────────────────────────────────

type WaitDeploymentInput struct {
	DeploymentID   string `json:"deployment_id"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"max wait, default 120, max 600"`
}

// registerDeployTools wires the deploy / rollback / inspect tools.
func registerDeployTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_deployment",
				Description: "Fetch a single deployment by id. Returns status (queued/building/succeeded/failed), phase, code_hash, duration, and any error message. Use this to poll a deployment that's in flight.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetDeploymentInput) (*mcpsdk.CallToolResult, DeploymentView, error) {
				dep, err := deps.DB.GetDeployment(in.DeploymentID)
				if err != nil {
					return nil, DeploymentView{}, fmt.Errorf("deployment not found: %s", in.DeploymentID)
				}
				return nil, toDeploymentView(dep), nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_deployment_logs",
				Description: "Read the build log for a deployment. Each line has a monotonically-increasing seq — pass from=<last_seq> to tail incrementally. Empty list = no new lines. Use this for diagnosing build failures.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetDeploymentLogsInput) (*mcpsdk.CallToolResult, GetDeploymentLogsOutput, error) {
				lim := in.Limit
				if lim <= 0 {
					lim = 200
				}
				if lim > 2000 {
					lim = 2000
				}
				lines, err := deps.DB.GetBuildLogs(in.DeploymentID, in.From, lim)
				if err != nil {
					return nil, GetDeploymentLogsOutput{}, err
				}
				out := GetDeploymentLogsOutput{Logs: make([]DeploymentLogLine, 0, len(lines))}
				for _, ln := range lines {
					out.Logs = append(out.Logs, DeploymentLogLine{Seq: ln.Seq, Stream: ln.Stream, Text: ln.Line, At: ln.TS})
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_function_deployments",
				Description: "List a function's deployment history (newest first). Each entry has the deployment id, code_hash, status, and timestamps — use it to find a target for rollback_function.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListFunctionDeploymentsInput) (*mcpsdk.CallToolResult, ListFunctionDeploymentsOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, ListFunctionDeploymentsOutput{}, err
				}
				lim := in.Limit
				if lim <= 0 {
					lim = 50
				}
				deplist, err := deps.DB.ListDeploymentsForFunction(fn.ID, lim)
				if err != nil {
					return nil, ListFunctionDeploymentsOutput{}, err
				}
				out := ListFunctionDeploymentsOutput{Deployments: make([]DeploymentView, 0, len(deplist))}
				for _, d := range deplist {
					out.Deployments = append(out.Deployments, toDeploymentView(d))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name: "deploy_function_inline",
				Description: "Deploy source code to a function. Pass the full handler file as `code`, optional dependency-file content as `dependencies`. `wait` is REQUIRED — pass true to block until the build terminates (so invoke_function won't race a still-queued build), or false to return immediately with status='queued' and poll yourself. Returns deployment_id either way; carries a `warning` field when the source imports the orva SDK but the function's network_mode is 'none' (the SDK call would fail at runtime).",
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrFalse(),
					OpenWorldHint:   ptrFalse(),
				},
			},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in DeployInlineInput) (*mcpsdk.CallToolResult, DeployInlineOutput, error) {
				return deployInline(ctx, deps, in)
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "wait_deployment",
				Description: "Block until a deployment reaches a terminal state (succeeded or failed) or the timeout fires. Useful after deploy_function_inline with wait=false. Default timeout 120 s, max 600 s.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in WaitDeploymentInput) (*mcpsdk.CallToolResult, DeploymentView, error) {
				timeout := in.TimeoutSeconds
				if timeout <= 0 {
					timeout = 120
				}
				if timeout > 600 {
					timeout = 600
				}
				return nil, waitDeployment(ctx, deps, in.DeploymentID, time.Duration(timeout)*time.Second), nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "rollback_function",
				Description: "Roll a function back to a prior succeeded deployment. Pass deployment_id (preferred — restores the env_vars + spawn config snapshot from that deploy) or code_hash. Pass confirm=true. Secrets are NOT versioned and remain at current values.",
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrTrue(),
					OpenWorldHint:   ptrFalse(),
				},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in RollbackFunctionInput) (*mcpsdk.CallToolResult, RollbackFunctionOutput, error) {
				if !in.Confirm {
					return nil, RollbackFunctionOutput{}, errors.New("rollback refused: pass confirm=true")
				}
				if in.DeploymentID == "" && in.CodeHash == "" {
					return nil, RollbackFunctionOutput{}, errors.New("either deployment_id or code_hash is required")
				}
				return rollbackFunction(deps, in)
			},
		)
	})
}

// ─── implementations ───────────────────────────────────────────────

// deployInline builds an inline-source tarball, inserts a deployment row,
// and submits to the build queue. With wait=true it then blocks until the
// build terminates and returns the final state.
func deployInline(ctx context.Context, deps Deps, in DeployInlineInput) (*mcpsdk.CallToolResult, DeployInlineOutput, error) {
	if in.Code == "" {
		return nil, DeployInlineOutput{}, errors.New("code is required")
	}
	fn, err := resolveFunction(deps, in.FunctionID)
	if err != nil {
		return nil, DeployInlineOutput{}, err
	}

	filename := in.Filename
	if filename == "" {
		switch {
		case runtimeIsNode(fn.Runtime):
			filename = "handler.js"
		case runtimeIsPython(fn.Runtime):
			filename = "handler.py"
		default:
			filename = fn.Entrypoint
		}
	}
	depsFilename := ""
	if in.Dependencies != "" {
		switch {
		case runtimeIsNode(fn.Runtime):
			depsFilename = "package.json"
		case runtimeIsPython(fn.Runtime):
			depsFilename = "requirements.txt"
		}
	}

	tarPath, err := writeInlineTarball(filename, []byte(in.Code), depsFilename, []byte(in.Dependencies))
	if err != nil {
		return nil, DeployInlineOutput{}, fmt.Errorf("failed to stage tarball: %w", err)
	}

	deploymentID := ids.New()
	dep := &database.Deployment{
		ID: deploymentID, FunctionID: fn.ID, Version: int64(fn.Version + 1),
		Status: "queued", Phase: "queued",
	}
	if err := deps.DB.InsertDeployment(dep); err != nil {
		_ = os.Remove(tarPath)
		return nil, DeployInlineOutput{}, fmt.Errorf("failed to record deployment: %w", err)
	}

	if deps.BuildQueue == nil {
		_ = os.Remove(tarPath)
		_ = deps.DB.FinishDeployment(deploymentID, "failed", "build queue not configured", 0)
		return nil, DeployInlineOutput{}, errors.New("build queue not configured on this Orva instance")
	}

	err = deps.BuildQueue.Submit(builder.BuildJob{
		DeploymentID: deploymentID,
		FunctionID:   fn.ID,
		TarballPath:  tarPath,
		SubmittedAt:  time.Now(),
		OnComplete: func(fnID string, success bool) {
			_ = os.Remove(tarPath)
			if success && deps.PoolMgr != nil {
				deps.PoolMgr.RefreshForDeploy(fnID)
			}
			if deps.Metrics != nil {
				deps.Metrics.RecordBuild(!success)
			}
		},
	})
	if err != nil {
		_ = os.Remove(tarPath)
		_ = deps.DB.FinishDeployment(deploymentID, "failed", err.Error(), 0)
		return nil, DeployInlineOutput{}, fmt.Errorf("build queue submit failed: %w", err)
	}

	out := DeployInlineOutput{
		DeploymentID: deploymentID,
		Status:       "queued",
		FunctionID:   fn.ID,
	}
	if fn.NetworkMode == database.NetworkModeNone && builder.SourceUsesOrvaSDK(in.Code) {
		out.Warning = builder.SDKNoneWarning
	}

	if !in.Wait {
		return nil, out, nil
	}

	// wait=true → block until terminal. Use the same wait helper so we
	// share the polling/streaming logic.
	final := waitDeployment(ctx, deps, deploymentID, 120*time.Second)
	out.Status = final.Status
	out.Phase = final.Phase
	out.CodeHash = final.CodeHash
	out.DurationMS = final.DurationMS
	out.Error = final.Error
	return nil, out, nil
}

// waitDeployment polls the DB at 500ms cadence (mirroring the existing
// /deployments/{id}/stream HTTP endpoint), returning as soon as the
// deployment is in a terminal state — or once timeout elapses (in
// which case it returns the last-seen state with status="timeout").
func waitDeployment(ctx context.Context, deps Deps, deploymentID string, timeout time.Duration) DeploymentView {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		dep, err := deps.DB.GetDeployment(deploymentID)
		if err == nil && (dep.Status == "succeeded" || dep.Status == "failed") {
			return toDeploymentView(dep)
		}
		if time.Now().After(deadline) {
			view := DeploymentView{ID: deploymentID, Status: "timeout"}
			if err == nil {
				view = toDeploymentView(dep)
				view.Status = "timeout"
			}
			return view
		}
		select {
		case <-ctx.Done():
			return DeploymentView{ID: deploymentID, Status: "cancelled"}
		case <-tick.C:
		}
	}
}

// rollbackFunction mirrors FunctionHandler.Rollback: resolve target hash,
// verify version dir exists, retarget current symlink, restore snapshot
// state, drain pool. Differs only in that we return the deployment view
// directly instead of writing JSON to a ResponseWriter.
func rollbackFunction(deps Deps, in RollbackFunctionInput) (*mcpsdk.CallToolResult, RollbackFunctionOutput, error) {
	fn, err := resolveFunction(deps, in.FunctionID)
	if err != nil {
		return nil, RollbackFunctionOutput{}, err
	}

	lk := fnLockGuard(deps, fn.ID)
	lk.Lock()
	defer lk.Unlock()

	started := time.Now()

	var (
		targetHash       = in.CodeHash
		parentDeployment *string
		targetSnapshot   *database.DeploymentSnapshot
	)
	if in.DeploymentID != "" {
		dep, err := deps.DB.GetDeployment(in.DeploymentID)
		if err != nil {
			return nil, RollbackFunctionOutput{}, fmt.Errorf("deployment not found: %s", in.DeploymentID)
		}
		if dep.FunctionID != fn.ID {
			return nil, RollbackFunctionOutput{}, errors.New("deployment belongs to a different function")
		}
		if dep.Status != "succeeded" {
			return nil, RollbackFunctionOutput{}, errors.New("cannot roll back to a non-succeeded deployment")
		}
		targetHash = dep.CodeHash
		parent := dep.ID
		parentDeployment = &parent
		targetSnapshot = dep.Snapshot
	}
	if targetHash == "" {
		return nil, RollbackFunctionOutput{}, errors.New("target deployment has no recorded code_hash")
	}
	if targetSnapshot == nil {
		if dep, err := deps.DB.FindLatestSucceededByHash(fn.ID, targetHash); err == nil {
			targetSnapshot = dep.Snapshot
		}
	}
	if targetHash == fn.CodeHash {
		return nil, RollbackFunctionOutput{}, errors.New("this version is already active")
	}

	versionDir := filepath.Join(deps.DataDir, "functions", fn.ID, "versions", targetHash)
	if _, err := os.Stat(filepath.Join(versionDir, ".orva-ready")); err != nil {
		return nil, RollbackFunctionOutput{}, fmt.Errorf("version %s has been garbage-collected", short12(targetHash))
	}

	depID := ids.New()
	depRow := &database.Deployment{
		ID: depID, FunctionID: fn.ID, Version: int64(fn.Version) + 1,
		Status: "queued", Phase: "activate", CodeHash: targetHash,
		Source: "rollback", ParentDeploymentID: parentDeployment,
	}
	if err := deps.DB.InsertDeployment(depRow); err != nil {
		return nil, RollbackFunctionOutput{}, err
	}

	if err := builder.ActivateVersion(deps.DataDir, fn.ID, targetHash); err != nil {
		_ = deps.DB.FinishDeployment(depID, "failed", err.Error(), time.Since(started).Milliseconds())
		return nil, RollbackFunctionOutput{}, fmt.Errorf("failed to activate version: %w", err)
	}

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
	if err := deps.Registry.Set(fn); err != nil {
		_ = deps.DB.FinishDeployment(depID, "failed", err.Error(), time.Since(started).Milliseconds())
		return nil, RollbackFunctionOutput{}, fmt.Errorf("rollback applied but registry update failed: %w", err)
	}

	if deps.PoolMgr != nil {
		deps.PoolMgr.RefreshForDeploy(fn.ID)
	}

	_ = deps.DB.SetDeploymentSnapshot(depID, database.SnapshotFromFunction(fn))
	dur := time.Since(started).Milliseconds()
	_ = deps.DB.FinishRollbackDeployment(depID, dur)

	return nil, RollbackFunctionOutput{
		DeploymentID: depID,
		CodeHash:     targetHash,
		DurationMS:   dur,
	}, nil
}

// writeInlineTarball writes (filename, content) and optionally
// (depsFilename, depsContent) into a gzip-compressed tar on disk.
// Returns the tarball path; caller is responsible for removing it
// (the build queue's OnComplete callback handles this).
func writeInlineTarball(filename string, code []byte, depsFilename string, depsContent []byte) (string, error) {
	f, err := os.CreateTemp("", "orva-mcp-inline-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: filename, Size: int64(len(code)), Mode: 0644}); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if _, err := tw.Write(code); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if depsFilename != "" {
		if err := tw.WriteHeader(&tar.Header{Name: depsFilename, Size: int64(len(depsContent)), Mode: 0644}); err != nil {
			_ = os.Remove(f.Name())
			return "", err
		}
		if _, err := tw.Write(depsContent); err != nil {
			_ = os.Remove(f.Name())
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := gw.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// _ = json keeps the import alive even if some helpers below shift
// out — defensive against future refactor breakage.
var _ = json.RawMessage{}
