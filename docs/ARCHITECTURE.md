# Architecture

Orva is a single-binary Go server (`orvad`) that fronts an HTTP API,
spawns nsjail-isolated workers per function, and serves a Vue
dashboard from the same port. SQLite is the only storage backend.
There is no separate control plane and no clustering — one host runs
the whole thing.

## Component map

What runs on the host:

```
host kernel  (Linux ≥ 5.10, cgroup v2, unprivileged userns)
  │
  └─ orvad   (single Go process, listens on :8443)
       │
       ├─ HTTP server     /api  /web  /auth  /events (SSE)  /mcp
       ├─ Pool manager    per-function warm workers + KPA autoscaler
       ├─ Build queue     extract → install deps → atomic publish
       ├─ Event hub       SSE pub/sub for metrics + exec + deploy
       ├─ MCP server      operator-management tools (also reused by the AI agent)
       ├─ AI assistant    in-product agentic chat: Bifrost LLM gateway +
       │                  agentic loop, dispatches MCP tools in-process (SSE)
       ├─ GC goroutine    prunes archived versions per system_config
       └─ SQLite          orva.db (WAL mode), single file
                          │
       spawns ▼ (one per cold-start invocation)
                          │
       ┌──────────────────┴───────────────────┐
       │                                      │
  nsjail worker                          nsjail worker     ...
  ┌────────────────────┐                 ┌────────────────────┐
  │ user namespace     │                 │ user namespace     │
  │ chroot → rootfs    │                 │ chroot → rootfs    │
  │ /code (read-only)  │                 │ /code (read-only)  │
  │ tmpfs /tmp         │                 │ tmpfs /tmp         │
  │ cgroup v2 limits   │                 │ cgroup v2 limits   │
  │ seccomp filter     │                 │ seccomp filter     │
  │ user code          │                 │ user code          │
  └────────────────────┘                 └────────────────────┘
```

What's connected to what (data flow inside orvad):

```
HTTP request  ──▶  HTTP server  ──▶  Pool manager  ──▶  nsjail worker
                                          │                  │
                                          │              user code
                                          │                  │
                                          ▼              response
                                     Idle/Busy
                                     channel pool

POST /functions/.../deploy-inline  ──▶  Build queue  ──▶  versions/<hash>/
                                                          + current symlink

Async writer  ◀──  invocation finished  ──▶  Event hub  ──▶  /events SSE
     │                                            │
     ▼                                            ▼
  SQLite                                    Browser EventSource
  (executions table)                        (Dashboard live tiles)

All subsystems read/write a single SQLite file (orva.db):
  users, sessions, api_keys, functions, deployments, executions,
  execution_logs, build_logs, secrets, routes, pool_config, system_config
```

## Process model

Single Go process. No fork, no IPC for control flow — the autoscaler,
build queue, GC, and event hub all live as goroutines inside `orvad`.

The only out-of-process work is **per-invocation nsjail spawn**:
`orvad` calls `nsjail` as a child, hands it the request frame on
stdin, reads the response frame from stdout, and reaps the child. The
nsjail child re-execs into the language runtime (`node` or `python3`)
with the chroot already set up.

Per-function pools keep nsjail+adapter processes warm so subsequent
invocations skip the ~50ms spawn cost.

## Request lifecycle (cold path)

```
POST /fn/xxx/health
     │
     ▼ middleware: auth → rate-limit → CORS
     │
     ▼ InvokeHandler.ServeHTTP
     │
     ▼ Registry.Get(fnID)               ← in-memory cache, SQLite fallback
     │
     ▼ poolMgr.Acquire(fnID, ctx)       ← per-fn pool; autoscaler may spawn
     │      │
     │      ├ idle worker available?    → reuse
     │      └ no, total < dynamicMax?   → spawnFn(ctx)
     │             │
     │             └ sandbox.Spawn(cfg) → fork nsjail → rootfs chroot
     │                                                  → exec adapter
     ▼ proxy.Forward(worker, req)
     │      │
     │      ├ JSON-encode request frame
     │      ├ write to worker stdin
     │      ├ read response frame from worker stdout (with TimeoutMS)
     │      └ JSON-decode statusCode/headers/body
     │
     ▼ writeResponse(w, frame)
     │
     ▼ async-insert execution row       ← drained_loop in async.go batches writes
     │
     ▼ events.Hub.Publish("execution")  ← SSE subscribers see it live
     │
     ▼ poolMgr.Release(worker)          ← worker returns to idle channel
                                          (or killed if dynamicMax shrank)
```

