# Configuration

Orva is configured entirely through environment variables. There is no
config file — every knob that needs operator input is an env var. A bare
`docker run` with no env set works out of the box.

On startup, Orva logs which of the 11 vars in `config.SupportedEnvVars` it
found set and
how many are at their defaults.

Every variable below has an observable runtime effect. A knob that an
operator can set with no consequence is worse than no knob at all, so
names that stopped doing anything are deleted rather than deprecated.

---

## Environment variables

| env | default | what |
|-----|---------|------|
| `ORVA_DATA_DIR` | `~/.orva` (dev) / `/var/lib/orva` (Docker) | Root for SQLite DB, function code, and rootfs trees. DB and rootfs paths are derived automatically. |
| `ORVA_HOST` | `0.0.0.0` | Bind address for the HTTP listener. Set to `127.0.0.1` to listen on loopback only (recommended behind a reverse proxy). |
| `ORVA_PORT` | `8443` | Listen port — plain HTTP, no TLS. Set to `8080` when a reverse proxy owns 8443. |
| `ORVA_WRITE_TIMEOUT_SEC` | `60` | Response write timeout. Must exceed your longest function `timeout_ms` for **buffered** responses, or Orva will cut them off. It does **not** bound streaming responses: the write deadline is cleared on the streaming path, which is governed by `stream_max_seconds` (default 300) instead. |
| `ORVA_MAX_BODY_BYTES` | `6291456` | Max request body (bytes) for `/api/v1/*` JSON endpoints. Function code uploads (`/deploy`, `/deploy-inline`) and `/restore` are exempt — those are bounded by the 50 MB code-size cap instead. |
| `ORVA_CORS_ORIGINS` | `*` | Comma-separated allow-list of browser origins for the API/dashboard. Default allows all; set an explicit list (e.g. `https://orva.example`) to lock it down. With an explicit list Orva echoes back the request's own `Origin` when it is a member and sends `Vary: Origin`; a request from an unlisted origin gets no `Access-Control-Allow-Origin` at all. The same list also gates `/mcp`: a browser request with an unlisted `Origin` is rejected 403 before auth, while requests with no `Origin` (every non-browser MCP client) are always allowed. Narrowing this therefore affects third-party agent-channel consumers too, not just the dashboard. |
| `ORVA_SECCOMP_POLICY` | `default` | Seccomp policy applied to every sandbox: `default` / `strict` / `permissive` / `disabled`. An unrecognized value is ignored (stays `default`). |
| `ORVA_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `ORVA_SECURE_COOKIES` | `false` | Force the `Secure` flag on session cookies. Orva already sets it automatically when the request arrives over TLS or carries `X-Forwarded-Proto: https`; set this when neither is visible to it. |
| `ORVA_TRUSTED_PROXY` | `false` | Set to `true` only when a reverse proxy in front of Orva sets `X-Forwarded-For`. It makes Orva trust that header (and `X-Real-IP`) for the client identity **every** rate limiter buckets on: per-function `rate_limit_per_min`, the login brute-force throttle, and the OAuth dynamic-registration limiter. Leave it off otherwise — trusting a client-settable header lets any caller bypass all three by varying one value per request. When it is on, Orva reads the **rightmost** `X-Forwarded-For` entry: nginx, Caddy and Traefik all append the peer they saw, so entries further left came from the client and remain forgeable. With two or more proxy hops (Cloudflare in front of nginx) the bucket is your outermost hop's address — coarser, never more permissive. |
| `ORVA_SESSION_DAYS` | `7` | Session cookie lifetime in days. Single-operator instances can set this to `30`. |
| `ORVA_PPROF_ADDR` | (unset) | When set (e.g. `127.0.0.1:6060`), starts a Go `net/http/pprof` debug listener on that address. Bind to loopback only — it exposes goroutine/heap profiles. Off by default. |
| `ORVA_IMAGE` | (set by the container image) | The image reference this instance runs from, echoed at `GET /api/v1/system/health` and in Settings → Build info. The published image stamps it; set it yourself only for a mirrored or re-tagged copy. A bare-metal install leaves it unset and reports no image. |
| `ORVA_PPROF_ADDR`, `ORVA_INTERNAL_API_BASE`, `ORVA_IMAGE` are read directly where they are used rather than through the config loader, so they do **not** appear in that startup line. They still work. | |
| `ORVA_INTERNAL_API_BASE` | (auto-detected) | The base URL sandboxed functions use to reach Orva's own internal SDK endpoints (KV, jobs, function-to-function). Orva probes for a routable address at startup — from inside a sandbox `127.0.0.1` is the sandbox's own loopback, so this is deliberately **not** a loopback address. Set it only on network setups the probe gets wrong (overlay networks, Swarm, k8s), as `http://host:port`. The compiled egress policy emits a narrow allow rule for exactly this address and port, so an operator blocking private ranges does not cut off the SDK. |

