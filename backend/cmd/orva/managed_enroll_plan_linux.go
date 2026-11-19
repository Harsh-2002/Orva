package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const enrollmentOwnerLimit = 256 // The managed daemon reconstructs at most 256 owners.

type enrollmentPlan struct {
	Version   int                  `json:"version"`
	Database  string               `json:"database_identity_sha256"`
	Functions []enrollmentFunction `json:"functions"`
	Work      enrollmentWork       `json:"work"`
}

type enrollmentWork struct {
	FunctionJobs int `json:"function_origin_jobs"`
	UnknownJobs  int `json:"unknown_provenance_jobs"`
	UnknownCrons int `json:"unknown_provenance_cron"`
}

type enrollmentFunction struct {
	ID            string   `json:"id"`
	Runtime       string   `json:"runtime"`
	Entrypoint    string   `json:"entrypoint"`
	Dependencies  []string `json:"dependency_markers"`
	TypeScript    bool     `json:"typescript"`
	CodeHash      string   `json:"current_code_hash"`
	SDK           bool     `json:"legacy_sdk"`
	Revision      string   `json:"revision_sha256"`
	Enrollable    bool     `json:"enrollable"`
	Reason        string   `json:"unsupported_reason,omitempty"`
	version       int
	status        string
	deployment    string
	runEntrypoint string
	network       string
}

