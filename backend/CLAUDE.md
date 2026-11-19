# Backend

`server.StartManaged` is an internal opt-in composition, not normal `serve`
wiring. The caller owns the sealed artifact Owner and drains its HTTP listener
before closing the instance. It requires matching durable managed mode and uses
the managed subdirectory under the same outer flock as `serve`. Legacy `serve`
follows directory aliases but refuses any mode file or root SQLite tombstone
before opening the database.
Offline relocation publishes the mode last; it does not enroll workloads.
Startup restores durable pause and reconstructs
only valid exact associations; restore gets a deny evaluator before dispatch.
`scheduler.RunManaged` starts only shared-authority job/cron loops and drains
before it returns. Legacy mutation routes and tools are not mounted in this
composition. Cold boot, legacy enrollment and recovery tooling remain gates.

Managed inbound webhooks (`/webhook/{id}`) use the same pattern: `MatchManagedInbound`
over `workload_inbound_publications` (triggers unpublish any non-managed write;
restore clears them), the existing signature verifiers, and only the managed
Factory path; `InboundTriggerHandler.Managed` has no legacy fallback.
Managed custom routes use one published-mapping lookup in `InvokeHandler`.
Route-only REST changes use `workloadtrigger` CAS; legacy rows do not dispatch.
Restore clears publications before admission; managed MCP/Chat and webhooks
are not part of this route slice.

Bound configuration writes persist the same pair against the new generation
and mark it inactive in the deny publication transaction. Explicit installation
can recover that association after a same-boot restart; policy review remains
separate. Managed deletion reserves metadata history and retires the Factory
at the same gate before deleting the row. It does not reclaim sealed outputs.

`workloadconfig` is an internal opt-in environment/execution-settings/secret
mutation service. It rechecks exact durable/source/ciphertext CAS and current
operator authority under owner → IAM → DB ordering, commits review-required deny,
then pins the exact current pair to the committed successor and invalidates the
old actual Factory before ACK. Explicit queued installation in the same stores
changes which pair later configuration resolves. Cancellation/retirement
callbacks run outside owner/IAM locks. Explicit new concrete Factory installation
precedes separate aggregate-policy review. Optional HTTP/MCP/shared-AI adapters
do not activate normal routes or boot schemas. Other writers, full lifecycle
and recovery remain separate gates. See its README.
`builder.NewPublicationQueue` is opt-in managed-store-only queued deployment
capture/publication, never normal boot activation. It refuses legacy function
filesystem directories, captures live token-free submitter/source/epoch provenance
transactionally, and publishes inactive review-required deny while invalidating
the old actual Factory under the same gate. Fresh concrete-pair Install and
source-aware Review are separate. Opt-in managed rollback promotes a verified
historical deployment ID without rewriting history and also publishes inactive
review-required deny. Durable pause blocks rollback; internal resume uses the
same promotion association with the exact current sealed pair and a fresh deny
generation. Pause/resume have explicit REST and MCP/shared-AI adapters; normal
routing, catalogs and boot are unchanged. No symlink promotion, legacy
rollback/GC adoption or autoresume; see `builder/README.md`.

