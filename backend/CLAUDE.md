# Backend

Go 1.25. Module: `github.com/Harsh-2002/Orva`. SQLite with no CGO (`modernc.org/sqlite`).

## Build & Test

```bash
# From repo root (recommended — handles adapters-embed):
make build          # → build/orva
make test           # cd backend && go test ./...
make lint           # cd backend && go vet ./...

# From backend/ (only if adapters/ is already synced):
go build ./cmd/orva
go test ./...
go vet ./...
```

## Package Layout (`internal/`)

| Package | Purpose |
|---|---|
| `config` | Config struct; env var + YAML loading |
| `database` | SQLite schema, migrations, all CRUD helpers |
| `registry` | In-memory function registry wrapping DB |
| `builder` | Deploy pipeline: tarball → `npm install` / `pip install` → optional `tsc` → register |
| `pool` | Warm-sandbox pool manager (`pool.Manager`) per function |
| `sandbox` | nsjail process lifecycle; `Worker` type with `Dispatch`/`DispatchEx` |
| `proxy` | HTTP → sandbox bridge; request capture (A3); streaming write-loop (C1) |
| `metrics` | Prometheus-text counters + histograms (no external deps, atomic ops) |
| `secrets` | AES-256-GCM encrypted secrets per function |
| `scheduler` | Cron runner (`robfig/cron/v3`) |
| `mcp` | MCP server (go-sdk); 70 operator-management tools OR channel-mode (one tool per bundled function, invoke-only). Auth accepts API keys, OAuth 2.1 access tokens, OR channel tokens. |
| `oauth` | OAuth 2.1 authorization server (RFC 7591 DCR + RFC 8414 metadata + PKCE S256 + RFC 8707 resource indicators + RFC 7009 revocation). Lets claude.ai/ChatGPT add `/mcp` as a custom connector via the browser. Connected apps + sessions managed at `/api/v1/oauth/connected-apps` and `/api/v1/auth/sessions` and surfaced in the dashboard's Settings page. DCR default scope is `read invoke write admin`. |
| `auth` | Shared `Principal` type (Kind=api_key / oauth / channel + ID/Label/Perms/Channel). Both REST middleware and MCP auth resolve the inbound bearer to a `*Principal`; downstream code (activity log, MCP tool registration) consumes the Kind directly. |
| `ids` | Single canonical UUIDv7 generator (RFC 9562 §5.7). Storage IDs across every table. Plaintext bearer tokens stay `crypto/rand` — UUIDv7 leaks creation time. |
| `urlhint` | Per-request `BaseURL(r)` helper. One source of truth for OAuth issuer URLs, MCP `invoke_url` fields, and audience-bound token validation. |

**Agent channels** — bundle N functions under a name + a static bearer token; presenting that token at `/mcp` exposes ONLY those functions as MCP tools (snake_case names, invoke-only). Operator-managed at `/api/v1/channels` (`Channels` page in the dashboard). Token format: `orva_chn_<32 hex>`. Channel tokens are explicitly rejected with 401 at `/api/v1/*` — they have no Orva-management authority.
| `firewall` | nftables outbound allow-list per function (lazy `sync.Once` probe) |
| `server` | HTTP router + middleware chain + all handlers |
| `server/events` | SSE event hub + outbound webhook fanout |
| `server/handlers` | One file per resource group; `respond/` sub-package |
| `cli` | Shared `Client` + `Config` for CLI subcommands |
| `backup` | `SnapshotDB` / `ArchiveTo` / `RestoreFrom` helpers |
| `version` | Single source of truth for the version string |

## CLI Commands (`cmd/orva/`)

All Cobra subcommands share one binary with the server. `orva serve` starts the daemon; every other command is a CLI client that reads `~/.orva/config.yaml`.

Key files: `deploy.go`, `functions.go`, `invoke.go`, `logs.go`, `cron.go`, `kv.go`, `jobs.go`, `secrets.go`, `webhooks.go`, `routes.go`, `keys.go`, `system.go`, `activity.go`, `completion.go`.

## Key Patterns

**Handler responses**: always use `respond.JSON(w, status, val)` / `respond.Error(w, status, "SLUG", "message")` from `server/handlers/respond/`.

**Invocation funnel**: HTTP, cron, jobs, and F2F calls all go through `Worker.Dispatch()` (sync response) or `Worker.DispatchEx()` (multi-frame streaming). Never invoke nsjail directly from handlers.

**Async DB writes**: execution rows use `database.AsyncInsertExecution*` batch writers — no synchronous DB calls on the hot proxy path.

**Name resolution**: functions can be referenced by UUID or by name. Use `resolveFnID(db, nameOrID)` from `handlers/functions_helpers.go`.

**Streaming wire protocol**: `response_start` → `chunk` (base64 body data) → `response_end` frames over the worker's stdin/stdout pipe. `proxy.Forward()` owns the write-loop.

## Middleware Chain

`CORS → BodySizeLimit → Auth → RequestID → Logger → Handler`

Auth middleware only runs on paths starting with `/api/`. Everything else (`/fn/`, `/metrics`, `/webhook/`, `/mcp`, custom routes) bypasses the API-key check entirely — per-function auth for invocations is enforced inside `InvokeHandler`. Internal SDK paths (`/api/v1/_kv/`, `/api/v1/_internal/`) use the per-process internal token instead of API keys.

## Database

SQLite WAL mode. All migrations in `internal/database/migrations.go` — additive only. `VACUUM INTO` produces consistent backup snapshots without a write lock.

## Gotchas

- `backend/cmd/orva/adapters/` is **generated** — edit `backend/runtimes/` instead; `make adapters-embed` syncs them.
- `execution_requests` has **no FK** to `executions` (intentionally dropped to fix async insert ordering); manual cascade runs in `DeleteExecution`.
- TypeScript deploys: after a successful `tsc`, the function's `Entrypoint` is updated to `dist/handler.js` in the DB. The validator on re-deploy checks for the source `.ts` file, not the compiled output.
- Zombie nsjail fix: `cmd.Wait()` is centralized in `Spawn` via `waitDone chan struct{}`; never call `Wait()` on the sandbox `cmd` anywhere else.
- **Docs single source:** `docs/reference.md` is the canonical Orva reference markdown (~53 KB). `make docs-embed` syncs it to `backend/internal/mcp/reference.md` (embedded by the `get_orva_docs` MCP tool) and `frontend/public/docs.md` (served at `/docs.md` for the dashboard's Copy as Markdown button). Edit the canonical file then run `make docs-embed`; the Vue Docs page is the rendered version (separate templates) and must be updated alongside if content changes.
