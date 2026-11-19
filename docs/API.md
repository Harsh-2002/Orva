# HTTP API reference

## Admission-closed managed server integration (internal)

This separate Linux composition is not selected by normal `orva serve`. It
mounts managed handlers without falling back to legacy mutation routes.
Authenticated `GET /api/v1/iam/capabilities` returns `{"managed":true}` only
on this composition.

- `POST /api/v1/iam/functions` accepts `name`, `runtime` (`node` or `python`)
  and optional `entrypoint`. It requires live read and write permissions.
  `201` returns `id`, `name`, `runtime` and `status`. The new function has
  an unbound empty policy and a private SDK channel, not SDK grants.
- `POST /api/v1/deployments/{id}/install` requires an exact deployment ID
  and live read/admin authority. It returns `200` with `artifact` and
  `review_required:true`. Installation does not review or grant authority.
- Function permissions, named policies, configuration, and pause/resume
  use the managed contracts below. The opt-in MCP catalog excludes legacy
  tools and channel credentials. Managed Chat uses that same tool registry.
- Use MCP `tools/list` to see the tools available to the current credential.
  The normal server exposes 73 tools to a key with all four permissions and
  28 to a read-only key. With the managed composition above, those counts are
  39 and 10 respectively. Normal channel credentials expose only their
  bundled functions; managed MCP does not accept channel credentials.
- A configuration write atomically records the same sealed pair against the
  new generation and leaves the function inactive. After a same-boot restart,
  install the exact current deployment ID to recover that pending generation.
  Installation still requires a separate permissions review.
- `DELETE /api/v1/functions/{fn_id}` requires an exact UUIDv7 and live
  read/admin authority. It reserves metadata history and invalidates the
  current Factory before retiring the pool. It does not remove sealed assets.
  The managed listener also mounts the work-history routes below.
- An invocation whose authorized function version no longer matches the
  acquired serving generation returns `409 NOT_ACTIVE` without dispatch.
- `GET /api/v1/functions/{fn_id}/source` reads the authored entrypoint and
  dependency manifest from the current sealed deployment; it never reads
  a legacy `current` symlink. Pending and paused deployments remain readable.

## Internal opt-in configuration publication (not normal routes)

`workloadconfig.HTTPAdapter` is an explicitly registered Linux scratch adapter.
Normal function/secret handlers retain legacy behavior; no boot schema or normal
route is activated. See
[`workloadconfig/README.md`](../backend/internal/workloadconfig/README.md).

All mutation bodies include `expected` with **seven string fields**:
`function_version`, `artifact_revision`, `artifact_epoch`, `generation`,
`policy_revision`, `authority_epoch`, `approved_artifact`. Revisions are canonical
positive decimal strings; generation is the exact durable UUID, and stored
approval is only a CAS token. Also supply `current_artifact`, the actual installed
Factory digest, except for installation. Owner is the exact path UUID, not a name.

| Optional endpoint | Other required fields |
| --- | --- |
| `GET /api/v1/functions/{id}/configuration` | READ-only metadata inspection; no body |
| `PUT /api/v1/functions/{id}/configuration/environment` | `environment`: complete string-to-string environment |
| `PUT /api/v1/functions/{id}/configuration/settings` | `environment`, plus complete `settings` |
| `PUT /api/v1/functions/{id}/configuration/secrets` | `secret: {key, value}` |
| `DELETE /api/v1/functions/{id}/configuration/secrets` | `secret: {key}` |
| `POST /api/v1/functions/{id}/configuration/install` | Only `expected`; installs a fresh Factory, **not approval** |

`settings` has exactly `timeout_ms`, `memory_mb`, `cpus`, `network_mode`, `sdk`,
`max_concurrency`, `concurrency_policy`, `auth_mode`, `rate_limit_per_min`.
It cannot change code, runtime, deployment identity, entrypoint or status.
Timeout is 1–86,400,000 ms, memory 16–1,048,576 MiB, CPUs finite 0–1024,
concurrency/rate nonnegative; existing network/auth/concurrency-mode validators
remain authoritative. Snapshot and concrete Factory bounds may refuse more
conservatively before publication.

