# Function runtimes

Orva ships four runtimes, all with the same handler protocol. Inside
the sandbox your function file is at `/code/<entrypoint>` (default
`handler.js` for Node, `handler.py` for Python). The Orva-provided
adapter wraps your handler and speaks a JSON frame protocol over
stdin/stdout to the parent `orvad` process.

| runtime    | base image     | entrypoint default | dependency file    |
|------------|----------------|--------------------|--------------------|
| `node22`   | node:22-slim   | `handler.js`       | `package.json`     |
| `node24`   | node:24-slim   | `handler.js`       | `package.json`     |
| `python313`| python:3.13-slim | `handler.py`     | `requirements.txt` |
| `python314`| python:3.14-slim | `handler.py`     | `requirements.txt` |

## The `event` object

When a request arrives, the adapter calls your handler with a
single argument:

```json
{
  "method": "POST",
  "path": "/health",
  "headers": {
    "content-type": "application/json",
    "x-orva-execution-id": "exec_abc123",
    "x-orva-function-id": "fn_xyz",
    ...
  },
  "body": "<raw request body as string>",
  "query": {"q": "search"}
}
```

- `path` is everything after `/api/v1/invoke/<fn-id>` (or the matched
  custom route prefix). For a request to
  `POST /api/v1/invoke/fn_xyz/health`, `event.path === "/health"`.
- `body` is the raw request body. JSON callers should `JSON.parse`
  it themselves.
- Headers are normalized to lowercase keys.

The adapter also passes `event.rawPath` and `event.httpMethod` aliases
so AWS-Lambda-style handlers Just Work without code changes.

## Handler shape — Node.js

```js
exports.handler = async (event) => {
  const path   = event.path || event.rawPath || '/';
  const method = event.method || event.httpMethod;

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
    method = req.get('method') or req.get('httpMethod')
    path   = req.get('path')   or req.get('rawPath') or '/'

    if method == 'GET' and path == '/health':
        return {'ok': True}

    if method == 'POST' and path == '/echo':
        import json
        body = json.loads(req.get('body') or '{}')
        return {'you_sent': body}

    return {'error': 'Not Found'}
```

The adapter looks for a top-level `handler` callable. Async (`async def`)
is supported on Python 3.13+; the adapter awaits if needed.

## Dependencies

Include a `package.json` (Node) or `requirements.txt` (Python) in
your deploy and Orva runs `npm install` / `pip install` during the
build phase, on the host (not in the sandbox). The installed packages
land in the version directory and are visible at `/code/node_modules`
or `/code/<package>/` inside the sandbox.

```bash
# Node
echo '{"dependencies":{"lodash":"^4.17.21"}}' > package.json

# Python
echo "requests==2.31.0" > requirements.txt
```

Pip uses `--only-binary=:all:` so wheels are required (no compilation
in the build host). For native deps that don't ship wheels, prebuild
them and include the `.whl` in the deploy.

## Environment variables

Two sources, merged at spawn time:

1. **Function `env_vars`** — set via `PUT /api/v1/functions/{id}` or
   the dashboard's Editor → Environment Variables panel.
2. **Function secrets** — encrypted at rest, set via
   `POST /api/v1/functions/{id}/secrets`. Same env-var contract; the
   only difference is that secret values are AES-256-GCM encrypted in
   the SQLite row.

Both arrive at your handler as `process.env` (Node) or `os.environ`
(Python). Plus a few Orva-provided vars:

| var | what |
|---|---|
| `ORVA_FUNCTION_ID`  | the function's ID (`fn_xyz`) |
| `ORVA_EXECUTION_ID` | this invocation's ID — useful for log correlation |
| `ORVA_ENTRYPOINT`   | your handler file (e.g. `handler.js`) |

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
`cpus`). Defaults: 128 MB memory, 0.5 CPUs. Both can be overridden at
deploy time.

- Memory cap is **1.5×** the declared `memory_mb` at the cgroup level.
  The 0.5× headroom lets the kernel reclaim via PSI pressure before
  OOM-killing.
- CPU is enforced as bandwidth (`cpu.max`), not affinity — so the
  scheduler can load-balance freely.
- `pids.max` defaults to 64 per spawn. Fork-bombing fails with `EAGAIN`.

## Cold starts vs warm hits

The first invocation after deploy or after a long idle period
spawns a fresh worker (~50–500 ms depending on runtime size and deps).
Subsequent invocations land on idle workers from the pool (~2–15 ms).

Per-function pool sizing is autoscaled based on EWMA request rate +
in-flight concurrency. You can tune the floor/ceiling via
`PUT /api/v1/pool/config`:

```json
{
  "function_id": "fn_xyz",
  "min_warm": 2,
  "max_warm": 32,
  "idle_ttl_seconds": 120,
  "target_concurrency": 10
}
```

See [`docs/CONFIG.md`](CONFIG.md) for everything tunable.

## Deploy checklist

```bash
# 1. Create the function
curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions \
  -d '{"name":"my-fn","runtime":"node22","memory_mb":128,"cpus":1}'

# 2. Deploy code (inline)
curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions/fn_xyz/deploy-inline \
  -d '{"code":"module.exports = async () => ({hello:\"world\"});"}'

# 3. Invoke
curl -X POST -H "X-Orva-API-Key: $KEY" \
  http://localhost:8443/api/v1/invoke/fn_xyz \
  -d '{}'
# → {"hello":"world"}
```

For deploys with deps, include `dependencies` (a string with the file
contents):

```bash
curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions/fn_xyz/deploy-inline \
  -d '{
    "code": "import requests\ndef handler(req): return {\"v\": requests.__version__}",
    "filename": "handler.py",
    "dependencies": "requests==2.31.0\n"
  }'
```
