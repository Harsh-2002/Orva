//go:build linux

package workloadspawn

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
)

const runtimeRecipeSourceRevision = "f6f2560a93f4a51840e10de72f5f008e46624336"

var runtimeRecipeBases = map[string]string{
	"node":   "34c7b8f1559a55018827fef7f96a540afb8cfeba665ee89dbbe3170852284ed3",
	"python": "ff692cfc7da5bc142b027b34194da7653db79fe1277945c2363227eb04c56ed5",
}

// Reviewed v1 build-time omissions, never a consumer dangling-link filter.
var runtimeRecipeOmissions = map[string]string{
	"etc/mtab":                       "/proc/mounts",
	"etc/alternatives/awk.1.gz":      "/usr/share/man/man1/mawk.1.gz",
	"etc/alternatives/nawk.1.gz":     "/usr/share/man/man1/mawk.1.gz",
	"etc/alternatives/rmt.8.gz":      "/usr/share/man/man8/rmt-tar.8.gz",
	"etc/alternatives/pager.1.gz":    "/usr/share/man/man1/more.1.gz",
	"etc/alternatives/builtins.7.gz": "/usr/share/man/man7/bash-builtins.7.gz",
	"etc/alternatives/which.1.gz":    "/usr/share/man/man1/which.debianutils.1.gz",
	"etc/alternatives/which.es1.gz":  "/usr/share/man/es/man1/which.debianutils.1.gz",
	"etc/alternatives/which.pl1.gz":  "/usr/share/man/pl/man1/which.debianutils.1.gz",
	"etc/alternatives/which.ja1.gz":  "/usr/share/man/ja/man1/which.debianutils.1.gz",
	"etc/alternatives/which.de1.gz":  "/usr/share/man/de/man1/which.debianutils.1.gz",
	"etc/alternatives/which.sl1.gz":  "/usr/share/man/sl/man1/which.debianutils.1.gz",
	"etc/alternatives/which.it1.gz":  "/usr/share/man/it/man1/which.debianutils.1.gz",
	"etc/alternatives/which.fr1.gz":  "/usr/share/man/fr/man1/which.debianutils.1.gz",
}

type runtimeSourceFile struct{ Source, Destination, SHA256 string }