Warm path is identical except `Acquire` returns immediately from the
idle channel.

## Deploy lifecycle

```
POST /api/v1/functions/{id}/deploy-inline   {code, dependencies}
     │
     ▼ FunctionHandler.DeployInline
     │
     ├ tar+gz the inline code to /tmp/orva-inline-*.tar.gz
     │
     ▼ DB.InsertDeployment(status="queued", phase=null)
     │
     ▼ buildQueue.Submit(BuildJob{tarball, deploymentID})
     │
     ▼ (async) Queue.runJob in worker goroutine
     │      │
     │      ├ FnLock(fnID)              ← serializes against rollback
     │      ├ DB.UpdateDeploymentPhase("extract")
     │      ├ Builder.Build(ctx, fn, tarball)
     │      │     │
     │      │     ├ pre-flight statvfs check        → ErrInsufficientDisk
     │      │     ├ hashFile(tarball) → codeHash
     │      │     ├ if versions/<hash>/.orva-ready exists → cached short-circuit
     │      │     ├ scratch_dir = versions/<hash>.tmp.<rand>
     │      │     │     ├ extract tarball
     │      │     │     ├ ValidateArchive (reject path-traversal symlinks)
     │      │     │     ├ npm install / pip install (HOST-side, not in sandbox)
     │      │     │     ├ install adapter wrapper (main.js / main.py)
     │      │     │     └ touch .orva-ready
     │      │     └ atomic os.Rename(scratch_dir, versions/<hash>)
     │      │
     │      ├ DB.SetDeploymentCodeHash(deployID, codeHash)
     │      ├ Registry.Set(fn) with bumped version + hash
     │      ├ ActivateVersion(dataDir, fnID, codeHash)  ← symlink retarget
     │      ├ DB.FinishDeployment(succeeded)
     │      ├ events.Hub.Publish("deployment", {status:"succeeded", phase:"done"})
     │      └ poolMgr.RefreshForDeploy(fnID)  ← drains idle workers
     │
     ▼ next Acquire → spawnFn reads `current` symlink fresh → new code
```

Symlink retarget is atomic (rename(2) on a symlink). nsjail binds the
symlink path itself (no deref at bind time), but reads-through happen
at exec time inside the new namespace, so workers that got their
`-R` mount before the swap continue serving the old code until they
exit. `RefreshForDeploy` drains the idle channel so the next spawn
picks up the new target.

## Storage layout

```
/var/lib/orva/
├── orva.db                                    SQLite (WAL mode)
├── orva.db-wal
├── orva.db-shm
├── .admin-key                                 mode 0600, plaintext bootstrap key
├── functions/
│   └── 019df200-7b00-7e00-9c00-aab1cd2e3f40/
│       ├── current → versions/abc1234...     atomic symlink (the active version)
│       └── versions/
│           ├── abc1234.../                   immutable per-hash dirs
│           │   ├── .orva-ready                marker = "this version is complete"
│           │   ├── handler.js                 user code
│           │   ├── package.json               (if deps were declared)
│           │   ├── node_modules/              installed at build time
│           │   └── main.js                    Orva's adapter wrapper
│           └── def5678.../
└── rootfs/
    ├── node22/                                debian-slim with /usr/bin/node
    ├── node24/
    ├── python313/
    └── python314/
```

## Database schema

One SQLite file, grouped by subsystem. Core tables:

