package database

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ActivityRow is a single line in the live activity feed. One row per
// inbound HTTP request, MCP tool call, webhook delivery attempt, or
// internal SDK call.
type ActivityRow struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"` // unix millis
	Source     string `json:"source"`
	ActorType  string `json:"actor_type"`
	ActorID    string `json:"actor_id"`
	ActorLabel string `json:"actor_label"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Summary    string `json:"summary"`
	RequestID  string `json:"request_id"`
	Metadata   string `json:"metadata"`
}

// InsertActivity queues a single row through the batched async writer.
// Never blocks the caller — the activity log is on the request hot path
// and must not slow down the request itself.
func (db *Database) InsertActivity(row ActivityRow) {
	if row.TS == 0 {
		row.TS = time.Now().UnixMilli()
	}
	db.AsyncExec(`
		INSERT INTO activity_log (
			ts, source, actor_type, actor_id, actor_label,
			method, path, status, duration_ms, summary, request_id, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.TS, row.Source, row.ActorType, row.ActorID, row.ActorLabel,
		row.Method, row.Path, row.Status, row.DurationMS, row.Summary,
		row.RequestID, row.Metadata,
	)
}

// ActivityFilter narrows ListActivity. Zero values mean "no filter".
type ActivityFilter struct {
	Source    string // exact match
	ActorID   string // exact match
	SinceMS   int64  // ts >= since
	UntilMS   int64  // ts < until
	StatusMin int    // status >= n  (use 400 for "errors only")
	Search    string // LIKE on path / summary / actor_label
	Limit     int    // default 200, max 1000
	Cursor    int64  // ts threshold (descending paginate; ts < cursor)
}

// ListActivity returns rows newest-first. The next-page cursor is the
// last row's ts; pass it back as Cursor on the next call.
func (db *Database) ListActivity(f ActivityFilter) (rows []ActivityRow, nextCursor int64, err error) {
	if f.Limit <= 0 {
		f.Limit = 200
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}

	var conds []string
	var args []any
	if f.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, f.Source)
	}
	if f.ActorID != "" {
		conds = append(conds, "actor_id = ?")
		args = append(args, f.ActorID)
	}
	if f.SinceMS > 0 {
		conds = append(conds, "ts >= ?")
		args = append(args, f.SinceMS)
	}
	if f.UntilMS > 0 {
		conds = append(conds, "ts < ?")
		args = append(args, f.UntilMS)
	}
	if f.StatusMin > 0 {
		conds = append(conds, "status >= ?")
		args = append(args, f.StatusMin)
	}
	if f.Search != "" {
		needle := "%" + strings.ToLower(f.Search) + "%"
		conds = append(conds, "(LOWER(path) LIKE ? OR LOWER(summary) LIKE ? OR LOWER(actor_label) LIKE ?)")
		args = append(args, needle, needle, needle)
	}
	if f.Cursor > 0 {
		conds = append(conds, "ts < ?")
		args = append(args, f.Cursor)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	q := fmt.Sprintf(`
		SELECT id, ts, source, actor_type, actor_id, actor_label,
		       method, path, status, duration_ms, summary, request_id, metadata
		FROM activity_log
		%s
		ORDER BY ts DESC, id DESC
		LIMIT %d`, where, f.Limit)

	res, err := db.read.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer res.Close()

	for res.Next() {
		var r ActivityRow
		if err := res.Scan(
			&r.ID, &r.TS, &r.Source, &r.ActorType, &r.ActorID, &r.ActorLabel,
			&r.Method, &r.Path, &r.Status, &r.DurationMS, &r.Summary,
			&r.RequestID, &r.Metadata,
		); err != nil {
			return nil, 0, err
		}
		rows = append(rows, r)
	}
	if err := res.Err(); err != nil {
		return nil, 0, err
	}
	if len(rows) == f.Limit {
		nextCursor = rows[len(rows)-1].TS
	}
	return rows, nextCursor, nil
}

// SweepActivity drops rows older than the configured retention window
// AND/OR truncates to the configured max-row cap, whichever bites first.
// Reads its knobs from system_config so operators can tune live.
func (db *Database) SweepActivity() (deleted int64, err error) {
	days := readSysConfigInt(db, "activity_retention_days", 7)
	maxRows := readSysConfigInt(db, "activity_retention_max_rows", 50000)

	if days > 0 {
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
		res, err := db.write.Exec(`DELETE FROM activity_log WHERE ts < ?`, cutoff)
		if err != nil {
			return deleted, err
		}
		n, _ := res.RowsAffected()
		deleted += n
	}

	if maxRows > 0 {
		res, err := db.write.Exec(`
			DELETE FROM activity_log
			WHERE id IN (
				SELECT id FROM activity_log
				ORDER BY ts DESC, id DESC
				LIMIT -1 OFFSET ?
			)`, maxRows)
		if err != nil {
			return deleted, err
		}
		n, _ := res.RowsAffected()
		deleted += n
	}

	return deleted, nil
}

// readSysConfigInt is a tiny lookup that falls back to the default on
// any error or non-numeric value. Activity sweep tolerates a missing
// row — it just uses the default.
func readSysConfigInt(db *Database, key string, fallback int) int {
	var s string
	err := db.read.QueryRow(`SELECT value FROM system_config WHERE key = ?`, key).Scan(&s)
	if err != nil {
		if err != sql.ErrNoRows {
			// log+swallow — config row exists in normal operation
		}
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return n
}
