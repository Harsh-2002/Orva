# Orva — Documentation

> On Linux, `orva serve` selects the managed server from a durable mode record;
> legacy instances stay legacy.
> It exposes authenticated capability discovery at `GET /api/v1/iam/capabilities`,
> fresh unbound creation at `POST /api/v1/iam/functions`, and exact Factory
> installation at `POST /api/v1/deployments/{id}/install`. The managed
> MCP/Chat tool `install_managed_deployment` does not review or approve policy.
> Normal routes and tool catalogs remain unchanged. See `docs/API.md`.
> On this composition, a configuration write leaves the function inactive
> until explicit installation. After a same-boot restart, use the exact
> current deployment ID to install the pending sealed pair, then review policy.
> Managed function deletion takes an exact UUIDv7; it retires the current
> Factory after reserving metadata-only history and does not reclaim sealed
> assets. Work-history routes and tools are exposed by this composition.
> A request authorized for a superseded function version cannot dispatch to
> its replacement generation; it returns `409 NOT_ACTIVE`.
> The managed Editor reads authored source from the seal, not from a legacy
> `current` symlink. Reading source neither installs nor approves it.
> An admin `GET /api/v1/backup` returns a portable v2 archive of SQLite, available
> keys and current authored source, never the host witness or sealed stores.
> Restore managed archives with the stopped server binary's `orva managed restore`
> command; the legacy `POST /api/v1/restore` route is not mounted in managed mode.
> Use `tools/list` to discover the tools available to your MCP credential.
> An all-permissions key sees 73 tools on the normal server and 66 on this
> composition; read-only keys see 28 and 22 respectively. This composition also
> mounts executions (with replay through managed admission), traces, activity,
> events, system, API-key, OAuth-app, session, channel and Chat routes at the same
> permission tiers as the normal server, and accepts channel tokens at `/mcp`
> as an invoke-only catalog dispatched through the managed pool. See
> `docs/API.md` "Managed operator surfaces".
> Dashboard Chat requires admin and uses only the managed tool catalog,
> scoped to the caller. A missing compiled egress policy stops provider calls
> without disabling Chat settings or history. Model discovery checks the
> actual connection address; a Chat turn checks the provider endpoint before
> the turn, not at dial time.

> Internal configuration qualification: optional `workloadconfig` HTTP and
> MCP/shared-AI adapters publish environment, execution settings and encrypted
> secret upsert/delete through one exact-CAS/live-operator boundary. These are
> **not normal routes or default tools**. Mutation publishes review-required
> deny and invalidates the old actual Factory before ACK. Explicit new Factory
> installation and current aggregate-policy review are separate steps; stored
> approval cannot establish current runtime authority. Configuration uses the
> actual installed concrete pair, including after queued deployment installation;
> it cannot install an uninstalled or superseded queued generation. Ordinary WRITE remains
> sufficient only for truly empty, unquarantined, review-free owners; any outbound,
> inbound or retained grant, quarantine/review state requires live ADMIN. No
> blanket READ requirement is added to writes. Old injected secrets cannot be
> erased by IAM. Boot/restore, all other writers and production activation remain
> separate gates. See `docs/API.md` and `backend/internal/workloadconfig/README.md`.
>
> Only when supplied an explicit `Configuration` dependency, MCP and the shared
> in-process AI registry expose READ-only `inspect_workload_configuration` and
> mutation tools `publish_workload_environment`,
> `publish_workload_settings`, `publish_workload_secret`,
> `delete_workload_secret` (requires `confirm:true`) and
> `install_workload_configuration` (ADMIN-only). Each takes exact function `id`
> plus the original strict API body in `request`; revisions remain strings.
> These mutation tools require WRITE or ADMIN catalog visibility, never imply
> authority from catalog permissions, and recheck the live credential at commit.
> MCP envelopes additionally use the existing bounded strict-management decoder.
> Inspection takes `{id,request:{}}`, returning exact CAS metadata, counts and
> actual current Factory digests, never environment or secret values. Mutation
> schemas describe every authorable request field; no READ grant is implied by
> ADMIN/WRITE and no write capability is implied by inspection.

> Internal opt-in SDK IAM: `spans.emit` requires an execution-only
> `own_execution` grant, a live original dispatch and a required current-artifact
> observer. Admission queues or sheds an owned metadata envelope, never reads
> traces or grants operator READ. Accepted telemetry may persist after revoke;
> ACK is not durability. Normal serving is unchanged; real-runtime qualification
> of this increment remains separate.
> Managed queued-build boundary: REST deploy and external MCP/shared-AI
> `deploy_function_inline` use one managed capture/publication service. Archive
> uploads support Node/npm, Python/pip and TypeScript. Installers require
> compiled egress policy; TypeScript compilation runs without network. Legacy
> function directories are refused, not linked or adopted. Token-free host
> submitter provenance and exact source/epoch/config digests are captured with
> deployment and live-rechecked at
> publication. Empty review-free authority requires WRITE; outbound/inbound/
> retained/quarantined/review-required authority requires ADMIN, without blanket
> READ. Epoch/source/config changes invalidate queued work even for ADMIN.
> `succeeded` / `review_required` means inactive DENY, not ready to invoke: fresh
> concrete-pair host Factory installation and source-aware review are separate.
> Managed inline deploy accepts a single source file without a dependency
> manifest or project files. It rejects duplicate, unknown or host metadata
> fields and invalid UTF-8/surrogates (256 KiB cap); MCP requires explicit
> `wait`. Use an archive upload for dependency manifests or a TypeScript
> project. Portable restore rebuilds from source under a new Owner.
> In this fixture only, REST rollback and MCP/shared-AI `rollback_function`
> promote a verified historical deployment UUID for an exact function UUID
> without allocating a version. MCP/AI additionally require `confirm: true`.
> Hash/name/latest aliases are not accepted. Current settings and stored policy
> are preserved; durable pause blocks rollback. A successful promotion is
> inactive `review_required`, pending separate host installation and policy review.
> See `backend/internal/builder/README.md`; do not enable a partial cut-over.

> Managed lifecycle is also opt-in only: REST `POST /api/v1/functions/{id}/pause`
> and `/resume`, and MCP/shared-AI `pause_workload` and `resume_workload`, are
> absent by default. Use an exact canonical function UUID (`function_id` in
> tools) and review `expected.generation`, `expected.policy_revision` and
> `expected.authority_epoch` through the separately enabled authenticated
> configuration inspection (`GET /api/v1/functions/{id}/configuration` or
> `inspect_workload_configuration` with `id` and an empty `request` object;
> inspection requires READ). Copy only those three
> fields into the mutation, using canonical UUIDs and positive signed-64-bit
> decimal strings for the counters; include `confirm: true`. Unknown and
> duplicate fields and JSON numbers for counters reject. Re-inspect after
> pause or conflict; there are no implicit reads or retries. Pause needs live
> WRITE for unprivileged sources or ADMIN for privileged sources and records
> `operator_paused: true` with `function_status: "inactive"`. Resume needs live
> ADMIN and the exact sealed pair; it returns `operator_paused: false`,
> `function_status: "inactive"`, `installation_required: true` and
> `review_required: true`. It does not resume execution or standing work.
> Install a fresh Factory and explicitly review its new current artifact
> separately. Both acknowledgements identify the function, not a successor
> CAS token. `orva functions pause|resume <function-uuid> --request FILE` (or
> `--request -`) forwards the reviewed JSON unchanged, with a CLI confirmation
> prompt unless `--yes` is passed. See `docs/API.md` for response details.

> Internal qualification note: deleted-work metadata history and explicit
> fresh-work reissue exist only in an opt-in scratch composition, not normal
> serving routes/catalogs. History stores no old payload, environment, secrets or
> raw keys and can never execute. READ protects inspection; live ADMIN plus
> explicit review/confirmation and fresh payload/config/target UUID are required
> to create NEW OPERATOR-origin work. Capacity is 100,000 canonical metadata
> records / 64 MiB including reservations; 90 days permits explicit audited purge,
> never automatic expiry. Function deletion reserves first and fails closed on
> insufficient capacity. Optional `Deps.History` adds `list_work_history`,
> `get_work_history`, `get_work_history_capacity`, `purge_work_history` and
> `reissue_work_history` to external MCP and the shared AI registry. Each takes
> `{request: ...}` using the corresponding strict original-raw HTTP request shape;
> mutations enforce confirmation/review in the service, not merely tool hints.
> Capacity counters and archive revisions remain decimal strings. Normal nil
> dependencies expose no history catalog. `orva history` uses bounded original
> request files/stdin and JSON stdout; confirmed mutations still require the
> service's live authorization. Optional Jobs/Schedules history controls use
> independent metadata/draft state and explicit new-work review; normal views
> do not enable them. CLI/service and component tests are not real-browser or
> cross-surface SDK qualification. Those workflows, actual AI turns and restore
> quarantine/cut-over remain separate gates. See the repository
> `docs/API.md` and `backend/internal/workloadhistory/README.md`; do not activate
> the partial fixture in production.

> Everything you need to write, deploy, and operate functions on Orva.
> Generated from the in-app Docs page (`{{ORIGIN}}/web/docs`).

## Table of contents

