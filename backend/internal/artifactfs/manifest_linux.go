//go:build linux

package artifactfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

type EntryType string

const (
	TypeDirectory EntryType = "directory"
	TypeFile      EntryType = "file"
	TypeSymlink   EntryType = "symlink"
)

// Entry uses POSIX permission/special mode bits (0000..07777), not Go FileMode.
// HardlinkGroup is the lexically first path in a multi-name regular-file group.
type Entry struct {
	Path          string    `json:"path"`
	Type          EntryType `json:"type"`
	Mode          uint32    `json:"mode"`
	UID           uint32    `json:"uid"`
	GID           uint32    `json:"gid"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	LinkTarget    string    `json:"link_target"`
	HardlinkGroup string    `json:"hardlink_group"`
}

type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validPath(p string, max int) bool {
	if p == "" || len(p) > max || !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\\") || path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	return p == "." || (p != ".." && !strings.HasPrefix(p, "../"))
}

func canonical(m Manifest, limits Limits) (Manifest, string, error) {
	if !limits.valid() || m.Version != 1 || len(m.Entries) == 0 || len(m.Entries) > limits.Entries {
		return Manifest{}, "", ErrInvalid
	}
	// Bound input before cloning or JSON escaping (at most six bytes per byte).
	budget := limits.ManifestBytes - 32
	for _, e := range m.Entries {
		n := len(e.Path) + len(e.Type) + len(e.SHA256) + len(e.LinkTarget) + len(e.HardlinkGroup)
		if n > limits.ManifestBytes || budget < 200 || n > (budget-200)/6 {
			return Manifest{}, "", ErrCapacity
		}
		budget -= 200 + 6*n
		if !validPath(e.Path, limits.PathBytes) || e.Mode > 07777 || e.Size < 0 || e.Size > limits.FileBytes ||
			len(e.LinkTarget) > limits.LinkBytes || !utf8.ValidString(e.LinkTarget) || strings.ContainsAny(e.LinkTarget, "\x00\\") {
			return Manifest{}, "", ErrInvalid
		}
		switch e.Type {
		case TypeFile:
			if !validDigest(e.SHA256) || e.LinkTarget != "" || (e.HardlinkGroup != "" && !validPath(e.HardlinkGroup, limits.PathBytes)) {
				return Manifest{}, "", ErrInvalid
			}
		case TypeDirectory:
			if e.Size != 0 || e.SHA256 != "" || e.LinkTarget != "" || e.HardlinkGroup != "" {
				return Manifest{}, "", ErrInvalid
			}
		case TypeSymlink:
			if e.Size != 0 || e.SHA256 != "" || e.LinkTarget == "" || e.HardlinkGroup != "" {
				return Manifest{}, "", ErrInvalid
			}
		default:
			return Manifest{}, "", ErrUnsupported
		}
	}
	owned := Manifest{Version: 1, Entries: append([]Entry(nil), m.Entries...)}
	sort.Slice(owned.Entries, func(i, j int) bool { return owned.Entries[i].Path < owned.Entries[j].Path })
	hasRoot := false
	for i, e := range owned.Entries {
		if i > 0 && e.Path == owned.Entries[i-1].Path {
			return Manifest{}, "", ErrInvalid
		}
		if e.Path == "." {
			if e.Type != TypeDirectory {
				return Manifest{}, "", ErrInvalid
			}
			hasRoot = true
		}
	}
	if !hasRoot {
		return Manifest{}, "", ErrInvalid
	}
	raw, err := json.Marshal(owned)
	if err != nil || len(raw) > limits.ManifestBytes {
		return Manifest{}, "", ErrCapacity
	}
	digest := sha256.Sum256(raw)
	return owned, hex.EncodeToString(digest[:]), nil
}

// Digest canonicalizes typed host inputs; it does not establish provenance.
// There is intentionally no JSON decoder or producer self-manifest API.
func Digest(manifest Manifest, limits Limits) (string, error) {
	_, digest, err := canonical(manifest, limits)
	return digest, err
}

type inodeKey struct {
	dev, ino uint64
}

type stamp struct {
	dev, ino, links uint64
	mode, uid, gid  uint32
	size            int64
	mtime, ctime    syscall.Timespec
}

func metadata(st *syscall.Stat_t) stamp {
	return stamp{uint64(st.Dev), st.Ino, uint64(st.Nlink), st.Mode, st.Uid, st.Gid, st.Size, st.Mtim, st.Ctim}
}

type observed struct {
	entry Entry
	stamp stamp
}

func noXattrs(p string) error {
	ptr, err := syscall.BytePtrFromString(p)
	if err != nil {
		return ErrInvalid
	}
	// llistxattr must not follow a runtime's chroot-relative absolute link.
	n, _, errno := syscall.Syscall(syscall.SYS_LLISTXATTR, uintptr(unsafe.Pointer(ptr)), 0, 0)
	if errno != 0 || n != 0 {
		return ErrUnsupported
	}
	return nil
}

func openNoFollow(p string, directory bool) (*os.File, error) {
	flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	if directory {
		flags |= syscall.O_DIRECTORY
	}
	fd, err := syscall.Open(p, flags, 0)
	if err != nil {
		return nil, ErrIntegrity
	}
	return os.NewFile(uintptr(fd), p), nil
}

func fileHash(ctx context.Context, p string, want stamp) (string, error) {
	f, err := openNoFollow(p, false)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || metadata(info.Sys().(*syscall.Stat_t)) != want {
		return "", ErrIntegrity
	}
	h := sha256.New()
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := f.Read(buf)
		if int64(n) > want.size-total {
			return "", ErrIntegrity
		}
		total += int64(n)
		_, _ = h.Write(buf[:n])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", ErrIO
		}
	}
	info, err = f.Stat()
	if err != nil || total != want.size || metadata(info.Sys().(*syscall.Stat_t)) != want {
		return "", ErrIntegrity
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func scan(ctx context.Context, root string, limits Limits, reserved int64) (map[string]observed, error) {
	out := make(map[string]observed)
	groups := make(map[inodeKey][]string)
	var bytes int64
	var walk func(string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(out) >= limits.Entries || !validPath(rel, limits.PathBytes) {
			return ErrCapacity
		}
		p := filepath.Join(root, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return ErrIntegrity
		}
		st := info.Sys().(*syscall.Stat_t)
		before := metadata(st)
		e := Entry{Path: rel, Mode: st.Mode & 07777, UID: st.Uid, GID: st.Gid}
		if err := noXattrs(p); err != nil {
			return err
		}
		switch st.Mode & syscall.S_IFMT {
		case syscall.S_IFDIR:
			e.Type = TypeDirectory
		case syscall.S_IFREG:
			e.Type, e.Size = TypeFile, st.Size
			if e.Size < 0 || e.Size > limits.FileBytes || e.Size > reserved-bytes {
				return ErrCapacity
			}
			bytes += e.Size
			e.SHA256, err = fileHash(ctx, p, before)
			if err != nil {
				return err
			}
			key := inodeKey{uint64(st.Dev), st.Ino}
			groups[key] = append(groups[key], rel)
		case syscall.S_IFLNK:
			e.Type = TypeSymlink
			if st.Nlink != 1 || st.Size > int64(limits.LinkBytes) {
				return ErrUnsupported
			}
			e.LinkTarget, err = os.Readlink(p)
			if err != nil || len(e.LinkTarget) > limits.LinkBytes {
				return ErrIntegrity
			}
		default:
			return ErrUnsupported
		}
		out[rel] = observed{e, before}
		if e.Type == TypeDirectory {
			dir, err := openNoFollow(p, true)
			if err != nil {
				return err
			}
			defer dir.Close()
			dirInfo, err := dir.Stat()
			if err != nil || metadata(dirInfo.Sys().(*syscall.Stat_t)) != before {
				return ErrIntegrity
			}
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				names, readErr := dir.Readdirnames(128)
				for _, name := range names {
					if err := walk(path.Join(rel, name)); err != nil {
						return err
					}
				}
				if errors.Is(readErr, io.EOF) {
					break
				}
				if readErr != nil {
					return ErrIO
				}
			}
		}
		after, err := os.Lstat(p)
		if err != nil || metadata(after.Sys().(*syscall.Stat_t)) != before {
			return ErrIntegrity
		}
		return nil
	}
	if err := walk("."); err != nil {
		return nil, err
	}
	for _, names := range groups {
		sort.Strings(names)
		for _, name := range names {
			o := out[name]
			if o.stamp.links != uint64(len(names)) {
				return nil, ErrUnsupported
			}
			if len(names) > 1 {
				o.entry.HardlinkGroup = names[0]
				out[name] = o
			}
		}
	}
	return out, nil
}

func checkLinks(kind Kind, tree map[string]observed) error {
	directories := make(map[string][]string)
	indegree := make(map[string]int)
	for rel, o := range tree {
		if o.entry.Type == TypeDirectory {
			indegree[rel] = 0
			if rel != "." {
				parent := path.Dir(rel)
				directories[parent] = append(directories[parent], rel)
			}
		}
	}
	for rel, o := range tree {
		if o.entry.Type != TypeSymlink {
			continue
		}
		target := o.entry.LinkTarget
		for _, component := range strings.Split(target, "/") {
			if component == ".." || component == "." {
				return ErrUnsupported
			}
		}
		var resolved string
		if path.IsAbs(target) {
			if kind != KindRuntime || path.Clean(target) != target {
				return ErrUnsupported
			}
			resolved = strings.TrimPrefix(path.Clean(target), "/")
			if resolved == "" {
				resolved = "."
			}
		} else {
			if path.Clean(target) != target {
				return ErrUnsupported
			}
			resolved = path.Join(path.Dir(rel), target)
		}
		if !validPath(resolved, 4096) {
			return ErrUnsupported
		}
		if resolved == "." || strings.HasPrefix(rel, resolved+"/") {
			return ErrUnsupported
		}
		// This deliberately small profile rejects all symlink chains/cycles,
		// dangling targets and symlinked target parents, without following them.
		for current := resolved; ; current = path.Dir(current) {
			entry, ok := tree[current]
			if !ok || entry.entry.Type == TypeSymlink || (current != resolved && entry.entry.Type != TypeDirectory) {
				return ErrUnsupported
			}
			if current == "." {
				break
			}
		}
		if tree[resolved].entry.Type == TypeDirectory {
			parent := path.Dir(rel)
			directories[parent] = append(directories[parent], resolved)
		}
	}
	// Directory links can form traversal cycles without targeting a symlink.
	for _, targets := range directories {
		for _, target := range targets {
			indegree[target]++
		}
	}
	queue := make([]string, 0, len(indegree))
	for dir, incoming := range indegree {
		if incoming == 0 {
			queue = append(queue, dir)
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, target := range directories[queue[i]] {
			indegree[target]--
			if indegree[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	if len(queue) != len(indegree) {
		return ErrUnsupported
	}
	return nil
}

func verify(ctx context.Context, root string, kind Kind, expected Manifest, limits Limits, reserved int64) error {
	first, err := scan(ctx, root, limits, reserved)
	if err != nil {
		return err
	}
	if len(first) != len(expected.Entries) {
		return ErrIntegrity
	}
	for _, e := range expected.Entries {
		if got, ok := first[e.Path]; !ok || got.entry != e {
			return ErrIntegrity
		}
	}
	if err := checkLinks(kind, first); err != nil {
		return err
	}
	second, err := scan(ctx, root, limits, reserved)
	if err != nil {
		return err
	}
	if len(first) != len(second) {
		return ErrIntegrity
	}
	for name, original := range first {
		if got, ok := second[name]; !ok || got != original {
			return ErrIntegrity
		}
	}
	return ctx.Err()
}
