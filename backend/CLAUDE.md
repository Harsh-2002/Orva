# Backend

Go 1.27 (the embedded Bifrost AI gateway requires go ≥1.27; the Dockerfile + all CI workflows pin golang 1.27 in lock-step). Module: `github.com/Harsh-2002/Orva` (rooted at the repo, not at
`backend/`). SQLite with no CGO (`modernc.org/sqlite`).

The server binary built from `backend/cmd/orva` is the daemon (`orva serve`)
plus all CLI subcommands. The CLI subcommands live at `cli/commands/`
(package `commands`) — `backend/cmd/orva/main.go` imports
`cli/commands` and calls `commands.NewRoot()` then bolts on its own
`serve` / `setup` constructors. Single source of truth for
every client subcommand; the slim standalone CLI at `cli/cmd/orva`
uses the same library.

## Build & Test

```bash
# From repo root:
make build          # → build/orva (server + CLI bundled)
make test           # go test ./...
make lint           # go vet ./...

# Direct invocations (also from repo root, since go.mod is here):
go build ./backend/cmd/orva
go test ./...
go vet ./...
```

## Package Layout (`internal/`)

| Package | Purpose |
|---|---|
| `config` | Config struct; environment variables only — there is no config file or YAML loader (see docs/CONFIG.md) |
| `database` | SQLite schema, migrations, all CRUD helpers |
| `registry` | In-memory function registry wrapping DB |
| `builder` | Deploy pipeline: tarball → `npm install` / `pip install` → optional `tsc` → register. Every one of those commands runs **inside nsjail** via `sandbox.RunBuild`, using the runtime rootfs's own toolchain and the same compiled NSTUN egress policy a worker gets — the installs fail closed without one. A function with no dependencies runs no installer and needs no policy. `buildcache.go` owns the **per-function** npm/pip cache and every path built from a function id; `gc.go` bounds the caches and reclaims orphaned function dirs. |
| `pool` | Warm-sandbox pool manager (`pool.Manager`) per function. Each worker executes one request at a time; pending counts derive from the detected memory/FD envelope, with a reserved share for other functions. Saturated pools wait at most 2 seconds; a pool that can still grow or is spawning waits up to the 10-second adapter-readiness budget plus one scaler tick. The HTTP proxy reserves a conservative share of daemon memory before body read; unknown-length bodies are charged at the configured cap. Waiting does not occupy the host execution limiter or count against the function's execution timeout. A spawned worker enters the idle pool only after its adapter emits the ready frame following user-code import; nsjail's nice-19 default is overridden to normal priority. Failed asynchronous spawns wake pending callers with the original error, preserving fail-closed egress-policy responses. |

`pool/hostmem.go` discovers the process's cgroup-v2 ancestry via
`/proc/self/cgroup`. It uses the tightest visible ancestor and host memory headroom and
CPU quota, plus the effective CPU set, for its startup capacity snapshot; a
1-second poll refreshes memory usage. This is resource discovery, not proof of
delegated per-worker cgroup enforcement. `proxy.Forward` does not consume a
seccomp policy; the worker's actual policy is built at spawn in `pool/pool.go`.
`sandbox.cgroupv2Delegate` separately creates `orva.daemon` and `orva.workers`
inside the process's own delegated cgroup, never a writable ancestor. It moves
only the daemon into its leaf before enabling domain controllers, and verifies
child `memory.max`, `pids.max` and `cpu.max`; an unavailable delegate yields
`rlimit_only`, not a false hard-limit claim.
No pool override means an automatic maximum from CPU slots, the 16-MiB
minimum worker reservation, and function concurrency. `max_warm=0` restores
that mode; a positive value only lowers the resource ceiling. The idle channel
is sized to this derived bound so `cap(idle) >= p.max >= dynamicMax` still
holds without a universal worker count cap. Live spawn reservation remains
the fail-closed memory/CPU gate.
Startup baseline warmup reads only a bounded recent execution window per
function through `idx_executions_function`; do not restore a whole-table
`ROW_NUMBER` rank, which delays the HTTP listener as history grows.
`proxy.Proxy` caches the non-security streaming settings for at most 30 seconds
per instance; a refresh never blocks concurrent invocations that already have
a prior snapshot.

