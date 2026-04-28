# Contributing

Orva is in active development. PRs welcome — for bug fixes especially,
and for the items called out as "not yet shipped" in the README.

## Dev setup

```bash
git clone https://github.com/Harsh-2002/Orva.git
cd Orva
make dev    # frontend on :5173 with hot reload, backend on :8443
```

Requires:

- **Go 1.25+**
- **Node 22+**
- **nsjail** on PATH — easy install: `make build-nsjail` (clones
  google/nsjail, builds with apt deps; needs `libprotobuf-dev`,
  `libnl-route-3-dev`, `bison`, `flex`).
- **Linux host** with cgroup v2 + unprivileged user namespaces. Mac /
  Windows: use the Docker image (`docker compose up`); native dev
  isn't supported because nsjail is Linux-only.

Verify your kernel:

```bash
[ "$(cat /proc/sys/kernel/unprivileged_userns_clone)" = "1" ] && echo "userns OK"
mount | grep cgroup2 && echo "cgroup v2 OK"
```

## Code layout

```
backend/
  cmd/orva/         CLI entry (cobra). `orva serve` is the daemon.
  internal/
    builder/        deploy pipeline: extract + install deps + atomic publish
    config/         YAML + env config loader
    database/       SQLite migrations + per-resource queries
    metrics/        in-memory ring buffer + percentile computation
    pool/           per-fn warm worker pool + Knative-KPA autoscaler
    proxy/          HTTP request → sandbox worker bridge
    registry/       in-memory function cache
    sandbox/        nsjail invocation, seccomp policy, host-wide limiter
    secrets/        AES-256-GCM at rest, env injection at spawn
    server/
      events/       SSE pub/sub broker
      handlers/     one file per concern (functions, invoke, secrets, ...)
      handlers/respond/  error envelope + Retry-After helpers
  runtimes/
    node22/adapter.js
    node24/adapter.js
    python313/adapter.py
    python314/adapter.py

frontend/
  src/
    api/            axios client + endpoint helpers
    components/     reusable Vue components (EditorCard, StatusBadge, Drawer)
    stores/         Pinia stores (auth, system, events)
    views/          one file per route (Dashboard, Editor, Functions, ...)

scripts/            install.sh, uninstall.sh, systemd unit, OpenRC
test/               end-to-end shell suites (run with bash test/run-all.sh)
docs/               human-readable docs (this folder)
```

[`docs/ARCHITECTURE.md`](ARCHITECTURE.md) has the deeper component map
+ data-flow diagrams.

## Running tests

```bash
# unit tests (Go)
make test

# integration tests — require a running orvad
docker run -d --name orva-test -p 18443:8443 \
  --cap-add SYS_ADMIN --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined --security-opt systempaths=unconfined \
  -v orva-test-data:/var/lib/orva orva:latest

KEY=$(docker exec orva-test cat /var/lib/orva/.admin-key)
API_KEY=$KEY BASE_URL=http://localhost:18443 ORVA_CONTAINER=orva-test \
  bash test/run-all.sh
```

The umbrella covers:

- `secrets-test.sh` — encrypt/decrypt, pool refresh on secret change
- `routes-test.sh` — exact + prefix matching, method restriction
- `heavy-deploy-test.sh` — `requirements.txt` / `package.json` deploys
- `errors-test.sh` — every Round-F error code is reachable
- `rollback-test.sh` — deploy A, deploy B, rollback to A, roll forward
- `onboarding-flow.sh` — first-run admin creation + login + refresh
- `atscale.sh` — concurrent c=25 hammering for capacity confirmation

## Adding a new error code

1. Define a sentinel in the package that produces the error
   (`internal/pool/`, `internal/sandbox/`, etc.).
2. Add a `case errors.Is(err, ...)` arm to `mapInvokeError` in
   `backend/internal/server/handlers/errmap.go`.
3. Add the code's row to the table in [`docs/ERRORS.md`](ERRORS.md).
4. Add a test case in `test/errors-test.sh` that provokes it.

## Adding a runtime

1. Add the rootfs build target to `Dockerfile` (a new `FROM ... AS rootfs-XXX`
   stage).
2. Write the adapter at `backend/runtimes/<runtime>/adapter.{js,py}`.
3. Add the runtime to the `validRuntimes` map in
   `backend/internal/server/handlers/functions.go`.
4. Update `runtimeIsNode` / `runtimeIsPython` in
   `backend/internal/server/handlers/functions.go`.
5. Update the rootfs build in `release.yml`'s matrix.
6. Add to [`docs/RUNTIMES.md`](RUNTIMES.md).
7. Re-deploy.

## Code conventions

- **Comments**: explain the *why*, not the *what*. Well-named
  identifiers explain *what*. Most files have terse top-of-file
  comments establishing context; per-line narration is a smell.
- **Error wrapping**: `fmt.Errorf("ctx: %w", err)` so `errors.Is` works
  through the chain.
- **Logging**: `slog` with structured kv pairs. Don't `log.Printf`.
- **Concurrency**: prefer channels for ownership transfer; mutexes for
  shared state. Never both for the same resource.
- **Tests**: prefer `subtest` per case (`t.Run`). Use `-race`.
- **Vue**: composition API + `<script setup>`. Pinia for store state.
  No vuex. No options API in new code.

## CI

Two workflows on push:

- **`ci.yml`** — go vet/test/build, frontend build, shell-lint, docker
  smoke build.
- **`install-test.yml`** — runs `scripts/install.sh` (dryrun) in
  containers for ubuntu, debian, alpine, rocky, arch on every push.

A third runs on `v*` tag push:

- **`release.yml`** — full multi-arch build matrix, publishes a
  GitHub Release with static binaries + rootfs tarballs + multi-arch
  Docker image to `ghcr.io/harsh-2002/orva`.

## Releasing

Tag the new version (CalVer: `vYYYY.MM.DD`):

```bash
git tag -a v$(date -u +%Y.%m.%d) -m "Release v$(date -u +%Y.%m.%d)"
git push origin v$(date -u +%Y.%m.%d)
```

The release workflow handles everything else. Take ~15 minutes to
finish (the slowest leg is the arm64 rootfs builds on `ubuntu-24.04-arm`
runners).

## Filing issues

When something's broken, include:

- Orva version (`orva --version`)
- Output of `curl http://<host>:8443/api/v1/system/metrics.json`
- Distro + kernel: `uname -a` and `cat /etc/os-release`
- For deploy issues: the deployment ID + the build log
  (`/api/v1/deployments/<id>/logs`)
- For invoke issues: the execution ID + its stderr
  (`/api/v1/executions/<id>/logs`)

The `OPERATIONS.md` runbook has the curl recipes for each.

## Style: writing user-facing copy

The dashboard, error messages, and docs share a tone:

- **Direct.** "rollback to the version" not "restore to the
  previously-deployed iteration."
- **Honest.** "Active development. Not for production yet." not
  "Enterprise-ready serverless platform."
- **Specific.** "p50 ~500ms at c=500 on 2 CPUs" not "blazing fast."

The README and About description set the tone; new copy should match.
