package artifactfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// SealInstalledRuntime copies a trusted, stopped instance's installed rootfs.
// It imports neither host capabilities nor set-id bits. Internal links are
// preserved and independently verified by the normal runtime Seal profile.
func SealInstalledRuntime(ctx context.Context, store *Store, owner, source string) (_ Identity, err error) {
	if store == nil || store.binding == nil || store.boundKind != KindRuntime || !validOwner(KindRuntime, owner) {
		return Identity{}, ErrInvalid
	}
	return sealInstalledRuntime(ctx, store, owner, source)
}

func sealInstalledRuntime(ctx context.Context, store *Store, owner, source string) (Identity, error) {
	return importInstalledRuntime(ctx, store, owner, source, nil, nil)
}

// SealInitialRuntime selects an exact match in the bound store or seals a new
// copy. The installed tree must be operator-provisioned and offline.
func SealInitialRuntime(ctx context.Context, store *Store, owner, source string, validate func(string) error) (Identity, error) {
	if store == nil || store.binding == nil || store.boundKind != KindRuntime || !validOwner(KindRuntime, owner) || validate == nil {
		return Identity{}, ErrInvalid
	}
	parent, err := os.Lstat(filepath.Dir(source))
	if err != nil {
		return Identity{}, err
	}
	if !parent.IsDir() || checkInitialRuntimeEntry(".", parent) != nil {
		return Identity{}, ErrUnsupported
	}
	return importInstalledRuntime(ctx, store, owner, source, checkInitialRuntimeEntry, validate)
}

func checkInitialRuntimeEntry(rel string, info os.FileInfo) error {
	if strings.HasPrefix(rel, "tmp/") || strings.HasPrefix(rel, "var/tmp/") {
		return ErrUnsupported
	}
	uid := info.Sys().(*syscall.Stat_t).Uid
	if uid != 0 && uid != uint32(os.Geteuid()) {
		return ErrUnsupported
	}
	if info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0022 != 0 &&
		!(info.IsDir() && info.Mode()&os.ModeSticky != 0 && (rel == "tmp" || rel == "var/tmp")) {
		return ErrUnsupported
	}
	return nil
}

func importInstalledRuntime(ctx context.Context, store *Store, owner, source string, check func(string, os.FileInfo) error, validate func(string) error) (_ Identity, err error) {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !filepath.IsAbs(source) || resolved != source {
		return Identity{}, errors.Join(ErrUnsupported, err)
	}
	limits := store.limits
	const maxRuntime = 512 << 20
	tree, err := scanTree(ctx, source, limits, maxRuntime, check)
	if err != nil {
		return Identity{}, err
	}
	omitMissingRuntimeManuals(tree)
	if err := checkLinks(KindRuntime, tree, limits); err != nil {
		return Identity{}, err
	}
	if tree["."].entry.Type != TypeDirectory {
		return Identity{}, ErrUnsupported
	}
	if validate != nil {
		// A crash after Seal but before SQLite commit must not consume another
		// slot on retry. Only an exact, active copy of this source may be reused.
		if id, err := store.matchInstalledRuntime(ctx, owner, tree, validate); err != nil || id.Generation != "" {
			return id, err
		}
	}
	var reserved int64 = 1
	for _, item := range tree {
		reserved += item.entry.Size
	}
	stage, err := store.Begin(ctx, reserved)
	if err != nil {
		if stage != nil {
			err = errors.Join(err, stage.Abort())
		}
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
	if validate != nil {
		if err := validate(root); err != nil {
			return Identity{}, err
		}
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

func (s *Store) matchInstalledRuntime(ctx context.Context, owner string, tree map[string]observed, validate func(string) error) (Identity, error) {
	var match string
	s.mu.Lock()
	for gen, r := range s.records {
		if r.phase == active && !r.quarantined && r.identity.Kind == KindRuntime && r.identity.Owner == owner &&
			(match == "" || gen < match) && sameInstalledRuntime(r.durable.Manifest, tree) {
			match = gen
		}
	}
	s.mu.Unlock()
	if match == "" {
		return Identity{}, nil
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	lease, err := s.Acquire(match)
	if err != nil {
		return Identity{}, err
	}
	defer lease.Release()
	if err := validate(lease.Path()); err != nil {
		return Identity{}, err
	}
	return lease.Identity(), nil
}

func sameInstalledRuntime(m Manifest, tree map[string]observed) bool {
	if len(m.Entries) != len(tree) {
		return false
	}
	for _, sealed := range m.Entries {
		item, ok := tree[sealed.Path]
		e := item.entry
		// The copy strips special bits and is owned by the store writer, not the source.
		e.Mode, e.UID, e.GID = e.Mode&0777, sealed.UID, sealed.GID
		if !ok || e != sealed {
			return false
		}
	}
	return true
}

var missingManualAlternative = regexp.MustCompile(`^/usr/share/man/([a-z]{2}(_[A-Z]{2})?/)?man[1-9]/[A-Za-z0-9_.+-]+\.[1-9]\.gz$`)

// Slim Debian-based images omit the manuals but retain update-alternatives'
// documentation links. Drop only these inert dangling links from the copy;
// everything else must pass the unchanged runtime verifier.
func omitMissingRuntimeManuals(tree map[string]observed) {
	for name, path := range tree {
		e := path.entry
		if e.Type == TypeSymlink && filepath.Dir(name) == "etc/alternatives" &&
			filepath.Ext(name) == ".gz" && missingManualAlternative.MatchString(e.LinkTarget) {
			parent := filepath.Dir(e.LinkTarget[1:])
			if _, ok := tree[parent]; !ok {
				delete(tree, name)
			}
		}
	}
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
