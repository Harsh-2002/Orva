# Orva

Self-hosted Function-as-a-Service (FaaS) for homelab and on-premises use. Users write JavaScript (Node.js 24), Python (3.14), or TypeScript functions — two generic runtimes, `node` and `python`, latest-stable only; Orva deploys them into nsjail sandboxes and exposes them over HTTP with a built-in dashboard, CLI, MCP server, and an in-product AI chat assistant (the **Chat** sidebar item, route `/web/ai`) that operates the instance end-to-end via in-process tool calling (BYO provider keys, embedded Bifrost gateway).

> **Operational contract:** read [`CONTRACT.md`](CONTRACT.md) **before proposing changes** — the canonical commands, build/CI invariants, ports, and must-not-break rules. Each directory's `CLAUDE.md` (mirrored as `AGENTS.md` so Codex/opencode read it too) covers how that subsystem works.
>
> **Docs ship with the code that changed them.** If your change alters anything an
> operator or a function author can observe, update the affected docs **in the same
> commit** — [`CONTRACT.md` §6a](CONTRACT.md) has the table of what to touch for what.
> This is not tidiness: a 2026-08-25 audit of every document against the source found
> 181 defects, 81 factually wrong, including a handler contract whose two headline
> examples produced an HTTP 500 and a silently wrong answer. It accumulated one
> un-updated commit at a time. Note that `frontend/src/utils/aiPrompts.js` counts as
> documentation — it is the prompt the in-product assistant generates code from, so a
> stale claim there ships as broken code rather than a stale sentence.

@CONTRACT.md

## Quick Start

```bash
# Docker (recommended)
docker compose up -d
# → dashboard at http://localhost:3000  (compose maps host 3000 → container 8443)

# Dev mode (frontend hot-reload + backend foreground process)
make dev
```

## Build Commands

```bash
make build          # backend binary → build/orva  (calls adapters-embed + docs-embed)
make build-all      # embed UI then build           (full release artifact)
make test           # go test -count=1 ./...  (from repo root)
make lint           # go vet ./...  (from repo root)
make ui             # cd frontend && npm install && npm run build
make embed          # build UI, copy dist/ → backend/internal/server/ui_dist/
make cli            # static CLI binary → build/orva (current OS)
make cli-all        # cross-compile CLI: linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/{amd64,arm64}
make adapters-embed # sync runtimes/ → backend/cmd/orva/adapters/ (auto-called by build)
make docs-embed     # sync docs/reference.md → mcp + frontend (auto-called by build/ui)
make clean          # remove build/ and embedded artefacts
```

## Repo Layout

```
go.mod, go.sum    Single Go module rooted at the repo (covers backend/ + cli/ + internal/)
backend/          Go server (see backend/CLAUDE.md)
  cmd/orva/       Server entry: registers commands.NewRoot() + serve/setup
  internal/       Server packages (config, database, pool, proxy, mcp, …)
  runtimes/       Runtime adapter source: node, python
cli/              Slim standalone CLI codebase (see cli/CLAUDE.md)
  cmd/orva/       Slim CLI entry point (no server packages — ~20 MB binary)
  commands/       Cobra subcommand library — single source of truth for
                  both binaries (server imports it for its CLI surface)
internal/         Shared utilities accessible to both backend/ and cli/
  client/         HTTP client + ~/.orva/config.yaml loader
  ids/            UUIDv7 generator
frontend/         Vue 3 dashboard (see frontend/CLAUDE.md)
docs/             Operator and developer documentation (see docs/CLAUDE.md)
scripts/          Installers (install.sh = server, install-cli.{sh,ps1} = CLI),
                  Docker entrypoint (entrypoint.sh); the systemd + OpenRC units
                  are emitted inline by install.sh, not separate files
test/             Shell-based integration test suite (see test/CLAUDE.md)
  cli/            CLI-specific tests (build matrix, install-cli, upgrade, command-tree)
  install/        Server-install e2e harness (privileged systemd-in-docker)
Makefile          All build/test/release targets
docker-compose.yml  Single-node Docker deployment
Dockerfile        Multi-stage image (dev and production — single file)
```

## Data & Configuration

- **Data dir**: `/var/lib/orva` (Docker volume `orva-data`) — contains `orva.db` (SQLite WAL) and `functions/<id>/versions/`
- **Server config**: environment variables only; full reference in `docs/CONFIG.md`
- **CLI config**: `~/.orva/config.yaml` with `endpoint` and `api_key`

## Release Policy

**Two-stage policy: verify on push/PR, ship on tag.** The release workflow does
**no testing** — it gates on the tagged commit's checks already being green, then builds and
publishes. All verification lives in one consolidated `CI` workflow (`.github/workflows/ci.yml`):
workflow lint, shellcheck, go vet/test/build, UI lint/build, dependency audit, a running-container
smoke test, plus the full E2E suites (source API/sandbox, CLI cross-build/installers, bare-metal
server installers, native runtime).

**A green PR is not a green `main`.** Jobs are path-filtered on pull requests, so a PR that
touches no `cli/` or installer files never runs the CLI matrix, the six server installers or
the native-engine job — they run on the `main` push instead. The same asymmetry applies to
CodeQL: PR #51 reported **0** results and the `main` scan of the identical tree opened four
alerts, because default setup analyses PRs diff-informed and never re-evaluated a file the PR
had not touched. Watch the merge commit, not just the PR. `main` pushes run everything.

`CI` also owns exact downloaded installer/asset validation after publication (its `artifacts`
suite).

There are exactly two workflows, and no others should be added without good reason:
`ci.yml` (all testing and validation) and `release.yml` (build and publish). Old releases,
tags, and untagged GHCR manifests are intentionally **not** pruned automatically — prune them
by hand from the Releases page / GHCR package settings if they ever accumulate enough to matter.

