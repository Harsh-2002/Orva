//go:build linux

package workloadspawn

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
)

// Test-only producer: the caller exclusively owns a release package and supplies
// its checksum from a separately trusted release channel, never from the package.
func verifiedRuntimePackage(name, expected string) (*os.File, error) {
	if !validDigest(expected) {
		return nil, errors.New("missing trusted package checksum")
	}
	fd, err := syscall.Open(name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > 1<<30 {
		_ = f.Close()
		return nil, errors.New("runtime package must be a bounded regular file")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (1<<30)+1))
	if err != nil || n > 1<<30 || hex.EncodeToString(h.Sum(nil)) != expected {
		_ = f.Close()
		return nil, errors.New("runtime package checksum/size mismatch")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func TestRuntimePackageRejectsSpecialInputs(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("reviewed input"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := digest("reviewed input")
	link := filepath.Join(root, "link")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{root, link} {
		f, err := verifiedRuntimePackage(name, expected)
		if f != nil {
			_ = f.Close()
		}
		if err == nil {
			t.Fatal("package alias or nonregular input accepted")
		}
	}
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := verifiedRuntimePackage(fifo, expected)
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO input accepted")
		}
	case <-time.After(2 * time.Second):
		// Unblock the historical blocking-open path so a failed test leaks no goroutine.
		wake, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			defer wake.Close()
			<-done
		}
		t.Fatal("package type validation blocked opening a FIFO")
	}
	f, err := verifiedRuntimePackage(regular, expected)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func packagePath(name string) (string, error) {
	if name == "" || path.IsAbs(name) {
		return "", errors.New("empty/absolute package path")
	}
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		name = "."
	}
	if name != path.Clean(name) || path.IsAbs(name) || name == ".." ||
		strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\x00\\") {
		return "", errors.New("noncanonical package path")
	}
	return name, nil
}

// Manifest expectations come from the authenticated archive stream, not a walk
// of an arbitrary installed tree. Unsupported metadata is never stripped.
func unpackRuntimePackage(root string, input io.Reader, limits artifactfs.Limits) (artifactfs.Manifest, error) {
	m := artifactfs.Manifest{Version: 1}
	entries := map[string]int{}
	groups := map[string][]string{}
	leaders := map[string]string{}
	tr := tar.NewReader(io.LimitReader(input, limits.Bytes+int64(limits.Entries)*8192+1))
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, err
		}
		name, err := packagePath(h.Name)
		if err != nil || len(name) > limits.PathBytes || len(h.Linkname) > limits.LinkBytes {
			return m, fmt.Errorf("package path rejected: %q", h.Name)
		}
		if _, exists := entries[name]; exists || len(entries) >= limits.Entries {
			return m, fmt.Errorf("duplicate/excess package entry: %s", name)
		}
		for key := range h.PAXRecords {
			switch key {
			case "path", "linkpath", "mtime", "atime", "ctime":
			default:
				return m, fmt.Errorf("unsupported package metadata at %s: %s", name, key)
			}
		}
		if len(h.Xattrs) != 0 || h.Mode < 0 || h.Mode > 07777 ||
			h.Uid < 0 || uint64(h.Uid) > 1<<32-1 || h.Gid < 0 || uint64(h.Gid) > 1<<32-1 {
			return m, fmt.Errorf("unsupported package metadata at %s", name)
		}
		if name != "." {
			parent, exists := entries[path.Dir(name)]
			if !exists || m.Entries[parent].Type != artifactfs.TypeDirectory {
				return m, fmt.Errorf("package parent must be an earlier explicit directory: %s", name)
			}
		}
		e := artifactfs.Entry{Path: name, Mode: uint32(h.Mode), UID: uint32(h.Uid), GID: uint32(h.Gid)}
		dest := filepath.Join(root, name)
		switch h.Typeflag {
		case tar.TypeDir:
			e.Type = artifactfs.TypeDirectory
			if name != "." {
				if err := os.Mkdir(dest, 0700); err != nil {
					return m, err
				}
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > limits.FileBytes || h.Size > limits.Bytes-total {
				return m, fmt.Errorf("package bytes exceeded at %s", name)
			}
			e.Type, e.Size = artifactfs.TypeFile, h.Size
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return m, err
			}
			hash := sha256.New()
			n, copyErr := io.CopyN(io.MultiWriter(f, hash), tr, h.Size)
			closeErr := f.Close()
			if copyErr != nil || closeErr != nil || n != h.Size {
				return m, fmt.Errorf("package file copy failed: %s", name)
			}
			e.SHA256 = hex.EncodeToString(hash.Sum(nil))
			groups[name] = []string{name}
			leaders[name] = name
			total += e.Size
		case tar.TypeLink:
			target, err := packagePath(h.Linkname)
			index, exists := entries[target]
			if err != nil || !exists || m.Entries[index].Type != artifactfs.TypeFile {
				return m, fmt.Errorf("unsupported package hardlink: %s -> %s", name, h.Linkname)
			}
			source := m.Entries[index]
			if e.Mode != source.Mode || e.UID != source.UID || e.GID != source.GID || source.Size > limits.Bytes-total {
				return m, fmt.Errorf("package hardlink metadata/bytes mismatch: %s", name)
			}
			e.Type, e.Size, e.SHA256 = artifactfs.TypeFile, source.Size, source.SHA256
			if err := os.Link(filepath.Join(root, target), dest); err != nil {
				return m, err
			}
			group := leaders[target]
			leaders[name] = group
			groups[group] = append(groups[group], name)
			total += e.Size
		case tar.TypeSymlink:
			e.Type, e.LinkTarget = artifactfs.TypeSymlink, h.Linkname
			if err := os.Symlink(h.Linkname, dest); err != nil {
				return m, err
			}
		default:
			return m, fmt.Errorf("unsupported package entry type at %s: %d", name, h.Typeflag)
		}
		if name == "." && e.Type != artifactfs.TypeDirectory {
			return m, errors.New("package root is not a directory")
		}
		entries[name] = len(m.Entries)
		m.Entries = append(m.Entries, e)
	}
	for _, names := range groups {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		for _, name := range names {
			m.Entries[entries[name]].HardlinkGroup = names[0]
		}
	}
	// Children first; parent permissions cannot prevent completing production.
	for i := len(m.Entries) - 1; i >= 0; i-- {
		e := m.Entries[i]
		dest := filepath.Join(root, e.Path)
		if err := os.Lchown(dest, int(e.UID), int(e.GID)); err != nil {
			return m, err
		}
		if e.Type != artifactfs.TypeSymlink {
			mode := os.FileMode(e.Mode & 0777)
			if e.Mode&04000 != 0 {
				mode |= os.ModeSetuid
			}
			if e.Mode&02000 != 0 {
				mode |= os.ModeSetgid
			}
			if e.Mode&01000 != 0 {
				mode |= os.ModeSticky
			}
			if err := os.Chmod(dest, mode); err != nil {
				return m, err
			}
		}
	}
	if _, err := artifactfs.Digest(m, limits); err != nil {
		return m, err
	}
	return m, nil
}

