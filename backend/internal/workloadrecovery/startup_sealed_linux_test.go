//go:build linux

package workloadrecovery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestStartupSealedSameBoot(t *testing.T) {
	if os.Getenv("ORVA_STARTUP_SEALED_TEST") != "disposable-vm" {
		t.Skip("requires an explicitly delegated disposable VM cgroup")
	}
	budget, parent := startupSealedBudget(t)
	for _, mode := range []string{"exact", "missing-runtime", "wrong-runtime"} {
		t.Run(mode, func(t *testing.T) {
			testStartupSealed(t, budget, parent, mode)
		})
	}
}

func startupSealedBudget(t *testing.T) (*workloadfence.Budget, string) {
	t.Helper()
	raw, err := os.ReadFile("/proc/self/cgroup")
	must(t, err)
	base := os.Getenv("ORVA_STARTUP_SEALED_BUDGET")
	if base != "/sys/fs/cgroup/orva-startup-sealed" || string(raw) != "0::/orva-startup-sealed\n" {
		t.Fatal("test process is not inside its dedicated VM budget")
	}
	budget, err := workloadfence.CaptureBudget()
	must(t, err)
	t.Cleanup(func() { must(t, budget.Close()) })
	daemon, parent := filepath.Join(base, "daemon"), filepath.Join(base, "domains")
	must(t, os.Mkdir(daemon, 0700))
	must(t, os.Mkdir(parent, 0700))
	must(t, os.WriteFile(filepath.Join(daemon, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600))
	raw, err = os.ReadFile(filepath.Join(base, "cgroup.procs"))
	must(t, err)
	if len(raw) != 0 {
		t.Fatal("refusing to delegate a shared cgroup")
	}
	for _, path := range []string{base, parent} {
		must(t, os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600))
	}
	return budget, parent
}

type startupSealedPair struct {
	owner, deployment, approved string
	code, runtime               artifactfs.Identity
}

