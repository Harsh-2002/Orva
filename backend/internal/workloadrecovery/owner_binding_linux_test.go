//go:build linux

package workloadrecovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestBoundRestoreRejectsForeignArchiveBeforeReplacement(t *testing.T) {
	ctx := t.Context()
	owner, foreign := ids.New(), ids.New()
	build := func(id string) (string, []byte) {
		t.Helper()
		dir := t.TempDir()
		db := openDB(t, dir)
		must(t, db.BindManagedOwner(ctx, id))
		snapshot := filepath.Join(dir, "snapshot.db")
		must(t, backup.SnapshotDB(db.ReadDB(), snapshot))
		must(t, db.Close())
		var archive bytes.Buffer
		must(t, backup.ArchiveTo(&archive, dir, snapshot))
		return dir, archive.Bytes()
	}
	dir, rightArchive := build(owner)
	wrongDir, wrongArchive := build(foreign)
	host := Host{Quiesce: func(context.Context) error { return nil }, AdmissionClosed: func() bool { return true }}
	wrong, err := Open(wrongDir, host)
	must(t, err)
	defer wrong.Close()
	if _, err := wrong.RestoreBound(ctx, owner, bytes.NewReader(rightArchive)); !errors.Is(err, database.ErrManagedOwnerMismatch) {
		t.Fatalf("foreign destination: %v", err)
	}
	must(t, database.RequireManagedOwnerBinding(ctx, filepath.Join(wrongDir, "orva.db"), foreign))
	c, err := Open(dir, host)
	must(t, err)
	defer c.Close()
	if _, err := c.RestoreBound(ctx, owner, bytes.NewReader(wrongArchive)); !errors.Is(err, database.ErrManagedOwnerMismatch) {
		t.Fatalf("foreign archive: %v", err)
	}
	must(t, database.RequireManagedOwnerBinding(ctx, filepath.Join(dir, "orva.db"), owner))
	marker := filepath.Join(dir, backup.RecoveryMarkerName)
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejection created restore intent: %v", err)
	}
	// Rehearse a restore from an old operator workflow that lacked preflight.
	failedOp, err := c.Restore(ctx, bytes.NewReader(wrongArchive))
	must(t, err)
	replaced, err := c.RestoreBound(ctx, owner, bytes.NewReader(rightArchive))
	must(t, err)
	if failedOp == replaced {
		t.Fatal("replacement reused a completed restore operation")
	}
	noop := func(context.Context, *database.Database) error { return nil }
	db, result, err := c.StartBound(ctx, owner, noop, noop)
	must(t, err)
	must(t, db.Close())
	if result.Operation != replaced {
		t.Fatal("replacement was not recovered")
	}
}

func TestBoundStartupKeepsReadyMarkerOnMismatch(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	owner := ids.New()
	noop := func(context.Context, *database.Database) error { return nil }
	host := Host{Quiesce: func(context.Context) error { return nil }, AdmissionClosed: func() bool { return true }}
	c, err := Open(dir, host)
	must(t, err)
	defer c.Close()
	db, _, err := c.StartBound(ctx, owner, noop, noop)
	must(t, err)
	must(t, db.Close())
	op, err := c.PrepareManual(ctx)
	must(t, err)
	must(t, c.CompleteManual(ctx, op))
	marker := filepath.Join(dir, backup.RecoveryMarkerName)
	before, err := os.ReadFile(marker)
	must(t, err)
	path := filepath.Join(dir, "orva.db")
	dbBefore, err := os.ReadFile(path)
	must(t, err)
	no := func(context.Context, *database.Database) error {
		t.Fatal("mismatched DB reached startup callback")
		return ErrClosed
	}
	db, _, err = c.StartBound(ctx, ids.New(), no, no)
	if db != nil || !errors.Is(err, database.ErrManagedOwnerMismatch) {
		t.Fatalf("wrong owner startup: %v", err)
	}
	after, err := os.ReadFile(marker)
	must(t, err)
	dbAfter, err := os.ReadFile(path)
	must(t, err)
	if !bytes.Equal(before, after) || !bytes.Equal(dbBefore, dbAfter) {
		t.Fatal("mismatch changed the marker or DB")
	}
	db, restored, err := c.StartBound(ctx, owner, noop, noop)
	must(t, err)
	must(t, db.Close())
	if restored.Operation != op {
		t.Fatal("valid owner did not finish recovery")
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("valid restore left a marker: %v", err)
	}
}

func TestBoundStartupRefusesUnenrolledSourceBeforeDatabaseCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "functions", ids.New())
	must(t, os.MkdirAll(path, 0700))
	opaque := []byte("unenrolled source")
	must(t, os.WriteFile(filepath.Join(path, "handler.js"), opaque, 0600))
	c, err := Open(dir, Host{
		Quiesce: func(context.Context) error { return nil }, AdmissionClosed: func() bool { return true },
	})
	must(t, err)
	defer c.Close()
	notCalled := func(context.Context, *database.Database) error {
		t.Fatal("startup callback reached despite legacy source")
		return ErrClosed
	}
	db, _, err := c.StartBound(t.Context(), ids.New(), notCalled, notCalled)
	if db != nil || !errors.Is(err, database.ErrManagedOwnerLegacy) {
		t.Fatalf("unenrolled source: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "orva.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source preflight opened a writer: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(path, "handler.js"))
	must(t, err)
	if !bytes.Equal(saved, opaque) {
		t.Fatal("source preflight changed the legacy tree")
	}
}
