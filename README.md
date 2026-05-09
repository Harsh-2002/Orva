# Orva

**Self-hosted Functions-as-a-Service for your homelab or on-prem server.**

Write a JavaScript or Python function, hit deploy — Orva runs it in an
isolated nsjail sandbox and exposes it over HTTP. No AWS account. No per-invocation
billing. No cold-start lottery. Just a single Docker container on hardware you
already own.

> **Active development.** Stable enough for homelabs, side-projects, and
> experiments. Not recommended for customer-facing production yet.

---

## One-command install

```bash
docker run -d --name orva \
  -p 8443:8443 \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  -v orva-data:/var/lib/orva \
  ghcr.io/harsh-2002/orva:latest
```

Then open **http://localhost:8443**, complete onboarding (takes ~30 seconds),
and you have a fully operational FaaS platform.

**docker-compose** (recommended for persistent setups):

```bash
curl -fsSL https://raw.githubusercontent.com/Harsh-2002/Orva/main/docker-compose.yml -o docker-compose.yml
docker compose up -d
```

---

## Screenshots

### System Overview — live metrics, warm pools, response-time percentiles
![System Overview Dashboard](docs/screenshots/system-overview-dashboard.jpeg)

### Functions — every deployed handler, runtime, resources, last deploy date
![Functions List](docs/screenshots/functions-list.jpeg)

### Editor — write and deploy code directly in the browser, with build logs and test pane
![Function Editor](docs/screenshots/function-editor.jpeg)

### Traces — automatic causal waterfall across HTTP → F2F invokes → background jobs
![Distributed Traces](docs/screenshots/distributed-traces.jpeg)

### Activity — live feed of every API call, CLI command, MCP tool, and webhook delivery
![Activity Live Feed](docs/screenshots/activity-live-feed.jpeg)

### Invocation Logs — every execution captured with request, response, duration, trace link
![Invocation Logs](docs/screenshots/invocation-logs.jpeg)

### API Keys — long-lived bearer tokens for CI, scripts, and AI agents
![API Keys](docs/screenshots/api-keys.jpeg)

### Built-in Docs — full reference always available at `/web/docs`, no tab-switching
![Built-in Docs](docs/screenshots/built-in-docs.jpeg)

### Firewall & DNS — per-function egress rules, custom resolvers, blocklist
![Firewall and DNS](docs/screenshots/firewall-and-dns.jpeg)

### Settings — storage, account, OAuth-connected apps (Claude, ChatWise, etc.)
![Settings and OAuth](docs/screenshots/settings-oauth.jpeg)

---

## What you get

| Feature | Detail |
|---|---|
| **Runtimes** | Node.js 22, Node.js 24, Python 3.13, Python 3.14, TypeScript (via Node) |
| **Isolation** | Every invocation in a fresh nsjail sandbox — user namespace, chroot, cgroup v2, seccomp |
| **Warm pools** | One pool per function; idle workers stay ready so the next call skips the cold start |
| **KV store** | `kv.put / kv.get / kv.delete / kv.list` — SQLite-backed, per-function, optional TTL |
| **Background jobs** | `jobs.enqueue(name, payload)` — persisted queue with retries + exponential backoff |
| **Cron schedules** | Fire any function on a cron expression; last/next run visible in the dashboard |
| **Function-to-function** | `invoke('name', payload)` calls another function via the warm pool — no HTTP roundtrip |
| **Tracing** | Automatic causal trace tree: HTTP → F2F → job spans linked by `trace_id`, zero code changes |
| **Custom routes** | Map `/webhooks/stripe` → a function; external callers never need your function UUID |
| **Secrets** | Encrypted at rest, injected as env vars at sandbox spawn; never logged |
| **Inbound webhooks** | Signed trigger endpoints (GitHub, Stripe, Slack, generic HMAC) that fan into a function |
| **Rollback** | Every deploy is content-hashed and archived; one click to revert |
| **MCP server** | 70 tools at `/mcp` — Claude Code, Cursor, ChatWise, or any MCP client can manage everything |
| **OAuth 2.1** | Add Orva as a custom connector in claude.ai or ChatGPT web UI — no API key copy-paste |
| **16 templates** | Stripe webhooks, GitHub events, JWT auth, OAuth, CSV→JSON, URL shortener, and more |

---

## SDK (inside a function)

No HTTP client setup needed. The `orva` module is pre-installed in every sandbox.

