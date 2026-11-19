package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/version"
	"github.com/google/uuid"
)

const (
	ManagedFormatVersion = 2
	maxManagedManifest   = 1 << 20
	maxManagedDatabase   = 32 << 30
	// Regeneration can emit explicit headers even for implicit upload
	// directories. Budget 50 MiB payload and up to 16K long-name headers.
	ManagedSourceLimit = 160 << 20
)

type managedManifest struct {
	Version int                   `json:"format_version"`
	Orva    string                `json:"orva_version"`
	Owner   string                `json:"prior_owner"`
	Count   int                   `json:"function_count"`
	Sources map[string]string     `json:"deployment_sources"`
	Files   map[string]FileDigest `json:"files"`
}

func sourceName(digest string) string { return "sources/" + digest + ".tar.gz" }

func managedEntryLimit(name string) int64 {
	switch name {
	case "orva.db":
		return maxManagedDatabase
	case "keys/master.key":
		return 32
	case "keys/admin.key":
		return 4096
	}
	if digest, ok := strings.CutPrefix(name, "sources/"); ok {
		if digest, ok := strings.CutSuffix(digest, ".tar.gz"); ok && isVersionHash(digest) {
			return ManagedSourceLimit
		}
	}
	return 0
}

// ArchiveManagedTo reads only fixed DB/key paths and source projected from the
// exact immutable generations named by the VACUUM INTO snapshot.
func ArchiveManagedTo(ctx context.Context, w io.Writer, owner *artifactfs.Owner, dataDir, snapshot, scratch string) error {
	id, err := owner.InstanceID(ctx)
	if err != nil {
		return err
	}
	assets, err := owner.Bindings(ctx)
	if err != nil {
		return err
	}
	if err := owner.CheckBackupPaths(ctx, []string{dataDir, filepath.Dir(snapshot), scratch}, []string{snapshot}); err != nil {
		return err
	}
	return archiveManaged(ctx, w, id, dataDir, snapshot, scratch,
		func(paths []string) error { return owner.CheckBackupPaths(ctx, nil, paths) },
		func(d database.PortableDeployment, dst io.Writer) error {
			lease, err := assets.Code.Acquire(d.Output.CodeGeneration)
			if err != nil {
				return err
			}
			defer lease.Release()
			got := lease.Identity()
			if got.Owner != d.Owner || got.Digest != d.Output.CodeDigest {
				return artifactfs.ErrIntegrity
			}
			if d.Output.SourceArchiveSHA256 != "" {
				return lease.WriteRetainedBuildSource(ctx, dst, d.Output.SourceArchiveSHA256)
			}
			return lease.WriteBuildSource(ctx, dst)
		})
}

func archiveManaged(ctx context.Context, w io.Writer, owner, dataDir, snapshot, scratch string,
	check func([]string) error, project func(database.PortableDeployment, io.Writer) error,
) (err error) {
	count, deployments, err := database.PortableInventory(ctx, snapshot, owner)
	if err != nil {
		return err
	}
	m := managedManifest{ManagedFormatVersion, version.Version, owner, count, map[string]string{}, map[string]FileDigest{}}
	plan := &archivePlan{manifest: Manifest{Files: m.Files}}
	add := func(path, name string) error {
		if err := check([]string{path}); err != nil {
			return err
		}
		if st, err := os.Lstat(path); err != nil || st.Size() <= 0 || st.Size() > managedEntryLimit(name) {
			return errors.Join(fmt.Errorf("invalid managed backup entry %s", name), err)
		}
		return planFile(plan, path, name)
	}
	if err := add(snapshot, "orva.db"); err != nil {
		return err
	}
	for _, key := range []string{"master.key", "admin.key"} {
		path := filepath.Join(dataDir, "."+key)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := add(path, "keys/"+key); err != nil {
			return err
		}
	}
	if err := database.CheckPortableMasterKey(ctx, snapshot, m.Files["keys/master.key"].Size); err != nil {
		return err
	}
	for _, d := range deployments {
		f, err := os.OpenFile(filepath.Join(scratch, d.Deployment), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		hash := sha256.New()
		if err := errors.Join(project(d, io.MultiWriter(f, hash)), f.Close()); err != nil {
			return fmt.Errorf("project current deployment %s: %w", d.Deployment, err)
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		name := sourceName(digest)
		m.Sources[d.Deployment] = digest
		if _, exists := m.Files[name]; !exists {
			if err := add(f.Name(), name); err != nil {
				return err
			}
		}
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > maxManagedManifest {
		return errors.Join(ErrBadArchive, err)
	}
	gz := gzip.NewWriter(w)
	gz.Name = managedGzipName
	tw := tar.NewWriter(gz)
	defer func() {
		if err != nil {
			return // An interrupted stream must not look complete.
		}
		tarErr := tw.Close()
		err = errors.Join(err, tarErr, gz.Close())
	}()
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(raw))}); err != nil {
		return err
	}
	if _, err := tw.Write(raw); err != nil {
		return err
	}
	for _, item := range plan.items {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.Open(item.srcPath)
		if err != nil {
			return err
		}
		want := m.Files[item.archivePath]
		hash := sha256.New()
		err = tw.WriteHeader(&tar.Header{Name: item.archivePath, Mode: 0600, Size: want.Size})
		if err == nil {
			_, err = io.CopyN(io.MultiWriter(tw, hash), f, want.Size)
		}
		st, statErr := f.Stat()
		if err := errors.Join(err, statErr, f.Close()); err != nil {
			return err
		}
		if st.Size() != want.Size || hex.EncodeToString(hash.Sum(nil)) != want.SHA256 {
			return fmt.Errorf("managed backup entry changed: %s", item.archivePath)
		}
	}
	return nil
}