Mutation requires live ADMIN for any outbound/inbound or retained source grant,
quarantine or review-required state; truly empty, review-free owners preserve
ordinary WRITE. Installation requires ADMIN. These writes do **not** additionally
require READ. A trusted authenticator supplies token-free API-key, session or
OAuth references; credentials/scopes/audience/expiry are rechecked transactionally.
There is no authorable actor, permission or artifact-approval claim.

The READ-only inspection returns `expected`, review/quarantine flags,
environment/secret entry counts, and the actual installed `current_artifact`
(empty if unavailable) with code/runtime digests when known. It returns no
environment values, ciphertext or secret names, and never converts durable
metadata into a current Factory. Writes can use deliberately supplied exact
expectations without being given READ; inspection grants no write authority.

Bodies are at most 1 MiB; duplicate/unknown fields, invalid UTF-8, unpaired UTF-16,
null string values and inexact revisions deny. Success is 200/no-store, returning
only the seven fresh expectation fields and `review_required:true`; no environment, plaintext,
ciphertext or secret-name list is returned. Installation instead returns
`{current_artifact,review_required:true}`. Errors are 400 `VALIDATION`,
401 `UNAUTHORIZED`, 403 `FORBIDDEN`, 409 `IAM_CONFLICT`, or sanitized 503
`IAM_UNAVAILABLE`. Never automatically replace expectations and retry.

Mutation commits generation/configuration/secrets, review-required deny,
policy/authority epoch and audit together; invalidates the old current Factory
before ACK; then retires/cancels outside locks. Follow with explicit installation
and the existing source-aware policy review of the **new installed artifact**.
The old current observer cannot approve the new durable generation. Busy workers
may still hold old injected values, but lose protected SDK access. Configuration
uses the actual installed pair, including after an explicit queued deployment
install. An uninstalled or superseded queued generation cannot be installed
through the configuration endpoint. This is not secret recall, production
cut-over or reboot recovery.
## Internal opt-in queued deployment publication

Normal API construction is unchanged. Scratch `FunctionHandler.DeployIAM` uses
shared managed capture/submit for upload and inline deployments. Required but
missing service denies, not legacy fallback (inline: 503 `UNAVAILABLE`).
Optional inline accepts only `code`, optional `filename` and empty `dependencies`
as strict original JSON (256 KiB; duplicate/unknown/metadata/invalid UTF-8 or
surrogate input: 400 `INVALID_JSON`). Extras, dependencies and TypeScript are
unqualified. Returns 202 `{deployment_id,function_id,status:"queued"}` after
authorized durable capture, or 403 `FORBIDDEN` for denied/conflicting submission.
The optional multipart upload is capped at 50 MiB and accepts exactly one `code`
file with no other metadata fields; duplicate/unknown form parts are rejected.
Already-inactive targets require explicit activation and fresh submission.
`succeeded` with phase `review_required` means inactive DENY publication, not
invocation readiness. Separate host Factory installation/current-artifact review
remain required; there is no public Install route or normal router switch.
See [`builder/README.md`](../backend/internal/builder/README.md) for filesystem
separation, transaction ordering and retained-failure recovery limits.

The optional history deletion root can include `managed_sources`: bounded
owner/policy UUID, owner revision/epoch, pin/enabled, before/after membership and
digest metadata. It is inspection-only, contains no policy body or payload, and
survives the function FK cascade. Its opt-in coordinator shares the managed
service's source CAS, owner/definition locks and live-admin transaction. Normal
routes remain unchanged; see `backend/internal/workloadpolicy/OWNER_DELETION.md`.

## Internal opt-in deleted-work history (not normal routes)

The scratch `workloadhistory.HTTPAdapter` registers POST
`/api/v1/work-history/{list,get,capacity,purge,reissue}` only when explicitly supplied with
the service and a trusted live authenticator. Normal router construction does
not register these endpoints. READ is required for list/get/capacity; ADMIN is required
for purge/reissue, rechecked against live credentials. See
[`workloadhistory/README.md`](../backend/internal/workloadhistory/README.md).

