# Orva error catalog

Orva's REST API errors return the same envelope:

```json
{
  "error": {
    "code": "INVOCATION_QUEUE_FULL",
    "message": "invocation queue is full or its wait budget expired",
    "request_id": "019df210-7b00-7e00-9c00-aab1cd2e3f42",
    "hint": "back off briefly and retry; sustained overload requires more host capacity or a faster handler",
    "retry_after_s": 1
  }
}
```

Fields beyond `code` and `message` are optional and may be absent. Transient errors set both the `Retry-After` HTTP header and the `error.retry_after_s` body field; the header value matches the body and is RFC 7231-compliant for clients that don't parse the body.

OAuth 2.1 endpoints (`/api/v1/oauth/*`) use the RFC 6749 `{"error": "<code>"}`
shape instead, and MCP (`/mcp`) uses JSON-RPC errors. Neither is covered here.

## Where these apply

The admission and worker-acquire codes — `FUNCTION_BUSY`,
`INVOCATION_QUEUE_FULL`, `MEMORY_EXHAUSTED`, `EGRESS_POLICY_UNAVAILABLE`,
`SHUTTING_DOWN` and the rest of the pool and build codes — reach callers on
**every** invoke path: `POST /fn/{id}`, custom routes, replay,
function-to-function `orva.invoke()`, and the inbound-webhook trigger. An SDK
can therefore tell a concurrency cap it should back off from a host OOM it
should not.

A failure **after** a worker is running is reported per path:

| path | function timed out | worker crashed |
|---|---|---|
| `POST /fn/{id}` and custom routes | 504 `TIMEOUT` | 502 `WORKER_CRASHED` |
| replay | 504 `TIMEOUT` | 502 `REPLAY_FAILED` |
| function-to-function, inbound webhook | 502 `INVOKE_FAILED` | 502 `INVOKE_FAILED` |

On the last row, tell the two apart by `error.message`: `function timed out`
for a timeout, `worker exited unexpectedly…` for a crash.

## Code reference

These tables cover every code the REST API emits — `respond.Error`, the
`invokeError` / `deployError` mappers, and the auth middleware's invoke and
session writers.

### 4xx — client errors

