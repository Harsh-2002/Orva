package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Legacy pip -t merged distribution files into /code. Use installed RECORDs
// to describe the projection; do not guess package names from requirements.
func enrolledPipFiles(ctx context.Context, dir, entrypoint string) (map[string]bool, error) {
	const maxPaths = 16384
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	base, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer base.Close()
	entries, err := base.ReadDir(maxPaths + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxPaths {
		return nil, errors.New("legacy Python target exceeds managed entry bounds")
	}
	files := make(map[string]bool)
	topDirs := make(map[string]bool)
	for _, d := range entries {
		if !strings.HasSuffix(d.Name(), ".dist-info") {
			continue
		}
		record := filepath.Join(d.Name(), "RECORD")
		if !d.IsDir() {
			return nil, errors.New("legacy Python distribution metadata is not a directory")
		}
		if err := readPipRecord(ctx, root, record, entrypoint, func(name string) error {
			if len(files) >= maxPaths {
				return errors.New("legacy Python dependency records exceed managed entry bounds")
			}
			files[name] = true
			if top, _, nested := strings.Cut(name, "/"); nested {
				topDirs[top] = true
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	count := 0
	for _, top := range slices.Sorted(maps.Keys(topDirs)) {
		info, err := root.Lstat(top)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("legacy pip output %q is not a directory", top)
		}
		err = fs.WalkDir(root.FS(), top, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			count++
			if count > maxPaths {
				return errors.New("legacy Python dependency tree exceeds managed entry bounds")
			}
			if d.IsDir() {
				if d.Name() == "__pycache__" {
					return fs.SkipDir
				}
				return nil
			}
			if !files[name] {
				return fmt.Errorf("legacy pip output %q shares a directory with untracked files", top)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		files[top] = true
	}
	return files, nil
}

func readPipRecord(ctx context.Context, root *os.Root, record, entrypoint string, add func(string) error) error {
	const limit = 1 << 20
	info, err := root.Lstat(record)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return fmt.Errorf("legacy pip metadata %q needs a bounded regular RECORD", record)
	}
	f, err := root.Open(record)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(io.LimitReader(f, limit+1))
	r.FieldsPerRecord = 3
	self := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid legacy pip RECORD %q: %w", record, err)
		}
		name := row[0]
		// pip rewrites script locations relative to purelib before moving
		// them to bin/ in the final target.
		if script, ok := strings.CutPrefix(name, "../../bin/"); ok && filepath.Base(script) == script {
			name = "bin/" + script
		}
		if !filepath.IsLocal(name) || filepath.Clean(name) != name || name == "." || len(name) > 4096 ||
			strings.ContainsAny(name, "\\\x00") || name == entrypoint || name == "requirements.txt" ||
			(filepath.Ext(name) == ".pyc" && filepath.Base(filepath.Dir(name)) != "__pycache__") {
			return fmt.Errorf("legacy pip RECORD %q cannot distinguish generated and authored path %q", record, row[0])
		}
		if name == record {
			self = true
		}
		if err := verifyPipRecordFile(root, name, record, row[1]); err != nil {
			return err
		}
		if err := add(name); err != nil {
			return err
		}
	}
	if !self {
		return fmt.Errorf("legacy pip RECORD %q does not identify its own distribution", record)
	}
	return nil
}

func verifyPipRecordFile(root *os.Root, name, record, digest string) error {
	// Bytecode and installer metadata can have no hash. Restrict that case
	// to pip's own metadata directory and the standard bytecode namespace.
	if digest == "" && (strings.HasPrefix(name, filepath.Dir(record)+"/") ||
		(filepath.Ext(name) == ".pyc" && filepath.Base(filepath.Dir(name)) == "__pycache__")) {
		return nil
	}
	want, ok := strings.CutPrefix(digest, "sha256=")
	info, err := root.Lstat(name)
	if !ok || err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return fmt.Errorf("legacy pip RECORD cannot verify installed file %q", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, info.Size()+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	if n != info.Size() || base64.RawURLEncoding.EncodeToString(hash.Sum(nil)) != want {
		return fmt.Errorf("legacy pip file %q differs from its RECORD", name)
	}
	return nil
}
