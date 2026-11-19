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

// WriteBuildSource projects only authored files from a checked code seal.
// The managed builder has no dependency or compilation phase; its queue
// discards the input tar. Host ownership and seal metadata are not portable.
func (l *Lease) WriteBuildSource(ctx context.Context, w io.Writer) (err error) {
	if l == nil || l.id.Kind != KindCode || l.store.boundKind != KindCode {
		return ErrInvalid
	}
	m := l.rec.durable.Manifest
	if err := verify(ctx, l.path, KindCode, m, l.store.limits, l.rec.reserved); err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	defer func() {
		tarErr := tw.Close()
		err = errors.Join(err, tarErr, gz.Close())
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
