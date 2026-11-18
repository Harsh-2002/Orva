//go:build linux

package builder

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func publicationQueueFixture(t *testing.T) (*PublicationQueue, context.Context, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	schema := func(name string) {
		src, err := parser.ParseFile(token.NewFileSet(), "../database/"+name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(src, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				body, _ := strconv.Unquote(lit.Value)
				if strings.Contains(body, "CREATE TABLE IF NOT EXISTS") {
					if _, err := conn.Exec(body); err != nil {
						t.Fatal(err)
					}
				}
			}
			return true
		})
	}
	schema("workload_iam_schema.go")
	schema("workload_artifact_schema.go")
	owner, key := ids.New(), ids.New()
	fn := &database.Function{ID: owner, Name: "queued", Runtime: "node", Entrypoint: "handler.js", Status: "active", Version: 1,
		MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, NetworkMode: "none", AuthMode: "none", ConcurrencyPolicy: "queue", EnvVars: map[string]string{}}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	actor := database.IAMActorReference{Kind: "system", ID: ids.New()}
	r, err := db.NewIAMPolicy(t.Context(), owner, false, actor)
	if err != nil {
		t.Fatal(err)
	}
	schema("workload_managed_schema.go")
	if _, err := db.NewWorkloadArtifact(t.Context(), owner, actor); err != nil {
		t.Fatal(err)
	}
	if err := db.InitWorkloadDeploymentSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAPIKey(&database.APIKey{ID: key, Name: "writer", KeyHash: "fixture", Prefix: "fixture", Permissions: `["write"]`}); err != nil {
		t.Fatal(err)
	}
	e, err := r.AdmissionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	a, err := workloadiam.NewAuthority(8, []*workloadiam.Evaluator{e})
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	b.DataDir = t.TempDir()
	q := NewQueue(b, db, registry.New(db))
	q.jobs = make(chan BuildJob, 1)
	q.workers = 0
	s := &PublicationQueue{queue: q, authority: a, pending: map[string]*artifactfs.PreparedPaths{}}
	q.publication = s
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: key})
	return s, ctx, owner
}

func TestPublicationQueueFullCancellationAndDisabledLegacy(t *testing.T) {
	s, ctx, owner := publicationQueueFixture(t)
	ref, _ := auth.OperatorFromContext(ctx)
	archive := func() string {
		f, err := os.CreateTemp(t.TempDir(), "archive-")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		return f.Name()
	}
	path := archive()
	ctx, cancel := context.WithCancel(ctx)
	first, err := s.Submit(ctx, owner, path)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("accepted work followed request cancellation")
	}
	secondPath := archive()
	second, err := s.Submit(auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: ref.ID}), owner, secondPath)
	if !errors.Is(err, ErrQueueFull) || second == "" {
		t.Fatalf("queue full not recorded: %v", err)
	}
	dep, err := s.queue.db.GetDeployment(second)
	if err != nil || dep.Status != "failed" {
		t.Fatal("full queue left live work", err)
	}
	if _, err := os.Stat(secondPath); !os.IsNotExist(err) {
		t.Fatal("rejected archive leaked")
	}
	if err := s.queue.Submit(BuildJob{DeploymentID: ids.New(), FunctionID: owner}); !errors.Is(err, ErrPublicationUnavailable) {
		t.Fatal("legacy bypass accepted")
	}
	s.Shutdown(context.Background())
	dep, err = s.queue.db.GetDeployment(first)
	if err != nil || dep.Status != "failed" {
		t.Fatal("shutdown left queued work", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("never-started archive leaked")
	}
	missing := archive()
	if _, err := s.Submit(t.Context(), owner, missing); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatal("missing reference fell back")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("untrusted archive leaked")
	}
}

func publicationFixtureSealer(t *testing.T, store *artifactfs.Store, limits artifactfs.Limits) func(artifactfs.Kind, string, []string, string, os.FileMode) string {
	return func(kind artifactfs.Kind, owner string, dirs []string, filename string, mode os.FileMode) string {
		t.Helper()
		stage, err := store.Begin(t.Context(), 4096)
		if err != nil {
			t.Fatal(err)
		}
		root, err := stage.Path()
		if err != nil {
			t.Fatal(err)
		}
		manifest := artifactfs.Manifest{Version: 1}
		for _, name := range append([]string{"."}, dirs...) {
			if name != "." {
				if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
					t.Fatal(err)
				}
			}
			dirMode := uint32(0755)
			if name == "." {
				dirMode = 0700
			}
			if err := os.Chmod(filepath.Join(root, name), os.FileMode(dirMode)); err != nil {
				t.Fatal(err)
			}
			manifest.Entries = append(manifest.Entries, artifactfs.Entry{Path: name, Type: artifactfs.TypeDirectory,
				Mode: dirMode, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())})
		}
		body := []byte("// orva-sdk-proto:1")
		files := map[string]os.FileMode{filename: mode}
		if kind == artifactfs.KindRuntime {
			files["opt/orva/adapter.js"] = 0644
		}
		sum := sha256.Sum256(body)
		for name, fileMode := range files {
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, body, fileMode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, fileMode); err != nil {
				t.Fatal(err)
			}
			manifest.Entries = append(manifest.Entries, artifactfs.Entry{Path: name, Type: artifactfs.TypeFile,
				Mode: uint32(fileMode), UID: uint32(os.Geteuid()), GID: uint32(os.Getegid()),
				Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
		}
		digest, err := artifactfs.Digest(manifest, limits)
		if err != nil {
			t.Fatal(err)
		}
		id, err := stage.Seal(t.Context(), kind, owner, manifest, digest)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := store.Retire(id); err != nil {
				t.Error(err)
			} else if err := store.Reclaim(id); err != nil {
				t.Error(err)
			}
		})
		return id
	}
}

