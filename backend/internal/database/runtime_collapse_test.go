package database

import (
	"path/filepath"
	"testing"
)

// TestCollapseRuntimes verifies the legacy-runtime → generic-runtime migration:
// functions stored under versioned ids (node20/node22/node24, python312/313/314)
// are rewritten to `node` / `python`, and rows already on the generic ids are
// left untouched. Idempotent on a second pass.
func TestCollapseRuntimes(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cases := []struct {
		name, stored, want string
	}{
		{"legacy-node22", "node22", "node"},
		{"legacy-node24", "node24", "node"},
		{"legacy-node20", "node20", "node"},
		{"legacy-py313", "python313", "python"},
		{"legacy-py314", "python314", "python"},
		{"already-node", "node", "node"},
		{"already-python", "python", "python"},
	}

	// Seed rows directly with the (possibly legacy) runtime value — the DB layer
	// stores the string verbatim; validation lives at the handler/MCP layer.
	for _, c := range cases {
		if _, err := db.write.Exec(
			"INSERT INTO functions (id, name, runtime, entrypoint, status) VALUES (?, ?, ?, ?, ?)",
			c.name, c.name, c.stored, "handler.js", "created",
		); err != nil {
			t.Fatalf("seed %s: %v", c.name, err)
		}
	}

	// Run twice to prove idempotency.
	db.collapseRuntimes()
	db.collapseRuntimes()

	for _, c := range cases {
		var got string
		if err := db.read.QueryRow("SELECT runtime FROM functions WHERE id = ?", c.name).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: runtime = %q, want %q", c.name, got, c.want)
		}
	}
}