- `list`: `{target_id?, after?:{kind,id}, limit:1..100}`; returns metadata entries
  in `(kind,id)` order, including a `reserved` flag. Omit target to inspect all.
- `get`: `{kind,id}`; returns one metadata entry. Archives are non-executable;
  payload is unavailable.
- `capacity`: `{}`; returns `rows_used`, `bytes_used`, `rows_reserved`,
  `bytes_reserved`, `max_rows` and `max_bytes` as decimal strings,
  `purge_eligibility_days:90`, and advisory `can_manage`. These are canonical
  serialized metadata budgets, not database file size; reservations are included
  in the hard 100,000-row / 64-MiB caps. Capability is not a durable grant.
- `purge`: `{selected:[{kind,id}], confirm:true}`; 1–32 exact records, no
  duplicates, no reservations, and only records eligible after 90 days.
- `reissue`: `{source:{kind:"job"|"cron",id}, target_id, payload, reviewed:true,
  confirm:true, ...fresh_config}`; job config requires `scheduled_at` and
  `max_attempts` (1–100). Cron config requires `name`, `expression`, `timezone`
  and `enabled`; enabled cron starts at a fresh future time. The target is an
  exact new UUID, never a name. Returns `{new_id,origin:"operator"}`.

Success is 200 with `Cache-Control:no-store`. Invalid, duplicate/unknown-field,
ineligible/missing source or unavailable target-authority requests return 400
`VALIDATION`; live permission denial returns 403 `FORBIDDEN`; history capacity
refusal returns 409 `CONFLICT`. Missing authentication returns 401
`UNAUTHORIZED`; an explicitly registered but unavailable adapter returns 503
`UNAVAILABLE`. There is no old-work resume/retry/adopt endpoint.

With the opt-in registry deletion hook, function DELETE first reserves history
before pool retirement or deleting status. A preparation failure may leave the
function fully active; an interrupted committed deletion remains `deleting` and
is resumable. The existing function-delete handler reports refused/incomplete
deletion as `INTERNAL` and directs the caller to inspect current state before
retrying. Nil-hook legacy deletion keeps its existing behavior.

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

In the optional managed-deployment fixture (not normal boot), both path owner
and `deployment_id` must be exact UUIDs. `code_hash`, `latest`, unknown and
duplicate fields are not accepted. HTTP 200 includes `id`, `deployment_id`,
`function_id`, `version`, `code_hash`, `status: "succeeded"`,
`phase: "review_required"` and `function_status: "inactive"`. This acknowledges
promotion, not readiness. Current configuration and history are preserved;
host Factory installation and policy review are separate. Invalid input returns
400, live operator denial 403, ineligible/current targets and pause 409, and a
missing service or unavailable sealed pair 503. No legacy fallback is attempted.

### Managed workload pause and resume (opt-in only)

The scratch `WorkloadLifecycleHandler` can explicitly register
`POST /api/v1/functions/{id}/pause` and `POST /api/v1/functions/{id}/resume`.
The normal router exposes neither route. Both use the exact canonical function
UUID, not a name, and require a JSON object of at most 1024 bytes with exactly
these fields:

| Field | Required value |
|---|---|
| `generation` | Exact canonical durable generation UUID |
| `policy_revision` | Exact positive decimal string, at most signed 64-bit |
| `authority_epoch` | Exact positive decimal string, at most signed 64-bit |
| `confirm` | JSON boolean `true` |

Staged precondition: use the separately enabled authenticated
`GET /api/v1/functions/{id}/configuration` with READ to review these three
fields from `expected` for this owner. Inspect again after pause and after
conflict; never infer the next epoch. Neither mutation does an automatic read
on the operator's behalf, accepts extra fields, or retries stale work.