func TestPublicationQueuePause(t *testing.T) {
	s, ctx, owner := publicationQueueFixture(t)
	db := s.queue.db
	if err := db.InitWorkloadOperatorPauseSchema(ctx); err != nil {
		t.Fatal(err)
	}
	store, _, limits := outputTestStore(t)
	// Sealed fixture files qualify Factory ownership, not runtime execution.
	seal := publicationFixtureSealer(t, store, limits)
	code := seal(artifactfs.KindCode, owner, nil, "handler.js", 0644)
	runtime := seal(artifactfs.KindRuntime, "node", []string{"usr", "usr/local", "usr/local/bin", "opt", "opt/orva"}, "usr/local/bin/node", 0755)
	raw, err := db.GetWorkloadArtifactSnapshot(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	raw.Function.CodeHash = strings.Repeat("a", 64)
	if err := db.UpdateFunction(raw.Function); err != nil {
		t.Fatal(err)
	}
	factory, err := workloadspawn.NewFactory(ctx, &secrets.PreparedArtifactSnapshot{
		Artifact: raw.Artifact, Function: raw.Function, Secrets: map[string]string{},
	}, store, code, store, runtime, workloadspawn.FactoryLimits{MaxPreparedPlans: 1, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	s.manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: s.authority}, pool.SandboxTemplate{}, db, s.queue.reg, nil)
	t.Cleanup(func() {
		if err := s.manager.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	defer factory.Close()
	if err := s.manager.InstallFactory(ctx, factory); err != nil {
		t.Fatal(err)
	}
	policy, err := db.GetIAMPolicy(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	req := database.WorkloadPauseRequest{OwnerID: owner, ExpectedGeneration: raw.Artifact.Generation,
		ExpectedPolicyRevision: policy.Revision, ExpectedAuthorityEpoch: policy.AuthorityEpoch}
	if err := s.Pause(t.Context(), req); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatalf("untrusted pause: %v", err)
	}
	stale := req
	stale.ExpectedPolicyRevision++
	if err := s.Pause(ctx, stale); !errors.Is(err, database.ErrIAMConflict) {
		t.Fatalf("stale installed revision: %v", err)
	}
	stale = req
	stale.ExpectedAuthorityEpoch++
	if err := s.Pause(ctx, stale); !errors.Is(err, database.ErrWorkloadArtifactConflict) {
		t.Fatalf("stale durable epoch: %v", err)
	}
	if s.manager.CurrentArtifact(owner) != factory.ArtifactRevision() || factory.Retired() {
		t.Fatal("denied pause invalidated Factory")
	}
	s.queue.reg.PublishEvent = func(_ string, _ any) {
		lock := s.queue.reg.MutationLock(owner)
		if !lock.TryLock() {
			t.Error("notification ran under owner lock")
			return
		}
		lock.Unlock()
		if err := s.authority.ChangeHostState(func() {
			if s.manager.CurrentFactory(owner) != nil {
				t.Error("notification preceded invalidation")
			}
		}); err != nil {
			t.Error(err)
		}
	}
	if err := s.Pause(ctx, req); err != nil {
		t.Fatal(err)
	}
	fn, err := s.queue.reg.Get(owner)
	if err != nil || fn.Status != "inactive" || !factory.Retired() || s.manager.CurrentArtifact(owner) != "" {
		t.Fatalf("pause did not close the actual Factory and publish inactive cache state: %v", err)
	}
	if err := s.Pause(ctx, req); !errors.Is(err, database.ErrWorkloadArtifactConflict) {
		t.Fatalf("old pause CAS was reusable: %v", err)
	}
}

func TestPublicationQueueRollback(t *testing.T) {
	s, _, owner := publicationQueueFixture(t)
	db := s.queue.db
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	admin := ids.New()
	must(db.InsertAPIKey(&database.APIKey{ID: admin, Name: "admin", KeyHash: "admin-fixture", Prefix: "admin-fixture", Permissions: `["admin","write"]`}))
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: admin})
	ref, _ := auth.OperatorFromContext(ctx)
	must(db.InitWorkloadOperatorPauseSchema(ctx))
	store, _, limits := outputTestStore(t)
	s.code, s.runtime = store, store
	s.secrets = secrets.New(db, t.TempDir())
	seal := publicationFixtureSealer(t, store, limits)
	runtime := seal(artifactfs.KindRuntime, "node", []string{"usr", "usr/local", "usr/local/bin", "opt", "opt/orva"}, "usr/local/bin/node", 0755)
	publish := func(code, run, hash string) string {
		t.Helper()
		paths, err := artifactfs.PreparePaths(store, code, store, runtime)
		must(err)
		defer paths.Release()
		c, r := paths.Identities()
		p, err := db.CaptureWorkloadDeployment(ctx, ref, owner)
		must(err)
		o := database.WorkloadBuildOutput{CodeGeneration: code, CodeDigest: c.Digest, RuntimeGeneration: runtime,
			RuntimeDigest: r.Digest, CodeHash: strings.Repeat(hash, 64), Entrypoint: run}
		must(db.RecordWorkloadBuildOutput(ctx, p.ID, o))
		_, err = db.PublishWorkloadDeployment(ctx, p.ID, o)
		must(err)
		return p.ID
	}
	targetCode := seal(artifactfs.KindCode, owner, nil, "handler.js", 0644)
	target := publish(targetCode, "handler.js", "a")
	unusableCode := seal(artifactfs.KindCode, owner, nil, "handler.js", 0644)
	unusable := publish(unusableCode, "absent.js", "b")
	retiredCode := seal(artifactfs.KindCode, owner, nil, "handler.js", 0644)
	retired := publish(retiredCode, "handler.js", "d")
	must(store.Retire(retiredCode))
	currentCode := seal(artifactfs.KindCode, owner, nil, "handler.js", 0644)
	current := publish(currentCode, "handler.js", "c")
	policy, err := db.GetIAMPolicy(ctx, owner)
	must(err)
	eval, err := policy.AdmissionEvaluator()
	must(err)
	s.authority, err = workloadiam.NewAuthority(8, []*workloadiam.Evaluator{eval})
	must(err)
	s.manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: s.authority}, pool.SandboxTemplate{}, db, s.queue.reg, nil)
	t.Cleanup(func() { must(s.manager.Shutdown(context.Background())) })
	old, err := s.Install(ctx, current)
	must(err)
	oldFactory := s.manager.CurrentFactory(owner)
	if old == "" || oldFactory == nil {
		t.Fatal("old Factory missing")
	}
	if err := s.Rollback(ctx, owner, unusable); !errors.Is(err, ErrPublicationUnavailable) {
		t.Fatalf("unverified entrypoint: %v", err)
	}
	if err := s.Rollback(ctx, owner, retired); !errors.Is(err, ErrPublicationUnavailable) {
		t.Fatalf("unavailable sealed pair: %v", err)
	}
	legacy := filepath.Join(s.queue.bld.DataDir, "functions", owner)
	must(os.MkdirAll(legacy, 0700))
	if err := s.Rollback(ctx, owner, target); !errors.Is(err, ErrPublicationUnavailable) {
		t.Fatalf("legacy namespace: %v", err)
	}
	must(os.Remove(legacy))
	conn, err := sql.Open("sqlite", db.Path())
	must(err)
	defer conn.Close()
	_, err = conn.Exec(`CREATE TABLE rollback_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
		CREATE TRIGGER reject_rollback AFTER INSERT ON workload_deployment_promotion
		BEGIN INSERT INTO rollback_guard VALUES('missing-owner'); END`)
	must(err)
	if err := s.Rollback(ctx, owner, target); !errors.Is(err, database.ErrWorkloadArtifactStorage) {
		t.Fatalf("rollback commit failure: %v", err)
	}
	if s.manager.CurrentFactory(owner) != oldFactory || oldFactory.Retired() {
		t.Fatal("failed rollback invalidated old Factory")
	}
	_, err = conn.Exec(`DROP TRIGGER reject_rollback`)
	must(err)
	s.queue.reg.PublishEvent = func(_ string, _ any) {
		for _, lock := range []*sync.Mutex{s.manager.FunctionLock(owner), s.queue.reg.MutationLock(owner)} {
			if !lock.TryLock() {
				t.Fatal("callback ran under mutation/build lock")
			}
			lock.Unlock()
		}
		must(s.authority.ChangeHostState(func() {
			if s.manager.CurrentFactory(owner) != nil {
				t.Error("callback preceded invalidation")
			}
		}))
	}
	must(s.Rollback(ctx, owner, target))
	fn, err := s.queue.reg.Get(owner)
	must(err)
	if fn.Status != "inactive" || fn.ActiveDeploymentID != target || !oldFactory.Retired() {
		t.Fatal("committed promotion not cached or old Factory not retired")
	}
	artifact, err := s.Install(ctx, target)
	must(err)
	c, _ := s.manager.CurrentFactory(owner).Identities()
	if artifact == old || c.Generation != targetCode {
		t.Fatal("installation failed to pin the exact promoted code")
	}
}