func packageFixture(t *testing.T, headers []*tar.Header, contents []string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	for i, h := range headers {
		h.Uid, h.Gid = os.Geteuid(), os.Getegid()
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, contents[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func runtimePackageLimits() artifactfs.Limits {
	return artifactfs.Limits{Generations: 4, Leases: 8, Staging: 1, Entries: 32768,
		PathBytes: 4096, LinkBytes: 4096, ManifestBytes: 64 << 20, Bytes: 1 << 30, FileBytes: 256 << 20}
}

func TestRuntimePackageProducer(t *testing.T) {
	limits := runtimePackageLimits()
	for _, scenario := range []string{"valid", "dangling", "xattr", "escape", "duplicate", "symlink-parent", "device", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			limits := limits
			headers := []*tar.Header{
				{Name: "./", Mode: 0755, Typeflag: tar.TypeDir},
				{Name: "./bin/", Mode: 0755, Typeflag: tar.TypeDir},
				{Name: "./bin/runtime", Mode: 0755, Size: 3, Typeflag: tar.TypeReg},
				{Name: "./alias", Mode: 0777, Typeflag: tar.TypeSymlink, Linkname: "/bin/runtime"},
				{Name: "./hard", Mode: 0755, Typeflag: tar.TypeLink, Linkname: "./bin/runtime"},
				{Name: "./aardvark", Mode: 0755, Typeflag: tar.TypeLink, Linkname: "./hard"},
			}
			contents := []string{"", "", "bin", "", "", ""}
			switch scenario {
			case "dangling":
				headers[3].Linkname = "/missing-manpage"
			case "xattr":
				headers[2].Xattrs = map[string]string{"user.fixture": "denied"}
			case "escape":
				headers[2].Name = "../escaped"
			case "duplicate":
				headers[3].Name = "./bin/runtime"
			case "symlink-parent":
				headers[4].Name = "./alias/child"
			case "device":
				headers[3].Typeflag = tar.TypeChar
			case "capacity":
				limits.FileBytes = 2
			}
			raw := packageFixture(t, headers, contents)
			archive := filepath.Join(t.TempDir(), "package.tar")
			if err := os.WriteFile(archive, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if f, err := verifiedRuntimePackage(archive, digest("wrong")); err == nil {
				_ = f.Close()
				t.Fatal("wrong external checksum accepted")
			}
			f, err := verifiedRuntimePackage(archive, digest(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			root := t.TempDir()
			_ = os.Chmod(root, 0700)
			store, err := artifactfs.New(root, limits)
			if err != nil {
				t.Fatal(err)
			}
			stage, err := store.Begin(t.Context(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			dest, _ := stage.Path()
			m, produceErr := unpackRuntimePackage(dest, f, limits)
			var generation string
			var sealErr error
			if produceErr == nil {
				hash, err := artifactfs.Digest(m, limits)
				if err != nil {
					t.Fatal(err)
				}
				generation, sealErr = stage.Seal(t.Context(), artifactfs.KindRuntime, "node", m, hash)
			}
			if scenario == "valid" {
				if produceErr != nil || sealErr != nil {
					t.Fatalf("producer=%v seal=%v", produceErr, sealErr)
				}
			} else if produceErr == nil && sealErr == nil {
				t.Fatal("unsupported package accepted")
			}
			if scenario == "dangling" && (produceErr != nil || !errors.Is(sealErr, artifactfs.ErrUnsupported)) {
				t.Fatalf("dangling package did not reach unchanged strict verifier: %v %v", produceErr, sealErr)
			}
			if generation != "" {
				if err := store.Retire(generation); err != nil {
					t.Fatal(err)
				}
				if err := store.Reclaim(generation); err != nil {
					t.Fatal(err)
				}
			} else if err := stage.Abort(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimePackagePaths(t *testing.T) {
	for _, name := range []string{"", "/", "/absolute", "../escape", "./../escape", "a/../b", "a//b", `a\b`} {
		if _, err := packagePath(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}
