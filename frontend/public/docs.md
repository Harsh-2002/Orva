# Orva — Documentation

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
def handler(event):
    body = event.get("body") or {}
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": {"hello": body.get("name", "world")},
    }
```

### Handler — Node.js

```js
exports.handler = async (event) => {
  const body = event.body || {};
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: { hello: body.name || 'world' },
  };
};
```

**Event shape:** `method`, `path`, `headers`, `query`, `body`.

**Response:** `{ statusCode, headers, body }`. Non-string bodies are
JSON-encoded by the adapter.

**Runtime env:** env vars and secrets land in `process.env` (Node) /
`os.environ` (Python).

| Runtime | ID | Entrypoint | Dependencies |
|---|---|---|---|
| Python 3.14 | `python314` | `handler.py` | `requirements.txt` |
| Python 3.13 | `python313` | `handler.py` | `requirements.txt` |
| Node.js 24 | `node24` | `handler.js` | `package.json` |
| Node.js 22 | `node22` | `handler.js` | `package.json` |

---

## Deploy & invoke

The dashboard handles day-to-day work; these calls are for CI and
automation. Builds run async — poll `/api/v1/deployments/<id>` or
stream `/api/v1/deployments/<id>/stream` until `phase: done`.

### 1. Create the function row

```bash
curl -X POST {{ORIGIN}}/api/v1/functions \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{"name":"hello","runtime":"python314","memory_mb":128,"cpus":0.5}'
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

> **Custom routes:** attach a friendly path with `POST /api/v1/routes`.
> Reserved prefixes: `/api/` `/fn/` `/mcp/` `/web/` `/_orva/`.

---

## Configuration reference

Everything below lives on the function record. Secrets are stored
encrypted and only decrypt into the worker environment at spawn time.

| Field | Purpose | Behaviour |
|---|---|---|
| `env_vars` | Plain config | Plaintext config stored on the function record. Use for feature flags and non-secret settings. |
| `/secrets` | Encrypted | AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time. |
| `network_mode` | Egress control | none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies. |
| `auth_mode` | Invoke gate | none = public. platform_key = require Orva API key. signed = require HMAC. |
| `rate_limit_per_min` | Per-IP throttle | Optional cap for public or webhook-facing functions. Exceeding it returns 429. |

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

The bundled `orva` module exposes three primitives every function can
use without extra dependencies: a per-function key/value store,
in-process calls to other Orva functions, and a fire-and-forget
background job queue.

- **`orva.kv`** — `put` / `get` / `delete` / `list`. Per-function namespace on SQLite, optional TTL.
- **`orva.invoke`** — `invoke(name, payload)`. In-process call to another function. 8-deep call cap.
- **`orva.jobs`** — `jobs.enqueue(name, payload)`. Fire-and-forget; persisted; retried with exp backoff.

### KV — get/put with TTL

### KV — Python

```python
from orva import kv

def handler(event):
    # Store with optional TTL (seconds). 0 = no expiry.
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

### Function-to-function — invoke()

### F2F — Python

```python
from orva import invoke, OrvaError

def handler(event):
    try:
        # invoke() returns the downstream {statusCode, headers, body}.
        # body is JSON-decoded when possible.
        result = invoke("resize-image", {"url": event["body"]["url"]})
        return {"statusCode": 200, "body": result["body"]}
    except OrvaError as e:
        # 404 = function not found, 507 = call depth exceeded.
        return {"statusCode": e.status or 502, "body": str(e)}
```

### F2F — Node.js

```js
const { invoke, OrvaError } = require('orva')

