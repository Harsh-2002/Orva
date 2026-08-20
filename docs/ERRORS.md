# Orva error catalog

Every API error returns the same envelope:

```json
{
  "error": {
    "code": "POOL_AT_CAPACITY",
    "message": "function pool at capacity for 019df200-7b00-7e00-9c00-aab1cd2e3f40",
    "request_id": "req_abc...",
    "hint": "inspect pool limiting_reason; raise max_warm only for operator_max",
    "retry_after_s": 5,
    "details": {
      "function_id": "019df200-7b00-7e00-9c00-aab1cd2e3f40",
      "function_name": "stripe-webhook"
    }
  }
}
```

Fields beyond `code` and `message` are optional and may be absent. Transient errors set both the `Retry-After` HTTP header and the `error.retry_after_s` body field; the header value matches the body and is RFC 7231-compliant for clients that don't parse the body.

## Where these apply

The invocation codes below — `FUNCTION_BUSY`, `MEMORY_EXHAUSTED`,
`EGRESS_POLICY_UNAVAILABLE`, `TIMEOUT`, `SHUTTING_DOWN` and the rest — reach
callers on **every** invoke path: `POST /fn/{id}`, custom routes, replay,
function-to-function `orva.invoke()`, and the inbound-webhook trigger.

That is new. Only `POST /fn/{id}` used the shared mapper; the other four
flattened every failure to a bare `503 POOL_ERROR`, so an SDK retrying on
those paths could not tell a concurrency cap it should back off from a host
OOM it should not.

## Code reference

### 4xx — client errors

| code | HTTP | when | retry? |
|---|---|---|---|
| `INVALID_JSON` | 400 | malformed request body | no — fix the payload |
| `VALIDATION` | 400 | required field missing or invalid value | no — fix the request |
| `UNAUTHORIZED` | 401 | missing or invalid API key/session, or invalid/stale SDK credential | no — re-auth or let the worker restart |
| `FORBIDDEN` | 403 | authenticated but lacks the required permission | no |
| `SDK_SCOPE_VIOLATION` | 403 | a signed worker credential attempted another KV/cron namespace or an execution/trace it does not own | no — fix the SDK request; caller scope cannot be elevated |
| `NOT_FOUND` | 404 | function or route doesn't exist | no |
| `FUNCTION_NOT_FOUND` | 404 | function lookup miss on a name-or-ID path (diff endpoint) | no |
| `VERSION_NOT_FOUND` | 404 | deployment row not in DB (e.g. `orva diff` with a bad `--from`/`--to`) | no |
| `VERSION_GCD` | 410 | rollback / diff target's version tree was pruned by the GC; `details.available_hashes` lists survivors | no — pick a still-archived hash |
| `METHOD_NOT_ALLOWED` | 405 | method not in the route's allowed list | no |
| `NOT_ACTIVE` | 409 | function status is `error` or `inactive` | no — redeploy or activate |
| `PAYLOAD_TOO_LARGE` | 413 | body exceeds `cfg.Server.MaxBodyBytes` (default 6 MB). Two paths: a `Content-Length` above the cap is refused up front; a **chunked** body with no `Content-Length` is refused when the read hits the cap. The chunked case used to be silently truncated and handed to the function with a 200. | no — send `Content-Length`, split the upload, or raise the cap |
| `CHECKPOINT_BUSY` | 409 | `POST /system/vacuum` could not checkpoint the WAL because another connection holds a read lock. Previously the checkpoint's busy result was discarded and VACUUM ran against a stale WAL. | yes — retry shortly |
| `CONFLICT` | 409 | `PUT /functions/{id}` renaming onto a name that already exists. Used to surface as a 500. | no — pick another name |
| `NOT_FOUND` | 404 | `DELETE /executions/{id}` for an id that does not exist. Used to report success. | no |
| `TOO_MANY_REQUESTS` | 429 | host-wide concurrency cap reached during TryAcquire grace | **yes** — back off briefly |
| `RATE_LIMITED` | 429 | rate limit exceeded — per-function invoke limit (`rate_limit_per_min`, per client IP) or too many login attempts (per client IP) | **yes** — `Retry-After: 60` |
| `FUNCTION_BUSY` | 429 | function at its own `max_concurrency` cap under the `reject` policy | **yes** — `Retry-After: 1`, or raise `max_concurrency` / switch the policy to `queue` |

