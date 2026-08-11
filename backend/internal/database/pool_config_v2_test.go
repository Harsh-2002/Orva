package database

import (
	"path/filepath"
	"testing"
)

func TestPoolConfigV2RebuildPreservesRowsAndForeignKey(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "pool-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	fn := &Function{Name: "legacy-pool", Runtime: "node", Entrypoint: "handler.js", MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, Status: "active"}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TABLE pool_config`,
		`CREATE TABLE pool_config (
			function_id TEXT PRIMARY KEY, min_warm INTEGER NOT NULL, max_warm INTEGER NOT NULL,
			idle_ttl_s INTEGER NOT NULL, max_use_count INTEGER NOT NULL,
			target_concurrency INTEGER NOT NULL, scale_to_zero INTEGER NOT NULL,
			FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE)`,
		`INSERT INTO pool_config VALUES ('` + fn.ID + `', 7, 23, 321, 777, 9, 1)`,
	} {
		if _, err := db.write.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	cfg, err := db.GetPoolConfig(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinWarm != 0 || cfg.MaxWarm != 23 || cfg.IdleTTLS != 321 || !cfg.ScaleToZero {
		t.Fatalf("migrated config mismatch: %+v", cfg)
	}
	var targetColumns int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('pool_config') WHERE name='target_concurrency'`).Scan(&targetColumns); err != nil {
		t.Fatal(err)
	}
	if targetColumns != 0 {
		t.Fatal("target_concurrency survived rebuild")
	}
	if _, err := db.write.Exec(`DELETE FROM functions WHERE id=?`, fn.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM pool_config WHERE function_id=?`, fn.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("pool_config foreign-key cascade was not preserved")
	}
}

func TestPoolConfigNormalization(t *testing.T) {
	db := newTestDB(t)
	fn := &Function{Name: "normalize-pool", Runtime: "node", Entrypoint: "handler.js", MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, Status: "active"}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	cfg := &PoolConfig{FunctionID: fn.ID, MinWarm: 4, MaxWarm: 10, IdleTTLS: 600, ScaleToZero: true}
	if err := db.UpsertPoolConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetPoolConfig(fn.ID); got.MinWarm != 0 {
		t.Fatalf("scale-to-zero min=%d", got.MinWarm)
	}
	cfg.ScaleToZero = false
	if err := db.UpsertPoolConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetPoolConfig(fn.ID); got.MinWarm != 1 {
		t.Fatalf("active min=%d", got.MinWarm)
	}
}
