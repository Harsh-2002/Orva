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
make clean          # remove build/ and embedded artefacts
```

## Repo Layout

```
backend/          Go server + CLI (same binary — see backend/CLAUDE.md)
  cmd/orva/       Cobra entry point and all CLI/server commands
  internal/       Go packages (config, database, pool, proxy, mcp, …)
  runtimes/       Runtime adapter source: node22, node24, python313, python314
frontend/         Vue 3 dashboard (see frontend/CLAUDE.md)
docs/             Operator and developer documentation (see docs/CLAUDE.md)
scripts/          Docker entrypoint, bare-metal installer, systemd unit
test/             Shell-based integration test suite (see test/CLAUDE.md)
Makefile          All build/test/release targets
docker-compose.yml  Single-node Docker deployment
Dockerfile        Multi-stage production image
Dockerfile.release  Release-variant image
```

## Data & Configuration

- **Data dir**: `/var/lib/orva` (Docker volume `orva_data`) — contains `orva.db` (SQLite WAL) and `functions/<id>/versions/`
- **Server config**: env vars or `/etc/orva/config.yaml`; full reference in `docs/CONFIG.md`
- **CLI config**: `~/.orva/config.yaml` with `endpoint` and `api_key`

## Non-obvious Gotchas

- **`adapters-embed` must run before any `go build`** — it copies `runtimes/` into `backend/cmd/orva/adapters/` for `//go:embed`. `make build` calls it automatically; bare `go build` does not.
- **Server and CLI are the same binary** — `orva serve` starts the daemon; every other subcommand is a CLI client. Deploy either as a server or as a standalone CLI (`make cli`).
- **UI is embedded** in the Go binary via `//go:embed ui_dist`; `make build` alone reuses the last embedded snapshot. Run `make build-all` (or `make embed` first) to pick up frontend changes.
- **nsjail required on Linux** for sandbox invocations; the server starts without it but every invocation fails until it is installed.
- **Firewall (nft) probe is lazy** — the nftables package does not probe on import; it probes on first use via `sync.Once`, so CLI invocations do not trigger nft warnings.
