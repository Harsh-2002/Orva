package database

import (
	"testing"
	"time"
)

// TestActivityCursorDoesNotSkipRowsSharingATimestamp — the list is ordered
// (ts DESC, id DESC) but the cursor carried only ts and resumed with a
// strict `ts <`, so every row sharing the last row's millisecond was
// skipped. A page boundary landing inside a burst silently dropped activity
// -- and a burst is exactly when rows share a timestamp.
func TestActivityCursorDoesNotSkipRowsSharingATimestamp(t *testing.T) {
	db := newTestDB(t)

	// Six rows in the SAME millisecond, so any page boundary lands inside a
	// tie and only a two-key cursor can resume correctly.
	const ts = int64(1770000000000)
	const total = 6
	for i := 0; i < total; i++ {
		if _, err := db.write.Exec(`
			INSERT INTO activity_log (ts, source, actor_type, actor_id, actor_label, summary)
			VALUES (?, 'test', 'anon', '', '', ?)`, ts, "row"); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[int64]bool{}
	var cursor, cursorID int64
	for page := 0; page < 10; page++ {
		rows, next, nextID, err := db.ListActivity(ActivityFilter{
			Limit: 2, Cursor: cursor, CursorID: cursorID,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatalf("row %d returned twice across pages", r.ID)
			}
			seen[r.ID] = true
		}
		if next == 0 {
			break
		}
		cursor, cursorID = next, nextID
	}

	if len(seen) != total {
		t.Fatalf("paged through %d of %d rows; the rest were skipped at a "+
			"page boundary inside a shared timestamp", len(seen), total)
	}
}

// TestActivityCursorStillWorksWithoutTieBreak — an older client that sends
// only `cursor` must keep paginating rather than erroring.
func TestActivityCursorStillWorksWithoutTieBreak(t *testing.T) {
	db := newTestDB(t)
	base := time.Now().UnixMilli()
	for i := 0; i < 4; i++ {
		if _, err := db.write.Exec(`
			INSERT INTO activity_log (ts, source, actor_type, actor_id, actor_label, summary)
			VALUES (?, 'test', 'anon', '', '', 'row')`, base-int64(i)*1000); err != nil {
			t.Fatal(err)
		}
	}
	rows, next, _, err := db.ListActivity(ActivityFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || next == 0 {
		t.Fatalf("first page: rows=%d next=%d", len(rows), next)
	}
	// No cursor_id, as an older client would send.
	rows2, _, _, err := db.ListActivity(ActivityFilter{Limit: 2, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) == 0 {
		t.Error("ts-only cursor stopped paginating")
	}
}
