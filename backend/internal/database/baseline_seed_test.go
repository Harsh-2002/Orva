package database

import (
	"fmt"
	"testing"
)

func TestListBaselineSeedUsesRecentPerFunctionWindow(t *testing.T) {
	db := newTestDB(t)
	for _, id := range []string{"seed-a", "seed-b"} {
		if _, err := db.write.Exec(`INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, 'node', 'handler.js')`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(fn string, n int, status string, cold int, duration any) {
		t.Helper()
		_, err := db.write.Exec(`INSERT INTO executions (id, function_id, status, cold_start, duration_ms, started_at)
			VALUES (?, ?, ?, ?, ?, ?)`, fmt.Sprintf("%s-%03d", fn, n), fn, status, cold, duration,
			fmt.Sprintf("2026-01-01T00:00:%02dZ", n))
		if err != nil {
			t.Fatal(err)
		}
	}
	for n := range 20 {
		status := "error"
		if n == 0 || n == 16 || n == 18 {
			status = "success"
		}
		insert("seed-a", n, status, 0, n)
	}
	insert("seed-b", 1, "success", 0, 101)
	insert("seed-b", 2, "success", 1, 102)
	insert("seed-b", 3, "success", 0, nil)
	insert("seed-b", 4, "success", 0, 104)

	seed, err := db.ListBaselineSeed(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) != 4 || seed[0] != (WarmBaselineSeed{"seed-a", 18}) ||
		seed[1] != (WarmBaselineSeed{"seed-a", 16}) ||
		seed[2] != (WarmBaselineSeed{"seed-b", 104}) ||
		seed[3] != (WarmBaselineSeed{"seed-b", 101}) {
		t.Fatalf("unexpected recent warm baseline: %+v", seed)
	}

	// With a one-sample request the scan covers only ten recent rows. The
	// older success is intentionally not revived after a long failure streak.
	if _, err := db.write.Exec(`UPDATE executions SET status='error' WHERE function_id='seed-a' AND id != 'seed-a-000'`); err != nil {
		t.Fatal(err)
	}
	seed, err = db.ListBaselineSeed(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) != 1 || seed[0] != (WarmBaselineSeed{"seed-b", 104}) {
		t.Fatalf("ancient success entered the recent baseline: %+v", seed)
	}
}