// Layout matches sdkFiles/sdkDestDir in cmd/orva/setup.go, including type files.
var runtimeRecipeSources = map[string][]runtimeSourceFile{
	"node": {
		{"node/adapter.js", "opt/orva/adapter.js", "9a9bfb6b16cff3979192d948eb031e4232cc8bc576cb013b5e1a71087f2a89f2"},
		{"node/orva.js", "opt/orva/node_modules/orva/orva.js", "1d40a85bbc9c3828f4ba96335c3bf269028c133769df761beec7e81bf6840b85"},
		{"node/orva.d.ts", "opt/orva/node_modules/orva/orva.d.ts", "b5fb143cf947da32246d8b4acaee7e74c14dd3411b89c51e3f8921a836c19689"},
		{"node/package.json", "opt/orva/node_modules/orva/package.json", "ceb18451e68626684a3828c1fcdc275daae247f72bc71b46f7c9da860b5ec448"},
	},
	"python": {
		{"python/adapter.py", "opt/orva/adapter.py", "10a95a6b8c697a520716aa448d2b0e894e94a18f7f216c39aa3b4994d8817c38"},
		{"python/orva.py", "opt/orva/orva.py", "47a4ebef3baa183a461a3a04ea6dd331224049c6bc5b02b62fb86be988fda3bf"},
		{"python/py.typed", "opt/orva/py.typed", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	},
}

func loadRuntimeRecipeSources(root, revision, lang string) (map[string][]byte, error) {
	if revision != runtimeRecipeSourceRevision || runtimeRecipeSources[lang] == nil {
		return nil, errors.New("unreviewed runtime source revision/language")
	}
	out := map[string][]byte{}
	for _, spec := range runtimeRecipeSources[lang] {
		f, err := verifiedRuntimePackage(filepath.Join(root, spec.Source), spec.SHA256)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", spec.Source, err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		closeErr := f.Close()
		if readErr != nil || closeErr != nil || len(raw) > 1<<20 {
			return nil, errors.New("runtime source read/budget failed")
		}
		out[spec.Destination] = raw
	}
	return out, nil
}

// base is the authenticated archive's typed output, not caller tree observations.
func applyRuntimeRecipe(root, lang, baseSHA, sourceRoot, revision string, base artifactfs.Manifest) (artifactfs.Manifest, error) {
	if !validDigest(baseSHA) || runtimeRecipeBases[lang] != baseSHA {
		return artifactfs.Manifest{}, errors.New("unreviewed base package checksum")
	}
	sources, err := loadRuntimeRecipeSources(sourceRoot, revision, lang)
	if err != nil {
		return artifactfs.Manifest{}, err
	}
	entries := map[string]artifactfs.Entry{}
	for _, e := range base.Entries {
		entries[e.Path] = e
	}
	for name, target := range runtimeRecipeOmissions {
		e, exists := entries[name]
		if !exists || e.Type != artifactfs.TypeSymlink || e.LinkTarget != target {
			return artifactfs.Manifest{}, fmt.Errorf("recipe omission mismatch: %s -> %s", name, target)
		}
	}
	parent := entries["opt/orva"]
	if parent.Type != artifactfs.TypeDirectory {
		return artifactfs.Manifest{}, errors.New("missing packaged adapter directory")
	}
	for name := range sources {
		e, exists := entries[name]
		isAdapter := name == "opt/orva/adapter.js" || name == "opt/orva/adapter.py"
		if (exists && (!isAdapter || e.Type != artifactfs.TypeFile || e.HardlinkGroup != "" || e.Mode != 0644)) ||
			(!exists && isAdapter) {
			return artifactfs.Manifest{}, fmt.Errorf("unexpected overlay destination: %s", name)
		}
	}
	if lang == "node" {
		for _, name := range []string{"opt/orva/node_modules", "opt/orva/node_modules/orva"} {
			if _, exists := entries[name]; exists {
				return artifactfs.Manifest{}, fmt.Errorf("SDK directory unexpectedly present: %s", name)
			}
		}
	}
	for name := range runtimeRecipeOmissions {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			return artifactfs.Manifest{}, err
		}
		delete(entries, name)
	}
	if lang == "node" {
		for _, name := range []string{"opt/orva/node_modules", "opt/orva/node_modules/orva"} {
			if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
				return artifactfs.Manifest{}, err
			}
			if err := os.Chown(filepath.Join(root, name), int(parent.UID), int(parent.GID)); err != nil {
				return artifactfs.Manifest{}, err
			}
			if err := os.Chmod(filepath.Join(root, name), 0755); err != nil {
				return artifactfs.Manifest{}, err
			}
			entries[name] = artifactfs.Entry{Path: name, Type: artifactfs.TypeDirectory, Mode: 0755, UID: parent.UID, GID: parent.GID}
		}
	}
	for name, raw := range sources {
		e, exists := entries[name]
		if exists {
			if err := os.Remove(filepath.Join(root, name)); err != nil {
				return artifactfs.Manifest{}, err
			}
		} else {
			e = artifactfs.Entry{Path: name, Type: artifactfs.TypeFile, Mode: 0644, UID: parent.UID, GID: parent.GID}
		}
		f, err := os.OpenFile(filepath.Join(root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return artifactfs.Manifest{}, err
		}
		_, writeErr := f.Write(raw)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			return artifactfs.Manifest{}, errors.New("runtime overlay write failed")
		}
		if err := os.Chown(filepath.Join(root, name), int(e.UID), int(e.GID)); err != nil {
			return artifactfs.Manifest{}, err
		}
		if err := os.Chmod(filepath.Join(root, name), 0644); err != nil {
			return artifactfs.Manifest{}, err
		}
		e.Size, e.SHA256 = int64(len(raw)), digest(string(raw))
		entries[name] = e
	}
	m := artifactfs.Manifest{Version: 1}
	for _, e := range entries {
		m.Entries = append(m.Entries, e)
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	return m, nil
}

