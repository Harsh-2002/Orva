# docs/

Human-maintained reference documentation. **Update it in the same commit as the
code that changed it** — [`CONTRACT.md` §6a](../CONTRACT.md) is the rule and the
table of what to touch for what.

An audit on 2026-08-25 checked every document in this directory against the
source and found 181 defects, 81 of them factually wrong. The worst were not
obscure: the canonical reference described `event.body` as parsed JSON when it
has always been a raw string, so both of its headline examples were broken — the
Python one returned HTTP 500, the Node one silently returned the wrong answer.
Nothing had ever run them. **Treat a doc example as a claim about behaviour and
execute it.**

| File | Contents |
|---|---|
| `API.md` | Full REST API reference — every endpoint, request/response shapes, error codes |
| `ARCHITECTURE.md` | System design, component diagram, request lifecycle |
| `CAPACITY.md` | Sizing guide, pool tuning, resource limits per runtime |
| `CLI.md` | `orva` CLI reference — every subcommand, flags, examples |
| `CONFIG.md` | All configuration keys, environment variables, and defaults |
| `CONTRIBUTING.md` | Dev environment setup, PR process, code style conventions |
| `DEPLOYMENT.md` | Docker, bare-metal, reverse proxy setup, TLS termination |
| `ERRORS.md` | Error slug catalog — `SLUG → HTTP status → human meaning` |
| `GVISOR.md` | gVisor (`runsc`) runtime evaluation — incompatibility writeup |
| `KATA.md` | Kata Containers runtime evaluation — supported configurations + measured cost |
| `OPERATIONS.md` | Day-2 ops: backup/restore, VACUUM, log rotation, upgrades |
| `RUNTIMES.md` | Per-runtime handler contract, streaming (generators/async iterables), TypeScript |
| `SECURITY.md` | Threat model, nsjail sandbox isolation, per-sandbox egress policy (nsjail NSTUN), credentials at the request boundary |
| `SUPPORT.md` | Support matrix — distros, kernels, container runtimes |
| `TESTING.md` | End-to-end verification guide — bring-up, the four testing layers, real user journeys with expected output, negative testing, writing a new test, triage, CI reproduction |
| `TRACING.md` | Causal trace model, propagation, W3C interop, outlier detection |
| `reference.md` | **Canonical** Orva reference (~68 KB GFM markdown) — single source of truth shipped to the dashboard's Copy-as-Markdown button (via `frontend/public/docs.md`), the `get_orva_docs` MCP tool (via `backend/internal/mcp/reference.md`), and the slim CLI's `orva docs` command (via `cli/commands/reference.md`). `make docs-embed` syncs all three copies. Uses `{{ORIGIN}}` placeholders that consumers substitute at runtime. |

## Update Triggers

- New REST endpoint → update `API.md`
- New CLI subcommand or flag → update `CLI.md`
- New config key → update `CONFIG.md`
- New error slug → update `ERRORS.md`
- Runtime behavior change → update `RUNTIMES.md`
- Backup/restore or vacuum changes → update `OPERATIONS.md`
- Security boundary change → update `SECURITY.md`
- New container-runtime evaluation → add `<NAME>.md` (mirror `KATA.md` / `GVISOR.md`) + update `SUPPORT.md` and the README runtime-support table
- Handler contract / event shape / SDK method change → update `reference.md`,
  `RUNTIMES.md`, **`frontend/src/views/Docs.vue`** (hand-maintained rendered
  page) **and `frontend/src/utils/aiPrompts.js`** (the prompt the in-product
  assistant generates code from — a stale claim there ships as broken code, not
  as a stale sentence). All four carry the same contract and drift apart
  silently.
- Anything an operator must do differently after upgrading → `CHANGELOG.md`,
  under **Breaking** or **Upgrade notes**

## Two things that make docs here drift silently

1. **`reference.md` has three embedded copies.** Edit the canonical file, then
   run `make docs-embed`. Never edit a copy — the next embed overwrites it.
2. **Four files carry the handler contract** (`reference.md`, `RUNTIMES.md`,
   `Docs.vue`, `aiPrompts.js`). Before the 2026-08-25 audit they disagreed with
   each other and three of the four were wrong; `RUNTIMES.md` was the one that
   was right. If you change one, grep the other three.