func testStartupSealed(t *testing.T, budget *workloadfence.Budget, parent, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	f, _ := seed(t, true)
	limits := artifactfs.Limits{Generations: 8, Leases: 16, Staging: 2, Entries: 8,
		PathBytes: 128, LinkBytes: 128, ManifestBytes: 4096, Bytes: 1 << 20, FileBytes: 4096}
	root := t.TempDir()
	cfg := artifactfs.OwnerConfig{MetadataRoot: filepath.Join(root, "owner"), CodeRoot: filepath.Join(root, "code"),
		RuntimeRoot: filepath.Join(root, "runtime"), Parent: parent, Budget: budget,
		CodeLimits: limits, RuntimeLimits: limits, MaxEpochs: 2}
	for _, dir := range []string{cfg.MetadataRoot, cfg.CodeRoot, cfg.RuntimeRoot} {
		must(t, os.Mkdir(dir, 0700))
	}
	old, err := artifactfs.NewOwner(ctx, cfg)
	must(t, err)
	t.Cleanup(func() { closeStartupOwner(t, old) })
	bindings, err := old.Bindings(ctx)
	must(t, err)
	oldRef, err := bindings.Domain.Reference(ctx)
	must(t, err)

	pairs := seedStartupSealed(t, f.dir, bindings, limits, mode)
	must(t, old.Close(ctx))
	if lease, err := bindings.Code.Acquire(pairs[0].code.Generation); !errors.Is(err, artifactfs.ErrClosed) {
		if lease != nil {
			lease.Release()
		}
		t.Fatalf("old owner still acquires code: %v", err)
	}
	c := coordinator(t, f.dir)
	op, err := c.PrepareManual(ctx)
	must(t, err)
	must(t, c.CompleteManual(ctx, op))
	f.deployment = ids.New()
	func() {
		db := openDB(t, f.dir)
		defer func() { must(t, db.Close()) }()
		_, err := db.WriteDB().Exec(`INSERT INTO deployments(id,function_id,version,status) VALUES(?,?,2,'building')`,
			f.deployment, f.owners[0])
		must(t, err)
		assertStartupBuildStatus(t, db, f.deployment, "building")
	}()

	var recovered *artifactfs.Owner
	var factories []*workloadspawn.Factory
	defer func() {
		for _, factory := range factories {
			factory.Close()
		}
		if recovered != nil {
			closeStartupOwner(t, recovered)
		}
	}()

	reconstructed, started := false, false
	db, result, err := c.Start(ctx, func(ctx context.Context, db *database.Database) error {
		reconstructed = true
		assertRetained(t, db, f, f.dir, true)
		paused, err := db.WorkloadOperatorPaused(ctx, f.owners[0])
		must(t, err)
		if !paused {
			t.Fatal("lost durable operator pause before reconstruction")
		}
		for _, pair := range pairs {
			assertStartupSealedDeny(t, db, pair)
		}
		recovered, err = artifactfs.OpenOwnerRecovery(ctx, cfg)
		if err != nil {
			return err
		}
		fresh, err := recovered.Bindings(ctx)
		if err != nil {
			return err
		}
		ref, err := fresh.Domain.Reference(ctx)
		if err != nil {
			return err
		}
		if ref.Boot != oldRef.Boot || ref.Epoch == oldRef.Epoch {
			t.Fatal("reconstruction did not create a distinct same-boot domain")
		}
		for _, pair := range pairs {
			// Verify the persisted selection, not whichever generation remains acquirable.
			factory, err := reconstructStartupPair(ctx, db, f.dir, fresh, pair)
			if err != nil {
				return err
			}
			factories = append(factories, factory)
		}
		return nil
	}, func(_ context.Context, db *database.Database) error {
		started = true
		if len(factories) != len(pairs) || !c.host.AdmissionClosed() {
			t.Fatal("start callback preceded complete closed reconstruction")
		}
		for _, pair := range pairs {
			assertStartupSealedDeny(t, db, pair)
		}
		return nil
	})
	if db != nil {
		must(t, db.Close())
	}
	if !reconstructed || result.Operation != op || !c.host.AdmissionClosed() {
		t.Fatalf("did not reconstruct after durable closed recovery: %v", err)
	}
	if mode != "exact" {
		want := artifactfs.ErrInvalid
		if mode == "wrong-runtime" {
			want = artifactfs.ErrIntegrity
		}
		if db != nil || started || len(factories) != 0 || !errors.Is(err, want) {
			t.Fatalf("invalid association reached startup: callback=%v factories=%d error=%v", started, len(factories), err)
		}
		return
	}
	must(t, err)
	if db == nil || !started {
		t.Fatal("exact sealed pairs did not reach staged start callback")
	}
}

func closeStartupOwner(t *testing.T, owner *artifactfs.Owner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	must(t, owner.Close(ctx))
}

func assertStartupBuildStatus(t *testing.T, db *database.Database, id, want string) {
	t.Helper()
	var status string
	must(t, db.ReadDB().QueryRow(`SELECT status FROM deployments WHERE id=?`, id).Scan(&status))
	if status != want {
		t.Fatalf("deployment status = %q, want %q", status, want)
	}
}

func startupDigest(data string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(data))) }

func sealStartupFile(t *testing.T, store *artifactfs.Store, limits artifactfs.Limits, kind artifactfs.Kind, owner, path, contents string) artifactfs.Identity {
	t.Helper()
	stage, err := store.Begin(t.Context(), 4096)
	must(t, err)
	sealed := false
	defer func() {
		if !sealed {
			must(t, stage.Abort())
		}
	}()
	dir, err := stage.Path()
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, path), []byte(contents), 0644))
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	m := artifactfs.Manifest{Version: 1, Entries: []artifactfs.Entry{
		{Path: ".", Type: artifactfs.TypeDirectory, Mode: 0700, UID: uid, GID: gid},
		{Path: path, Type: artifactfs.TypeFile, Mode: 0644, UID: uid, GID: gid, Size: int64(len(contents)), SHA256: startupDigest(contents)},
	}}
	digest, err := artifactfs.Digest(m, limits)
	must(t, err)
	generation, err := stage.Seal(t.Context(), kind, owner, m, digest)
	must(t, err)
	sealed = true
	return artifactfs.Identity{Kind: kind, Owner: owner, Generation: generation, Digest: digest}
}