| code | HTTP | when | retry? |
|---|---|---|---|
| `INVALID_JSON` | 400 | malformed request body | no — fix the payload |
| `BAD_REQUEST` | 400 | AI endpoints: invalid JSON body, or `content` missing on a chat turn | no — fix the request |
| `VALIDATION` | 400 | required field missing or invalid value | no — fix the request |
| `INVALID_REQUEST` | 400 | missing or malformed path/query parameter (e.g. a deployment or function id is absent) | no — fix the request |
| `INVALID_BODY` | 400 | request body could not be read or parsed on the cron/fixture operator endpoints | no — fix the payload |
| `VALIDATION_ERROR` | 400 | change-password: `old_password`/`new_password` missing, or the new password is under 8 characters | no — fix the request |
| `BAD_MULTIPART` | 400 | backup restore: the multipart body could not be parsed | no — resend as `multipart/form-data` |
| `MISSING_ARCHIVE` | 400 | backup restore: no `archive` part in the multipart body | no — attach the archive |
| `BAD_ARCHIVE` | 400 | backup restore: the upload is not a valid Orva backup (bad gzip, corrupt DB, checksum mismatch) | no — upload an archive produced by `GET /api/v1/backup` |
| `INCOMPATIBLE_FORMAT` | 400 | backup restore: the archive was written by an incompatible backup format version | no — restore it with the Orva version that wrote it |
| `CONFIRM_REQUIRED` | 400 | backup restore without `?confirm=1`; the live database would be overwritten | no — repeat with `confirm=1` once you intend to replace the data |
| `TOOL_NAME_COLLISION` | 400 | channel create/update: two bundled functions map to the same MCP tool name (e.g. `stripe-charge` and `stripe_charge`) | no — rename one function |
| `INVALID_FUNCTION` | 400 | channel create or `PUT /channels/{id}/functions`: a `function_ids` entry does not exist (`function <id> not found`). Every id is checked, including a single one | no — fix the function list |
| `CANT_DELETE_SELF` | 400 | session revoke targeting the calling session without `?allow_self=1` | no — use `POST /api/v1/auth/logout` |
| `AMBIGUOUS_PREFIX` | 400 | session revoke: the prefix matches more than one session | no — use a longer prefix |
| `WRONG_PASSWORD` | 400 | change-password: the current password is incorrect | no — retry with the correct password |
| `UNAUTHORIZED` | 401 | missing or invalid API key/session, or invalid/stale SDK credential | no — re-auth or let the worker restart |
| `INVALID_CREDENTIALS` | 401 | login: wrong username or password | no — retry with valid credentials |
| `SIGNATURE_INVALID` | 401 | inbound webhook: the delivery signature did not verify against the trigger's secret | no — fix the signing secret or the signed payload |
| `FORBIDDEN` | 403 | authenticated but lacks the required permission | no |
| `SDK_SCOPE_VIOLATION` | 403 | a signed worker credential attempted another KV/cron namespace or an execution/trace it does not own | no — fix the SDK request; caller scope cannot be elevated |
| `NOT_FOUND` | 404 | function, route, session or execution doesn't exist. `DELETE /executions/{id}` used to report success for a missing id. | no |
| `FUNCTION_NOT_FOUND` | 404 | function lookup miss on a name-or-ID path (diff endpoint) | no |
| `FUNCTION_GONE` | 404 | replay: the function that produced the captured execution has since been deleted | no |
| `VERSION_NOT_FOUND` | 404 | deployment row not in DB (e.g. `orva diff` with a bad `--from`/`--to`) | no |
| `METHOD_NOT_ALLOWED` | 405 | method not in the route's allowed list | no |
| `NOT_ACTIVE` | 409 / 503 | function status is `error` or `inactive` | no — redeploy or activate |
| `ALREADY_SETUP` | 409 | onboarding after an admin user already exists | no — sign in instead |
| `CONVERSATION_BUSY` | 409 | AI chat: another turn is already running in this conversation | yes — wait for it to finish, then retry |
| `VACUUM_IN_PROGRESS` | 409 | `POST /system/vacuum` while another VACUUM holds the lock | yes — retry shortly |
| `KV_INCR_FAILED` | 409 | KV `incr` on a value that is not an integer | no — overwrite the key with a numeric value |
| `CONFLICT` | 409 | `PUT /functions/{id}` renaming onto a name that already exists. Used to surface as a 500. | no — pick another name |
| `CHECKPOINT_BUSY` | 409 | `POST /system/vacuum` could not checkpoint the WAL because another connection holds a read lock. Previously the checkpoint's busy result was discarded and VACUUM ran against a stale WAL. | yes — retry shortly |
| `VERSION_GCD` | 410 | rollback / diff target's version tree was pruned by the GC; `details.available_hashes` lists survivors | no — pick a still-archived hash |
| `INACTIVE` | 410 | inbound webhook trigger is disabled | no — enable the trigger |
| `REQUEST_TRUNCATED` | 410 | replay: the captured request body was truncated at the configured cap, so a replay would not match the original | no |
| `PAYLOAD_TOO_LARGE` | 413 | body exceeds `cfg.Server.MaxBodyBytes` (default 6 MB). Two paths: a `Content-Length` above the cap is refused up front; a **chunked** body with no `Content-Length` is refused when the read hits the cap. The chunked case used to be silently truncated and handed to the function with a 200. | no — send `Content-Length`, split the upload, or raise the cap |
| `TOO_MANY_REQUESTS` | 429 | legacy host-wide concurrency-cap response | **yes** — back off briefly |
| `INVOCATION_QUEUE_FULL` | 429 | memory/FD-derived pending or pre-body memory budget was exhausted, or the worker wait expired (2 seconds at a saturated pool; up to 12 seconds while the pool can grow or workers are starting) | **yes** — `Retry-After: 1`; no function code ran |
| `STORAGE_BACKPRESSURE` | 429 | the critical SQLite execution-record writer could not reserve a slot within five seconds before an HTTP, inbound-webhook, replay, or internal SDK invocation | **yes** — `Retry-After: 1`; no function code ran |
| `RATE_LIMITED` | 429 | rate limit exceeded — per-function invoke limit (`rate_limit_per_min`, per client IP) or too many login attempts (per client IP). “Client IP” is the TCP peer address unless `ORVA_TRUSTED_PROXY=true`; see `docs/CONFIG.md` | **yes** — `Retry-After: 60` |
| `FUNCTION_BUSY` | 429 | function at its own `max_concurrency` cap under the `reject` policy | **yes** — `Retry-After: 1`, or raise `max_concurrency` / switch the policy to `queue` |

