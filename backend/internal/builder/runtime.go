package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
)

type Option func(*Builder)

// RuntimeRequest contains only captured build identity, never policy or grants.
type RuntimeRequest struct {
	FunctionID string
	Language   sandbox.Language
}

// RuntimeBinding supplies host-owned runtime and optional epoch placement.
// The domain owner retains descendant fencing responsibility; Build never Fences it.
type RuntimeBinding struct {
	Runtime sandbox.BuildRuntime
	Domain  *workloadfence.Domain
}

type RuntimeProvider func(context.Context, RuntimeRequest) (RuntimeBinding, error)

// WithRuntimeBinding opts into fail-closed capture, even when provider is nil.
// Inputs must be exclusively owned while Build captures them.
func WithRuntimeBinding(provider RuntimeProvider) Option {
	return func(b *Builder) {
		b.runtimeBindingEnabled, b.runtimeProvider = true, provider
	}
}

func (b *Builder) captureRuntime(ctx context.Context, fn *database.Function) (*artifactfs.Lease, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if b.runtimeProvider == nil || (!isNodeRuntime(fn.Runtime) && !isPythonRuntime(fn.Runtime)) {
		return nil, "", fmt.Errorf("build runtime provider/language: %w", artifactfs.ErrInvalid)
	}
	binding, err := b.runtimeProvider(ctx, RuntimeRequest{FunctionID: fn.ID, Language: sandbox.Language(fn.Runtime)})
	if err != nil {
		return nil, "", fmt.Errorf("capture build runtime: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if binding.Runtime.Store == nil || binding.Runtime.Generation == "" {
		return nil, "", fmt.Errorf("build runtime binding: %w", artifactfs.ErrInvalid)
	}
	lease, err := binding.Runtime.Store.Acquire(binding.Runtime.Generation)
	if err != nil {
		return nil, "", fmt.Errorf("anchor build runtime: %w", err)
	}
	id := lease.Identity()
	if id.Kind != artifactfs.KindRuntime || id.Owner != fn.Runtime {
		lease.Release()
		return nil, "", fmt.Errorf("build runtime kind/language: %w", artifactfs.ErrInvalid)
	}
	abi, err := pinnedRuntimeABI(lease.Path(), fn.Runtime)
	if err != nil {
		lease.Release()
		return nil, "", err
	}
	b.buildRuntime, b.buildDomain = &binding.Runtime, binding.Domain
	return lease, abi, nil
}

func pinnedRuntimeABI(path, language string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", fmt.Errorf("open pinned runtime: %w", err)
	}
	defer root.Close()
	f, err := root.Open(RuntimeStampFile)
	if os.IsNotExist(err) {
		return language + " unstamped", nil
	}
	if err != nil {
		return "", fmt.Errorf("open pinned interpreter stamp: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		return "", fmt.Errorf("invalid pinned interpreter stamp: %w", artifactfs.ErrInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(raw) > 1024 {
		return "", fmt.Errorf("read pinned interpreter stamp: %w", artifactfs.ErrInvalid)
	}
	if abi := strings.TrimSpace(string(raw)); abi != "" {
		return abi, nil
	}
	return language + " unstamped", nil
}