// ManagedStage is a verified private scratch extraction. The caller owns the
// scratch directory and its cleanup. It must never be served in place.
type ManagedStage struct {
	Dir, PriorOwner string
	Deployments     []database.PortableDeployment
	sources         map[string]string
}

func (s *ManagedStage) Source(deployment string) (path, digest string) {
	digest = s.sources[deployment]
	if digest == "" {
		return "", ""
	}
	return filepath.Join(s.Dir, sourceName(digest)), digest
}

func managedBad(reason string) error {
	return fmt.Errorf("%w: managed archive %s", ErrBadArchive, reason)
}

// StageManaged accepts exactly one gzip stream, one leading canonical
// manifest, and each declared entry once. No archive attributes are applied.
func StageManaged(ctx context.Context, r io.Reader, stage string) (*ManagedStage, error) {
	buf := bufio.NewReader(r)
	gz, err := gzip.NewReader(buf)
	if err != nil {
		return nil, managedBad("gzip header")
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != "manifest.json" || !managedRegular(h) || h.Size <= 0 || h.Size > maxManagedManifest {
		return nil, managedBad("manifest header")
	}
	raw, err := io.ReadAll(tr)
	if err != nil {
		return nil, managedBad("manifest read")
	}
	var m managedManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, managedBad("manifest encoding")
	}
	if m.Version > ManagedFormatVersion {
		return nil, ErrIncompatibleFormat
	}
	canonical, err := json.Marshal(m)
	if err != nil || !bytes.Equal(raw, canonical) || !validManagedManifest(m) {
		return nil, managedBad("manifest inventory")
	}
	seen := make(map[string]bool, len(m.Files))
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, managedBad("tar read")
		}
		want, ok := m.Files[h.Name]
		if !ok || !filepath.IsLocal(h.Name) || seen[h.Name] || !managedRegular(h) || want.Size != h.Size {
			return nil, managedBad("unexpected archive entry")
		}
		seen[h.Name] = true
		target := filepath.Join(stage, h.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		_, err = io.CopyN(io.MultiWriter(hash, f), managedContextReader{ctx, tr}, h.Size)
		if err := errors.Join(err, f.Close()); err != nil {
			return nil, errors.Join(managedBad("entry read"), err)
		}
		if hex.EncodeToString(hash.Sum(nil)) != want.SHA256 {
			return nil, managedBad("entry checksum mismatch")
		}
	}
	if _, err := gz.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		return nil, managedBad("invalid gzip trailer or trailing tar data")
	}
	if _, err := buf.Peek(1); !errors.Is(err, io.EOF) || len(seen) != len(m.Files) {
		return nil, managedBad("trailing data or missing entries")
	}
	db := filepath.Join(stage, "orva.db")
	if err := validateStagedDB(db); err != nil {
		return nil, errors.Join(managedBad("invalid SQLite snapshot"), err)
	}
	n, ds, err := database.PortableInventory(ctx, db, m.Owner)
	if err != nil || n != m.Count || len(ds) != len(m.Sources) {
		return nil, errors.Join(managedBad("deployment inventory mismatch"), err)
	}
	if err := database.CheckPortableMasterKey(ctx, db, m.Files["keys/master.key"].Size); err != nil {
		return nil, errors.Join(managedBad("master key mismatch"), err)
	}
	for _, d := range ds {
		if m.Sources[d.Deployment] == "" {
			return nil, managedBad("deployment source missing")
		}
		if d.Output.SourceArchiveSHA256 != "" && d.Output.SourceArchiveSHA256 != m.Sources[d.Deployment] {
			return nil, managedBad("deployment source does not match the sealed association")
		}
	}
	return &ManagedStage{stage, m.Owner, ds, m.Sources}, nil
}

type managedContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r managedContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func managedRegular(h *tar.Header) bool {
	return h.Typeflag == tar.TypeReg && h.Linkname == "" && len(h.PAXRecords) == 0 && len(h.Xattrs) == 0
}

func validManagedUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s && id.Version() == 7 && id.Variant() == uuid.RFC4122
}

func validManagedManifest(m managedManifest) bool {
	if m.Version != ManagedFormatVersion || m.Orva == "" || !validManagedUUID(m.Owner) ||
		m.Count < 0 || m.Count > database.PortableFunctionLimit || m.Sources == nil ||
		len(m.Sources) > m.Count || m.Files["orva.db"].Size <= 0 {
		return false
	}
	expected := map[string]bool{"orva.db": true, "keys/master.key": true, "keys/admin.key": true}
	for deployment, digest := range m.Sources {
		if !validManagedUUID(deployment) || !isVersionHash(digest) || m.Files[sourceName(digest)].SHA256 != digest {
			return false
		}
		expected[sourceName(digest)] = true
	}
	for name, f := range m.Files {
		if !expected[name] || f.Size <= 0 || f.Size > managedEntryLimit(name) || !isVersionHash(f.SHA256) {
			return false
		}
	}
	return true
}

const managedGzipName = "orva-managed-v2"

func stageIfPortable(r io.Reader, stage string) (io.Reader, bool, error) {
	b := bufio.NewReader(r)
	p, _ := b.Peek(10 + len(managedGzipName) + 1)
	if len(p) < 10 || p[3] != 8 || string(p[10:]) != managedGzipName+"\x00" {
		return b, false, nil
	}
	_, err := StageManaged(context.Background(), b, stage)
	return b, true, err
}
