# HTTP API reference

All endpoints under `/api/v1/`. Auth via either:

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

## Functions

### `POST /api/v1/functions`
Create a function record.

```json
{
  "name": "my-fn",
  "runtime": "node22",        // node22|node24|python313|python314
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
  can call external HTTPS APIs. Host interfaces stay isolated.

Toggling on an existing function via `PUT /api/v1/functions/{id}`
drains the warm pool so the next invocation picks up the new mode.

### `GET /api/v1/functions`
List all functions. Optional `?status=active|inactive`, `?runtime=...`.

### `GET /api/v1/functions/{id}`
Single function record.

### `PUT /api/v1/functions/{id}`
Partial update. Whitelisted fields: `name`, `entrypoint`, `timeout_ms`,
`memory_mb`, `cpus`, `env_vars`, `network_mode`, `status`.

`status` accepts only `active` | `inactive`. Setting `inactive` causes
`POST /invoke/<id>` to return 409 NOT_ACTIVE.

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

### `GET /api/v1/functions/{id}/deployments`
Deployment history for a function. Optional `?limit=N` (default 50).

## Invoke

### `POST /fn/{id}/{path}`
Calls the function. `id` is the function's UUID (the same value returned in the `id` field by GET /api/v1/functions).
(e.g. function `019df200-7b00-7e00-9c00-aab1cd2e3f40` → URL `/fn/ttp836b9x3m1`). Method,
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

## Secrets

### `GET /api/v1/functions/{id}/secrets`
List secret keys for a function. Values are not returned (encrypted at
rest; only injected into the sandbox at spawn time).

### `POST /api/v1/functions/{id}/secrets`
Upsert. Body: `{"key": "STRIPE_KEY", "value": "sk_..."}`. Triggers a
pool refresh so the next invocation sees the new value.

### `DELETE /api/v1/functions/{id}/secrets/{key}`
Remove. Triggers a pool refresh.

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
Reserved prefixes (`/api/`, `/fn/`, `/mcp/`, `/web/`, `/_orva/`) are rejected.

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