`workloadhistory` is an internal opt-in deletion/history service. Its explicit
schema and registry hook are absent from normal boot/router construction.
Capacity and canonical metadata capture commit before deleting status and IAM
retirement; process callbacks run outside the gate. History is inspection-only;
confirmed/admin reissue creates new operator work with a fresh target UUID and
payload. Real Node/Python sealed-pool scratch lifecycle tests qualify this narrow
path, not complete function lifecycle or restore. Explicit managed-policy
coordination removes the retiring owner's attachments and advances membership/
audit in the history reservation transaction; surviving owners are unchanged.
Public history controls, boot/restore and complete lifecycle remain separate
cut-over gates. See its README.

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
| `artifactfs` | Linux staging, strict manifest verification and code/runtime leases. Unbound recovery stays inspection-only. The Owner gates same-boot recovery on exact fencing and reconciles fully prepared host-bound appends. Host-bound recovery authenticates and retires the oldest proven epoch at the cap before appending; version 1 still denies at capacity. The explicit cold opener requires the private witness and stable filesystem identities. Neither path is wired into daemon boot; see OWNER.md. |
| `workloadspawn` | Inactive frozen-generation factories issue bounded one-use spawn plans from owned config/secrets and concrete code/runtime leases. Matching resource facts do not establish current-generation proof, policy approval or serving-pool wiring. |
| `workloadiam` | Function-IAM policy parser, immutable evaluator and admission/publication coordinator. SDK-host KV integration is internal opt-in only; no serving, scheduler, boot migration or public API activation yet. See the package README and `docs/FUNCTION_IAM_PLAN.md`. |
| `workloadpolicy` | Shared policy publication core and unregistered HTTP adapter. Requires trusted live authentication/current-artifact observation; mutations recheck live operator authority transactionally. The live scratch KV workflow exercises it, but no serving route or observer is installed. |
| `workloadfence` | Linux cgroup-v2 epoch ownership, pre-exec enrollment and kernel-verified descendant fencing. Package-only acceptance does not authorize artifact recovery, reclamation or IAM admission; see its README for ownership and relocation constraints. |
| `config` | Config struct; environment variables only — there is no config file or YAML loader (see docs/CONFIG.md) |
| `database` | SQLite schema, migrations, all CRUD helpers |
| `registry` | In-memory function registry wrapping DB |
| `builder` | Deploy pipeline: tarball → `npm install` / `pip install` → optional `tsc` → register. Every one of those commands runs **inside nsjail** via `sandbox.RunBuild`, using the runtime rootfs's own toolchain and the same compiled NSTUN egress policy a worker gets — the installs fail closed without one. A function with no dependencies runs no installer and needs no policy. **The version hash is the source archive plus the installed interpreter** (`Builder.runtimeIdentity` reads the rootfs's `.orva-runtime` stamp, `unstamped` if absent), because dependencies are built into the version directory and compiled packages do not load on another interpreter; hashing the source alone made an identical redeploy reuse the old build after a runtime change. pip is deliberately given **no** `--platform`/`--python-version`: it runs on the rootfs interpreter and detects its own tags, whereas a pinned `manylinux2014` hid numpy, pandas, scipy, pillow, pyarrow and torch. `buildcache.go` owns the **per-function** npm/pip cache and every path built from a function id; `gc.go` bounds the caches and reclaims orphaned function dirs. |

