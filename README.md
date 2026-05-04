# Orva

A self-hosted serverless stack you can run with one Docker container.
Deploy Node.js and Python functions to your own machine, invoke them
over HTTP, and get warm-pool latency without paying anyone.

> **Active development.** Don't use this in production yet.
> The feature surface is stable but rough edges remain. It's built for
> homelabs, side projects, and people who want to experiment with
> Functions-as-a-Service without renting cloud capacity.

## What it does

- **Deploy** Node.js (22, 24) or Python (3.13, 3.14) functions
  inline (paste code in the dashboard) or as a tarball with
  `package.json` / `requirements.txt`.
- **Invoke** at `POST /fn/<id>/<path>` where `<id>` is the function's UUID. The path tail
  reaches your handler as `event.path`. AWS-Lambda-style and HTTP-style
  handlers both work.
- **Schedule** functions on cron expressions. The Schedules dashboard
  shows last/next run + status; pair with templates like `cron-cleanup`
  or `email-digest` for one-click setups.
- **Background jobs** — `orva.jobs.enqueue(name, payload)` from inside
  a function persists a fire-and-forget run with retries and
  exponential backoff. Visible in the Jobs dashboard with retry/delete.
- **Function-to-function** — `orva.invoke(name, payload)` calls another
  function in-process via the warm pool, no HTTP roundtrip. 8-deep
  call-depth guard.
- **KV store** — `orva.kv.put/get/delete/list` per-function namespace
  on SQLite, optional TTL. No external service needed. Operators can
  browse / edit / delete / set keys from the dashboard at
  `/functions/<name>/kv`, the REST API
  (`/api/v1/functions/<id>/kv[/<key>]`), or via MCP tools.
- **Roll back** to any prior content-hashed version in one click.
  Each deploy is archived; rollback is an atomic symlink retarget.
- **MCP server** — `/mcp` exposes the full management surface (functions,
  deploys, invocations, secrets, routes, keys, firewall, cron, KV,
  jobs, webhooks, fixtures, inbound triggers, traces, full Orva docs export) as 70 tools an AI agent (Claude Code, Cursor, etc.)
  can call directly. Authenticates with either a static API-key bearer
  *or* OAuth 2.1 — the latter lets operators add Orva as a custom
  connector in the **claude.ai web UI** and **ChatGPT web UI** with
  one URL paste, without copying an API key.
- **Live dashboard** — every invocation, deploy, and metric streams
  over a single SSE connection. No polling.
- **Per-function secrets**, encrypted at rest, injected as env vars
  at sandbox spawn time.
- **Custom routes** — map `/webhooks/stripe` to a function so external
  callers don't need the function ID.
- **Function templates** — 16 production-ready starters (Stripe,
  GitHub, Slack, Discord, JWT, OAuth, CSV→JSON, Markdown→HTML,
  thumbnailer, RSS summarizer, URL shortener, more). Pickable in the
  editor's Settings panel.

## How isolation works

Every invocation runs in a fresh **nsjail** sandbox:

- **User namespace** — function code thinks it's UID 0 but maps to
  `nobody` (UID 65534) on the host with **zero Linux capabilities**.
- **Chroot + bind mounts** — only the function's own `versions/<hash>/`
  is visible at `/code`, **read-only**. A private `tmpfs` covers `/tmp`.
- **cgroup v2** — `memory.max`, `cpu.max`, `pids.max` enforced
  per-spawn. Per-host memory budget refuses spawns past 80% reservation.
- **seccomp filter** — Kafel policy blocks ~150 dangerous syscalls
  (`mount`, `unshare`, `bpf`, `kexec_load`, `init_module`, etc.).
- **Network namespace** — `isolated` mode (default) gives the function
  only loopback. Outbound HTTP fails with `ENETUNREACH`.

Full security model in [`docs/SECURITY.md`](docs/SECURITY.md), with a
copy-pasteable verification recipe that reads `/proc/self/status` from
inside a function and shows all 64 capability bits cleared.

## Install

### Linux (any distro)

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sudo sh
```

Tested end-to-end on Ubuntu 24.04, Debian 12, Alpine 3.19, Rocky 9,
Fedora 40, Arch, openSUSE Leap 15.6. Static binaries — no glibc /
libstdc++ runtime dependencies.

### Docker

```bash
docker run -d --name orva -p 8443:8443 \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  -v orva-data:/var/lib/orva \
  ghcr.io/harsh-2002/orva:latest
```

Or `docker compose up -d` from this repo.

After install, open `http://localhost:8443` and complete onboarding.

## CLI install

The same `orva` binary that powers the daemon doubles as a standalone CLI
(`orva functions list`, `orva deploy`, `orva invoke`, `orva logs`, etc.).
You can grab just the client without installing the server.

### Standalone (laptop / CI runner / remote operator)

```bash
# Linux amd64 — pick arm64 / darwin-arm64 from the assets list
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/orva-cli-linux-amd64 \
  -o /usr/local/bin/orva
chmod +x /usr/local/bin/orva

orva login --endpoint https://orva.example.com --api-key <admin-key>
orva functions list
```

Or via the installer:

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh \
  | sudo sh -s -- --cli-only
