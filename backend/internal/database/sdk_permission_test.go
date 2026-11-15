package database

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSDKPermissionFreshDefaultAndStorage(t *testing.T) {
	db := newTestDB(t)
	for _, network := range []string{"none", "egress"} {
		fn := &Function{ID: network, Name: network, Runtime: "node", Entrypoint: "handler.js", NetworkMode: network}
		if err := db.InsertFunction(fn); err != nil {
			t.Fatal(err)
		}
		for _, enabled := range []bool{false, true, false} {
			fn.SDK = enabled
			if err := db.UpdateFunction(fn); err != nil {
				t.Fatal(err)
			}
			got, err := db.GetFunctionByName(network)
			if err != nil || got.SDK != enabled {
				t.Fatalf("sdk=%v: got %+v, err %v", enabled, got, err)
			}
			list, err := db.ListFunctions(ListFunctionsParams{})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range list.Functions {
				if row.ID == fn.ID && row.SDK != enabled {
					t.Fatalf("list sdk=%v, want %v", row.SDK, enabled)
				}
			}
			blob, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(blob, &fields); err != nil || fields["sdk"] != enabled {
				t.Fatalf("JSON sdk omitted or wrong: %s (%v)", blob, err)
			}
		}
	}
	if _, err := db.write.Exec(`INSERT INTO functions(id,name,runtime,network_mode) VALUES('default','default','node','egress')`); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetFunction("default")
	if err != nil || got.SDK {
		t.Fatalf("SQL default granted sdk: %+v (%v)", got, err)
	}
}

func TestSDKPermissionLegacyBackfillOnceAndReboot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	// Reproduce the pre-permission schema, including already-populated snapshots.
	if _, err := db.write.Exec(`ALTER TABLE functions DROP COLUMN sdk`); err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{"egress", "none"} {
		id := "019df200-7b00-7e00-9c00-aab1cd2e3f41"
		if network == "none" {
			id = "019df200-7b00-7e00-9c00-aab1cd2e3f42"
		}
		if _, err := db.write.Exec(`INSERT INTO functions(id,name,runtime,network_mode) VALUES(?,?,'node',?)`, id, network, network); err != nil {
			t.Fatal(err)
		}
		if _, err := db.write.Exec(`INSERT INTO deployments(id,function_id,version,status,snapshot) VALUES(?,?,1,'succeeded',?)`, id, id, `{"network_mode":"`+network+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	const explicitDep = "019df200-7b00-7e00-9c00-aab1cd2e3f43"
	if _, err := db.write.Exec(`INSERT INTO deployments(id,function_id,version,status,snapshot)
	 VALUES(?, '019df200-7b00-7e00-9c00-aab1cd2e3f41',2,'succeeded','{"network_mode":"egress","sdk":false}')`, explicitDep); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	explicit, err := db.GetDeployment(explicitDep)
	if err != nil || explicit.Snapshot == nil || explicit.Snapshot.SDK {
		t.Fatalf("migration overwrote explicit snapshot revocation: %+v (%v)", explicit, err)
	}
	for _, network := range []string{"egress", "none"} {
		fn, err := db.GetFunctionByName(network)
		if err != nil || fn.SDK != (network == "egress") {
			t.Fatalf("backfill %s: %+v (%v)", network, fn, err)
		}
		dep, err := db.GetDeployment(fn.ID)
		if err != nil || dep.Snapshot.SDK != fn.SDK {
			t.Fatalf("snapshot backfill %s: %+v (%v)", network, dep, err)
		}
		fn.SDK = false
		if err := db.UpdateFunction(fn); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Migrate(); err != nil {
			t.Fatal(err)
		}
		fn, err := db.GetFunctionByName("egress")
		if err != nil || fn.SDK {
			t.Fatalf("restart re-enabled revoked SDK: %+v (%v)", fn, err)
		}
	}
}

func TestSDKPermissionMigrationSkipsMalformedSnapshots(t *testing.T) {
	db := newTestDB(t)
	fn := insertTestFunction(t, db, "malformed-snapshot")
	if _, err := db.write.Exec(`ALTER TABLE functions DROP COLUMN sdk`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`INSERT INTO deployments(id,function_id,version,status,snapshot)
	 VALUES('invalid',?,1,'failed','not-json')`, fn.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateSDKPermission(); err != nil {
		t.Fatalf("unreadable historic snapshot blocked permission migration: %v", err)
	}
}

func TestSDKPermissionSnapshotEquality(t *testing.T) {
	fn := &Function{SDK: true}
	on := SnapshotFromFunction(fn)
	fn.SDK = false
	off := SnapshotFromFunction(fn)
	if on.Equal(off) || off.Equal(on) || !on.Equal(SnapshotFromFunction(&Function{SDK: true})) {
		t.Fatal("snapshot equality lost SDK permission")
	}
}
