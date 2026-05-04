package database

import (
	"database/sql"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// CronSchedule is a persisted cron job that fires a function on a schedule.
// The scheduler goroutine consumes DueSchedules() and updates rows via
// UpdateAfterRun() once each invocation finishes.
type CronSchedule struct {
	ID         string     `json:"id"`
	FunctionID string     `json:"function_id"`
	CronExpr   string     `json:"cron_expr"`
	// Timezone is the IANA name (e.g. "Asia/Kolkata", "America/Los_Angeles",
	// "UTC") that the cron expression is interpreted against. A row with
	// CronExpr "0 9 * * *" and Timezone "Asia/Kolkata" fires at 9 AM IST,
	// regardless of the orvad process timezone. Defaults to "UTC".
	Timezone   string     `json:"timezone"`
	Enabled    bool       `json:"enabled"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
	LastStatus string     `json:"last_status,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
	Payload    string     `json:"payload"` // JSON string sent as the invoke body
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// NewCronID returns a fresh UUIDv7. Replaces the legacy cron_<hex> form.
func NewCronID() string { return ids.New() }

func (db *Database) InsertCronSchedule(s *CronSchedule) error {
	if s.ID == "" {
		s.ID = NewCronID()
	}
	if s.Payload == "" {
		s.Payload = "{}"
	}
	if s.Timezone == "" {
		s.Timezone = "UTC"
	}
	now := time.Now().UTC()
	s.CreatedAt = now
	s.UpdatedAt = now
	_, err := db.write.Exec(`
		INSERT INTO cron_schedules
			(id, function_id, cron_expr, timezone, enabled, last_run_at, next_run_at, last_status, last_error, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.FunctionID, s.CronExpr, s.Timezone, boolToInt(s.Enabled),
		nullTime(s.LastRunAt), nullTime(s.NextRunAt),
		nullString(s.LastStatus), nullString(s.LastError),
		s.Payload, s.CreatedAt, s.UpdatedAt,
	)
	return err
}

func (db *Database) GetCronSchedule(id string) (*CronSchedule, error) {
	var s CronSchedule
	var enabled int
	var last, next sql.NullTime
	var lastStatus, lastErr sql.NullString
	err := db.read.QueryRow(`
		SELECT id, function_id, cron_expr, timezone, enabled, last_run_at, next_run_at, last_status, last_error, payload, created_at, updated_at
		FROM cron_schedules WHERE id = ?`, id,
	).Scan(&s.ID, &s.FunctionID, &s.CronExpr, &s.Timezone, &enabled, &last, &next, &lastStatus, &lastErr, &s.Payload, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	s.Enabled = enabled == 1
	if last.Valid {
		t := last.Time
		s.LastRunAt = &t
	}
	if next.Valid {
		t := next.Time
		s.NextRunAt = &t
	}
	s.LastStatus = lastStatus.String
	s.LastError = lastErr.String
	return &s, nil
}

// ListCronSchedulesForFunction returns every schedule attached to a single
// function, newest first.
func (db *Database) ListCronSchedulesForFunction(functionID string) ([]*CronSchedule, error) {
	rows, err := db.read.Query(`
		SELECT id, function_id, cron_expr, timezone, enabled, last_run_at, next_run_at, last_status, last_error, payload, created_at, updated_at
		FROM cron_schedules WHERE function_id = ? ORDER BY created_at DESC`, functionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCronRows(rows)
}

// ListAllCronSchedules returns every schedule across the system. Used by
// the dashboard.
func (db *Database) ListAllCronSchedules() ([]*CronSchedule, error) {
	rows, err := db.read.Query(`
		SELECT id, function_id, cron_expr, timezone, enabled, last_run_at, next_run_at, last_status, last_error, payload, created_at, updated_at
		FROM cron_schedules ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCronRows(rows)
}

// CronScheduleWithFunction is a CronSchedule joined with the function's
// friendly name, used by the dashboard listing so the UI doesn't need a
// second roundtrip per row.
type CronScheduleWithFunction struct {
	*CronSchedule
	FunctionName string `json:"function_name"`
}

// ListAllCronSchedulesWithFunction joins cron_schedules with functions so
// the dashboard can render rows with the friendly name without a second
// query per row.
func (db *Database) ListAllCronSchedulesWithFunction() ([]*CronScheduleWithFunction, error) {
	rows, err := db.read.Query(`
		SELECT c.id, c.function_id, c.cron_expr, c.timezone, c.enabled, c.last_run_at, c.next_run_at, c.last_status, c.last_error, c.payload, c.created_at, c.updated_at,
		       COALESCE(f.name, '')
		FROM cron_schedules c
		LEFT JOIN functions f ON f.id = c.function_id
		ORDER BY c.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*CronScheduleWithFunction
	for rows.Next() {
		var s CronSchedule
		var enabled int
		var last, next sql.NullTime
		var lastStatus, lastErr sql.NullString
		var fnName string
		if err := rows.Scan(&s.ID, &s.FunctionID, &s.CronExpr, &s.Timezone, &enabled, &last, &next, &lastStatus, &lastErr, &s.Payload, &s.CreatedAt, &s.UpdatedAt, &fnName); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		if last.Valid {
			t := last.Time
			s.LastRunAt = &t
		}
		if next.Valid {
			t := next.Time
			s.NextRunAt = &t
		}
		s.LastStatus = lastStatus.String
		s.LastError = lastErr.String
		out = append(out, &CronScheduleWithFunction{CronSchedule: &s, FunctionName: fnName})
	}
	return out, rows.Err()
}

// DueCronSchedules returns enabled schedules whose next_run_at is <= now.
// Limits to a batch so a backlog can't starve the tick loop.
func (db *Database) DueCronSchedules(now time.Time, limit int) ([]*CronSchedule, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.read.Query(`
		SELECT id, function_id, cron_expr, timezone, enabled, last_run_at, next_run_at, last_status, last_error, payload, created_at, updated_at
		FROM cron_schedules
		WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC
		LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCronRows(rows)
}

// UpdateCronSchedule applies the editable fields. cron_expr / timezone /
// enabled / payload pass through; next_run_at must be supplied so the
// scheduler doesn't drift after expr or TZ changes.
func (db *Database) UpdateCronSchedule(s *CronSchedule) error {
	if s.Timezone == "" {
		s.Timezone = "UTC"
	}
	s.UpdatedAt = time.Now().UTC()
	_, err := db.write.Exec(`
		UPDATE cron_schedules
		SET cron_expr = ?, timezone = ?, enabled = ?, payload = ?, next_run_at = ?, updated_at = ?
		WHERE id = ?`,
		s.CronExpr, s.Timezone, boolToInt(s.Enabled), s.Payload, nullTime(s.NextRunAt), s.UpdatedAt, s.ID)
	return err
}

// UpdateCronAfterRun stamps the row with the result of a single fire and
// the next scheduled time. Called by the scheduler goroutine.
func (db *Database) UpdateCronAfterRun(id string, ranAt, nextAt time.Time, status, errMsg string) error {
	_, err := db.write.Exec(`
		UPDATE cron_schedules
		SET last_run_at = ?, next_run_at = ?, last_status = ?, last_error = ?, updated_at = ?
		WHERE id = ?`,
		ranAt.UTC(), nextAt.UTC(), status, errMsg, time.Now().UTC(), id)
	return err
}

func (db *Database) DeleteCronSchedule(id string) error {
	_, err := db.write.Exec(`DELETE FROM cron_schedules WHERE id = ?`, id)
	return err
}

// ── helpers ────────────────────────────────────────────────────────

func scanCronRows(rows *sql.Rows) ([]*CronSchedule, error) {
	var out []*CronSchedule
	for rows.Next() {
		var s CronSchedule
		var enabled int
		var last, next sql.NullTime
		var lastStatus, lastErr sql.NullString
		if err := rows.Scan(&s.ID, &s.FunctionID, &s.CronExpr, &s.Timezone, &enabled, &last, &next, &lastStatus, &lastErr, &s.Payload, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		if last.Valid {
			t := last.Time
			s.LastRunAt = &t
		}
		if next.Valid {
			t := next.Time
			s.NextRunAt = &t
		}
		s.LastStatus = lastStatus.String
		s.LastError = lastErr.String
		out = append(out, &s)
	}
	return out, rows.Err()
}

// boolToInt is shared with blocklist.go (defined there).

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
