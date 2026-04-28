# Configuration

Three layers, applied in order:

1. **Defaults** baked into the binary (`backend/internal/config/defaults.go`)
2. **YAML config file** at `/etc/orva/orva.yaml` or `--config <path>`
3. **Environment variables** (`ORVA_*` prefix) — override everything

Plus runtime-tunable knobs in two SQLite tables:

- `system_config` — global, edited via SQL or upcoming `/api/v1/system/config`
- `pool_config` — per-function, edited via `PUT /api/v1/pool/config`

## Server (HTTP listener)

| key (YAML)         | env                     | default       | what |
|--------------------|-------------------------|---------------|------|
| `server.host`      | `ORVA_HOST`             | `0.0.0.0`     | listen address |
| `server.port`      | `ORVA_PORT`             | `8443`        | listen port (despite the 8443, **not TLS** — terminate TLS upstream) |
| `server.read_timeout_sec`  | `ORVA_READ_TIMEOUT_SEC`  | `30`          | request body read timeout |
| `server.write_timeout_sec` | `ORVA_WRITE_TIMEOUT_SEC` | `90`          | response write timeout (must exceed function `timeout_ms`) |
| `server.max_body_bytes`    | `ORVA_MAX_BODY_BYTES`    | `6291456`     | request body cap (6 MB); over → `413 PAYLOAD_TOO_LARGE` |

## Data

| key (YAML)    | env              | default              | what |
|---------------|------------------|----------------------|------|
| `data.dir`    | `ORVA_DATA_DIR`  | `/var/lib/orva`      | sqlite + functions + rootfs |
| `database.path`| `ORVA_DB_PATH`  | `${data.dir}/orva.db`| explicit override |

## Sandbox

| key (YAML)               | env                       | default          | what |
|--------------------------|---------------------------|------------------|------|
| `sandbox.nsjail_bin`     | `ORVA_NSJAIL_BIN`         | `/usr/local/bin/nsjail` | nsjail binary path |
| `sandbox.rootfs_dir`     | `ORVA_ROOTFS_DIR`         | `${data.dir}/rootfs`    | per-runtime rootfs trees |
| `sandbox.seccomp_policy` | `ORVA_SECCOMP_POLICY`     | `default`        | `default` / `strict` / `permissive` / `disabled` |
| `sandbox.max_concurrent` | `ORVA_MAX_CONCURRENT`     | `500`            | host-wide invocation cap. Excess → `429 TOO_MANY_REQUESTS` |
| `sandbox.max_pids`       | `ORVA_MAX_PIDS`           | `64`             | per-spawn pid cgroup cap |

## Logging

| key (YAML)         | env                | default | what |
|--------------------|--------------------|---------|------|
| `log.level`        | `ORVA_LOG_LEVEL`   | `info`  | `debug` / `info` / `warn` / `error` |
| `log.format`       | `ORVA_LOG_FORMAT`  | `text`  | `text` / `json` |

## `system_config` table (runtime-tunable)

Read at startup or per-tick by background loops. Edit via SQL today;
a future `/api/v1/system/config` endpoint is planned.

| key                          | default   | what |
|------------------------------|-----------|------|
| `max_total_containers`       | 100       | unused (legacy; superseded by pool_config) |
| `default_timeout_ms`         | 30000     | new-function default |
| `default_memory_mb`          | 128       | new-function default |
| `max_code_size_bytes`        | 52428800  | per-deploy tarball cap (50 MB) |
| `max_request_body_bytes`     | 6291456   | mirror of server.max_body_bytes |
| `log_retention_days`         | 7         | future: prune old execution_logs / build_logs |
| `reap_interval_seconds`      | 30        | how often the per-pool reaper runs |
| `replenish_interval_seconds` | 5         | autoscaler tick rate |
| `versions_to_keep`           | 5         | per-function archived versions retained by GC |
| `gc_interval_seconds`        | 300       | how often the GC scans every function |
| `min_free_disk_mb`           | 500       | pre-flight gate; below this, deploys return `503 INSUFFICIENT_DISK` |

Edit:
```sql
docker exec orva sqlite3 /var/lib/orva/orva.db \
  "UPDATE system_config SET value='10' WHERE key='versions_to_keep'"
```

The active hash is **always** retained regardless of `versions_to_keep`.

## `pool_config` table (per-function)

| column                | default | what |
|-----------------------|---------|------|
| `function_id`         | —       | FK to `functions(id)` |
| `min_warm`            | 1       | floor on idle workers — pool never shrinks below this when traffic is steady |
| `max_warm`            | 50      | hard ceiling. The autoscaler also computes a `dynamic_max` from CPU + memory budget; the effective cap is `min(max_warm, dynamic_max)` |
| `idle_ttl_s`          | 120     | a worker idle this long is reaped (was 600 pre-Round-G) |
| `target_concurrency`  | 10      | Knative-KPA's "request per worker before scale-up considered" |
| `scale_to_zero`       | 0       | if `1`, pool can shrink to 0 (cold-start on next request) |

Edit via API:
```bash
curl -X PUT -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/pool/config \
  -d '{
    "function_id": "fn_xyz",
    "min_warm": 2,
    "max_warm": 32,
    "idle_ttl_seconds": 60,
    "target_concurrency": 5
  }'
```

Changes take effect on the next pool acquire (the running pool is
torn down via `RefreshForDeploy`).

## Function-level config

These live on the `functions` table row and are set at create / update
time:

| field          | what |
|----------------|------|
| `timeout_ms`   | per-invocation timeout — exceeded → `504 TIMEOUT` |
| `memory_mb`    | declared cgroup memory.max (× 1.5 actual) |
| `cpus`         | fractional CPU bandwidth (e.g. `0.5` = 500ms / 1000ms) |
| `env_vars`     | JSON-encoded map; merged with secrets at spawn time |
| `network_mode` | `isolated` (loopback only) / `host` / `bridge` |
| `status`       | `created` / `building` / `active` / `inactive` / `error` |

## YAML example

```yaml
# /etc/orva/orva.yaml
server:
  host: 0.0.0.0
  port: 8443
  max_body_bytes: 10485760    # 10 MB

data:
  dir: /var/lib/orva

sandbox:
  seccomp_policy: strict
  max_concurrent: 1000
  max_pids: 128

log:
  level: info
  format: json
```

Pass with `orva serve --config /etc/orva/orva.yaml`.

## What's NOT configurable yet

- Reverse-proxy aware logging (real client IP via `X-Forwarded-For`)
- Per-function rate limiting (host-wide cap is the only knob today)
- Custom adapter scripts (the bundled adapter is the only option)
- Multiple data dirs / multi-tenant isolation

These are explicit non-goals for the single-host, self-hosted target.
