# Configuration

Orva is configured entirely through environment variables. There is no
config file — every knob that needs operator input is an env var. A bare
`docker run` with no env set works out of the box.

On startup, Orva logs which of the 9 supported env vars it found set and
how many are at their defaults.

---

## Environment variables

| env | default | what |
|-----|---------|------|
| `ORVA_DATA_DIR` | `~/.orva` (dev) / `/var/lib/orva` (Docker) | Root for SQLite DB, function code, and rootfs trees. DB and rootfs paths are derived automatically. |
| `ORVA_PORT` | `8443` | Listen port — plain HTTP, no TLS. Set to `8080` when a reverse proxy owns 8443. |
| `ORVA_WRITE_TIMEOUT_SEC` | `60` | Response write timeout. Must exceed your longest function `timeout_ms` or Orva will cut the response. |
| `ORVA_DEFAULT_TIMEOUT_MS` | `30000` | Per-invocation timeout applied to new functions that don't set one explicitly. Exceeded → `504 TIMEOUT`. |
| `ORVA_DEFAULT_MEMORY_MB` | `64` | cgroup memory cap applied to new functions. Tune to your host's available RAM. |
| `ORVA_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `ORVA_LOG_RETENTION_DAYS` | `7` | Execution and build log retention in days. Logs older than this are pruned on startup. |
| `ORVA_SECURE_COOKIES` | `false` | Set to `true` when Orva is behind an HTTPS reverse proxy. Adds the `Secure` flag to session cookies. |
| `ORVA_SESSION_DAYS` | `7` | Session cookie lifetime in days. Single-operator instances can set this to `30`. |

---

## What's hardcoded (not configurable)

These values are intentionally fixed — they are correct for every
deployment and exposing them as knobs would only create confusion:

| what | value |
|------|-------|
| Bind address | `0.0.0.0` |
| HTTP read timeout | 30 s |
| Request body cap | 6 MB |
| nsjail binary | `/usr/local/bin/nsjail` |
| Rootfs dir | `${ORVA_DATA_DIR}/rootfs` |
| DB path | `${ORVA_DATA_DIR}/orva.db` |
| CORS origins | `*` |
| Default function CPUs | `0.5` |
| Deploy tarball cap | 50 MB |
| Seccomp policy | `default` |
| Log format | `json` |
| Max concurrent invocations | `cpu_count × 64` (min 200) |

---

## Typical docker-compose snippet

```yaml
environment:
  ORVA_DEFAULT_MEMORY_MB: "128"   # tune to host RAM
  ORVA_WRITE_TIMEOUT_SEC: "90"    # headroom above your longest function timeout
  ORVA_SESSION_DAYS: "30"         # single-operator instance
  ORVA_LOG_RETENTION_DAYS: "30"   # keep a month of logs
```

Orva does not terminate TLS. Run a reverse proxy (nginx, Caddy, Traefik)
in front and set `ORVA_SECURE_COOKIES=true` when you do. See
[DEPLOYMENT.md](DEPLOYMENT.md) for proxy config examples.

---

## Runtime-tunable: pool config (per-function)

Edited via `PUT /api/v1/pool/config` — no restart needed.

| field | default | what |
|-------|---------|------|
| `min_warm` | 1 | Idle workers floor — pool never shrinks below this |
| `max_warm` | 50 | Hard ceiling on warm pool size |
| `idle_ttl_s` | 120 | Worker idle this long gets reaped |
| `target_concurrency` | 10 | Requests per worker before scale-up triggers |
| `scale_to_zero` | 0 | `1` = pool can drain to 0 (cold-start on next request) |

```bash
curl -X PUT -H "X-Orva-API-Key: $KEY" -H 'Content-Type: application/json' \
  http://localhost:8443/api/v1/pool/config \
  -d '{"function_id":"019df200-7b00-7e00-9c00-aab1cd2e3f40","min_warm":2,"max_warm":32,"idle_ttl_seconds":60}'
```