Pause requires live WRITE for empty, review-free, non-quarantined policy sources
and live ADMIN for privileged sources. Its HTTP 200 acknowledgement contains
`function_id`, `function_status: "inactive"` and `operator_paused: true`.
Resume requires live ADMIN and an exact current sealed pair. Its HTTP 200
acknowledgement contains `function_id`, `function_status: "inactive"`,
`operator_paused: false`, `installation_required: true`, `review_required: true`.
This acknowledges a new inactive deny generation, not resumed execution or
standing work. Fresh host Factory installation and explicit source-aware
policy review are separate.

Responses are `Cache-Control: no-store`. Invalid or unconfirmed input is
400 `VALIDATION`, missing operator authentication is 401 `UNAUTHORIZED`,
denied live authority is 403 `FORBIDDEN`, stale or ineligible state is
409 `CONFLICT`, and unavailable composition, sealed pair or storage is
503 `UNAVAILABLE`. No legacy fallback is attempted.

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
# Staged opt-in exposure mutation contract

`workloadtrigger` is an internal candidate, not normal serving behavior. No
normal router, boot schema or default MCP dependency enables it. Existing
nil-dependency route/webhook APIs remain unchanged. Inspection/list/sign
permissions remain unchanged.

Explicit fixtures may supply `Triggers` to `RouteHandler`,
`InboundWebhookHandler` and MCP dependencies, and register the inspection adapter:

`GET /api/v1/exposures?kind=route|inbound&key=<path-or-trigger-UUID>&function_id=<literal-owner-UUID>`

Inspection requires live READ and returns `version` (decimal string), `expected`
(owner UUID → `{policy_revision, authority_epoch}`, both decimal strings), and
the optional route/webhook projection. For a new inbound trigger omit `key`;
for route rebind supply the intended new owner so inspection includes both
existing and intended owners. Inspection returns no signature secret and does
not authorize a subsequent write.

All staged mutation bodies include `version` and `expected` from inspection:

- `POST /api/v1/routes`: add `path`, literal `function_id`, optional `methods`.
- `DELETE /api/v1/routes?path=...`: add `confirm:true`.
- `POST /api/v1/functions/{fn_id}/inbound-webhooks`: add `name`, optional
  `signature_format`, `signature_header`, `active`.
- `PUT /api/v1/functions/{fn_id}/inbound-webhooks/{id}`: optional editable fields
  as above; omitted values retain their previous values.
- `DELETE /api/v1/functions/{fn_id}/inbound-webhooks/{id}`: add `confirm:true`.

REST URL selectors are not accepted again in the body. Inbound owner and secret
are immutable; rotation remains delete/recreate. Create returns the existing
`inbound_webhook`, one-time `secret` and `trigger_url` envelope; update returns
the existing row projection; route and delete acknowledgments retain their shapes.
No plaintext secret is serialized by update/get/list.

When explicitly enabled, external MCP/shared AI use the same core and existing
`set_route`, `delete_route`, `create_inbound_webhook`, `delete_inbound_webhook`
names with this CAS contract. They additionally expose `inspect_exposure` and
`update_inbound_webhook`. MCP arguments are flat: include `function_id` for
inbound operations and `id` for update/delete; route delete includes `path`.
Delete requires `confirm:true`. Normal catalogs are unchanged.

The staged MCP/shared-AI adapter preserves the legacy MCP result projection:
create returns exactly `inbound_webhook` (an `InboundWebhookView`, including
`function_name` and nested relative `trigger_url`) plus the one-time `secret`;
there is no top-level `trigger_url`. Update returns only `inbound_webhook` with
the same masked view; delete returns only `id` and `status`. Route operations
retain `RouteOpOutput`. Display-label lookup follows successful mutation and
does not add a READ requirement, change authorization, or gate a committed ACK.
REST response envelopes and the shared mutation core are unchanged.

Mutation needs current WRITE or ADMIN on truly empty review-free owners;
any stored/attached/retained grant, inbound SDK acceptance, review requirement
or quarantine requires live ADMIN for both old and new route owners. READ is
independent, not an implied mutation prerequisite. Commit rechecks token-free
API-key/session/OAuth provenance, permissions, revocation, expiry and audience.