### 5xx — server / platform errors

| code | HTTP | when | retry? |
|---|---|---|---|
| `INTERNAL` | 500 | unmapped server fault | no — file a bug with `request_id` |
| `SIGNING_NOT_CONFIGURED` | 500 | a function with `auth_mode=signed` was invoked but its `ORVA_SIGNING_SECRET` secret is not set | no — set the secret, or switch the function's `auth_mode` |
| `DB_ERROR` | 500 | AI conversation store read/write failed | no — retry; if it persists, file a bug with `request_id` |
| `NO_STREAM` | 500 | an AI SSE endpoint could not take over the response writer (a proxy/server that does not support streaming) | no — fix the proxy or use a streaming-capable path |
| `BACKUP_FAILED` | 500 | creating the backup snapshot or tempfile failed | no — check disk space and the server log |
| `RESTORE_FAILED` | 500 | restore failed after the archive was accepted (disk I/O, rename); the live database is left in place | no — check the server log and free space |
| `VACUUM_FAILED` | 500 | checkpoint or `VACUUM` failed | yes — retry; if it persists, check free disk and open readers |
| `BUILD_ERROR` | 500 | the deploy's build failed; the function is left in `error` status | no — check `/api/v1/deployments/<id>/logs` for the npm/pip error, fix, redeploy |
| `WORKER_CRASHED` | 502 | on `POST /fn/{id}` and custom routes: adapter exited unexpectedly (`process.exit`, OOM-kill, syntax error in handler, or an older/manual nsjail host-policy setup). Replay reports this as `REPLAY_FAILED`; function-to-function and webhook paths as `INVOKE_FAILED` | no — inspect the execution log and sandbox health |
| `INVOKE_FAILED` | 502 | a function-to-function call or inbound-webhook delivery reached a worker, but the run failed: a timeout (`function timed out`) or a crash (`worker exited unexpectedly…`). `POST /fn/{id}` reports the same failures as `TIMEOUT` / `WORKER_CRASHED` | depends on the cause — raise `timeout_ms` or fix the handler; the execution log has the detail |
| `REPLAY_FAILED` | 502 | replay: the replayed run failed in the worker (a crash or dispatch error). A timeout is reported as `TIMEOUT` | no — inspect the handler and the new execution's log |
| `INVALID_RESPONSE` | 502 | replay: the adapter returned a response the server could not parse | no — inspect the handler and its execution log |
| `BUILDING` | 503 | first deploy in flight; no prior code to serve | **yes** — `Retry-After: 5` |
| `BUILD_QUEUE_FULL` | 503 | build queue at channel capacity | yes — `Retry-After: depth × 30s` |
| `POOL_AT_CAPACITY` | 503 | legacy pool-capacity response; normal HTTP admission expiry now returns `INVOCATION_QUEUE_FULL` | yes — `Retry-After: 5` |
| `POOL_ERROR` | 503 | replay: the pool manager is not configured (server wiring fault) | no — fix the server configuration |
| `MEMORY_EXHAUSTED` | 503 | host memory budget at 80% reservation | yes — `Retry-After: 30` |
| `EGRESS_POLICY_UNAVAILABLE` | 503 | a `network_mode=egress` function was invoked while no sandbox egress policy had compiled; the spawn is refused rather than run unfiltered | yes — `Retry-After: 10` (the manager recompiles every 10s), but a bad rule must be fixed first |
| `KV_UNAVAILABLE` | 503 | an atomic KV batch could not commit; no batch writes were retained | yes — retry the complete batch |
| `INSUFFICIENT_DISK` | 503 | not enough free disk on the data volume to start the build | no — free space or lower `system_config.min_free_disk_mb` |
| `SHUTTING_DOWN` | 503 | server is closing down | no, on this host — redirect |
| `SANDBOX_ERROR` | 503 | unmapped sandbox / dispatch failure | no — investigate |
| `UNAVAILABLE` | 503 | onboarding cannot verify setup state, so it refuses to create an admin user | yes — retry once the database is reachable |
| `SERVICE_UNAVAILABLE` | 503 | authentication lookup failed (database error); the request is refused rather than treated as unauthenticated | yes — retry once the database is reachable |
| `FIREWALL_DISABLED` | 503 | firewall manager not initialized; status/resolve cannot report enforcement | no — check the server log |
| `FUNCTION_UNAVAILABLE` | 503 | inbound webhook: the target function is not in the registry (deleted or not yet loaded) | yes — retry once the function is active |
| `TIMEOUT` | 504 | function exceeded its `timeout_ms` on `POST /fn/{id}`, custom routes, or replay. Function-to-function and webhook paths report a timeout as `INVOKE_FAILED` | no — raise it or optimize handler |
| `MAX_CALL_DEPTH` | 507 | function-to-function chain exceeded the call-depth cap | no — break the chain or restructure |
| `CLIENT_DISCONNECTED` | 499 | client closed the connection mid-request | n/a (never reaches client) |

