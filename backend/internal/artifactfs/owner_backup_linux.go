package artifactfs

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// CheckBackupPaths requires an open host-bound Owner and excludes witness
// aliases from the explicit set of host files the backup may read.
func (o *Owner) CheckBackupPaths(ctx context.Context, roots, files []string) error {
	if _, err := o.Bindings(ctx); err != nil {
		return err
	}
	s := o.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.closed {
		return ErrClosed
	}
	if s.witness == nil {
		return ErrInvalid
	}
	return checkBackupPaths(s.cfg.Host.WitnessPath, s.witness.file, append(append([]string{}, s.cfg.Host.BackupRoots...), roots...), files)
}

func checkBackupPaths(path string, witnessFile *os.File, roots, files []string) error {
	if err := validateWitnessPlacement(path, roots); err != nil {
		return err
	}
	witness, err := witnessFile.Stat()
	if err != nil {
		return err
	}
	for _, path := range files {
		st, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() || os.SameFile(st, witness) {
			return ErrIntegrity
		}
	}
	return nil
}

// BuildSourceName is reserved for the original archive attached by the builder
// after its writable build domain has been fenced.
const BuildSourceName = ".orva-build-source.tar.gz"

// WriteBuildSource exports the authored-only projection of a pre-retention seal.
func (l *Lease) WriteBuildSource(ctx context.Context, w io.Writer) error {
	gz := gzip.NewWriter(w)
	return errors.Join(l.WriteBuildSourceTar(ctx, gz), gz.Close())
}

// WriteRetainedBuildSource exports the original input only when the trusted
// deployment association records its digest.
func (l *Lease) WriteRetainedBuildSource(ctx context.Context, w io.Writer, digest string) error {
	m, err := l.verifiedCodeManifest(ctx)
	if err != nil {
		return err
	}
	for _, e := range m.Entries {
		if e.Path == BuildSourceName {
			if e.Type != TypeFile || e.HardlinkGroup != "" || e.Mode != 0600 || !validDigest(digest) || e.SHA256 != digest {
				return ErrIntegrity
			}
			return l.copyBuildSource(w, e)
		}
	}
	return ErrIntegrity
}

func (l *Lease) copyBuildSource(w io.Writer, source Entry) error {
	f, err := openNoFollow(filepath.Join(l.path, source.Path), false)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.CopyN(io.MultiWriter(w, hash), f, source.Size)
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != source.SHA256 {
		return ErrIntegrity
	}
	return nil
}

func (l *Lease) verifiedCodeManifest(ctx context.Context) (Manifest, error) {
	if l == nil || l.store == nil || l.id.Kind != KindCode || l.store.boundKind != KindCode {
		return Manifest{}, ErrInvalid
	}
	m := l.rec.durable.Manifest
	return m, verify(ctx, l.path, KindCode, m, l.store.limits, l.rec.reserved)
}

// WriteBuildSourceTar is the old projection without gzip encoding.
func (l *Lease) WriteBuildSourceTar(ctx context.Context, w io.Writer) (err error) {
	m, err := l.verifiedCodeManifest(ctx)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	defer func() {
		err = errors.Join(err, tw.Close())
	}()
	for _, e := range m.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Path == "." {
			continue
		}
		h := &tar.Header{Name: e.Path, Mode: int64(e.Mode), Format: tar.FormatGNU}
		switch e.Type {
		case TypeDirectory:
			h.Typeflag = tar.TypeDir
		case TypeFile:
			if e.HardlinkGroup != "" {
				return ErrUnsupported
			}
			h.Typeflag, h.Size = tar.TypeReg, e.Size
		default:
			return ErrUnsupported
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg {
			f, err := openNoFollow(filepath.Join(l.path, e.Path), false)
			if err != nil {
				return err
			}
			hash := sha256.New()
			_, err = io.CopyN(io.MultiWriter(tw, hash), f, e.Size)
			if err := errors.Join(err, f.Close()); err != nil {
				return err
			}
			if hex.EncodeToString(hash.Sum(nil)) != e.SHA256 {
				return ErrIntegrity
			}
		}
	}
	return nil
}
