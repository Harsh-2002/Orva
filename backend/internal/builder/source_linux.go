//go:build linux

package builder

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
)

// ReadSource never follows the legacy mutable functions/current namespace.
func (s *PublicationQueue) ReadSource(ctx context.Context, owner string) (code, dependencies string, err error) {
	if s == nil || ctx == nil {
		return "", "", ErrPublicationUnavailable
	}
	source, err := s.queue.db.ReadWorkloadSource(ctx, owner)
	if err != nil || source.Output.CodeGeneration == "" {
		return "", "", err
	}
	lease, err := s.code.Acquire(source.Output.CodeGeneration)
	if err != nil {
		return "", "", ErrPublicationUnavailable
	}
	defer lease.Release()
	id := lease.Identity()
	if id.Kind != artifactfs.KindCode || id.Owner != source.Owner || id.Digest != source.Output.CodeDigest {
		return "", "", ErrPublicationUnavailable
	}
	root, err := os.OpenRoot(lease.Path())
	if err != nil {
		return "", "", ErrPublicationUnavailable
	}
	defer root.Close()
	code, err = readSourceText(root, source.Entrypoint, 8<<20)
	if err != nil {
		return "", "", ErrPublicationUnavailable
	}
	depsName := "package.json"
	if source.Runtime == "python" {
		depsName = "requirements.txt"
	}
	dependencies, err = readSourceText(root, depsName, 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return "", "", ErrPublicationUnavailable
	}
	return code, dependencies, nil
}

func readSourceText(root *os.Root, name string, max int64) (string, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > max {
		return "", ErrPublicationUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max || !utf8.Valid(data) {
		return "", ErrPublicationUnavailable
	}
	return string(data), nil
}
