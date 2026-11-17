# HTTP API reference

Management endpoints live under `/api/v1/`; invocation, MCP, metrics,
and OAuth discovery/authorization also expose the public paths called out
below. Management auth uses either:

- **API key**: `X-Orva-API-Key: orva_xxx...` header, or
  `Authorization: Bearer orva_xxx...` — both are accepted everywhere. Used by
  curl, CI, external callers.
- **Session cookie**: set by `POST /api/v1/auth/login`. Used by the dashboard.

A handful of endpoints are **cookie-only** and cannot be driven with a key,
because they act on the calling user rather than the instance:
`/auth/me`, `/auth/refresh`, `/auth/change-password`, `/auth/sessions*`, and
`/oauth/connected-apps*`.

API keys carry a permission set. The bootstrap admin key has all four:
`invoke`, `read`, `write`, `admin`. Operator-issued keys can be
narrowed.

Error envelope (every 4xx/5xx):

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

`Retry-After` HTTP header set in parallel when `retry_after_s` is
present. Full code catalog in [ERRORS.md](ERRORS.md).

## Auth

### `POST /api/v1/auth/onboard`
First-run only. Creates the admin user.

- **409 `ALREADY_SETUP`** — a user already exists.
- **401 `UNAUTHORIZED`** — the instance is already in use (operator-minted
  API keys, or deployed functions) and the caller presented no `admin` key.
- **503 `UNAVAILABLE`** — the setup state could not be read. It fails closed
  rather than assuming the instance is unclaimed.

A virgin instance onboards with no credentials, which is the documented
first-run flow: the auto-minted `bootstrap-admin` key does not count as use.
The gate exists because `CreateUser` is only ever called here, so an operator
who works exclusively through API keys never onboards — and unguarded, this
endpoint (exempt from the auth middleware, handing back a session cookie that
bypasses the permission model) would let anyone who can reach the port claim
a working instance.

```json
// request
{"username": "admin", "password": "AdminPass123!Secure"}
// response 201
{"user": {"id": "u_xxx", "username": "admin"}, "expires_at": "..."}
```

### `POST /api/v1/auth/login`
Sets the session cookie.

```json
{"username": "admin", "password": "..."}
```

### `GET /api/v1/auth/me`
Returns the current user (cookie-authed).

### `GET /api/v1/auth/status`
Reports whether any dashboard user exists. **Fails closed:** if the count
cannot be read it answers `{"has_user": true}`, so a transient database error
never advertises a claimable instance.
Returns `{"has_user": bool}` so the UI knows whether to route to
`/onboarding` or `/login`.

### `POST /api/v1/auth/refresh`
Mints a **new** session token, sets it as the cookie, and revokes the old
one — the previous cookie value stops working immediately. The lifetime is
`ORVA_SESSION_DAYS` (default 7).

### `POST /api/v1/auth/logout`
Invalidates the session.

### Account and session management

- `POST /api/v1/auth/change-password` — change the current user's password.
- `GET /api/v1/auth/sessions` — list active login sessions.
- `DELETE /api/v1/auth/sessions/{prefix}` — revoke a session by token prefix.
- `GET /api/v1/oauth/connected-apps` — list authorized OAuth clients.
- `DELETE /api/v1/oauth/connected-apps/{id}` — revoke a connected client.
- `DELETE /api/v1/oauth/clients/{client_id}` — retire the application itself (instance-wide): revokes its grants, drops pending authorization codes, and blocks re-authorization without fresh consent.

## Functions

### `POST /api/v1/functions`
Create a function record.

```json
{
  "name": "my-fn",
  "runtime": "node",        // node | python
  "entrypoint": "handler.js", // optional, defaults match the runtime
  "memory_mb": 128,
  "cpus": 1,
  "timeout_ms": 30000,
  "env_vars": {"NODE_ENV": "production"},
  "network_mode": "none",     // none (default) | egress
  "sdk": false                // explicit SDK permission; defaults to false
}
```

`network_mode` controls per-function network access:

- `none` (default) — isolated net namespace, loopback only. DNS / TCP /
  UDP all blocked. Best for pure-compute handlers.
- `egress` — userspace TCP/UDP stack via nsjail `--user_net`. Function
  can call external HTTPS APIs. Host interfaces stay isolated. The
  instance-wide egress policy is compiled into every such sandbox; a
  blocked destination gives the handler `ECONNREFUSED`.

Toggling on an existing function via `PUT /api/v1/functions/{id}`
drains the warm pool so the next invocation picks up the new mode.

`sdk` independently grants the function access to Orva KV, function calls,
jobs, cron declarations and custom spans. Set `sdk: true` for SDK-dependent
handlers; current adapters use the private worker channel even in `none`
network mode. New functions default to `false`, including new egress functions.
On the first migration, existing egress functions are backfilled to `true`;
subsequent restarts do not undo an explicit revocation. Enabling outbound
networking does not enable SDK permission, and disabling SDK permission does
not disable outbound networking.

Update `sdk` using an explicit boolean (`false` revokes permission). Changes
hard-drain existing workers so stale worker capabilities cannot be reused.
In-flight invocations may finish on their retired workers; their credentials
remain valid until release and reaping. New invocations use the new permission.
The field is returned in individual function and list records. This permission
governs the function's outgoing SDK access, not whether it may receive a call:
an SDK-enabled caller can invoke an active callee whose own `sdk` is false.

