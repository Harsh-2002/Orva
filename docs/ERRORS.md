# Orva error catalog

Every API error returns the same envelope:

```json
{
  "error": {
    "code": "POOL_AT_CAPACITY",
    "message": "function pool at capacity for 019df200-7b00-7e00-9c00-aab1cd2e3f40",
    "request_id": "req_abc...",
    "hint": "raise pool_config.max_warm via PUT /api/v1/pool/config",
    "retry_after_s": 5,
    "details": {
      "function_id": "019df200-7b00-7e00-9c00-aab1cd2e3f40",
      "function_name": "stripe-webhook"
    }
  }
}
```

Fields beyond `code` and `message` are optional and may be absent. Transient errors set both the `Retry-After` HTTP header and the `error.retry_after_s` body field; the header value matches the body and is RFC 7231-compliant for clients that don't parse the body.

## Code reference

### 4xx — client errors

| code | HTTP | when | retry? |
|---|---|---|---|
| `INVALID_JSON` | 400 | malformed request body | no — fix the payload |
| `VALIDATION` | 400 | required field missing or invalid value | no — fix the request |
| `UNAUTHORIZED` | 401 | missing or invalid API key / session | no — re-auth |
| `FORBIDDEN` | 403 | authenticated but lacks the required permission | no |
| `NOT_FOUND` | 404 | function or route doesn't exist | no |
| `METHOD_NOT_ALLOWED` | 405 | method not in the route's allowed list | no |
| `NOT_ACTIVE` | 409 | function status is `error` or `inactive` | no — redeploy or activate |
| `PAYLOAD_TOO_LARGE` | 413 | body exceeds `cfg.Server.MaxBodyBytes` (default 6 MB) | no — split or raise the cap |
| `TOO_MANY_REQUESTS` | 429 | host-wide concurrency cap reached during TryAcquire grace | **yes** — back off briefly |
| `RATE_LIMITED` | 429 | per-function rate limit (reserved; not yet emitted) | yes |

### 5xx — server / platform errors

| code | HTTP | when | retry? |
|---|---|---|---|
| `INTERNAL` | 500 | unmapped server fault | no — file a bug with `request_id` |
| `BUILD_FAILED` | 502 | last build was bad and there's no prior version to fall back to | no — fix code/deps and redeploy |
| `WORKER_CRASHED` | 502 | adapter exited unexpectedly (`process.exit`, OOM-kill, syntax error in handler) | no — fix the function |
| `BUILDING` | 503 | first deploy in flight; no prior code to serve | **yes** — `Retry-After: 5` |
| `BUILD_QUEUE_FULL` | 503 | build queue at channel capacity | yes — `Retry-After: depth × 30s` |
| `POOL_AT_CAPACITY` | 503 | function pool at `dynamicMax` and ctx fired waiting | yes — `Retry-After: 5` |
| `MEMORY_EXHAUSTED` | 503 | host memory budget at 80% reservation | yes — `Retry-After: 30` |
| `SHUTTING_DOWN` | 503 | server is closing down | no, on this host — redirect |
| `SANDBOX_ERROR` | 503 | unmapped sandbox / dispatch failure | no — investigate |
| `TIMEOUT` | 504 | function exceeded its `timeout_ms` | no — raise it or optimize handler |
| `CLIENT_DISCONNECTED` | 499 | client closed the connection mid-request | n/a (never reaches client) |

## Operator-actionable hints

Every transient error includes a `hint` field telling the operator what to change. Examples:

- `POOL_AT_CAPACITY`: "raise pool_config.max_warm via PUT /api/v1/pool/config or reduce client concurrency"
- `MEMORY_EXHAUSTED`: "deploy fewer concurrent functions or increase host RAM; see /api/v1/system/metrics.json host.mem_*"
- `BUILD_QUEUE_FULL`: "wait for current builds to drain; consider raising NumCPU or staggering deploys"
- `WORKER_CRASHED`: "check stderr in the latest execution log; common causes: process.exit, OOM, syntax error in handler"

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
- `internal/builder/queue.go`: `ErrQueueFull`, `ErrQueueStopping`

Adding a new code: define a sentinel, return it from the relevant code path, add a case to `invokeError` (or `deployError`), append a row to the table above.
