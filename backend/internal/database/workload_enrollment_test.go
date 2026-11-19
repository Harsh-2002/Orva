package database

import (
	"errors"
	"maps"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestRecordManagedRuntimeGenerations(t *testing.T) {
	ctx := t.Context()
	db, err := New(filepath.Join(t.TempDir(), "orva.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assert := func(want map[string]string) {
		t.Helper()
		got, err := db.LegacyEnrollmentRuntimes(ctx)
		if err != nil || !maps.Equal(got, want) {
			t.Fatalf("selections = %v, %v; want %v", got, err, want)
		}
	}
	node, py := ids.New(), ids.New()
	for _, malformed := range []map[string]string{{"node": node, "ruby": py}, {"node": node, "python": "invalid"}} {
		if err := db.RecordManagedRuntimeGenerations(ctx, malformed); !errors.Is(err, ErrManagedOwnerInvalid) {
			t.Fatalf("invalid selection: %v", err)
		}
		assert(nil)
	}
	for _, selections := range []map[string]string{{"node": node}, {"node": ids.New(), "python": py}, {"node": node, "python": py}} {
		if err := db.RecordManagedRuntimeGenerations(ctx, selections); err != nil {
			t.Fatal(err)
		}
	}
	assert(map[string]string{"node": node, "python": py})
}