func writeRuntimeRecipeEvidence(dir, root, lang, baseSHA string, m artifactfs.Manifest, manifestSHA string) (string, error) {
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	output, err := os.OpenFile(filepath.Join(dir, "runtime.tar"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(output, hash))
	writeErr := emitRuntimeRecipeArchive(tw, root, m)
	tarErr, closeErr := tw.Close(), output.Close()
	if writeErr != nil || tarErr != nil || closeErr != nil {
		return "", fmt.Errorf("recipe archive: write=%v tar=%v close=%v", writeErr, tarErr, closeErr)
	}
	archiveSHA := hex.EncodeToString(hash.Sum(nil))
	evidence := struct {
		Recipe, Language, SourceRevision, BaseSHA256, ArchiveSHA256, ManifestSHA256 string
		Sources                                                                     []runtimeSourceFile
		Omissions                                                                   map[string]string
		Manifest                                                                    artifactfs.Manifest
	}{"orva.runtime-qualification.v1", lang, runtimeRecipeSourceRevision, baseSHA,
		archiveSHA, manifestSHA, runtimeRecipeSources[lang], runtimeRecipeOmissions, m}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "producer.json"), raw, 0600); err != nil {
		return "", err
	}
	return archiveSHA, nil
}

func emitRuntimeRecipeArchive(w *tar.Writer, root string, m artifactfs.Manifest) error {
	for _, e := range m.Entries {
		h := &tar.Header{Name: "./" + e.Path, Mode: int64(e.Mode), Uid: int(e.UID), Gid: int(e.GID), Format: tar.FormatPAX}
		switch e.Type {
		case artifactfs.TypeDirectory:
			h.Typeflag = tar.TypeDir
		case artifactfs.TypeSymlink:
			h.Typeflag, h.Linkname = tar.TypeSymlink, e.LinkTarget
		case artifactfs.TypeFile:
			h.Typeflag, h.Size = tar.TypeReg, e.Size
			if e.HardlinkGroup != "" && e.HardlinkGroup != e.Path {
				h.Typeflag, h.Linkname, h.Size = tar.TypeLink, "./"+e.HardlinkGroup, 0
			}
		default:
			return errors.New("unsupported output entry")
		}
		if err := w.WriteHeader(h); err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		fd, err := syscall.Open(filepath.Join(root, e.Path), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), e.Path)
		hash := sha256.New()
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
			_ = f.Close()
			return errors.New("recipe archive source changed")
		}
		_, copyErr := io.CopyN(io.MultiWriter(w, hash), f, e.Size)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != e.SHA256 {
			return errors.New("recipe archive content mismatch")
		}
	}
	return nil
}

func TestRuntimeRecipeTrust(t *testing.T) {
	for _, bad := range []string{"", "not-a-checksum", strings.Repeat("A", 64), digest("unreviewed package")} {
		if _, err := applyRuntimeRecipe(t.TempDir(), "node", bad, "", "", artifactfs.Manifest{}); err == nil {
			t.Fatal("unreviewed checksum accepted")
		}
	}
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			if _, err := loadRuntimeRecipeSources("../../runtimes", runtimeRecipeSourceRevision, lang); err != nil {
				t.Fatal("current adapter/SDK source is not reviewed:", err)
			}
			if _, err := loadRuntimeRecipeSources("../../runtimes", "sender-claim", lang); err == nil {
				t.Fatal("unreviewed source revision accepted")
			}
			for _, spec := range runtimeRecipeSources[lang] {
				root := t.TempDir()
				for _, file := range runtimeRecipeSources[lang] {
					raw, err := os.ReadFile(filepath.Join("../../runtimes", file.Source))
					if err != nil {
						t.Fatal(err)
					}
					if file.Source == spec.Source {
						raw = append(raw, '\n')
					}
					target := filepath.Join(root, file.Source)
					if err := os.MkdirAll(path.Dir(target), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, raw, 0644); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := loadRuntimeRecipeSources(root, runtimeRecipeSourceRevision, lang); err == nil {
					t.Fatal("incoherent source accepted:", spec.Source)
				}
			}
		})
	}
}

