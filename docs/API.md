# HTTP API reference

Management endpoints live under `/api/v1/`; invocation, MCP, metrics,
and OAuth discovery/authorization also expose the public paths called out
below. Management auth uses either:

- **API key**: `X-Orva-API-Key: orva_xxx...` header. Used by curl, CI,
  external callers.
- **Session cookie**: set by `POST /api/v1/auth/login`. Used by the dashboard.

API keys carry a permission set. The bootstrap admin key has all four:
`invoke`, `read`, `write`, `admin`. Operator-issued keys can be
narrowed.

Error envelope (every 4xx/5xx):

```json
{
  "error": {
    "code": "POOL_AT_CAPACITY",
    "message": "function pool at capacity for 019df200-7b00-7e00-9c00-aab1cd2e3f40",
    "request_id": "req_abc",
    "hint": "raise pool_config.max_warm via PUT /api/v1/pool/config",
    "retry_after_s": 5,
    "details": {"function_id": "019df200-7b00-7e00-9c00-aab1cd2e3f40", "current": 16, "limit": 16}
  }
}
```

`Retry-After` HTTP header set in parallel when `retry_after_s` is
present. Full code catalog in [ERRORS.md](ERRORS.md).

## Auth

### `POST /api/v1/auth/onboard`
First-run only. Creates the admin user. Returns 409 if a user already
exists.

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
Returns `{"has_user": bool}` so the UI knows whether to route to
`/onboarding` or `/login`.

### `POST /api/v1/auth/refresh`
Rotates the cookie's expiry forward by 7 days.

### `POST /api/v1/auth/logout`
Invalidates the session.

### Account and session management

- `POST /api/v1/auth/change-password` — change the current user's password.
- `GET /api/v1/auth/sessions` — list active login sessions.
- `DELETE /api/v1/auth/sessions/{prefix}` — revoke a session by token prefix.
- `GET /api/v1/oauth/connected-apps` — list authorized OAuth clients.
- `DELETE /api/v1/oauth/connected-apps/{id}` — revoke a connected client.

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
  "network_mode": "none"      // none (default) | egress
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

### `GET /api/v1/functions`
List all functions. Optional `?status=active|inactive`, `?runtime=...`.

### `GET /api/v1/functions/{id}`
Single function record.

### `PUT /api/v1/functions/{id}`
Partial update. Whitelisted fields: `name`, `description`, `entrypoint`,
`timeout_ms`, `memory_mb`, `cpus`, `env_vars`, `network_mode`,
`max_concurrency`, `concurrency_policy`, `auth_mode`, `rate_limit_per_min`,
`status`.

`status` accepts only `active` | `inactive`. Setting `inactive` causes
`POST /fn/<id>` to return 409 NOT_ACTIVE.

`auth_mode` accepts `public` | `platform_key` | `signed` and governs how
`POST /fn/<id>` is authorized. `concurrency_policy` accepts `reject` | `queue`
and decides what happens once `max_concurrency` is reached (`reject` returns
429 FUNCTION_BUSY). `rate_limit_per_min` is a per-client-IP cap; exceeding it
returns 429 RATE_LIMITED with `Retry-After: 60`.

### `DELETE /api/v1/functions/{id}`
Removes the row + the on-disk versions dir. Irreversible.

### `POST /api/v1/functions/{id}/deploy-inline`
Deploy from JSON.

```json
{
  "code": "module.exports = async () => ({ok:true});",
  "filename": "handler.js",
  "dependencies": "lodash@^4.17.21"  // optional, becomes package.json or requirements.txt
}
```

Returns 202 with the deployment record. Build runs asynchronously.

### `POST /api/v1/functions/{id}/deploy`
Deploy from a tarball (multipart upload).

### `POST /api/v1/functions/{id}/rollback`
Roll back to a prior version.

```json
{"deployment_id": "019df210-1234-7000-8000-deadbeef0001"}    // or {"code_hash": "abc..."}
```

Returns 200 with a synthetic deployment row of `source: "rollback"`.
Returns 410 `VERSION_GCD` if the target version was pruned by the GC.

### `GET /api/v1/functions/{id}/source`
Returns the function's current code + dependencies as JSON. Used by
the Editor view.

### `GET /api/v1/functions/{id}/diff?from=<dep_id>&to=<dep_id>&format=json|unified`
Compares the handler source + dependency manifest between two past
**succeeded** deployments. Both `from` and `to` must be deployment UUIDs
belonging to this function.

- `format=json` (default — dashboard `Compare versions` view) returns
  `{from, to, files: [{path, kind:"handler"|"manifest", before, after,
  added, removed}]}`. `before` / `after` carry the raw file bytes so
  the browser-side merge viewer can compute its own hunks.