### 5xx — server / platform errors

| code | HTTP | when | retry? |
|---|---|---|---|
| `INTERNAL` | 500 | unmapped server fault | no — file a bug with `request_id` |
| `BUILD_ERROR` | 500 | the deploy's build failed; the function is left in `error` status | no — check `/api/v1/deployments/<id>/logs` for the npm/pip error, fix, redeploy |
| `WORKER_CRASHED` | 502 | adapter exited unexpectedly (`process.exit`, OOM-kill, syntax error in handler) | no — fix the function |
| `BUILDING` | 503 | first deploy in flight; no prior code to serve | **yes** — `Retry-After: 5` |
| `BUILD_QUEUE_FULL` | 503 | build queue at channel capacity | yes — `Retry-After: depth × 30s` |
| `POOL_AT_CAPACITY` | 503 | function pool at `dynamicMax` and ctx fired waiting | yes — `Retry-After: 5` |
| `MEMORY_EXHAUSTED` | 503 | host memory budget at 80% reservation | yes — `Retry-After: 30` |
| `EGRESS_POLICY_UNAVAILABLE` | 503 | a `network_mode=egress` function was invoked while no sandbox egress policy had compiled; the spawn is refused rather than run unfiltered | yes — `Retry-After: 10` (the manager recompiles every 10s), but a bad rule must be fixed first |
| `KV_UNAVAILABLE` | 503 | an atomic KV batch could not commit; no batch writes were retained | yes — retry the complete batch |
| `INSUFFICIENT_DISK` | 503 | not enough free disk on the data volume to start the build | no — free space or lower `system_config.min_free_disk_mb` |
| `SHUTTING_DOWN` | 503 | server is closing down | no, on this host — redirect |
| `SANDBOX_ERROR` | 503 | unmapped sandbox / dispatch failure | no — investigate |
| `TIMEOUT` | 504 | function exceeded its `timeout_ms` | no — raise it or optimize handler |
| `CLIENT_DISCONNECTED` | 499 | client closed the connection mid-request | n/a (never reaches client) |

## Operator-actionable hints

Every transient error includes a `hint` field telling the operator what to change. Examples:

- `POOL_AT_CAPACITY`: "inspect pool limiting_reason; raise max_warm only for operator_max, otherwise add host capacity or reduce worker limits"
- `MEMORY_EXHAUSTED`: "deploy fewer concurrent functions or increase host RAM; see /api/v1/system/metrics.json host.mem_*"
- `BUILD_QUEUE_FULL`: "wait for current builds to drain; consider raising NumCPU or staggering deploys"
- `WORKER_CRASHED`: "check stderr in the latest execution log; common causes: process.exit, OOM, syntax error in handler"
- `EGRESS_POLICY_UNAVAILABLE`: "see GET /api/v1/firewall/status (last_compile_error) — fix the offending rule, then POST /api/v1/firewall/resolve". nsjail's NSTUN stack is default-**allow**, so a missing policy would mean no egress filtering at all; Orva fails the invocation closed instead. `GET /api/v1/firewall/status` reports `enforced`, `policy_generation`, `policy_stale` and `unenforced_rules`.

## Backward-compatibility

Round F is **additive**:
- New codes added; no codes renamed.
- New optional envelope fields (`hint`, `retry_after_s`, `details`); JSON consumers ignoring unknown keys keep working.
- A few invocation failures shift HTTP status (e.g. handler `process.exit(1)` was 503 SANDBOX_ERROR, now 502 WORKER_CRASHED). Clients that retry on 503 but not on 5xx-other will stop retrying handler bugs — which is the correct behaviour. If you have a client that retries everything, it stays compatible.

## Implementation

Wire-level mapping lives in `internal/server/handlers/errmap.go` (`invokeError`, `deployError`). Sentinel errors are defined alongside the code that raises them:

- `internal/pool/pool.go`: `ErrManagerClosed`, `ErrPoolAtCapacity`, `ErrMemoryExhausted`
- `internal/sandbox/limiter.go`: `ErrTooManyRequests`
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

Adding a new code: define a sentinel, return it from the relevant code path, add a case to `invokeError` (or `deployError`), append a row to the table above.