---

## What's hardcoded (not configurable)

These values are intentionally fixed — they are correct for every
deployment and exposing them as knobs would only create confusion:

| what | value |
|------|-------|
| HTTP read timeout | 30 s |
| nsjail binary | `/usr/local/bin/nsjail` |
| Rootfs dir | `${ORVA_DATA_DIR}/rootfs` (derived) |
| DB path | `${ORVA_DATA_DIR}/orva.db` (derived) |
| New-function timeout | `30000` ms — change per function, see below |
| New-function memory | `64` MB — change per function, see below |
| Default function CPUs | `0.5` |
| Deploy tarball cap | 50 MB |
| Log format | `json` |
| Max concurrent invocations | `cpu_count × 64` (min 200) |
| Max processes per sandbox | 32 (`--cgroup_pids_max`) |

Timeout and memory are **per-function**, not per-instance: the values above
are only what a function gets when it is created without an explicit one.
Change them on the function itself and the change applies immediately, with
no restart:

```bash
curl -X PUT -H "X-Orva-API-Key: $KEY" -H 'Content-Type: application/json' \
  http://localhost:8443/api/v1/functions/my-fn \
  -d '{"timeout_ms":60000,"memory_mb":256}'
```

There is deliberately no instance-wide override for these — one existed as
`ORVA_DEFAULT_TIMEOUT_MS` / `ORVA_DEFAULT_MEMORY_MB` and was removed because
nothing read it, so setting it silently did nothing.

---

## Typical docker-compose snippet

```yaml
environment:
  ORVA_WRITE_TIMEOUT_SEC: "90"    # headroom above your longest function timeout
  ORVA_SESSION_DAYS: "30"         # single-operator instance
```

Orva does not terminate TLS. Run a reverse proxy (nginx, Caddy, Traefik) in
front; it sets the `Secure` flag on session cookies by itself once it can see
either TLS or an `X-Forwarded-Proto: https` from the proxy.

Caddy, Traefik and cloudflared send that header without being asked. **nginx
does not** — a bare `proxy_pass` forwards no `X-Forwarded-*` at all, so you must
add `proxy_set_header X-Forwarded-Proto $scheme;` yourself. The example in
[DEPLOYMENT.md](DEPLOYMENT.md) includes it.

`ORVA_SECURE_COOKIES=true` is the override for the setup where neither reaches
Orva. **Do not set it on a plain-HTTP instance** — a browser will not store a
`Secure` cookie over `http://` on anything but `localhost`, so signing in from a
LAN address bounces straight back to the login screen.

---

## Runtime-tunable: pool config (per-function)

Edited via `PUT /api/v1/pool/config` — no restart needed.

| field | default | what |
|-------|---------|------|
| `min_warm` | 1 | Idle workers floor — pool never shrinks below this |
| `max_warm` | 50 | Hard ceiling on warm pool size |
| `idle_ttl_seconds` | 600 | No-demand interval before an opted-in pool scales to zero |
| `scale_to_zero` | `false` | `true` = pool can drain to 0 (cold-start on next request) |

Pool Controller v2 chooses capacity automatically from 60-second arrival
rate × service p95, 6-second arrival rate × (service p95 + spawn p95), and
immediate busy + queued pressure, with a 70% internal utilization target.
`target_concurrency` was removed; stale requests receive `400 VALIDATION`
with migration guidance.

Admission is global across functions: the host CPU quota supplies eight
I/O-overlap worker slots per CPU, weighted by each function's declared `cpus`,
and memory uses cgroup v2 headroom plus per-worker reservations.

`scale_to_zero=true` owns `min_warm=0`. Turning it off restores a minimum of
at least one. Sending both fields with an incompatible pair is rejected.

```bash
curl -X PUT -H "X-Orva-API-Key: $KEY" -H 'Content-Type: application/json' \
  http://localhost:8443/api/v1/pool/config \
  -d '{"function_id":"019df200-7b00-7e00-9c00-aab1cd2e3f40","min_warm":2,"max_warm":32,"idle_ttl_seconds":600}'
```

---

## Runtime-tunable: data retention

Every invocation writes an `executions` row plus its logs, and — when replay
capture is enabled — the captured request. Those are trimmed on a schedule so
the database does not grow without bound.