GitHub-hosted E2E runs Orva directly on the VM, provisions nsjail plus Node/Python rootfs
trees, and sets `ORVA_REQUIRE_SANDBOX=1`; real deploy/invoke is therefore mandatory and a
sandbox skip fails the gate. Local Docker runs may still skip when their host forbids nested
namespaces. Use the same environment flag on a provisioned target node when validating the
kernel boundary manually.

**One published release at a time.** After a tag ships, the previous releases are deleted, so
the Releases page holds exactly the current version — but **only once `artifacts` has
finished**, never while it is running. That suite's CLI-upgrade leg resolves the previous
release and pulls its assets; pruning mid-run 404s it, which is how v2026.08.26's run went red. `install.sh` / `install-cli.sh` resolve
GitHub's "latest", so the order is not optional: **publish the new release first, then delete
the old ones.** Deleting the current release before its replacement is live opens a window
where "latest" resolves to 404 and every install and upgrade fails.

Two consequences worth knowing. Nothing may pin to a superseded version — an
`ORVA_VERSION=` example in the docs 404s the moment its release is pruned, so pins name the
current release only. And `CI`'s `artifacts` suite has a CLI-upgrade-round-trip leg that
upgrades *from the previous release*; with only one release published it skips cleanly, so a
skip there is expected rather than a regression.

**The tag goes with the release.** Pruning removes both, so exactly one tag exists at any
time. Nothing may depend on an older tag resolving: a `git log <old-tag>..HEAD` range breaks
the moment that tag is pruned, and a `releases/tag/<old>` link 404s. Use the current tag for
ranges, and `CHANGELOG.md` itself as the record for anything older.

The flow:

0. **Update `CHANGELOG.md`** — move `## Unreleased` under the new `vYYYY.MM.DD`
   heading. Breaking changes and upgrade steps must be written there before the
   tag exists; a dated version cannot signal them on its own.
1. **Merge to `main`** and wait for **CI** to go green on the merge commit. This is the
   verification — the release will not re-run it.
2. **Tag today's date and push** (zero-padded `vYYYY.MM.DD`):
   ```bash
   git tag -a v2026.05.03 -m "Orva v2026.05.03" && git push origin v2026.05.03
   ```
   The Release workflow's **`gate`** job confirms `CI` already concluded `success` for
   that exact commit (a status lookup — seconds, not a test run; it polls briefly if you tag
   right after the merge). It **refuses to build** if it is missing or red. On pass it builds
   + publishes `ghcr.io/harsh-2002/orva:latest` (multi-arch), all CLI binaries, rootfs tarballs,
   checksums, and the GitHub Release. Every build job `needs: gate`; a successful Release then
   dispatches released-artifact CI.
   *Emergency only:* `workflow_dispatch` with `force=true` skips the gate, but still
   checks out and builds the exact stable date tag supplied to the workflow.
3. **On release publish**, dispatch `CI`'s `artifacts` suite against the
   freshly-published CLI + server assets (a `GITHUB_TOKEN`-created release does not auto-fire
   downstream workflows, so Release calls `gh workflow run ci.yml -f suite=artifacts`). The CLI
   upgrade leg uses the previous release when present and skips cleanly when there is none.
   That green `artifacts` run is the end of the pipeline — nothing runs after it.

### Build-time identity

Every server binary stamps three variables via `-X` ldflags at link time. They flow Makefile → Dockerfile → release.yml and surface at `/api/v1/system/health` + Settings → Build info in the dashboard.

| Variable | Source | Example |
|---|---|---|
| `backend/internal/version.Version`   | git tag on release; `git describe` in dev   | `v2026.06.14` |
| `backend/internal/version.Commit`    | `git rev-parse --short HEAD` (CI: `${GITHUB_SHA::7}`) | `1be3399` |
| `backend/internal/version.BuildTime` | `date -u +%Y-%m-%dT%H:%M:%SZ` at link time   | `2026-05-15T14:20:34Z` |

Go silently ignores unknown `-X` targets, so renaming the version package or any of its variables MUST be done in lock-step across `Makefile`, `Dockerfile`, and `.github/workflows/release.yml` — otherwise the binary ships with defaults (`"dev"` / `"unknown"`) and the dashboard's Build info card lights up red flags.

## Non-obvious Gotchas

- **`adapters-embed` must run before any `go build`** — it copies `runtimes/` into `backend/cmd/orva/adapters/` for `//go:embed`. `make build` calls it automatically; bare `go build` does not.
- **Full server and slim CLI are separate binaries with one command library** — the Linux
  server build includes `serve` / `setup` plus every client command; `make cli`
  builds the smaller cross-platform client without server packages.
- **UI is embedded** in the Go binary via `//go:embed ui_dist`; `make build` alone reuses the last embedded snapshot. Run `make build-all` (or `make embed` first) to pick up frontend changes.
- **nsjail required on Linux** for sandbox invocations; the server starts without it but every invocation fails until it is installed.
- **Egress policy is per-sandbox and fail-closed** — `internal/firewall` compiles the `egress_blocklist` table into an nsjail NSTUN config generation; every `network_mode: egress` spawn passes it as `--config` (which MUST be argv[0..1]) and refuses to start without one. No host firewall table is ever created.
- **Docs single source:** `docs/reference.md` is the canonical Orva reference markdown. `make docs-embed` ships copies to `backend/internal/mcp/reference.md` (embedded by the `get_orva_docs` MCP tool), `frontend/public/docs.md` (served at `/web/docs.md` — it is a Vite `public/` asset so it lives under the UI base path — and read by the dashboard's Copy as Markdown button), and `cli/commands/reference.md` (embedded into the CLI, served by `orva docs`). All three consumers serve identical bytes.
