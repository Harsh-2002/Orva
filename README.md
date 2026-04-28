# Orva

Self-hosted serverless function platform. Deploy Node.js and Python
functions to your own hardware, invoke them over HTTP, get warm-pool
latency without paying for it. nsjail isolation, content-addressed
deploys with one-click rollback, server-sent events for live UI.

## Quick start

### Docker

```bash
docker run -d --name orva -p 8443:8443 \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  -v orva-data:/var/lib/orva \
  ghcr.io/harsh-2002/orva:latest

# or
docker compose up -d
```

### Bare metal

One-line installer — POSIX sh, multi-distro
(Ubuntu, Debian, Alpine, RHEL/Rocky/AlmaLinux, Fedora, Arch, openSUSE):

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sudo sh
```

Pin a specific release:
```bash
ORVA_VERSION=v2026.04.28 sh install.sh
```

The installer downloads a fully-static tarball (no glibc/libstdc++
deps, musl-linked nsjail), drops binaries at `/opt/orva/bin/{orva,nsjail}`,
creates a system user, registers a `systemd` (or `OpenRC` on Alpine)
unit, and prints the bootstrap admin key location. Re-run is safe.

Uninstall: `sudo /opt/orva/share/orva/scripts/uninstall.sh [--purge]`

The container prints a one-time bootstrap admin API key on first boot
(also persisted at `/var/lib/orva/.admin-key` inside the volume). Open
`http://localhost:8443` and complete the onboarding form to create your
admin user; subsequent visits use cookie-based session auth.

## What it does

- **Deploy** Node.js (22, 24) or Python (3.13, 3.14) functions inline
  or as a tarball; dependencies in `package.json` / `requirements.txt`
  install during the build phase.
- **Invoke** at `POST /api/v1/invoke/<fn-id>/<path>` — the path tail is
  passed to your handler as `event.path`. AWS-Lambda-shape handlers and
  HTTP-shape handlers both work.
- **Warm pools** per function with autoscaling (Knative-KPA-style EWMA),
  bounded by host memory + CPU headroom.
- **Rollback** to any prior content-hashed version with one click. The
  `versions/<hash>/` archive is content-addressed; the `current`
  symlink retargets atomically.
- **Custom routes** — map `/webhooks/stripe` to a function so external
  callers don't need the function ID.
- **Secrets** — encrypted at rest, injected as env vars at sandbox
  spawn time.
- **Live dashboard** — system metrics, invocation log, deployment
  status all stream over a single SSE connection.

## Architecture

```
┌─ orvad (Go)                    HTTP + SSE on :8443
│   ├─ async build queue          extract → install deps → atomic publish
│   ├─ warm worker pools          per-function, autoscaled
│   ├─ event hub                  metrics / execution / deployment fan-out
│   └─ SQLite                     functions, deployments, executions, secrets
│
└─ nsjail per invocation          chroot + user namespace + cgroup v2 + seccomp
        └─ user code              /code (read-only) + private /tmp
```

See [docs/SECURITY.md](docs/SECURITY.md) for the isolation model in
detail and [docs/CAPACITY.md](docs/CAPACITY.md) for measured throughput
on a 2-CPU box (881 req/s aggregate at c=500, 65% success rate before
graceful 429 backpressure kicks in).

## API surface

| endpoint | purpose |
|---|---|
| `POST /auth/onboard` | first-run admin user creation |
| `POST /auth/login` | session login |
| `POST /api/v1/functions` | create a function record |
| `POST /api/v1/functions/{id}/deploy-inline` | deploy from a JSON body |
| `POST /api/v1/functions/{id}/deploy` | deploy from a tarball |
| `POST /api/v1/functions/{id}/rollback` | restore a prior version |
| `GET /api/v1/functions/{id}/deployments` | version history |
| `POST /api/v1/invoke/{id}/...` | invoke a function |
| `GET /api/v1/system/metrics.json` | current snapshot (curl-friendly) |
| `GET /api/v1/events` | SSE stream of metrics + executions + deployments |
| `GET /api/v1/keys` / `POST /api/v1/keys` | API key management |
| `POST /api/v1/routes` | custom URL → function mappings |
| `GET /api/v1/functions/{id}/secrets` / `POST` / `DELETE` | per-fn secrets |

Error responses follow a structured catalog — see
[docs/ERRORS.md](docs/ERRORS.md). Every transient failure carries
`code`, `hint`, optional `retry_after_s`, and optional `details`.

## Project layout

```
cmd/orva/               CLI entrypoint (cobra) — `orva serve` is the daemon
internal/builder/       extract + dep install + atomic publish + version GC
internal/database/      SQLite schema + queries (functions, deploys, etc)
internal/pool/          warm worker pools, autoscaler, host memory budget
internal/proxy/         request → sandbox bridge
internal/sandbox/       nsjail invocation, seccomp policy, concurrency limiter
internal/server/        HTTP routes, middleware, handlers, SSE event hub
runtimes/               adapter.js / adapter.py per supported runtime
ui/                     Vue 3 + Pinia frontend
test/                   end-to-end shell suites (run with bash test/run-all.sh)
docs/                   SECURITY, ERRORS, CAPACITY
```

## Building from source

```bash
make build           # build the Go binary + UI bundle
make test            # go test ./...
docker build -t orva .   # production image (multi-stage)
```

End-to-end test suite (requires a running container):

```bash
docker run -d --name orva-test -p 18443:8443 \
  --cap-add SYS_ADMIN --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined --security-opt systempaths=unconfined \
  -v orva-test-data:/var/lib/orva orva:latest

KEY=$(docker exec orva-test cat /var/lib/orva/.admin-key)
API_KEY=$KEY BASE_URL=http://localhost:18443 ORVA_CONTAINER=orva-test \
  bash test/run-all.sh
```

The umbrella covers secrets injection, custom routes, heavy-dep
deploys, onboarding flow, error envelope coverage, rollback, and
concurrent-load capacity (~700 req/s aggregate on a 2-CPU box).

## Operational notes

- **Volume** — `/var/lib/orva` holds the SQLite DB, the version archive
  (`functions/<id>/versions/<hash>/`), and the runtime rootfs trees.
  Use a named Docker volume; `down -v` will wipe everything including
  your admin user.
- **Bootstrap key** — printed once at first boot, also at
  `/var/lib/orva/.admin-key` (mode 0600). Treat with the same trust
  level as the SQLite file.
- **Disk usage** — each archived version retains its `node_modules` /
  `site-packages`. Default retention is 5 versions per function, GC
  every 5 minutes; tune via `system_config.versions_to_keep` and
  `gc_interval_seconds`.
- **HTTPS** — terminate TLS in a reverse proxy (caddy, nginx, traefik).
  The browser Clipboard API requires either HTTPS or `localhost`, so
  copy-to-clipboard buttons silently fail when accessed via plain HTTP
  from a LAN IP.
- **Resource ceilings** — `cfg.Sandbox.MaxConcurrent` caps host-wide
  invocation parallelism (defaults documented in `internal/config/`).
  When exceeded, requests get `429 TOO_MANY_REQUESTS` with a
  `Retry-After` header rather than queuing forever.

## Status

In active development. The feature surface is stable; the wire
protocol is additive (we do not rename error codes or remove fields).
Production deployments behind a reverse proxy on a single host are
the well-tested path. Multi-node clustering, multi-arch images, and a
bare-metal install script are not yet shipped.

## License

Apache-2.0 — see [LICENSE](LICENSE).
