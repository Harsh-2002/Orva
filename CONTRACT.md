# Orva — agent operating contract

The single, canonical, present-tense operating contract for anyone (human or AI
agent) changing this repo. Read this **before** proposing changes. It is the one
place the load-bearing operational facts live; `CLAUDE.md`/`AGENTS.md` and
`docs/CONTRIBUTING.md` reference it rather than restating it, so it cannot drift.

- **Orientation** (what each subsystem is / how it works): the per-directory
  `CLAUDE.md` (root + `backend/`, `cli/`, `frontend/`, `docs/`, `scripts/`,
  `test/`, `test/e2e/`). Each is mirrored by an `AGENTS.md` symlink so
  Codex/opencode read it too.
- **This file** (`CONTRACT.md`): the mechanical rules that make a change land green.
- **Human onboarding / PR etiquette**: `docs/CONTRIBUTING.md`.

> All commands run from the **repo root** unless stated otherwise. This repo uses
> tracked symlinks (`AGENTS.md` → `CLAUDE.md`); on Windows, clone with
> `git clone -c core.symlinks=true …` or the mirrors materialize as plain text.

## 1. Toolchain (pinned, lock-step)

- **Go 1.26+** (the embedded Bifrost AI gateway requires it), **Node.js 24**,
  **Python 3.14**.
- **nsjail** at the hardcoded path `/usr/local/bin/nsjail` (Linux only) — `PATH` is
  never consulted and there is no env override (`config/defaults.go` `NsjailBin`,
  pinned by `config_test.go`). The server starts without it, but **every
  function invocation fails until nsjail is installed**. The Docker image ships it
  at `/usr/local/bin/nsjail`.
- One **`go.mod` at the repo root**, module `github.com/Harsh-2002/Orva`, spanning
  `backend/` + `cli/` + `internal/`. The shared `internal/ids` and `internal/client`
  live at **repo-root `internal/`** (NOT `backend/internal/`) because the slim CLI
  imports them.
- Versions are held **lock-step** across `Dockerfile`, `.github/workflows/ci.yml`,
  and `.github/workflows/release.yml` — bump them together.

## 2. Canonical commands

| Command | Does |
|---|---|
| `make build` | server binary → `build/orva` (runs `adapters-embed` + `docs-embed` first) |
| `make build-all` | `embed` (rebuild UI) **then** `build` — full release artifact |
| `make test` | `go test -count=1 ./...` |
| `make lint` | `go vet ./...` |
| `make ui` / `make embed` | build the Vue UI / build UI **and** copy `dist/` → `backend/internal/server/ui_dist/` |
| `make cli` / `make cli-all` | slim CLI (current OS) / cross-compile all 6 release targets |
| `make adapters-embed` | sync `backend/runtimes/` → `backend/cmd/orva/adapters/` (for `//go:embed`) |
| `make docs-embed` | sync `docs/reference.md` → its 3 embedded copies (§4) |
| `make dev` | frontend hot-reload (:5173) + backend foreground |
| `make clean` | remove `build/` + embedded artifacts |

## 3. Build invariants (silent-stale-artifact traps)

- **`adapters-embed` must run before any bare `go build`.** It copies `runtimes/`
  into `backend/cmd/orva/adapters/` for `//go:embed`. `make build` does it; a raw
  `go build ./backend/cmd/orva` does **not**.
- **The UI is embedded via `//go:embed ui_dist`.** `make build` reuses the **last
  embedded snapshot**. To pick up frontend changes you must run `make build-all`
  (or `make embed` first) — `ui_dist/` is committed, so a stale snapshot ships
  silently otherwise.

## 4. Docs single source

`docs/reference.md` is **canonical**. `make docs-embed` copies it **byte-identically**
to three consumers (using `{{ORIGIN}}` placeholders substituted at runtime):

- `backend/internal/mcp/reference.md` — embedded by the `get_orva_docs` MCP tool
- `frontend/public/docs.md` — served at `/web/docs.md` (dashboard "Copy as Markdown").
  It is a Vite `public/` asset, so it ships under the UI base path; `/docs.md` is a 404
- `cli/commands/reference.md` — embedded by `orva docs`

**Edit `docs/reference.md`, then run `make docs-embed`. Never edit a copy directly.**
(The dashboard's rendered Docs page `frontend/src/views/Docs.vue` is a separate
hand-maintained view — update it alongside if content changes.)

## 5. Ports & data

| Context | Port |
|---|---|
| Server (`orva serve`) | **8443** |
| Docker compose host map | **3000 → 8443** |
| Frontend dev server | **5173** |
| Shell tests (`test/`) default `BASE_URL` | **18443** |
| Python E2E (`test/e2e/`) | **8443** in-container, published on host **8455** (`ORVA_E2E_PORT`, `env.py`) |

