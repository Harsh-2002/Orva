# Orva

Self-hosted Function-as-a-Service (FaaS) for homelab and on-premises use. Users write JavaScript (Node.js 24), Python (3.14), or TypeScript functions — two generic runtimes, `node` and `python`, latest-stable only; Orva deploys them into nsjail sandboxes and exposes them over HTTP with a built-in dashboard, CLI, MCP server, and an in-product AI chat assistant (the **AI** sidebar section) that operates the instance end-to-end via in-process tool calling (BYO provider keys, embedded Bifrost gateway).

## Quick Start

```bash
# Docker (recommended)
docker compose up -d
# → dashboard at http://localhost:3000  (compose maps host 3000 → container 8443)

# Dev mode (frontend hot-reload + backend auto-restart)
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
  cmd/orva/       Server entry: registers commands.NewRoot() + serve/setup/init
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
- **Server config**: env vars or `/etc/orva/config.yaml`; full reference in `docs/CONFIG.md`
- **CLI config**: `~/.orva/config.yaml` with `endpoint` and `api_key`

## Release Policy

**Two single-purpose pipelines: verify on push/PR, ship on tag.** The release pipeline does
**no testing** — it gates on the tagged commit's checks already being green, then builds and
publishes. All verification (`ci` = shellcheck + go vet/test/build + ui build + docker smoke,
and `e2e` = full programmatic suite) runs on every PR and on the push to `main`; `cli-e2e` /
`install-e2e` cover the CLI + bare-metal install (on PR for source, on `release:published`
against the real artifacts).

**One active release at a time** — never delete the old release *before* the new one is live
(that opens a window where `install.sh`/`install-cli.sh` resolve "latest" → 404). The flow:

1. **Merge to `main`** and wait for **CI** + **e2e** to go green on the merge commit. This is the
   verification — the release will not re-run it.
2. **Tag today's date and push** (zero-padded `vYYYY.MM.DD`):
   ```bash
   git tag -a v2026.05.03 -m "Orva v2026.05.03" && git push origin v2026.05.03
   ```
   The Release workflow's **`gate`** job confirms `CI` + `e2e` already concluded `success` for
   that exact commit (a status lookup — seconds, not a test run; it polls briefly if you tag
   right after the merge). It **refuses to build** if either is missing or red. On pass it builds
   + publishes `ghcr.io/harsh-2002/orva:latest` (multi-arch), all CLI binaries, rootfs tarballs,
   checksums, and the GitHub Release, then prunes ghcr. Every build job `needs: gate`.
   *Emergency/rc only:* `workflow_dispatch` with `force=true` skips the gate.
3. **On release publish**, dispatch `install-e2e` + `cli-e2e` against the freshly-published
   artifacts (a `GITHUB_TOKEN`-created release does not auto-fire downstream workflows, so trigger
   them with `gh workflow run`). The `cli-e2e` upgrade-leg upgrades from the *previous* release's
   binary, so it stays red until the next release makes this one the baseline — expected.
4. **After** the new release is confirmed live, prune the previous one — last, not first:
   ```bash
   gh release delete v<old-tag> --yes --cleanup-tag   # removes the release + its tag
   ```

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
- **Server and CLI are the same binary** — `orva serve` starts the daemon; every other subcommand is a CLI client. Deploy either as a server or as a standalone CLI (`make cli`).
- **UI is embedded** in the Go binary via `//go:embed ui_dist`; `make build` alone reuses the last embedded snapshot. Run `make build-all` (or `make embed` first) to pick up frontend changes.
- **nsjail required on Linux** for sandbox invocations; the server starts without it but every invocation fails until it is installed.
- **Firewall (nft) probe is lazy** — the nftables package does not probe on import; it probes on first use via `sync.Once`, so CLI invocations do not trigger nft warnings.
- **Docs single source:** `docs/reference.md` is the canonical Orva reference markdown. `make docs-embed` ships copies to `backend/internal/mcp/reference.md` (embedded by the `get_orva_docs` MCP tool), `frontend/public/docs.md` (served at `/docs.md` and read by the dashboard's Copy as Markdown button), and `cli/commands/reference.md` (embedded into the CLI, served by `orva docs`). All three consumers serve identical bytes.