exports.handler = async (event) => {
  try {
    const result = await invoke('resize-image', { url: event.body.url })
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
from orva import jobs

def handler(event):
    # Fire-and-forget. Returns the job id immediately; the function
    # body runs later via the scheduler. max_attempts retries with
    # exponential backoff on 5xx / exception.
    job_id = jobs.enqueue(
        "send-welcome-email",
        {"to": event["body"]["email"]},
        max_attempts=3,
    )
    return {"statusCode": 202, "body": job_id}
```

### Jobs — Node.js

```js
const { jobs } = require('orva')

exports.handler = async (event) => {
  const jobId = await jobs.enqueue(
    'send-welcome-email',
    { to: event.body.email },
    { maxAttempts: 3 }
  )
  return { statusCode: 202, body: jobId }
}
```

> **Network mode:** the SDK reaches orvad over loopback through the
> host gateway, so the function needs `network_mode: "egress"`. On
> the default `"none"` the SDK throws `OrvaUnavailableError` with a
> clear hint.

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
curl -X PUT {{ORIGIN}}/api/v1/functions/<function_id>/cron/<cron_id> \
  -H 'X-Orva-API-Key: <YOUR_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{"enabled": false}'

# change schedule
curl -X PUT {{ORIGIN}}/api/v1/functions/<function_id>/cron/<cron_id> \
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
curl -X DELETE {{ORIGIN}}/api/v1/functions/<function_id>/cron/<cron_id> \
  -H 'X-Orva-API-Key: <YOUR_KEY>'
```

> **Cron-fired headers:** every cron-triggered invocation arrives at
> the function with `x-orva-trigger: cron` and
> `x-orva-cron-id: cron_…` on the event headers, so user code can
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

Same API surface the dashboard uses, exposed as 69 tools an agent can
call directly. API key permissions scope the available tool set.

- **Endpoint:** `{{ORIGIN}}/mcp`
- **Auth header:** `Authorization: Bearer <token>`
  (fallback: `X-Orva-API-Key: <token>`)
- **Transport:** Streamable HTTP, MCP 2025-11-25.

> Generate a token from the Docs page in the dashboard, then drop it
> into your client config (Claude Code, Claude Desktop, Cursor, Cline,
> Codex, Windsurf, ChatGPT, etc.). Either header works against the
> same API key store with identical permission gating.

### Install snippets (primary clients)

### MCP — Claude Code

> Anthropic's `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva's 57 tools.

```bash
claude mcp add --transport http --scope user orva {{ORIGIN}}/mcp --header "Authorization: Bearer <YOUR_ORVA_TOKEN>"
```

### MCP — curl

> Talk to MCP directly. Step 1 returns a session id (Mcp-Session-Id) that Step 2 references.

```bash
curl -sD - -X POST {{ORIGIN}}/mcp \
  -H 'Authorization: Bearer <YOUR_ORVA_TOKEN>' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST {{ORIGIN}}/mcp \
  -H 'Authorization: Bearer <YOUR_ORVA_TOKEN>' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Session-Id: <SID>' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
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
| `POST /register` | 7591 | Dynamic Client Registration. Per-IP rate-limited. |
| `GET/POST /oauth/authorize` | OAuth 2.1 | Server-rendered consent screen (uses session cookie). |
| `POST /oauth/token` | OAuth 2.1 | `authorization_code` + `refresh_token` grants. |
| `POST /oauth/revoke` | 7009 | Revoke an access or refresh token. |

PKCE S256 is mandatory for every authorization request — "plain" is
forbidden per OAuth 2.1 §7.5.2. Access tokens live 1 hour; refresh
tokens live 30 days and rotate on use. Tokens are stored as SHA-256
hashes (mirroring Orva's API-key posture). The consent screen is
gated by the Orva session cookie; if the user isn't logged in,
the request bounces through `/web/login` and back.

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

Manage channels from the dashboard.s **Channels** page or via REST:

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
Orva is a self-hosted serverless platform — think Cloudflare Workers / Vercel Functions / AWS Lambda, but on the user's own box. Each function runs in a firecracker-style microsandbox with cold start ~200 ms and warm reuse for ~5 minutes. The platform ships HTTP routing, encrypted secrets, custom routes, scheduled triggers, durable background jobs, an in-sandbox KV store, function-to-function calls, system-event webhooks, per-function rate limiting, an outbound firewall, content-addressed deploys with rollback, and a 57-tool MCP endpoint. Everything below is the surface you write against.
</context>

<runtimes>
Pick exactly one — Orva has no Docker, no buildpacks, no per-function Python/Node version pinning beyond this:
- python314 (default) or python313 — entry: handler.py — deps: requirements.txt
- node24 (default) or node22 — entry: handler.js — deps: package.json
Older minor versions auto-migrate to the latest patch on next deploy. Native modules (psycopg2-binary, sharp, bcrypt, etc.) are supported via prebuilt wheels / npm prebuilts; if a dep needs a system library not present in the runtime image, the build will fail with a clear error.
</runtimes>

<handler_contract>
Export ONE function. It receives an event and returns an HTTP-shaped object. Sync or async are both valid; prefer async for I/O.

Event:
  event.method  → "GET" | "POST" | "PUT" | "DELETE" | "PATCH" | "OPTIONS" | …
  event.path    → "/path?query=string"
  event.headers → { "header-name": "value", ... }   (lowercase keys, comma-joined dups)
  event.query   → { "key": "value", ... }           (parsed from ?…; repeats become arrays)
  event.body    → string OR parsed JSON value, depending on Content-Type:
                    - application/json            → parsed dict / array
                    - application/x-www-form-urlencoded → parsed dict
                    - multipart/form-data         → { fields: {...}, files: [{name, filename, contentType, data: <bytes>}] }
                    - everything else             → raw string (or bytes for binary)

Return:
  { "statusCode": 200,
    "headers":    { "Content-Type": "application/json", ... },
    "body":       <string OR any JSON-serialisable value> }

Non-string bodies are JSON-encoded by the adapter. To return binary (image, PDF), set Content-Type to the right MIME and return base64 in body with header { "x-orva-base64": "1" }.

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
Every function has the `orva` module pre-imported — zero install, zero config. It speaks to a per-process internal control-plane socket, so it works regardless of the network toggle. THREE primitives:

## orva.kv — per-function key/value store on SQLite
Per-function namespace; keys never collide across functions. Optional TTL in seconds (sweep every 5 min AND filtered at read time, so stale reads are impossible). Values JSON-serialised; cap 64 KB per value. Keys cap 256 chars. Use for: caches, idempotency keys, rate-limit counters, light session state, feature flags, last-seen markers. NOT a primary database, NOT a queue, NOT for blob storage.

  Python:
    from orva import kv

    kv.put("user:42", {"name": "Ada", "tier": "pro"}, ttl_seconds=3600)
    user  = kv.get("user:42", default=None)         # → dict, or None
    pages = kv.list(prefix="page:", limit=50)       # → [keys]; pass cursor= for pagination
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
at /web/functions/<name>/kv (the "KV" button in the editor's action
bar) — useful for hand-fixing a stuck counter or seeding test data
without redeploying. The same surface is reachable via REST
(GET/PUT/DELETE /api/v1/functions/<id>/kv[/<key>]) and via MCP tools.
Tell the user about this when their function uses kv state and they
might want to inspect it.
  kv.put(f"req:{idempotency_key}", "1", ttl_seconds=86400)

## orva.invoke — function-to-function calls (no HTTP, no auth)
Bypasses the proxy stack and dispatches via the warm pool. Faster than internal HTTP, no signing required. Recursion guard: max call depth 8. The callee's full {statusCode, headers, body} is returned; body is JSON-decoded when possible.

  Python:
    from orva import invoke, OrvaError

    try:
        res = invoke("resize-image", {"url": event["body"]["url"]})
        # res = {"statusCode": 200, "headers": {...}, "body": <decoded>}
    except OrvaError as e:
        # e.status: 404 = function not found, 408 = timeout,
        #           507 = call depth exceeded, 5xx = downstream error
        return {"statusCode": e.status or 502, "body": {"error": str(e)}}

  Node:
    const { invoke, OrvaError } = require('orva')

    try {
      const res = await invoke('resize-image', { url: event.body.url })
    } catch (e) {
      if (e instanceof OrvaError) {
        return { statusCode: e.status || 502, body: { error: e.message } }
      }
      throw e
    }

## orva.jobs — durable background queue with retries
Fire-and-forget. Producer returns immediately; worker runs async on the same pool. Backed by SQLite; survives orvad restart. Failed jobs retry with exponential backoff (1m, 2m, 4m, 8m, …) up to max_attempts, then move to "failed" terminal state (visible on the Jobs page; emits a job.failed webhook).

  Python:
    from orva import jobs
    job_id = jobs.enqueue(
        "send-welcome-email",
        {"to": "user@x.com", "tpl": "welcome"},
        delay_seconds=10,    # optional, default 0
        max_attempts=3,      # optional, default 3
    )

  Node:
    const { jobs } = require('orva')
    const jobId = await jobs.enqueue(
      'send-welcome-email',
      { to: 'user@x.com', tpl: 'welcome' },
      { delaySeconds: 10, maxAttempts: 3 }
    )

The worker function receives the payload as event.body (parsed dict). Job-fired invocations arrive with header x-orva-trigger: "job" and x-orva-job-id: "job_..." — branch on those when the same function handles both HTTP and queue work.

Idempotency rule: jobs CAN run more than once on retry. Make worker handlers idempotent (check kv for a "done" marker keyed on payload, or use the job id).
</orva_sdk>

<schedules>
Wire any function to a cron expression from the Schedules page or:
  POST /api/v1/functions/<name>/cron   { "expression": "*/5 * * * *", "timezone": "UTC", "enabled": true }

Standard 5-field cron with shorthands: @hourly, @daily, @weekly, @monthly, @yearly. Plus the usual */N, ranges (1-5), and lists (1,15,30). Timezone defaults to the orvad process timezone; pass an IANA name to override per schedule.

Cron-fired invocations arrive with these event headers — branch on them for dry-run / real-run logic, or to tag log lines:
  x-orva-trigger: "cron"
  x-orva-cron-id: "cron_..."

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
- Defaults (configurable per function): 128 MB memory, 0.5 CPU, 30 s timeout, 6 MB max payload, max 10 MB total response.
- Filesystem: read-only EXCEPT /code (your code) and /tmp (writable, ephemeral, cleared between cold starts).
- NO subprocess execution (subprocess / child_process disabled). NO raw sockets. NO listening ports — the platform owns the HTTP server.
- Network is OFF by default — sandbox has only loopback (no DNS, no outbound TCP). The user must flip "Allow outbound network" in the editor's Settings modal to call external HTTPS APIs (Stripe, OpenAI, a remote DB). Tell the user to do this whenever your code makes outbound calls.
- orva.kv / orva.invoke / orva.jobs do NOT need egress — they go over the internal control-plane socket regardless of the network toggle.
- When egress IS enabled, the operator can further restrict it via the firewall + DNS allowlist (Firewall page). Assume best-effort; handle failures.
- Concurrency: each warm worker handles one request at a time. The pool autoscales workers up to the function's max_concurrent setting. Don't rely on in-process module-level state surviving across requests beyond best-effort caching.
</sandbox_limits>

<auth_modes>
Configure auth_mode on the function record (editor Settings modal or PUT /api/v1/functions/<name>):
- "public" (default) — anyone with the URL can invoke. If the function needs user auth, verify a JWT IN the handler.
- "platform_key" — caller must send X-Orva-API-Key: <key>  OR  Authorization: Bearer <key>, OR be in the Orva session cookie. Use for server-to-server, CI deploys, internal dashboards, cron-triggered functions invoked from elsewhere. Mint keys from the API Keys page.
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
The platform never injects CORS headers. The handler controls them.
- Answer OPTIONS before any auth check.
- Attach CORS headers to EVERY response, including 401 / 500.
- Allowlist origins; do not echo "*" with credentials.
- Set Access-Control-Allow-Headers explicitly (Content-Type, Authorization, X-Requested-With, …).
Pattern:
  CORS = {
      "Access-Control-Allow-Origin":  "https://app.example.com",
      "Access-Control-Allow-Methods": "POST, OPTIONS",
      "Access-Control-Allow-Headers": "Content-Type, Authorization",
      "Access-Control-Max-Age":       "600",
  }
  if event["method"] == "OPTIONS":
      return {"statusCode": 204, "headers": CORS, "body": ""}
  # ... real handler ...
  return {"statusCode": 200, "headers": {**CORS, "Content-Type": "application/json"}, "body": data}
</cors>

<custom_routes>
Default URL: /fn/<id> (the function id is a UUIDv7). To attach a friendly path (/api/payments, /webhooks/stripe, /v1/users/{id}), the operator configures a route via the dashboard or:
  POST /api/v1/routes   { "path": "/api/payments", "function_id": "<uuid>" }
Path params with {name} are passed in event.path_params. Reserved prefixes (do NOT suggest these for custom routes): /api/, /fn/, /mcp/, /web/, /_orva/.
</custom_routes>

<production_patterns>
Treat each handler as a tiny service. Apply these by default:

1. Validate input early. Return 400 with {"error": "..."} for missing/typed-wrong fields. NEVER trust event.body without checking shape.
2. Structured logs. print(json.dumps({...})) in Python or console.log(JSON.stringify({...})) in Node — one JSON object per line, includes a level, a request id (use event.headers["x-orva-request-id"]), and any relevant ids. Logs land on the Activity page and on stdout.
3. Idempotency where it matters (POST / job workers / webhook receivers). Key on a client-supplied Idempotency-Key header or the job id; store a "done" marker in orva.kv with 24 h TTL.
4. Timeouts on outbound HTTPS. httpx default is no timeout — set timeout=10. node fetch default is also no timeout — pass an AbortSignal.timeout(10_000). 30 s sandbox cap means you get killed mid-request otherwise.
5. Catch broad, return narrow. try/except around your business logic; map to 400 / 401 / 404 / 502 / 500 with a short message. Don't leak stack traces in production responses (log them, return a request id).
6. Hot-path safety. Module-level work runs once per cold start and re-runs on warm timeout. Cache JWKS / config / heavy imports at module level. Don't open DB connections at import time if they can fail — lazy-init inside the handler with a simple cached singleton.
7. JSON everywhere unless asked. Default Content-Type: application/json. Use text/html only when serving a web page.

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
**Plan.** Python 3.14 handler that accepts `{ "url": "..." }`, hashes the URL, checks orva.kv for a cached og_image (24-hour TTL), and on miss does an outbound HTTPS GET, parses the og:image meta tag, caches, and returns. Needs egress. Dep: `httpx`. No auth gate — receivers like this are typically public; add a JWT check if you'll expose it to anonymous web clients.

```python
import hashlib, json, re, httpx
from orva import kv

OG_RE = re.compile(rb'<meta[^>]+property=["\']og:image["\'][^>]+content=["\']([^"\']+)', re.I)

async def handler(event):
    if event["method"] != "POST":
        return {"statusCode": 405, "headers": {"Content-Type": "application/json"},
                "body": {"error": "POST only"}}

    body = event.get("body") or {}
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
**Plan.** Python 3.14 handler triggered by a cron schedule `0 3 * * *` UTC. Lists keys with prefix "session:" via orva.kv.list, page by page, and lets the TTL-based sweep handle expiration — but for keys we want gone NOW, we delete explicitly. No deps. No egress. Recommended auth: leave public; cron triggers don't go through the HTTP gate, but if anyone could hit it from outside, set platform_key.

```python
import json
from orva import kv

def handler(event):
    is_cron = event["headers"].get("x-orva-trigger") == "cron"
    deleted = 0
    cursor = None

    while True:
        page = kv.list(prefix="session:", limit=200, cursor=cursor)
        for key in page.get("keys", []):
            kv.delete(key)
            deleted += 1
        cursor = page.get("cursor")
        if not cursor:
            break

    print(json.dumps({
        "level": "info",
        "msg": "session sweep done",
        "deleted": deleted,
        "trigger": "cron" if is_cron else "manual",
        "request_id": event["headers"].get("x-orva-request-id"),
    }))

    return {"statusCode": 200,
            "headers": {"Content-Type": "application/json"},
            "body": {"deleted": deleted}}
```

Wire it up: open Schedules → New Schedule → expression `0 3 * * *`, timezone UTC, function this one. Set auth_mode to platform_key on the function so the URL can't be triggered manually by random callers.
</your_response>
</example_2_cron_cleanup>

<example_3_webhook_receiver>
<user_request>
A function that receives Stripe webhook events, verifies the signature, and on `checkout.session.completed` enqueues a background job to provision the user's account.
</user_request>

<your_response>
**Plan.** Node 24 handler. Reads STRIPE_WEBHOOK_SECRET from env, verifies the X-Stripe-Signature header (HMAC-SHA256 over "<ts>.<raw_body>"), and on `checkout.session.completed` enqueues an orva.jobs job. Returns 200 fast (Stripe times out at 10 s). Worker function "provision-account" runs in the background. No egress needed (we only RECEIVE; no outbound calls). No deps beyond the SDK and node:crypto.

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
  // Stripe sends raw bytes. Orva passes the unparsed string through when
  // Content-Type isn't application/json — make sure the route's content-type
  // handling preserves the raw body. If event.body is an object (already
  // parsed), JSON.stringify it back for verification.
  const rawBody = typeof event.body === 'string' ? event.body : JSON.stringify(event.body)
  const sigHeader = event.headers['stripe-signature']
  if (!verifyStripe(rawBody, sigHeader)) {
    return { statusCode: 401, body: { error: 'bad signature' } }
  }

  let payload
  try {
    payload = typeof event.body === 'string' ? JSON.parse(event.body) : event.body
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
    request_id: event.headers['x-orva-request-id'],
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

Now ask me what kind of function I want to build. When I describe it, return a complete, ready-to-paste handler file plus requirements.txt or package.json if any third-party deps are needed. Default to Python 3.14 unless I say otherwise. If my idea fits orva.kv (caching/state), orva.jobs (background work), orva.invoke (chaining functions), a cron schedule, or a webhook receiver, use those primitives instead of inventing external infrastructure.
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

### What user code sees

Two env vars are stamped per invocation. Read them only if you want to
log the trace_id alongside your own messages — they're optional.

```text
# Available inside every running function — refresh per-invocation:
ORVA_TRACE_ID=tr_3e39f6991c66f140577c6021da7dd13b   # one per causal chain
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

### Where to look

- `/traces` — list of recent traces, filterable by function / status / outlier-only.
- `/traces/:id` — waterfall + per-span detail. Click a span to jump to its execution in the Invocations log.
- `GET /api/v1/traces/{id}` — full span tree as JSON. Pair with `list_traces` / `get_trace` MCP tools for AI agents.
- `GET /api/v1/functions/{id}/baseline` — current P95/P99/mean for a function.

---

## Errors & recovery

Every error response uses the same envelope so log scrapers and
retries can match on `code`. Deploys are content-addressed; rollback
retargets the active version pointer and refreshes warm workers.

```json
{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "req_abc123"
  }
}
```

| Code | When you see it |
|---|---|
| `VALIDATION` | Bad request body or path parameter. |
| `UNAUTHORIZED` | Missing or invalid API key / session cookie. |
| `NOT_FOUND` | Function, deployment, or secret doesn't exist. |
| `RATE_LIMITED` | Too many requests — check the Retry-After header. |
| `VERSION_GCD` | Rollback target was garbage-collected. |
| `INSUFFICIENT_DISK` | Host is below min_free_disk_mb. |

---

## CLI

`orva` is a single static binary that talks to a remote (or local)
Orva server over HTTPS. Same binary as the daemon — `orva serve`
starts a server, every other subcommand is a CLI client. Drop it on
operator laptops, CI runners, or anywhere bash runs.

### Install

- **Server included:** `curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sh` — daemon + nsjail + rootfs + CLI.
- **CLI only:** add `--cli-only` for a ~10 MB binary at `/usr/local/bin/orva` (no service, no rootfs).
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
| `orva init` | — | Scaffold an orva.yaml in the current directory |
| `orva deploy` | [path] | Package a directory and deploy as a function |
| `orva invoke` | [name|id] | POST to /fn/<id>/ and print the response |
| `orva logs` | [name|id] [--tail] | List recent executions; --tail follows live via SSE |
| `orva functions` | list / get / create / delete | CRUD for the function registry |
| `orva cron` | list / create / update / delete | Manage cron schedules attached to functions |
| `orva jobs` | list / enqueue / retry / delete | Background queue management |
| `orva kv` | list / get / put / delete | Browse a function’s key/value store |
| `orva secrets` | list / set / delete | AES-256-GCM secrets per function |
| `orva webhooks` | list / create / test / delete / inbound | System-event subscribers + inbound triggers |
| `orva routes` | list / set / delete | Custom URL → function path mappings |
| `orva keys` | list / create / revoke | Manage API keys |
| `orva activity` | [--tail] [--source web|api|...] | Paginated activity rows; live SSE with --tail |
| `orva system` | health / metrics / db-stats / vacuum | Server diagnostics |
| `orva setup` | [--skip-nsjail] [--skip-rootfs] | Install nsjail + rootfs on a bare host |
| `orva serve` | [--port N] | Run as the server daemon (not the CLI client) |
| `orva completion` | bash / zsh / fish / powershell | Emit shell completion script |

### Common recipes

#### Deploy

```bash
# Init a project in cwd (creates orva.yaml + handler stub)
orva init

# Deploy from a directory. Auto-detects handler.ts when tsconfig.json
# is present; else uses the runtime default (handler.js / handler.py).
orva deploy ./my-fn \
  --name    resize-image \
  --runtime node24

# Override the entrypoint explicitly:
orva deploy ./my-fn --name api --runtime python314 --entrypoint app.py
```

#### Invoke + tail logs

```bash
# Invoke a function by name or UUID id:
orva invoke resize-image --data '{"url":"https://example.com/cat.jpg"}'

# Recent executions:
orva logs resize-image

# Single execution, with stdout/stderr:
orva logs resize-image --exec-id exec_abc123

# Live tail — SSE stream, Ctrl-C to stop:
orva logs resize-image --tail
```

#### KV

```bash
# List keys (optionally by prefix)
orva kv list resize-image
orva kv list resize-image --prefix user:

# Read / write / delete
orva kv get  resize-image cache:home
orva kv put  resize-image cache:home '{"hits":42}' --ttl 3600
orva kv delete resize-image cache:home
```

#### Secrets, cron, jobs, webhooks

```bash
# Secrets — encrypted at rest, injected as env vars at spawn:
orva secrets set    resize-image S3_BUCKET my-bucket
orva secrets list   resize-image
orva secrets delete resize-image S3_BUCKET

# Cron — fire a function on a schedule:
orva cron create --fn daily-report --expr '0 9 * * *' --tz Asia/Kolkata
orva cron list
orva cron update <cron_id> --enabled false   # pause
orva cron delete <cron_id>

# Jobs — fire-and-forget background queue:
orva jobs enqueue --fn send-email --data '{"to":"a@b.c"}'
orva jobs list --status pending
orva jobs retry  <job_id>
orva jobs delete <job_id>

# Outbound webhooks (system events):
orva webhooks create --url https://hooks.slack.com/... --events deployment.failed,job.failed
orva webhooks test   <webhook_id>

# Inbound webhook triggers (external POST → function):
orva webhooks inbound create --fn order-handler --signature stripe
```

#### System health, metrics, vacuum

```bash
orva system health        # daemon up + DB ok
orva system metrics       # JSON metrics snapshot
orva system db-stats      # on-disk breakdown (orva.db, WAL, functions/)
orva system vacuum        # rewrite SQLite to reclaim freelist pages

orva activity                          # last 50 activity rows
orva activity --tail                   # live feed (Ctrl-C)
orva activity --source mcp --limit 200 # MCP-only, last 200
```

### Shell completion

```bash
orva completion bash | sudo tee /etc/bash_completion.d/orva
# or zsh / fish / powershell
```