| table | what it stores |
|---|---|
| `users`              | onboarded admin/operator accounts |
| `sessions`           | UI cookie tokens (7-day TTL) |
| `api_keys`           | bearer tokens for `/api/v1/*` |
| `functions`          | name, runtime, current version + code_hash, status |
| `deployments`        | per-build audit trail (queued/building/succeeded/failed; rollback rows are source='rollback') |
| `executions` / `execution_requests` / `execution_logs` / `execution_log_entries` | every invocation + its captured request + stderr/stdout |
| `build_logs`         | stdout/stderr from `npm install` / `pip install` |
| `function_secrets`   | per-function encrypted env vars (AES-256-GCM) |
| `routes`             | custom URL → function-id mappings (`/webhooks/stripe`) |
| `pool_config`        | per-fn autoscaler tuning (min_warm, max_warm, target_concurrency) |
| `system_config`      | global tuning knobs (versions_to_keep, gc_interval_seconds, etc.) |

Feature tables added since v0.2:

| group | tables |
|---|---|
| Scheduling & queues  | `cron_schedules`, `jobs` |
| KV & fixtures        | `kv_store`, `fixtures` |
| Webhooks             | `event_subscriptions`, `webhook_deliveries`, `inbound_webhooks` |
| Tracing & activity   | `user_spans`, `activity_log` |
| Firewall             | `egress_blocklist` |
| Agent channels       | `channels`, `channel_functions` |
| OAuth 2.1            | `oauth_clients`, `oauth_authorization_codes`, `oauth_access_tokens` |
| AI assistant         | `ai_conversations`, `ai_messages`, `ai_tool_calls`, `ai_provider_configs`, `ai_settings` |

Schema lives in [`backend/internal/database/migrations.go`](../backend/internal/database/migrations.go) (~34 tables).
Migrations are additive only — `CREATE TABLE IF NOT EXISTS` + idempotent ALTER columns run on every boot. AI message/tool-call rows cascade-delete with their `ai_conversations` parent; editing or deleting a chat message truncates the conversation tail by `seq` (see `DeleteMessagesFromSeq`).

## Component responsibilities

### `backend/internal/server/`

The HTTP layer. Routes, middleware, handlers, the SSE event hub.
Stateless — every request reaches into the pool manager, registry, or
DB for actual work.

- `router.go` — `http.ServeMux` wiring, middleware chain
- `server.go` — `New()` constructs all subsystems and their cross-references
- `middleware.go` — request-id, body-size cap, CORS, logger
- `middleware_auth.go` — session cookie + API-key dispatch
- `events/` — SSE pub/sub broker (`hub.go`, `handler.go`)
- `handlers/` — one file per concern (functions, invoke, secrets, routes, etc.)
- `handlers/respond/` — error envelope helpers + `Retry-After` injection

### `backend/internal/pool/`

Warm worker pools, one per function, autoscaled.

- `pool.go` — `Manager` owns a `sync.Map[fnID]*functionPool`
- `function_pool.go` — per-fn idle channel + acquire/release/sweep
- `autoscaler.go` — Knative-KPA-style: 60s stable + 6s panic windows, `dynamicMax` derived from CPU + memory budget
- `hostmem.go` — global memory budget tracker (refuses spawns past 80% reservation)

### `backend/internal/sandbox/`

nsjail invocation, per-spawn config, host-wide concurrency limiter.

- `sandbox.go` — builds the nsjail argv (chroot, mounts, cgroup, seccomp)
- `worker.go` — JSON frame protocol over stdin/stdout, expiry tracking
- `seccomp.go` — Kafel policy (`default`, `strict`, `permissive`)
- `limiter.go` — host-wide `MaxConcurrent` semaphore with `TryAcquire`

### `backend/internal/builder/`

Build queue + version archive + GC + first-boot migration.

