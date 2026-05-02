package builder

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultMaxCodeSize is 50MB.
	DefaultMaxCodeSize int64 = 50 * 1024 * 1024
)

// ValidateArchive checks the extracted code directory for common issues:
// - Entrypoint file must exist
// - Total size must be under the limit
// - No ELF binaries
// - No symlinks pointing outside the directory
// - No path traversal
//
// TypeScript redeploy note: when the tarball contains a tsconfig.json the
// validator looks up the *source* .ts file rather than the persisted
// post-compile entrypoint (e.g. dist/handler.js). This is required because
// after the first successful TS build the function row's entrypoint has
// already been rewritten to dist/handler.js (see queue.go FIX-2), but the
// user's tarball on the second deploy ships only handler.ts — the dist/
// directory is freshly populated by tsc during the build phase that runs
// AFTER validation. Without this rewrite the validator would refuse the
// archive and tsc would never get a chance to run.
//
// The lookup tries `<stem-of-entrypoint>.ts` first (so a row with
// entrypoint = "dist/handler.js" resolves to "handler.ts"), then falls back
// to "handler.ts", then to the first non-.d.ts file at the top level.
func ValidateArchive(dir string, runtime string, entrypoint string) error {
	// Resolve the directory to an absolute path.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}

	// Resolve the entrypoint we actually expect to find on disk. For TS
	// redeploys this swaps the persisted dist/handler.js for handler.ts;
	// for everything else it returns the entrypoint unchanged.
	checkEntry := resolveSourceEntrypoint(absDir, entrypoint)

	// Check entrypoint exists.
	entrypointPath := filepath.Join(absDir, checkEntry)
	if _, err := os.Stat(entrypointPath); os.IsNotExist(err) {
		return fmt.Errorf("entrypoint not found: %s", checkEntry)
	}

	var totalSize int64

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check for path traversal: the path must be within absDir.
		relPath, err := filepath.Rel(absDir, path)
		if err != nil {
			return fmt.Errorf("compute relative path: %w", err)
		}
		if strings.HasPrefix(relPath, "..") {
			return fmt.Errorf("path traversal detected: %s", relPath)
		}

		// Check symlinks.
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read symlink: %w", err)
			}
			// Resolve the symlink target relative to the directory containing the link.
			resolvedTarget := target
			if !filepath.IsAbs(target) {
				resolvedTarget = filepath.Join(filepath.Dir(path), target)
			}
			resolvedTarget, err = filepath.Abs(resolvedTarget)
			if err != nil {
				return fmt.Errorf("resolve symlink target: %w", err)
			}
			if !strings.HasPrefix(resolvedTarget, absDir) {
				return fmt.Errorf("symlink points outside directory: %s -> %s", relPath, target)
			}
		}

		if info.IsDir() {
			return nil
		}

		// Accumulate size.
		totalSize += info.Size()

		// Check for ELF binaries (magic bytes: 0x7f 'E' 'L' 'F').
		if info.Size() >= 4 && info.Mode().IsRegular() {
			if err := checkELF(path); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if totalSize > DefaultMaxCodeSize {
		return fmt.Errorf("code size %d exceeds limit %d", totalSize, DefaultMaxCodeSize)
	}

	return nil
}

// resolveSourceEntrypoint maps the persisted function-row entrypoint onto
// the actual source file present in the tarball when the archive contains a
// tsconfig.json. Returns the original entrypoint unchanged for non-TS
// projects, or when no plausible .ts source is found (in which case the
// caller's stat() will surface the original error).
func resolveSourceEntrypoint(absDir, entrypoint string) string {
	if _, err := os.Stat(filepath.Join(absDir, "tsconfig.json")); err != nil {
		return entrypoint
	}
	// If the persisted entrypoint already exists in the tarball (e.g. the
	// user uploaded a pre-compiled dist/) honour it.
	if _, err := os.Stat(filepath.Join(absDir, entrypoint)); err == nil {
		return entrypoint
	}
	// Try `<basename-without-ext>.ts` derived from the persisted entrypoint
	// so a row stamped with "dist/handler.js" resolves to "handler.ts".
	base := filepath.Base(entrypoint)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem != "" {
		candidate := stem + ".ts"
		if _, err := os.Stat(filepath.Join(absDir, candidate)); err == nil {
			return candidate
		}
	}
	// Canonical Orva starter name.
	if _, err := os.Stat(filepath.Join(absDir, "handler.ts")); err == nil {
		return "handler.ts"
	}
	// Last resort: first non-.d.ts at the top level.
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return entrypoint
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".d.ts") {
			continue
		}
		if strings.HasSuffix(name, ".ts") {
			return name
		}
	}
	return entrypoint
}

// checkELF opens a file and checks if it starts with the ELF magic bytes.
func checkELF(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return nil // skip files we can't read
	}
	defer f.Close()

	var magic [4]byte
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		return nil // too small or unreadable
	}

	if magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F' {
		rel, _ := filepath.Rel(filepath.Dir(path), path)
		return fmt.Errorf("ELF binary detected: %s", rel)
	}

	return nil
}
