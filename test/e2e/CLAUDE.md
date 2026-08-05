# test/e2e/

Comprehensive, **programmatic** end-to-end test suite for Orva — server API **and**
CLI **and** the AI assistant — exercised against a **freshly-built, isolated Docker
container**, never the dev instance. Python, stdlib-only (no pip). This is the
self-owned full-E2E harness: it can drive every flow itself (including the AI
agentic loop, with no real provider key), so validation never depends on CI or a
human clicking around.

> Relationship to `test/` (the shell suite): that one runs ad-hoc against a live
> instance. **This** suite is the comprehensive, isolated, self-spinning one and is
> the source of truth for "does everything still work as spec'd".

## How to run

```bash
cd test/e2e

# Default: build a fresh local image, run an ISOLATED container, run every
# module, write CHECKLIST.md, tear down. Nothing touches the dev instance.
python3 run.py

python3 run.py --rebuild          # force-rebuild the image first (after code changes)
python3 run.py --keep             # leave the container up afterward (debugging)
python3 run.py --filter ai        # only modules whose filename contains "ai"

# Against an already-running instance (no Docker) — e.g. the host dev service:
python3 run.py --url http://127.0.0.1:8443 --api-key "$(sudo cat /var/lib/orva/.admin-key)"
```

**Requirements:** `docker` (for isolated mode), `python3`, and a built `build/orva`
(for the CLI tests — `make build` from repo root). The image build needs Go 1.26
(the Dockerfile pins it; the AI gateway requires it).

## Architecture

| File | Role |
|---|---|
| `harness.py` | `OrvaClient` (full `/api/v1` REST + SSE), `CLIRunner` (runs `orva … --endpoint --api-key`), `check/section/summary` report helpers, mock-LLM helpers. Tests import from here. |
| `mock_llm.py` | A deterministic OpenAI-compatible streaming server. Lets the AI agentic loop be tested with **no real key**. |
| `env.py` | Builds `orva:e2e` from source and runs an **isolated** container (nsjail caps, `host.docker.internal` for the mock, fresh volume), with health-wait + admin-key + teardown. |
| `run.py` | Orchestrator: spin env → run each `tests/test_*.py` as an isolated subprocess → aggregate → write `CHECKLIST.md` → tear down. |
| `tests/test_*.py` | One module per domain. Auto-discovered by `run.py`. |
| `CHECKLIST.md` | Living result record — regenerated every run. |

### How the isolated environment works (`env.py`)
`docker build -t orva:e2e .` → `docker run` with `--cap-add SYS_ADMIN/NET_ADMIN`,
`--cgroupns=host --pid=host`, `seccomp/apparmor=unconfined`, a `/sys/fs/cgroup`
mount, and `--add-host host.docker.internal:host-gateway` (so a test's host-side
mock LLM is reachable from inside the container). A fresh named volume gives each
run pristine state. The container is removed (with its volume) on teardown unless
`--keep`.

### Keyless AI testing (`mock_llm.py`)
Bifrost can point a provider at any base URL, so a test configures an Orva
`openai` provider at `http://host.docker.internal:<port>/v1` and the mock answers.
The chat **content** is the script:
- `CALL <tool> <json-args>` → the model emits one tool_call for `<tool>`
- `CALL2 <toolA> <argsA> || <toolB> <argsB>` → two tool_calls in one turn
- plain text → a text reply
- once a tool **result** is in the history → the model emits a final answer (loop ends)

This drives the *real* agentic loop (approval gate + in-process dispatch against
the real instance) deterministically.

## Adding a test module

1. Copy an existing `tests/test_*.py`.
2. `from harness import OrvaClient, section, check, summary, …` (+ mock helpers / `CLIRunner` as needed).
3. Structure: `def main(): c = OrvaClient(); if not c.key: return 2; try: <sections + check()s> finally: <cleanup>; return summary()` and `if __name__=="__main__": sys.exit(main())`.
4. Use **unique** resource names and **always clean up** what you create.
5. `run.py` auto-discovers it. Done.

Each module is its own process; `summary()` prints a `RESULT pass=<n> fail=<m>`
trailer that `run.py` parses into `CHECKLIST.md`. Use `skip(reason)` (exit 3) when
a capability is missing (e.g. nsjail unavailable → deploy/invoke can't run).

## Validating results

- **Exit code:** `run.py` returns non-zero if any module FAILed.
- **`CHECKLIST.md`:** per-module ✅/❌/⚠️ + check counts + the target it ran against.
- A module is **PASS** only if every `check()` in it passed.

## The practice — keep improving (important)

This suite is meant to **grow on every change**:
- A test surfaced a real issue → **fix the code AND harden the test** so the
  regression can never come back. Real issues this suite has already caught:
  - The AI gateway forced Go 1.26 — the Docker build failed until the
    Dockerfile/CI pins were bumped in lock-step (host-only testing missed it).
  - **Security: a deleted API key kept authenticating** — the auth middleware
    cached keys and only evicted on expiry, never on delete. Fixed by sharing
    the cache and evicting on delete (`test_keys.py` now asserts a revoked key
    returns 401 immediately).
  - REST `GET /functions/{id}` resolves by UUID only, not name (the handler calls `Registry.Get` directly); `DELETE` and the other id-taking routes resolve by UUID OR name via `resolveFnID`.
  - REST `create_function` is lenient (fills defaults), unlike the strict MCP
    tool — validation tests must use genuinely invalid input.
- New feature/endpoint/CLI command → it is **not done** until it has a module/cases here.
- Never let coverage shrink. Prefer isolated-Docker runs for anything that mutates state.

## Coverage map & known constraints

- **Server API:** functions, deploy/invoke/logs, executions, secrets, routes, cron,
  jobs, kv, webhooks, inbound-webhooks, fixtures, firewall/dns, api-keys, channels,
  traces, system, backup, auth — plus the **AI** assistant (chat, providers,
  settings, conversations, approval, perms).
- **CLI:** the shared client subcommands (`orva deploy/invoke/functions/logs/kv/…`)
  via `CLIRunner`, confirming slim-CLI ↔ full-server command parity.
- **nsjail-dependent** scenarios (real function **deploy-build** + **invoke**) skip
  when an ordinary local container cannot provide nested sandboxing. Set
  `ORVA_REQUIRE_SANDBOX=1` (as the `e2e` job in `.github/workflows/ci.yml` does on its runner)
  to turn any such skip into a hard failure.
- **Function lookup:** REST `GET /functions/{id}` resolves by **UUID only**
  (capture the id from create responses); `DELETE` and most other id-taking
  routes accept a UUID **or** a name via `resolveFnID`, as does the agent/MCP layer.
- **Settings** is a shared singleton row; the mock helpers snapshot/restore it so
  modules don't contaminate each other's view of "defaults".
