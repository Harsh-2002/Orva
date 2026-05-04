package database

import (
	"fmt"
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
    memory_mb     INTEGER NOT NULL DEFAULT 64,
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

-- Scheduled invocations. The scheduler goroutine polls every 30s for
-- rows where enabled=1 AND next_run_at <= NOW(), fires the function
-- via pool.Manager.Acquire(), and updates last_run_at + next_run_at.
-- payload is a JSON blob delivered as the invoke body.
CREATE TABLE IF NOT EXISTS cron_schedules (
    id            TEXT PRIMARY KEY,                      -- cron_<nanoid>
    function_id   TEXT NOT NULL,
    cron_expr     TEXT NOT NULL,                         -- 5-field "M H DOM MON DOW"
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_run_at   DATETIME,
    next_run_at   DATETIME,                              -- precomputed; refreshed on schedule + after each run
    last_status   TEXT,                                  -- 'ok' | 'failed' | NULL
    last_error    TEXT,                                  -- short error string when last_status='failed'
    payload       TEXT NOT NULL DEFAULT '{}',            -- JSON delivered as the invoke body
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_cron_due ON cron_schedules(enabled, next_run_at);
CREATE INDEX IF NOT EXISTS idx_cron_fn  ON cron_schedules(function_id);

-- Per-function key/value store. Single-host on SQLite, namespaced by
-- function_id so two functions can't see each other's keys. The
-- scheduler's TTL sweep deletes rows where expires_at <= NOW().
CREATE TABLE IF NOT EXISTS kv_store (
    function_id  TEXT NOT NULL,
    key          TEXT NOT NULL,
    value        BLOB NOT NULL,
    expires_at   DATETIME,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (function_id, key),
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_kv_expires ON kv_store(expires_at) WHERE expires_at IS NOT NULL;

-- Background job queue (Phase 5). status transitions:
--   pending → running → succeeded | failed (terminal)
-- A failed run with attempts < max_attempts goes back to pending with
-- scheduled_at advanced by exponential backoff.
CREATE TABLE IF NOT EXISTS jobs (
    id            TEXT PRIMARY KEY,
    function_id   TEXT NOT NULL,
    payload       BLOB NOT NULL,
    status        TEXT NOT NULL,
    scheduled_at  DATETIME NOT NULL,
    started_at    DATETIME,
    finished_at   DATETIME,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_jobs_due    ON jobs(status, scheduled_at);
CREATE INDEX IF NOT EXISTS idx_jobs_fn     ON jobs(function_id, created_at DESC);

-- Operator-managed webhook subscriptions for system events. One row
-- per "send these events to this URL". The events column is a JSON
-- array of event names; '*' matches all. The HMAC secret is the key
-- the receiver verifies signatures with.
CREATE TABLE IF NOT EXISTS event_subscriptions (
    id               TEXT PRIMARY KEY,            -- sub_<nanoid>
    name             TEXT NOT NULL,
    url              TEXT NOT NULL,
    secret           TEXT NOT NULL,                -- 32-byte hex, generated server-side
    events           TEXT NOT NULL DEFAULT '["*"]',
    enabled          INTEGER NOT NULL DEFAULT 1,
    last_delivery_at DATETIME,
    last_status      TEXT,                         -- 'ok' | 'failed' | NULL
    last_error       TEXT,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_event_subs_enabled ON event_subscriptions(enabled);

-- Per-(subscription × event) delivery queue. Status state machine
-- mirrors the jobs table: pending → running → succeeded | failed
-- (terminal). Failed runs with attempts < max_attempts go back to
-- pending with exponential backoff scheduled_at.
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id               TEXT PRIMARY KEY,            -- whd_<nanoid>
    subscription_id  TEXT NOT NULL,
    event_name       TEXT NOT NULL,
    payload          BLOB NOT NULL,
    status           TEXT NOT NULL,                -- pending|running|succeeded|failed
    scheduled_at     DATETIME NOT NULL,
    started_at       DATETIME,
    finished_at      DATETIME,
    attempts         INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 5,
    response_status  INTEGER,
    last_error       TEXT,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (subscription_id) REFERENCES event_subscriptions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_whd_due ON webhook_deliveries(status, scheduled_at);
CREATE INDEX IF NOT EXISTS idx_whd_sub ON webhook_deliveries(subscription_id, created_at DESC);

CREATE TABLE IF NOT EXISTS system_config (
    key           TEXT PRIMARY KEY,
    value         TEXT NOT NULL,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Global egress blocklist. Applies to every function with
-- network_mode='egress' regardless of who created the function. Three
-- categories: 'default' rules ship enabled (cloud metadata, link-local),
-- 'suggested' ship disabled (RFC1918 ranges — operator opts in), and
-- 'custom' are operator-entered. UNIQUE(kind, value) protects toggle
-- state across reboots: re-seeds always use INSERT OR IGNORE so the
-- operator's enabled/disabled choices survive.
CREATE TABLE IF NOT EXISTS egress_blocklist (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT NOT NULL,        -- 'default' | 'suggested' | 'custom'
    rule_type   TEXT NOT NULL,        -- 'cidr' | 'hostname' | 'wildcard'
    value       TEXT NOT NULL,
    label       TEXT,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(kind, value)
);

CREATE INDEX IF NOT EXISTS idx_egress_blocklist_kind ON egress_blocklist(kind);
CREATE INDEX IF NOT EXISTS idx_egress_blocklist_enabled ON egress_blocklist(enabled);

-- Single live activity log: one row per inbound HTTP request, MCP tool
-- call, webhook delivery attempt, or internal SDK call. Rendered live in
-- the dashboard's Activity page; swept on a TTL.
CREATE TABLE IF NOT EXISTS activity_log (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           INTEGER NOT NULL,                      -- unix millis
    source       TEXT NOT NULL,                         -- web|api|mcp|sdk|webhook|cron|internal
    actor_type   TEXT NOT NULL DEFAULT '',              -- session|api_key|internal_token|webhook|system|anon
    actor_id     TEXT NOT NULL DEFAULT '',
    actor_label  TEXT NOT NULL DEFAULT '',
    method       TEXT NOT NULL DEFAULT '',              -- HTTP method, "tool", or "deliver"
    path         TEXT NOT NULL DEFAULT '',
    status       INTEGER NOT NULL DEFAULT 0,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    summary      TEXT NOT NULL DEFAULT '',
    request_id   TEXT NOT NULL DEFAULT '',
    metadata     TEXT NOT NULL DEFAULT ''               -- optional JSON blob
);

CREATE INDEX IF NOT EXISTS idx_activity_ts ON activity_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_activity_source_ts ON activity_log(source, ts DESC);
CREATE INDEX IF NOT EXISTS idx_activity_actor ON activity_log(actor_id, ts DESC);

-- Captured request envelope for replay (v0.4 A3). One row per execution
-- that ran while replay_capture_enabled=1. Headers are JSON-serialised
-- AFTER redaction (Authorization, Cookie, Orva API key, internal token,
-- proxy-authorization). truncated=1 means the body exceeded
-- replay_capture_max_bytes and was cut at the cap; replay handler
-- refuses to re-run those (would corrupt at-rest state).
--
-- No FK on execution_id by design (v0.4 A3 fix): the proxy queues this
-- row at request-receive time, but the parent executions row only lands
-- on the async batch *after* the function returns. Because batches flush
-- on a 50ms timer, the two rows often commit in different transactions —
-- a hard FK would fail at commit ("FOREIGN KEY constraint failed (787)")
-- and roll the whole batch back, losing every other row riding the
-- transaction. Cascade-on-delete semantics are preserved by the explicit
-- cleanup paths in PurgeOldExecutions and DeleteExecution.
CREATE TABLE IF NOT EXISTS execution_requests (
    execution_id  TEXT PRIMARY KEY,
    method        TEXT NOT NULL,
    path          TEXT NOT NULL,
    headers_json  TEXT NOT NULL,
    body          BLOB,
    truncated     INTEGER NOT NULL DEFAULT 0,
    captured_at   INTEGER NOT NULL
);

-- Saved request fixtures (v0.4 B3). Per-function "Postman-style" presets
-- reused from the editor's Test pane and the test_function_with_fixture
-- MCP tool. Headers are stored as a JSON object; body is opaque bytes.
-- (function_id, name) is the natural composite key — UNIQUE prevents
-- duplicate-name races and lets callers upsert by the human-friendly
-- name without first looking up the random id.
CREATE TABLE IF NOT EXISTS fixtures (
    id            TEXT PRIMARY KEY,
    function_id   TEXT NOT NULL,
    name          TEXT NOT NULL,
    method        TEXT NOT NULL,
    path          TEXT NOT NULL,
    headers_json  TEXT NOT NULL DEFAULT '{}',
    body          BLOB,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE(function_id, name),
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_fixtures_fn ON fixtures(function_id, name);

-- Inbound webhook triggers (v0.4 C2a). One row per "POST /webhook/<id>
-- with this signature scheme fires this function". Auth is the HMAC
-- signature; the path is intentionally outside /api/v1 so external
-- callers don't need an Orva API key. signature_format is a string enum
-- ("hmac_sha256_hex" | "hmac_sha256_base64" | "github" | "stripe" |
-- "slack") — the trigger handler picks the verifier to run.
CREATE TABLE IF NOT EXISTS inbound_webhooks (
    id                TEXT PRIMARY KEY,                     -- iwh_<hex>
    function_id       TEXT NOT NULL,
    name              TEXT NOT NULL,
    secret            TEXT NOT NULL,                        -- 32-byte hex
    signature_header  TEXT NOT NULL,
    signature_format  TEXT NOT NULL DEFAULT 'hmac_sha256_hex',
    active            INTEGER NOT NULL DEFAULT 1,
    created_at        INTEGER NOT NULL,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_inbound_webhooks_fn ON inbound_webhooks(function_id);

-- Seed system config (ignore if already exists)
INSERT OR IGNORE INTO system_config (key, value) VALUES
    ('max_total_containers', '100'),
    ('default_timeout_ms', '30000'),
    ('default_memory_mb', '64'),
    ('max_code_size_bytes', '52428800'),
    ('max_request_body_bytes', '6291456'),
    ('log_retention_days', '7'),
    ('reap_interval_seconds', '30'),
    ('replenish_interval_seconds', '5'),
    ('versions_to_keep', '5'),
    ('gc_interval_seconds', '300'),
    ('min_free_disk_mb', '500'),
    -- Global DNS for sandboxed functions with network_mode=egress.
    -- Comma-separated list of resolver IPs (v4 or v6). Empty = use the
    -- host's /etc/resolv.conf. Operator-editable from the Firewall page.
    ('dns_servers', '1.1.1.1,8.8.8.8'),
    ('dns_search', ''),
    -- Operator-managed host→IP overrides for sandboxes with
    -- network_mode=egress. Format: one record per line, "host ip"
    -- (matches /etc/hosts format). Empty by default.
    ('dns_records', ''),
    -- Activity log retention. The Activity page is observability,
    -- not audit; rotate aggressively to keep the table small.
    ('activity_retention_days', '7'),
    ('activity_retention_max_rows', '50000'),
    -- v0.4 A3: per-invocation Request Capture for the dashboard's
    -- Replay button. Disabled by setting replay_capture_enabled=0 if
    -- the operator wants to skip the SQLite write entirely. The cap
    -- mirrors the API max_request_body_bytes default; bodies larger
    -- than the cap are truncated and replay is refused for them.
    ('replay_capture_enabled', '1'),
    ('replay_capture_max_bytes', '1048576'),
    -- v0.4 C1: streaming responses. When streaming_enabled=0 the
    -- adapter falls back to buffering generator yields into a single
    -- response frame so existing handlers keep working byte-for-byte;
    -- operators can flip this off if their reverse-proxy / LB doesn't
    -- handle chunked responses well. stream_keepalive_seconds is the
    -- adapter-side heartbeat interval — if no chunk arrived within
    -- that window an empty chunk is emitted to stop intermediaries
    -- closing an idle connection. stream_max_seconds is the proxy-side
    -- wall-clock cap; runaway generators are torn down at this fence
    -- and the worker is reaped.
    ('streaming_enabled', '1'),
    ('stream_keepalive_seconds', '15'),
    ('stream_max_seconds', '300');

-- Seed default rules (shipped enabled). Kept deliberately minimal:
-- only entries that are universally dangerous to expose to user code
-- AND that no legitimate function will ever need to reach. Operators
-- who want stricter posture (loopback, link-local, RFC1918) can add
-- those as custom rules. UNIQUE(kind, value) means subsequent boots
-- leave the operator's toggles alone.
INSERT OR IGNORE INTO egress_blocklist (kind, rule_type, value, label, enabled) VALUES
    ('default', 'cidr', '169.254.0.0/16',     'Cloud metadata (AWS/Azure/GCP IPv4)', 1),
    ('default', 'cidr', 'fd00:ec2::254/128',  'Cloud metadata (GCP IPv6)', 1);

-- Seed suggested rules (shipped disabled — operator opts each in).
INSERT OR IGNORE INTO egress_blocklist (kind, rule_type, value, label, enabled) VALUES
    ('suggested', 'cidr', '10.0.0.0/8',     'Private network (RFC1918)', 0),
    ('suggested', 'cidr', '172.16.0.0/12',  'Private network (RFC1918)', 0),
    ('suggested', 'cidr', '192.168.0.0/16', 'Private network (RFC1918)', 0),
    ('suggested', 'cidr', '100.64.0.0/10',  'CGNAT / Tailscale', 0);

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
		// Snapshot of the function's mutable spawn config + env_vars at the
		// moment this deployment succeeded. Used by Rollback to restore the
		// full "state of the function" rather than only the code. JSON-encoded
		// DeploymentSnapshot. Empty for legacy rows; rollback gracefully
		// degrades to "code only" when absent.
		"ALTER TABLE deployments ADD COLUMN snapshot TEXT NOT NULL DEFAULT ''",
		// Per-function egress: "none" (default) blocks outbound network;
		// "egress" enables nsjail --user_net for external API calls.
		"ALTER TABLE functions ADD COLUMN network_mode TEXT NOT NULL DEFAULT 'none'",
		// Per-function concurrency cap. 0 = unlimited (default).
		// Policy controls behaviour when the cap is reached: "queue"
		// (block until a slot frees) or "reject" (return 429 BUSY).
		"ALTER TABLE functions ADD COLUMN max_concurrency INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE functions ADD COLUMN concurrency_policy TEXT NOT NULL DEFAULT 'queue'",
		// Per-function invoke auth gate. Default 'none' = public, matching
		// Cloudflare Workers / Vercel Functions / Lambda Function URLs.
		// Other values: 'platform_key' (require Orva API key) and 'signed'
		// (HMAC via X-Orva-Signature). Function code remains free to layer
		// JWT/session/etc. on top regardless of this setting.
		"ALTER TABLE functions ADD COLUMN auth_mode TEXT NOT NULL DEFAULT 'none'",
		// Per-function rate limit (requests/minute, per client IP). 0 =
		// unlimited. Token-bucket implementation lives in handlers/ratelimit.go.
		"ALTER TABLE functions ADD COLUMN rate_limit_per_min INTEGER NOT NULL DEFAULT 0",
		// Per-cron timezone. The expression "0 9 * * *" with timezone
		// "Asia/Kolkata" fires at 9 AM IST every day, regardless of the
		// orvad process timezone. UTC default keeps legacy rows behaving
		// identically to before this migration.
		"ALTER TABLE cron_schedules ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC'",
		// v0.4 A3: pointer back to the original execution when this row
		// was created via POST /api/v1/executions/{id}/replay. NULL on
		// the first-class invocation; non-NULL on every replay.
		"ALTER TABLE executions ADD COLUMN replay_of TEXT",
		// v0.5 tracing: each execution row IS a span. trace_id groups
		// every execution that resulted (directly or indirectly) from
		// the same top-level invocation. parent_span_id chains them
		// into a tree. trigger captures how this span was started so
		// the UI can show "this trace started from a cron / webhook /
		// HTTP / etc." parent_function_id is denormalised so trace
		// queries don't need a separate join. is_outlier + baseline_p95_ms
		// power the anomaly indicator without recomputing.
		"ALTER TABLE executions ADD COLUMN trace_id TEXT",
		"ALTER TABLE executions ADD COLUMN span_id TEXT",
		"ALTER TABLE executions ADD COLUMN parent_span_id TEXT",
		"ALTER TABLE executions ADD COLUMN trigger TEXT",
		"ALTER TABLE executions ADD COLUMN parent_function_id TEXT",
		"ALTER TABLE executions ADD COLUMN is_outlier INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE executions ADD COLUMN baseline_p95_ms INTEGER",
		"CREATE INDEX IF NOT EXISTS idx_executions_trace_id ON executions(trace_id)",
		"CREATE INDEX IF NOT EXISTS idx_executions_parent_span_id ON executions(parent_span_id)",
		// Jobs preserve trace context across the queue gap so the
		// scheduled execution that runs them lands in the same trace
		// as whatever enqueued them.
		"ALTER TABLE jobs ADD COLUMN trace_id TEXT",
		"ALTER TABLE jobs ADD COLUMN parent_span_id TEXT",
		"ALTER TABLE jobs ADD COLUMN enqueued_by_function_id TEXT",
		// Activity rows can also be linked into traces for cross-correlation.
		"ALTER TABLE activity_log ADD COLUMN trace_id TEXT",
		"CREATE INDEX IF NOT EXISTS idx_activity_log_trace_id ON activity_log(trace_id)",
		// v0.5 OAuth 2.1 authorization server. Three tables back the full
		// browser-based MCP connector flow (claude.ai web + ChatGPT web).
		// Tokens map onto the same permission strings api_keys uses; the
		// MCP middleware accepts both bearer types.
		`CREATE TABLE IF NOT EXISTS oauth_clients (
			id                          TEXT PRIMARY KEY,
			client_id                   TEXT UNIQUE NOT NULL,
			client_secret_hash          TEXT,
			client_name                 TEXT NOT NULL,
			client_uri                  TEXT,
			redirect_uris               TEXT NOT NULL,
			grant_types                 TEXT NOT NULL DEFAULT '["authorization_code","refresh_token"]',
			response_types              TEXT NOT NULL DEFAULT '["code"]',
			token_endpoint_auth_method  TEXT NOT NULL DEFAULT 'none',
			scope                       TEXT NOT NULL DEFAULT 'read invoke',
			created_at                  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			revoked_at                  DATETIME
		)`,
		"CREATE INDEX IF NOT EXISTS idx_oauth_clients_client_id ON oauth_clients(client_id)",
		`CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
			code                  TEXT PRIMARY KEY,
			client_id             TEXT NOT NULL,
			user_id               INTEGER NOT NULL,
			redirect_uri          TEXT NOT NULL,
			scope                 TEXT NOT NULL,
			resource              TEXT,
			code_challenge        TEXT NOT NULL,
			code_challenge_method TEXT NOT NULL DEFAULT 'S256',
			expires_at            DATETIME NOT NULL,
			used_at               DATETIME,
			created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		"CREATE INDEX IF NOT EXISTS idx_oauth_codes_expires ON oauth_authorization_codes(expires_at)",
		`CREATE TABLE IF NOT EXISTS oauth_access_tokens (
			id                  TEXT PRIMARY KEY,
			access_token_hash   TEXT UNIQUE NOT NULL,
			refresh_token_hash  TEXT UNIQUE,
			client_id           TEXT NOT NULL,
			user_id             INTEGER NOT NULL,
			scope               TEXT NOT NULL,
			resource            TEXT,
			issued_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			access_expires_at   DATETIME NOT NULL,
			refresh_expires_at  DATETIME,
			revoked_at          DATETIME
		)`,
		"CREATE INDEX IF NOT EXISTS idx_oauth_at_access_hash  ON oauth_access_tokens(access_token_hash)",
		"CREATE INDEX IF NOT EXISTS idx_oauth_at_refresh_hash ON oauth_access_tokens(refresh_token_hash)",
		"CREATE INDEX IF NOT EXISTS idx_oauth_at_access_expires ON oauth_access_tokens(access_expires_at)",
		// Settings → Connected applications: surface "last used N
		// minutes ago" so operators can spot abandoned grants. Added
		// after the table existed in v2026.05.03; the loop below
		// silences "duplicate column" so it's safe to re-run.
		"ALTER TABLE oauth_access_tokens ADD COLUMN last_used_at DATETIME",
	} {
		if _, err := db.write.Exec(stmt); err != nil {
			// "duplicate column name" is expected on boot after the first.
			if !strings.Contains(err.Error(), "duplicate column") {
				slog.Warn("schema alter failed", "stmt", stmt, "err", err)
			}
		}
	}

	// v0.4 A3 fix: drop the legacy FK on execution_requests.execution_id
	// for databases that were created before the FK-removal change. SQLite
	// has no ALTER TABLE DROP CONSTRAINT, so the only way to remove an FK
	// is a table rebuild. The check below uses sqlite_master to detect the
	// "REFERENCES executions" substring; on a fresh DB the CREATE TABLE
	// already lacks the FK and the rebuild is skipped.
	if err := dropExecutionRequestsFK(db); err != nil {
		slog.Warn("execution_requests FK drop failed", "err", err)
	}

	// Backfill deployment snapshots for the most-recent succeeded deploy
	// of each function. Without this, rolling back from a brand-new build
	// to anything that landed before this migration would only swap code
	// — not env / memory / cpu / network mode / auth mode etc. The
	// backfill is one-shot and idempotent: subsequent boots find nothing
	// to update because the snapshot column is non-empty for these rows.
	if _, err := db.write.Exec(`
		UPDATE deployments
		SET snapshot = json_object(
			'env_vars',           json(COALESCE(f.env_vars, '{}')),
			'memory_mb',          f.memory_mb,
			'cpus',               f.cpus,
			'timeout_ms',         f.timeout_ms,
			'network_mode',       f.network_mode,
			'auth_mode',          COALESCE(f.auth_mode, 'none'),
			'rate_limit_per_min', COALESCE(f.rate_limit_per_min, 0),
			'max_concurrency',    f.max_concurrency,
			'concurrency_policy', f.concurrency_policy
		)
		FROM functions f
		WHERE deployments.function_id = f.id
		  AND deployments.status = 'succeeded'
		  AND COALESCE(deployments.snapshot, '') = ''
		  AND deployments.id IN (
			SELECT id FROM deployments d2
			WHERE d2.function_id = f.id AND d2.status = 'succeeded'
			ORDER BY d2.submitted_at DESC LIMIT 1
		  )
	`); err != nil {
		slog.Warn("deployment snapshot backfill failed", "err", err)
	}

	// Slim the default firewall rules down to the universally-dangerous
	// minimum (cloud metadata only). Earlier builds seeded loopback and
	// IPv6 link-local as defaults; both could break legitimate flows
	// (Docker's resolver at 127.0.0.11; IPv6 SLAAC) and most operators
	// don't need them blocked. We delete only kind='default' rows that
	// match exact retired values — operator-edited custom rules are
	// untouched.
	for _, retired := range []string{"127.0.0.0/8", "fe80::/10"} {
		if _, err := db.write.Exec(
			"DELETE FROM egress_blocklist WHERE kind = 'default' AND value = ?",
			retired,
		); err != nil {
			slog.Warn("firewall default-rules cleanup failed", "value", retired, "err", err)
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

	// One-shot rewrite of every prefix-typed storage ID (fn_, key_,
	// oat_, etc.) to UUIDv7. Idempotent — guarded by a marker row in
	// system_config. Runs AFTER the schema migrations so the marker
	// table exists. Fails loud if FK integrity breaks, which is the
	// only safe behaviour for a one-way data migration.
	if err := db.MigrateToUUIDv7(); err != nil {
		return fmt.Errorf("uuidv7 migration: %w", err)
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

// dropExecutionRequestsFK rebuilds the execution_requests table without
// the legacy FK on execution_id. Idempotent: if the stored CREATE TABLE
// no longer contains a REFERENCES clause we skip the rebuild. The proxy
// queues capture rows on request-receive while the parent executions
// row only lands at the end of dispatch, so the two writes commonly
// land in different async batches and the FK fires at commit time,
// rolling back the entire batch (FOREIGN KEY constraint failed (787)).
// See migrations.go schema comment for full rationale.
func dropExecutionRequestsFK(db *Database) error {
	var sqlText string
	err := db.write.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='execution_requests'",
	).Scan(&sqlText)
	if err != nil {
		// Fresh DB or table not yet created — nothing to do.
		return nil
	}
	if !strings.Contains(strings.ToUpper(sqlText), "REFERENCES EXECUTIONS") {
		return nil
	}

	tx, err := db.write.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmts := []string{
		`CREATE TABLE execution_requests_new (
			execution_id  TEXT PRIMARY KEY,
			method        TEXT NOT NULL,
			path          TEXT NOT NULL,
			headers_json  TEXT NOT NULL,
			body          BLOB,
			truncated     INTEGER NOT NULL DEFAULT 0,
			captured_at   INTEGER NOT NULL
		)`,
		`INSERT INTO execution_requests_new (execution_id, method, path, headers_json, body, truncated, captured_at)
			SELECT execution_id, method, path, headers_json, body, truncated, captured_at
			FROM execution_requests`,
		`DROP TABLE execution_requests`,
		`ALTER TABLE execution_requests_new RENAME TO execution_requests`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("rebuild step: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	slog.Info("execution_requests FK dropped")
	return nil
}