Worker release parks a healthy worker even if a transient capacity snapshot is
below the current pool size; the controller applies its 30-second scale-down
grace before pruning idle workers. A queued pool may reclaim idle capacity from
another function, but not from a donor with its own queue or from an active
donor below its desired worker count. New spawns still require host memory and
CPU reservations. Do not reintroduce release-path pruning or reclaim from an
actively demanded pool: both cause repeated nsjail/adapter cold starts under
mixed load.

The pool's demand history uses sixty one-second arrival buckets instead of a
timestamp per request. Service, queue-wait and spawn durations use bounded
overwrite rings; a full ring records without allocation, and percentile sorting
uses a snapshot outside the signal lock. Controller wakeups coalesce within
20 ms. None of these is an execution-concurrency cap. The async SQLite writer
prepares each distinct SQL statement once per batch; HTTP execution
baseline/outlier fields are included
in the execution INSERT instead of a second UPDATE. It has separate bounded
critical execution, operator activity, and optional replay/log/span lanes.
Per-priority cumulative writer timing counters expose connection acquisition,
SQL execution, commit, and submit-to-commit latency for the normal batch path;
the savepoint failure-recovery path is intentionally excluded from committed
job and queue-wait samples. Use deltas over the same scrape interval when
diagnosing saturation, because cumulative values mix idle and busy periods.
The consumer prioritizes critical rows when their queue reaches three
quarters of its capacity; activity is then deferred and optional telemetry
is read only when both higher-priority channels are empty. Activity remains non-blocking
and can still drop if its own lane or SQLite is saturated.
Adjacent final-execution and operator-activity INSERTs in a batch become
one multi-row VALUES statement; if that statement fails, the transaction
rolls back and the per-row savepoint fallback isolates the offending row
without losing its neighbors. Never bulk a generic write job or remove the
failure fallback.
All invocation entrypoints reserve a critical completion slot before executing user code;
the slot remains owned by a queued/retrying writer job until commit, deletion,
or an explicit failure. Reject storage pressure before execution with
`429 STORAGE_BACKPRESSURE` on HTTP paths, never after the sandbox has produced side effects.
All three queues reserve bytes atomically before publication and return reservations on
timeout or shedding; do not move accounting after a channel send, because the
consumer may already have received the job. The reservation remains held while
a batch is in flight or retrying, then is released on commit or explicit shed.
Transient retries must copy any alias of the batch before clearing the old
backing array. Writer shutdown closes the producer
stop signal, waits for in-flight enqueues under `enqueueMu`, then signals the
consumer to drain; a late critical producer writes directly while the DB is
still open. Function deletion takes a writer commit fence and removes
parentless captured requests, spans, and structured logs by their stored
function id. Function-tagged jobs processed after deletion are discarded with
an explicit counter; the live-function cache contains only existing ids, so
repeated deletions do not accumulate tombstones. `metrics.latency_ms` stops
at proxy return, while `response_latency_ms` measures the complete public
invoke handler (including admission and record enqueue), not network transit.
The pool's `service_p95_ms` samples full worker leases from successful acquire
to release, including response processing and streaming, but excludes queue
wait and spawn time. `Manager.Release` records it once for every invocation
path; handlers must not record separate dispatch-only samples.

Each spawned `sandbox.Worker` has one lifetime stdout frame reader. Readiness,
buffered replies and streaming chunks consume from that reader's channel;
context cancellation still marks/kills the worker. Do not reintroduce a
goroutine per response frame on the warm path or allow concurrent dispatch on
one worker.

