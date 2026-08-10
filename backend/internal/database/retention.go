package database

import (
	"context"
	"log/slog"
	"time"
)

// Execution history retention.
//
// Every invocation writes an `executions` row plus its logs and, when replay
// capture is on, a captured request. Nothing ever removed them: PurgeOldExecutions
// existed but was called from nowhere, so an instance's database grew without
// bound for the life of the deployment while the docs claimed old logs were
// "pruned on startup".
//
// Retention is a DB-backed setting rather than an environment variable. It is
// operational state an operator may want to change on a running instance —
// the same category as the DNS servers and the egress blocklist — and making
// it an env var would mean a restart to change it, and one more knob on a
// surface we are deliberately keeping small.
const (
	// RetentionSettingKey is the system_config key holding the retention
	// window in days. 0 disables purging entirely (keep everything).
	RetentionSettingKey = "execution_retention_days"

	// DefaultRetentionDays is deliberately generous. This deletes user-visible
	// diagnostic history, so the default errs towards keeping too much rather
	// than surprising an operator who was relying on it being there.
	DefaultRetentionDays = 30

	retentionInterval = 24 * time.Hour
)

// StartRetention purges expired execution history once at boot and then daily
// until ctx is cancelled. The setting is re-read every tick, so changing it on
// a running instance takes effect without a restart.
//
// Failures are logged and retried on the next tick: a purge that cannot run is
// a disk-usage problem, never a reason to stop serving.
func (db *Database) StartRetention(ctx context.Context) {
	go func() {
		db.purgeOnce()
		t := time.NewTicker(retentionInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				db.purgeOnce()
			}
		}
	}()
}

func (db *Database) purgeOnce() {
	days := db.GetSystemConfigInt(RetentionSettingKey, DefaultRetentionDays)
	if days <= 0 {
		// Explicitly disabled. Say so once per tick at debug level rather than
		// silently doing nothing, so "why is my disk full" has an answer.
		slog.Debug("execution retention disabled", "setting", RetentionSettingKey)
		return
	}

	var before, after int64
	_ = db.read.QueryRow("SELECT COUNT(*) FROM executions").Scan(&before)

	if err := db.PurgeOldExecutions(days); err != nil {
		slog.Warn("execution retention: purge failed", "err", err, "retention_days", days)
		return
	}

	if err := db.read.QueryRow("SELECT COUNT(*) FROM executions").Scan(&after); err != nil {
		return
	}
	if removed := before - after; removed > 0 {
		slog.Info("execution retention: purged old history",
			"removed", removed, "retention_days", days, "remaining", after)
	}
}
