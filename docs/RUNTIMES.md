# Function runtimes

Orva ships two runtimes, both with the same handler protocol. Inside
the sandbox your function file is at `/code/<entrypoint>` (default
`handler.js` for Node, `handler.py` for Python). The Orva-provided
adapter wraps your handler and speaks a JSON frame protocol over
stdin/stdout to the parent `orvad` process.

| runtime  | base image       | entrypoint default | dependency file    |
|----------|------------------|--------------------|--------------------|
| `node`   | node:24-slim     | `handler.js`       | `package.json`     |
| `python` | python:3.14-slim | `handler.py`       | `requirements.txt` |

## The `event` object

When a request arrives, the adapter calls your handler with a
single argument:

```json
{
  "method": "POST",
  "path": "/health",
  "headers": {
    "content-type": "application/json",
    "x-orva-execution-id": "019df200-7b00-7e00-9c00-aab1cd2e3f41",
    "x-orva-function-id": "019df200-7b00-7e00-9c00-aab1cd2e3f40",
    ...
  },
  "body": "<raw request body as string>",
  "query": {"q": "search"}          // Node only — see below
}
```

- `path` is everything after `/fn/<id>` (or the matched custom route
  prefix). For a request to `POST /fn/xyz/health`, `event.path === "/health"`.
- `body` is the raw request body, always a string, whatever the
  `Content-Type`. Parse it yourself — the platform never does.
- `query` is present **only on Node**, where the adapter parses it out of
  `path` (last value wins on repeats). Python handlers get no `query` key and
  should split `event["path"]` on `?` themselves.
- Headers are normalized to lowercase keys.

Those five keys are the whole event — there are no `rawPath` / `httpMethod`
aliases. What makes AWS-Lambda-style code run unchanged is the calling
convention, not the key names: the Node adapter accepts several handler
shapes besides the default: `handler(event, context)` (Lambda) and
`handler(req, res)` (Vercel/Express). The Python adapter accepts a plain
`handler(event)` and also speaks WSGI and ASGI, so Flask and FastAPI apps run
unmodified.

## TypeScript

TypeScript is a first-class deploy path on the `node` runtime — there is no
separate TS runtime. Include a `tsconfig.json` and declare `typescript` in your
`package.json` dependencies or devDependencies, and the build runs
`tsc --project tsconfig.json` after the install step.

`compilerOptions.outDir` decides where the output lands (default `dist`, and
`"."` means "beside the sources"). It must stay inside your code directory: a
traversing or absolute `outDir` is refused and the build falls back to `dist`.

The file you authored stays in `entrypoint`; the compiled file the worker
actually loads is recorded separately in `run_entrypoint`. You never set
`run_entrypoint` yourself — deploy and rollback both derive it.

The bundled `orva` SDK is **not** on `tsc`'s module path. It lives at
`/opt/orva/node_modules/orva/`, and TypeScript only walks `node_modules`
upward from your sources under `/code`, so `import { kv } from 'orva'` fails
the build with `TS2307: Cannot find module 'orva'`. Point `paths` at the
declarations the runtime ships:

```json
{ "compilerOptions": { "module": "commonjs", "baseUrl": ".",
  "paths": { "orva": ["/opt/orva/node_modules/orva/orva.d.ts"] } } }
```

Keep `module: commonjs`. `tsc` then emits `require('orva')`, which the adapter
resolves at runtime; an ESM emit leaves a bare `import`, and the adapter patches
only Node's CommonJS resolver.

## Handler shape — Node.js

```js
exports.handler = async (event) => {
  const path   = event.path || '/';
  const method = event.method;

  if (method === 'GET' && path === '/health') {
    return { ok: true, ts: Date.now() };
  }

  if (method === 'POST' && path === '/echo') {
    const body = JSON.parse(event.body || '{}');
    return { you_sent: body };
  }

  return { error: 'Not Found' };
};
```

Whatever you `return` is JSON-serialized as the response body. HTTP
status code is **200** unless you throw. To return a non-200, throw
or use the AWS-shape return:

```js
return {
  statusCode: 404,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ error: 'Not Found' }),
};
```

## Handler shape — Python

```python
def handler(req):
    method = req.get('method')
    path   = req.get('path') or '/'

    if method == 'GET' and path == '/health':
        return {'ok': True}

    if method == 'POST' and path == '/echo':
        import json
        body = json.loads(req.get('body') or '{}')
        return {'you_sent': body}

    return {'error': 'Not Found'}
```

The adapter looks for a top-level `handler` callable. Async (`async def`)
is supported on the python runtime (Python 3.14); the adapter awaits if needed.

## Dependencies

Include a `package.json` (Node) or `requirements.txt` (Python) in
your deploy and Orva runs `npm install` / `pip install` during the
build phase. **The installer runs inside nsjail**, not on the host — a
dependency's install scripts are third-party code and are treated as such. The
installed packages
land in the version directory and are visible at `/code/node_modules`
or `/code/<package>/` inside the sandbox.

```bash
# Node
echo '{"dependencies":{"lodash":"^4.17.21"}}' > package.json

# Python
echo "requests==2.31.0" > requirements.txt
```

