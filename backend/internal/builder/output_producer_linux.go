//go:build linux

package builder

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
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
)

func outputPath(name string, max int) bool {
	return name != "" && len(name) <= max && utf8.ValidString(name) && !strings.ContainsAny(name, "\\\x00") &&
		!filepath.IsAbs(name) && filepath.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}

// extract admits only bounded data files/directories, never archive links or author metadata authority.
func (j *outputJob) extract(ctx context.Context, archive string) (string, error) {
	if j.owner.cfg.MetadataRoot == "" || j.source != "" {
		return "", artifactfs.ErrInvalid
	}
	cfg := j.owner.cfg
	fd, err := syscall.Open(archive, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), archive)
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > cfg.ArchiveBytes {
		return "", artifactfs.ErrCapacity
	}
	hash := sha256.New()
	// A Domain verifies its own metadata namespace. Keep build input beside
	// that namespace, never inside it or inside the jail's writable tree.
	source, err := os.CreateTemp(cfg.MetadataRoot, "build-source-*.tar.gz")
	if err != nil {
		return "", err
	}
	j.source = source.Name()
	defer source.Close()
	if err := source.Chmod(0600); err != nil {
		return "", err
	}
	raw := io.TeeReader(io.LimitReader(f, cfg.ArchiveBytes+1), io.MultiWriter(hash, source))
	gz, err := gzip.NewReader(raw)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	bounded := &io.LimitedReader{R: outputContextReader{ctx, gz}, N: cfg.ArchiveBytes + 1}
	tr := tar.NewReader(bounded)
	var total int64
	paths := map[string]bool{".": true}
	headersSeen := map[string]bool{}
	directories := map[string]uint32{}
	headers := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		headers++
		if headers > cfg.Limits.Entries {
			return "", artifactfs.ErrCapacity
		}
		for _, component := range strings.Split(h.Name, "/") {
			if component == ".." {
				return "", artifactfs.ErrUnsupported
			}
		}
		name := filepath.Clean(h.Name)
		if !filepath.IsLocal(name) || !outputPath(name, cfg.Limits.PathBytes) || len(h.PAXRecords) != 0 || len(h.Xattrs) != 0 {
			return "", artifactfs.ErrUnsupported
		}
		if name == artifactfs.BuildSourceName || strings.HasPrefix(name, artifactfs.BuildSourceName+"/") {
			return "", artifactfs.ErrUnsupported
		}
		if headersSeen[name] {
			return "", artifactfs.ErrUnsupported
		}
		headersSeen[name] = true
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			return "", artifactfs.ErrUnsupported
		}
		if h.Mode < 0 || h.Mode > 07777 {
			return "", artifactfs.ErrUnsupported
		}
		if name == "." {
			if h.Typeflag != tar.TypeDir {
				return "", artifactfs.ErrUnsupported
			}
			continue
		}
		var parents []string
		for p := filepath.Dir(name); p != "."; p = filepath.Dir(p) {
			parents = append(parents, p)
		}
		for i := len(parents) - 1; i >= 0; i-- {
			p := parents[i]
			if !paths[p] {
				if len(paths) >= cfg.Limits.Entries {
					return "", artifactfs.ErrCapacity
				}
				if err := os.Mkdir(filepath.Join(j.path, p), 0755); err != nil {
					return "", err
				}
				paths[p] = true
			}
		}
		target := filepath.Join(j.path, name)
		if h.Typeflag == tar.TypeDir {
			if paths[name] {
				info, err := os.Lstat(target)
				if err != nil || !info.IsDir() {
					return "", artifactfs.ErrUnsupported
				}
			}
			if !paths[name] {
				if len(paths) >= cfg.Limits.Entries {
					return "", artifactfs.ErrCapacity
				}
				if err := os.Mkdir(target, 0755); err != nil {
					return "", err
				}
				paths[name] = true
			}
			directories[target] = uint32(h.Mode)
			continue
		}
		if paths[name] || len(paths) >= cfg.Limits.Entries {
			return "", artifactfs.ErrUnsupported
		}
		if h.Size < 0 || h.Size > cfg.Limits.FileBytes || h.Size > cfg.ReservedBytes-total {
			return "", artifactfs.ErrCapacity
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", err
		}
		n, copyErr := outputCopy(ctx, out, tr, h.Size)
		if copyErr == nil && n != h.Size {
			copyErr = artifactfs.ErrIntegrity
		}
		if copyErr == nil {
			copyErr = syscall.Fchmod(int(out.Fd()), uint32(h.Mode))
		}
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		total += n
		paths[name] = true
	}
	if _, err := outputCopy(ctx, io.Discard, bounded, cfg.ArchiveBytes); err != nil {
		return "", err
	}
	if bounded.N <= 0 {
		return "", artifactfs.ErrCapacity
	}
	// Hash the entire compressed input, including any trailing bytes, under the same file bound.
	if _, err := outputCopy(ctx, io.Discard, raw, cfg.ArchiveBytes); err != nil {
		return "", err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return "", artifactfs.ErrIntegrity
	}
	if st, err := source.Stat(); err != nil || st.Size() != before.Size() {
		return "", artifactfs.ErrIntegrity
	}
	if err := source.Close(); err != nil {
		return "", err
	}
	for path, mode := range directories {
		if err := syscall.Chmod(path, mode); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (j *outputJob) attachSource(ctx context.Context) error {
	f, err := os.Open(j.source)
	if err != nil {
		return err
	}
	defer f.Close()
	dst, err := os.OpenFile(filepath.Join(j.path, artifactfs.BuildSourceName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := dst.Chmod(0600); err != nil {
		return errors.Join(err, dst.Close())
	}
	_, err = outputCopy(ctx, dst, f, j.owner.cfg.Limits.FileBytes)
	return errors.Join(err, dst.Close())
}

type outputContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r outputContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func outputCopy(ctx context.Context, w io.Writer, r io.Reader, limit int64) (int64, error) {
	var total int64
	buf := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := r.Read(buf)
		if int64(n) > limit-total {
			return total, artifactfs.ErrCapacity
		}
		if n > 0 {
			written, e := w.Write(buf[:n])
			total += int64(written)
			if e != nil {
				return total, e
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

type outputInode struct{ dev, ino uint64 }

// produce has no caller path or caller "fenced" flag. Its only input tree is this owned stage.
func (j *outputJob) produce(ctx context.Context) (artifactfs.Manifest, string, error) {
	if j.stage == nil || j.domain == nil || j.proof == nil || !j.finished {
		return artifactfs.Manifest{}, "", artifactfs.ErrInvalid
	}
	if err := j.domain.Check(ctx, j.proof); err != nil {
		return artifactfs.Manifest{}, "", err
	}
	cfg := j.owner.cfg
	m := artifactfs.Manifest{Version: 1}
	groups := map[outputInode][]int{}
	var total int64
	var walk func(string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(m.Entries) >= cfg.Limits.Entries || !outputPath(rel, cfg.Limits.PathBytes) {
			return artifactfs.ErrCapacity
		}
		p := filepath.Join(j.path, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := info.Sys().(*syscall.Stat_t)
		e := artifactfs.Entry{Path: rel, Mode: st.Mode & 07777, UID: st.Uid, GID: st.Gid}
		index := len(m.Entries)
		switch st.Mode & syscall.S_IFMT {
		case syscall.S_IFDIR:
			e.Type = artifactfs.TypeDirectory
		case syscall.S_IFLNK:
			e.Type = artifactfs.TypeSymlink
			e.LinkTarget, err = os.Readlink(p)
			if err != nil {
				return err
			}
			if len(e.LinkTarget) > cfg.Limits.LinkBytes {
				return artifactfs.ErrCapacity
			}
		case syscall.S_IFREG:
			e.Type, e.Size = artifactfs.TypeFile, st.Size
			if e.Size < 0 || e.Size > cfg.Limits.FileBytes || e.Size > cfg.ReservedBytes-total {
				return artifactfs.ErrCapacity
			}
			total += e.Size
			fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			f := os.NewFile(uintptr(fd), p)
			h := sha256.New()
			n, readErr := outputCopy(ctx, h, f, e.Size)
			observed, statErr := f.Stat()
			closeErr := f.Close()
			if readErr != nil {
				return readErr
			}
			if statErr != nil {
				return statErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n != e.Size || !os.SameFile(info, observed) || observed.Size() != e.Size || observed.ModTime() != info.ModTime() {
				return artifactfs.ErrIntegrity
			}
			e.SHA256 = hex.EncodeToString(h.Sum(nil))
			groups[outputInode{uint64(st.Dev), st.Ino}] = append(groups[outputInode{uint64(st.Dev), st.Ino}], index)
		default:
			return artifactfs.ErrUnsupported
		}
		m.Entries = append(m.Entries, e)
		if e.Type == artifactfs.TypeDirectory {
			d, err := os.Open(p)
			if err != nil {
				return err
			}
			defer d.Close()
			for {
				entries, err := d.ReadDir(64)
				for _, entry := range entries {
					if err := walk(filepath.Join(rel, entry.Name())); err != nil {
						return err
					}
				}
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("."); err != nil {
		return artifactfs.Manifest{}, "", err
	}
	for _, indices := range groups {
		names := make([]string, 0, len(indices))
		for _, i := range indices {
			names = append(names, m.Entries[i].Path)
		}
		sort.Strings(names)
		info, err := os.Lstat(filepath.Join(j.path, names[0]))
		if err != nil {
			return artifactfs.Manifest{}, "", err
		}
		if uint64(info.Sys().(*syscall.Stat_t).Nlink) != uint64(len(indices)) {
			return artifactfs.Manifest{}, "", artifactfs.ErrUnsupported
		}
		if len(indices) > 1 {
			for _, i := range indices {
				m.Entries[i].HardlinkGroup = names[0]
			}
		}
	}
	if err := j.domain.Check(ctx, j.proof); err != nil {
		return artifactfs.Manifest{}, "", err
	}
	digest, err := artifactfs.Digest(m, cfg.Limits)
	return m, digest, err
}