- **Data dir** `/var/lib/orva` (env `ORVA_DATA_DIR`) — `orva.db` (SQLite WAL) +
  `functions/<id>/versions/`.
- **Server config is environment variables only** (`docs/CONFIG.md`); there is no
  server config file. **CLI config** is `~/.orva/config.yaml` (`endpoint` + `api_key`).

## 6. Definition of Done (minimum green)

- **Fast, host-only** (always run before proposing a change):
  `make lint` && `make test`.
- **Authoritative** (the CI gate): the full E2E suite `test/e2e/run.py` (isolated
  Docker; needs a built `build/orva`). It requires **Docker + nsjail +
  `ORVA_REQUIRE_SANDBOX=1`** on a provisioned Linux host, where **real deploy/invoke
  is mandatory and a sandbox skip is a hard failure**. If you cannot run it locally,
  say so — do not claim a change is verified on `make test` alone.
- **Supplementary, NOT gated**: `test/run-all.sh` and its members, run against a
  live instance. No CI job executes *those* — several mutate or litter the
  instance they run against, so point them at a scratch instance rather than one
  you care about. Note this does **not** mean "CI only shellchecks `test/`":
  `ci.yml` executes `test/sdk-test.sh` inside the gated E2E job
  (`ORVA_REQUIRE_SANDBOX=1`), plus `test/cli/{build-matrix,command-tree,install-cli-test,upgrade-test}.sh`
  and `test/install/{run-distro,native-engine}.sh`. `docs/TESTING.md` marks each
  harness CI-gated or not; trust that table.
- **How to actually do it:** [`docs/TESTING.md`](docs/TESTING.md) — bring-up, what each
  testing layer can and cannot prove, expected output per journey, and triage.
- **Docs move with the change** — see §6a. A change that alters documented
  behaviour is not done until the docs say the new thing.

## 6a. Documentation is part of the change, not a follow-up

**If your change alters anything an operator or function author can observe,
update the docs in the same commit.** Not the next PR, not a cleanup pass — the
same commit, so the two can never be separately reviewable and separately
forgotten.

This rule exists because it was missing. An audit on 2026-08-25 compared every
document against the source and found **181 defects, 81 of them factually
wrong** — including a handler contract that described `event.body` as parsed
JSON when it has always been a raw string, which made the two headline examples
in the canonical reference produce an HTTP 500 (Python) and a silently wrong
answer (Node). None of it was one careless commit. All of it was code moving
and docs not.

**What to update, by what you touched:**

| You changed | Update |
|---|---|
| an HTTP route, its request/response shape, or its status codes | `docs/API.md` **and** `docs/reference.md` |
| the handler contract, event shape, or an SDK method | `docs/reference.md`, `docs/RUNTIMES.md`, `frontend/src/views/Docs.vue`, **and** `frontend/src/utils/aiPrompts.js` |
| a CLI command, flag, or output shape | `docs/CLI.md` + the command's own help string |
| an env var or a default | `docs/CONFIG.md` |
| an error code or its meaning | `docs/ERRORS.md` |
| a sandbox boundary, credential, or revocation path | `docs/SECURITY.md` |
| an operational procedure, or anything an operator recovers with | `docs/OPERATIONS.md`, `docs/DEPLOYMENT.md` |
| an MCP tool's name, arguments, or description | `docs/reference.md` (the tool descriptions are themselves documentation) |
| how a subsystem works | that directory's `CLAUDE.md` |
| anything an operator must do differently after upgrading | `CHANGELOG.md` — **Breaking** or **Upgrade notes** (§7) |

**Two traps specific to this repo:**

- **`frontend/src/utils/aiPrompts.js` is documentation that executes.** It is the
  prompt handed to the in-product AI assistant, so a stale claim there is not a
  stale sentence — it is generated code that does not run. It duplicates the
  handler contract in `docs/reference.md`; both must change together.
- **`docs/reference.md` has three embedded copies** (§4) and a separate
  hand-maintained rendered page at `frontend/src/views/Docs.vue`. Edit the
  canonical file, run `make docs-embed`, and update `Docs.vue` by hand.

**Verify, do not assume.** A doc example is a claim about behaviour: run it. The
audit's worst findings were all in examples that read perfectly well and had
never been executed.

## 7. CI / release model

- **Exactly two workflows**, and no others should be added without good reason:
  `ci.yml` (**all** verification) and `release.yml` (**build + publish only, no
  testing**).
- **Two-stage: verify on push/PR, ship on tag.** `ci.yml` runs on PRs and every push
  to `main` (docs-only changes skip only the fast lint/go/ui/docker jobs).
- **Move `CHANGELOG.md`'s `## Unreleased` section under the new version heading
  before tagging.** Releases are dated, not semver, so "breaking" is never
  implied by the version — it has to be written down. Anything an operator must
  do differently, or that will 403/behave differently after upgrading, belongs
  in **Breaking** or **Upgrade notes**.
