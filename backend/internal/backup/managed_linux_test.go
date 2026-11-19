package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedArchive(t *testing.T) {
	root, scratch := t.TempDir(), t.TempDir()
	db, err := database.New(filepath.Join(root, "orva.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	owner := ids.New()
	must(db.Migrate())
	must(db.BindManagedOwner(t.Context(), owner))
	_, err = db.ApplyRecoveryForFixture(t.Context(), "")
	must(err)
	must(db.InitManagedServingSchemas(t.Context()))
	hash := strings.Repeat("a", 64)
	deploys := make(map[string]string)
	for _, status := range []string{"active", "inactive"} {
		id, dep := ids.New(), ids.New()
		must(db.InsertFunction(&database.Function{ID: id, Name: status, Runtime: "node", Entrypoint: "handler.js", Status: status,
			CodeHash: hash, ActiveDeploymentID: dep}))
		_, err := db.WriteDB().Exec(`INSERT INTO workload_iam_policies
			(function_id,revision,authority_epoch,approved_artifact,review_required,quarantined,policy_json,created_at,updated_at)
			VALUES(?,1,1,?,1,0,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, database.IAMUnboundArtifact, database.RecoveryEmptyPolicy)
		must(err)
		currentGen, originalGen := ids.New(), ids.New()
		_, err = db.WriteDB().Exec(`INSERT INTO workload_artifact_generations
			(function_id,revision,epoch,generation,actor_kind,actor_id,change_kind,created_at,updated_at)
			VALUES(?,1,1,?,'system',?,'initialize',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, currentGen, owner)
		must(err)
		must(db.InsertDeployment(&database.Deployment{ID: dep, FunctionID: id, Version: 1, Status: "succeeded", CodeHash: hash}))
		raw, err := json.Marshal(database.WorkloadBuildOutput{
			CodeGeneration: ids.New(), CodeDigest: hash, RuntimeGeneration: ids.New(), RuntimeDigest: hash, CodeHash: hash, Entrypoint: "handler.js"})
		must(err)
		p, err := json.Marshal(map[string]any{"Owner": id, "Runtime": "node", "Entrypoint": "handler.js", "Artifact": workloadiam.Metadata{OwnerID: id}})
		must(err)
		_, err = db.WriteDB().Exec(`INSERT INTO workload_deployment_capture VALUES(?,?,?,?,?)`, dep, id, string(p), string(raw), originalGen)
		must(err)
		deploys[dep] = id
	}
	snapshot := filepath.Join(scratch, "snapshot.db")
	must(SnapshotDB(db.ReadDB(), snapshot))
	must(os.WriteFile(filepath.Join(root, ".master.key"), bytes.Repeat([]byte{'k'}, 32), 0600))
	for _, p := range []string{"sealed-owner/owner.json", "sealed-owner/epochs/epoch.json", "sealed-code/tree", "sealed-runtime/tree", "witness"} {
		f := filepath.Join(root, p)
		must(os.MkdirAll(filepath.Dir(f), 0700))
		must(os.WriteFile(f, []byte("not archived"), 0600))
	}
	var archive bytes.Buffer
	must(archiveManaged(t.Context(), &archive, owner, root, snapshot, scratch, func([]string) error { return nil },
		func(d database.PortableDeployment, w io.Writer) error {
			if deploys[d.Deployment] != d.Owner || d.Output.CodeHash != hash {
				t.Fatalf("wrong current association: %+v", d)
			}
			_, err := io.WriteString(w, "fixture source bytes")
			return err
		}))
	stage, err := StageManaged(t.Context(), bytes.NewReader(archive.Bytes()), t.TempDir())
	must(err)
	if stage.PriorOwner != owner || len(stage.Deployments) != 2 {
		t.Fatal("wrong staged inventory")
	}
	var names []string
	correct := archive.Bytes()
	rewrite := func(change func(*tar.Header, []byte) []byte, extra string) []byte {
		gz, err := gzip.NewReader(bytes.NewReader(correct))
		must(err)
		defer gz.Close()
		tr := tar.NewReader(gz)
		var out bytes.Buffer
		z := gzip.NewWriter(&out)
		tw := tar.NewWriter(z)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			must(err)
			data, err := io.ReadAll(tr)
			must(err)
			if change != nil {
				data = change(h, data)
			} else {
				names = append(names, h.Name)
			}
			h.Size = int64(len(data))
			must(tw.WriteHeader(h))
			_, err = tw.Write(data)
			must(err)
		}
		if extra != "" {
			must(tw.WriteHeader(&tar.Header{Name: extra, Mode: 0600, Size: 1}))
			_, err = tw.Write([]byte{'x'})
			must(err)
		}
		must(tw.Close())
		must(z.Close())
		return out.Bytes()
	}
	rewrite(nil, "")
	p, digest := stage.Source(stage.Deployments[0].Deployment)
	if bytes, err := os.ReadFile(p); err != nil || string(bytes) != "fixture source bytes" ||
		strings.Join(names, ",") != "manifest.json,orva.db,keys/master.key,"+sourceName(digest) {
		t.Fatalf("wrong archived contents: %v", names)
	}
	t.Run("guarded same owner", func(t *testing.T) {
		dst := t.TempDir()
		must(RestoreSealedFromGuardedDB(bytes.NewReader(correct), dst, func(path string) error {
			return database.RequireManagedOwnerBinding(t.Context(), path, owner)
		}))
		if _, err := os.Stat(filepath.Join(dst, "sources")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("same-host restore installed portable sources")
		}
		other := ids.New()
		if err := RestoreSealedFromGuardedDB(bytes.NewReader(correct), t.TempDir(), func(path string) error {
			return database.RequireManagedOwnerBinding(t.Context(), path, other)
		}); !errors.Is(err, database.ErrManagedOwnerMismatch) {
			t.Fatalf("foreign restore accepted: %v", err)
		}
	})
	for name, data := range map[string][]byte{
		"changed": rewrite(func(h *tar.Header, data []byte) []byte {
			if h.Name == sourceName(digest) {
				data[0] ^= 1
			}
			return data
		}, ""),
		"unlisted":     rewrite(nil, "sealed-owner/owner.json"),
		"duplicate":    rewrite(nil, "orva.db"),
		"concatenated": bytes.Join([][]byte{correct, correct}, nil),
		"truncated":    correct[:len(correct)-1],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := StageManaged(t.Context(), bytes.NewReader(data), t.TempDir()); !errors.Is(err, ErrBadArchive) {
				t.Fatalf("bad archive: %v", err)
			}
		})
	}
}