func captureEnrollmentPlan(ctx context.Context, dataDir, scratch string) (*enrollmentPlan, error) {
	dbIdentity, err := snapshotEnrollmentDB(dataDir, scratch)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(scratch, "snapshot.db"), RawQuery: "mode=ro&immutable=1"}
	conn, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	p := &enrollmentPlan{Version: 1, Database: dbIdentity, Functions: []enrollmentFunction{}}
	var managed bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE lower(name) GLOB 'workload_*')`).Scan(&managed); err != nil {
		return nil, err
	}
	if managed {
		return nil, errors.New("database contains managed workload state; refusing legacy enrollment")
	}
	if err := conn.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM jobs WHERE COALESCE(enqueued_by_function_id,'')<>''),
		(SELECT count(*) FROM jobs WHERE COALESCE(enqueued_by_function_id,'')=''),
		(SELECT count(*) FROM cron_schedules)`).Scan(&p.Work.FunctionJobs, &p.Work.UnknownJobs, &p.Work.UnknownCrons); err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT f.id,f.runtime,f.entrypoint,COALESCE(f.code_hash,''),f.sdk,
		f.version,f.status,COALESCE(d.id,''),COALESCE(f.run_entrypoint,''),f.network_mode
		FROM functions f LEFT JOIN deployments d ON d.id=f.active_deployment_id AND d.function_id=f.id AND d.status='succeeded'
		ORDER BY f.id LIMIT ?`, enrollmentOwnerLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f enrollmentFunction
		if err := rows.Scan(&f.ID, &f.Runtime, &f.Entrypoint, &f.CodeHash, &f.SDK, &f.version, &f.status, &f.deployment, &f.runEntrypoint, &f.network); err != nil {
			return nil, err
		}
		if len(p.Functions) == enrollmentOwnerLimit {
			return nil, errors.New("legacy instance exceeds the managed serving limit of 256 functions")
		}
		if !enrollmentUUID(f.ID) {
			return nil, errors.New("function lacks a canonical UUIDv7; complete the legacy migration first")
		}
		if err := f.inspect(ctx, dataDir, scratch); err != nil {
			return nil, fmt.Errorf("inspect function %s: %w", f.ID, err)
		}
		p.Functions = append(p.Functions, f)
	}
	return p, rows.Err()
}

func enrollmentUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s && id.Version() == 7 && id.Variant() == uuid.RFC4122
}

func enrollmentDigest(s string) bool {
	v, err := hex.DecodeString(s)
	return err == nil && len(v) == sha256.Size && strings.ToLower(s) == s
}

func openEnrollmentFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("expected a regular single-link file")
	}
	return f, nil
}

func snapshotEnrollmentDB(dataDir, scratch string) (string, error) {
	if _, err := os.Lstat(filepath.Join(dataDir, "orva.db-journal")); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("legacy SQLite rollback journal must be recovered by legacy Orva first")
	}
	// SQLite may create or update -shm even on a read-only WAL connection.
	// Copy the DB and WAL under the outer lock before opening SQLite at all.
	h := sha256.New()
	for _, name := range []string{"orva.db", "orva.db-wal"} {
		in, err := openEnrollmentFile(filepath.Join(dataDir, name))
		if name != "orva.db" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		var st unix.Stat_t
		if err := unix.Fstat(int(in.Fd()), &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
			in.Close()
			return "", errors.New("unsafe legacy SQLite input")
		}
		fmt.Fprintf(h, "%s:%d:%d:%d\n", name, st.Dev, st.Ino, st.Size)
		out, err := os.OpenFile(filepath.Join(scratch, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return "", err
		}
		_, err = io.Copy(io.MultiWriter(out, h), in)
		if err := errors.Join(err, in.Close(), out.Close()); err != nil {
			return "", err
		}
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(scratch, "orva.db"), RawQuery: "mode=rw"}
	conn, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return "", err
	}
	err = backup.SnapshotDB(conn, filepath.Join(scratch, "snapshot.db"))
	return hex.EncodeToString(h.Sum(nil)), errors.Join(err, conn.Close())
}

func (f *enrollmentFunction) archive(scratch string) string {
	return filepath.Join(scratch, f.ID+".tar.gz")
}

func (f *enrollmentFunction) reject(reason string) {
	if f.Reason == "" {
		f.Reason = reason
	}
}

func (f *enrollmentFunction) inspect(ctx context.Context, dataDir, scratch string) (err error) {
	f.Dependencies = []string{}
	f.TypeScript = strings.HasSuffix(f.Entrypoint, ".ts")
	h := sha256.New()
	defer func() {
		fmt.Fprintf(h, "\n%d:%s:%s:%s\n", f.version, f.status, f.deployment, f.runEntrypoint)
		if err == nil {
			f.Enrollable = f.Reason == ""
			err = json.NewEncoder(h).Encode(f)
			f.Revision = hex.EncodeToString(h.Sum(nil))
		}
	}()
	if f.Runtime != "node" && f.Runtime != "python" {
		f.reject("runtime is not Node or Python")
	}
	if f.status != "active" || !enrollmentUUID(f.deployment) || !enrollmentDigest(f.CodeHash) {
		f.reject("no current successful active deployment with a valid code hash")
	}
	if !enrollmentDigest(f.CodeHash) {
		return nil
	}
	dir := dataDir
	for _, component := range []string{"functions", f.ID, "versions", f.CodeHash} {
		dir = filepath.Join(dir, component)
		stat, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			f.reject("current version directory is missing")
			return nil
		}
		if err != nil {
			return err
		}
		if !stat.IsDir() {
			f.reject("current version path contains a link or non-directory")
			return nil
		}
		if component == f.ID {
			// Legacy workers execute through this alias; do not choose a
			// different revision just because the DB still points at it.
			link, err := os.Readlink(filepath.Join(dir, "current"))
			if err != nil || link != filepath.Join("versions", f.CodeHash) {
				f.reject("legacy current symlink does not match the database code hash")
			}
			fmt.Fprintf(h, "legacy-current:%q\n", link)
		}
	}
	markers := make(map[string]bool)
	for _, marker := range []string{"package.json", "requirements.txt", "node_modules", "tsconfig.json"} {
		if info, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			markers[marker] = true
			if (marker == "node_modules" && !info.IsDir()) || (marker != "node_modules" && !info.Mode().IsRegular()) {
				f.reject(marker + " is not a supported regular manifest or dependency directory")
			}
			if marker == "tsconfig.json" {
				f.TypeScript = true
			} else {
				f.Dependencies = append(f.Dependencies, marker)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if (f.Runtime == "python" && (markers["package.json"] || markers["node_modules"] || markers["tsconfig.json"])) ||
		(f.Runtime == "node" && markers["requirements.txt"]) {
		f.reject("dependency manifests do not match the selected runtime")
	}
	if markers["node_modules"] && !markers["package.json"] {
		f.reject("node_modules exists without package.json; dependencies cannot be rebuilt")
	}
	ext := filepath.Ext(f.Entrypoint)
	if !filepath.IsLocal(f.Entrypoint) || filepath.Clean(f.Entrypoint) != f.Entrypoint || f.Entrypoint == "." ||
		strings.ContainsAny(f.Entrypoint, "\\\x00") || !utf8.ValidString(f.Entrypoint) ||
		(f.Runtime == "node" && ext != ".js" && ext != ".cjs" && ext != ".mjs" && ext != ".ts") ||
		(f.Runtime == "python" && ext != ".py") {
		f.reject("entrypoint is not a supported relative .js, .cjs, .mjs, .ts or .py file")
	}
	if f.network != "" && f.network != "none" {
		f.reject("managed serving does not provision legacy egress")
	}
	var outDir string
	if f.Reason == "" && f.TypeScript {
		switch {
		case !markers["tsconfig.json"]:
			f.reject("TypeScript requires tsconfig.json")
		case !markers["package.json"]:
			f.reject("TypeScript requires package.json with a declared typescript dependency")
		default:
			root, err := os.OpenRoot(dir)
			if err != nil {
				return err
			}
			var run string
			outDir, run, err = builder.ManagedTypeScriptOutput(root, f.Entrypoint)
			root.Close()
			switch {
			case err != nil:
				f.reject(err.Error())
			case outDir == "." || strings.HasPrefix(f.Entrypoint, outDir+"/") ||
				outDir == "node_modules" || strings.HasPrefix(outDir, "node_modules/"):
				f.reject("TypeScript output must be in a separate project directory for enrollment")
			case f.runEntrypoint != "" && f.runEntrypoint != run:
				f.reject("legacy TypeScript run entrypoint does not match the compiler output path")
			default:
				if info, err := os.Lstat(filepath.Join(dir, outDir)); err != nil || !info.IsDir() {
					f.reject("legacy TypeScript output directory is missing or not a directory")
				}
			}
		}
	} else if f.runEntrypoint != "" && f.runEntrypoint != f.Entrypoint {
		f.reject("non-TypeScript deployment has a different legacy run entrypoint")
	}
	var pipFiles map[string]bool
	if f.Reason == "" && markers["requirements.txt"] {
		var err error
		pipFiles, err = enrolledPipFiles(ctx, dir, f.Entrypoint)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			f.reject(err.Error())
		}
	}
	out, err := os.OpenFile(f.archive(scratch), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	found := false
	var entries int
	var total, tarBytes int64
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		var stat unix.Stat_t
		if err := unix.Lstat(path, &stat); err != nil {
			return err
		}
		fmt.Fprintf(h, "%q:%d:%d:%d:%d:%d:%d\n", rel, stat.Mode, stat.Size, stat.Nlink, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec)
		generated := (rel == ".orva-ready" && info.Mode().IsRegular()) || (rel == "node_modules" && markers["package.json"]) ||
			(outDir != "" && rel == outDir) || pipFiles[rel] ||
			(f.Runtime == "python" && filepath.Base(rel) == "__pycache__")
		if generated {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		if entries >= 16384 {
			f.reject("source tree exceeds managed builder entry bounds")
		}
		if rel == f.Entrypoint && info.Mode().IsRegular() {
			found = true
		}
		if len(rel) > 4096 || !utf8.ValidString(rel) || strings.ContainsAny(rel, "\\\x00") || strings.HasPrefix(rel, "..") {
			f.reject("source tree contains an unsupported path")
		}
		if rel == artifactfs.BuildSourceName || strings.HasPrefix(rel, artifactfs.BuildSourceName+"/") {
			f.reject("source tree contains the reserved managed build-source path")
		}
		if !d.IsDir() && (!info.Mode().IsRegular() || stat.Nlink != 1) {
			f.reject("source tree contains links or special files")
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				fmt.Fprintf(h, "%q\n", target)
			}
			return nil
		}
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			f.reject("source tree contains special permission bits")
		}
		if n, err := unix.Llistxattr(path, nil); err != nil {
			return err
		} else if n != 0 {
			f.reject("source tree contains extended attributes")
		}
		tarBytes += 512
		if len(rel) > 100 {
			tarBytes += 512 + ((int64(len(rel))+1+511)/512)*512
		}
		if !d.IsDir() {
			tarBytes += ((info.Size() + 511) / 512) * 512
		}
		if tarBytes >= (64<<20)-(512<<10) {
			f.reject("source tree exceeds the managed builder archive budget")
		}
		if d.IsDir() {
			if f.Reason != "" {
				return nil
			}
			return tw.WriteHeader(&tar.Header{Name: rel, Mode: int64(info.Mode().Perm()), Typeflag: tar.TypeDir, ModTime: time.Unix(0, 0), Format: tar.FormatGNU})
		}
		total += info.Size()
		if total > builder.DefaultMaxCodeSize {
			f.reject("source tree exceeds the managed builder's 50 MiB uncompressed code limit")
		}
		// Oversized trees cannot be imported; still include every path's
		// identity and ctime in the exclusion plan without unbounded reads.
		if total > 512<<20 {
			return nil
		}
		file, err := openEnrollmentFile(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if info.Size() >= 4 {
			var prefix [4]byte
			if _, err := file.ReadAt(prefix[:], 0); err != nil {
				return err
			}
			if string(prefix[:]) == "\x7fELF" {
				f.reject("source tree contains ELF binaries")
			}
		}
		dst := io.Writer(h)
		if f.Reason == "" {
			if err := tw.WriteHeader(&tar.Header{Name: rel, Size: info.Size(), Mode: int64(info.Mode().Perm()), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatGNU}); err != nil {
				return err
			}
			dst = io.MultiWriter(h, tw)
		}
		_, err = io.CopyN(dst, file, info.Size())
		return err
	})
	if !found {
		f.reject("current source entrypoint is missing or not a regular file")
	}
	return errors.Join(err, tw.Close(), gz.Close(), out.Close())
}
