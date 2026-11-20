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
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

var ErrDeploymentSourceGone = errors.New("retained deployment source unavailable")

type DeploymentSourceText struct {
	Entrypoint, Code, Manifest, Dependencies string
	HasManifest                              bool
}

func (s *PublicationQueue) ReadDeploymentSource(ctx context.Context, owner, deployment string) (DeploymentSourceText, error) {
	source, err := s.queue.db.ReadWorkloadDeploymentSource(ctx, owner, deployment)
	if errors.Is(err, database.ErrWorkloadArtifactConflict) {
		return DeploymentSourceText{}, ErrDeploymentSourceGone
	}
	if err != nil {
		return DeploymentSourceText{}, err
	}
	return s.readSource(source)
}

// ReadSource never follows the legacy mutable functions/current namespace.
func (s *PublicationQueue) ReadSource(ctx context.Context, owner string) (code, dependencies string, err error) {
	if s == nil || ctx == nil {
		return "", "", ErrPublicationUnavailable
	}
	source, err := s.queue.db.ReadWorkloadSource(ctx, owner)
	if err != nil || source.Output.CodeGeneration == "" {
		return "", "", err
	}
	text, err := s.readSource(source)
	return text.Code, text.Dependencies, err
}

func (s *PublicationQueue) readSource(source database.WorkloadSource) (DeploymentSourceText, error) {
	lease, err := s.code.Acquire(source.Output.CodeGeneration)
	if errors.Is(err, artifactfs.ErrInvalid) {
		return DeploymentSourceText{}, ErrDeploymentSourceGone
	}
	if err != nil {
		return DeploymentSourceText{}, ErrPublicationUnavailable
	}
	defer lease.Release()
	id := lease.Identity()
	if id.Kind != artifactfs.KindCode || id.Owner != source.Owner || id.Digest != source.Output.CodeDigest {
		return DeploymentSourceText{}, ErrPublicationUnavailable
	}
	root, err := os.OpenRoot(lease.Path())
	if err != nil {
		return DeploymentSourceText{}, ErrPublicationUnavailable
	}
	defer root.Close()
	code, err := readSourceText(root, source.Entrypoint, 8<<20)
	if err != nil {
		return DeploymentSourceText{}, ErrPublicationUnavailable
	}
	depsName := "package.json"
	if source.Runtime == "python" {
		depsName = "requirements.txt"
	}
	dependencies, err := readSourceText(root, depsName, 1<<20)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return DeploymentSourceText{}, ErrPublicationUnavailable
	}
	return DeploymentSourceText{source.Entrypoint, code, depsName, dependencies, err == nil}, nil
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