`server.detectInternalAPIBase` chooses a local interface IP (default-route
interface first) for the sandbox SDK control plane. Do not reintroduce a
gateway or generic health probe: it can select a separate Orva instance before
this server begins listening. The environment override remains available for
unusual routing.
| `sandbox` | nsjail process lifecycle; `Worker` type with `Dispatch`/`DispatchEx` |
| `proxy` | HTTP → sandbox bridge; request capture (A3); streaming write-loop (C1) |
| `metrics` | Prometheus-text counters + histograms (no external deps, atomic ops) |
| `safepath` | Containment for user-supplied relative paths. `Validate` on the write side so a bad `entrypoint` never reaches storage; `Join` on the read side, which must hold independently because rows written before validation still carry whatever they carry. Traversal is rejected, not collapsed. |
| `secrets` | AES-256-GCM encrypted secrets per function. The key lives at `<dataDir>/.master.key`, outside the database — back it up with `orva.db` or restored secrets are undecryptable ciphertext |
| `sdkauth` | Mints and verifies the worker credential (`ORVA_INTERNAL_TOKEN`). HMAC over the function id **and a per-spawn nonce**, under a process-random key — so a credential dies both when orvad restarts and when the worker it was issued to is reaped. `Mint` returns a release; the pool wires it to `sandbox.ExecConfig.OnExit` and must also call it on every spawn error path. `live` holds the nonces, `active` the executions the gated SDK surfaces check against |
| `scheduler` | Cron runner (`robfig/cron/v3`) |
| `mcp` | MCP server (go-sdk); 73 operator-management tools OR channel-mode (one tool per bundled function, invoke-only). Auth accepts API keys, OAuth 2.1 access tokens, OR channel tokens. **Transport is stateless** (`StreamableHTTPOptions{Stateless: true}`) — the SDK serves protocol `2026-07-28` only in that mode, and older clients still negotiate down. No `initialize` handshake required, no `Mcp-Session-Id`, `GET`/`DELETE /mcp` → 405. Operator servers are cached by the four-bit permission surface (at most 16 variants); channel servers remain request-scoped. Actor identity and public origin live in request context, so cached servers cannot cross-label activity or leak one host into another's `invoke_url`. A process-wide `ServerOptions.SchemaCache` keeps `AddTool` reflection off first-build paths. `cacheScopeMiddleware` rewrites the SDK's hardcoded `cacheScope: "public"` to `"private"` — the catalog is permission- and channel-scoped, so a shared HTTP cache entry would leak one principal's tool surface to another. |
| `oauth` | OAuth 2.1 authorization server (RFC 7591 DCR + RFC 8414 metadata + PKCE S256 + RFC 8707 resource indicators + RFC 7009 revocation). Lets claude.ai/ChatGPT add `/mcp` as a custom connector via the browser. Connected apps + sessions managed at `/api/v1/oauth/connected-apps`, `DELETE /api/v1/oauth/clients/{client_id}` (retires an application: revokes its grants, drops pending codes, blocks re-authorization without fresh consent) and `/api/v1/auth/sessions` and surfaced in the dashboard's Settings page. DCR default scope is `read invoke write admin`; a client's **registered** scope is a ceiling, applied via `IntersectScope` on both authorize paths, so a client registered `read` cannot be issued `admin`. |
| `auth` | Shared `Principal` type (Kind=api_key / oauth / channel + ID/Label/Perms/Channel). Both REST middleware and MCP auth resolve the inbound bearer to a `*Principal`; downstream code (activity log, MCP tool registration) consumes the Kind directly. |
| `trace` | Causal-trace collector + span lifecycle (W3C `traceparent` interop, outlier detection). See `docs/TRACING.md`. |
| `urlhint` | Per-request `BaseURL(r)` helper. One source of truth for OAuth issuer URLs, MCP `invoke_url` fields, and audience-bound token validation. Also owns `ClientIP(r)` — the identity **every** rate limiter buckets on (invoke, login, OAuth DCR). Proxy headers decide it only when `TrustProxyHeaders` is set from `ORVA_TRUSTED_PROXY` at boot, and then the **rightmost** `X-Forwarded-For` entry, because everything left of the one your proxy appended is still client-supplied. |