- `format=unified` returns `text/x-diff` with git-style hunks per file
  (`--- a/path` / `+++ b/path` / `@@ …`). Consumed by `orva diff`.

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
List recent invocations. Optional `?function_id=...`, `?limit=N`.

### `GET /api/v1/executions/{id}`
Single execution row (status, duration, cold_start flag).

### `GET /api/v1/executions/{id}/logs`
The function's stderr from this invocation.

### Execution lifecycle and observability

- `GET /api/v1/executions/{id}/request` — return the captured invocation request.
- `DELETE /api/v1/executions/{id}` — delete one execution record.
- `POST /api/v1/executions/bulk-delete` — delete matching execution records.
- `POST /api/v1/executions/{id}/replay` — replay a captured request.
- `GET /api/v1/traces` and `GET /api/v1/traces/{id}` — list and inspect traces.
- `GET /api/v1/functions/{id}/baseline` — return the function's trace baseline.
- `GET /api/v1/activity` — list the operator activity feed.

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

- `GET /api/v1/functions/{id}/kv`
- `GET|PUT|DELETE /api/v1/functions/{id}/kv/{key}`
- `POST /api/v1/functions/{id}/kv/{key}/incr`
- `POST /api/v1/functions/{id}/kv/{key}/cas`
- `GET|POST /api/v1/functions/{id}/fixtures`
- `GET|PUT|DELETE /api/v1/functions/{id}/fixtures/{name}`
- `GET|POST /api/v1/functions/{id}/cron`
- `PUT|DELETE /api/v1/functions/{id}/cron/{schedule_id}`
- `GET /api/v1/cron` — list schedules across all functions.

The `/_kv` and `/_internal` route families are sandbox-SDK transport
endpoints authenticated with invocation-scoped credentials. They are not an
operator API and should not be called with long-lived API keys.

## Jobs

- `POST /api/v1/jobs` — enqueue a background job.
- `GET /api/v1/jobs` and `GET /api/v1/jobs/{id}` — list or inspect jobs.
- `POST /api/v1/jobs/{id}/retry` — retry a failed job.
- `DELETE /api/v1/jobs/{id}` — delete a job.

## Webhooks

Inbound function endpoints:

- `GET|POST /api/v1/functions/{id}/inbound-webhooks`
- `GET|PUT|DELETE /api/v1/functions/{id}/inbound-webhooks/{webhook_id}`

Outbound event delivery:

- `GET|POST /api/v1/webhooks`
- `GET|PUT|DELETE /api/v1/webhooks/{id}`
- `POST /api/v1/webhooks/{id}/test`
- `GET /api/v1/webhooks/{id}/deliveries`
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
Reserved prefixes (`/api/`, `/auth/`, `/web/`, `/_orva/`) are rejected.

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
  "idle_ttl_seconds": 120,
  "target_concurrency": 10,
  "scale_to_zero": false
}
```

Fields are partial — unspecified ones keep the prior value (or default
for new rows).

## API keys

### `GET /api/v1/keys`
List keys. Returns prefixes, names, last_used_at, expires_at. **Never**
returns the plaintext key.

### `POST /api/v1/keys`
```json
{
  "name": "ci-deployer",
  "permissions": ["invoke", "read", "write"],   // optional, defaults to all 4
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

The REST endpoints below (CRUD on `/api/v1/channels`) are operator-
managed and require an **API key** or session cookie — channel tokens
themselves cannot manage channels.

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
immediately — it's not recoverable. Two functions whose names
snake_case to the same MCP tool name are rejected with 400 / `TOOL_NAME_COLLISION`.

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
overlapping function IDs are preserved unless explicitly overridden.

### `POST /api/v1/channels/{id}/rotate`
Re-issues the bearer token. Returns `{token: "orva_chn_..."}` once;
the previous token stops working immediately.

### `DELETE /api/v1/channels/{id}`
Cascade — removes the channel and every junction row.

## System

### `GET /api/v1/system/health`
`{"status": "ok"}` when orvad is up. Used by Docker HEALTHCHECK and
load balancers.

### `GET /api/v1/system/metrics`
Prometheus text format.

### `GET /api/v1/system/metrics.json`
Same data, JSON shape, used by the dashboard.

Prometheus also scrapes the unauthenticated `GET /metrics` path.

### Backup, storage, and firewall administration

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
  "control_plane_allow": {"addrs": ["172.17.0.1"], "port": 8443},
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
| `last_compile_error` | Present only on failure |
| `last_success_at` | RFC 3339 timestamp of the last successful compile |
| `control_plane_allow` | The narrow ALLOW that keeps orvad's internal SDK (`orva.kv` / `orva.jobs` / `orva.invoke`) reachable: exact addresses, exact port, TCP only |
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