1. [Handler contract](#handler-contract)
2. [Deploy & invoke](#deploy--invoke)
3. [Configuration reference](#configuration-reference)
4. [SDK from inside a function](#sdk-from-inside-a-function)
5. [Schedules](#schedules)
6. [Webhooks](#webhooks)
7. [MCP — Model Context Protocol](#mcp--model-context-protocol)
8. [System prompt for AI assistants](#system-prompt-for-ai-assistants)
9. [Tracing](#tracing)
10. [Errors & recovery](#errors--recovery)
11. [CLI](#cli)

---

## Handler contract

One exported function receives the inbound HTTP event and returns an
HTTP-shaped response. The adapter handles serialization and headers.

### Handler — Python

```python
import json


def handler(event):
    # event["body"] is the raw request body, as a string. Always parse it.
    raw = event.get("body") or ""
    body = json.loads(raw) if raw else {}
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": {"hello": body.get("name", "world")},
    }
```

### Handler — Node.js

```js
exports.handler = async (event) => {
  // event.body is the raw request body, as a string. Always parse it.
  const body = event.body ? JSON.parse(event.body) : {};
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: { hello: body.name || 'world' },
  };
};
```

**Event shape:** `method`, `path`, `headers`, `body` — plus `query` on Node
only (its adapter parses it out of `path`; Python handlers split
`event["path"]` themselves). **`body` is always the raw request body as a
string** — the platform never parses it, whatever the `Content-Type`, so
`json.loads` / `JSON.parse` it yourself and guard the empty case.

**Response:** `{ statusCode, headers, body }`. Non-string bodies are
JSON-encoded by the adapter.

**Runtime env:** env vars and secrets land in `process.env` (Node) /
`os.environ` (Python).

Orva offers two runtimes, latest-stable only. The ID is generic (`node` /
`python`); the version column shows what they currently track.

| Runtime | ID | Version | Entrypoint | Dependencies |
|---|---|---|---|---|
| Python | `python` | 3.15 | `handler.py` | `requirements.txt` |
| Node.js | `node` | 26 | `handler.js` | `package.json` |

---

## Deploy & invoke

The dashboard handles day-to-day work; these calls are for CI and
automation. Builds run async — poll `/api/v1/deployments/<id>` or
stream `/api/v1/deployments/<id>/stream` until `phase: done`.
The dashboard editor shows advisory syntax hints while you type. During a
deploy, Orva checks JavaScript and Python entrypoints with the shipped runtime
inside a network-disabled build jail before dependency installation; TypeScript
uses the existing jailed `tsc` step. Syntax errors fail the build with a file
location in its log and do not replace an earlier live version. The editor's
absence of hints is not a guarantee that a build or invocation will succeed.

### 1. Create the function row

Authors can also upload an already-built CommonJS JavaScript bundle to the
`node` runtime with entrypoint `dist/handler.js`. See
[Author-side prebundling](https://github.com/Harsh-2002/Orva/blob/main/docs/PREBUNDLING.md)
for the opt-in JS/TS recipe and compatibility limits. Keep the bundler and
original sources in the author workspace; `/source`, editor and diff show the
uploaded bundle, not original TypeScript. Orva does not bundle automatically.

```bash
curl -X POST {{ORIGIN}}/api/v1/functions \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{"name":"hello","runtime":"python","memory_mb":128,"cpus":0.5}'
```

### 2. Upload code

```bash
tar czf code.tar.gz handler.py requirements.txt
curl -X POST {{ORIGIN}}/api/v1/functions/<function_id>/deploy \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -F code=@code.tar.gz
```

### Invoke

### Invoke — curl

```bash
curl -X POST {{ORIGIN}}/fn/<function_id> \
  -H 'Content-Type: application/json' \
  -d '{"name": "Orva"}'
```

### Invoke — fetch

```js
const res = await fetch('{{ORIGIN}}/fn/<function_id>', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ name: 'Orva' }),
});
console.log(await res.json());
```

### Invoke — Python

```python
import httpx

r = httpx.post(
    "{{ORIGIN}}/fn/<function_id>",
    json={"name": "Orva"},
)
print(r.json())
```

> **Custom routes:** attach a friendly path with `POST /api/v1/routes`,
> the `set_route` MCP tool, the `orva routes set` CLI, OR the
> dashboard's function settings → "Custom routes" section (it
> collision-checks against other functions before saving).
> Reserved prefixes: `/api/` `/auth/` `/fn/` `/mcp/` `/web/` `/webhook/` `/_orva/`.

### Check response latency

`GET {{ORIGIN}}/api/v1/system/metrics.json` (with a read-capable API key)
returns `response_latency_ms: {p50, p95, p99}` for the most recent 8,192
public invoke-handler completions, including admission failures and execution
record enqueue. The dashboard's **Server response time** card uses these
values. They exclude reverse-proxy, network, and client time. The separate
`latency_ms` field stops when the sandbox proxy returns, before record enqueue;
it is not the end-to-end response time seen by an HTTP client.
HTTP, inbound-webhook, replay, and internal SDK invocations reserve a critical
execution-record writer slot before user code runs. If storage pressure
prevents a reservation within five seconds,
the server returns `429 STORAGE_BACKPRESSURE` with `Retry-After: 1`; the
function has not run, so retrying cannot duplicate its side effects.
MCP invocation tools report storage pressure as a tool error before execution.
Cron dispatch checks the same reservation and records a failed schedule tick
without running user code; queued jobs reserve before claiming an attempt.

For writer-drain checks, `GET {{ORIGIN}}/api/v1/system/health` reports
`writer.critical_queue_bytes`, `writer.activity_queue_bytes`, and
`writer.telemetry_queue_bytes`. These include
accepted jobs still in the channel, being committed, or awaiting retry; a
channel depth of zero alone does not mean the writer is drained. Activity
has a separate best-effort lane from optional replay/log/span records;
`writer.dropped_activity` counts lost activity rows and is included in
`writer.dropped_telemetry`. A function delete removes its execution history;
`writer.deleted_function_writes` separately counts async execution-related
jobs discarded because their function was deleted before commit. Such jobs
are not counted as storage failures.

---

## Configuration reference

Everything below lives on the function record. Secrets are stored
encrypted and only decrypt into the worker environment at spawn time.

| Field | Purpose | Behaviour |
|---|---|---|
| `description` | Intent | One-sentence summary of what the function does (e.g. "resize uploaded images to webp"). Surfaces in `list_functions`, the dashboard's function card and search, and channel-mode tool descriptions exposed to other agents. Required when creating via MCP; optional via REST/CLI for backwards compat. |
| `env_vars` | Plain config | Plaintext config stored on the function record. Use for feature flags and non-secret settings. |
| `/secrets` | Encrypted | AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time. |
| `network_mode` | Egress control | none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies. Current adapters route supported orva SDK operations over the inherited worker channel, including in `none`; external function network requests still require `egress`. |
| `sdk` | SDK permission | Explicit access to Orva KV, function calls/streams, jobs, cron declarations and custom spans. New functions default to false, independently of `network_mode`. Enable for SDK-dependent handlers; receiving a function call does not itself require this permission. |
| `auth_mode` | Invoke gate | none = public. platform_key = require an Orva session cookie, or an API key carrying the `invoke` permission (via `X-Orva-API-Key` or `Authorization: Bearer`). signed = require HMAC. |
| `rate_limit_per_min` | Per-IP throttle | Optional cap for public or webhook-facing functions. Exceeding it returns 429. The IP is the TCP peer address; `X-Forwarded-For` is honoured only when the operator sets `ORVA_TRUSTED_PROXY=true`, and then its rightmost entry. |
| custom routes | Pretty URLs | Operator-defined `/path` or `/prefix/*` mappings to the function. Manage via `POST /api/v1/routes`, the `set_route` MCP tool, the `orva routes set` CLI, or the dashboard's function settings → "Custom routes" section (collision-checks against other functions). Optional. |

### Set a secret

```bash
curl -X POST {{ORIGIN}}/api/v1/functions/<function_id>/secrets \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'
```

### Signed-invoke recipe (HMAC, opt-in)

```bash
# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST {{ORIGIN}}/fn/<function_id> \
  -H "X-Orva-Timestamp: $TS" \
  -H "X-Orva-Signature: sha256=$SIG" \
  -H 'Content-Type: application/json' \
  -d "$BODY"
```

---

## SDK from inside a function

The bundled `orva` module ships beside the runtime adapter — `require('orva')` (Node) and
`from orva import …` (Python) work without `npm install` / `pip
install`. The Node SDK ships TypeScript declarations (`orva.d.ts`); the
Python SDK ships a `py.typed` marker so IDEs surface full type hints. A
TypeScript handler must map the module in `tsconfig.json` before `tsc` can
see those declarations — see the TypeScript section of `docs/RUNTIMES.md`.

When `sdk: true`, current adapters route `kv`, `invoke`/`invokeStream`, `jobs`, `crons`, and
`trace.span` operations over a private inherited per-worker socket channel; the
host derives the function ID from that channel. SDK operations require this channel. Missing or incompatible adapters must
be refreshed; no HTTP fallback or SDK token/base URL is provided.
KV reads and writes can run at module scope; they use the worker's function
identity but no dispatch lease, so a value read there can become stale.
`invoke`, `invokeStream`, `jobs.enqueue`, `crons.upsert`, and custom spans
require an active handler dispatch on that channel. Calls are bounded to depth
8; up to 8 streams may be open per channel, and each stream read yields a chunk no larger than 256 KiB, with stream state
closed on cancellation/lease end. Frames are limited to 4 MiB, with 32
in-flight calls and bounded pending replies. KV list replies paginate, and KV
batches remain limited to 100 operations. Channel list pages stop at about
2 MiB and return a continuation cursor; batch results over 3 MiB are refused
with `413 RESULT_TOO_LARGE` (request fewer keys). The host bounds pending
handler/reply work to 64 calls per channel and disconnects a stalled reader.

Surface, as of v0.7:

- **`kv`** — `get` / `put` / `delete` / `list(cursor)` / `getMany` / `putMany` / `deleteMany` / `incr` / `cas`. Per-function namespace, optional TTL, all-or-nothing batch ops, atomic counter & CAS. Keys are non-empty UTF-8 up to 256 characters; JSON values are capped at 64 KiB; batches are capped at 100 operations.
- **`invoke(name, payload, {timeoutMs})`** — synchronous F2F call with `{statusCode, headers, body}` envelope. 8-deep call cap.
- **`invokeStream(name, payload, {timeoutMs})`** — same, but yields `Uint8Array` chunks via `for await`.
- **`jobs.enqueue(name, payload, {idempotencyKey, maxAttempts, scheduledAt})`** — durable background queue with built-in dedup.
- **`crons.upsert(name, schedule, {payload, timezone, enabled})`** — declare a cron schedule from the function body itself. Must be called from **inside** your handler and awaited; calling it at module scope returns `403 SDK_SCOPE_VIOLATION`. A function may declare at most 25 schedules.
- **`trace.span(name, fn, attrs?)`** — wrap a code block as a child span; durations land in the trace waterfall.
- **`log.{debug,info,warn,error}(msg, fields?)`** — structured logs surfaced in the dashboard Logs lane.
- **`context`** — frozen view of `functionId`, `executionId`, `traceId`, `spanId`, `callDepth`, `timeoutMs`, `memoryMb`, `sdkVersion`. `timeoutMs` is the function's configured timeout (from `ORVA_TIMEOUT_MS`), also reachable as `getRemainingTimeInMillis()` / `get_remaining_time_in_millis()` on the handler's context argument.
- **`secrets.get(name)`** — explicit accessor over the secret environment vars.
- **`webhook.parse(event)`** — extract source / verified / payload from an inbound-webhook event without re-parsing headers.
- **`__test_mode__(impl)`** — swap the transport for tests so handlers run without a live server.

### KV — get/put with TTL

### KV — Python

```python
from orva import kv

def handler(event):
    # Omit TTL to preserve an existing expiry (new keys are persistent).
    # Pass 0 to clear expiry, or a positive number to set/refresh it.
    kv.put("user:42", {"name": "Ada", "tier": "pro"}, ttl_seconds=3600)

    # Read; default returned if missing or expired.
    user = kv.get("user:42", default=None)

    # List by prefix.
    pages = kv.list(prefix="page:", limit=50)

    # Delete is idempotent.
    kv.delete("user:42")

    return {"statusCode": 200, "body": str(user)}
```

### KV — Node.js

```js
const { kv } = require('orva')

exports.handler = async (event) => {
  // Omit ttlSeconds to preserve expiry, use 0 to clear it, or a
  // positive number to set/refresh it.
  await kv.put('user:42', { name: 'Ada', tier: 'pro' }, { ttlSeconds: 3600 })

  const user = await kv.get('user:42', null)

  const pages = await kv.list({ prefix: 'page:', limit: 50 })

  await kv.delete('user:42')

  return { statusCode: 200, body: JSON.stringify(user) }
}
```

> Browse / inspect / edit / delete / set keys without leaving the
> dashboard at `/web/functions/<name>/kv`. REST mirror at
> `GET/PUT/DELETE /api/v1/functions/<id>/kv[/<key>]`. MCP tools:
> `kv_list` / `kv_get` / `kv_put` / `kv_delete`.

> KV batches are atomic: validation or storage failure rejects the request and
> rolls back every write. The SDK's `getMany`, `putMany`, and `deleteMany`
> throw `OrvaError` when the batch fails; they never report partial success.

### Function-to-function — invoke()

### F2F — Python

```python
import json

from orva import invoke, OrvaError

def handler(event):
    # event["body"] is the raw request body, as a string. Always parse it.
    url = json.loads(event["body"] or "{}")["url"]
    try:
        # invoke() returns the downstream {statusCode, headers, body}.
        # body is JSON-decoded when possible.
        result = invoke("resize-image", {"url": url})
        return {"statusCode": 200, "body": result["body"]}
    except OrvaError as e:
        # 404 = function not found, 507 = call depth exceeded.
        return {"statusCode": e.status or 502, "body": str(e)}
```

### F2F — Node.js

```js
const { invoke, OrvaError } = require('orva')

exports.handler = async (event) => {
  // event.body is the raw request body, as a string. Always parse it.
  const { url } = JSON.parse(event.body || '{}')
  try {
    const result = await invoke('resize-image', { url })
    return { statusCode: 200, body: result.body }
  } catch (e) {
    if (e instanceof OrvaError) {
      return { statusCode: e.status || 502, body: e.message }
    }
    throw e
  }
}
```

### Background jobs — jobs.enqueue()

### Jobs — Python

```python
import json

from orva import jobs

def handler(event):
    # Fire-and-forget. Returns {"id": ..., "replayed": ...} immediately;
    # the body runs later via the scheduler. max_attempts retries with
    # exponential backoff on 5xx / exception.
    body = json.loads(event["body"] or "{}")
    job = jobs.enqueue(
        "send-welcome-email",
        {"to": body["email"]},
        max_attempts=3,
    )
    return {"statusCode": 202, "body": job}
```

### Jobs — Node.js

```js
const { jobs } = require('orva')

exports.handler = async (event) => {
  const { email } = JSON.parse(event.body || '{}')
  const job = await jobs.enqueue(
    'send-welcome-email',
    { to: email },
    { maxAttempts: 3 }
  )
  return { statusCode: 202, body: job }
}
```

> **Network mode:** current adapters use the inherited SDK channel, so these
> SDK calls work with `network_mode: "none"`; missing-channel calls fail closed with adapter/permission guidance. `network_mode: "egress"` is still needed
> for outbound network requests made by your function.

### KV — batch ops, atomic counter, compare-and-swap

```python
from orva import kv, OrvaCASMismatch

def handler(event):
    # Hydrate a dashboard view in a single round trip.
    users = kv.get_many(["user:1", "user:2", "user:3"])

    # Atomic counter — safe under concurrent writers.
    visits = kv.incr("visits", 1)

    # Compare-and-swap loop. Idiomatic safe read-modify-write.
    while True:
        cur = kv.get("counter", default=0)
        try:
            kv.cas("counter", cur, cur + 1)
            break
        except OrvaCASMismatch:
            continue

    # Cursor-based pagination over the entire namespace.
    cursor, walked = "", []
    while True:
        page = kv.list(prefix="post:", limit=100, cursor=cursor)
        walked.extend(page["keys"])
        cursor = page["next_cursor"]
        if not cursor:
            break

    return {"statusCode": 200, "body": {"visits": visits, "n": len(walked)}}
```

```js
const { kv, OrvaCASMismatch } = require('orva')

exports.handler = async () => {
  const users = await kv.getMany(['user:1', 'user:2', 'user:3'])
  const visits = await kv.incr('visits')

  while (true) {
    const cur = await kv.get('counter', 0)
    try {
      await kv.cas('counter', cur, cur + 1)
      break
    } catch (e) {
      if (e instanceof OrvaCASMismatch) continue
      throw e
    }
  }

  return { statusCode: 200, body: JSON.stringify({ visits }) }
}
```

### Jobs — idempotency

```python
import json

from orva import jobs

def handler(event):
    body = json.loads(event["body"] or "{}")
    # Same idempotency_key inside the window returns the existing job
    # id instead of enqueuing again. Useful for webhook handlers that
    # may be retried by the source.
    res = jobs.enqueue(
        "send-welcome-email",
        {"to": body["email"]},
        idempotency_key=f"welcome:{body['email']}",
        idempotency_window_seconds=3600,
    )
    return {"statusCode": 202, "body": res}  # {"id": "...", "replayed": false}
```

### Custom spans and structured logs

```python
from orva import trace, log

def handler(event):
    log.info("incoming", fields={"path": event.get("path")})

    with trace.span("parse"):
        parsed = parse_body(event["body"])

    with trace.span("transform", attributes={"rows": len(parsed)}):
        result = transform(parsed)

    log.info("done", fields={"rows": len(result)})
    return {"statusCode": 200, "body": result}
```

```js
const { trace, log } = require('orva')

exports.handler = async (event) => {
  log.info('incoming', { path: event.path })

  const parsed = await trace.span('parse', () => parseBody(event.body))
  const result = await trace.span('transform', () => transform(parsed),
    { rows: parsed.length })

  log.info('done', { rows: result.length })
  return { statusCode: 200, body: JSON.stringify(result) }
}
```

User spans and log entries render inline in the dashboard's trace
waterfall — `parse` and `transform` show as bars nested under the
function's main span, and the level-tagged log lines appear in the
Logs lane below.

Code spans always belong directly to the containing execution. Nested calls to
`trace.span` do not establish nested code-span parentage, and SDK logs are
execution-scoped, not code-span-scoped. Selecting a code span shows its
containing execution's logs with that scope stated explicitly. Structured logs
are collected on HTTP, function-to-function, cron/job, and inbound-webhook
execution paths, including replays. Ordinary stderr and malformed log
records remain in invocation logs. Structured telemetry is best-effort and may
arrive after the response or be dropped under pressure.

Await Node `trace.span`: completion waits for the telemetry channel ACK/failure
or its existing 30-second request deadline (including queue wait). The original
value/error is preserved and recorded duration excludes this submission wait.
Telemetry errors are swallowed; ACK means queued, not durable persistence.

### Streaming F2F

```js
const { invokeStream } = require('orva')

exports.handler = async () => {
  let total = 0
  for await (const chunk of invokeStream('big-report', {})) {
    total += chunk.length
  }
  return { statusCode: 200, body: JSON.stringify({ bytes: total }) }
}
```

```python
from orva import invoke_stream

def handler(event):
    total = 0
    for chunk in invoke_stream("big-report", {}):
        total += len(chunk)
    return {"statusCode": 200, "body": {"bytes": total}}
```

### Cron-from-code, context, secrets, webhook helper

```js
const { crons, context, secrets, webhook } = require('orva')

exports.handler = async (event) => {
  // Register a daily sweep — idempotent by (function, name).
  await crons.upsert('daily-cleanup', '0 3 * * *', {
    timezone: 'UTC',
    payload: { source: 'self' },
  })

  // Frozen view of the execution context.
  if (context.callDepth >= 6) return { statusCode: 507, body: 'too deep' }

  // Explicit secret accessor (env-var passthrough today; reserved for
  // per-secret access auditing later).
  const token = secrets.get('STRIPE_KEY')

  // Parse an inbound-webhook event: HMAC was already verified
  // server-side before this handler ran.
  const w = webhook.parse(event)
  if (!w.verified) return { statusCode: 401, body: 'unverified' }

  return { statusCode: 200, body: JSON.stringify({ src: w.source }) }
}
```

> **What `verified` rests on.** It is derived from the `x-orva-trigger`
> header, which the server sets on the webhook path after checking the HMAC.
> Orva strips the entire inbound `x-orva-*` namespace before a request reaches
> your handler, so a caller hitting `/fn/<id>/` directly cannot set it — the
> only way `verified` is true is that the signature check passed. The same
> guarantee is what makes it safe to branch on `x-orva-trigger` to tell a cron
> or job invocation from an HTTP one.

### Testing handlers without a server

```js
const orva = require('orva')

const memKV = new Map()
orva.__test_mode__({
  async request(method, path, opts) {
    // Implement just enough of the wire protocol for your tests.
    // Returns { status, body } the same shape the real transport does.
    return { status: 200, body: '{}' }
  },
})

// then exercise your handler ...
```

---

## Schedules

Fire any function on a cron expression. The scheduler runs as part of
the orvad process — no external service. Manage from the Schedules
page or via the API. Standard 5-field cron with the usual shorthands
(`@daily`, `@hourly`, `*/5 * * * *`).

### Cron — curl

> Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.

```bash
curl -X POST {{ORIGIN}}/api/v1/functions/<function_id>/cron \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{
    "cron_expr": "0 9 * * *",
    "enabled":   true,
    "payload":   {"task": "daily-summary"}
  }'
```

### Cron — Toggle / edit

> PUT accepts any subset of {cron_expr, enabled, payload}; omitted fields keep their previous value. next_run_at is recomputed on expr changes.

```bash
# pause
curl -X PUT {{ORIGIN}}/api/v1/functions/<function_id>/cron/<019df210-7b00-7e00-9c00-aab1cd2e3f44> \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{"enabled": false}'

# change schedule
curl -X PUT {{ORIGIN}}/api/v1/functions/<function_id>/cron/<019df210-7b00-7e00-9c00-aab1cd2e3f44> \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{"cron_expr": "*/15 * * * *"}'
```

### Cron — List & delete

> GET /api/v1/cron lists every schedule across functions (with function_name JOIN); per-function uses the nested route.

```bash
# all schedules
curl {{ORIGIN}}/api/v1/cron \
  -H 'X-Orva-API-Key: <YOUR_KEY>'

# delete one
curl -X DELETE {{ORIGIN}}/api/v1/functions/<function_id>/cron/<019df210-7b00-7e00-9c00-aab1cd2e3f44> \
  -H 'X-Orva-API-Key: <YOUR_KEY>'
```

> **Cron-fired headers:** every cron-triggered invocation arrives at
> the function with `x-orva-trigger: cron` and
> `x-orva-cron-id: <uuid>` on the event headers, so user code can
> branch on origin.

---

## Webhooks

Operator-managed subscriptions for system events. Configure URLs from
the Webhooks page; Orva delivers signed POSTs to them when matching
events fire (deployments, function lifecycle, cron failures, job
outcomes). Subscriptions are global, not per-function.

**Headers:** `X-Orva-Event`, `X-Orva-Delivery-Id`,
`X-Orva-Timestamp`, `X-Orva-Signature`.

**Signature:** `sha256=hex(hmac(secret, ts.body))`. Same shape as
Stripe / signed-invoke. Receivers verify with the secret returned at
create time.

**Retries:** 5 attempts, exponential backoff (≤ 1h). Receiver must 2xx
within 15s.

| Event | When it fires |
|---|---|
| `deployment.succeeded` | A function build finished and the new version is active. |
| `deployment.failed` | A build failed or was rejected. |
| `function.created` | A new function row was created via POST /api/v1/functions. |
| `function.updated` | A function config was edited via PUT /api/v1/functions/{id} (status flips during a deploy do NOT fire this — see deployment.*). |
| `function.deleted` | A function was removed. |
| `execution.error` | An invocation finished with status=error or 5xx. |
| `cron.failed` | A scheduled run failed (bad expr, missing fn, dispatch error, or 5xx). |
| `job.succeeded` | A queued background job finished successfully. |
| `job.failed` | A queued job exhausted its retries (terminal failure). |

### Verify a delivery

### Verify — Python

> Run on the receiver. Reject anything that fails verification — the signature ensures the request really came from this Orva instance.

```python
import hmac, hashlib, time

def verify(secret: str, ts: str, body: bytes, sig_header: str) -> bool:
    if abs(time.time() - int(ts)) > 300:   # 5-min skew window
        return False
    mac = hmac.new(secret.encode(), f"{ts}.".encode() + body, hashlib.sha256)
    expected = "sha256=" + mac.hexdigest()
    return hmac.compare_digest(expected, sig_header)

# In your Flask/FastAPI/etc. handler:
ts  = request.headers["X-Orva-Timestamp"]
sig = request.headers["X-Orva-Signature"]
if not verify(WEBHOOK_SECRET, ts, request.get_data(), sig):
    return "bad signature", 401
```

### Verify — Node.js

> Same shape as Stripe. Use timingSafeEqual to avoid sig-leak via timing.

```js
const crypto = require('crypto')

function verify(secret, ts, body, sigHeader) {
  if (Math.abs(Date.now() / 1000 - parseInt(ts, 10)) > 300) return false
  const mac = crypto.createHmac('sha256', secret)
  mac.update(ts + '.')
  mac.update(body)
  const expected = 'sha256=' + mac.digest('hex')
  if (expected.length !== sigHeader.length) return false
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(sigHeader))
}

// In an express handler with raw body middleware:
app.post('/webhooks/orva', (req, res) => {
  const ok = verify(
    process.env.WEBHOOK_SECRET,
    req.headers['x-orva-timestamp'],
    req.body,                  // raw bytes — NOT parsed JSON
    req.headers['x-orva-signature']
  )
  if (!ok) return res.status(401).send('bad signature')
  res.sendStatus(200)
})
```

---

## MCP — Model Context Protocol

Same API surface the dashboard uses, exposed as 73 tools an agent can
call directly. API key permissions scope the available tool set.

- **Endpoint:** `{{ORIGIN}}/mcp`
- **Auth header:** `Authorization: Bearer <token>`
  (fallback: `X-Orva-API-Key: <token>`)
- **Transport:** Streamable HTTP, stateless. Orva serves MCP **2026-07-28**
  and still accepts older clients — the SDK negotiates down, and
  `server/discover` advertises `2026-07-28`, `2025-11-25`, `2025-06-18`,
  `2025-03-26`, `2024-11-05`.

> **No handshake, no session.** The transport is stateless: there is no
> `initialize` step to perform first, no `Mcp-Session-Id` is ever issued, and
> `GET /mcp` / `DELETE /mcp` — the SSE-resume and session-teardown verbs of the
> older session transport — return `405`. Every POST carries its own bearer
> token and is answered on its own. A legacy client that still sends
> `initialize` gets a normal reply (with its own `protocolVersion` echoed back)
> and simply never receives a session header. Every POST must send
> `Accept: application/json, text/event-stream`. A **successful** reply is a
> single SSE `message` event (`Content-Type: text/event-stream`); a request
> rejected at the transport layer comes back as plain `application/json` with a
> 4xx status, so a client has to handle both framings.

> **Origin.** A browser request whose `Origin` is not in an explicit
> `ORVA_CORS_ORIGINS` list is rejected `403` *before* authentication. A request
> with **no** `Origin` header — every non-browser MCP client, which is nearly
> all of them — is always allowed. The default (`ORVA_CORS_ORIGINS` unset, or
> `*`) allows any origin. If you narrow that list for the dashboard, remember it
> also governs `/mcp`, including third-party agent-channel consumers.

> **2026-07-28 request shape.** A client that opts into the new protocol
> replaces the handshake with wire headers plus two `params._meta` keys:
> ```jsonc
> Mcp-Protocol-Version: 2026-07-28   // required once _meta declares a protocolVersion
> Mcp-Method: tools/list             // required — must equal the JSON-RPC method
> Mcp-Name: <name>                   // required for tools/call, resources/read,
>                                    // prompts/get — must equal params.name / params.uri
>
> "_meta": {
>   "io.modelcontextprotocol/protocolVersion":    "2026-07-28",   // required
>   "io.modelcontextprotocol/clientCapabilities": {},             // required
>   "io.modelcontextprotocol/clientInfo": { "name": "curl", "version": "0" }  // optional
> }
> ```
> The headers exist so a proxy or rate-limiter can route on the operation
> without parsing the body, which is why they must agree with it: a
> `Mcp-Method` or `Mcp-Name` that disagrees is rejected rather than ignored.
>
> Failure modes, all observed:
>
> | Mistake | Response |
> |---|---|
> | `clientCapabilities` omitted from `_meta` | `400` · `-32602 missing or invalid _meta field` |
> | `Mcp-Method` omitted, or disagreeing with the body | `400` · `-32020` |
> | `Mcp-Name` omitted on `tools/call` / `resources/read` / `prompts/get` | `400` · `-32020 missing required Mcp-Name header for method "tools/call"` |
> | `_meta.protocolVersion` present but `Mcp-Protocol-Version` header absent | `400` · `-32020 Mcp-Protocol-Version header is required for requests carrying "io.modelcontextprotocol/protocolVersion"` |
>
> Note the last row: declaring **any** `protocolVersion` in `_meta` — including
> an older one — commits the request to the header-validated path. It does not
> fall back. The legacy path is reached by sending no `_meta.protocolVersion` at
> all, and a bare `tools/list` with `"params":{}` and no protocol headers still
> returns the full catalog.

> **List results are private and immediately stale.** `tools/list`,
> `resources/list`, `resources/templates/list`, `prompts/list`,
> `resources/read` and `server/discover` results carry `ttlMs` and `cacheScope`.
> Orva returns `cacheScope: "private"` because the catalog is permission-scoped
> (a full-permission key lists 73 tools; a `read`-only key lists 28) and
> channel-specific (a channel token sees only that channel's functions) — a
> shared cache entry would hand one caller another caller's tool surface.
> `ttlMs` is `0` because the catalog changes on any deploy, channel edit, or
> permission change, and statelessness removed the session a
> `tools/list_changed` notification would have travelled over. Re-list instead
> of caching.

> **Strict input contract.** The MCP tool surface is required-by-default: optional fields are exceptions
> (pagination, list filters, true patches, opt-in TTLs). Notable required fields the agent must declare
> explicitly: on `create_function` — `name`, `description`, `runtime`, `entrypoint`, `timeout_ms`,
> `memory_mb`, `cpus`, `network_mode`, `auth_mode`; on `invoke_function` — `method` (no silent POST default);
> on `deploy_function_inline` — `wait` (true blocks until built, false returns queued); on `create_api_key` —
> `permissions` (least-privilege subset of `[invoke, read, write, admin]`) and `expires_in_days`. The schema
> rejects missing fields at the JSON-RPC layer, so agents see "missing properties" errors at the moment of
> the call rather than runtime surprises later.

> **`invoke_function` body envelope.** The `body` field is a typed discriminator, not free-form JSON. Pick one shape:
> ```jsonc
> { "type": "json",   "json":   { "name": "World" } }   // sent as application/json
> { "type": "string", "string": "raw text payload" }     // sent verbatim, no Content-Type forced
> { "type": "empty" }                                    // no body — for GET / DELETE / HEAD
> ```
> Omit the `body` field entirely if you have no payload. The platform validates the type at the JSON-RPC layer; an unknown `type` is rejected with a clear error.

> **`invoke_function` diagnostic hints.** When the handler crashes with a network-shaped error (`ENETUNREACH`, `ECONNREFUSED`, `fetch failed`, `OrvaUnavailableError`) AND the function's `network_mode` is `"none"`, the response carries an `orva_hint` field telling the agent exactly what to fix (`network_mode='egress'` via `update_function`). Always check this field before doing your own root-cause analysis on a network error.

> **Fixture tools** (`list_fixtures`, `save_fixture`, `delete_fixture`, `test_function_with_fixture`) let you save Postman-style request envelopes per function and replay them — useful for debugging and regression checks. The `test_function_with_fixture` tool accepts a shallow-merge `override` object so a single fixture can be parameterised per call without mutating the saved row.

> **Tool naming and metadata.** Every operator-mode tool name matches `^[a-z][a-z0-9_]{0,62}$` and ships with a human-readable `Title`, ≥80-char description, and honest annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`). The same rules apply to channel-mode auto-generated tools — see `backend/internal/mcp/CHECKLIST.md` for the canonical 12-rule policy if you contribute new tools.

> **Channel-mode tool input.** When a downstream agent calls a channel-bundled tool, the input shape is the same `ChannelInvokeInput` envelope (`method`, `path`, `headers`, `body` with the discriminator above, `timeout_ms`). The output mirrors `invoke_function` (`status_code`, `headers`, `body` string, `execution_id`, optional `stderr`, optional `orva_hint`).

> Set the per-function `sdk` permission to use Orva SDK operations independently of outbound networking. SDK calls work in `network_mode: "none"`; external network calls still require `egress`. There is no SDK HTTP fallback: update installed adapters alongside the server.

> Generate a token from the Docs page in the dashboard, then drop it
> into your client config (Claude Code, Claude Desktop, Cursor, Cline,
> Codex, Windsurf, ChatGPT, etc.). Either header works against the
> same API key store with identical permission gating.

### Install snippets (primary clients)

### MCP — Claude Code

> Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 73 operator-mode tools.

```bash
claude mcp add --transport http --scope user orva {{ORIGIN}}/mcp --header "Authorization: Bearer <YOUR_ORVA_TOKEN>"
```

### MCP — curl

> Talk to MCP directly — no handshake, no session id. Step 1 asks the server what it supports; Step 2 lists the tools. Each reply is one SSE `message` event.

```bash
curl -sN -X POST {{ORIGIN}}/mcp \
  -H 'Authorization: Bearer <YOUR_ORVA_TOKEN>' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: server/discover' \
  -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'

curl -sN -X POST {{ORIGIN}}/mcp \
  -H 'Authorization: Bearer <YOUR_ORVA_TOKEN>' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: tools/list' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"curl","version":"0"}}}}'
```

`server/discover` returns `supportedVersions`, `capabilities`
(`logging`, `resources.listChanged`, `tools.listChanged`), the server
`instructions`, and `serverInfo` under `result._meta`'s
`io.modelcontextprotocol/serverInfo` key — everything a client used to learn
from the `initialize` reply. Older clients need none of it; a bare list with
no `_meta` returns the same catalog:

```bash
curl -sN -X POST {{ORIGIN}}/mcp \
  -H 'Authorization: Bearer <YOUR_ORVA_TOKEN>' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Calling a tool needs the third header. `Mcp-Name` must repeat `params.name`, or
the request is refused with `-32020` before the tool ever runs:

```bash
curl -sN -X POST {{ORIGIN}}/mcp \
  -H 'Authorization: Bearer <YOUR_ORVA_TOKEN>' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: tools/call' \
  -H 'Mcp-Name: system_health' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_health","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
```

### More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT)

### MCP (extra) — Claude Desktop

> Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\Claude\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.

```json
{
  "mcpServers": {
    "orva": {
      "url": "{{ORIGIN}}/mcp",
      "headers": {
        "Authorization": "Bearer <YOUR_ORVA_TOKEN>"
      }
    }
  }
}
```

### MCP (extra) — Cursor

> Open the link in your browser. Cursor pops an approval dialog and writes ~/.cursor/mcp.json.

```bash
cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=eyJ1cmwiOiJodHRwOi8vbG9jYWxob3N0Ojg0NDMvbWNwIiwiaGVhZGVycyI6eyJBdXRob3JpemF0aW9uIjoiQmVhcmVyIDxZT1VSX09SVkFfVE9LRU4+In19
```

### MCP (extra) — VS Code

> User-scoped install via the Copilot-MCP `code --add-mcp` flag. Pick "Workspace" at the prompt to write .vscode/mcp.json instead.

```bash
code --add-mcp '{"name":"orva","type":"http","url":"{{ORIGIN}}/mcp","headers":{"Authorization":"Bearer <YOUR_ORVA_TOKEN>"}}'
```

### MCP (extra) — Codex CLI

> OpenAI's `codex` CLI. Writes to ~/.codex/config.toml.

```bash
codex mcp add --transport streamable-http orva {{ORIGIN}}/mcp --header "Authorization: Bearer <YOUR_ORVA_TOKEN>"
```

### MCP (extra) — OpenCode

> Interactive add. Pick "Remote", paste {{ORIGIN}}/mcp, then add the header Authorization: Bearer <YOUR_ORVA_TOKEN>.

```bash
opencode mcp add
```

### MCP (extra) — Zed

> Zed runs MCP as stdio subprocesses, so use the `mcp-remote` bridge. Paste under context_servers in ~/.config/zed/settings.json. Restart Zed.

```json
{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "{{ORIGIN}}/mcp",
        "--header", "Authorization:Bearer <YOUR_ORVA_TOKEN>"
      ]
    }
  }
}
```

### MCP (extra) — Windsurf

> Paste into ~/.codeium/windsurf/mcp_config.json and reload Windsurf.

```json
{
  "mcpServers": {
    "orva": {
      "serverUrl": "{{ORIGIN}}/mcp",
      "headers": {
        "Authorization": "Bearer <YOUR_ORVA_TOKEN>"
      }
    }
  }
}
```

### MCP (extra) — claude.ai web

> UI-only flow. Settings → Connectors → Add custom connector. claude.ai opens an Orva login + consent popup, then issues an OAuth 2.1 token automatically — no token paste required. Refresh tokens rotate per OAuth 2.1 §4.3.1.

```text
URL:  {{ORIGIN}}/mcp
Auth: OAuth (auto-discovered)
```

### MCP (extra) — ChatGPT

> UI-only flow. Settings → Apps & Connectors → Developer mode → Add new connector. ChatGPT discovers OIDC metadata, performs Dynamic Client Registration, and pops the Orva consent screen. No token paste required.

```text
URL:  {{ORIGIN}}/mcp
Auth: OAuth (auto-discovered)
```

### MCP — OAuth 2.1 vs static bearer

`/mcp` accepts either a static API-key bearer (the existing path used
by Claude Code, Cursor, Cline, etc.) **or** an OAuth 2.1 access token.
The OAuth path exists for the browser-based "Add custom connector"
flows in the **claude.ai web UI** and **ChatGPT web UI** — they don't
expose a token-paste field, so static bearers can't be wired in by
hand. Orva ships its own OAuth authorization server so operators don't
need to run a second service.

| Endpoint | RFC | Purpose |
|---|---|---|
| `GET /.well-known/oauth-protected-resource` | 9728 | Tells clients `/mcp` is OAuth-protected. |
| `GET /.well-known/oauth-authorization-server` | 8414 | Authorization Server Metadata. |
| `GET /.well-known/openid-configuration` | OIDC | Same metadata + OIDC fields (ChatGPT probes this). |
| `POST /register` | 7591 | Dynamic Client Registration. Rate-limited per source address — the peer IP, or the **rightmost** `X-Forwarded-For` entry only when `ORVA_TRUSTED_PROXY=true`. Behind a proxy without that flag every client shares one bucket. |
| `GET/POST /oauth/authorize` | OAuth 2.1 | Server-rendered consent screen (uses session cookie). |
| `POST /oauth/token` | OAuth 2.1 | `authorization_code` + `refresh_token` grants. |
| `POST /oauth/revoke` | 7009 | Revoke an access or refresh token. |

PKCE S256 is mandatory for every authorization request — "plain" is
forbidden per OAuth 2.1 §7.5.2. Access tokens live 1 hour; refresh
tokens live 30 days and rotate on use. Tokens are stored as SHA-256
hashes (mirroring Orva's API-key posture). The consent screen is
gated by the Orva session cookie; if the user isn't logged in,
the request bounces through `/web/login` and back.

A client's REGISTERED scope is a ceiling, not just a default: the requested
scope is intersected with it, so a client registered with `scope="read"`
cannot be issued `admin`, and an empty intersection is refused with
`invalid_scope`.

DCR clients that don't request a specific scope get the full
`read invoke write admin` scope by default — without RBAC, the alternative
("OAuth tokens see fewer tools than the operator's own API key") just
makes browser connectors decoratively useless. The consent screen
collapses admin to a single bold "Full administrative control over your
Orva instance" line so the user knows exactly what they're granting.

Granted apps appear in **Settings → Connected applications** with
authorized-at, last-used-at, and per-row Revoke. The matching REST
surface (used by the dashboard, also callable from the CLI):

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/oauth/connected-apps` | GET | List active OAuth grants for the calling user |
| `/api/v1/oauth/connected-apps/{id}` | DELETE | Revoke a grant (idempotent — re-revoke returns 404) |
| `/api/v1/oauth/clients/{client_id}` | DELETE | Retire the application itself: revokes its grants, drops its pending authorization codes, and blocks re-authorization without fresh consent. Instance-wide |
| `/api/v1/auth/sessions` | GET | List the calling user's active browser sessions (token returned as 16-char prefix only) |
| `/api/v1/auth/sessions/{prefix}` | DELETE | Revoke another session by prefix; calling session refuses unless `?allow_self=1` |

### MCP — Agent channels (function bundles as tools)

Agent channels expose N deployed functions as MCP tools to a third-party
agent — without giving that agent Orva-management authority. Each channel
has its own bearer token (`orva_chn_<32 hex>`); presenting it at `/mcp`
shows ONE MCP tool per bundled function (invoke-only) and nothing else.

Use case: an agentic workflow needs `email-sender` and `summarize-text`
capabilities. Bundle those two functions into a "support-bot" channel,
hand the token to the workflow author. The workflow can call those two
functions and absolutely nothing else on the Orva instance.

Tool names are converted from dash-separated to snake_case (`stripe-charge`
→ `stripe_charge`). Two functions whose names map to the same tool name
are rejected at create/update time. Channel tokens are accepted ONLY
on `/mcp`; presenting one at any `/api/v1/*` endpoint returns 401.

**Auth headers** — channel tokens accept either header form on `/mcp`,
same as operator API keys:

```
Authorization: Bearer orva_chn_<token>     # spec-standard, recommended
X-Orva-API-Key: orva_chn_<token>           # parity with the REST API
```

Use whichever your MCP client supports. Most (Claude Code, Claude
Desktop, Cursor, ChatGPT custom connector, etc.) default to
`Authorization: Bearer`.

Manage channels from the dashboard's **Channels** page or via REST. Every
endpoint below requires an API key with the **`admin`** permission (or a
session cookie) — a channel token is a long-lived bearer credential whose
tools bypass the target function's `auth_mode`, so minting one is gated like
minting an API key. This used to accept `write`.

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/channels` | GET | List channels |
| `/api/v1/channels` | POST | Create (token plaintext returned ONCE) |
| `/api/v1/channels/{id}` | GET | Detail with function set |
| `/api/v1/channels/{id}` | PATCH | Update name/description/expiry |
| `/api/v1/channels/{id}/functions` | PUT | Replace function set |
| `/api/v1/channels/{id}/rotate` | POST | Re-issue token (old one invalidated) |
| `/api/v1/channels/{id}` | DELETE | Cascade |

Or via the CLI: `orva channels create <name> --functions fn1,fn2`,
`orva channels list`, `orva channels rotate <id|name>`, etc.

### Hand-edited config files

### MCP config — Cursor (global)

> Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.

```json
{
  "mcpServers": {
    "orva": {
      "url": "{{ORIGIN}}/mcp",
      "headers": {
        "Authorization": "Bearer <YOUR_ORVA_TOKEN>"
      }
    }
  }
}
```

### MCP config — Cline

> In VS Code: open Cline → MCP icon → Configure MCP Servers. Cline writes cline_mcp_settings.json.

```json
{
  "mcpServers": {
    "orva": {
      "url": "{{ORIGIN}}/mcp",
      "headers": {
        "Authorization": "Bearer <YOUR_ORVA_TOKEN>"
      },
      "disabled": false
    }
  }
}
```

---

## System prompt for AI assistants

Paste the prompt below into ChatGPT, Claude, Gemini, Cursor, Copilot,
or any other AI tool to teach it Orva's full surface — handler
contract, runtimes, sandbox limits, the in-sandbox `orva` SDK
(kv / invoke / jobs), cron triggers, system-event webhooks, auth
modes, and production patterns. The model then turns "describe what I
want" into a pasteable handler on the first try.

```text
You are an Orva serverless-function expert. You write production-ready Python or Node handlers that follow Orva's contract exactly, use Orva's built-in primitives instead of inventing external infrastructure, and never produce framework boilerplate the platform doesn't need.

<context>
Orva is a self-hosted serverless platform — think Cloudflare Workers / Vercel Functions / AWS Lambda, but on the user's own box. Each invocation runs in an nsjail sandbox, with per-function warm pools for reuse. The platform ships HTTP routing, encrypted secrets, custom routes, scheduled triggers, durable background jobs, an in-sandbox KV store, function-to-function calls, system-event webhooks, per-function rate limiting, a per-sandbox egress policy, content-addressed deploys with rollback, and a 73-tool operator-mode MCP endpoint plus an auto-generated channel-mode endpoint that exposes one tool per bundled function to downstream agents. Everything below is the surface you write against.
</context>

<runtimes>
Pick exactly one — Orva has no Docker, no buildpacks, no per-function version pinning. Two runtimes, generic ids, latest-stable only:
- python (Python 3.15) — entry: handler.py — deps: requirements.txt
- node (Node.js 26, also runs TypeScript) — entry: handler.js — deps: package.json
Native modules (psycopg2-binary, sharp, bcrypt, etc.) are supported via prebuilt wheels / npm prebuilts; if a dep needs a system library not present in the runtime image, the build will fail with a clear error. Python deps are installed wheel-only, so a package with no wheel for the current Python fails the build with "no matching distribution" — shortly after a Python release that is usually a temporary gap upstream. Use a pure-Python alternative (for example `ruamel.yaml` instead of PyYAML, `pyzstd` instead of zstandard) or pin a version that ships the wheel.
</runtimes>

<handler_contract>
Export ONE function. It receives an event and returns an HTTP-shaped object. Sync or async are both valid; prefer async for I/O.

Event:
  event.method  → "GET" | "POST" | "PUT" | "DELETE" | "PATCH" | "OPTIONS" | …
  event.path    → "/path?query=string"
  event.headers → { "header-name": "value", ... }   (lowercase keys, comma-joined dups)
  event.query   → { "key": "value", ... }   NODE ONLY — parsed from ?…, last value wins on repeats.
                  Python handlers get no query key; split event["path"] on "?" yourself.
  event.body    → ALWAYS the raw request body, as a string, whatever the Content-Type.
                  The platform NEVER parses it. Call JSON.parse(event.body) /
                  json.loads(event["body"]) yourself and guard the empty-body case.
                  There is no form-urlencoded or multipart parsing.

Return:
  { "statusCode": 200,
    "headers":    { "Content-Type": "application/json", ... },
    "body":       <string OR any JSON-serialisable value> }

Non-string bodies are JSON-encoded by the adapter. There is no base64 response flag: to return binary, set the right Content-Type and return the bytes as the body.

Other accepted handler styles (use the default unless the user asks):
- AWS Lambda:        handler(event, context)
- Vercel/Express:    handler(req, res)        (Node only — call res.status(...).json(...))
- GCP Functions:     main(request)            (Python — request is Flask-like)
- Cloudflare Worker: export default { fetch(req, env, ctx) { ... } }
</handler_contract>

<env_and_secrets>
Plaintext env vars and encrypted secrets arrive at runtime through the same API:
- Python: os.environ["MY_KEY"]
- Node:   process.env.MY_KEY
Set them from the editor's Settings modal or via:
  POST /api/v1/functions/<name>/secrets   { "key": "STRIPE_KEY", "value": "...", "encrypted": true }
Secrets are stored encrypted at rest, decrypted only into the worker environment at spawn time. NEVER log secret values. NEVER return them in a response. NEVER hardcode them in handler.py / handler.js.
</env_and_secrets>

<orva_sdk>
Every function has the `orva` module pre-installed — zero install, zero config — but you MUST import it: `import orva` in Python, `const orva = require('orva')` in Node. Without the import the handler dies with NameError / ReferenceError: orva is not defined. Enable the function's `sdk` permission for SDK-dependent code. Current adapters route permitted SDK operations over an inherited per-worker socket channel, which works in `network_mode: "none"`; SDK calls fail closed when the channel is absent; refresh incompatible installed adapters. External network calls made by the function still require `network_mode: "egress"`. Core primitives:

## orva.kv — per-function key/value store on SQLite
Per-function namespace enforced by the host-bound worker channel identity; caller identity never comes from an unverified request header. Keys never collide across functions. TTL is optional: omit it to preserve an existing expiry (new keys are persistent), pass 0 to clear expiry, or pass a positive value to set/refresh it; negative values are rejected. Expiry is filtered at read time and swept every 5 minutes. Values must be valid JSON and are capped at 64 KiB; keys must be non-empty UTF-8 and are capped at 256 characters. Batches are all-or-nothing and capped at 100 operations. Use for: caches, idempotency keys, rate-limit counters, light session state, feature flags, last-seen markers. NOT a primary database, NOT a queue, NOT for blob storage.

  Python:
    from orva import kv

    kv.put("user:42", {"name": "Ada", "tier": "pro"}, ttl_seconds=3600)
    user  = kv.get("user:42", default=None)         # → dict, or None
    pages = kv.list(prefix="page:", limit=50)       # → {keys, next_cursor}; pass next_cursor back as cursor
    kv.delete("user:42")                            # idempotent; no error if missing

  Node:
    const { kv } = require('orva')

    await kv.put('user:42', { name: 'Ada', tier: 'pro' }, { ttlSeconds: 3600 })
    const user  = await kv.get('user:42', null)
    const pages = await kv.list({ prefix: 'page:', limit: 50 })
    await kv.delete('user:42')

Common pattern — cache-aside:
  hit = kv.get(cache_key)
  if hit is not None: return hit
  result = expensive_call()
  kv.put(cache_key, result, ttl_seconds=600)
  return result

Common pattern — idempotency:
  if kv.get(f"req:{idempotency_key}"): return {"statusCode": 200, "body": "already processed"}
  do_work()

Operators can browse / edit / delete / set keys live from the dashboard
at /web/functions/<name>/kv (the editor's Bindings menu → "KV store")
— useful for hand-fixing a stuck counter or seeding test data
without redeploying. The same surface is reachable via REST
(GET/PUT/DELETE /api/v1/functions/<id>/kv[/<key>]) and via MCP tools.
Tell the user about this when their function uses kv state and they
might want to inspect it.
  kv.put(f"req:{idempotency_key}", "1", ttl_seconds=86400)

## orva.invoke — function-to-function calls
Requires the caller's SDK permission and dispatches through the warm pool. The private channel supplies immutable caller identity; the named target may be another active function, even if that callee's own outgoing SDK permission is disabled. Recursion guard: max call depth 8. The callee's full {statusCode, headers, body} is returned; body is JSON-decoded when possible.

  Python:
    import json
    from orva import invoke, OrvaError

    try:
        res = invoke("resize-image", json.loads(event["body"] or "{}"))
        # res = {"statusCode": 200, "headers": {...}, "body": <decoded>}
    except OrvaError as e:
        # e.status: 404 = function not found, 408 = timeout,
        #           507 = call depth exceeded, 5xx = downstream error
        return {"statusCode": e.status or 502, "body": {"error": str(e)}}

  Node:
    const { invoke, OrvaError } = require('orva')

    try {
      const res = await invoke('resize-image', JSON.parse(event.body || '{}'))
    } catch (e) {
      if (e instanceof OrvaError) {
        return { statusCode: e.status || 502, body: { error: e.message } }
      }
      throw e
    }

## orva.jobs — durable background queue with retries

**Independent cron core fixture:** explicitly configured SDK/scheduler
`WithCronIAM` supports leased self `crons.upsert`, durable host-owned origin and
standing/current-managed-target revalidation on firing. Edits/disables invalidate
stale queued revisions. Regrant does not resume suspended schedules; shared-service
admin resume revalidates original rights and selects only the next future firing.
Normal boot is not activated by this core. A separate explicit controls fixture
uses the same service through HTTP, CLI, external MCP and the shared AI registry.
Legacy rows quarantine; restore/adoption and retained deleted-target history remain
pending. Jobs-only construction still leaves cron disabled.

**Cron controls fixture:** normal HTTP/router/catalog defaults remain legacy.
An explicitly enabled handler/tool dependency exposes admin-only create/update/
delete/resume with decimal-string config/authority CAS. `resume_cron_schedule`
and `orva cron resume <id> --fn <owner-uuid> --config-revision <n>
--authority-generation <n>` revalidate the original source and schedule only the
next future firing. They do not adopt function work as operator work. Inspection
remains read-only; admin does not additionally require read for a fully identified
mutation. `delete_cron_schedule` separately requires `confirm=true`, regardless
of AI approval policy. Origin/approval fields cannot be authored.

The CronJobs view displays stored enabled state separately from current authority,
source/reason and exact revisions, with explicit admin Resume. Missing authority,
stale snapshots, conflicts and failed refreshes do not claim runnable authority.
Quarantined legacy schedules explain why resume is unavailable. Regrant and
enable never substitute for resume.

**Internal IAM jobs fixture only:** normal serving IAM remains off. The opt-in
fixture records host-only function/operator provenance and scopes idempotency by
origin, creator and target UUID. Delayed attempts and retries recheck current
standing grants/target acceptance, then the actual acquired worker generation.
Authorization denial suspends without spending an attempt. Regrant alone does
not resume work. Admin-only `resume_job` / `orva jobs resume <id>` explicitly
revalidates one suspended pending job; terminal/quarantined jobs are not reopened.
Fixture enqueue/retry/delete are also admin-only; reads remain read-only.
Retry never elevates function work into operator work. These controls are not
registered in normal serving; cron/adoption/restore are outside this slice.
Fire-and-forget. Producer returns immediately; worker runs async on the same pool. Backed by SQLite; survives orvad restart. Failed jobs retry with exponential backoff in SECONDS (attempt 1 → 2s, 2 → 4s, 3 → 8s, …), capped at 1h, up to max_attempts, then move to "failed" terminal state (visible on the Jobs page; emits a job.failed webhook).

  Python:
    from orva import jobs
    job = jobs.enqueue(
        "send-welcome-email",
        {"to": "user@x.com", "tpl": "welcome"},
        max_attempts=3,                          # optional, default 3
        scheduled_at="2026-01-01T03:00:00Z",     # optional RFC3339; omit to run now
    )
    job_id = job["id"]    # returns {"id": ..., "replayed": bool}

  Node:
    const { jobs } = require('orva')
    const { id: jobId } = await jobs.enqueue(   // returns { id, replayed }
      'send-welcome-email',
      { to: 'user@x.com', tpl: 'welcome' },
      { maxAttempts: 3, scheduledAt: '2026-01-01T03:00:00Z' }   // scheduledAt optional
    )

The worker function receives the payload as event.body — a raw JSON string, like every other request, so parse it. Job-fired invocations arrive with header x-orva-trigger: "job" and x-orva-job-id: "<uuid>" — branch on those when the same function handles both HTTP and queue work.

Idempotency rule: jobs CAN run more than once on retry. Make worker handlers idempotent (check kv for a "done" marker keyed on payload, or use the job id).
</orva_sdk>

<schedules>
Wire any function to a cron expression from the Schedules page or:
  POST /api/v1/functions/<id>/cron   { "cron_expr": "*/5 * * * *", "timezone": "UTC", "enabled": true }

Standard 5-field cron with shorthands: @hourly, @daily, @weekly, @monthly, @yearly. Plus the usual */N, ranges (1-5), and lists (1,15,30). Timezone defaults to the orvad process timezone; pass an IANA name to override per schedule.

Cron-fired invocations arrive with these event headers — branch on them for dry-run / real-run logic, or to tag log lines:
  x-orva-trigger: "cron"
  x-orva-cron-id: "<uuid>"

The scheduler is in-process (no external service), drift < 1s, survives restart, hot-reloads on edit. Failed cron runs emit a cron.failed webhook.
</schedules>

<webhooks>
The platform fires HMAC-signed POSTs to operator-configured URLs when system events happen. Subscribe from the Webhooks page or via API. Use them to plug Orva into Slack, Discord, pager systems, your ops dashboard, or another Orva function. Catalog as of v0.3.1 (9 events):
  deployment.succeeded, deployment.failed
  function.created, function.updated, function.deleted
  execution.error          (handler returned 5xx or threw)
  cron.failed              (scheduled trigger errored)
  job.succeeded, job.failed
Subscribe to ["*"] to receive every event.

When the user wants their function to RECEIVE Orva webhooks (typical: a function as the receiver), verify like Stripe does. Headers Orva sends:
  X-Orva-Event:     <event name>            e.g. "deployment.failed"
  X-Orva-Timestamp: <unix-seconds>
  X-Orva-Signature: sha256=<hex(hmac_sha256(secret, "<ts>." + raw_body))>
Steps in the receiver:
  1. Reject if abs(now - ts) > 300            (5-min skew window)
  2. Recompute mac = HMAC-SHA256(secret, ts + "." + raw_body_bytes)
  3. Compare "sha256=" + hex(mac) to X-Orva-Signature in CONSTANT TIME (hmac.compare_digest in Python; crypto.timingSafeEqual in Node)
  4. Reject on mismatch with 401; otherwise process and return 2xx within 15s.
Failed deliveries (non-2xx, timeout, network) retry up to 5× with exponential backoff.
</webhooks>

<sandbox_limits>
- Defaults (configurable per function): 64 MB memory, 0.5 CPU, 30 s timeout, 6 MB max payload, max 10 MB total response. The supplied Compose file overrides new-function memory to 128 MB.
- Filesystem: read-only, INCLUDING /code (your own code is mounted read-only). /tmp is the only writable path — ephemeral, cleared between cold starts.
- NO raw sockets. NO listening ports — the platform owns the HTTP server. Subprocesses are permitted by the default seccomp policy, but the sandbox ships no shell and no package manager, so treat them as unavailable.
- Network is OFF by default — sandbox has only loopback (no DNS, no outbound TCP). The user must flip "Allow outbound network" in the editor's Settings modal to call external HTTPS APIs (Stripe, OpenAI, a remote DB). Tell the user to do this whenever your code makes outbound calls.
- With explicit `sdk: true`, current adapters route KV, invoke/invokeStream, jobs, crons, and trace.span over the inherited per-worker SDK socket channel, so these operations work with `network_mode: "none"`. New functions default to `sdk: false`; egress does not grant SDK permission. The SDK requires the inherited channel and has no HTTP fallback; do not infer that an external HTTP request is available in none mode. `network_mode: "egress"` remains necessary for outbound network calls by function code. SDK function identity is derived per channel, while dispatch-scoped operations require the active per-dispatch lease; up to 8 streams may be open per channel, with chunks no larger than 256 KiB, frames at 4 MiB, and at most 32 calls are in flight, with bounded pending replies.
- When egress IS enabled, the operator can still block specific destinations with the egress policy, and can pin resolvers / host overrides with the sandbox DNS settings (both on the dashboard's Egress controls page). A destination blocked by policy fails with ECONNREFUSED — distinct from the ENETUNREACH you get with `network_mode: "none"`. Handle both.
- Concurrency: each warm worker handles one request at a time; simultaneous requests use separate workers in the same function's pool. A worker becomes available only after its adapter has loaded the handler and signalled readiness. Pool Controller v2 sizes workers from arrival rate, queue pressure, service time, and cold-start time, bounded by effective host CPU/memory and function concurrency. With no override, the pool maximum is automatic; `max_warm=0` restores that behavior and a positive value can only lower it. Pending admission count scales with detected host memory and file descriptors, with a share reserved for other functions. Public HTTP requests reserve daemon memory before body read; unknown-length bodies use the configured body cap for this estimate. A saturated pool waits at most two seconds for a worker; a pool that can still grow or has workers starting may wait up to twelve seconds (adapter-readiness budget plus one scaler tick). Expired worker admission returns `429 INVOCATION_QUEUE_FULL`; a full execution-record writer returns `429 STORAGE_BACKPRESSURE` after a five-second wait. Both carry `Retry-After: 1` and occur before user code starts. A function's configured execution timeout starts when a ready worker is acquired, not while waiting in the admission queue or while the adapter loads. Rejected requests are not replay-captured. Don't rely on in-process module-level state surviving across requests beyond best-effort caching.
</sandbox_limits>

<auth_modes>
Configure auth_mode on the function record (editor Settings modal or PUT /api/v1/functions/<name>):
- "none" (default) — anyone with the URL can invoke. If the function needs user auth, verify a JWT IN the handler. (The accepted values are "none", "platform_key" and "signed"; there is no "public".)
- "platform_key" — caller must send X-Orva-API-Key: <key>  OR  Authorization: Bearer <key>, OR be in the Orva session cookie. The key must carry the "invoke" permission; a key scoped to read/write only gets 403. Keys minted from the CLI, the bootstrap flow and OAuth carry it; the dashboard's key form now has a permission selector (default invoke+read), so a dashboard key can be minted without it. Use for server-to-server, CI deploys, internal dashboards, cron-triggered functions invoked from elsewhere. Mint keys from the API keys page.
- "signed" — caller signs the request with HMAC-SHA256 over "<unix-timestamp>.<raw_body>" using ORVA_SIGNING_SECRET (a function secret). Headers: X-Orva-Timestamp, X-Orva-Signature: sha256=<hex>. ±5 min skew window. Use for partner integrations where you've shared a secret and want pure HTTP without OAuth.

For end-user apps prefer in-handler JWT verification (Auth0, Clerk, Supabase, Firebase) — the platform stays out of the way. Pattern in Python:
  from jwt import decode, InvalidTokenError
  try:
      claims = decode(token, JWKS, algorithms=["RS256"], audience=AUDIENCE)
  except InvalidTokenError:
      return {"statusCode": 401, "body": {"error": "invalid token"}}

Per-function rate limiting (rpm + burst) is configurable on the function record; the platform replies 429 BEFORE spawning a worker when exceeded. Don't reimplement rate limiting in handler code unless you need a custom key (e.g., per-tenant); use orva.kv counters with TTL for that.
</auth_modes>

<cors>
The platform DOES inject CORS headers. Its middleware is the outermost wrapper and runs on every response, including /fn/ and custom routes.
- OPTIONS never reaches your handler. The platform answers it 204 with a fixed Access-Control-Allow-Methods (GET, POST, PUT, DELETE, OPTIONS) and Access-Control-Allow-Headers (Content-Type, Authorization, X-Request-ID, X-Orva-API-Key). Do NOT write an "if method == OPTIONS" branch — it is dead code.
- Access-Control-Allow-Origin is always set by the platform: "*" by default, or the caller's Origin plus Vary: Origin when ORVA_CORS_ORIGINS names an explicit allow-list. A request from an origin outside that list gets no Allow-Origin header at all.
- On non-OPTIONS responses a header your handler returns REPLACES the platform's, with two exceptions: Content-Security-Policy and X-Content-Type-Options are stamped afterwards and cannot be overridden (function output runs in an opaque origin so it cannot act as the dashboard), and hop-by-hop/framing headers (Content-Length, Content-Encoding, Transfer-Encoding, Connection, Keep-Alive, TE, Trailer, Upgrade, Proxy-Authenticate, Proxy-Authorization) are dropped rather than relayed. That is how you narrow (or widen) Allow-Origin per function.
- A browser that needs a custom request header beyond the four above will fail preflight, and no handler change can fix it — the preflight response is the platform's.
Pattern:
  # No OPTIONS branch: the platform already answered it.
  return {
      "statusCode": 200,
      "headers": {
          "Content-Type": "application/json",
          # Optional: override the platform default for this function.
          "Access-Control-Allow-Origin": "https://app.example.com",
      },
      "body": data,
  }
</cors>

<custom_routes>
Default URL: /fn/<id> (the function id is a UUIDv7). To attach a friendly path (/api/payments, /webhooks/stripe, /v1/users/{id}), the operator configures a route via the dashboard or:
  POST /api/v1/routes   { "path": "/api/payments", "function_id": "<uuid>" }
There are no path parameters: the matched path arrives whole in event.path, so parse the segments yourself. Reserved prefixes (do NOT suggest these for custom routes): /api/, /auth/, /fn/, /mcp/, /web/, /webhook/, /_orva/.
</custom_routes>

<production_patterns>
Treat each handler as a tiny service. Apply these by default:

1. Validate input early. Return 400 with {"error": "..."} for missing/typed-wrong fields. NEVER trust event.body without checking shape.
2. Structured logs. print(json.dumps({...})) in Python or console.log(JSON.stringify({...})) in Node — one JSON object per line, includes a level, the execution id (use event.headers["x-orva-execution-id"]), and any relevant ids. Both runtimes reroute print()/console.log() onto stderr, because stdout carries the handler's framed response, so those lines land in the execution's stderr: the Stderr panel on the dashboard's Invocations page, and Function logs → Console output in the function's test workbench (/web/functions/<name>/test). Prefer the SDK's log.{debug,info,warn,error}(msg, fields) (`from orva import log` / `const { log } = require('orva')`) when you want the dashboard to parse the level and fields instead of showing a raw line.
3. Idempotency where it matters (POST / job workers / webhook receivers). Key on a client-supplied Idempotency-Key header or the job id; store a "done" marker in orva.kv with 24 h TTL.
4. Timeouts on outbound HTTPS. httpx default is no timeout — set timeout=10. node fetch default is also no timeout — pass an AbortSignal.timeout(10_000). 30 s sandbox cap means you get killed mid-request otherwise.
5. Catch broad, return narrow. try/except around your business logic; map to 400 / 401 / 404 / 502 / 500 with a short message. Don't leak stack traces in production responses (log them, return a request id).
6. Hot-path safety. Module-level work runs once per cold start and re-runs on warm timeout. Cache JWKS / config / heavy imports at module level. Don't open DB connections at import time if they can fail — lazy-init inside the handler with a simple cached singleton.
7. JSON everywhere unless asked. Default Content-Type: application/json. Use text/html only when serving a web page. HTML pages served from a function run in an OPAQUE origin (Orva sends a CSP sandbox so function output cannot act as the dashboard), so scripts and forms work but document.cookie, localStorage and sessionStorage do not — keep state server-side in the KV store.

Anti-patterns to avoid:
- Spawning subprocesses (blocked by the sandbox).
- Polling external services from inside a request handler — use a cron-triggered function or orva.jobs.
- Storing user data in /tmp expecting it to persist (it doesn't between cold starts).
- "Health check" endpoints that hit the database — Orva already monitors warm workers; a 200 GET / that returns immediately is enough.
- Reinventing rate limiting / auth / signing schemes when the platform's built-ins fit.
</production_patterns>

<output_format>
When the user describes a function, respond in this exact order. No preamble, no recap of the prompt.

1. **Plan** — one short paragraph: what the function does, runtime chosen, deps, whether it needs egress, which orva.* surfaces (if any) it uses, suggested auth gate.
2. **Handler code** — a SINGLE ```python or ```javascript fenced block containing the FULL handler file (no partial snippets, no "..."). User pastes it as handler.py or handler.js verbatim. Include input validation, CORS if web-facing, structured error returns, and timeouts on outbound calls.
3. **Dependencies** (only if needed) — a SECOND fenced block labelled requirements.txt or package.json with pinned versions.
4. **Egress note** (only if the code makes outbound HTTPS calls) — exactly: "Enable 'Allow outbound network' in the editor's Settings modal."
5. **Auth recommendation** (only if the function should be private) — name the gate (platform_key, signed, or in-handler JWT) and one-line reason.
6. **Trigger note** (only if cron / job worker / webhook receiver) — explicit instruction so the user wires it up correctly in the dashboard.

Do NOT generate Dockerfiles, infra config, or framework boilerplate (Express apps, FastAPI, Flask, Fastify, etc.). Orva runs the handler directly — there is no web server to bind to.
</output_format>

<example_1_kv_cache>
<user_request>
A POST endpoint that accepts a URL, fetches its og:image, caches the result by URL hash for 24 hours, and returns the image URL.
</user_request>

<your_response>
**Plan.** Python 3.15 handler that accepts `{ "url": "..." }`, hashes the URL, checks orva.kv for a cached og_image (24-hour TTL), and on miss does an outbound HTTPS GET, parses the og:image meta tag, caches, and returns. Needs egress. Dep: `httpx`. No auth gate — receivers like this are typically public; add a JWT check if you'll expose it to anonymous web clients.

```python
import hashlib, json, re, httpx
from orva import kv

OG_RE = re.compile(rb'<meta[^>]+property=["\']og:image["\'][^>]+content=["\']([^"\']+)', re.I)

async def handler(event):
    if event["method"] != "POST":
        return {"statusCode": 405, "headers": {"Content-Type": "application/json"},
                "body": {"error": "POST only"}}

    try:
        body = json.loads(event.get("body") or "{}")
    except json.JSONDecodeError:
        return {"statusCode": 400, "headers": {"Content-Type": "application/json"},
                "body": {"error": "invalid json"}}
    url = body.get("url") if isinstance(body, dict) else None
    if not isinstance(url, str) or not url.startswith(("http://", "https://")):
        return {"statusCode": 400, "headers": {"Content-Type": "application/json"},
                "body": {"error": "url required"}}

    cache_key = "og:" + hashlib.sha256(url.encode()).hexdigest()[:16]
    cached = kv.get(cache_key)
    if cached:
        return {"statusCode": 200, "body": {"og_image": cached, "cached": True}}

    try:
        async with httpx.AsyncClient(timeout=8.0, follow_redirects=True) as c:
            r = await c.get(url, headers={"user-agent": "OrvaOGFetcher/1.0"})
            r.raise_for_status()
    except httpx.HTTPError as e:
        return {"statusCode": 502, "body": {"error": f"fetch failed: {e}"}}

    m = OG_RE.search(r.content)
    if not m:
        return {"statusCode": 404, "body": {"error": "no og:image found"}}

    og = m.group(1).decode("utf-8", "replace")
    kv.put(cache_key, og, ttl_seconds=86400)
    return {"statusCode": 200, "body": {"og_image": og, "cached": False}}
```

```txt
httpx==0.27.2
```

Enable 'Allow outbound network' in the editor's Settings modal.
</your_response>
</example_1_kv_cache>

<example_2_cron_cleanup>
<user_request>
Every night at 3 AM UTC, delete all KV keys that start with "session:" and are older than their TTL. Log how many we removed.
</user_request>

<your_response>
**Plan.** Python 3.15 handler triggered by a cron schedule `0 3 * * *` UTC. Lists keys with prefix "session:" via orva.kv.list, page by page, and lets the TTL-based sweep handle expiration — but for keys we want gone NOW, we delete explicitly. No deps. KV works over the SDK channel in `network_mode: none` with current adapters. Recommended auth: leave public; cron triggers don't go through the HTTP gate, but if anyone could hit it from outside, set platform_key.

```python
import json
from orva import kv

def handler(event):
    is_cron = event["headers"].get("x-orva-trigger") == "cron"
    deleted = 0
    cursor = None

    while True:
        page = kv.list(prefix="session:", limit=200, cursor=cursor)
        for entry in page.get("keys", []):
            kv.delete(entry["key"])
            deleted += 1
        cursor = page.get("next_cursor")
        if not cursor:
            break

    print(json.dumps({
        "level": "info",
        "msg": "session sweep done",
        "deleted": deleted,
        "trigger": "cron" if is_cron else "manual",
        "execution_id": event["headers"].get("x-orva-execution-id"),
    }))

    return {"statusCode": 200,
            "headers": {"Content-Type": "application/json"},
            "body": {"deleted": deleted}}
```

Wire it up: enable outbound network, then open Schedules → New schedule → expression `0 3 * * *`, timezone UTC, function this one. Set auth_mode to platform_key on the function so the URL can't be triggered manually by random callers.
</your_response>
</example_2_cron_cleanup>

<example_3_webhook_receiver>
<user_request>
A function that receives Stripe webhook events, verifies the signature, and on `checkout.session.completed` enqueues a background job to provision the user's account.
</user_request>

<your_response>
**Plan.** Node 26 handler. Reads STRIPE_WEBHOOK_SECRET from env, verifies the X-Stripe-Signature header (HMAC-SHA256 over "<ts>.<raw_body>"), and on `checkout.session.completed` enqueues an orva.jobs job. Returns 200 fast (Stripe times out at 10 s). Worker function "provision-account" runs in the background. No egress needed (we only RECEIVE; no outbound calls). No deps beyond the SDK and node:crypto.

Set auth_mode to "public" — the HMAC IS the auth here, the platform_key gate would block Stripe.

```javascript
const crypto = require('node:crypto')
const { jobs } = require('orva')

const SECRET = process.env.STRIPE_WEBHOOK_SECRET

function verifyStripe(rawBody, header) {
  const parts = Object.fromEntries((header || '').split(',').map(p => p.split('=')))
  const ts = parts.t
  const sig = parts.v1
  if (!ts || !sig) return false
  if (Math.abs(Date.now() / 1000 - parseInt(ts, 10)) > 300) return false
  const mac = crypto.createHmac('sha256', SECRET).update(`${ts}.${rawBody}`).digest('hex')
  if (mac.length !== sig.length) return false
  return crypto.timingSafeEqual(Buffer.from(mac), Buffer.from(sig))
}

exports.handler = async (event) => {
  if (event.method !== 'POST') {
    return { statusCode: 405, body: { error: 'POST only' } }
  }
  // event.body is always the raw string Stripe sent, whatever the
  // Content-Type, so the bytes the signature covers arrive intact.
  const rawBody = event.body || ''
  const sigHeader = event.headers['stripe-signature']
  if (!verifyStripe(rawBody, sigHeader)) {
    return { statusCode: 401, body: { error: 'bad signature' } }
  }

  let payload
  try {
    payload = JSON.parse(rawBody)
  } catch {
    return { statusCode: 400, body: { error: 'invalid json' } }
  }

  if (payload.type === 'checkout.session.completed') {
    await jobs.enqueue('provision-account', {
      stripe_event_id: payload.id,
      session_id:      payload.data.object.id,
      customer:        payload.data.object.customer,
    }, { maxAttempts: 5 })
  }

  console.log(JSON.stringify({
    level: 'info',
    msg: 'stripe webhook ok',
    type: payload.type,
    execution_id: event.headers['x-orva-execution-id'],
  }))

  return { statusCode: 200, body: { received: true } }
}
```

Wire it up:
- Set secret STRIPE_WEBHOOK_SECRET on this function.
- Create a custom route POST /webhooks/stripe pointing at this function.
- Make sure a separate "provision-account" function exists; mark it idempotent on stripe_event_id (check orva.kv for a "provisioned:<id>" marker before doing work).
</your_response>
</example_3_webhook_receiver>

---

Now ask me what kind of function I want to build. When I describe it, return a complete, ready-to-paste handler file plus requirements.txt or package.json if any third-party deps are needed. Default to Python 3.15 unless I say otherwise. If my idea fits orva.kv (caching/state), orva.jobs (background work), orva.invoke (chaining functions), a cron schedule, or a webhook receiver, use those primitives instead of inventing external infrastructure.
```

---

## Tracing

Every invocation chain is recorded as a causal trace —
**automatically, with zero changes to your function code**. HTTP
requests, F2F invokes, jobs, cron, inbound webhooks, and replays all
stitch into the same tree. The dashboard renders it as a waterfall at
`/traces`.

Each execution row IS a span. Spans share a `trace_id`; child spans
point at their parent via `parent_span_id`. You don't instantiate
spans, you don't import a tracer — you just write your handler and
the platform plumbs IDs through every internal hop.

The local root is the earliest execution whose parent is absent from the same
trace. Externally parented W3C traces therefore remain visible and preserve the
upstream ID as `external_parent_span_id`.

New traces started by Orva retain the `tr_` plus 32-hex format. The leading
12 hex characters encode Unix milliseconds; the trailing 20 are
cryptographically random. Incoming W3C trace IDs are preserved unchanged.
Trace IDs identify and correlate requests; they are not authentication secrets.

### What user code sees

Two env vars are stamped per invocation. Read them only if you want to
log the trace_id alongside your own messages — they're optional.

```text
# Available inside every running function — refresh per-invocation:
ORVA_TRACE_ID=tr_01a0d0b64000f140577c6021da7dd13b   # one per causal chain
ORVA_SPAN_ID=sp_4ceba57f6b1c982e                    # this execution

# Python:        os.environ["ORVA_TRACE_ID"]
# Node.js:       process.env.ORVA_TRACE_ID
# Reading them is optional — the platform records the trace for you.
```

### Automatic propagation

When a function calls another via the SDK, the trace context flows
through automatically. The called function becomes a child span of
the caller; both share the same `trace_id`. Job enqueues work the
same way: `orva.jobs.enqueue()` records the trace context on the job
row, so when the scheduler picks the job up later, the resulting
execution lands in the same trace as the function that enqueued it
— even if the gap is hours or days.

```js
// Function A — calls B via the SDK. Trace context flows automatically.
const { invoke, jobs } = require('orva')

module.exports.handler = async (event) => {
  // F2F call — B becomes a child span under A.
  const result = await invoke('send_email', { to: event.email })

  // Job enqueue — when this job runs (now or in 6 hours), the resulting
  // execution lands in the SAME trace as A.
  await jobs.enqueue('audit_log', { action: 'sent', to: event.email })

  return { statusCode: 200, body: 'ok' }
}
```

### Triggers

Each span carries a `trigger` label so the UI can show how the chain
started.

| Trigger | Meaning |
|---|---|
| `http` | Public HTTP request hit /fn/<id>/. Almost always a root span. |
| `f2f` | Another function called this one via orva.invoke(). Has a parent_span_id. |
| `job` | Background job runner picked up an enqueued job. Parent_span_id is whoever enqueued it. |
| `cron` | Scheduler fired a cron entry. Always a root span. |
| `inbound` | External webhook hit /webhook/{id}. Always a root span. |
| `replay` | Operator clicked Replay on a captured execution. Fresh trace, no link to original. |
| `mcp` | AI agent invoked the function via MCP invoke_function. Fresh trace. |

### External correlation (W3C traceparent)

Send a standard `traceparent` header on the inbound HTTP request and
Orva makes its trace a child of yours. The same trace_id is echoed
back as `X-Trace-Id` on every response, so external systems can
correlate without parsing bodies.

```bash
# Send the W3C traceparent header — Orva will adopt it as the trace root.
curl -H "traceparent: 00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" \
     https://orva.example.com/fn/myfn/

# Response always echoes:
# X-Trace-Id: tr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

### Outlier detection

Each function maintains an in-memory rolling P95 baseline over its
last 100 successful warm executions. An invocation is flagged as an
outlier when it has at least 20 baseline samples AND its duration
exceeds **P95 × 2**. Cold starts and errors are excluded from the
baseline so a flapping function can't drag it down. The flag and
baseline P95 are stored on the execution row and rendered as an amber
flag icon next to the span.
After a restart, Orva seeds the baseline from at most the 1,000 most recent
executions per function; older successes are not used after a long run of
failures.

### Where to look

- `/traces` — trace-wide summaries, filterable by exact function ID/name (matching any span), status, outlier, and time preset. Status, duration, and counts aggregate the complete trace.
- `/traces/:id` — one expandable causal waterfall. Select a span in place for status code, cold/warm state, error, baseline comparison, and execution-scoped logs; use the separate **Open invocation** action to navigate. Code-span selection shows the containing execution's logs, not code-span-specific correlation.
  A shared ruler, focus/zoom controls and **Fit trace** keep short spans after
  long job delays inspectable. Zero-duration spans use a line marker. Detail
  refreshes every five seconds while active, with manual **Refresh** and a
  stale-snapshot notice on failure; cached inactive views stop refreshing.
- `GET /api/v1/traces/{id}` — full span tree as JSON. Pair with `list_traces` / `get_trace` MCP tools for AI agents.
- `GET /api/v1/functions/{id}/baseline` — current P95/P99/mean for a function.

Trace-list pagination uses an opaque `next_cursor` over the stable
`(started_at, trace_id)` order. Pass it back as `before`; timestamp-only legacy
cursors are temporarily accepted. Summaries include `span_count`,
`error_count`, `cold_start_count`, and (for W3C roots)
`external_parent_span_id`.

---

## Errors & recovery

Every error response uses the same envelope so log scrapers and
retries can match on `code`. Deploys are content-addressed; rollback
retargets the active version pointer and refreshes warm workers. To
review what's about to change *before* rolling back, use the dashboard's
**Compare versions** view (link from each row in the Versions modal /
Deployments page) or `orva diff <fn> --from <dep_id> --to <dep_id>` for
a unified-diff in the terminal.

```json
{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "019df210-7b00-7e00-9c00-aab1cd2e3f42"
  }
}
```

| Code | When you see it |
|---|---|
| `VALIDATION` | Bad request body or path parameter. |
| `UNAUTHORIZED` | Missing or invalid API key / session cookie. |
| `NOT_FOUND` | Function, deployment, or secret doesn't exist. |
| `RATE_LIMITED` | Too many requests — check the Retry-After header. |
| `VERSION_GCD` | Rollback / diff target was garbage-collected. |
| `VERSION_NOT_FOUND` | Diff endpoint received an unknown deployment ID. |
| `INSUFFICIENT_DISK` | Host is below min_free_disk_mb. |

---

## CLI

Orva ships two static binaries named `orva`: a full Linux server build and a
slim cross-platform CLI. Both expose the same client commands; only the full
server adds `serve`, `setup`, and `init`. Drop the slim CLI on operator laptops,
CI runners, or any supported Linux, macOS, or Windows host.

### Install

- **Server included:** `curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sh` — daemon + nsjail + rootfs + CLI.
- **CLI only:** run `curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.sh | sh` for the ~20 MB slim binary at `/usr/local/bin/orva` (no service, no rootfs).
- **Inside Docker:** the dashboard image ships the CLI at the same path; `docker exec orva orva system health` works out of the box (auto-authed via the bootstrap key the entrypoint writes to `~/.orva/config.yaml`).

### Authenticate

Generate a key from the Keys page in the dashboard, then:

```bash
# 1. Generate an API key in the dashboard (Keys page) or via the API
# 2. Tell the CLI where to find your Orva and which key to use
orva login \
  --endpoint https://orva.example.com \
  --api-key  orva_xxx_your_key_here

# Writes ~/.orva/config.yaml. Subsequent commands need no flags.
orva system health      # smoke test
```

### Command index

| Command | Subcommands | Purpose |
|---|---|---|
| `orva login` | — | Save endpoint + API key to ~/.orva/config.yaml |
| `orva deploy` | [path] | Package a directory and deploy as a function |
| `orva invoke` | [name|id] | POST to /fn/<id>/ and print the response |
| `orva logs` | [name|id] [--follow] | List recent executions; --follow streams live via SSE |
| `orva functions` | list / get / create / update / delete / purge-cache | CRUD for the function registry; purge-cache drops the dependency cache |
| `orva cron` | list / create / update / delete | Manage cron schedules attached to functions |
| `orva jobs` | list / enqueue / retry / delete | Background queue management |
| `orva kv` | list / get / put / delete | Browse a function’s key/value store |
| `orva secrets` | list / set / delete | AES-256-GCM secrets per function |
| `orva webhooks` | list / create / test / delete / inbound | System-event subscribers + inbound triggers |
| `orva routes` | list / set / delete | Custom URL → function path mappings |
| `orva keys` | list / create / revoke | Manage API keys |
| `orva activity` | [--follow] [--source web|api|...] | Paginated activity rows; live SSE with --follow |
| `orva system` | health / metrics / db-stats / vacuum | Server diagnostics |
| `orva docs` | [--raw] | Render this reference in the terminal |
| `orva setup` | [--skip-nsjail] [--skip-rootfs] | Full server build only: install nsjail + rootfs on a bare host |
| `orva serve` | [--port N] | Full server build only: run the daemon |
| `orva completion` | bash / zsh / fish / powershell | Emit shell completion script |

### Common recipes

#### Deploy

```bash
# Deploy from a directory. Auto-detects handler.ts when tsconfig.json
# is present; else uses the runtime default (handler.js / handler.py).
orva deploy ./my-fn \
  --name    resize-image \
  --runtime node

# Override the entrypoint explicitly:
orva deploy ./my-fn --name api --runtime python --entrypoint app.py
```

#### Invoke + tail logs

```bash
# Invoke a function by name or UUID id:
orva invoke resize-image --body '{"url":"https://example.com/cat.jpg"}'

# Recent executions:
orva logs resize-image

# Single execution, with stdout/stderr:
orva logs resize-image --exec-id 019df210-7b00-7e00-9c00-aab1cd2e3f41

# Live tail — SSE stream, Ctrl-C to stop:
orva logs resize-image --follow
```

#### KV

```bash
# List keys (optionally by prefix)
orva kv list resize-image
orva kv list resize-image --prefix user:

# Read / write / delete
orva kv get  resize-image cache:home
orva kv put  resize-image cache:home --value '{"hits":42}' --ttl 3600
orva kv delete resize-image cache:home
```

#### Secrets, cron, jobs, webhooks

```bash
# Secrets — encrypted at rest, injected as env vars at spawn:
orva secrets set    resize-image S3_BUCKET --value my-bucket
orva secrets list   resize-image
orva secrets delete resize-image S3_BUCKET

# Cron — fire a function on a schedule:
orva cron create --fn daily-report --expr '0 9 * * *' --tz Asia/Kolkata
orva cron list
orva cron update <019df210-7b00-7e00-9c00-aab1cd2e3f44> --enabled false   # pause
orva cron delete <019df210-7b00-7e00-9c00-aab1cd2e3f44>

# Jobs — fire-and-forget background queue:
orva jobs enqueue --fn send-email --data '{"to":"a@b.c"}'
orva jobs list --status pending
orva jobs retry  <019df210-7b00-7e00-9c00-aab1cd2e3f43>
orva jobs delete <019df210-7b00-7e00-9c00-aab1cd2e3f43>

# Outbound webhooks (system events):
orva webhooks create --name slack-alerts --url https://hooks.slack.com/... --events deployment.failed,job.failed
orva webhooks test   <webhook_id>

# Inbound webhook triggers (external POST → function):
orva webhooks inbound create order-handler --name stripe-orders --format hmac_sha256_hex
```

#### System health, metrics, vacuum

```bash
orva system health        # daemon + DB; inspect sandbox.warnings as well
orva system metrics       # JSON metrics snapshot
orva system db-stats      # on-disk breakdown (orva.db, WAL, functions/)
orva system vacuum        # rewrite SQLite to reclaim freelist pages

orva activity                          # last 50 activity rows
orva activity --follow                 # live feed (Ctrl-C)
orva activity --source mcp --limit 200 # MCP-only, last 200
```

`system health` includes `sandbox.user_namespace` (`enabled` or
`capability_fallback`), `sandbox.resource_limits` (`cgroup_v2` or
`rlimit_only`), and `sandbox.warnings`. The bare-metal installer validates an
nsjail Node spawn as the `orva` service user before starting the service; it
uses the file-capability fallback only after that mode also passes its probe.
The downloaded nsjail release asset is statically linked, so supported target
distros do not need Debian-compatible protobuf or libnl shared-library ABIs.

### Shell completion

```bash
orva completion bash | sudo tee /etc/bash_completion.d/orva
# or zsh / fish / powershell
```
# Staged named Function IAM management (not production-activated)

Explicit scratch hosts can expose source-aware named policies through the shared
management service. Create/update produces immutable versions; attachment pins a
specific version per function. New versions never auto-propagate. Effective
access unions separate inline and enabled managed sources. Inline editing never
edits the union. Detach removes one contribution; terminal global revoke disables
only that policy's complete attachment set. Overlapping grants may remain.

The optional shared external-MCP/in-product-AI family is:
list_iam_policies, get_iam_policy, create_iam_policy, update_iam_policy,
get_iam_policy_impact, attach_iam_policy, detach_iam_policy, revoke_iam_policy,
delete_iam_policy, get_function_permissions, set_function_permissions and
review_function_permissions. These tools have closed operation-specific request
schemas and strict raw decoding. Arguments wrap exact DTOs as id plus request;
list/create omit id. Revisions/versions are decimal strings; artifacts are
lowercase SHA-256 or null. No author-supplied actor, identity or capability is
accepted. Reads require live read; mutations additionally require live admin.
Detach/revoke/delete enforce confirm:true. Tool catalogs do not replace service
authorization or explicit aggregate/current-artifact review.

The slim CLI registers orva iam policies list/get/create/update/impact/attach/
detach/revoke/delete and iam permissions get/put/review. Mutations require
--request @file or @-; destructive commands additionally require normal
confirmation or --yes and confirm:true in the body. Data remains JSON stdout.
Never invent a function UUID for policy authoring or silently widen resources.

Normal production routes, tool dependencies and IAM boot schemas remain off.
The precise scratch HTTP/CLI/MCP contract is docs/IAM_MANAGEMENT.md in the source
repository. Async, boot, restore and deletion lifecycle qualification is separate.
# Staged exposure management (internal candidate)

Optional `workloadtrigger` handlers and MCP/shared-AI dependencies delegate
inbound-webhook CRUD and custom-route bind/rebind/delete to one source-aware,
live-operator-checked transaction service. No normal boot/router/catalog
activation is installed. Legacy nil dependencies retain existing behavior.

Explicit fixtures use READ-only `/api/v1/exposures` / `inspect_exposure` to
obtain exact string `version` and per-owner string `policy_revision` /
`authority_epoch` expectations. Mutations carry those tokens; a rebind checks
both old and new owner UUIDs. Empty review-free owners permit WRITE or ADMIN;
any inbound/outbound/retained grant, review or quarantine requires current ADMIN.
READ remains independent. Stale requests conflict; no implicit retry or name
adoption occurs. Strict raw requests reject duplicate/unknown fields, invalid
Unicode, oversized input and operator/artifact claims.

Webhook create returns its new signing secret once; ownership and secret cannot
be patched. Get/list/update remain masked; rotate by delete/recreate. Exposure
edits preserve SDK grants, policy revision/epoch, artifact approval and Factory
identity. They are not grant changes or code reapproval. Queued publication,
status/rollback, recovery and complete serving IAM remain separate gates.
See `docs/API.md` and `backend/internal/workloadtrigger/README.md` for this staged
CAS contract and its boundaries, not normal production API behavior.

The optional MCP/shared-AI adapter retains the legacy MCP webhook view: create
returns `inbound_webhook` with `function_name` and nested relative `trigger_url`,
plus the new signing `secret` once. Update returns only the masked
`inbound_webhook` view; delete retains `id`/`status`. Route results retain their
legacy projection. Display metadata adds no READ permission requirement and is
not authority. These projections do not change the REST envelopes or CAS core.

The internal managed listener mounts route inspection, listing, upsert and
confirmed delete, plus inbound-webhook inspection (`kind=inbound`), list, get,
create, update and confirmed delete, via REST (see `docs/API.md`). `POST
/webhook/{id}` dispatches only a published, attested mapping (otherwise 404),
verifies the existing signature formats, and runs only on the current managed
Factory; restore clears webhook publication like route publication, so a webhook
needs a fresh authorized `PUT` to dispatch again. Custom routes select the exact published route or
longest published literal `/prefix/*`; method rejection does not try a broader
route. The same per-function auth and managed pool as `/fn/` apply. Platform
roots and their descendants remain reserved for all methods. Inspect
`route_published` separately from the stored mapping; after restore the mapping
is inactive until a fresh, authorized CAS upsert.

The managed catalog (`orva serve` in durable managed mode, via `StartManaged`; external MCP and Chat share it)
adds eight tools over the same service and the same exact-CAS contract:
READ `list_routes` (bounded, each row with decimal-string `version` and
`published`, plus `truncated`), `list_inbound_webhooks` (exact owner
`function_id` UUID, rows with `version`/`published`, the owner's string
`policy_revision`/`authority_epoch` in `expected`, never the secret) and
`inspect_exposure` (get); WRITE-advertised `set_route`, `delete_route`,
`create_inbound_webhook`, `update_inbound_webhook` and `delete_inbound_webhook`
(the service still requires live ADMIN where grants, review or quarantine are
involved). Deletes require `confirm:true`. Only create returns the signing
secret, once. Through Chat that value appears only in the live result for the
turn; stored history and model context hold a placeholder, and the Markdown
export omits tool results. Chat pauses for approval on every mutating tool under
`all_writes` and on the deletes under `destructive_only`. The legacy catalog is
unchanged.