## Operator-actionable hints

Every transient error includes a `hint` field telling the operator what to change. Examples:

- `POOL_AT_CAPACITY`: "inspect pool limiting_reason; raise max_warm only for operator_max, otherwise add host capacity or reduce worker limits"
- `MEMORY_EXHAUSTED`: "deploy fewer concurrent functions or increase host RAM; see /api/v1/system/metrics.json host.mem_*"
- `BUILD_QUEUE_FULL`: "wait for current builds to drain; consider raising NumCPU or staggering deploys"
- `WORKER_CRASHED`: "check stderr in the latest execution log; common causes: process.exit, OOM, syntax error in handler" — you do not have to guess which execution that was: the failing response itself carries `X-Orva-Execution-ID` (set for every invoke that reaches the sandbox, timeouts and crashes included), and the crashed run's stderr is stored under that same id, so pass it to `GET /api/v1/executions/{id}/logs`. If stderr names `setgroups`, `/proc/<pid>/setgroups`, or user-namespace initialization, re-run the installer rather than run the daemon as root; it validates and persists the safe nsjail fallback.
- `EGRESS_POLICY_UNAVAILABLE`: "see GET /api/v1/firewall/status (last_compile_error) — fix the offending rule, then POST /api/v1/firewall/resolve". nsjail's NSTUN stack is default-**allow**, so a missing policy would mean no egress filtering at all; Orva fails the invocation closed instead. `GET /api/v1/firewall/status` reports `enforced`, `policy_generation`, `policy_stale` and `unenforced_rules`.

## Backward-compatibility

Round F is **additive**:
- New codes added; no codes renamed.
- New optional envelope fields (`hint`, `retry_after_s`, `details`); JSON consumers ignoring unknown keys keep working.
- A few invocation failures shift HTTP status (e.g. handler `process.exit(1)` was 503 SANDBOX_ERROR, now 502 WORKER_CRASHED). Clients that retry on 503 but not on 5xx-other will stop retrying handler bugs — which is the correct behaviour. If you have a client that retries everything, it stays compatible.

## Implementation

Wire-level mapping lives in `backend/internal/server/handlers/errmap.go` (`invokeError`, `deployError`). Sentinel errors are defined alongside the code that raises them:

- `backend/internal/pool/pool.go`: `ErrManagerClosed`, `ErrPoolAtCapacity`, `ErrMemoryExhausted`
- `backend/internal/sandbox/limiter.go`: `ErrTooManyRequests`
- `internal/sandbox/worker.go`: `ErrWorkerExited`
- `internal/sandbox/sandbox.go`: `ErrEgressPolicyMissing`
- `internal/firewall/policy.go`: `ErrPolicyUnavailable`
- `internal/builder/queue.go`: `ErrQueueFull`, `ErrQueueStopping`
- `internal/proxy/proxy.go`: `ErrBodyTooLarge` — an inbound body over the cap,
  including the chunked case the middleware's `Content-Length` check cannot see
- `internal/sandbox/worker.go`: `ErrStreamTooLarge` — a streaming handler
  exceeding the 32 MiB buffer a **non**-streaming caller has to accumulate into
  (cron, jobs, function-to-function, inbound webhook). It has no `invokeError`
  case yet, so it currently surfaces as `SANDBOX_ERROR`.

Adding a new code: define a sentinel, return it from the relevant code path, add a case to `invokeError` (or `deployError`), append a row to the table above. A code a handler raises directly with `respond.Error` needs only the row. `TestErrorCatalogMatchesHandlers` (`backend/internal/server/error_catalog_test.go`) fails the build if a handler emits a code that has no row here, or a row has no emitter.