### `GET /api/v1/functions`
List all functions. Optional `?status=active|inactive`, `?runtime=...`.

### `GET /api/v1/functions/{id}`
Single function record. Accepts a function **id or name**.

### `PUT /api/v1/functions/{id}`
Partial update. Whitelisted fields: `name`, `description`, `entrypoint`,
`timeout_ms`, `memory_mb`, `cpus`, `env_vars`, `network_mode`, `sdk`,
`max_concurrency`, `concurrency_policy`, `auth_mode`, `rate_limit_per_min`,
`status`.

`status` accepts only `active` | `inactive`. Setting `inactive` causes
`POST /fn/<id>` to return 409 NOT_ACTIVE — and, since this release, also
blocks function-to-function `orva.invoke()` calls, which previously ran an
inactive function anyway.

`entrypoint` is **the file you authored** and the build pipeline never
rewrites it. When a runtime compiles — TypeScript through `tsc` — the build
output is recorded separately in `run_entrypoint`; empty means "same as
`entrypoint`". Send only `entrypoint` when creating or updating a function.

`auth_mode` accepts `none` | `platform_key` | `signed` and governs how
`POST /fn/<id>` is authorized. Under `platform_key` the caller must present a
key carrying the **`invoke`** permission (or a session cookie); a key scoped
to `read`/`write` only gets 403. `concurrency_policy` accepts `reject` | `queue`
and decides what happens once `max_concurrency` is reached (`reject` returns
429 FUNCTION_BUSY). `rate_limit_per_min` is a per-client-IP cap; exceeding it
returns 429 RATE_LIMITED with `Retry-After: 60`. The client IP is the TCP peer
address — `X-Forwarded-For` decides it only when `ORVA_TRUSTED_PROXY=true`, so
behind an un-declared proxy every caller shares one bucket.

`filename` defaults to the function's own `entrypoint` before falling back to
`handler.js` / `handler.py`. It used to hardcode those two, so a function
created with any other entrypoint deployed a file the builder then refused to
find. `filename` and every `extras` key must be a contained relative path;
traversal returns 400 `VALIDATION`.

`extras` is REST-only — the MCP `deploy_function_inline` tool does not accept
it.

### `DELETE /api/v1/functions/{id}`
Removes the function row, its executions, KV rows, jobs and the on-disk code
and build cache. Irreversible.

