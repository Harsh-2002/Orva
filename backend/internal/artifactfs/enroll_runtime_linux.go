package artifactfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// SealInstalledRuntime copies a trusted, stopped instance's installed rootfs.
// It imports neither host capabilities nor set-id bits. Internal links are
// preserved and independently verified by the normal runtime Seal profile.
func SealInstalledRuntime(ctx context.Context, store *Store, owner, source string) (_ Identity, err error) {
	if store == nil || store.boundKind != KindRuntime || !validOwner(KindRuntime, owner) {
		return Identity{}, ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !filepath.IsAbs(source) || resolved != source {
		return Identity{}, errors.Join(ErrUnsupported, err)
	}
	limits := store.limits
	const maxRuntime = 512 << 20
	tree, err := scan(ctx, source, limits, maxRuntime)
	if err != nil {
		return Identity{}, err
	}
	if err := checkLinks(KindRuntime, tree, limits); err != nil {
		return Identity{}, err
	}
	if tree["."].entry.Type != TypeDirectory {
		return Identity{}, ErrUnsupported
	}
	stage, err := store.Begin(ctx, maxRuntime)
	if err != nil {
		return Identity{}, err
	}
	sealed := false
	defer func() {
		if !sealed {
			err = errors.Join(err, stage.Abort())
		}
	}()
	root, err := stage.Path()
	if err != nil {
		return Identity{}, err
	}
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return Identity{}, err
		}
		if name == "." {
			continue
		}
		from, to := filepath.Join(source, name), filepath.Join(root, name)
		e := tree[name].entry
		switch {
		case e.Type == TypeDirectory:
			err = os.Mkdir(to, 0700)
		case e.Type == TypeSymlink:
			err = os.Symlink(e.LinkTarget, to)
		case e.HardlinkGroup != "" && e.HardlinkGroup != name:
			err = os.Link(filepath.Join(root, e.HardlinkGroup), to)
		default:
			err = copyInstalledRuntimeFile(from, to, e)
		}
		if err != nil {
			return Identity{}, err
		}
	}
	manifest := Manifest{Version: 1, Entries: make([]Entry, 0, len(names))}
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		e := tree[name].entry
		to := filepath.Join(root, name)
		if e.Type == TypeDirectory {
			if err := os.Chmod(to, os.FileMode(e.Mode&0777)); err != nil {
				return Identity{}, err
			}
		}
		actual, err := os.Lstat(to)
		if err != nil {
			return Identity{}, err
		}
		st := actual.Sys().(*syscall.Stat_t)
		e.Mode, e.UID, e.GID = st.Mode&07777, st.Uid, st.Gid
		manifest.Entries = append(manifest.Entries, e)
	}
	digest, err := Digest(manifest, limits)
	if err != nil {
		return Identity{}, err
	}
	gen, err := stage.Seal(ctx, KindRuntime, owner, manifest, digest)
	if err != nil {
		return Identity{}, err
	}
	sealed = true
	lease, err := store.Acquire(gen)
	if err != nil {
		return Identity{}, err
	}
	defer lease.Release()
	return lease.Identity(), nil
}

func copyInstalledRuntimeFile(from, to string, e Entry) error {
	in, err := openNoFollow(from, false)
	if err != nil {
		return err
	}
	defer in.Close()
	stat, err := in.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != e.Size {
		return ErrIntegrity
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(out, in, e.Size)
	if err == nil {
		err = out.Chmod(os.FileMode(e.Mode & 0777))
	}
	return errors.Join(err, out.Close())
}