- `queue.go` — bounded channel + `runtime.NumCPU()` worker goroutines
- `builder.go` — extract → validate → install deps → atomic publish
- `activate.go` — symlink retarget for both deploy and rollback
- `gc.go` — periodic prune of versions beyond `versions_to_keep`
- `migrate_fs.go` — one-shot upgrade of legacy `code/` dirs to `versions/<hash>/`
- `validator.go` — archive sanity (path traversal, runtime entrypoint check)

### `backend/internal/database/`

SQLite access. No ORM — explicit queries, prepared statements where
hot.

- `migrations.go` — schema + idempotent ALTER loop
- `async.go` — batched-flush writer for executions (50-job batches or 50ms tick)
- One file per resource: `functions.go`, `deployments.go`, `secrets.go`, etc.

### `backend/internal/proxy/`

The bridge between HTTP and a sandbox worker.

- `proxy.go` — encodes request as JSON frame, writes to worker stdin, reads response frame, propagates timeouts and errors

### `backend/internal/registry/`

In-memory function cache backed by SQLite. Hot path reads (every
invocation) avoid a DB round-trip.

### `backend/internal/secrets/`

Encrypts/decrypts per-function env vars. Master key is derived from
the data dir's existence; rotation is a future project.

### `backend/internal/metrics/`

In-memory ring buffer over the last ~8k invocations. Computes p50,
p95, p99 server-side so the dashboard doesn't recompute on every
poll.

### `backend/internal/ai/`

The in-product AI chat assistant — an agentic loop that operates the
instance end-to-end through the same tools the MCP server exposes, with
BYO provider keys. Served at `/api/v1/ai/*` (SSE for chat + approval).

- `manager.go` — service layer: wires the SQLite chat store, the
  secrets cipher (provider-key encryption at rest), the in-process tool
  registry, the LLM gateway, and the agentic loop. Holds the agent's
  `defaultSystemPrompt` (a Go raw string — must stay backtick-free).
- `llm/` — wraps the **embedded Bifrost gateway**
  (`github.com/maximhq/bifrost/core`) behind neutral types + a
  normalized event stream. `Account` resolves BYO keys from
  `ai_provider_configs` at request time; thinking levels map to
  `ChatReasoning`. (The Bifrost dependency is why the module requires
  Go ≥1.26.)
- `agent/` — the agentic loop (≈25-iteration budget): stream a model
  turn → emit SSE deltas → detect tool calls → approval-gate writes →
  dispatch the tool **in-process as a Go call** (no MCP transport, no
  HTTP) → feed the result back. Decoupled from `mcp` — it takes a tool
  set + a dispatcher. Two independent gates protect mutations: the
  per-conversation approval policy (`all_writes` / `destructive_only` /
  `auto`) and a code-enforced `confirm=true` requirement on destructive
  tools.

The tools the agent dispatches come from `mcp.BuildAgentRegistry`,
which is fed by the **same** `regAddTool` declarations that register
the external MCP server — one source of truth, gated to the principal's
perms, so the two fronts never drift. Served by
`backend/internal/server/ai_handler.go`; the dashboard surface is the
`AI` sidebar section (`frontend/src/views/AI.vue`).

Concurrency + lifecycle invariants: every `/api/v1/ai/*` route requires
the `admin` permission; conversations are a shared operator space (not
isolated per credential). The manager serializes turns with a
per-conversation try-lock (overlapping turns are rejected, not
interleaved), `ai_messages.seq` is assigned atomically inside the
INSERT, and the LLM gateway is closed on `Server.Shutdown`.

### `backend/runtimes/`

Per-runtime adapter scripts. The adapter is the entrypoint nsjail
exec's into; it reads request frames from stdin, calls the user's
exported handler, and writes response frames back.

- `node22/adapter.js` — also used for node24
- `python313/adapter.py` — also used for python314

### `frontend/`

Vue 3 + Pinia + Vite. Single-page dashboard served from `/web/` by the
Go server (the build output is embedded at compile time via
`//go:embed ui_dist`).