```js
// Node.js
const { kv, invoke, jobs } = require('orva')

exports.handler = async (event) => {
  // KV store
  await kv.put('counter', (await kv.get('counter') || 0) + 1)

  // Call another function — becomes a child span in the same trace
  const result = await invoke('send-notification', { msg: 'hello' })

  // Background job — runs async, retries on failure
  await jobs.enqueue('audit-log', { action: 'ping', at: Date.now() })

  return { statusCode: 200, body: { ok: true } }
}
```

```python
# Python
from orva import kv, invoke, jobs

def handler(event):
    kv.put("counter", (kv.get("counter") or 0) + 1)
    result = invoke("send-notification", {"msg": "hello"})
    jobs.enqueue("audit-log", {"action": "ping"})
    return {"statusCode": 200, "body": {"ok": True}}
```

---

## How isolation works

Every invocation runs in a fresh **nsjail** sandbox:

- **User namespace** — code thinks it's UID 0 but maps to `nobody` (65534) on the host with **zero Linux capabilities**
- **Chroot** — only the function's own versioned code directory is visible at `/code`, read-only
- **cgroup v2** — `memory.max`, `cpu.max`, `pids.max` enforced per-spawn; host rejects spawns past 80% reservation
- **seccomp** — Kafel policy blocks ~150 dangerous syscalls (`mount`, `bpf`, `kexec_load`, etc.)
- **Network namespace** — default `none` gives only loopback; flip to `egress` per-function for outbound HTTPS

Full threat model and a verification recipe in [`docs/SECURITY.md`](docs/SECURITY.md).

---

## AI agent integration (MCP)

Orva ships a full **Model Context Protocol** server. Add it to any MCP client with one URL:

```
https://your-orva-instance/mcp
```

From there an AI agent can create functions, deploy code, invoke them, read logs, manage secrets,
browse KV state, and pull the full Orva reference docs — all without leaving the chat. Works
with Claude Code, Cursor, ChatWise, and any OAuth-capable MCP client like the claude.ai web UI.

---

## CLI

The same binary powers both server and CLI.

```bash
# Install (Linux)
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/orva-cli-linux-amd64 \
  -o /usr/local/bin/orva && chmod +x /usr/local/bin/orva

orva login --endpoint https://orva.example.com --api-key <key>
orva functions list
orva deploy my-fn ./src
orva invoke my-fn --body '{"name":"world"}'
orva logs my-fn --follow
```

Binaries: `linux-amd64`, `linux-arm64`, `darwin-arm64`. Fully static, no runtime deps.

---

## Configuration

All settings are optional — defaults work out of the box.

| Variable | Default | Description |
|---|---|---|
| `ORVA_PORT` | `8443` | HTTP listen port |
| `ORVA_DATA_DIR` | `/var/lib/orva` | SQLite DB, function code, rootfs |
| `ORVA_DEFAULT_MEMORY_MB` | `64` | Memory ceiling for new functions |
| `ORVA_DEFAULT_TIMEOUT_MS` | `30000` | Execution timeout for new functions |
| `ORVA_LOG_RETENTION_DAYS` | `7` | Days of execution logs to keep |
| `ORVA_SESSION_DAYS` | `7` | Session cookie lifetime |
| `ORVA_SECURE_COOKIES` | _false_ | Set `true` when behind HTTPS |
| `ORVA_WRITE_TIMEOUT_SEC` | `60` | HTTP write timeout (set ≥ function timeout) |

Full reference: [`docs/CONFIG.md`](docs/CONFIG.md)

---

## Build from source

```bash
git clone https://github.com/Harsh-2002/Orva.git && cd Orva
make dev          # hot-reload frontend :5173 + backend :8443
make build-all    # production binary → ./build/orva
make test         # go test ./...
```

Requires Go 1.25+, Node 22+, and nsjail on Linux for sandbox invocations.

---

## Documentation

| | |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System design, request + deploy lifecycle |
| [`docs/SECURITY.md`](docs/SECURITY.md) | Threat model, sandbox isolation, verification recipe |
| [`docs/RUNTIMES.md`](docs/RUNTIMES.md) | Handler contract, event shape, streaming |
| [`docs/API.md`](docs/API.md) | Full REST API reference |
| [`docs/CONFIG.md`](docs/CONFIG.md) | All config knobs |
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) | TLS, reverse proxy, backups, upgrades |
| [`docs/OPERATIONS.md`](docs/OPERATIONS.md) | Monitoring, troubleshooting, common errors |
| [`docs/CAPACITY.md`](docs/CAPACITY.md) | Throughput numbers + benchmark methodology |

---

## License

Apache-2.0