**Agent channels** — bundle N functions under a name + a static bearer token; presenting that token at `/mcp` exposes ONLY those functions as MCP tools (snake_case names, invoke-only). Operator-managed at `/api/v1/channels` (`Channels` page in the dashboard). Token format: `orva_chn_<32 hex>`. Channel tokens are explicitly rejected with 401 at `/api/v1/*` — they have no Orva-management authority.
| `firewall` | Sandbox egress policy. Compiles `egress_blocklist` into nsjail NSTUN `user_net { rule4/rule6 }` rules, publishes each as an immutable generation under `<dataDir>/firewall/policy/`, and retires warm egress pools when the generation changes. Also serves sandbox DNS (`resolv.conf` + `hosts`). **No nftables, no host table.** Carve-outs (NSTUN gateway, control plane, DNS) MUST precede rejects — NSTUN is default-ALLOW + first-match-wins, and its own rule parsing is fail-open, so every value is validated and canonicalised here rather than trusted to nsjail. `Policy.Blocks` exposes the same rule set to orvad's own dialer so daemon and sandboxes cannot drift. |
| `server` | HTTP router + middleware chain + all handlers |
| `server/events` | SSE event hub + outbound webhook fanout |
| `server/handlers` | One file per resource group; `respond/` sub-package |
| `backup` | `SnapshotDB` / `ArchiveTo` / `RestoreFrom` helpers |
| `version` | Single source of truth for the version string |
| `ai` | In-product AI chat assistant. `Manager` (service layer) wires the SQLite store, the secrets cipher (provider-key encryption), the in-process tool registry, the embedded Bifrost LLM gateway (`ai/llm`), and the agentic loop (`ai/agent`). Served at `/api/v1/ai/*` by `server/ai_handler.go` (SSE for chat/approval). The agent's `defaultSystemPrompt` const lives in `ai/manager.go` — it's a Go **raw string, so it must stay backtick-free** (escape any fenced-code examples by description, not literal ```). |
| `ai/llm` | Wraps the embedded Bifrost gateway (`github.com/maximhq/bifrost/core`) behind neutral types + a normalized event stream. `Account` resolves BYO keys from `ai_provider_configs` live. Thinking levels → `ChatReasoning`. **Bifrost owns its own transport** (fasthttp + `providers/utils.ConfigureDialer`) and exposes no hook for Orva's dialer, so the egress control for model calls is the per-turn `firewall.CheckEndpoint` preflight, not `Policy.Blocks`. `privatenet.go` decides `NetworkConfig.AllowPrivateNetwork`: Bifrost refuses every RFC1918 destination without it, which made LAN-hosted endpoints (ollama, vLLM) unreachable regardless of operator policy. It is enabled **only when the configured base URL is itself private**, so a public provider keeps Bifrost's resolve-then-dial rebinding guard. The decision is memoised — `GetConfigForProvider` is on the per-request path. |
| `ai/agent` | Agentic loop (≈25-iteration budget): stream model turn → emit SSE deltas → detect tool calls → approval-gate writes → in-process dispatch → feed results back. Decoupled from `mcp` (takes tools + a dispatcher). Two independent gates: the per-conversation **approval policy** (`all_writes` / `destructive_only` / `auto`, checked in `approvalNeeded`) and a separate code-enforced `confirm=true` requirement on destructive tools. |

The canonical UUIDv7 generator (`ids`) and HTTP client (`client`) live at **repo-root** `internal/ids/` and `internal/client/` — shared with the slim CLI codebase, not under `backend/internal/`.

## CLI Commands (`cli/commands/`)

All Cobra subcommands share one binary with the server. `orva serve` starts the daemon; every other command is a CLI client that reads `~/.orva/config.yaml`. The command library lives at repo-root `cli/commands/` (NOT under `backend/cmd/orva/`, which holds only `main.go`/`serve.go`/`setup.go` + the embedded `adapters/`); both binaries register it via `commands.NewRoot()`. See `cli/CLAUDE.md`.

Key files: `deploy.go`, `deployments.go`, `diff.go`, `rollback.go`, `functions.go`, `invoke.go`, `logs.go`, `executions.go`, `cron.go`, `kv.go`, `jobs.go`, `secrets.go`, `webhooks.go`, `routes.go`, `dns.go`, `firewall.go`, `fixtures.go`, `channels.go`, `traces.go`, `pool.go`, `keys.go`, `system.go`, `backup.go`, `activity.go`, `chat.go`, `docs.go`, `completion.go`.

## Key Patterns

**Handler responses**: always use `respond.JSON(w, status, val)` / `respond.Error(w, status, "SLUG", "message", requestID)` from `server/handlers/respond/` (the last arg is the request ID, often `RequestID(r.Context())` or `""`).

**Invocation funnel**: HTTP, cron, jobs, and F2F calls all go through `Worker.Dispatch()` (sync response) or `Worker.DispatchEx()` (multi-frame streaming). Never invoke nsjail directly from handlers. `Dispatch` buffers a streaming handler's output into memory and caps it at 32 MiB — cron, jobs, F2F and inbound webhooks all take that path, so a handler that streams a large body works over `/fn/` and is refused when fired by cron.

**Async DB writes**: execution rows use `database.AsyncInsertExecution*` batch writers — no synchronous DB calls on the hot proxy path.

**Name resolution**: functions can be referenced by UUID or by name. Use the handler method `(h *FunctionHandler) resolveFnID(idOrName string) (string, bool)` in `handlers/functions.go` (sibling copies on `FixtureHandler` / `KVOperatorHandler` / `InboundWebhookHandler`).

**Streaming wire protocol**: `response_start` → `chunk` (base64 body data) → `response_end` frames over the worker's stdin/stdout pipe. `proxy.Forward()` owns the write-loop.

**Shared tool registry (single source of truth)**: every operator tool is declared once via `regAddTool` (`mcp/agent_registry.go`). That call registers the tool with BOTH the external MCP server (`server.go`, unchanged behavior) AND the in-process agent registry (`mcp.BuildAgentRegistry(deps, perms)`, gated to the principal's perms). The internal AI agent dispatches these tools directly as Go calls — no MCP transport, no HTTP. Same name/description/schema/destructive-hint feed both fronts, so they never drift. When adding a tool, use `regAddTool(rc, perm, def, handler)` inside a `register*Tools(rc *regCtx)` function — never bare `mcpsdk.AddTool`.

## Middleware Chain

`CORS → BodySizeLimit → Auth → RequestID → Logger → Handler`

Auth middleware only runs on paths starting with `/api/`. Everything else (`/fn/`, `/metrics`, `/webhook/`, `/mcp`, custom routes) bypasses the API-key check entirely — per-function auth for invocations is enforced inside `InvokeHandler`. Internal SDK paths (`/api/v1/_kv/`, `/api/v1/_internal/`) use process-signed, function-scoped credentials, and the gate **rejects an unverifiable one with 401 before the handler runs** (it used to discard the error and call the handler anyway, leaving the prefixes authenticated only by each handler remembering to re-check) instead of API keys.

## Database

SQLite WAL mode. All migrations in `internal/database/migrations.go` — additive only. `VACUUM INTO` produces consistent backup snapshots without a write lock.

## Gotchas

- `backend/cmd/orva/adapters/` is **generated** — edit `backend/runtimes/` instead; `make adapters-embed` syncs them.
- `execution_requests`, `user_spans` and `execution_log_entries` have **no FK** to `executions` (intentionally, for async insert ordering). CASCADE cannot reach them, so the `executionChildTables` list drives a manual cascade in `DeleteExecution`, `PurgeOldExecutions` **and** `DeleteFunction` — deleting a function cascades to `executions`, and that cascade is precisely what makes the child rows unjoinable. Never hand-list the tables at a call site. `reclaimOrphanedExecutionRows` (boot, batched, one-shot behind the `orphan_reclaim_done` marker) clears what earlier builds already stranded.
- TypeScript deploys: `Entrypoint` stays the authored file (`handler.ts`); the compiler's output is recorded separately in `RunEntrypoint` (`dist/handler.js`), and empty means the two are the same. The pipeline never overwrites `Entrypoint`, so the validator, the diff, the build-cache resolver and `GetSource` all read the file the operator actually wrote. `pool` publishes `ORVA_ENTRYPOINT` from `RunEntrypoint` and falls back to `Entrypoint`. Rollback does **not** restore `RunEntrypoint` from the deployment snapshot — it derives it from the promoted version's directory via `builder.RunEntrypointFor`, because rows written before the column existed carry no value and treating that absence as "" hands Node a TypeScript file (`WORKER_CRASHED` on every invocation).
- Zombie nsjail fix: `cmd.Wait()` is centralized in `Spawn` via `waitDone chan struct{}`; never call `Wait()` on the sandbox `cmd` anywhere else.
- **The build cache is per-function and must stay that way.** `<dataDir>/build-cache/<fnID>/{npm,pip}` is bound at `/tmp/cache` in the build jail (one bind, one function). npm keeps the packument in the same URL-keyed cacache as the tarball behind an *unkeyed* checksum and pip's http cache verifies nothing on read, so a shared cache would let one function's dependency poison every later build of every other function — and `npm install` runs postinstall scripts. The mount point lives *under* `/tmp` because nsjail must create it and the rootfs is read-only: the image pre-creates only `/code` and `/tmp` (`scripts/build-rootfs.sh`), so a top-level `/cache` fails with EPERM on every deployed rootfs. HOME stays throwaway. Bounds (all mandatory): `build_cache_max_age_days`, `build_cache_max_mb` (whole-directory LRU eviction — never per-file, which would desync the cache index), explicit purge on delete + `DELETE /api/v1/functions/{id}/build-cache`.
- Orva **owns** `npm_config_cache` / `PIP_CACHE_DIR` / `npm_config_logs_dir` in the build jail: they name in-jail paths, so a value forwarded from orvad's own environment (`withRegistryEnv`) points at an unmounted path on a read-only chroot and the build dies with a bare EROFS. `setOwnedEnv` strips every case spelling before setting ours.
- Deleting a function removes `functions/<id>/` and `build-cache/<id>/` from disk (non-fatally — the row is already gone). The GC's orphan sweep is the retry, and reclaims what leaked before this existed.
- **AI conversation editing is destructive-tail:** editing or deleting a chat message (`EditMessage` / `DeleteMessage` in `server/ai_handler.go`, backed by `database.DeleteMessagesFromSeq`) truncates the conversation at that message's `seq` — it deletes that message and every message + tool call after it, then (for edit) re-runs the turn. There is no branching history; the tail is gone. `Regenerate` is the same truncate-then-rerun on the last assistant turn.
- **AI turns are one-per-conversation:** the `ai.Manager` holds a keyed try-lock (`tryLockConv`/`unlockConv`) acquired by every mutating entry point (Chat, Resume, RegenerateLast, EditAndResend, DeleteMessageFrom). An overlapping turn on the same conversation is rejected — SSE `error` for streaming paths, `ai.ErrConversationBusy` → 409 for the JSON delete. `database.InsertMessage` assigns `seq` atomically inside the INSERT (`MAX(seq)+1` subquery); never split it back into a SELECT-then-INSERT.
- **AI gateway lifecycle:** `ai.Manager.Close()` releases the embedded Bifrost pools and is called from `Server.Shutdown` (via `s.router.ai`). The gateway is built lazily and rebuilt on provider-config change (`invalidateClient`).
- **Docs single source:** `docs/reference.md` is the canonical Orva reference markdown (~68 KB). `make docs-embed` syncs it to `backend/internal/mcp/reference.md` (embedded by the `get_orva_docs` MCP tool), `frontend/public/docs.md` (served at `/web/docs.md` for the dashboard's Copy as Markdown button), and `cli/commands/reference.md` (embedded into the slim CLI, served by `orva docs`). Edit the canonical file then run `make docs-embed`; the Vue Docs page is the rendered version (separate templates) and must be updated alongside if content changes.

## SDK surface rules that a later change could silently undo

- **`crons.upsert` requires a live execution.** `CronHandler.UpsertInternal`
  gates on `SDKAuth.OwnsExecution`, and there is a 25-schedule cap on that path.
  This is deliberate and is the only SDK surface gated this way: a cron row is
  the one thing a leaked `ORVA_INTERNAL_TOKEN` can create that survives the
  process-random signing key it was minted under, and nothing in
  `internal/scheduler` consults `auth_mode`. Removing the gate re-opens that.
- **KV is deliberately NOT gated the same way.** Module-scope code runs at
  spawn with no execution bound, and the docs tell operators to cache config
  there, so an execution gate on KV would break a documented pattern silently
  at cold start.
- **The SDK credential's release is wired to the reaper, not to Kill or to
  retire.** `Kill` and `retirePool` express an intention to stop a worker; a
  busy one keeps serving until it drains, so releasing there would 401 a live
  request. The reaper goroutine in `sandbox/worker.go` is the only place the
  process is known to be gone, and it covers every exit path. A missed release
  leaks a map entry and degrades to the old behaviour — a credential valid
  until restart — which is why the pool releases on its error paths too.
- **Revocation evicts.** Every path that deletes an API key or ends a session
  must evict the auth middleware's cache; the entries carry a 30s TTL as a
  backstop, not as the mechanism. `router.go` builds one `invalidateKey`
  closure and hands it to both the REST handler and `mcp.Deps` for that reason.
