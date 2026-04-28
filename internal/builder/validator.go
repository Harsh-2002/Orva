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
func ValidateArchive(dir string, runtime string, entrypoint string) error {
	// Resolve the directory to an absolute path.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}

	// Check entrypoint exists.
	entrypointPath := filepath.Join(absDir, entrypoint)
	if _, err := os.Stat(entrypointPath); os.IsNotExist(err) {
		return fmt.Errorf("entrypoint not found: %s", entrypoint)
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