Pip uses `--only-binary=:all:` so wheels are required (no compilation
during the build). For native deps that don't ship wheels, prebuild
them and include the `.whl` in the deploy.

## Environment variables

Two sources, merged at spawn time:

1. **Function `env_vars`** — set via `PUT /api/v1/functions/{id}` or
   the dashboard's Editor → Environment variables panel.
2. **Function secrets** — encrypted at rest, set via
   `POST /api/v1/functions/{id}/secrets`. Same env-var contract; the
   only difference is that secret values are AES-256-GCM encrypted in
   the SQLite row.

Both arrive at your handler as `process.env` (Node) or `os.environ`
(Python). Plus a few Orva-provided vars:

| var | what |
|---|---|
| `ORVA_FUNCTION_ID`  | the function's ID (`019df200-7b00-7e00-9c00-aab1cd2e3f40`) |
| `ORVA_FUNCTION_NAME`| the function's name |
| `ORVA_MEMORY_MB`    | the function's declared memory limit |
| `ORVA_TIMEOUT_MS`   | the function's configured timeout — also surfaced as `getRemainingTimeInMillis()` / `get_remaining_time_in_millis()` on the context argument, and as `ctx.timeoutMs` |
| `ORVA_ENTRYPOINT`   | the file the worker **loads**. For a compiled runtime that is the build output (`dist/handler.js`), not the file you authored (`handler.ts`) — see `run_entrypoint` in the TypeScript section. Omitted when the function has no explicit entrypoint |
| `ORVA_EXECUTION_ID` | this invocation's ID — useful for log correlation |

The first five are **spawn-scoped**: a warm worker's environment is fixed when
it is created, so they cannot vary per request. `ORVA_EXECUTION_ID`,
`ORVA_TRACE_ID` and `ORVA_SPAN_ID` are genuinely per-request and are refreshed
by the adapter from the `x-orva-*` request headers on every invocation.

Because the first five are baked in at spawn, changing `timeout_ms`,
`memory_mb`, `cpus`, `env_vars` or `entrypoint` drains the idle workers so
the next spawn picks up the new value.

`entrypoint` used to be the exception: it did not trigger the drain, so the
PUT returned 200 while every warm worker kept serving the old handler
indefinitely. It now behaves like the rest. (It is also validated as a
contained relative path on write — it names a file inside the function's own
code directory, and is read back by `GET /functions/{id}/source`.)

## Filesystem inside the sandbox

```
/                  rootfs (debian-slim with the runtime)
├── code/          bind-mount of versions/<hash>/, READ-ONLY
│   ├── handler.js (or .py) — your code
│   ├── node_modules/ or installed pkgs
│   ├── main.js / main.py — Orva's adapter wrapper (calls your handler)
│   └── .orva-ready
├── tmp/           tmpfs, private to this spawn (writable)
├── usr/, lib/, etc.   from the runtime rootfs
```

Functions can write freely to `/tmp` (it's wiped when the worker exits).
**Cannot** write to `/code` — the bind mount is read-only by design.

## Resource limits

Set via `PUT /api/v1/functions/{id}` (or onboard with `memory_mb` and
`cpus`). Defaults: 64 MB memory, 0.5 CPUs. Both can be overridden at
deploy time.

- Memory cap is **1.5×** the declared `memory_mb` at the cgroup level.
  The 0.5× headroom lets the kernel reclaim via PSI pressure before
  OOM-killing.
- CPU is enforced as bandwidth (`cpu.max`), not affinity — so the
  scheduler can load-balance freely.
- `pids.max` defaults to 32 per spawn. Fork-bombing fails with `EAGAIN`.

## Cold starts vs warm hits

The first invocation after deploy or after a long idle period
spawns a fresh worker (~50–500 ms depending on runtime size and deps).
Subsequent invocations land on idle workers from the pool (~2–15 ms).

Per-function pool sizing uses 60-second stable demand, 6-second burst demand,
queue pressure, service p95, and cold-start p95. You tune only the policy
floor/ceiling via
`PUT /api/v1/pool/config`:

```json
{
  "function_id": "019df200-7b00-7e00-9c00-aab1cd2e3f40",
  "min_warm": 2,
  "max_warm": 32,
  "idle_ttl_seconds": 600,
  "scale_to_zero": false
}
```

See [`docs/CONFIG.md`](CONFIG.md) for everything tunable.

## Deploy checklist

```bash
# 1. Create the function
curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions \
  -d '{"name":"my-fn","runtime":"node","memory_mb":128,"cpus":1}'

# 2. Deploy code (inline)
curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions/019df200-7b00-7e00-9c00-aab1cd2e3f40/deploy-inline \
  -d '{"code":"module.exports = async () => ({hello:\"world\"});"}'

# 3. Invoke
curl -X POST -H "X-Orva-API-Key: $KEY" \
  http://localhost:8443/fn/xyz \
  -d '{}'
# → {"hello":"world"}
```

For deploys with deps, include `dependencies` (a string with the file
contents):

```bash
curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions/019df200-7b00-7e00-9c00-aab1cd2e3f40/deploy-inline \
  -d '{
    "code": "import requests\ndef handler(req): return {\"v\": requests.__version__}",
    "filename": "handler.py",
    "dependencies": "requests==2.31.0\n"
  }'
```
