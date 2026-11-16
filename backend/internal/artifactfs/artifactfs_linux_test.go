//go:build linux

package artifactfs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func testLimits() Limits {
	return Limits{Generations: 8, Leases: 64, Staging: 4, Entries: 100, PathBytes: 256, LinkBytes: 256, ManifestBytes: 1 << 20, Bytes: 1 << 20, FileBytes: 1 << 16}
}

func newStore(t *testing.T, limits Limits) *Store {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, r := range s.records {
			if r.isStage {
				if err := (&Staging{s, r}).Abort(); err != nil {
					t.Errorf("stage cleanup: %v", err)
				}
			} else {
				_ = s.Retire(r.identity.Generation)
				if err := s.Reclaim(r.identity.Generation); err != nil {
					t.Errorf("generation cleanup: %v", err)
				}
			}
		}
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s
}

func begin(t *testing.T, s *Store, reserve int64) (*Staging, string) {
	t.Helper()
	st, err := s.Begin(context.Background(), reserve)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.Path()
	if err != nil {
		t.Fatal(err)
	}
	return st, p
}

func write(t *testing.T, p, contents string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(contents), 0640); err != nil {
		t.Fatal(err)
	}
}

// Build fixture expectations independently, rather than reusing scan/verify.
func fixtureManifest(t *testing.T, root string) Manifest {
	t.Helper()
	m := Manifest{Version: 1}
	inodes := make(map[inodeKey][]int)
	err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := info.Sys().(*syscall.Stat_t)
		rel, _ := filepath.Rel(root, p)
		e := Entry{Path: rel, Mode: st.Mode & 07777, UID: st.Uid, GID: st.Gid}
		switch st.Mode & syscall.S_IFMT {
		case syscall.S_IFDIR:
			e.Type = TypeDirectory
		case syscall.S_IFREG:
			e.Type, e.Size = TypeFile, st.Size
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			e.SHA256 = hex.EncodeToString(sum[:])
			key := inodeKey{uint64(st.Dev), st.Ino}
			inodes[key] = append(inodes[key], len(m.Entries))
		case syscall.S_IFLNK:
			e.Type = TypeSymlink
			e.LinkTarget, err = os.Readlink(p)
			if err != nil {
				return err
			}
		default:
			e.Type = "special"
		}
		m.Entries = append(m.Entries, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range inodes {
		if len(group) > 1 {
			for _, i := range group {
				m.Entries[i].HardlinkGroup = m.Entries[group[0]].Path
			}
		}
	}
	return m
}

func sealManifest(t *testing.T, st *Staging, kind Kind, owner string, m Manifest) (string, error) {
	t.Helper()
	digest, err := Digest(m, st.store.limits)
	if err != nil {
		t.Fatal(err)
	}
	return st.Seal(context.Background(), kind, owner, m, digest)
}

func sealed(t *testing.T, s *Store, kind Kind, owner string) string {
	t.Helper()
	st, p := begin(t, s, 100)
	write(t, filepath.Join(p, "handler"), "hello")
	id, err := sealManifest(t, st, kind, owner, fixtureManifest(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if !ids.IsUUID(id) || len(id) != 36 || id[14] != '7' {
		t.Fatal("generation is not store-minted UUIDv7")
	}
	return id
}

func TestOwnedGenerationLifetime(t *testing.T) {
	s := newStore(t, testLimits())
	st, oldPath := begin(t, s, 100)
	write(t, filepath.Join(oldPath, "file"), "original")
	if err := os.Link(filepath.Join(oldPath, "file"), filepath.Join(oldPath, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(oldPath, 0750|os.ModeSticky|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(oldPath, "file"), 0751|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	m := fixtureManifest(t, oldPath)
	original, _ := os.Lstat(filepath.Join(oldPath, "file"))
	id, err := sealManifest(t, st, KindCode, ids.New(), m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Path(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("producer retained access: %v", err)
	}
	l, err := s.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	after, err := os.Lstat(filepath.Join(l.Path(), "file"))
	if err != nil || !os.SameFile(original, after) || original.Mode() != after.Mode() {
		t.Fatal("rename changed inode or mode")
	}
	aStat, bStat := original.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if aStat.Uid != bStat.Uid || aStat.Gid != bStat.Gid || aStat.Nlink != bStat.Nlink {
		t.Fatal("rename changed ownership or hardlink relationships")
	}
	if err := os.Mkdir(oldPath, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(oldPath, "file"), "replacement")
	t.Cleanup(func() { _ = os.RemoveAll(oldPath) })
	// Simulate old-version GC after a newer generation is published.
	_ = sealed(t, s, KindCode, l.Identity().Owner)
	if err := s.Retire(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(id); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	if err := s.Reclaim(id); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(l.Path(), "file"))
	if err != nil || string(data) != "original" {
		t.Fatal("owned generation replaced or deleted")
	}
	l.Release()
	l.Release()
	if err := s.Reclaim(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(l.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retired generation not removed")
	}
}

func TestManifestTampering(t *testing.T) {
	for _, which := range []string{"extra", "missing", "hash", "mode", "uid", "gid", "rootmode", "hardlink", "digest"} {
		t.Run(which, func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			file := filepath.Join(p, "file")
			write(t, file, "hello")
			if err := os.Link(file, filepath.Join(p, "hard")); err != nil {
				t.Fatal(err)
			}
			m := fixtureManifest(t, p)
			switch which {
			case "extra":
				write(t, filepath.Join(p, "extra"), "x")
			case "missing":
				// Missing without changing a hardlink count first.
				m.Entries = append(m.Entries, Entry{Path: "absent", Type: TypeDirectory, Mode: 0700})
			case "hash":
				write(t, file, "world")
			case "mode":
				if err := os.Chmod(file, 0700); err != nil {
					t.Fatal(err)
				}
			case "uid":
				m.Entries[1].UID++
			case "gid":
				m.Entries[1].GID++
			case "rootmode":
				m.Entries[0].Mode ^= 0100
			case "hardlink":
				m.Entries[1].HardlinkGroup = ""
			}
			digest, err := Digest(m, s.limits)
			if err != nil {
				t.Fatal(err)
			}
			if which == "digest" {
				digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			if _, err = st.Seal(context.Background(), KindCode, ids.New(), m, digest); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("wanted integrity error, got %v", err)
			}
			if s.bytes != 100 || len(s.records) != 1 {
				t.Fatal("failed verification released reservation")
			}
		})
	}
}

func TestLinksAndSpecialFiles(t *testing.T) {
	cases := []struct {
		name, target string
		kind         Kind
		allow        bool
	}{
		{"relative", "file", KindCode, true},
		{"absolute-code", "/file", KindCode, false},
		{"absolute-runtime", "/file", KindRuntime, true},
		{"escape", "../outside", KindCode, false},
		{"absolute-escape", "/../file", KindRuntime, false},
		{"dangling", "absent", KindRuntime, false},
		{"self", "link", KindCode, false},
		{"ancestor", ".", KindCode, false},
		{"dot-segment-profile", "dir/../file", KindCode, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			write(t, filepath.Join(p, "file"), "x")
			if err := os.Symlink(tc.target, filepath.Join(p, "link")); err != nil {
				t.Fatal(err)
			}
			owner := ids.New()
			if tc.kind == KindRuntime {
				owner = "node"
			}
			_, err := sealManifest(t, st, tc.kind, owner, fixtureManifest(t, p))
			if tc.allow && err != nil || !tc.allow && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("allow=%v, got %v", tc.allow, err)
			}
		})
	}
	for _, which := range []string{"chain", "parent-chain", "outside-hardlink", "fifo"} {
		t.Run(which, func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			write(t, filepath.Join(p, "file"), "x")
			switch which {
			case "chain":
				_ = os.Symlink("file", filepath.Join(p, "a"))
				_ = os.Symlink("a", filepath.Join(p, "b"))
			case "parent-chain":
				_ = os.Mkdir(filepath.Join(p, "dir"), 0700)
				write(t, filepath.Join(p, "dir", "target"), "x")
				_ = os.Symlink("dir", filepath.Join(p, "a"))
				_ = os.Symlink("a/target", filepath.Join(p, "b"))
			case "outside-hardlink":
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.Link(filepath.Join(p, "file"), outside); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(filepath.Join(p, "fifo"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m := fixtureManifest(t, p)
			if which == "fifo" {
				// A trusted manifest claiming a regular file cannot bless a FIFO.
				for i := range m.Entries {
					if m.Entries[i].Path == "fifo" {
						sum := sha256.Sum256(nil)
						m.Entries[i].Type, m.Entries[i].SHA256 = TypeFile, hex.EncodeToString(sum[:])
					}
				}
			}
			if _, err := sealManifest(t, st, KindCode, ids.New(), m); !errors.Is(err, ErrUnsupported) {
				t.Fatal(err)
			}
		})
	}
}

func linkFixture(t *testing.T, root string, dirs, files []string, links map[string]string) Manifest {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		write(t, filepath.Join(root, file), "x")
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	return fixtureManifest(t, root)
}

func manifestTree(m Manifest) map[string]observed {
	tree := make(map[string]observed, len(m.Entries))
	for _, e := range m.Entries {
		tree[e.Path] = observed{entry: e}
	}
	return tree
}

func TestRuntimeLinkResolution(t *testing.T) {
	cases := []struct {
		name, probe, want string
		dirs, files       []string
		links             map[string]string
	}{
		{
			name: "relative-dotdot", probe: "sub/link", want: "file",
			dirs: []string{"sub"}, files: []string{"file"},
			links: map[string]string{"sub/link": "../file"},
		},
		{
			name: "packaged-os-release", probe: "etc/os-release", want: "usr/lib/os-release",
			dirs: []string{"etc", "usr/lib"}, files: []string{"usr/lib/os-release"},
			links: map[string]string{"etc/os-release": "../usr/lib/os-release"},
		},
		{
			name: "dot-component", probe: "link", want: "dir/file",
			dirs: []string{"dir"}, files: []string{"dir/file"},
			links: map[string]string{"link": "dir/./file"},
		},
		{
			name: "chain", probe: "b", want: "file", files: []string{"file"},
			links: map[string]string{"a": "file", "b": "a"},
		},
		{
			name: "absolute-chain-resets-root", probe: "sub/b", want: "file",
			dirs: []string{"sub"}, files: []string{"file", "sub/file"},
			links: map[string]string{"sub/a": "/file", "sub/b": "a"},
		},
		{
			name: "parent-chain", probe: "b", want: "dir/file",
			dirs: []string{"dir"}, files: []string{"dir/file"},
			links: map[string]string{"a": "dir", "b": "a/file"},
		},
		{
			name: "expand-before-dotdot", probe: "link", want: "other/file",
			dirs: []string{"a", "other/deep"}, files: []string{"a/file", "other/file"},
			links: map[string]string{"a/p": "/other/deep", "link": "a/p/../file"},
		},
		{
			name: "lexical-escape-is-not-resolution", probe: "sub/link", want: "file",
			dirs: []string{"sub", "x/y"}, files: []string{"file"},
			links: map[string]string{"sub/a": "/x/y", "sub/link": "a/../../file"},
		},
		{
			name: "same-link-different-tail", probe: "link", want: "dir/file",
			dirs: []string{"dir"}, files: []string{"dir/file"},
			links: map[string]string{"alias": "dir", "link": "alias/../alias/file"},
		},
		{
			name: "packaged-alternatives-through-bin", probe: "usr/bin/pager", want: "usr/bin/more",
			dirs: []string{"usr/bin", "etc/alternatives"}, files: []string{"usr/bin/more"},
			links: map[string]string{
				"bin": "usr/bin", "usr/bin/pager": "/etc/alternatives/pager",
				"etc/alternatives/pager": "/bin/more",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			m := linkFixture(t, p, tc.dirs, tc.files, tc.links)
			got, err := resolveRuntimeLink(tc.probe, manifestTree(m), s.limits)
			if err != nil || got != tc.want {
				t.Fatalf("resolved %q, want %q: %v", got, tc.want, err)
			}
			// The same fixture must not widen the existing code profile.
			if err := checkLinks(KindCode, manifestTree(m), s.limits); err != ErrUnsupported {
				t.Fatalf("code profile accepted runtime-only links: %v", err)
			}
			digest, err := Digest(m, s.limits)
			if err != nil {
				t.Fatal(err)
			}
			id, err := st.Seal(context.Background(), KindRuntime, "node", m, digest)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := s.Acquire(id)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			if lease.Identity().Digest != digest {
				t.Fatal("resolution changed manifest identity")
			}
			for name, target := range tc.links {
				got, err := os.Readlink(filepath.Join(lease.Path(), name))
				if err != nil || got != target {
					t.Fatalf("link text changed for %q: %q, %v", name, got, err)
				}
			}
		})
	}
}

func TestRuntimeLinkRejections(t *testing.T) {
	cases := []struct {
		name        string
		dirs, files []string
		links       map[string]string
	}{
		{name: "escape", links: map[string]string{"link": "../file"}},
		{name: "absolute-escape", links: map[string]string{"link": "/../file"}},
		{
			name: "escape-after-parent-expansion", dirs: []string{"a", "dir"},
			links: map[string]string{"a/p": "/dir", "link": "a/p/../../file"},
		},
		{name: "dangling-chain", links: map[string]string{"a": "absent", "link": "a"}},
		{name: "missing-before-dotdot", links: map[string]string{"link": "absent/../file"}},
		{name: "file-parent", links: map[string]string{"link": "file/child"}},
		{name: "file-dotdot", links: map[string]string{"link": "file/../file"}},
		{name: "file-dot", links: map[string]string{"link": "file/."}},
		{name: "file-parent-chain", links: map[string]string{"a": "file", "link": "a/../file"}},
		{name: "root", links: map[string]string{"link": "/"}},
		{
			name: "ancestor-through-parent-chain", dirs: []string{"a/deep"},
			links: map[string]string{"alias": "a/deep", "a/link": "/alias/.."},
		},
		{name: "self", links: map[string]string{"link": "link"}},
		{name: "two-link-cycle", links: map[string]string{"a": "b", "b": "a"}},
		{name: "absolute-cycle", dirs: []string{"sub"}, links: map[string]string{"a": "/sub/b", "sub/b": "/a"}},
		{name: "growing-cycle", links: map[string]string{"a": "a/x"}},
		{
			name: "directory-cycle-through-parent-chain", dirs: []string{"a", "b/deep"},
			links: map[string]string{"alias": "b", "a/link": "/alias/deep", "b/deep/back": "/a"},
		},
		{name: "double-slash", links: map[string]string{"link": "//file"}},
		{name: "empty-component", dirs: []string{"dir"}, links: map[string]string{"link": "dir//../file"}},
		{name: "trailing-slash", dirs: []string{"dir"}, links: map[string]string{"link": "dir/"}},
		{name: "backslash", links: map[string]string{"link": "dir\\file"}},
		{name: "invalid-utf8", links: map[string]string{"link": "bad\xff"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			m := linkFixture(t, p, tc.dirs, append([]string{"file"}, tc.files...), tc.links)
			digest, err := Digest(m, s.limits)
			if tc.name == "backslash" || tc.name == "invalid-utf8" {
				if err != ErrInvalid {
					t.Fatalf("invalid typed target accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.Seal(context.Background(), KindRuntime, "python", m, digest); err != ErrUnsupported {
				t.Fatalf("unsupported runtime link accepted: %v", err)
			}
		})
	}
}

func TestRuntimeResolutionBounds(t *testing.T) {
	for _, count := range []int{40, 41} {
		t.Run(fmt.Sprintf("hops-%d", count), func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			links := make(map[string]string)
			for i := 0; i < count; i++ {
				target := "file"
				if i+1 < count {
					target = fmt.Sprintf("link%d", i+1)
				}
				links[fmt.Sprintf("link%d", i)] = target
			}
			m := linkFixture(t, p, nil, []string{"file"}, links)
			_, err := sealManifest(t, st, KindRuntime, "node", m)
			if count == 40 && err != nil || count == 41 && err != ErrUnsupported {
				t.Fatalf("hop boundary %d: %v", count, err)
			}
		})
	}
	for _, which := range []string{"expanded-path", "resolved-path", "components"} {
		t.Run(which, func(t *testing.T) {
			s := newStore(t, testLimits())
			_, p := begin(t, s, 100)
			m := linkFixture(t, p, []string{"long-directory"}, []string{"long-directory/file"}, map[string]string{
				"a": "long-directory", "link": "a/././././file",
			})
			limits := s.limits
			switch which {
			case "expanded-path":
				limits.PathBytes = len("long-directory/file")
			case "resolved-path":
				limits.PathBytes = len("long-directory") - 1
			case "components":
				limits.Entries = 5
			}
			if _, err := resolveRuntimeLink("link", manifestTree(m), limits); err != ErrUnsupported {
				t.Fatalf("resolution %s budget ignored: %v", which, err)
			}
		})
	}
	t.Run("typed-target-validation", func(t *testing.T) {
		for _, target := range []string{"bad\x00target", "bad\xff", "bad\\target", strings.Repeat("x", 257)} {
			m := Manifest{Version: 1, Entries: []Entry{{Path: ".", Type: TypeDirectory}, {Path: "link", Type: TypeSymlink, LinkTarget: target}}}
			if _, err := Digest(m, testLimits()); err != ErrInvalid {
				t.Fatalf("invalid target accepted: %v", err)
			}
			if _, _, err := runtimeTarget(target, testLimits()); err != ErrUnsupported {
				t.Fatalf("resolver accepted invalid target: %v", err)
			}
		}
	})
}

func TestRuntimeLinkTextRemainsDigestAuthority(t *testing.T) {
	m := Manifest{Version: 1, Entries: []Entry{
		{Path: ".", Type: TypeDirectory},
		{Path: "file", Type: TypeFile, SHA256: strings.Repeat("a", 64)},
		{Path: "link", Type: TypeSymlink, LinkTarget: "file"},
	}}
	d1, err := Digest(m, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	m.Entries[2].LinkTarget = "/file"
	d2, err := Digest(m, testLimits())
	if err != nil || d1 == d2 {
		t.Fatal("equivalent runtime targets lost distinct manifest identity", err)
	}
}

func TestRejectXattrs(t *testing.T) {
	for _, name := range []string{"user.artifactfs", "system.posix_acl_access", "security.capability"} {
		t.Run(name, func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			file := filepath.Join(p, "file")
			write(t, file, "x")
			m := fixtureManifest(t, p)
			value := []byte("metadata")
			switch name {
			case "system.posix_acl_access":
				value = make([]byte, 4+5*8)
				binary.LittleEndian.PutUint32(value, 2)
				for i, tag := range []uint16{1, 2, 4, 16, 32} {
					offset := 4 + i*8
					binary.LittleEndian.PutUint16(value[offset:], tag)
					binary.LittleEndian.PutUint16(value[offset+2:], 4)
					id := uint32(0xffffffff)
					if tag == 2 {
						id = uint32(os.Geteuid() + 1)
					}
					binary.LittleEndian.PutUint32(value[offset+4:], id)
				}
			case "security.capability":
				value = make([]byte, 20)
				binary.LittleEndian.PutUint32(value, 0x02000001)
				binary.LittleEndian.PutUint32(value[4:], 1<<10)
			}
			if err := syscall.Setxattr(file, name, value, 0); err != nil {
				t.Skipf("cannot create %s fixture on this filesystem/as euid: %v", name, err)
			}
			if _, err := sealManifest(t, st, KindCode, ids.New(), m); !errors.Is(err, ErrUnsupported) {
				t.Fatal(err)
			}
		})
	}
}

func TestCapacityCancellationAndCleanupRetry(t *testing.T) {
	limits := testLimits()
	limits.Generations, limits.Staging, limits.Bytes, limits.FileBytes = 1, 1, 100, 100
	s := newStore(t, limits)
	st, p := begin(t, s, 100)
	write(t, filepath.Join(p, "file"), "x")
	if _, err := s.Begin(context.Background(), 1); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := fixtureManifest(t, p)
	digest, _ := Digest(m, limits)
	if _, err := st.Seal(ctx, KindCode, ids.New(), m, digest); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	actualRemove := s.removeAll
	s.removeAll = func(string) error { return errors.New("private diagnostic must not escape") }
	if err := st.Abort(); err != ErrIO || s.bytes != 100 || s.stages != 1 {
		t.Fatal("failed cleanup freed capacity or leaked detail", err)
	}
	s.removeAll = actualRemove
	if err := st.Abort(); err != nil {
		t.Fatal(err)
	}
	_ = st.Abort()
	if s.bytes != 0 || s.stages != 0 {
		t.Fatal("cleanup did not return reservations")
	}
	if _, err := s.Begin(ctx, 1); !errors.Is(err, context.Canceled) || s.bytes != 0 {
		t.Fatal(err)
	}
	st, _ = begin(t, s, 100)
	_ = st.Abort()
}

// Deterministic cancellation during chunked verification, without a timing race.
type cancelAfter struct {
	context.Context
	calls, at int
}

func (c *cancelAfter) Err() error {
	c.calls++
	if c.calls >= c.at {
		return context.Canceled
	}
	return nil
}

func TestCancelDuringHashAndBeginRollback(t *testing.T) {
	s := newStore(t, testLimits())
	st, p := begin(t, s, 100000)
	write(t, filepath.Join(p, "large"), string(make([]byte, 65000)))
	m := fixtureManifest(t, p)
	digest, _ := Digest(m, s.limits)
	ctx := &cancelAfter{Context: context.Background(), at: 7}
	if _, err := st.Seal(ctx, KindCode, ids.New(), m, digest); !errors.Is(err, context.Canceled) || s.bytes != 100000 {
		t.Fatal("hash cancellation/accounting", err)
	}
	if err := st.Abort(); err != nil {
		t.Fatal(err)
	}
	realRemove := s.removeAll
	s.removeAll = func(string) error { return ErrIO }
	ctx = &cancelAfter{Context: context.Background(), at: 2}
	failedStage, err := s.Begin(ctx, 100)
	if !errors.Is(err, context.Canceled) || failedStage == nil || s.bytes != 100 {
		t.Fatal("Begin lost explicit cleanup handle", err)
	}
	s.removeAll = realRemove
	if err := failedStage.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestPairPartialReleaseAndClose(t *testing.T) {
	s := newStore(t, testLimits())
	code := sealed(t, s, KindCode, ids.New())
	runtime := sealed(t, s, KindRuntime, "python")
	if _, err := PreparePaths(s, code, s, "unknown"); err == nil || s.records[code].refs != 0 {
		t.Fatal("partial acquisition leaked")
	}
	if _, err := PreparePaths(s, runtime, s, code); err != ErrInvalid || s.records[code].refs != 0 || s.records[runtime].refs != 0 {
		t.Fatal("wrong kind acquired pair")
	}
	pair, err := PreparePaths(s, code, s, runtime)
	if err != nil {
		t.Fatal(err)
	}
	a, b := pair.Paths()
	if a == "" || b == "" || a == b {
		t.Fatal("invalid prepared paths")
	}
	if err := s.Close(); err != ErrBusy {
		t.Fatal(err)
	}
	if _, err := s.Acquire(code); err != ErrClosed {
		t.Fatal(err)
	}
	if _, err := s.Begin(context.Background(), 1); err != ErrClosed {
		t.Fatal(err)
	}
	if err := s.Retire(code); err != nil {
		t.Fatal(err)
	}
	if err := s.Reclaim(code); err != ErrBusy {
		t.Fatal(err)
	}
	pair.Release()
	pair.Release()
	if err := s.Reclaim(code); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRetireReclaimRace(t *testing.T) {
	s := newStore(t, testLimits())
	id := sealed(t, s, KindRuntime, "node")
	pin, err := s.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				l, err := s.Acquire(id)
				if err == nil {
					if _, err := os.Stat(l.Path()); err != nil {
						t.Error("deleted while leased", err)
					}
					l.Release()
					l.Release()
				} else if err != ErrRetired {
					t.Error(err)
				}
				_ = s.Retire(id)
				if err := s.Reclaim(id); err != ErrBusy {
					t.Error("deleted while original pin held", err)
				}
			}
		}()
	}
	wg.Wait()
	// Even a copied handle shares release bookkeeping.
	copied := *pin
	pin.Release()
	copied.Release()
	if err := s.Reclaim(id); err != nil {
		t.Fatal(err)
	}
}

func TestReclaimFailureAndIOOutsideLock(t *testing.T) {
	s := newStore(t, testLimits())
	id := sealed(t, s, KindRuntime, "node")
	_ = s.Retire(id)
	started, proceed, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	actualRemove := s.removeAll
	s.removeAll = func(p string) error {
		close(started)
		<-proceed
		if err := os.Remove(filepath.Join(p, "handler")); err != nil {
			return err
		}
		return ErrIO
	}
	go func() { done <- s.Reclaim(id) }()
	<-started
	if _, err := s.Acquire(id); err != ErrRetired {
		t.Fatal(err)
	}
	if err := s.Reclaim(id); err != ErrBusy {
		t.Fatal(err)
	}
	if err := s.Retire(id); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-done; err != ErrIO || s.bytes != 100 {
		t.Fatal("cleanup failure lost accounting", err)
	}
	s.removeAll = actualRemove
	if err := s.Reclaim(id); err != nil || s.bytes != 0 {
		t.Fatal(err)
	}
}

func TestConstructorAndManifestBounds(t *testing.T) {
	for _, which := range []string{"zero", "generations", "leases", "staging", "entries", "path", "link", "manifest", "file", "bytes"} {
		t.Run(which, func(t *testing.T) {
			l := testLimits()
			switch which {
			case "zero":
				l = Limits{}
			case "generations":
				l.Generations = 0
			case "leases":
				l.Leases = 0
			case "staging":
				l.Staging = l.Generations + 1
			case "entries":
				l.Entries = 0
			case "path":
				l.PathBytes = 4097
			case "link":
				l.LinkBytes = 4097
			case "manifest":
				l.ManifestBytes = 64<<20 + 1
			case "file":
				l.FileBytes = l.Bytes + 1
			case "bytes":
				l.Bytes = 1<<50 + 1
			}
			if _, err := New(t.TempDir(), l); err != ErrInvalid {
				t.Fatal(err)
			}
		})
	}
	t.Run("root", func(t *testing.T) {
		s := newStore(t, testLimits())
		if _, err := New(s.root, testLimits()); err != ErrBusy {
			t.Fatal("multiple writers", err)
		}
		root := t.TempDir()
		_ = os.Chmod(root, 0700)
		write(t, filepath.Join(root, "unrecognized"), "x")
		if _, err := New(root, testLimits()); err != ErrInvalid {
			t.Fatal("adopted existing state", err)
		}
		link := filepath.Join(t.TempDir(), "link")
		_ = os.Symlink(root, link)
		if _, err := New(link, testLimits()); err != ErrInvalid {
			t.Fatal("accepted symlink root", err)
		}
	})
	s := newStore(t, testLimits())
	st, p := begin(t, s, 100)
	write(t, filepath.Join(p, "file"), "hello")
	base := fixtureManifest(t, p)
	for _, which := range []string{"null", "version", "duplicate", "path", "mode", "file-size", "digest", "extraneous", "root", "entry-cap", "manifest-cap", "path-cap", "link-cap"} {
		t.Run(which, func(t *testing.T) {
			m := Manifest{Version: base.Version, Entries: append([]Entry(nil), base.Entries...)}
			l := s.limits
			switch which {
			case "null":
				m.Entries = nil
			case "version":
				m.Version = 2
			case "duplicate":
				m.Entries = append(m.Entries, m.Entries[0])
			case "path":
				m.Entries[1].Path = "../escape"
			case "mode":
				m.Entries[1].Mode = 010000
			case "file-size":
				m.Entries[1].Size = l.FileBytes + 1
			case "digest":
				m.Entries[1].SHA256 = "not-a-digest"
			case "extraneous":
				m.Entries[1].LinkTarget = "file"
			case "root":
				m.Entries = m.Entries[1:]
			case "entry-cap":
				l.Entries = 1
			case "manifest-cap":
				l.ManifestBytes = 256
			case "path-cap":
				l.PathBytes = 3
			case "link-cap":
				l.LinkBytes = 3
				m.Entries[1] = Entry{Path: "a", Type: TypeSymlink, LinkTarget: "long"}
			}
			if _, err := Digest(m, l); err == nil {
				t.Fatal("accepted invalid or unbounded manifest")
			}
		})
	}
	d1, _ := Digest(base, s.limits)
	sort.Slice(base.Entries, func(i, j int) bool { return base.Entries[i].Path > base.Entries[j].Path })
	d2, _ := Digest(base, s.limits)
	if d1 != d2 {
		t.Fatal("canonical digest depends on input order")
	}
	for _, owner := range []string{"", "legacy", "NODE"} {
		if validOwner(KindRuntime, owner) {
			t.Fatal("unknown runtime accepted")
		}
	}
	_ = st
}

func TestObservedBounds(t *testing.T) {
	for _, which := range []string{"reservation", "file", "entries", "path", "link"} {
		t.Run(which, func(t *testing.T) {
			l := testLimits()
			s := newStore(t, l)
			reserve := int64(100)
			if which == "reservation" {
				reserve = 1
			}
			st, p := begin(t, s, reserve)
			write(t, filepath.Join(p, "file"), "hello")
			m := fixtureManifest(t, p)
			switch which {
			case "file":
				s.limits.FileBytes = 4
				// A small expected file does not permit a larger observed file.
				m.Entries[1].Size = 4
			case "entries":
				write(t, filepath.Join(p, "extra"), "x")
				s.limits.Entries = 2
			case "path":
				write(t, filepath.Join(p, "long-path"), "x")
				s.limits.PathBytes = 4
			case "link":
				_ = os.Symlink("very-long-target", filepath.Join(p, "l"))
				s.limits.LinkBytes = 4
			}
			digest, err := Digest(m, s.limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.Seal(context.Background(), KindCode, ids.New(), m, digest); err == nil {
				t.Fatal("observed bounds not enforced")
			}
		})
	}
}

func TestZeroOpaqueHandles(t *testing.T) {
	var st Staging
	if _, err := st.Path(); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := st.Seal(context.Background(), KindCode, ids.New(), Manifest{}, ""); err != ErrInvalid {
		t.Fatal(err)
	}
	if err := st.Abort(); err != ErrInvalid {
		t.Fatal(err)
	}
	var lease Lease
	lease.Release()
	if lease.Path() != "" || lease.Identity() != (Identity{}) {
		t.Fatal("zero handle has authority")
	}
}

var _ context.Context = (*cancelAfter)(nil)

func TestIndependentCapacityLimits(t *testing.T) {
	for _, which := range []string{"generations", "staging", "bytes", "leases"} {
		t.Run(which, func(t *testing.T) {
			l := testLimits()
			switch which {
			case "generations":
				l.Generations, l.Staging = 1, 1
			case "staging":
				l.Staging = 1
			case "bytes":
				l.Bytes, l.FileBytes = 100, 100
			case "leases":
				l.Leases = 1
			}
			s := newStore(t, l)
			if which == "staging" {
				_, _ = begin(t, s, 1)
			} else {
				id := sealed(t, s, KindCode, ids.New())
				if which == "leases" {
					pin, err := s.Acquire(id)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := s.Acquire(id); err != ErrCapacity {
						t.Fatal("lease limit not enforced", err)
					}
					if _, err := PreparePaths(s, id, s, "unknown"); err != ErrCapacity {
						t.Fatal(err)
					}
					pin.Release()
					return
				}
			}
			if _, err := s.Begin(context.Background(), 1); err != ErrCapacity {
				t.Fatal("independent limit not enforced", err)
			}
		})
	}
}

type blockingContext struct {
	context.Context
	once             sync.Once
	started, proceed chan struct{}
}

func (c *blockingContext) Err() error {
	c.once.Do(func() {
		close(c.started)
		<-c.proceed
	})
	return nil
}

func TestCloseWhileVerifyingAndCancellationAfterRename(t *testing.T) {
	t.Run("close", func(t *testing.T) {
		s := newStore(t, testLimits())
		st, p := begin(t, s, 100)
		write(t, filepath.Join(p, "file"), "x")
		m := fixtureManifest(t, p)
		digest, _ := Digest(m, s.limits)
		ctx := &blockingContext{Context: context.Background(), started: make(chan struct{}), proceed: make(chan struct{})}
		type result struct {
			id  string
			err error
		}
		done := make(chan result, 1)
		go func() {
			id, err := st.Seal(ctx, KindCode, ids.New(), m, digest)
			done <- result{id, err}
		}()
		<-ctx.started
		if err := st.Abort(); err != ErrBusy {
			t.Fatal(err)
		}
		if err := s.Close(); err != ErrBusy {
			t.Fatal(err)
		}
		close(ctx.proceed)
		res := <-done
		if res.err != ErrClosed || res.id == "" || s.records[res.id].phase != retired {
			t.Fatal("close published generation", res)
		}
		if err := s.Reclaim(res.id); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancel-after-rename", func(t *testing.T) {
		s := newStore(t, testLimits())
		makeStage := func() (*Staging, Manifest, string) {
			st, p := begin(t, s, 100)
			write(t, filepath.Join(p, "file"), "x")
			m := fixtureManifest(t, p)
			d, _ := Digest(m, s.limits)
			return st, m, d
		}
		st, m, d := makeStage()
		counter := &cancelAfter{Context: context.Background(), at: 1000000}
		if _, err := st.Seal(counter, KindCode, ids.New(), m, d); err != nil {
			t.Fatal(err)
		}
		st, m, d = makeStage()
		ctx := &cancelAfter{Context: context.Background(), at: counter.calls}
		id, err := st.Seal(ctx, KindCode, ids.New(), m, d)
		if !errors.Is(err, context.Canceled) || id == "" || s.records[id].phase != retired {
			t.Fatal("canceled rename must return retired cleanup identity", id, err)
		}
		if _, err := s.Acquire(id); err != ErrRetired {
			t.Fatal(err)
		}
		if err := s.Reclaim(id); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCanonicalPunctuationAndOwnerValidation(t *testing.T) {
	s := newStore(t, testLimits())
	st, p := begin(t, s, 100)
	write(t, filepath.Join(p, "!before-dot"), "x")
	m := fixtureManifest(t, p)
	if _, err := sealManifest(t, st, KindCode, ids.New(), m); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{
		"00000000-0000-0000-0000-000000000000",
		"01939a6e-3a4f-0b8c-9d2e-f1a2b3c4d5e6",
		"01939a6e-3a4f-7b8c-1d2e-f1a2b3c4d5e6",
		"01939A6e-3a4f-7b8c-9d2e-f1a2b3c4d5e6",
	} {
		if validOwner(KindCode, owner) {
			t.Fatal("noncanonical owner accepted")
		}
	}
}

func TestDirectoryLinkCyclesAndDestinationCollision(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory-link", true: "directory-cycle"}[cycle], func(t *testing.T) {
			s := newStore(t, testLimits())
			st, p := begin(t, s, 100)
			for _, dir := range []string{"a", "b"} {
				if err := os.Mkdir(filepath.Join(p, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("/b", filepath.Join(p, "a", "link")); err != nil {
				t.Fatal(err)
			}
			if cycle {
				if err := os.Symlink("/a", filepath.Join(p, "b", "link")); err != nil {
					t.Fatal(err)
				}
			}
			_, err := sealManifest(t, st, KindRuntime, "node", fixtureManifest(t, p))
			if cycle && err != ErrUnsupported || !cycle && err != nil {
				t.Fatal("directory traversal cycle result", err)
			}
		})
	}
	t.Run("destination-collision", func(t *testing.T) {
		s := newStore(t, testLimits())
		st, p := begin(t, s, 100)
		m := fixtureManifest(t, p)
		destination := filepath.Join(s.root, "generation-"+st.rec.identity.Generation)
		if err := os.Mkdir(destination, 0700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(destination) })
		write(t, filepath.Join(destination, "sentinel"), "do not overwrite")
		if _, err := sealManifest(t, st, KindCode, ids.New(), m); err != ErrIntegrity {
			t.Fatal("overwrote unexpected destination", err)
		}
		data, err := os.ReadFile(filepath.Join(destination, "sentinel"))
		if err != nil || string(data) != "do not overwrite" {
			t.Fatal("unexpected destination was changed")
		}
	})
}

type actionContext struct {
	context.Context
	calls, at int
	act       func()
}

func (c *actionContext) Err() error {
	c.calls++
	if c.calls == c.at {
		c.act()
	}
	return nil
}

func TestNoFollowAndChangeDuringHash(t *testing.T) {
	for _, which := range []string{"replace-symlink", "stat-open-change", "mid-hash-change"} {
		t.Run(which, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "file")
			write(t, p, string(make([]byte, 65000)))
			info, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			want := metadata(info.Sys().(*syscall.Stat_t))
			ctx := context.Context(context.Background())
			switch which {
			case "replace-symlink":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/not-a-host-read-target", p); err != nil {
					t.Fatal(err)
				}
			case "stat-open-change":
				if err := os.Chmod(p, 0700); err != nil {
					t.Fatal(err)
				}
			case "mid-hash-change":
				ctx = &actionContext{Context: context.Background(), at: 2, act: func() {
					// Deterministic change after one chunk, not a sleep/racy test.
					write(t, p, "changed")
				}}
			}
			if _, err := fileHash(ctx, p, want); err != ErrIntegrity {
				t.Fatal("no-follow/change check did not deny", err)
			}
		})
	}
}
