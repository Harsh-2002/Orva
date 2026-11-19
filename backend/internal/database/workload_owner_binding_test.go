package database

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedOwnerBinding(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orva.db")
	owner := ids.New()
	if err := InspectManagedOwnerBinding(ctx, path, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only preflight created database: %v", err)
	}
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := InspectManagedOwnerBinding(ctx, path, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.BindManagedOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := InspectManagedOwnerBinding(ctx, path, owner); err != nil {
		t.Fatal(err)
	}
	if other := ids.New(); !errors.Is(InspectManagedOwnerBinding(ctx, path, other), ErrManagedOwnerMismatch) ||
		!errors.Is(db.BindManagedOwner(ctx, other), ErrManagedOwnerMismatch) {
		t.Fatal("wrong Owner binding accepted")
	}
	if _, err := db.WriteDB().Exec(`DELETE FROM workload_owner_binding`); err == nil {
		t.Fatal("owner identity silently removed")
	}
}

func TestManagedOwnerBindingLegacyDoesNotWrite(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orva.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertFunction(&Function{ID: ids.New(), Name: "legacy", Runtime: "node", SDK: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := InspectManagedOwnerBinding(ctx, path, ids.New()); !errors.Is(err, ErrManagedOwnerLegacy) {
		t.Fatalf("legacy preflight: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy database changed during preflight")
	}
}

func TestManagedOwnerBindingReadsWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orva.db")
	ctx := t.Context()
	owner := ids.New()
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WriteDB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.BindManagedOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	walBefore, err := os.ReadFile(path + "-wal")
	if err != nil || len(walBefore) == 0 {
		t.Fatal("missing uncheckpointed binding")
	}
	if !errors.Is(RequireManagedOwnerBinding(ctx, path, ids.New()), ErrManagedOwnerMismatch) {
		t.Fatal("preflight ignored newer owner binding in WAL")
	}
	if err := RequireManagedOwnerBinding(ctx, path, owner); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only preflight checkpointed the database")
	}
	after, err = os.ReadFile(path + "-wal")
	if err != nil || !bytes.Equal(walBefore, after) {
		t.Fatal("read-only preflight changed the WAL")
	}
}
