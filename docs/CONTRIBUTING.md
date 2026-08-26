# Contributing

Orva is in active development. PRs welcome — for bug fixes especially,
and for the items called out as "not yet shipped" in the README.

> The canonical build/test commands, CI gates, ports, and must-not-break
> invariants live in [`../CONTRACT.md`](../CONTRACT.md) (the repo's agent/operator
> contract). This guide is the human-onboarding companion; where they overlap,
> `CONTRACT.md` is the source of truth.

## Dev setup

```bash
git clone https://github.com/Harsh-2002/Orva.git
cd Orva
make dev          # frontend on :5173 with hot reload, backend on :8443
make build-all    # production binary (server + embedded UI) → ./build/orva
make test         # go test ./...
```

All build/dev/test workflows go through the `Makefile` — read it for the full
target list (`build`, `build-all`, `cli`, `cli-all`, `embed`, `adapters-embed`,
`docs-embed`, `test`, `lint`, `ui`, `dev`, `clean`).

Requires:

- **Go 1.26+** (the embedded AI gateway requires it)
- **Node 24+**
- **nsjail** at exactly `/usr/local/bin/nsjail` — the path is hardcoded
  (`config/defaults.go`, pinned by `config_test.go`) and `PATH` is never
  consulted, so installing it elsewhere does not work. Build it from source
  (clone google/nsjail and run
  `make`; needs `libprotobuf-dev`, `libnl-route-3-dev`, `bison`, `flex`),
  or let `scripts/install.sh` fetch the prebuilt release binary. The Docker
  image already ships it at `/usr/local/bin/nsjail`.
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
    config/         environment-variable config loader (no config file)
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
    node/adapter.js
    python/adapter.py

frontend/
  src/
    api/            axios client + endpoint helpers
    components/     reusable Vue components (Button, StatusBadge, Drawer, RuntimeTag)
    stores/         Pinia stores (auth, system, events)
    views/          one file per route (Dashboard, Editor, Functions, ...)

scripts/            installers + entrypoint; install.sh emits service units/uninstaller
test/               end-to-end shell suites (run with bash test/run-all.sh)
docs/               human-readable docs (this folder)
```

[`docs/ARCHITECTURE.md`](ARCHITECTURE.md) has the deeper component map
+ data-flow diagrams.

## Running tests

[`TESTING.md`](TESTING.md) is the full end-to-end verification guide — which
layer answers which question, the real user journeys with expected output, what
should fail, how to triage a red run, and which harnesses here are currently
stale or destructive. Read it before trusting a green suite.

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
- `errors-test.sh` — every error code is reachable and returns the right slug
- `rollback-test.sh` — deploy A, deploy B, rollback to A, roll forward
- `onboarding-flow.sh` — first-run admin creation + login + refresh
- `egress-test.sh` — outbound HTTPS reachable in egress mode, blocked in none
- `auth-test.sh` — per-function `auth_mode` (none / platform_key / signed HMAC)
  and `rate_limit_per_min`. It does not touch API-key scopes, sessions or OAuth.
- `tracing-test.sh` — trace IDs propagate across F2F invokes and job enqueues
- `build-cache-test.sh` — the per-function npm/pip cache and its eviction bounds
- `atscale.sh` — concurrent c=25 hammering for capacity confirmation. Needs `hey`,
  and issues no DELETEs: it leaves every function it deploys behind.

## Install + bare-metal lifecycle tests

A separate harness under `test/install/` runs the install script
end-to-end against a privileged systemd-in-docker container per distro,
deploys + invokes a function through the API and the CLI, then verifies
uninstall + reinstall preserves data. Replaces the old dryrun-only
matrix.

```bash
# Single distro (Ubuntu 24, ~6 min including image pull):
bash test/install/run-distro.sh ubuntu24

# Full matrix (sequential, ~35 min):
for d in ubuntu24 debian12 alpine321 rocky9 fedora44 arch; do
  bash test/install/run-distro.sh "$d"
done

# Lighter follow-ups:
bash test/install/failure-modes.sh        # --cli-only + reinstall idempotency
bash test/install/kata-flow.sh            # Kata Containers compat — skipped if the runtime is absent
```

Requires Docker with `--privileged` allowed and `/sys/fs/cgroup`
mountable. Output goes to `test/install/logs/<distro>-*.log`.

The same harness drives the installer jobs in `.github/workflows/ci.yml`.

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

## CI & releasing

The CI/release model — two workflows (`ci.yml` verifies, `release.yml` ships),
verify-on-push/PR + ship-on-`vYYYY.MM.DD`-tag, the `gate` job, the
publish-before-delete "latest 404" rule, and build-time identity stamping — is the
canonical operating contract and lives in one place: [`../CONTRACT.md`](../CONTRACT.md)
(§6 Definition of Done, §7 CI/release model). It is intentionally **not** restated
here so it can't drift. The deeper release-policy walkthrough (tag/gate/artifacts
steps) is in the root [`CLAUDE.md`](../CLAUDE.md).

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
