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
- **Invoke** at `POST /api/v1/invoke/<fn-id>/<path>`. The path tail
  reaches your handler as `event.path`. AWS-Lambda-style and HTTP-style
  handlers both work.
- **Roll back** to any prior content-hashed version in one click.
  Each deploy is archived; rollback is an atomic symlink retarget.
- **Live dashboard** — every invocation, deploy, and metric streams
  over a single SSE connection. No polling.
- **Per-function secrets**, encrypted at rest, injected as env vars
  at sandbox spawn time.
- **Custom routes** — map `/webhooks/stripe` to a function so external
  callers don't need the function ID.

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

## Layout

```
backend/    Go server, sandbox runtime, SQLite, build queue
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
