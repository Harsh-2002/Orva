//go:build linux

package pool

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedEgressRotationRejectsLateSpawn(t *testing.T) {
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	limits := artifactfs.Limits{Generations: 3, Leases: 8, Staging: 1, Entries: 8,
		PathBytes: 256, LinkBytes: 256, Bytes: 8192, FileBytes: 1024, ManifestBytes: 8192}
	root := t.TempDir()
	check(os.Chmod(root, 0700))
	store, err := artifactfs.New(root, limits)
	check(err)
	t.Cleanup(func() { check(store.Close()) })
	seal := func(kind artifactfs.Kind, owner string) string {
		t.Helper()
		stage, err := store.Begin(t.Context(), 1)
		check(err)
		root, err := stage.Path()
		check(err)
		check(os.Chmod(root, 0700))
		manifest := artifactfs.Manifest{Version: 1, Entries: []artifactfs.Entry{{
			Path: ".", Type: artifactfs.TypeDirectory, Mode: 0700, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid()),
		}}}
		digest, err := artifactfs.Digest(manifest, limits)
		check(err)
		generation, err := stage.Seal(t.Context(), kind, owner, manifest, digest)
		check(err)
		t.Cleanup(func() {
			check(store.Retire(generation))
			check(store.Reclaim(generation))
		})
		return generation
	}
	runtime := seal(artifactfs.KindRuntime, "node")
	factory := func(mode string) *workloadspawn.Factory {
		t.Helper()
		owner := ids.New()
		code := seal(artifactfs.KindCode, owner)
		f, err := workloadspawn.NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{
			Function: &database.Function{ID: owner, Name: mode, Runtime: "node", Entrypoint: "handler.js",
				Status: "active", Version: 1, CodeHash: strings.Repeat("a", 64), AuthMode: "none", NetworkMode: mode,
				MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, MaxConcurrency: 1, ConcurrencyPolicy: "queue"},
			Artifact: database.WorkloadArtifactRecord{OwnerID: owner, Generation: ids.New(), Revision: 1, Epoch: 1},
		}, store, code, store, runtime, workloadspawn.FactoryLimits{
			MaxPreparedPlans: 1, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20,
		})
		check(err)
		t.Cleanup(f.Close)
		return f
	}
	egress, offline := factory("egress"), factory("none")
	m, _ := egressTestManager(t)
	authority, err := workloadiam.NewAuthority(4, nil)
	check(err)
	m.managed = newManagedState(authority)
	_, m.hostMem = controllerTestPool()
	t.Cleanup(func() { check(m.Shutdown(context.Background())) })
	s := newScaler(m, m.hostMem)
	check(m.InstallFactory(t.Context(), egress))
	check(m.InstallFactory(t.Context(), offline))
	old, untouched := poolOf(t, m, egress.Owner()), poolOf(t, m, offline.Owner())
	offlineWorker := &sandbox.Worker{}
	untouched.idle <- offlineWorker
	replacementWorker := &sandbox.Worker{}

	generation := "g1"
	simulateSpawn := errors.New("fixture stops before starting nsjail")
	var policyReads []string
	m.SetEgressPolicy(func() (string, string, error) {
		policyReads = append(policyReads, generation)
		return "", generation, simulateSpawn
	})
	oldWorker := &sandbox.Worker{}
	started, finish := make(chan struct{}), make(chan struct{})
	endSpawn := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(func() {
		endSpawn()
		s.spawnWG.Wait()
		_ = offlineWorker.Kill()
		_ = replacementWorker.Kill()
	})
	old.spawnFn = func(context.Context) (*sandbox.Worker, error) {
		_, _, _ = m.egressPolicyForSpawn("egress")
		close(started)
		<-finish
		return oldWorker, nil
	}
	if !s.startSpawn(old, "test") {
		t.Fatal("old pool did not schedule a spawn")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("old spawn did not reach the barrier")
	}
	generation = "g2"
	if got := m.RetireEgressPools(); got != 1 {
		t.Fatalf("rotated %d pools, want 1", got)
	}
	next := poolOf(t, m, egress.Owner())
	if next == old || !old.closing.Load() || next.closing.Load() || m.CurrentFactory(egress.Owner()) != egress {
		t.Fatal("rotation did not replace only the egress worker pool")
	}
	endSpawn()
	s.spawnWG.Wait()
	if !oldWorker.IsDead() || len(old.idle) != 0 || len(next.idle) != 0 ||
		old.spawning.Load() != 0 || m.hostMem.reserved.Load() != 0 {
		t.Fatal("late old spawn survived, parked, or leaked its reservation")
	}

	// Use the installed replacement's real Factory preparation and policy lookup;
	// only the process startup is simulated.
	spawn := next.spawnFn
	next.spawnFn = func(ctx context.Context) (*sandbox.Worker, error) {
		w, err := spawn(ctx)
		if errors.Is(err, simulateSpawn) {
			return replacementWorker, nil
		}
		return w, err
	}
	if !s.startSpawn(next, "test") {
		t.Fatal("replacement pool could not schedule work")
	}
	s.spawnWG.Wait()
	if want := "g1,g2"; strings.Join(policyReads, ",") != want {
		t.Fatalf("policy reads = %v, want %s", policyReads, want)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	acquired, err := m.Acquire(ctx, egress.Owner())
	check(err)
	defer m.Release(acquired, nil)
	if acquired.Worker != replacementWorker || !m.AcceptsManagedVersion(acquired, 1) {
		t.Fatal("managed acquire did not use the replacement pool and original Factory version")
	}
	if poolOf(t, m, offline.Owner()) != untouched || untouched.closing.Load() || len(untouched.idle) != 1 ||
		offlineWorker.IsDead() || m.CurrentFactory(offline.Owner()) != offline {
		t.Fatal("egress rotation changed the offline pool")
	}

	t.Run("concurrent tuning keeps the installed Factory and busy slot", func(t *testing.T) {
		tuning := &database.PoolConfig{FunctionID: egress.Owner(), IdleTTLS: 47, ScaleToZero: true}
		start := make(chan struct{})
		var rotations sync.WaitGroup
		rotations.Go(func() { <-start; m.RefreshManagedPool(tuning)() })
		rotations.Go(func() { <-start; m.RetireEgressPools() })
		close(start)
		rotations.Wait()
		current := poolOf(t, m, egress.Owner())
		if current == next || !next.closing.Load() || current.closing.Load() ||
			!current.scaleToZero || current.min != 0 || current.idleTTL != 47*time.Second {
			t.Fatal("egress rotation lost the new tuning")
		}
		if current.managed != next.managed || m.CurrentFactory(egress.Owner()) != egress || egress.Retired() {
			t.Fatal("tuning replaced the installed Factory")
		}
		if current.concSem == nil || current.concSem != next.concSem || len(current.concSem) != 1 {
			t.Fatal("tuning created a second concurrency budget or released a busy slot")
		}
		m.Release(acquired, nil)
		if len(current.concSem) != 0 || !replacementWorker.IsDead() {
			t.Fatal("release did not free the shared slot and retire the old worker")
		}
	})
}