```

Released binaries: `orva-cli-linux-amd64`, `orva-cli-linux-arm64`,
`orva-cli-darwin-arm64`. CGO-disabled, fully static (Linux), `-trimpath` +
stripped — no dependencies.

### Inside the container (zero-config)

The Docker image pre-writes `~/.orva/config.yaml` on first boot using the
auto-generated bootstrap admin key, so the CLI works straight away:

```bash
docker exec -it orva orva system health
docker exec -it orva orva functions list
docker exec -it orva orva deploy hello-world ./code
```

If you exposed orvad on a non-default port, edit
`/root/.orva/config.yaml` inside the container or pass `--endpoint`
on each call.

## SDK from inside a function

Every worker is spawned with `ORVA_INTERNAL_TOKEN` + `ORVA_API_BASE`
in its environment. The bundled `orva` module (Python and Node) routes
through them so user code can call into Orva without setting up any
HTTP client:

```python
# Python
from orva import kv, invoke, jobs

await kv.put("user:42", {"name": "Ada"}, ttl_seconds=3600)
v = kv.get("user:42")

result = invoke("resize-image", {"url": "..."})

job_id = jobs.enqueue("send-welcome-email", {"to": "ada@example.com"})
```

```js
// Node
const { kv, invoke, jobs } = require('orva')

await kv.put('user:42', { name: 'Ada' }, { ttlSeconds: 3600 })
const v = await kv.get('user:42')

const result = await invoke('resize-image', { url: '...' })

const jobId = await jobs.enqueue('send-welcome-email', { to: 'ada@example.com' })
```

The SDK requires `network_mode=egress` on the function (the default
`none` mode has no network namespace at all). If absent the SDK throws
`OrvaUnavailableError` with a clear hint.

## Configuration (environment variables)

Orva reads its configuration from environment variables at startup. All vars are optional — sensible defaults ship out of the box. Set them on the container (`environment:` block in `docker-compose.yml`) or as systemd `Environment=` lines.

| Variable | Default | Description |
|---|---|---|
| `ORVA_DATA_DIR` | `/var/lib/orva` (Docker) · `~/.orva` (bare-metal) | Root directory for `orva.db`, function code, rootfs, secrets. Database path becomes `<dir>/orva.db`; sandbox rootfs is mounted from `<dir>/rootfs`. |
| `ORVA_PORT` | `8443` | TCP port the HTTP server listens on. Bind address is always `0.0.0.0`. |
| `ORVA_WRITE_TIMEOUT_SEC` | `60` | HTTP write timeout. Keep ≥ your function `timeout_ms` plus headroom — set to `90`+ if you allow 30 s functions. |
| `ORVA_DEFAULT_TIMEOUT_MS` | `30000` | Default execution timeout for new functions when none is set on the function record. |
| `ORVA_DEFAULT_MEMORY_MB` | `64` | Default memory ceiling (MB) for new functions when none is set on the function record. |
| `ORVA_LOG_LEVEL` | `info` | Slog level: `debug`, `info`, `warn`, `error`. |
| `ORVA_LOG_RETENTION_DAYS` | `7` | How many days of execution logs / activity rows the sweeper keeps before pruning. |
| `ORVA_SECURE_COOKIES` | _unset_ (false) | Set to `true` or `1` when fronting Orva with HTTPS (Caddy / Traefik / nginx) so the session cookie carries the `Secure` flag. **Do not enable for plain HTTP** — browsers will refuse to send the cookie and you'll be logged out instantly. |
| `ORVA_SESSION_DAYS` | `7` | Session cookie lifetime in days. Long-lived single-operator instances often want `30`. |

At startup the server logs which of these were actually picked up under the key `active_env_vars` — useful for confirming a change took effect.

## Layout

```
backend/    Go server, sandbox runtime, SQLite, build queue, scheduler
frontend/   Vue 3 dashboard
scripts/    install.sh, uninstall.sh, systemd unit, OpenRC
docs/       architecture, security, API, runtimes, operations
test/       end-to-end shell suites
```

## Documentation

| doc | what's in it |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | components, request lifecycle, deploy lifecycle, storage layout, why each design choice |
| [`docs/SECURITY.md`](docs/SECURITY.md) | threat model, isolation chain, capability dropping, FAQ |
| [`docs/RUNTIMES.md`](docs/RUNTIMES.md) | handler shapes, what `event` looks like, how to read body / headers |
| [`docs/API.md`](docs/API.md) | full HTTP API reference |
| [`docs/CONFIG.md`](docs/CONFIG.md) | every tunable knob (env vars + `system_config` + `pool_config`) |
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) | production setup, TLS, backups, log rotation, upgrades |
| [`docs/OPERATIONS.md`](docs/OPERATIONS.md) | day-2: monitoring, troubleshooting, common errors |
| [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) | dev environment, running tests, code conventions |
| [`docs/ERRORS.md`](docs/ERRORS.md) | error code catalog (HTTP status → meaning → action) |
| [`docs/CAPACITY.md`](docs/CAPACITY.md) | measured throughput numbers + benchmark methodology |

## Build from source

```bash
make build-all     # frontend + backend → ./build/orva
make dev           # frontend on :5173, backend on :8443
make test          # go test ./...
```

## License

Apache-2.0