- `src/views/` — one file per route (Dashboard, Editor, FunctionsList,
  Deployments, InvocationsLog, ApiKeys, CronJobs, Jobs, KVStore,
  Webhooks, InboundWebhooks, Channels, Firewall, Traces, TraceDetail,
  FunctionDiff, Settings, Docs, Onboarding, Login, and **AI** — the
  agentic chat)
- `src/stores/` — Pinia stores (`auth`, `system`, `events`, `confirm`,
  and `ai` — the chat timeline + streaming client)
- `src/components/common/` — shared primitives (`Modal`, `Drawer`,
  `Input`, `Button`, `IconButton`, `StatusBadge`, `ConfirmDialog`,
  `Toast`, …)
- `src/components/ai/` — the chat surface (composer, message list,
  tool-call cards, code/thinking blocks, provider settings)
- `src/api/` — axios client + endpoint helpers + EventSource wrapper.
  The AI chat is the exception: it streams over `fetch` +
  `ReadableStream` (the chat POST carries a body, so `EventSource`
  won't do)

## Concurrency model

Goroutines do almost everything. Critical concurrency primitives:

- **Pool manager**: per-fn `sync.Map` lookup. Each function pool has a
  `chan *Worker` (buffered) for idle workers — Go channels handle the
  acquire/release synchronization.
- **Per-fn lock** (`Manager.FunctionLock`): serializes deploy and
  rollback on the same function. Different functions are independent.
- **Async writer**: single goroutine drains a `chan writeJob` and
  batches DB inserts. Replaces the old goroutine-per-call pattern that
  burned CPU at sustained 500+ req/s.
- **Autoscaler**: one goroutine per `Manager`, ticks every second,
  evaluates each function's pool against its EWMA stable + panic
  windows.
- **Reaper**: one goroutine per pool, sweeps idle workers past TTL.
- **GC**: one goroutine, prunes archived version dirs every 5 minutes.
- **Build queue**: `runtime.NumCPU()` worker goroutines drain a job
  channel.
- **Event hub**: subscribers each have a 32-deep buffered channel.
  Producers drop events for slow consumers rather than block.

## Why these choices

**Why a single binary?** Operational simplicity. One process to
upgrade, one log to tail, one volume to back up. We give up the option
of horizontal scaling — but the target is a self-hosted, single-host
deployment, where horizontal scaling is over-engineering.

**Why SQLite?** The target is single-host. Postgres would force
operators to run two services and manage credentials between them. The
write rate is bounded by invocation throughput (one row per
invocation), and the async writer batches commits. SQLite in WAL mode
handles thousands of writes per second on commodity hardware.

**Why nsjail per invocation?** Hardware isolation requires either
process-level (nsjail / gvisor / kata) or VM-level (firecracker)
sandboxing. nsjail is the cheapest — single fork + namespace setup,
no VM boot. Per-invocation isolation means a function compromise
cannot affect another function or the host. Warm pools amortize the
spawn cost.

**Why Knative-style autoscaling?** Two-window EWMA (stable +
panic) handles bursty traffic gracefully. Stable smooths out transient
spikes; panic responds within seconds when sustained load arrives.
Combined with per-fn `dynamicMax` capped on CPU + memory budget, the
host stays healthy during traffic floods.

**Why content-addressed deploys?** Identical redeploys are a no-op
(same hash → cached version dir). Rollback is a symlink retarget.
Garbage collection is "delete versions not in the keep window." The
alternative — re-extract and re-install on every redeploy — is
slower and provides no rollback story.

## Reading order for new contributors

1. `backend/cmd/orva/serve.go` — entrypoint; see how everything wires up
2. `backend/internal/server/server.go::New` — main constructor
3. `backend/internal/server/handlers/invoke.go` — hot path (HTTP → pool → sandbox)
4. `backend/internal/pool/pool.go::Manager` + `function_pool.go::acquire` — the warm-pool dance
5. `backend/internal/builder/builder.go::Build` + `activate.go::ActivateVersion` — deploy + rollback
6. `frontend/src/stores/events.js` + `frontend/src/views/Dashboard.vue` — SSE consumption
