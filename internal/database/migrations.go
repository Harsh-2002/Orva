package database

import (
	"log/slog"
	"strings"
)

func (db *Database) Migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS functions (
    id            TEXT PRIMARY KEY,
    name          TEXT UNIQUE NOT NULL,
    runtime       TEXT NOT NULL,
    entrypoint    TEXT NOT NULL DEFAULT 'handler.js',
    image         TEXT,
    timeout_ms    INTEGER NOT NULL DEFAULT 30000,
    memory_mb     INTEGER NOT NULL DEFAULT 128,
    cpus          REAL NOT NULL DEFAULT 0.5,
    env_vars      TEXT NOT NULL DEFAULT '{}',
    network_mode  TEXT NOT NULL DEFAULT 'none',
    version       INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'created',
    code_hash     TEXT,
    image_size    INTEGER DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_functions_name ON functions(name);
CREATE INDEX IF NOT EXISTS idx_functions_status ON functions(status);
CREATE INDEX IF NOT EXISTS idx_functions_runtime ON functions(runtime);

CREATE TABLE IF NOT EXISTS executions (
    id            TEXT PRIMARY KEY,
    function_id   TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'running',
    cold_start    INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER,
    status_code   INTEGER,
    request_size  INTEGER,
    response_size INTEGER,
    container_id  TEXT,
    error_message TEXT,
    started_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at   DATETIME,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_executions_function ON executions(function_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_executions_status ON executions(status);
CREATE INDEX IF NOT EXISTS idx_executions_started ON executions(started_at DESC);

CREATE TABLE IF NOT EXISTS execution_logs (
    execution_id  TEXT PRIMARY KEY,
    stdout        TEXT DEFAULT '',
    stderr        TEXT DEFAULT '',
    FOREIGN KEY (execution_id) REFERENCES executions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS pool_config (
    function_id        TEXT PRIMARY KEY,
    min_warm           INTEGER NOT NULL DEFAULT 1,
    max_warm           INTEGER NOT NULL DEFAULT 50,       -- Knative-style soft cap; autoscaler respects mem/cpu budget
    idle_ttl_s         INTEGER NOT NULL DEFAULT 600,
    max_use_count      INTEGER NOT NULL DEFAULT 1000,
    target_concurrency INTEGER NOT NULL DEFAULT 10,       -- Knative target concurrency per worker
    scale_to_zero      INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login    DATETIME
);

CREATE TABLE IF NOT EXISTS sessions (
    token         TEXT PRIMARY KEY,
    user_id       INTEGER NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at    DATETIME NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS api_keys (
    id            TEXT PRIMARY KEY,
    key_hash      TEXT UNIQUE NOT NULL,
    name          TEXT NOT NULL,
    permissions   TEXT NOT NULL DEFAULT '["invoke","read"]',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at  DATETIME,
    expires_at    DATETIME
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

CREATE TABLE IF NOT EXISTS function_secrets (
    function_id       TEXT NOT NULL,
    key               TEXT NOT NULL,
    value_encrypted   TEXT NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (function_id, key),
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_secrets_fn ON function_secrets(function_id);

CREATE TABLE IF NOT EXISTS deployments (
    id             TEXT PRIMARY KEY,              -- dep_<nanoid>
    function_id    TEXT NOT NULL,
    version        INTEGER NOT NULL,
    status         TEXT NOT NULL,                 -- queued|building|succeeded|failed
    phase          TEXT,                          -- extract|deps|validate|install|done
    error_message  TEXT,
    submitted_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at     DATETIME,
    finished_at    DATETIME,
    duration_ms    INTEGER,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_deployments_fn ON deployments(function_id, submitted_at DESC);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON deployments(status);

CREATE TABLE IF NOT EXISTS build_logs (
    deployment_id TEXT NOT NULL,
    seq           INTEGER NOT NULL,
    stream        TEXT NOT NULL,                  -- stdout|stderr|phase
    line          TEXT NOT NULL,
    ts            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (deployment_id, seq),
    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS routes (
    path         TEXT PRIMARY KEY,
    function_id  TEXT NOT NULL,
    methods      TEXT NOT NULL DEFAULT '*',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_routes_fn ON routes(function_id);

CREATE TABLE IF NOT EXISTS system_config (
    key           TEXT PRIMARY KEY,
    value         TEXT NOT NULL,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Seed system config (ignore if already exists)
INSERT OR IGNORE INTO system_config (key, value) VALUES
    ('max_total_containers', '100'),
    ('default_timeout_ms', '30000'),
    ('default_memory_mb', '128'),
    ('max_code_size_bytes', '52428800'),
    ('max_request_body_bytes', '6291456'),
    ('log_retention_days', '7'),
    ('reap_interval_seconds', '30'),
    ('replenish_interval_seconds', '5'),
    ('versions_to_keep', '5'),
    ('gc_interval_seconds', '300'),
    ('min_free_disk_mb', '500');

PRAGMA foreign_keys = ON;
`
	if _, err := db.write.Exec(schema); err != nil {
		return err
	}

	// Additive columns for the smart autoscaler. Idempotent — SQLite errors
	// if the column already exists, which we ignore.
	for _, stmt := range []string{
		"ALTER TABLE pool_config ADD COLUMN target_concurrency INTEGER NOT NULL DEFAULT 10",
		"ALTER TABLE pool_config ADD COLUMN scale_to_zero INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE api_keys ADD COLUMN key_prefix TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN code_hash TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN source TEXT NOT NULL DEFAULT 'deploy'",
		"ALTER TABLE deployments ADD COLUMN parent_deployment_id TEXT",
	} {
		if _, err := db.write.Exec(stmt); err != nil {
			// "duplicate column name" is expected on boot after the first.
			if !strings.Contains(err.Error(), "duplicate column") {
				slog.Warn("schema alter failed", "stmt", stmt, "err", err)
			}
		}
	}

	// Runtime refresh: bump EOL runtimes to the nearest supported version.
	// This is a one-shot, idempotent update — on subsequent boots there
	// are no rows to migrate.
	runtimeMigrations := []struct {
		from, to string
	}{
		{"node20", "node22"},
		{"python312", "python313"},
	}
	for _, m := range runtimeMigrations {
		res, err := db.write.Exec(
			"UPDATE functions SET runtime = ? WHERE runtime = ?",
			m.to, m.from,
		)
		if err != nil {
			slog.Warn("runtime migration failed", "from", m.from, "to", m.to, "err", err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			slog.Info("runtime migrated", "from", m.from, "to", m.to, "functions", n)
		}
	}

	// Kick off the batched async writer now that the schema exists. Safe
	// to call multiple times — Migrate is idempotent and we only start the
	// writer if it's nil. Tests that don't call Migrate continue to use
	// the goroutine-per-call fallback in Async().
	if db.writer == nil {
		db.writer = newAsyncWriter(db)
		db.writer.start()
	}
	return nil
}