The delete is chunked so a function with millions of executions does not stall
other functions' writes. It first sets the function's `status` to `deleting`
(visible in `GET /api/v1/functions`, the dashboard and the `function.updated`
event): invocation, cron and job firing stop at once, and `PUT`, rollback and
redeploy answer `409 CONFLICT`. The name stays taken until the delete finishes.
The call stays synchronous and answers `200 {"status":"deleted","id":...}` when
done, which can take minutes for a very large function (about 11,000 rows per
second was measured on an idle server; 300,000 executions took about 143 s while
the server handled 800 requests/s for another function, and the reply is held
open past the server's write timeout); a client or proxy that times out does not stop it, because
the delete runs detached from the request, bounded at 30 minutes. If it does not
finish (that bound, a restart or a database error) the function stays
`deleting`: repeat the `DELETE` to resume, and the server also resumes it at
startup. A repeated `DELETE` for a function that is already gone answers `404`.

### `POST /api/v1/functions/{id}/deploy-inline`
Deploy from JSON.

```json
{
  "code": "module.exports = async () => ({ok:true});",
  "filename": "handler.js",
  "dependencies": "{\"dependencies\":{\"lodash\":\"^4.17.21\"}}",
  "extras": {"tsconfig.json": "{...}"}  // optional support files written alongside the handler
}
```

`dependencies` is optional and is written **verbatim** as the dependency file for
the runtime: the contents of a `package.json` for `node` (a JSON document, not a
bare `name@version`), or the contents of a `requirements.txt` for `python`
(`"requests==2.31.0\n"`). A value that is not valid for that file fails the
build.

Returns 202 with the deployment record. Build runs asynchronously.

### `POST /api/v1/functions/{id}/deploy`
Deploy from a tarball (multipart upload).

### `POST /api/v1/functions/{id}/rollback`
Roll back to a prior version.

```json
{"deployment_id": "019df210-1234-7000-8000-deadbeef0001"}    // or {"code_hash": "abc..."}
```

Rollback **promotes an existing deployment** — it does not append a new one,
so the version history does not grow and the deployment count is unchanged.
`active_deployment_id` moves to the target, the `current` symlink is
retargeted, and `run_entrypoint` is re-derived from the promoted version's own
directory rather than restored from its snapshot (a snapshot written before
that column existed carries no value, and applying that absence points a
compiled TypeScript version back at its `.ts` source). Returns 200 with the
**promoted** deployment record.

Returns 410 `VERSION_GCD` if the target version was pruned by the GC, and 400
if the target is not a succeeded deployment of this function, or if it is
already active.

### `GET /api/v1/functions/{id}/source`
Returns the function's current code + dependencies as JSON. Used by
the Editor view.

### `DELETE /api/v1/functions/{id}/build-cache`
Drops the function's persistent installer cache (npm / pip). The next deploy that
builds a new version re-downloads its dependencies from scratch; a redeploy of
identical source is served from the existing version directory and is unaffected.
Use when a cached dependency is suspect; it never touches deployed versions or
the running function.

### `GET /api/v1/functions/{id}/diff?from=<dep_id>&to=<dep_id>&format=json|unified`
Compares the handler source + dependency manifest between two past
**succeeded** deployments. Both `from` and `to` must be deployment UUIDs
belonging to this function.

- `format=json` (default — dashboard `Compare versions` view) returns
  `{from, to, files: [{path, kind:"handler"|"manifest", before, after,
  added, removed}]}`. `before` / `after` carry the raw file bytes so
  the browser-side merge viewer can compute its own hunks.
- `format=unified` returns `text/x-diff` with git-style hunks per file
  (`--- a/path` / `+++ b/path` / `@@ …`). Consumed by `orva diff`. Hunk headers
  are `patch`-compatible: each hunk's new-side start accounts for the lines
  earlier hunks added or removed.

Errors:
- 400 `VALIDATION` if `from` and `to` are equal, belong to different
  functions, or aren't in status `succeeded`.
- 404 `VERSION_NOT_FOUND` if either deployment ID is unknown; details
  include the requested ID.
- 410 `VERSION_GCD` if either version's source tree was pruned by the
  GC. `details.available_hashes` lists the surviving on-disk versions
  so the caller can retry against a still-archived target.

### `GET /api/v1/functions/{id}/deployments`
Deployment history for a function. Optional `?limit=N` (default 50).

## Invoke

### `POST /fn/{id}/{path}`
Calls the function. `id` is the function's UUID (the same value returned in the `id` field by GET /api/v1/functions).
(e.g. function `019df200-7b00-7e00-9c00-aab1cd2e3f40` → URL
`/fn/019df200-7b00-7e00-9c00-aab1cd2e3f40`). Method,
headers, body, query, and `path` (everything after `/{id}`) are all
passed to the handler as `event`.

Response is whatever the handler returns. HTTP status is 200 unless
the handler throws or returns an AWS-shape `{statusCode, body}`.

Custom routes (e.g. `/webhooks/stripe`) reach the same handler — see
the routes section below.

Pending invocation capacity is derived from detected memory and file-descriptor
limits rather than a fixed request count. One function can use at most three
quarters of that pending budget, leaving room for another function. The server
also reserves memory before reading a public request body (unknown-length
bodies are charged at the configured body cap). A saturated pool waits at most
two seconds; a pool that can still grow or has workers starting may wait up to
the ten-second adapter-readiness budget plus one scaler tick (twelve seconds).
Exhausted capacity or an expired wait returns `429 INVOCATION_QUEUE_FULL` with
`Retry-After: 1`; no user code ran.
HTTP, inbound-webhook, replay, and internal SDK invocations also reserve a
critical execution-record slot before the sandbox runs. If the SQLite writer
cannot supply one within five seconds,
Orva returns `429 STORAGE_BACKPRESSURE` with `Retry-After: 1`; no user code ran.
This prevents an accepted function's completion record from being lost just
because the writer queue filled after the function executed.
The function's `timeout_ms` starts after admission, when a worker is ready.
Replay capture begins only after a worker has been acquired, so rejected
requests have no replay body even when capture is enabled.

**Response headers.** Every invoke carries `X-Orva-Execution-ID`, including a
timeout or a sandbox failure, which answer from the invoke handler rather than
the proxy. Pass it to `GET /api/v1/executions/{id}/logs` to read what the
handler wrote — `stderr` for `console.log`/`print`, and `log_entries` for
`orva.log.*`. It used to be set only on the success path, so the runs whose
logs you most need — a crashed sandbox, a timeout — returned no id and their
stored stderr was unreachable.

A non-streaming response that the handler produced — whatever status it
returned — also carries `X-Orva-Duration-MS`. A streaming response carries
`X-Orva-Ttfb-Ms` instead, because its headers flush on the first chunk and a
duration written there would measure time-to-first-byte; total streaming
duration is on the execution row. Neither duration header is set when the
invoke handler answers for itself.

## Deployments

### `GET /api/v1/deployments/{id}`
Single deployment record.

### `GET /api/v1/deployments/{id}/logs`
Build logs for that deployment.

### `GET /api/v1/deployments/{id}/stream`
Server-sent events stream of build progress. Live tail; closes when
the build reaches a terminal state (`succeeded` | `failed`).

## Executions

### `GET /api/v1/executions`
List recent invocations. Optional `?function_id=...`, `?limit=N` (default 50,
**capped at 1000**), `?since=`/`?until=` (RFC3339).

> `since`/`until` are compared as **times**, not as text. They used to be raw
> string comparisons against a differently-formatted stored value, so a
> "last 1 hour" query returned nothing at all and `orva executions prune`
> over-deleted by up to a day at the boundary.

`GET /api/v1/functions` is likewise capped at 1000.

### `GET /api/v1/executions/{id}`
Single execution row (status, duration, cold_start flag).

### `GET /api/v1/executions/{id}/logs`
The function's stderr from this invocation.

### Execution lifecycle and observability

- `GET /api/v1/executions/{id}/request` — return the captured invocation request.
- `DELETE /api/v1/executions/{id}` — delete one execution record. Returns
  **404** for an id that does not exist; it used to report success.
- `POST /api/v1/executions/bulk-delete` — delete matching execution records.
  Responds `{deleted, not_found, failed}`. `deleted` counts rows that
  actually existed: it used to count attempts, so 1000 unknown ids reported
  `{deleted: 1000, failed: 0}`.
- `POST /api/v1/executions/{id}/replay` — replay a captured request.
- `GET /api/v1/traces` and `GET /api/v1/traces/{id}` — list trace-wide summaries with opaque stable cursors and inspect the complete causal waterfall.
- `GET /api/v1/functions/{id}/baseline` — return the function's trace baseline.
- `GET /api/v1/activity` — list the operator activity feed. Filters:
  `source`, `q`, `status_min` (≥ n; use `400` for errors), `status_max`
  (≤ n; use `399` for successes), `since`/`until` (unix millis), `limit`.
  Paginate with `cursor` **and** `cursor_id` from the previous response's
  `next_cursor` / `next_cursor_id` — the feed is ordered `(ts, id)` and a
  timestamp-only cursor silently skips every row sharing the last row's
  millisecond.

## Secrets

### `GET /api/v1/functions/{id}/secrets`
List secret keys for a function. Values are not returned (encrypted at
rest; only injected into the sandbox at spawn time).

### `POST /api/v1/functions/{id}/secrets`
Upsert. Body: `{"key": "STRIPE_KEY", "value": "sk_..."}`. Triggers a
pool refresh so the next invocation sees the new value.

### `DELETE /api/v1/functions/{id}/secrets/{key}`
Remove. Triggers a pool refresh.

## Function data, fixtures, and schedules

Operator-facing function resources use the normal API-key/session auth:

- `GET /api/v1/functions/{id}/kv` — `prefix`, `limit` (default 200, max
  1000) and `cursor`. Returns `next_cursor`; `truncated` reflects whether one
  was issued. Page with it rather than narrowing the prefix.
- `GET|PUT|DELETE /api/v1/functions/{id}/kv/{key}`
- `POST /api/v1/functions/{id}/kv/{key}/incr`
- `POST /api/v1/functions/{id}/kv/{key}/cas`
- `GET|POST /api/v1/functions/{id}/fixtures`
- `GET|PUT|DELETE /api/v1/functions/{id}/fixtures/{name}`
- `GET|POST /api/v1/functions/{id}/cron`
- `PUT|DELETE /api/v1/functions/{id}/cron/{schedule_id}`
- `GET /api/v1/cron` — list schedules across all functions.

KV keys must be non-empty UTF-8 up to 256 characters and values must be valid
JSON up to 64 KiB. Internal SDK batches accept at most 100 operations and are
atomic. For put/increment/CAS, omitted `ttl_seconds` preserves an existing
expiry (new keys remain persistent), zero clears expiry, a positive value sets
or refreshes it, and a negative value returns `400 VALIDATION`.

SDK operations use the inherited worker channel, not HTTP endpoints.
The former `/api/v1/_kv` and `/api/v1/_internal` routes are removed.
Caller function identity comes from the channel; dispatch-scoped operations
use a host-issued lease. KV works at module scope. Function calls, jobs,
cron declarations and custom spans require an active dispatch. Cron declarations
remain caller-owned and capped at 25 per function; operator-created schedules
are not counted against that cap.

The public jobs API below still requires ordinary operator authentication;
legacy SDK tokens are not accepted.

## Jobs

### Opt-in cron IAM controls (normal router remains unchanged)

Explicit `CronHandler{CronEnabled: true, CronIAM: service}` uses the existing
`GET /cron`, `GET|POST /functions/{fn_id}/cron` and
`PUT|DELETE /functions/{fn_id}/cron/{id}` shapes under `/api/v1`. A scratch
constructor additionally binds `POST /functions/{fn_id}/cron/{id}/resume`.
These controls are not enabled by normal boot, router or schema construction.

Inspection still requires `read`; create/update/enable/disable/delete/resume
require live `admin`, not `read` plus `admin`. Trusted authentication supplies
the API-key/OAuth/session reference; the service rechecks current credentials at
commit. Request-supplied origin, creator, operator or approval fields are rejected.
Create accepts the existing cron expression/timezone/enabled/payload body and
assigns durable operator origin. It does not impersonate a function.

Update and resume require decimal-string `expected_config_revision` and
`expected_authority_generation`. Delete requires those as query parameters plus
`confirm=true`. Missing, malformed or stale CAS, or ineligible state, returns
`409 CONFLICT`; authority denial returns `403 IAM_DENIED`, invalid configuration
`400 VALIDATION`, missing schedule `404 NOT_FOUND`, and unavailable storage
`503 INTERNAL`. Resume accepts no edited configuration, preserves original
provenance, revalidates current original rights and schedules only the next future
fire. Enabling or regranting cannot resume suspended/quarantined work. Adoption
and restore remain unavailable.

List responses include `cron_controls: true` and advisory `can_manage`. Each row
includes authority state/reason, host origin/source, immutable creation revisions,
admission revisions, authority generation and config revision (decimal strings).
Unknown historic schedules quarantine. The dashboard distinguishes Enabled from
current authority, and disables mutations on stale/failed snapshots. A conflict
requires an explicit refresh/review; refresh never acknowledges a failed mutation.

- `POST /api/v1/jobs` — enqueue a background job.
- `GET /api/v1/jobs` and `GET /api/v1/jobs/{id}` — list or inspect jobs.
- `POST /api/v1/jobs/{id}/retry` — retry a failed job.
- `DELETE /api/v1/jobs/{id}` — delete a job.

### Staged IAM jobs fixture (not normal serving API)

The internal, explicitly initialized jobs fixture additionally binds
`POST /api/v1/jobs/{id}/resume`. It requires live `admin`, as do enqueue, retry
and delete in that fixture; reads retain `read`. Normal routes and their
permission gates are unchanged until coordinated cut-over.

Fixture job responses add `authority_state` (`active`, `suspended`,
`quarantined`), bounded `authority_reason`, host-set `origin_kind`, immutable
creator/operator provenance, creation revisions, admission revisions and an
authority generation. Revisions/generation are decimal strings. List responses
include advisory `async_controls: true` and `can_manage`; the server still
reauthorizes each mutation. Enqueue rejects unknown origin/authority fields.

Resume rechecks the original creator UUID/current target rights and only
reactivates suspended pending work. Terminal/quarantined jobs are not reopened.
Retry keeps original provenance/idempotency scope; it is not operator adoption.
Regrant alone does not resume old work. Denied management returns `403
IAM_DENIED`, stale/ineligible state `409 CONFLICT`, and unavailable storage
`503 INTERNAL`, without dispatch. See `backend/internal/workloadasync/README.md`
for the fixture's exclusions and target-deletion retention limit.

## Inbound webhooks — the public receiver

`POST /webhook/<id>` is the public endpoint the inbound-webhook rows below
configure, and it is **not** under `/api/v1/`, so the API-key middleware never
sees it. Authentication is the signature scheme chosen per hook (GitHub,
Stripe, Slack, generic HMAC, or none), verified before the function is invoked.
A handler reads the outcome with `orva.webhook.parse(event)`; `verified` is set
from a server-controlled header that inbound requests cannot forge.

## Webhooks

Inbound function endpoints:

- `GET|POST /api/v1/functions/{id}/inbound-webhooks`
- `GET|PUT|DELETE /api/v1/functions/{id}/inbound-webhooks/{webhook_id}`

Outbound event delivery:

- `GET|POST /api/v1/webhooks`
- `GET|PUT|DELETE /api/v1/webhooks/{id}`
- `POST /api/v1/webhooks/{id}/test`
- `GET /api/v1/webhooks/{id}/deliveries` — accepts `?limit=` (default 100,
  max 500). It used to be hardcoded to 100 with no parameter.
- `POST /api/v1/webhooks/deliveries/{id}/retry`

## Routes

Map a custom URL to a function so external callers don't need the
function ID.

### `GET /api/v1/routes`
List custom routes.

### `POST /api/v1/routes`
```json
{"path": "/webhooks/stripe", "function_id": "019df200-7b00-7e00-9c00-aab1cd2e3f40", "methods": "POST"}
```

`methods` accepts `*` for all methods or comma-separated (`GET,POST`).
Reserved prefixes (`/api/`, `/auth/`, `/web/`, `/_orva/`, `/fn/`, `/mcp/`,
`/webhook/`) are rejected — Orva serves those itself, so a route under one
could only shadow the platform or never fire.

### `DELETE /api/v1/routes?path=/webhooks/stripe`
Remove a route.

## Pool config

Per-function autoscaler tuning.

### `GET /api/v1/pool/config?function_id=...`
Read the row.

### `PUT /api/v1/pool/config`
```json
{
  "function_id": "019df200-7b00-7e00-9c00-aab1cd2e3f40",
  "min_warm": 2,
  "max_warm": 32,
  "idle_ttl_seconds": 600,
  "scale_to_zero": false
}
```

Fields are partial — unspecified ones keep the prior value (or default
for new rows). Defaults are min 1, max 0 (automatic host-derived capacity),
idle TTL 600 seconds, and scale-to-zero off. A positive `max_warm` is an
operator upper bound, never permission to exceed host CPU/memory or function
concurrency; `0` removes that operator cap. Existing positive overrides remain
unchanged on upgrade. Pool Controller v2 derives desired capacity from demand;
the removed `target_concurrency` field returns `400 VALIDATION`.

## API keys

### `GET /api/v1/keys`
List keys. Returns prefixes, names, last_used_at, expires_at. **Never**
returns the plaintext key.

### `POST /api/v1/keys`
```json
{
  "name": "ci-deployer",
  "permissions": ["invoke", "read", "write"],   // optional, defaults to all 4
  // expires_in_days is capped at 36500. Above that, time.Duration overflows
  // and used to mint a key whose expires_at was already in the past.
  "expires_in_days": 90                          // or expires_at: "ISO timestamp"
}
```

Returns the plaintext key **once**. Save it immediately — it's not
recoverable.

### `DELETE /api/v1/keys/{id}`
Revoke a key.

## Channels

A channel bundles N deployed functions under a name and a static bearer
token. Presenting that token at `/mcp` exposes ONE MCP tool per
bundled function (invoke-only) and nothing else — no Orva-management
surface. Token format: `orva_chn_<32 hex>`. Channel tokens are
explicitly rejected at every `/api/v1/*` endpoint (401); they're
MCP-only.

Auth header at `/mcp` — channel tokens accept either form, same as
operator API keys:

```
Authorization: Bearer orva_chn_<token>     # spec-standard, recommended
X-Orva-API-Key: orva_chn_<token>           # parity with the REST API
```

The REST endpoints below (CRUD on `/api/v1/channels`) are operator-managed
and require an API key carrying the **`admin`** permission, or a session
cookie — channel tokens themselves cannot manage channels.

> **Changed:** these used to accept `read`/`write`. A channel token is a
> long-lived bearer credential whose tools bypass the target function's
> `auth_mode`, so a write-scoped key could mint itself a credential that
> outranked it. Minting one is now gated like minting an API key.

### `GET /api/v1/channels`
List channels. Returns `{channels: [...]}` with name, description,
prefix, function_count, last_used_at, expires_at, created_at.

### `POST /api/v1/channels`
```json
{
  "name": "support-bot",
  "description": "Support workflow toolkit",   // optional
  "function_ids": ["<uuid>", "<uuid>"],
  "expires_in_days": 30                         // optional; or expires_at: "ISO timestamp"
}
```
Returns the plaintext token **once** in the `token` field. Save it
immediately — it's not recoverable. A `function_ids` entry that does not exist
is rejected with 400 / `INVALID_FUNCTION`, an id listed twice with 400 /
`VALIDATION`, and two functions whose names snake_case to the same MCP tool name
with 400 / `TOOL_NAME_COLLISION`.

### `GET /api/v1/channels/{id}`
Detail with the bundled function set + per-function description overrides.

### `PATCH /api/v1/channels/{id}`
Update name / description / expires_at. Function set is unchanged.

### `PUT /api/v1/channels/{id}/functions`
```json
{
  "function_ids": ["<uuid>", ...],
  "descriptions": {"<uuid>": "tool description override"}   // optional
}
```
Replaces the function set wholesale. Junction descriptions on
overlapping function IDs are preserved unless explicitly overridden. The same
`INVALID_FUNCTION`, `VALIDATION` (duplicate id) and `TOOL_NAME_COLLISION` checks
apply, and a rejected request leaves the existing set unchanged.

### `POST /api/v1/channels/{id}/rotate`
Re-issues the bearer token. Returns `{token: "orva_chn_..."}` once;
the previous token stops working immediately.

### `DELETE /api/v1/channels/{id}`
Cascade — removes the channel and every junction row.

## System

### `GET /api/v1/system/health`
Returns 200 and `{"status": "healthy", ...}` when orvad is up, alongside
`version`, `commit`, `build_time`, `image`, `uptime_seconds`, and `database`,
`sandbox`, `host` and `writer` sub-objects. `image` echoes the `ORVA_IMAGE`
stamp — the published container image sets it; a bare-metal install has no
image and reports it empty. Returns **503** with
`{"status": "degraded"}` when the 2-second database ping fails. Used by Docker
HEALTHCHECK and load balancers — match on `healthy`, not `ok`.

`sandbox.user_namespace` is `enabled` or `capability_fallback` and
`sandbox.resource_limits` is `cgroup_v2` or `rlimit_only`. `sandbox.warnings`
contains operator-visible caveats. These are readiness diagnostics, not liveness
gates: a database-healthy instance remains HTTP 200 so a load balancer does not
flap while the dashboard explains the reduced resource-limit mode.

### `GET /api/v1/system/metrics`
Prometheus text format.

### `GET /api/v1/system/metrics.json`
Structured metrics used by the dashboard. `latency_ms` contains p50/p95/p99
for the dispatch/proxy interval of recent invocations; it stops before
execution-record enqueue and does not represent the full HTTP response.
`response_latency_ms` contains p50/p95/p99 for the complete public invoke
handler, including admission rejection and record enqueue. Both are rolling
windows of at most 8,192 samples across functions, in integer milliseconds.
Neither includes reverse-proxy, network, or client-side time. Compare a public
load test with `response_latency_ms`, not `latency_ms`, and remember that the
two windows can contain different sets of requests.

Prometheus also scrapes the unauthenticated `GET /metrics` path.
In health's writer object, `critical_queue_bytes`, `activity_queue_bytes`, and
`telemetry_queue_bytes` include admitted jobs currently queued, being
committed, or held for retry. They return to zero only after those jobs have
committed or been explicitly failed/shed. The depth fields count channel
entries only, so depth zero alone is not a drain signal. `dropped_activity`
counts lost operator activity rows; it is a subset of `dropped_telemetry`,
which counts all lost best-effort writes. Activity has a separate admission
lane from optional replay capture, logs, and spans, but can still drop if its
own bounded lane or the SQLite writer is saturated. The Prometheus metrics
are `orva_writer_dropped_activity_total` and
`orva_writer_dropped_telemetry_total`; `orva_writer_queue_depth` has
`critical`, `activity`, and `telemetry` priority labels. Deleting a function
removes its execution history, including completions that arrive during the
delete. `deleted_function_writes` counts function-owned async jobs deliberately
discarded after deletion; it is separate from `critical_failures` and
`dropped_telemetry`. Prometheus exposes the same count as
`orva_writer_deleted_function_writes_total`.
The `orva_writer_batch_attempts_total`, `orva_writer_committed_jobs_total`,
`orva_writer_connection_wait_seconds_total`,
`orva_writer_statement_seconds_total`, `orva_writer_commit_seconds_total`,
`orva_writer_queue_wait_seconds_total`, and
`orva_writer_queue_wait_samples_total` counters are split by `priority`
(`critical`, `activity`, `telemetry`). They measure the normal batched writer
path only; failure-isolation savepoint recovery is excluded from committed-job
and queue-wait counts. Divide each cumulative time by its matching attempt or
sample count over the same scrape interval, not by process uptime. Queue wait
starts when the writer job is submitted for admission, so it can include
waiting for a channel slot as well as SQL and commit time; it is not a
SQLite-only execution timer.
The per-function `orva_pool_service_p95_ms` gauge and `service_p95_ms` in
pool telemetry measure worker occupancy from successful acquire to release,
including response processing and streaming. They exclude queue wait and
cold-start time; they are not end-to-end HTTP latency.

### Backup, storage, and firewall administration

Everything in this section requires the **`admin`** permission — including
the firewall **reads** (`GET /firewall/rules`, `/firewall/status`,
`/firewall/dns`).

> **Changed:** the `/api/v1/firewall` surface used to be `read`/`write`.
> Egress blocklists and the DNS every sandbox resolves through are
> instance-wide security state, so a deploy-scoped key could repoint every
> sandbox's resolver. Breaking for write-scoped automation touching these
> endpoints.

- `GET /api/v1/backup` and `POST /api/v1/restore` — download or restore an instance backup.
- `GET /api/v1/system/storage` — inspect disk/database usage.
- `POST /api/v1/system/vacuum` — compact the SQLite database.
- `GET|POST /api/v1/firewall/rules`
- `PUT|DELETE /api/v1/firewall/rules/{rule_id}`
- `POST /api/v1/firewall/resolve`
- `GET|PUT /api/v1/firewall/dns`

### Firewall status

`GET /api/v1/firewall/rules` returns `{"rules": [...], "status": {...}}`, and
`POST /api/v1/firewall/resolve` returns the same `status` object. It describes
the compiled sandbox egress policy (see
[`SECURITY.md`](SECURITY.md#sandbox-egress-policy)):

```json
{
  "ipv4": ["169.254.0.0/16", "93.184.216.34/32"],
  "ipv6": ["fd00:ec2::254/128"],
  "hostname_map": {"example.com": ["93.184.216.34"]},
  "backend": "nstun",
  "enforced": true,
  "policy_generation": "3f9a1c0d4b7e2851",
  "policy_rule_counts": {"v4": 8, "v6": 2, "allow": 7, "reject": 3},
  "policy_stale": false,
  "last_success_at": "2026-08-09T11:02:14Z",
  "unenforced_rules": [
    {"id": 12, "value": "*.corp.com",
     "reason": "wildcard hostnames are not enforceable: egress policy matches IP/CIDR, not DNS names. Use a CIDR or an exact hostname."}
  ]
}
```

| Field | Meaning |
|---|---|
| `ipv4` / `ipv6` | The REJECT prefixes actually present in the compiled policy — derived from what is enforced, not from the raw table |
| `hostname_map` | `hostname` rule → the addresses currently resolved for it |
| `backend` | Always `"nstun"`. Enforcement is nsjail NSTUN rules loaded per sandbox |
| `enforced` | A policy has compiled and is in use. **`false` means egress functions refuse to spawn**, since NSTUN's no-match default is ALLOW |
| `policy_generation` | 16 hex chars — a hash of the exact bytes handed to nsjail. Changes only when enforcement changes |
| `policy_rule_counts` | Compiled rule counts: `v4`, `v6`, `allow` (carve-outs), `reject` (blocklist) |
| `policy_stale` | A recompile failed and the last known-good generation is still in force. Read with `last_compile_error` |
| `pending_recycle` | Warm workers are still running the previous generation. Enforcement is live for new spawns but not yet universal — without this field the status reads "enforcing" while old workers keep the old policy |
| `last_compile_error` | Present only on failure |
| `last_success_at` | RFC 3339 timestamp of the last successful compile |
| `unenforced_rules` | Stored rules deliberately **not** compiled, each with a `reason`. Wildcard rules land here. Surfaced so the UI never implies a rule is in force when it isn't |

`nftables_available` was **removed, with no compatibility alias.** The field
described a host-firewall mechanism that no longer exists, and reporting it as
permanently `true` would be a lie in the API. Clients that read it should read
`enforced` (is a policy in force) and `backend` instead.

Creating or enabling a `wildcard` rule now fails with `400 VALIDATION`;
wildcards cannot be expressed as packet rules. Existing wildcard rows are left
untouched and reported in `unenforced_rules`.

### `GET /api/v1/events`
Server-sent events stream of:

- `event: metrics` — periodic 5-second snapshots
- `event: execution` — every new invocation
- `event: deployment` — every status / phase change

Browser EventSource automatically reconnects. Cookie auth (API-key
auth not supported on EventSource — browsers can't set custom
headers).

## Runtimes & syscalls

### `GET /api/v1/runtimes`
List supported runtimes.

### `GET /api/v1/syscalls`
The seccomp policy catalog. Useful for the dashboard's "what is this
function allowed to do" tooltip.

## AI assistant

The in-product agentic chat (the dashboard's **AI** section). Requires
a configured provider (BYO key). Streaming endpoints emit
`text/event-stream`; everything else is JSON. All paths require `admin`.

### `POST /api/v1/ai/chat`
Send a user message and stream the assistant turn. Body carries
`conversation_id` (or omit to start one), `content`, and the selected
provider/model/thinking level. Response is SSE: `message_start`,
`delta` (text), `thinking`, `tool_call`, `tool_result`,
`awaiting_approval`, `message_end`, `error`. Long pre-token gaps are
kept alive with `: ping` comment frames.

### `GET /api/v1/ai/conversations`
List conversations (most-recently-updated first).

### `POST /api/v1/ai/conversations`
Create an empty conversation.

### `DELETE /api/v1/ai/conversations`
Delete every conversation and cascade-delete their messages and tool calls in
one operation. Returns `{"deleted": N}`. Responds with `409
CONVERSATION_BUSY` without deleting anything when any conversation has a turn
in progress.

### `GET /api/v1/ai/conversations/{id}`
Fetch one conversation with its full message + tool-call timeline.

### `PATCH /api/v1/ai/conversations/{id}`
Rename (`{"title": "..."}`) or archive (`{"archived": true}`).

### `DELETE /api/v1/ai/conversations/{id}`
Delete a conversation and all its messages + tool calls (cascade).

### `GET /api/v1/ai/conversations/{id}/messages`
List messages, optionally `?since_seq=N` for incremental loads.

### `POST /api/v1/ai/conversations/{id}/regenerate`
Truncate the last assistant turn and re-run it. SSE, same frames as
`/chat`.

### `POST /api/v1/ai/conversations/{id}/messages/{mid}/edit`
Replace a user message's content, **truncate everything after it**, and
re-run the turn. SSE. (There is no branching history — the tail is
discarded.)

### `DELETE /api/v1/ai/conversations/{id}/messages/{mid}`
Delete a message and every message + tool call after it (truncate by
`seq`).

### `POST /api/v1/ai/tool-calls/{id}/approve`
### `POST /api/v1/ai/tool-calls/{id}/reject`
Resolve a tool call that is `awaiting_approval` and resume the stream
(approve) or skip it (reject). SSE.

### `GET /api/v1/ai/providers`
### `POST /api/v1/ai/providers`
### `DELETE /api/v1/ai/providers/{id}`
List, upsert, and remove provider configs (provider, label, base URL,
API key). Keys are encrypted at rest with the same cipher as function
secrets and **never returned** in responses.

### `GET /api/v1/ai/providers/{id}/models`
List the models the configured provider/endpoint reports.

### `GET /api/v1/ai/settings`
### `PUT /api/v1/ai/settings`
Read/update assistant settings: default provider/model, thinking level,
and approval policy (`all_writes` / `destructive_only` / `auto`). The
`max_tool_iterations` response field is retained for API compatibility but is
an internal runaway-work guard fixed at `25`; values supplied by `PUT` are
ignored and normalized to `25`.

### `PUT /api/v1/ai/selection`
Persist the dashboard's active provider/model/thinking selection.

## MCP and OAuth public endpoints

`POST /mcp` is the Streamable HTTP MCP endpoint. Operator API keys expose
the management tools; channel tokens expose only the functions bundled into
that channel.

The transport is **stateless** and speaks MCP **2026-07-28**, negotiating down
for older clients (`server/discover` reports every supported version). Three
things follow that are visible on the wire:

- **No `initialize` handshake is required.** A 2026-07-28 request carries
  `protocolVersion` and `clientCapabilities` in `params._meta` (`clientInfo` is
  optional), plus `Mcp-Protocol-Version` and `Mcp-Method` headers — and, for
  `tools/call` / `resources/read` / `prompts/get`, an `Mcp-Name` header equal to
  the name in the body. The headers let a proxy route on the operation without
  parsing the body, so one that disagrees with the body is rejected (`-32020`),
  not ignored. Declaring any `protocolVersion` in `_meta` commits the request to
  that validated path; the legacy handshake is reached by omitting it, and still
  returns `200`.
- **No `Mcp-Session-Id` is issued**, and none is read. Any request may be served
  by any instance.
- **`GET /mcp` and `DELETE /mcp` return `405`.** There is no session to resume
  or terminate, and no long-lived server→client stream to attach to.

List results carry the new `ttlMs` and `cacheScope` hints. Orva returns
`cacheScope: "private"` — the tool catalog is scoped to the caller's permissions
and to their channel, so it is never safe for an intermediary to share — and
`ttlMs: 0`, because the catalog changes on any deploy, channel edit, or
permission change.

OAuth-capable MCP clients discover and authorize through:

- `GET /.well-known/oauth-protected-resource[/mcp]`
- `GET /.well-known/oauth-authorization-server[/mcp]`
- `GET /.well-known/openid-configuration[/mcp]`
- `POST /register`
- `GET|POST /oauth/authorize`
- `POST /oauth/token`
- `POST /oauth/revoke`
# Staged named Function IAM management

The source-aware REST adapter is **scratch-only and unregistered in normal
serving**. Its explicit DTOs, routes, authorization, errors and version/source
semantics are documented in [IAM_MANAGEMENT.md](IAM_MANAGEMENT.md).
Do not interpret client command availability as production route activation.