func seedStartupSealed(t *testing.T, dir string, bindings artifactfs.OwnerBindings, limits artifactfs.Limits, mode string) []startupSealedPair {
	t.Helper()
	ctx := t.Context()
	db := openDB(t, dir)
	defer func() { must(t, db.Close()) }()
	must(t, db.InitWorkloadOperatorPauseSchema(ctx))
	must(t, db.InitWorkloadDeploymentSchema(ctx))
	// The artifact initializer is package-private. Use its exact fixture DDL.
	raw, err := os.ReadFile("../database/workload_artifact_schema.go")
	must(t, err)
	ddl := strings.Split(string(raw), "`")
	if len(ddl) != 3 || !strings.Contains(ddl[1], "CREATE TABLE IF NOT EXISTS workload_artifact_generations") {
		t.Fatal("artifact fixture DDL not found")
	}
	_, err = db.WriteDB().Exec(ddl[1])
	must(t, err)
	actor := database.IAMActorReference{Kind: "system", ID: ids.New()}
	var pausedOwner string
	must(t, db.ReadDB().QueryRow(`SELECT function_id FROM jobs WHERE origin_kind='function'`).Scan(&pausedOwner))
	pausedGeneration, err := db.NewWorkloadArtifact(ctx, pausedOwner, actor)
	must(t, err)
	pausedPolicy, err := db.GetIAMPolicy(ctx, pausedOwner)
	must(t, err)
	_, err = db.WriteDB().Exec(`INSERT INTO workload_operator_pause VALUES(?,?,?,?,?,?,?)`,
		pausedOwner, pausedGeneration.Generation, pausedPolicy.Revision, pausedPolicy.AuthorityEpoch, actor.Kind, actor.ID, time.Now().UTC().Format(time.RFC3339Nano))
	must(t, err)

	var pairs []startupSealedPair
	var outputs []database.WorkloadBuildOutput
	for _, lang := range []string{"node", "python"} {
		owner, deployment := ids.New(), ids.New()
		entrypoint, source := "handler.js", `exports.handler = () => ({statusCode:200,body:"sealed-node"})`
		if lang == "python" {
			entrypoint, source = "handler.py", "def handler(event):\n    return {'statusCode': 200, 'body': 'sealed-python'}\n"
		}
		code := sealStartupFile(t, bindings.Code, limits, artifactfs.KindCode, owner, entrypoint, source)
		runtime := sealStartupFile(t, bindings.Runtime, limits, artifactfs.KindRuntime, lang, "fixture-runtime", lang)
		must(t, db.InsertFunction(&database.Function{ID: owner, Name: "startup-" + lang, Runtime: lang,
			Entrypoint: entrypoint, Status: "active", CodeHash: startupDigest(source), Version: 1, SDK: true,
			MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, NetworkMode: "none", EnvVars: map[string]string{}, ActiveDeploymentID: deployment}))
		generation, err := db.NewWorkloadArtifact(ctx, owner, actor)
		must(t, err)
		prepared, err := secrets.New(db, dir).PrepareArtifactSnapshot(ctx, owner, startupSecretLimits)
		must(t, err)
		factory, err := workloadspawn.NewFactory(ctx, prepared, bindings.Code, code.Generation, bindings.Runtime, runtime.Generation, startupFactoryLimits)
		must(t, err)
		approved := factory.ArtifactRevision()
		factory.Close()
		const policy = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"retained"}},"context":"execution"}],"inbound":[]}`
		before, err := db.NewIAMPolicy(ctx, owner, false, actor)
		must(t, err)
		saved, err := db.SaveIAMPolicy(ctx, database.IAMPolicyChange{OwnerID: owner, ExpectedRevision: before.Revision,
			ExpectedApprovedArtifact: before.ApprovedArtifact, ApprovedArtifact: approved, PolicyJSON: []byte(policy), Actor: actor})
		must(t, err)
		_, err = db.WriteDB().Exec(`INSERT INTO workload_iam_inline(function_id,policy_json) VALUES(?,?)`, owner, saved.PolicyJSON)
		must(t, err)
		_, err = db.WriteDB().Exec(`INSERT INTO deployments(id,function_id,version,status) VALUES(?,?,1,'succeeded')`, deployment, owner)
		must(t, err)
		pairs = append(pairs, startupSealedPair{owner, deployment, approved, code, runtime})
		outputs = append(outputs, database.WorkloadBuildOutput{CodeGeneration: code.Generation, CodeDigest: code.Digest,
			RuntimeGeneration: runtime.Generation, RuntimeDigest: runtime.Digest, CodeHash: prepared.Function.CodeHash, Entrypoint: entrypoint})
		_, err = db.WriteDB().Exec(`INSERT INTO workload_deployment_capture(deployment_id,function_id,provenance,published_generation) VALUES(?,?,'{}',?)`,
			deployment, owner, generation.Generation)
		must(t, err)
	}
	switch mode {
	case "missing-runtime":
		outputs[0].RuntimeGeneration = ids.New()
	case "wrong-runtime":
		outputs[0].RuntimeGeneration, outputs[0].RuntimeDigest = pairs[1].runtime.Generation, pairs[1].runtime.Digest
	}
	for i, output := range outputs {
		raw, err := json.Marshal(output)
		must(t, err)
		_, err = db.WriteDB().Exec(`UPDATE workload_deployment_capture SET output=? WHERE deployment_id=?`, string(raw), pairs[i].deployment)
		must(t, err)
	}
	return pairs
}

var startupSecretLimits = secrets.SecretSnapshotLimits{MaxSecrets: 4, MaxTotalBytes: 4096}
var startupFactoryLimits = workloadspawn.FactoryLimits{MaxPreparedPlans: 2, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20}

func reconstructStartupPair(ctx context.Context, db *database.Database, dir string, bindings artifactfs.OwnerBindings, pair startupSealedPair) (*workloadspawn.Factory, error) {
	installation, err := db.PrepareWorkloadDeploymentInstallation(ctx, pair.deployment)
	if err != nil {
		return nil, err
	}
	output := installation.Output()
	prepared, err := secrets.New(db, dir).PrepareArtifactSnapshot(ctx, pair.owner, startupSecretLimits)
	if err != nil {
		return nil, err
	}
	fn := prepared.Function
	paths, err := artifactfs.PreparePaths(bindings.Code, output.CodeGeneration, bindings.Runtime, output.RuntimeGeneration)
	if err != nil {
		return nil, err
	}
	defer paths.Release()
	code, runtime := paths.Identities()
	if code.Owner != fn.ID || code.Digest != output.CodeDigest ||
		runtime.Owner != fn.Runtime || runtime.Digest != output.RuntimeDigest ||
		fn.ActiveDeploymentID != pair.deployment || fn.CodeHash != output.CodeHash ||
		fn.Entrypoint != output.Entrypoint {
		return nil, artifactfs.ErrIntegrity
	}
	if code != pair.code || runtime != pair.runtime {
		return nil, fmt.Errorf("recovered the wrong committed pair")
	}
	return workloadspawn.NewFactory(ctx, prepared, bindings.Code, code.Generation, bindings.Runtime, runtime.Generation, startupFactoryLimits)
}

func assertStartupSealedDeny(t *testing.T, db *database.Database, pair startupSealedPair) {
	t.Helper()
	policy, err := db.GetIAMPolicy(t.Context(), pair.owner)
	must(t, err)
	if !policy.Quarantined || policy.ApprovedArtifact != pair.approved || policy.PolicyJSON == database.RecoveryEmptyPolicy {
		t.Fatal("sealed workload lost quarantine or retained inspection metadata")
	}
	eval, err := policy.AdmissionEvaluator()
	must(t, err)
	key := "retained"
	if eval.Allows(workloadiam.Context{Principal: workloadiam.Principal{FunctionID: pair.owner, WorkerID: "worker", ArtifactRevision: pair.approved},
		Phase: workloadiam.Execution, Ready: true, Lease: workloadiam.Lease{ID: "dispatch", WorkerID: "worker", ExecutionID: ids.New(), Active: true}},
		workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: pair.owner, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}) {
		t.Fatal("recovered exact bytes revived the stored grant")
	}
}
