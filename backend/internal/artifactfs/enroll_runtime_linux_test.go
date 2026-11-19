package artifactfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInitialRuntimeReusesUnrecordedSeal(t *testing.T) {
	limits := testLimits()
	limits.Generations, limits.Staging = 1, 1
	store, err := NewPersistent(t.TempDir(), limits)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src := t.TempDir()
	bin := filepath.Join(src, "bin")
	if err := os.WriteFile(bin, []byte("runtime"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"tmp", "var/tmp"} {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, os.ModeSticky|0777); err != nil {
			t.Fatal(err)
		}
	}
	check := func(path string) error {
		if path == "" || path == src {
			t.Fatalf("validated mutable source instead of store: %q", path)
		}
		for _, rel := range []string{"tmp", "var/tmp"} {
			info, err := os.Stat(filepath.Join(path, rel))
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode() & (os.ModeSticky | os.ModePerm); got != os.ModeSticky|0777 {
				t.Fatalf("%s permissions = %v, want sticky and 0777", rel, got)
			}
		}
		return nil
	}
	seal := func() (Identity, error) {
		return importInstalledRuntime(t.Context(), store, "node", src, checkInitialRuntimeEntry, check)
	}
	first, err := seal()
	if err != nil || first.Generation == "" {
		t.Fatalf("initial seal: %v, %v", first, err)
	}
	if again, err := seal(); err != nil || first != again {
		t.Fatalf("retry at capacity selected %v, %v; want %v", again, err, first)
	}
	if err := os.WriteFile(bin, []byte("changed"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := seal(); !errors.Is(err, ErrCapacity) {
		t.Fatalf("different source reused the old seal: %v", err)
	}
	if err := os.Chmod(bin, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := seal(); !errors.Is(err, ErrUnsupported) || !errors.Is(err, ErrOwnership) {
		t.Fatalf("writable runtime accepted: %v", err)
	}
}

func TestRuntimeXattrListFailure(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "file"), []byte("runtime"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := scanTree(t.Context(), src, testLimits(), 1024, func(rel string, _ os.FileInfo) error {
		if rel == "file" {
			return os.Remove(filepath.Join(src, rel))
		}
		return nil
	})
	if !errors.Is(err, ErrIO) || !errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, ErrUnsupported) || errors.Is(err, ErrOwnership) ||
		!strings.Contains(err.Error(), "file: ") || !strings.Contains(err.Error(), "llistxattr failed") {
		t.Fatalf("list failure = %v, want path and filesystem error", err)
	}
}

func TestEnrollmentRuntimeCopy(t *testing.T) {
	base := newStore(t, testLimits())
	src := t.TempDir()
	d := filepath.Join(src, "etc", "alternatives")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"awk.1.gz": "/usr/share/man/man1/mawk.1.gz", "which.de1.gz": "/usr/share/man/de/man1/which.debianutils.1.gz",
		"escape": "/bin/absent",
	} {
		if err := os.Symlink(target, filepath.Join(d, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sealInstalledRuntime(t.Context(), base, "node", src); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("non-manual dangling link accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(d, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "retained"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := sealInstalledRuntime(t.Context(), base, "node", src)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := base.Acquire(id.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if _, err := os.Lstat(filepath.Join(lease.Path(), "etc", "alternatives", "awk.1.gz")); !os.IsNotExist(err) {
		t.Fatalf("dangling manual was copied: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(d, "awk.1.gz")); err != nil {
		t.Fatalf("legacy manual link was changed: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(lease.Path(), "etc", "alternatives", "retained")); err != nil || string(raw) != "data" {
		t.Fatalf("unexpected retained file: %v", err)
	}
}

// initialRuntimeStore binds a fresh store the way the managed owner does, so
// SealInitialRuntime accepts it. Test cleanup unbinds it before closing.
func initialRuntimeStore(t *testing.T) *Store {
	t.Helper()
	limits := testLimits()
	limits.Generations, limits.Staging = 4, 1
	s, err := NewPersistent(t.TempDir(), limits)
	if err != nil {
		t.Fatal(err)
	}
	s.boundKind, s.binding = KindRuntime, &ownerState{}
	t.Cleanup(func() {
		s.binding = nil
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

type fakeInfo struct {
	mode os.FileMode
	stat syscall.Stat_t
}

func (f fakeInfo) Name() string       { return "entry" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return &f.stat }

func TestInitialRuntimeEntryRules(t *testing.T) {
	euid := uint32(os.Geteuid())
	other := euid + 1
	if other == 0 {
		other = 2
	}
	ownerRule := fmt.Sprintf("owned by uid %d, want root (the server user)", other)
	if euid != 0 {
		ownerRule = fmt.Sprintf("owned by uid %d, want root or the server user (uid %d)", other, euid)
	}
	cases := []struct {
		name, rel, want string
		info            fakeInfo
		ownership       bool
	}{
		{"foreign-owner", "usr/bin/node", ownerRule, fakeInfo{mode: 0755, stat: syscall.Stat_t{Uid: other}}, true},
		{"group-writable", "var/lib", "var/lib: group/other-writable (mode 0775)", fakeInfo{mode: os.ModeDir | 0775, stat: syscall.Stat_t{Uid: euid}}, true},
		{"other-writable-file", "bin/node", "bin/node: group/other-writable (mode 0666)", fakeInfo{mode: 0666, stat: syscall.Stat_t{Uid: euid}}, true},
		{"tmp-entry", "tmp/sock", "tmp/sock: runtime trees may not contain tmp/ or var/tmp/ entries", fakeInfo{mode: 0644, stat: syscall.Stat_t{Uid: euid}}, false},
		{"var-tmp-entry", "var/tmp/lock", "var/tmp/lock: runtime trees may not contain tmp/ or var/tmp/ entries", fakeInfo{mode: 0644, stat: syscall.Stat_t{Uid: euid}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkInitialRuntimeEntry(tc.rel, tc.info)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("refusal = %v, want ErrUnsupported", err)
			}
			if errors.Is(err, ErrOwnership) != tc.ownership {
				t.Fatalf("refusal = %v, want ownership category %v", err, tc.ownership)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
	for name, info := range map[string]fakeInfo{
		"sticky-tmp":     {mode: os.ModeDir | os.ModeSticky | 0777, stat: syscall.Stat_t{Uid: euid}},
		"symlink-bits":   {mode: os.ModeSymlink | 0777, stat: syscall.Stat_t{Uid: euid}},
		"root-owned-dir": {mode: os.ModeDir | 0755, stat: syscall.Stat_t{Uid: 0}},
	} {
		rel := "."
		if name == "sticky-tmp" {
			rel = "tmp"
		}
		if err := checkInitialRuntimeEntry(rel, info); err != nil {
			t.Fatalf("%s refused: %v", name, err)
		}
	}
}

func TestSpecialFileRules(t *testing.T) {
	for mode, want := range map[uint32]string{
		syscall.S_IFCHR: "device node", syscall.S_IFBLK: "device node",
		syscall.S_IFIFO: "named pipe", syscall.S_IFSOCK: "socket", syscall.S_IFREG: "unsupported file type",
	} {
		if got := specialFileRule(mode); got != want {
			t.Fatalf("mode %#o = %q, want %q", mode, got, want)
		}
	}
}

func TestInitialRuntimeRefusals(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, src string)
		want  string
	}{
		{name: "writable-entry", build: func(t *testing.T, src string) {
			p := filepath.Join(src, "bin")
			if err := os.WriteFile(p, []byte("runtime"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, 0777); err != nil {
				t.Fatal(err)
			}
		}, want: "bin: group/other-writable (mode 0777)"},
		{name: "tmp-entry", build: func(t *testing.T, src string) {
			if err := os.MkdirAll(filepath.Join(src, "var", "tmp"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "var", "tmp", "lock"), []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
		}, want: "var/tmp/lock: runtime trees may not contain tmp/ or var/tmp/ entries"},
		{name: "named-pipe", build: func(t *testing.T, src string) {
			if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0600); err != nil {
				t.Fatal(err)
			}
		}, want: "pipe: named pipe"},
		{name: "extended-attributes", build: func(t *testing.T, src string) {
			p := filepath.Join(src, "stamped")
			if err := os.WriteFile(p, []byte("runtime"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Setxattr(p, "user.orva-test", []byte("1"), 0); err != nil {
				t.Skipf("filesystem does not store user xattrs: %v", err)
			}
		}, want: "stamped: extended attributes present"},
		{name: "dangling-link", build: func(t *testing.T, src string) {
			if err := os.Symlink("/usr/share/absent", filepath.Join(src, "link")); err != nil {
				t.Fatal(err)
			}
		}, want: "link: link target does not resolve inside the tree"},
		{name: "escaping-link", build: func(t *testing.T, src string) {
			if err := os.Symlink(".", filepath.Join(src, "loop")); err != nil {
				t.Fatal(err)
			}
		}, want: "loop: link escapes the tree"},
		{name: "hard-link-from-outside", build: func(t *testing.T, src string) {
			outside := filepath.Join(filepath.Dir(src), "outside")
			if err := os.WriteFile(outside, []byte("shared"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(outside, filepath.Join(src, "hard")); err != nil {
				t.Skipf("filesystem does not support hard links: %v", err)
			}
		}, want: "hard: hard-link count 2, want 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0755); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(root, "node")
			if err := os.Mkdir(src, 0755); err != nil {
				t.Fatal(err)
			}
			tc.build(t, src)
			_, err := SealInitialRuntime(t.Context(), initialRuntimeStore(t), "node", src, func(string) error { return nil })
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("refusal = %v, want ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestInitialRuntimeSourceSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "node"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "node"), link); err != nil {
		t.Fatal(err)
	}
	_, err := SealInitialRuntime(t.Context(), initialRuntimeStore(t), "node", link, func(string) error { return nil })
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("symlinked source accepted: %v", err)
	}
	if !strings.Contains(err.Error(), link+": source path is a symlink") {
		t.Fatalf("refusal %q does not name the symlinked source", err)
	}
}