`builder` checks JS and Python entrypoint syntax inside the network-disabled
build jail after archive validation and before dependency installation. A `.ts`
entrypoint without `tsconfig.json` fails early; the existing jailed `tsc` step
checks a configured TypeScript project after npm install. No host-side parsing
or author-code execution is involved in the pre-install checks.
Internal `WithRuntimeBinding` captures one concrete runtime generation per Build,
holds a pipeline anchor and gives commands independent leases through cleanup.
Managed cache hits without provenance deny; legacy construction is unchanged.
No serving caller enables this path. Runtime pinning does not fence build
descendants or seal/approve code output; see `builder/README.md`.
Separate internal managed-output mode owns fresh staging and an exclusive build
Domain, fencing before inspection, Seal or Abort. Failed cleanup retains bounded
ownership for retry. Only plain no-dependency Node/Python is qualified; no
serving caller, durable publication, current authority or recovery is enabled.
Managed output can use a concrete ParentDomain instead of raw budget/path
provisioning. Build domains then inherit ancestor entry gates; root fencing
blocks later starts through retained child handles. This is not boot wiring
or a durable code/runtime store association.
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
No pool override means an automatic maximum from CPU slots, the full
per-worker `memory.max` budget (with a 16-MiB floor), and function concurrency. `max_warm=0` restores
that mode; a positive value only lowers the resource ceiling. The idle channel
is sized to this derived bound so `cap(idle) >= p.max >= dynamicMax` still
holds without a universal worker count cap. Live spawn reservation remains
the fail-closed memory/CPU gate. A recent low `memory.current` sample cannot
discount the reservation: all workers may grow to their hard bounds at once.
Startup baseline warmup reads only a bounded recent execution window per
function through `idx_executions_function`; do not restore a whole-table
`ROW_NUMBER` rank, which delays the HTTP listener as history grows.
Successful-execution history uses a bounded newest-window probe when there is
no date/search filter or offset; a few recent failures no longer force the
status-index sort. Sparse successes fall back after at most four page widths
rather than scanning old history through `idx_executions_started`. Do not force
that index for all statuses: a 1.47-million-row scratch database had only 19 error rows, and a
forced oldest-reaching scan took 66.5 seconds.
Locally generated trace IDs keep the W3C-compatible 32-hex shape, with a
48-bit millisecond prefix and 80 cryptographically random trailing bits. This
clusters new trace-index writes without changing incoming W3C IDs or reducing
the randomness of the rightmost seven bytes. Do not use trace IDs as secrets.
The pool controller's steady/burst targets use measured service time, not
cold-start time multiplied by every arrival. That multiplication fills idle
pools to their resource caps and can starve another function. A bounded
speculative burst is limited to one wave of the existing per-pool spawn slots
plus rotation spares; current queue pressure can still request more workers
up to the resource ceiling. A hot pool expected to hit its max-use limit
inside the stable window keeps a bounded rotation spare derived from measured
spawn time. The controller also counts live workers within a rate-aware
max-use lead window and temporarily requests warm replacements before a
same-generation cohort retires together; those reservations remain under the
normal host ceiling. This controller variant is still being qualified on
mixed-runtime long-soak and cross-pool fairness workloads.
`proxy.Proxy` caches the non-security streaming settings for at most 30 seconds
per instance; a refresh never blocks concurrent invocations that already have
a prior snapshot.

Internal `ManagedAuthority` mode installs concrete frozen Factories, derives
resources from the same generation used for spawn, and denies missing owners
without legacy fallback. Current caller/target observers share the IAM gate;
retirement and generation-bound Release reject late or obsolete workers.
Public Proxy timeout/header facts come from the acquired generation. The live
scratch fixture exercises pooled KV and two-sided buffered calls; normal
serving construction, boot schemas and production policy routes remain off.

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

