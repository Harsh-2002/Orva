// Package safepath contains the containment checks used wherever a
// user-supplied relative path is joined onto a server-owned directory.
//
// It exists because that check was previously written out by hand at some
// call sites and skipped at others. `entrypoint` is the clearest example:
// it is a plain string on the function record that operators can set to
// anything, and the source-read handler joined it onto the function's code
// directory by string concatenation, so "../../../.admin-key" resolved to
// the plaintext bootstrap admin key next to the database. Any read-and-write
// scoped key could escalate to admin with one PUT and one GET.
package safepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrEscapes is returned when a relative path resolves outside its base.
var ErrEscapes = errors.New("path escapes its base directory")

// Validate reports whether rel is acceptable as a stored relative path:
// non-empty, genuinely relative, and free of any traversal once cleaned.
//
// This is the write-side gate. It runs when a value is accepted, so a bad
// path never reaches storage in the first place — but callers must still
// use Join on the read side, because rows predating this check exist and
// because storage is not the only way a value arrives.
func Validate(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return errors.New("path must not be empty")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return errors.New("path must be relative, not absolute")
	}
	// Reject a Windows drive/UNC prefix explicitly: filepath.IsAbs is
	// platform-dependent and returns false for "C:\x" on Linux.
	if len(rel) >= 2 && rel[1] == ':' {
		return errors.New("path must not carry a drive letter")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("%w: %q", ErrEscapes, rel)
	}
	if clean == "." {
		return errors.New("path must name a file, not the directory itself")
	}
	return nil
}

// Join resolves rel against base and fails if the result would leave base.
//
// This is the read-side gate, and it is deliberately independent of
// Validate: a stored value that predates validation, or one that arrives by
// some path that skipped it, must still be contained here.
//
// Traversal is REJECTED, not silently collapsed. Anchoring rel to "/" first
// would also be safe — it cannot escape — but it would quietly rewrite
// "../../../.admin-key" into "<base>/.admin-key" and surface as a confusing
// "no such file" instead of naming the actual problem.
func Join(base, rel string) (string, error) {
	if base == "" {
		return "", errors.New("base directory must not be empty")
	}
	base = filepath.Clean(base)
	full := filepath.Join(base, filepath.FromSlash(rel))

	// filepath.Join cleans its result, so a prefix test is sufficient — but
	// compare against base + separator, or "/data/functions-evil" would pass
	// as being inside "/data/functions".
	if full != base && !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q", ErrEscapes, rel)
	}
	return full, nil
}