Malformed/duplicate/unknown/null/oversize/invalid-Unicode fields return
`400 VALIDATION`; live credential/permission failure returns `403 FORBIDDEN`;
stale source/mapping/version returns `409 IAM_CONFLICT`. Missing required owner,
IAM state or storage is a generic `503 IAM_UNAVAILABLE`, without name adoption
or fallback. Requests are bounded to 32 KiB and revisions stay decimal strings.
Never retry automatically after conflict: inspect and explicitly resubmit.

These are public exposure edits, not SDK policy changes: revision/epoch,
artifact approval, grant sources and current Factories are preserved. Queued
publication, status/rollback, full lifecycle and production cut-over remain
separate unimplemented/unqualified boundaries; see the package README.

## Custom routes on the internal managed listener

`StartManaged` mounts the staged route and inbound-webhook REST subset above:
`GET /api/v1/exposures` takes `kind=route` with a literal `key`, or
`kind=inbound` with a literal owner `function_id` (omit `key` to obtain the
create-time expectations). `POST /api/v1/routes` and
`DELETE /api/v1/routes?path=...` require the staged CAS bodies. Inspection
returns `route_published` / `inbound_published` separately from the stored
mapping (which may be `null`). They describe publication only, not the target's
runtime readiness. `GET /api/v1/routes` lists up to 256 mappings, each with its
`version` (decimal string) and `published`, and a `truncated` flag; it needs
live READ. Normal serving is unchanged. Managed MCP/Chat route and webhook
tools are not part of this slice.

Inbound webhooks use `/api/v1/functions/{fn_id}/inbound-webhooks` with exact
owner and trigger UUIDs (names are rejected with `400 VALIDATION`):
`GET` lists one owner's triggers plus its `expected` tokens (secret-free, each
with `version` and `published`); `GET .../{id}` returns `inbound_webhook`,
`published`, `version` and `expected`; `POST`, `PUT` and `DELETE` use the staged
CAS bodies above, with `confirm:true` for delete. Create's body `version` is
`"0"`; each create mints a fresh trigger UUID. There is no managed `/sign`
endpoint: the secret is returned once on create and the caller signs.

`POST /webhook/{id}` is served only for a published mapping: the stored row must
match its publication on exposure version, owner, signature format, header,
active flag and secret digest. Unknown, unpublished, legacy, raw-edited and
restored rows return `404 NOT_FOUND`; a published but disabled trigger returns
`410 INACTIVE`. Signatures use the existing formats (`hmac_sha256_hex`,
`hmac_sha256_base64`, `github`, `stripe`, `slack`); a mismatch is
`401 SIGNATURE_INVALID` and no function runs. A verified request runs only on the
current managed Factory generation (the same pool, SDK policy and function
status checks as `/fn/`), and never reaches a legacy fallback. `405` for
non-POST. Secrets appear only in the create response.

Custom invocation resolves one published mapping: exact path, then longest
literal `/prefix/*`. A method mismatch returns `405 METHOD_NOT_ALLOWED` without
trying a broader route; `HEAD` requires explicit permission or `*`. The same
per-function authentication and managed invocation path as `/fn/` apply.
`/` and the roots and descendants of `/api`, `/auth`, `/web`, `/_orva`, `/fn`,
`/mcp`, `/webhook`, `/metrics`, `/register`, `/oauth`, and `/.well-known` are
reserved even when their method or endpoint is absent. Use clean decoded path
patterns; encoded separators do not enter custom dispatch.

An unattested or restored mapping is inspectable but cannot win lookup.
Restore retains mappings and their version counters while clearing public
publication, for routes and inbound webhooks alike. Reinspect and explicitly
republish via the same CAS API to reactivate a route (`POST /api/v1/routes`) or
a webhook (`PUT` with no field changes). Any non-managed write to a webhook row
also unpublishes it without changing its counter. This does not install or review the target workload.
