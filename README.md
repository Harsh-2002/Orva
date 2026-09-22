# Orva

[![Release](https://img.shields.io/github/v/release/Harsh-2002/Orva?style=flat-square&label=release&color=7c5cbf)](https://github.com/Harsh-2002/Orva/releases/latest)
[![CI](https://img.shields.io/github/actions/workflow/status/Harsh-2002/Orva/ci.yml?branch=main&style=flat-square&label=CI)](https://github.com/Harsh-2002/Orva/actions/workflows/ci.yml)
[![Docker](https://img.shields.io/badge/docker-ghcr.io%2Fharsh--2002%2Forva-2496ED?style=flat-square&logo=docker&logoColor=white)](https://github.com/Harsh-2002/Orva/pkgs/container/orva)
[![License](https://img.shields.io/badge/license-Apache%202.0-green?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![Node](https://img.shields.io/badge/Node.js-24-339933?style=flat-square&logo=node.js&logoColor=white)](https://nodejs.org)
[![Python](https://img.shields.io/badge/Python-3.14-3776AB?style=flat-square&logo=python&logoColor=white)](https://python.org)

**Self-hosted Functions-as-a-Service for your homelab or on-prem server.**

Write a JavaScript, TypeScript, or Python function, hit deploy — Orva runs it in an
isolated nsjail sandbox and serves it over HTTP. One Docker container gives you the
runtime, a dashboard, a CLI, an MCP server, and a built-in AI assistant. It's for the
Lambda/Workers workflow — write a function, invoke it over HTTP, schedule it, chain it —
on hardware you control (a Pi, a homelab box, a VPS, bare metal). No cloud account, no
per-invocation billing.

> **Active development.** Solid for homelabs, side-projects, and internal tools.
> Not recommended for customer-facing production yet.

---

## Features

- **Two runtimes** — `node` (Node.js 24, also runs TypeScript) and `python` (Python 3.14).
- **Real isolation** — every function runs in its own nsjail sandbox: user namespace, chroot, cgroup v2 limits, and a seccomp syscall allowlist. → [Security](docs/SECURITY.md)
- **Warm pools** — idle workers stay resident per function, so repeat calls skip cold start.
- **Built-in primitives** — per-function KV store, background jobs (retries + backoff), cron schedules, function-to-function calls, encrypted secrets, custom routes, and signed inbound webhooks.
- **Distributed tracing** — every HTTP → F2F → job chain shares one trace, with a waterfall view and zero code changes.
- **Versioning** — content-hashed deploys with one-click (or one-command) rollback and side-by-side diffs.
- **MCP + AI** — a 73-tool operator MCP server at `/mcp` and a built-in agentic AI assistant (dashboard or `orva chat`) that operate your instance with your own provider key. → [AI & MCP](#ai--mcp)
- **Templates** — 21 starters (Stripe/GitHub webhooks, JWT/OAuth, CSV→JSON, URL shortener, …) in the editor.

---

## Quick start

**Docker Compose** (recommended for persistent setups):

```bash
curl -fsSL https://raw.githubusercontent.com/Harsh-2002/Orva/main/docker-compose.yml -o docker-compose.yml
docker compose up -d
```

Compose publishes on **http://localhost:3000** (it maps `3000:8443`), not
`:8443`. Open it and finish the short onboarding flow.

**Bare metal / VM** (systemd or OpenRC):

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sh
```

The installer supports Debian/Ubuntu, Fedora/RHEL/Rocky/Alma, Alpine, Arch, and
openSUSE. It verifies a real sandbox as the unprivileged `orva` service user and
stops with a host-specific diagnosis if the kernel cannot run Orva securely.

**CLI only** (operator laptop or CI runner):

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.sh | sh   # macOS / Linux
irm  https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.ps1 | iex        # Windows
```

Installers are idempotent — re-run to upgrade; pin a version with `ORVA_VERSION=vYYYY.MM.DD`.
TLS, reverse proxy, and backup guidance: [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).

After onboarding, connect the CLI and deploy:

```bash
orva login http://localhost:3000
orva deploy ./src --name my-fn --runtime node   # runtimes: node | python
orva invoke my-fn --body '{"name":"world"}'
orva logs my-fn --follow
```

CLI reference: [docs/CLI.md](docs/CLI.md).

---

## Environment variables

Defaults work out of the box. These are all supported server environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `ORVA_DATA_DIR` | `~/.orva` from a source binary; `/var/lib/orva` in packaged installs | Database, function code, runtime rootfs, and other persistent data. |
| `ORVA_HOST` | `0.0.0.0` | HTTP bind address. Use `127.0.0.1` behind a local reverse proxy. |
| `ORVA_PORT` | `8443` | Plain-HTTP listen port. |
| `ORVA_WRITE_TIMEOUT_SEC` | `60` | Buffered-response write timeout in seconds. |
| `ORVA_MAX_BODY_BYTES` | `6291456` | JSON API request-body limit; deploy and restore uploads use their own limits. |
| `ORVA_CORS_ORIGINS` | `*` | Comma-separated browser and MCP Origin allow-list. |
| `ORVA_SECCOMP_POLICY` | `default` | Sandbox policy: `default`, `strict`, `permissive`, or `disabled`. |
| `ORVA_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `ORVA_SECURE_COOKIES` | `false` | Force secure session cookies when Orva cannot observe TLS or `X-Forwarded-Proto`. |
| `ORVA_TRUSTED_PROXY` | `false` | Trust proxy client-IP headers; enable only behind a proxy that rewrites them. |
| `ORVA_SESSION_DAYS` | `7` | Session-cookie lifetime in days. |
| `ORVA_PPROF_ADDR` | unset | Optional loopback-only Go diagnostics listener, for example `127.0.0.1:6060`. |
| `ORVA_IMAGE` | image-stamped; unset on bare metal | Image identity reported by health and Settings. |
| `ORVA_DISABLE_USERNS` | installer-selected; `0` in Docker | `0` uses user namespaces; `1` uses the installer-verified capability fallback. |
| `ORVA_CGROUPV2_MOUNT` | auto-detected | Delegated cgroup v2 subtree used for hard CPU, memory, and process limits. |
| `ORVA_INTERNAL_API_BASE` | auto-detected | Internal SDK base URL; override only when sandbox-to-host routing detection is wrong. |

See [docs/CONFIG.md](docs/CONFIG.md) for validation rules, security implications, and examples.

---

## Write a function

The `orva` SDK is preinstalled in every sandbox — KV, function-to-function invoke, and
background jobs, with nothing to install:

```js
// Node — Python uses the same shape: from orva import kv, invoke, jobs
const { kv, invoke, jobs } = require('orva')

exports.handler = async (event) => {
  await kv.put('hits', (await kv.get('hits') || 0) + 1)
  await invoke('send-notification', { msg: 'hello' })   // child span in the same trace
  await jobs.enqueue('audit-log', { at: Date.now() })   // async, retried on failure
  return { statusCode: 200, body: { ok: true } }
}
```

The SDK reaches Orva over HTTP, and `network_mode` defaults to `none` — so set
the function's network mode to **egress** or every SDK call fails with
`ENETUNREACH`. The editor warns you at deploy time when it spots the import.

Handler contract, event shape, and streaming: [docs/RUNTIMES.md](docs/RUNTIMES.md).

---

## AI & MCP

The dashboard's **AI** section and `orva chat` operate the instance using your own
OpenAI, Anthropic, or OpenAI-compatible provider key, with optional approval before writes.

To use an external MCP client such as Claude Code or Cursor, add:

```
https://your-orva-instance/mcp
```

The MCP server can create and deploy functions, invoke them, read logs, manage secrets,
and browse KV. See the [Orva reference](docs/reference.md).

---

## License

Apache-2.0
