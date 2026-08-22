package database

import (
	"testing"
)

// Deployment numbers used to be handed out by functions.version, which is a
// mutation counter rather than a sequence: PUT /functions/{id} bumps it whether
// or not a field changed, and a successful build bumps it again. The dashboard's
// deploy does both, so every deploy consumed two increments and a function's
// history read v3, v5, v7, v9 instead of v1, v2, v3, v4. A brand new function's
// very first deploy was "v2".
//
// The number is a property of the deployment sequence, so it comes from that
// sequence now. This test pins the two things the old scheme got wrong: the
// first deploy is 1, and consecutive deploys are consecutive.
func TestNextDeploymentVersionIsGaplessFromOne(t *testing.T) {
	db := newTestDB(t)
	fn := insertTestFunction(t, db, "counter-fn")

	// Whatever the function's own mutation counter is doing, the sequence does
	// not care. Push it somewhere unhelpful first.
	fn.Version = 41
	if err := db.UpdateFunction(fn); err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}

	for want := int64(1); want <= 4; want++ {
		got, err := db.NextDeploymentVersion(fn.ID)
		if err != nil {
			t.Fatalf("NextDeploymentVersion: %v", err)
		}
		if got != want {
			t.Fatalf("deploy %d got version %d, want %d: versions must be gapless from 1, independent of functions.version (%d)",
				want, got, want, fn.Version)
		}
		if err := db.InsertDeployment(&Deployment{
			ID: "dep-" + string(rune('a'+want)), FunctionID: fn.ID,
			Version: got, Status: "succeeded", Phase: "done",
		}); err != nil {
			t.Fatalf("InsertDeployment: %v", err)
		}
	}
}

// Numbers are per function. Two functions deploying independently both start at
// 1 and neither sees the other's sequence.
func TestNextDeploymentVersionIsPerFunction(t *testing.T) {
	db := newTestDB(t)
	a := insertTestFunction(t, db, "fn-a")
	b := insertTestFunction(t, db, "fn-b")

	for i := 0; i < 3; i++ {
		v, err := db.NextDeploymentVersion(a.ID)
		if err != nil {
			t.Fatalf("NextDeploymentVersion(a): %v", err)
		}
		if err := db.InsertDeployment(&Deployment{
			ID: "a-" + string(rune('0'+i)), FunctionID: a.ID,
			Version: v, Status: "succeeded", Phase: "done",
		}); err != nil {
			t.Fatalf("InsertDeployment(a): %v", err)
		}
	}

	got, err := db.NextDeploymentVersion(b.ID)
	if err != nil {
		t.Fatalf("NextDeploymentVersion(b): %v", err)
	}
	if got != 1 {
		t.Errorf("a second function's first deploy got v%d, want v1: sequences are per function", got)
	}
}

// A failed deploy still consumes its number. Reusing it would make two rows
// share a version, and the dashboard addresses versions by number.
func TestNextDeploymentVersionCountsFailedDeploys(t *testing.T) {
	db := newTestDB(t)
	fn := insertTestFunction(t, db, "fails-fn")

	if err := db.InsertDeployment(&Deployment{
		ID: "d1", FunctionID: fn.ID, Version: 1, Status: "failed", Phase: "deps",
	}); err != nil {
		t.Fatalf("InsertDeployment: %v", err)
	}
	got, err := db.NextDeploymentVersion(fn.ID)
	if err != nil {
		t.Fatalf("NextDeploymentVersion: %v", err)
	}
	if got != 2 {
		t.Errorf("got v%d after a failed v1, want v2: a burned number stays burned", got)
	}
}

func insertTestFunction(t *testing.T, db *Database, name string) *Function {
	t.Helper()
	fn := &Function{
		ID: name + "-id", Name: name, Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 128, CPUs: 0.5, EnvVars: map[string]string{},
		NetworkMode: "none", ConcurrencyPolicy: "queue", AuthMode: "none",
		Version: 1, Status: "active",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatalf("InsertFunction(%s): %v", name, err)
	}
	return fn
}