| Setting (`system_config` key) | Default | Meaning |
|---|---|---|
| `execution_retention_days` | `30` | Delete finished rows older than this many days. **`0` disables purging entirely** (keep everything). |

The sweep covers executions and their children (logs, captured requests,
structured log entries, user spans) **and**, past the same cutoff:
`jobs`, `webhook_deliveries` and `build_logs`. Only **terminal** rows are
removed — a pending or running job is left alone regardless of age, so
nothing is swept out from under the scheduler. Expired `sessions` are removed
on every pass regardless of the window.

Deployment **rows** are deliberately kept: they are the rollback audit trail
the dashboard lists, and `build_logs` is where the volume actually is.
On-disk version pruning is the version GC's job (`versions_to_keep`), not
this setting's.

The first purge is deliberately delayed by an hour after startup, then runs
every 24 hours. The delay exists so that an operator upgrading into a build
that has this enabled can see the setting (it is seeded in `system_config`) and
the startup log line, and change it, before anything is deleted — deleted rows
are the one thing a rollback cannot restore. The setting is re-read
on each pass, so changing it takes effect without restarting the server.

This is deliberately **not** an environment variable: it is operational state an
operator may want to change on a running instance, in the same category as the
DNS resolvers and the egress blocklist, and an env var would require a restart
to change.

> Earlier builds shipped an `ORVA_LOG_RETENTION_DAYS` environment variable and
> documented that old logs were "pruned on startup". Nothing read that variable
> and no purge ever ran, so history accumulated forever. The variable is gone
> and the behaviour it promised is now real.

---

## Runtime-tunable: build cache

Dependency installs run inside an nsjail build jail whose `/tmp` is thrown away
after every build. To stop each deploy re-downloading every dependency, Orva
keeps npm's and pip's caches in a **per-function** directory at
`<data-dir>/build-cache/<function-id>/{npm,pip}`, mounted at `/tmp/cache` in the
build jail.

The cache is per-function on purpose and must not be made shared: npm keeps the
packument in the same URL-keyed cache as the tarball behind an unkeyed
corruption checksum, pip's HTTP cache has no content verification at all, and
`npm install` runs whatever postinstall script a package ships. A shared cache
would let one function's bad dependency poison every later build of every other
function.

| Setting (`system_config` key) | Default | Meaning |
|---|---|---|
| `build_cache_max_age_days` | `14` | Delete a function's build cache once it has gone this many days without a build. **`0` disables the age sweep** (the size cap still applies). |
| `build_cache_max_mb` | `2048` | Total size ceiling across all functions. Over it, Orva evicts **whole per-function caches**, least-recently-used first — never individual files, which would leave a cache internally inconsistent. **`0` disables the size cap.** |

Both bounds are enforced by the same background pass that prunes old version
directories (`gc_interval_seconds`, default 300 s), and both settings are re-read
on every pass, so changing them takes effect without restarting the server. A
cache is also dropped when its function is deleted, and when free disk falls
below `min_free_disk_mb` — a rebuildable cache is never the reason a deploy
fails for want of space.

To drop one function's cache by hand — the clean recovery from a build that
installed a bad package, since the cache is the only place those bytes persist
between deploys:

```bash
orva functions purge-cache greeter

curl -X DELETE -H "X-Orva-API-Key: $KEY" \
  http://localhost:8443/api/v1/functions/greeter/build-cache
```

The next deploy of that function refetches its dependencies and is slower once.

## Build identity

The server binary stamps three values at link time and exposes them at `GET /api/v1/system/health` + Settings → Build info in the dashboard.

| Field | Source | Example |
|---|---|---|
| `version`    | git tag (release) or `git describe` (dev)       | `v2026.05.15` |
| `commit`     | short git SHA at build time                     | `1be3399` |
| `build_time` | wall-clock RFC3339 UTC at link time             | `2026-05-15T14:20:34Z` |
| `image`      | `ORVA_IMAGE`, stamped into the published image  | `ghcr.io/harsh-2002/orva:latest`, empty on bare metal |

Override at build time:

```bash
make build VERSION=v2026.05.15 \
           COMMIT=$(git rev-parse --short HEAD) \
           BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
```

Container images carry the same identity as OCI labels (`org.opencontainers.image.{version,revision,created}`) so `docker inspect` agrees with the running server's `/api/v1/system/health` response. Unstamped binaries report `"dev"` / `"unknown"` — an intentional signal that the build chain wasn't wired through.

Orva publishes exactly **one** image tag, `:latest` — there is no per-version image tag, and pruning removes any that appear — so nothing derives a pullable reference from `version`. `image` reports whatever `ORVA_IMAGE` holds, and nothing when it is unset.