SDK traffic never selects a host interface or gateway: it uses the inherited
worker channel. Do not restore HTTP endpoint discovery or an SDK firewall
exception to work around an incompatible installed adapter.
`Worker.WorkloadState` and `SDKCaller.WorkloadState` expose host-owned identity,
monotonic readiness and original-lease observations for future IAM integration.
Artifact revisions are trusted spawn configuration, never environment/frame
claims. Empty artifacts stay unknown. These snapshots do not provide atomic
admission; readiness/lease invalidation must join the IAM gate at cut-over.
Channel-less workers report `no_channel` with identity/readiness only, never
bootstrap/execution authority; their dispatch path still emits no SDK lease frame.
`ExecConfig.WorkloadTransitionHook` optionally wraps readiness/lease/death atomics.
Nil preserves current behavior. Bind it to the same gate as observed admission
at cut-over; cancellation, session teardown and I/O must remain outside the hook.
Dispatch can hold its worker mutex before the gate; gate-held observers must
never acquire that mutex. No serving instance binds an IAM hook yet.
Dispatch rejects cancellation observed before lease publication/submission,
including after waiting for its mutex, without invalidating a healthy worker.
Cancellation after submission still marks the worker dead. This does not make
cancellation atomic with the subsequent wire write.
`ExecConfig.VerifiedPaths` is internal opt-in lease transfer for concrete code/
runtime trees. Nil keeps legacy resolution. Function/runtime labels must match;
spawn failure releases ownership, and successful workers release only during
centralized post-Wait cleanup. It does not mint artifact approval. Host endpoint
probes reject absolute links before following them; normal serving pools still
supply no leases. Internal managed pools transfer concrete Factory leases.
Optional `ExecConfig.Domain` and `BuildConfig.Domain` enroll Spawn, Execute and
RunBuild before exec and constrain nsjail relocation to the same owned epoch.
Required controller failure denies without legacy fallback. No serving caller
supplies a domain; see `sandbox/DOMAIN.md` for ownership and validation limits.
| `sdkhost` | SDK channel op executor (`kv.*`, `invoke`, `invoke.stream.*`). `invoke*` call the identity-based F2F cores in `server/handlers/internal_invoke.go` (`CheckDepth`/`ResolveCallee`/`RunInvoke`/`OpenStream`), preserved independently of the removed SDK HTTP wrappers; trace/span/depth come only from `caller.Exec`, the callee ctx derives from `caller.Exec.Ctx`. Streams live in a per-channel `sandbox.SDKSession` (max 8, lease-scoped) and are torn down by `LeaseRevoked`/`Closed`. The invoker is attached after the pool exists (`Host.SetInvoker`). |
| `sandbox` | nsjail process lifecycle; `Worker` type with `Dispatch`/`DispatchEx`. Also the function runtime versions: `NodeMajor` / `PythonMinor`, which the runtime labels, the build version hash and `TestRuntimeVersionsAgree` all read |
| `proxy` | HTTP → sandbox bridge; request capture (A3); streaming write-loop (C1) |
| `executionlogs` | Shared worker-stderr processing for HTTP, buffered/streaming F2F, scheduler, inbound webhook and replay paths. Extracts structured SDK records into the optional telemetry writer and preserves ordinary stderr and malformed records for invocation logs. |
| `metrics` | Prometheus-text counters + histograms (no external deps, atomic ops) |
| `safepath` | Containment for user-supplied relative paths. `Validate` on the write side so a bad `entrypoint` never reaches storage; `Join` on the read side, which must hold independently because rows written before validation still carry whatever they carry. Traversal is rejected, not collapsed. |
| `secrets` | AES-256-GCM encrypted secrets per function. The key lives at `<dataDir>/.master.key`, outside the database — back it up with `orva.db` or restored secrets are undecryptable ciphertext |
| `sdkauth` | Host-owned active execution identity, depth and trace/span bookkeeping. It no longer mints or verifies credentials; SDK authority comes from the private channel and per-dispatch lease. |
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

Key files: `deploy.go`, `deployments.go`, `diff.go`, `rollback.go`, `functions.go`, `invoke.go`, `logs.go`, `executions.go`, `cron.go`, `kv.go`, `jobs.go`, `secrets.go`, `webhooks.go`, `routes.go`, `dns.go`, `firewall.go`, `fixtures.go`, `channels.go`, `traces.go`, `pool.go`, `keys.go`, `system.go`, `backup.go`, `activity.go`, `docs.go`, `completion.go`.

## Key Patterns

**Function mutations:** the registry's per-function mutation lock protects
read-modify-write configuration and deployment snapshot publication. Acquire the
build/rollback lock first when both are needed; queued compilation releases the
mutation lock, then reloads current configuration before publishing build outputs.
Never persist the build's stale function record over a newer permission update.

**Handler responses**: always use `respond.JSON(w, status, val)` / `respond.Error(w, status, "SLUG", "message", requestID)` from `server/handlers/respond/` (the last arg is the request ID, often `RequestID(r.Context())` or `""`).

**Invocation funnel**: HTTP, cron, jobs, and F2F calls all go through `Worker.Dispatch()` (sync response) or `Worker.DispatchEx()` (multi-frame streaming). Never invoke nsjail directly from handlers. `Dispatch` buffers a streaming handler's output into memory and caps it at 32 MiB — cron, jobs, F2F and inbound webhooks all take that path, so a handler that streams a large body works over `/fn/` and is refused when fired by cron.

**Async DB writes**: execution rows use `database.AsyncInsertExecution*` batch writers — no synchronous DB calls on the hot proxy path.