- **Ship on `vYYYY.MM.DD` tag** (zero-padded). `release.yml`'s `gate` job confirms
  `CI` already concluded `success` for that exact commit (a status lookup, not a
  re-run), then builds + publishes. It refuses to build on missing/red CI.
- Releases resolve GitHub's "latest": if you delete an old release by hand, **publish
  the new one first** or "latest" 404s.
- **Build-time identity** — `version.Version/Commit/BuildTime` via `-X` ldflags flow
  **Makefile → Dockerfile → release.yml**. Go silently ignores unknown `-X` targets,
  so renaming the `version` package or its vars must be done in **all three** or the
  binary ships `dev`/`unknown`.

## 8. CLI hard constraints (`cli/`)

- **No `backend/internal/…` imports** (keeps the slim CLI slim — lift shared code to
  repo-root `internal/` first, like `internal/ids`, `internal/client`).
- **No CGO** — all binaries are pure-Go static (must run on a fresh Alpine).
- **No `os.Exit` in subcommand bodies** — use `RunE` and return errors.
- New top-level commands **must** be registered in `RegisterClient` (`cli/commands/root.go`),
  assigned a group, and added to `cli/commands/commands_test.go`
  (`TestCommandTree` + `TestRequiredFlagsPresent`) — or the command-tree golden diff fails.

## 9. Must-not-break invariants

- **SQLite migrations are additive-only** (`CREATE TABLE IF NOT EXISTS` + idempotent
  `ALTER`), run on every boot. No `DROP`/destructive migrations. **One carve-out:**
  `migrate_to_uuidv7.go` rewrites primary keys and renames
  `<dataDir>/functions/<id>/` to match. Its child columns are derived from
  `PRAGMA foreign_key_list` — never hand-list them, that is how
  `channel_functions` came to be missing — and the functions old→new map is
  committed in the same transaction so `ReconcileFunctionDirs` can finish or
  resume the rename. `serve.go` treats a rename failure as fatal, and the build
  GC refuses to sweep orphans while one is outstanding: without the rename every
  function fails to spawn and the GC deletes the operator's only copy of their
  source.
- `execution_requests`, `user_spans` and `execution_log_entries` have **no FK** to
  `executions` (intentional, for async insert ordering). The cascade is manual, via
  the single `executionChildTables` list, used by **both** `DeleteExecution` and
  `PurgeOldExecutions`. Missing two of them is what let retention leave the two
  fastest-growing tables growing.
- **`entrypoint` is the file the operator authored and the build pipeline never
  writes it.** A compiling runtime records its output in `run_entrypoint`
  instead; empty means "same as `entrypoint`". It used to carry both meanings --
  `tsc` stamped `dist/handler.js` over `handler.ts` -- so four readers each grew
  a private heuristic to undo the rewrite, `GetSource` served compiled
  JavaScript to the editor, and re-deploying failed on a path nobody had typed.
  **Rollback derives `run_entrypoint` from the promoted version's own directory
  rather than restoring it from the deployment snapshot**, because a snapshot
  written before the column existed carries no value, and applying that absence
  points a compiled version back at its `.ts` source, which Node cannot execute.
- **nsjail `cmd.Wait()` is centralized in `Spawn`** (via `waitDone`); never call
  `Wait()` on a sandbox `cmd` elsewhere (zombie-nsjail regression).
- The AI agent's `defaultSystemPrompt` (`backend/internal/ai/manager.go`) is a Go
  **raw string — it must stay backtick-free**.
- **One AI turn per conversation** — a keyed try-lock rejects overlapping turns
  (SSE `error` / `409 ErrConversationBusy`); `ai_messages.seq` is assigned atomically
  inside the INSERT.
- **AI conversation edit/delete is destructive-tail**: it truncates the conversation
  at that message's `seq` (deletes it and everything after), then re-runs. No branching.
- `scripts/entrypoint.sh` **overwrites the runtime adapters** from the image on every
  container start (so runtime upgrades roll out even with a persistent volume).
- Installers **SHA-256-verify** downloads against `checksums.txt` with **no fail-open
  path** and no bypass env var.
- The systemd unit **must** carry `RestartForceExitStatus=70`. `orva backup restore`
  exits 70 deliberately to force a restart; with `Restart=on-failure` alone, exit 0
  reads as "finished" and a successful restore leaves the server down.

## 10. Code style

- Go: `slog` with structured kv pairs (never `log.Printf`); wrap errors with
  `fmt.Errorf("ctx: %w", err)` so `errors.Is` works; prefer `t.Run` subtests, use `-race`.
- Vue: Composition API + `<script setup>` only; Pinia for state (no Vuex, no Options API
  in new code).
- Handlers respond via `respond.JSON` / `respond.Error` (`backend/internal/server/handlers/respond/`).
- Comments explain the **why**, not the **what**.
