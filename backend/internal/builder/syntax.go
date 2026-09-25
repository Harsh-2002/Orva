package builder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

const pythonSyntaxScript = "import sys, tokenize; path = sys.argv[1]; source = tokenize.open(path).read(); compile(source, path, 'exec')"

func (b *Builder) checkSyntax(ctx context.Context, codeDir, runtime, entrypoint string) error {
	var language sandbox.Language
	var argv []string
	ext := strings.ToLower(filepath.Ext(entrypoint))
	path := sandbox.BuildCodeDir + "/" + filepath.ToSlash(entrypoint)
	switch {
	case isNodeRuntime(runtime) && ext == ".ts":
		if _, err := os.Stat(filepath.Join(codeDir, "tsconfig.json")); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: TypeScript requires tsconfig.json and a declared typescript dependency", entrypoint)
		} else if err != nil {
			return fmt.Errorf("read tsconfig.json: %w", err)
		}
		return nil // The existing jailed tsc step checks the whole TypeScript project.
	case isNodeRuntime(runtime) && (ext == ".js" || ext == ".mjs" || ext == ".cjs"):
		language = sandbox.Node
		argv = []string{"/usr/local/bin/node", "--check", path}
	case isPythonRuntime(runtime) && ext == ".py":
		language = sandbox.Python
		argv = []string{"/usr/local/bin/python3", "-I", "-c", pythonSyntaxScript, path}
	default:
		return nil
	}

	timeout := min(b.buildStepTimeout(), 30*time.Second)
	out, err := b.runBuildStep(ctx, buildStep{
		language: language,
		codeDir:  codeDir,
		stream:   "syntax",
		timeout:  timeout,
		argv:     argv,
	})
	if err != nil {
		if errors.Is(err, sandbox.ErrBuildTimedOut) {
			return fmt.Errorf("%s: check timed out after %s: %w", entrypoint, timeout, err)
		}
		return fmt.Errorf("%s: %w\n%s", entrypoint, err, strings.TrimSpace(string(out)))
	}
	return nil
}