**Name resolution**: functions can be referenced by UUID or by name. Use the handler method `(h *FunctionHandler) resolveFnID(idOrName string) (string, bool)` in `handlers/functions.go` (sibling copies on `FixtureHandler` / `KVOperatorHandler` / `InboundWebhookHandler`).

**Streaming wire protocol**: `response_start` → `chunk` (base64 body data) → `response_end` frames over the worker's stdin/stdout pipe. `proxy.Forward()` owns the write-loop.

**Shared tool registry (single source of truth)**: every operator tool is declared once via `regAddTool` (`mcp/agent_registry.go`). That call registers the tool with BOTH the external MCP server (`server.go`, unchanged behavior) AND the in-process agent registry (`mcp.BuildAgentRegistry(deps, perms)`, gated to the principal's perms). The internal AI agent dispatches these tools directly as Go calls — no MCP transport, no HTTP. Same name/description/schema/destructive-hint feed both fronts, so they never drift. When adding a tool, use `regAddTool(rc, perm, def, handler)` inside a `register*Tools(rc *regCtx)` function — never bare `mcpsdk.AddTool`.

## Middleware Chain

`CORS → BodySizeLimit → Auth → RequestID → Logger → Handler`

Auth middleware only runs on paths starting with `/api/`. Everything else (`/fn/`, `/metrics`, `/webhook/`, `/mcp`, custom routes) bypasses the API-key check entirely — per-function auth for invocations is enforced inside `InvokeHandler`. The former SDK HTTP routes (`/api/v1/_kv/`, `/api/v1/_internal/`) are removed. No SDK token bypass exists; public jobs enqueue uses normal operator authentication.

## Database

SQLite WAL mode. All migrations in `internal/database/migrations.go` — additive only. `VACUUM INTO` produces consistent backup snapshots without a write lock.

`KVListLiteralWithCursorContext` is used only by the internal opt-in SDK-host IAM
path, never serving/operator routes yet. It queries case-sensitive literal UTF-8
prefix ranges using binary index bounds; existing serving cursor-list callers
retain their legacy `LIKE` semantics until the coordinated IAM cut-over.

`sdkhost.WithIAM` is a staged constructor option, not an operator feature flag.
Omitting it preserves current behavior; explicitly passing nil denies protected
operations. It authorizes KV under fresh observed admission, including all batch
selectors before the transaction, and binds literal-list cursors to owner/prefix.
Buffered and streaming F2F have staged acquired-worker admission behind a trusted
target-state observer; missing observers or unbound pool artifacts deny. Stream
heads, chunks, completion and errors reauthorize at bounded handoff; frame I/O,
encoding and cleanup stay outside admission. Jobs require the separate explicit
`workloadasync` fixture service; cron requires separate explicit `WithCronIAM`;
spans require explicit own-execution emission grants and a required current-artifact
observer; optional persistence never reauthorizes accepted envelopes;
span jobs retain the deciding owner/revision as private immutable queue metadata,
not a stored span column or SDK claim;
owned stream close is cleanup. Target-state
observers must read active/current-generation facts under the same IAM gate,
never infer activity from policy existence or perform DB/I/O/worker-lock reads.
Never enable the partial path in serving configuration before coordinated cut-over.

`workloadasync` composes staged jobs through the same Authority and actual managed
target observer. Its schema/HTTP/MCP/scheduler options are explicit fixture-only;
normal constructors and boot migration remain unchanged. See its README for
standing provenance, admission-revision invalidation, admin-only controls and the
unresolved target-FK history-retention limit. Cron remains disabled in jobs-only
mode. The independent cron core fixture adds explicit schema, leased self-upsert,
standing/final managed-target firing and admin-service future-only resume; no
normal boot activation is installed. Separate opt-in `CronHandler` / MCP cron
dependencies delegate management to the same service with exact string CAS and
live admin checks. Normal router/catalog defaults remain unchanged. The explicit
scratch controls UI entry compiles the real CronJobs view, not API snapshots.

`workload_iam*.go` holds inert policy storage and read-only legacy preflight.
Its explicit schema initializer is test-only for now, never called on boot.
CAS saves validate signed revisions and atomically append digest/actor audit
records (32 retained per live function). Policy and audit rows cascade on
function deletion; host-level deletion audit remains a cut-over obligation.
Storage is not admin authorization or live artifact verification.
`IAMPolicyRecord.AdmissionEvaluator` preserves revision and approval metadata
but denies protected and standing work for quarantined/review-required records;
the future loader must use it rather than compiling retained grants directly.

`secrets.Manager.SnapshotForIAM` strictly decrypts caller-supplied snapshot rows
into a bounded owned map or returns an error without partial output. It checks
owner/key consistency and ciphertext bounds before decoding; unlike the legacy
best-effort getter, corruption denies. It is not serving-wired or proof that rows
and artifact generation were read atomically; the future snapshot service owns
that DB boundary.

`workload_artifact*.go` provides inert generation CAS and transactional reads of
generation, function configuration and sorted ciphertext rows. Its schema
initializer is test-only. Explicit secret mutations rotate generation in the
same transaction; legacy CRUD does not maintain these records, so they are not
yet authoritative for admission. Snapshots bound ciphertext/config allocations;
strict decryption, concrete path/runtime pinning and policy publication remain
separate integration obligations.
`secrets.Manager.PrepareArtifactSnapshot` composes that transactional read with
strict decryption outside IAM/pool locks. It returns owned durable inputs only,
not approval or pinned runtime/code paths; publication must revalidate generation.
`PublishWorkloadEnvironment` is an inert environment-only transaction seam: exact
function/artifact/policy CAS, generation rotation, environment persistence, and
review-required policy/audit update commit together. It preserves quarantine and
retained grants/approval without rebinding authority. Live admin checks and
deny-state/pool publication remain service obligations; no serving caller uses it.
`session_identity*.go` is an inert additive UUID-reference/backfill seam for
browser sessions. Its initializer is test-only and reads rowids, never bearer
tokens, during backfill. Token-free lookups recheck row/user/expiry in the DB.
Before explicit initialization, login writers retain legacy NULL-ID behavior.
Afterward, new sessions receive independent UUIDs transactionally; browser auth
captures those token-free references on fresh and cached paths and keeps cache
validity within observed session expiry. NULL-ID sessions still clear inherited
operator references. Identity records are provenance, not admin grants.
`PublishWorkloadEnvironmentAsOperator` rechecks live credential/admin facts in the
same transaction as publication. References must come from trusted authentication,
never author input. It derives audit actors and checks expiry before mutation and
commit; browser-session/queued-build capture and serving wiring remain absent.
`SaveIAMPolicyAsOperator` applies the same live operator checks to policy CAS,
authority-epoch advancement and bounded audit in one transaction. It derives
actors and rechecks credentials before commit, preserving the persistence-only
save API. It does not verify current runtime/code artifacts or install authority:
the shared management service must bind reviewed current artifacts and publish
under the registry mutation lock and IAM gate before exposing a serving API.
`auth.OperatorReference` is the shared token-free credential reference. API-key
REST authentication and MCP capture it under a private context key; OAuth retains
its validated stored audience. AI request contexts inherit it. It carries no
frozen permissions or bearer. Legacy browser sessions clear inherited references
instead of promoting cookie prefixes. Explicitly initialized UUID sessions retain
stable provenance through browser auth and AI request contexts; commit-time live
checks remain mandatory.

`workload_managed*.go` adds test-initialized named definitions, immutable versions,
version-pinned attachments and separately preserved inline sources. Explicit
`workloadpolicy.NewManaged` composes these through sorted owner locks, one IAM
gate and one operator-rechecked transaction, then publishes all affected owners
and signals cancellations after releasing locks. Source removals retain
approval/quarantine; new versions never auto-propagate. The old HTTP adapter
refuses managed mode rather than exposing the union as editable inline. This is
an internal opt-in service, not boot migration or production cut-over; see
`workloadpolicy/MANAGED.md`.

`ExecuteManaged` supplies the strict source-aware boundary for the optional HTTP,
external MCP and AI registry adapters. The CLI uses that HTTP contract, not a
local evaluator. Managed mutations recheck read and live admin authority; the
per-operation schemas retain raw duplicate/unknown-field rejection. Normal
router/MCP construction supplies no IAM service. Scratch workflows qualify
Node/Python enforcement and UI/CLI/MCP parity; deterministic-provider AI-turn
tests qualify approval/resume separately, not browser Chat or public providers.

## Runtime SDK channel tests

`backend/runtimes/{node/orva.js,python/orva.py}` route every SDK operation over a private per-worker socketpair
(fd `ORVA_SDK_FD` when `ORVA_SDK_PROTO=1`; missing channels fail closed).
The adapters carry the literal `orva-sdk-proto:1` marker near the top (the host reads it
to detect adapter/host skew) and store the `{"type":"lease"}` stdin frame for the SDK. Tests play the
host over a real socketpair and need no nsjail or Docker:

```bash
node --test backend/runtimes/test/node_sdk_channel.test.js
python3 -m unittest discover -s backend/runtimes/test -p 'test_*.py'
```

Edit `backend/runtimes/`, then run `make adapters-embed`.

## Gotchas

- `backend/cmd/orva/adapters/` is **generated** — edit `backend/runtimes/` instead; `make adapters-embed` syncs them.
- `execution_requests`, `user_spans` and `execution_log_entries` have **no FK** to `executions` (intentionally, for async insert ordering). CASCADE cannot reach them, so the `executionChildTables` list drives a manual cascade in `DeleteExecution`, `PurgeOldExecutions` **and** `DeleteFunction` — deleting a function cascades to `executions`, and that cascade is precisely what makes the child rows unjoinable. Never hand-list the tables at a call site. `reclaimOrphanedExecutionRows` (boot, batched, one-shot behind the `orphan_reclaim_done` marker) clears what earlier builds already stranded.
- TypeScript deploys: `Entrypoint` stays the authored file (`handler.ts`); the compiler's output is recorded separately in `RunEntrypoint` (`dist/handler.js`), and empty means the two are the same. The pipeline never overwrites `Entrypoint`, so the validator, the diff, the build-cache resolver and `GetSource` all read the file the operator actually wrote. `pool` publishes `ORVA_ENTRYPOINT` from `RunEntrypoint` and falls back to `Entrypoint`. Rollback does **not** restore `RunEntrypoint` from the deployment snapshot — it derives it from the promoted version's directory via `builder.RunEntrypointFor`, because rows written before the column existed carry no value and treating that absence as "" hands Node a TypeScript file (`WORKER_CRASHED` on every invocation).
- **Stderr is synchronised with the reply by a marker, not by timing.** A worker's stdout (framed protocol) and stderr are separate pipes drained by separate goroutines, so a snapshot taken when the response frame arrives can miss bytes the child wrote first, and the next dispatch's `errBefore` then skips them for good. Adapters write `\n__ORVA_EOR__\n` to stderr before every `response`, `response_end` and `error` frame and advertise `stderr_sync` on the ready frame; `DispatchResult.Stderr` waits (≤250 ms) for it only when the adapter advertised it and the dispatch completed, then strips it. Keep the marker, the capability flag and the adapters in lock-step, and emit it from the shared frame writer rather than at call sites. Do not replace it with a sleep or a quiescence heuristic: that taxes every request.
- Zombie nsjail fix: `cmd.Wait()` is centralized in `Spawn` via `waitDone chan struct{}`; never call `Wait()` on the sandbox `cmd` anywhere else.
- **The build cache is per-function and must stay that way.** `<dataDir>/build-cache/<fnID>/{npm,pip}` is bound at `/tmp/cache` in the build jail (one bind, one function). npm keeps the packument in the same URL-keyed cacache as the tarball behind an *unkeyed* checksum and pip's http cache verifies nothing on read, so a shared cache would let one function's dependency poison every later build of every other function — and `npm install` runs postinstall scripts. The mount point lives *under* `/tmp` because nsjail must create it and the rootfs is read-only: the image pre-creates only `/code` and `/tmp` (`scripts/build-rootfs.sh`), so a top-level `/cache` fails with EPERM on every deployed rootfs. HOME stays throwaway. Bounds (all mandatory): `build_cache_max_age_days`, `build_cache_max_mb` (whole-directory LRU eviction — never per-file, which would desync the cache index), explicit purge on delete + `DELETE /api/v1/functions/{id}/build-cache`.
- Orva **owns** `npm_config_cache` / `PIP_CACHE_DIR` / `npm_config_logs_dir` in the build jail: they name in-jail paths, so a value forwarded from orvad's own environment (`withRegistryEnv`) points at an unmounted path on a read-only chroot and the build dies with a bare EROFS. `setOwnedEnv` strips every case spelling before setting ours.
- Deleting a function removes `functions/<id>/` and `build-cache/<id>/` from disk (non-fatally — the row is already gone). The GC's orphan sweep is the retry, and reclaims what leaked before this existed.
- **AI conversation editing is destructive-tail:** editing or deleting a chat message (`EditMessage` / `DeleteMessage` in `server/ai_handler.go`, backed by `database.DeleteMessagesFromSeq`) truncates the conversation at that message's `seq` — it deletes that message and every message + tool call after it, then (for edit) re-runs the turn. There is no branching history; the tail is gone. `Regenerate` is the same truncate-then-rerun on the last assistant turn.
- **AI turns are one-per-conversation:** the `ai.Manager` holds a keyed try-lock (`tryLockConv`/`unlockConv`) acquired by every mutating entry point (Chat, Resume, RegenerateLast, EditAndResend, DeleteMessageFrom). An overlapping turn on the same conversation is rejected — SSE `error` for streaming paths, `ai.ErrConversationBusy` → 409 for the JSON delete. `database.InsertMessage` assigns `seq` atomically inside the INSERT (`MAX(seq)+1` subquery); never split it back into a SELECT-then-INSERT.
- **AI gateway lifecycle:** `ai.Manager.Close()` releases the embedded Bifrost pools and is called from `Server.Shutdown` (via `s.router.ai`). The gateway is built lazily and rebuilt on provider-config change (`invalidateClient`).
- **Docs single source:** `docs/reference.md` is the canonical Orva reference markdown (~68 KB). `make docs-embed` syncs it to `backend/internal/mcp/reference.md` (embedded by the `get_orva_docs` MCP tool), `frontend/public/docs.md` (served at `/web/docs.md` for the dashboard's Copy as Markdown button), and `cli/commands/reference.md` (embedded into the slim CLI, served by `orva docs`). Edit the canonical file then run `make docs-embed`; the Vue Docs page is the rendered version (separate templates) and must be updated alongside if content changes.

## SDK surface rules that a later change could silently undo

- **`crons.upsert` requires a live execution.** `sdkhost.cronsUpsert` checks
  the host-bound execution lease before the shared core persists a caller-owned
  schedule; the 25-schedule cap remains. Calls, jobs and spans are also scoped
  to the active dispatch. Do not replace host identity with SDK arguments.
- **KV is deliberately NOT gated the same way.** Module-scope code runs at
  spawn with no execution bound, and the docs tell operators to cache config
  there, so an execution gate on KV would break a documented pattern silently
  at cold start.
- **Channel lifetime follows the worker.** Lease revocation cancels callee work
  and closes owned streams. Retired busy workers finish current calls but must
  never return to the idle pool; the reaper closes their channel. Do not restore
  a legacy credential, HTTP fallback, or control-plane network exemption.
- **Revocation evicts.** Every path that deletes an API key or ends a session
  must evict the auth middleware's cache; the entries carry a 30s TTL as a
  backstop, not as the mechanism. `router.go` builds one `invalidateKey`
  closure and hands it to both the REST handler and `mcp.Deps` for that reason.