func TestRuntimeRecipeOutput(t *testing.T) {
	for _, lang := range []string{"node", "python"} {
		for _, scenario := range []string{"valid", "different omission", "missing omission", "unknown dangling"} {
			t.Run(lang+"/"+scenario, func(t *testing.T) {
				root := t.TempDir()
				base := recipeFixture(t, root, lang, scenario)
				m, err := applyRuntimeRecipe(root, lang, runtimeRecipeBases[lang], "../../runtimes", runtimeRecipeSourceRevision, base)
				if scenario == "different omission" || scenario == "missing omission" {
					if err == nil {
						t.Fatal("unreviewed omission accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				for name := range runtimeRecipeOmissions {
					if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
						t.Fatal("omitted path survived:", name)
					}
				}
				limits := runtimePackageLimits()
				expected, err := artifactfs.Digest(m, limits)
				if err != nil {
					t.Fatal(err)
				}
				output := filepath.Join(t.TempDir(), "producer")
				archiveSHA, err := writeRuntimeRecipeEvidence(output, root, lang, runtimeRecipeBases[lang], m, expected)
				if err != nil {
					t.Fatal(err)
				}
				storeRoot := t.TempDir()
				_ = os.Chmod(storeRoot, 0700)
				store, err := artifactfs.New(storeRoot, limits)
				if err != nil {
					t.Fatal(err)
				}
				stage, err := store.Begin(t.Context(), 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				dest, _ := stage.Path()
				archive, err := verifiedRuntimePackage(filepath.Join(output, "runtime.tar"), archiveSHA)
				if err != nil {
					t.Fatal(err)
				}
				roundTrip, produceErr := unpackRuntimePackage(dest, archive, limits)
				closeErr := archive.Close()
				if produceErr != nil || closeErr != nil {
					t.Fatalf("archive round-trip: %v %v", produceErr, closeErr)
				}
				got, err := artifactfs.Digest(roundTrip, limits)
				if err != nil || got != expected {
					t.Fatal("producer expectations changed across archive round-trip:", err)
				}
				id, sealErr := stage.Seal(t.Context(), artifactfs.KindRuntime, lang, m, expected)
				if scenario == "unknown dangling" {
					if !errors.Is(sealErr, artifactfs.ErrUnsupported) || id != "" {
						t.Fatal("unknown dangling link escaped strict Seal:", sealErr)
					}
					if err := stage.Abort(); err != nil {
						t.Fatal(err)
					}
				} else {
					if sealErr != nil {
						t.Fatal(sealErr)
					}
					reclaimQualified(t, store, id)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func recipeFixture(t *testing.T, root, lang, scenario string) artifactfs.Manifest {
	t.Helper()
	headers := []*tar.Header{}
	contents := []string{}
	for _, name := range []string{".", "etc", "etc/alternatives", "opt", "opt/orva"} {
		headers = append(headers, &tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0755})
		contents = append(contents, "")
	}
	names := []string{}
	for name := range runtimeRecipeOmissions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := runtimeRecipeOmissions[name]
		if name == "etc/mtab" {
			if scenario == "missing omission" {
				continue
			}
			if scenario == "different omission" {
				target = "/different"
			}
		}
		headers = append(headers, &tar.Header{Name: name, Typeflag: tar.TypeSymlink, Mode: 0777, Linkname: target})
		contents = append(contents, "")
	}
	adapter := "opt/orva/adapter.js"
	if lang == "python" {
		adapter = "opt/orva/adapter.py"
	}
	headers = append(headers, &tar.Header{Name: adapter, Typeflag: tar.TypeReg, Mode: 0644, Size: 3})
	contents = append(contents, "old")
	if scenario == "unknown dangling" {
		headers = append(headers, &tar.Header{Name: "etc/unreviewed", Typeflag: tar.TypeSymlink, Mode: 0777, Linkname: "/missing"})
		contents = append(contents, "")
	}
	m, err := unpackRuntimePackage(root, bytes.NewReader(packageFixture(t, headers, contents)), runtimePackageLimits())
	if err != nil {
		t.Fatal(err)
	}
	return m
}
