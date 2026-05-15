# Orva

Self-hosted Function-as-a-Service (FaaS) for homelab and on-premises use. Users write JavaScript (Node 22/24), Python (3.13/3.14), or TypeScript functions; Orva deploys them into nsjail sandboxes and exposes them over HTTP with a built-in dashboard, CLI, and MCP server.

## Quick Start

```bash
# Docker (recommended)
docker compose up -d
# → dashboard at http://localhost:8443

# Dev mode (frontend hot-reload + backend auto-restart)
make dev
```

## Build Commands

```bash
make build          # backend binary → build/orva  (calls adapters-embed)
make build-all      # embed UI then build           (full release artifact)
make test           # cd backend && go test ./...
make lint           # cd backend && go vet ./...
make ui             # cd frontend && npm install && npm run build
make embed          # build UI, copy dist/ → backend/internal/server/ui_dist/
make cli            # static CLI binary → build/orva-cli (current OS)
make cli-all        # cross-compile CLI: linux/amd64, linux/arm64, darwin/arm64
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
  runtimes/       Runtime adapter source: node22, node24, python313, python314
cli/              Slim standalone CLI codebase (see cli/CLAUDE.md)
  cmd/orva/       Slim CLI entry point (no server packages — ~12 MB binary)
  commands/       Cobra subcommand library — single source of truth for
                  both binaries (server imports it for its CLI surface)
internal/         Shared utilities accessible to both backend/ and cli/
  client/         HTTP client + ~/.orva/config.yaml loader
  ids/            UUIDv7 generator
frontend/         Vue 3 dashboard (see frontend/CLAUDE.md)
docs/             Operator and developer documentation (see docs/CLAUDE.md)
scripts/          Installers (install.sh = server, install-cli.{sh,ps1} = CLI),
                  Docker entrypoint, systemd unit, OpenRC unit
test/             Shell-based integration test suite (see test/CLAUDE.md)
  cli/            CLI-specific tests (build matrix, install-cli, upgrade, command-tree)
  install/        Server-install e2e harness (privileged systemd-in-docker)
Makefile          All build/test/release targets
docker-compose.yml  Single-node Docker deployment
Dockerfile        Multi-stage image (dev and production — single file)
```

## Data & Configuration

- **Data dir**: `/var/lib/orva` (Docker volume `orva_data`) — contains `orva.db` (SQLite WAL) and `functions/<id>/versions/`
- **Server config**: env vars or `/etc/orva/config.yaml`; full reference in `docs/CONFIG.md`
- **CLI config**: `~/.orva/config.yaml` with `endpoint` and `api_key`

## Release Policy

**One active release at a time.** When cutting a release:
1. Delete the existing GitHub release and its tag first
2. Tag as `vYYYY.MM.DD` (today's date, zero-padded) and push — the workflow does the rest

```bash
gh release delete v<old-tag> --yes
git tag -d v<old-tag> && git push origin --delete v<old-tag>
git tag v2026.05.03 && git push origin v2026.05.03
```

The release workflow builds `ghcr.io/harsh-2002/orva:<tag>` + `:latest` (multi-arch), all CLI binaries, rootfs tarballs, and checksums automatically on any `v*` tag push.

### Build-time identity

Every server binary stamps three variables via `-X` ldflags at link time. They flow Makefile → Dockerfile → release.yml and surface at `/api/v1/system/health` + Settings → Build info in the dashboard.

| Variable | Source | Example |
|---|---|---|
| `internal/version.Version`   | git tag on release; `git describe` in dev   | `v2026.05.15` |
| `internal/version.Commit`    | `git rev-parse --short HEAD` (CI: `${GITHUB_SHA::7}`) | `1be3399` |
| `internal/version.BuildTime` | `date -u +%Y-%m-%dT%H:%M:%SZ` at link time   | `2026-05-15T14:20:34Z` |

Go silently ignores unknown `-X` targets, so renaming the version package or any of its variables MUST be done in lock-step across `Makefile`, `Dockerfile`, and `.github/workflows/release.yml` — otherwise the binary ships with defaults (`"dev"` / `"unknown"`) and the dashboard's Build info card lights up red flags.

## Non-obvious Gotchas

- **`adapters-embed` must run before any `go build`** — it copies `runtimes/` into `backend/cmd/orva/adapters/` for `//go:embed`. `make build` calls it automatically; bare `go build` does not.
- **Server and CLI are the same binary** — `orva serve` starts the daemon; every other subcommand is a CLI client. Deploy either as a server or as a standalone CLI (`make cli`).
- **UI is embedded** in the Go binary via `//go:embed ui_dist`; `make build` alone reuses the last embedded snapshot. Run `make build-all` (or `make embed` first) to pick up frontend changes.
- **nsjail required on Linux** for sandbox invocations; the server starts without it but every invocation fails until it is installed.
- **Firewall (nft) probe is lazy** — the nftables package does not probe on import; it probes on first use via `sync.Once`, so CLI invocations do not trigger nft warnings.
- **Docs single source:** `docs/reference.md` is the canonical Orva reference markdown. `make docs-embed` ships copies to `backend/internal/mcp/reference.md` (embedded by the `get_orva_docs` MCP tool) and `frontend/public/docs.md` (served at `/docs.md` and read by the dashboard's Copy as Markdown button). Both consumers serve identical bytes.
