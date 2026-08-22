# test/

Shell-based integration tests. They run against a **live Orva instance** — they
do not start their own server. The backend must already be running. (The
comprehensive, self-spinning suite is the Python one under `test/e2e/` — see
below; these shell scripts are the ad-hoc checks against a running instance.)

> **Verifying Orva end to end?** Read [`docs/TESTING.md`](../docs/TESTING.md) first —
> it covers which layer to reach for, what each suite's pass/fail signal and exit-code
> convention actually is, and which of these scripts are stale or destructive.

## Config

Most scripts read `BASE_URL` + `API_KEY` from the environment (the default port
is **18443**, not 8443):

```bash
export BASE_URL=http://localhost:18443
export API_KEY=orva_...
```

Most scripts **hard-require** `API_KEY` and do NOT fall back to
`~/.orva/config.yaml`. Two exceptions — `sdk-test.sh` and `tracing-test.sh` —
also accept `ORVA_ENDPOINT`/`ORVA_API_KEY` and fall back to `~/.orva/config.yaml`.
`loadtest.sh` and `ceiling.sh` are standalone (own args / hardcoded host).

## Running

```bash
# Umbrella suite — requires API_KEY set; writes run-all-results.tsv. Runs 11:
# secrets, routes, heavy-deploy, onboarding, errors, rollback, egress, auth,
# tracing, build-cache, atscale. (api-smoke / loadtest / ceiling / sdk-test are
# run individually.) NOT a CI gate — ci.yml shellchecks test/*.sh and runs only
# sdk-test.sh against its provisioned sandbox instance. Several mutate the instance they run against (atscale.sh issues
# no DELETEs at all), so point BASE_URL at a scratch instance, not one you care
# about. See docs/TESTING.md.
./test/run-all.sh

# Individual suites
./test/api-smoke.sh         # fast smoke of public REST endpoints
./test/auth-test.sh         # per-function auth_mode + rate limiting
./test/rollback-test.sh     # deploy → rollback → redeploy
./test/routes-test.sh       # custom HTTP route mapping
./test/secrets-test.sh      # secret injection into sandbox
./test/egress-test.sh       # per-function network_mode toggle (none vs egress)
./test/errors-test.sh       # error response shapes
./test/tracing-test.sh      # causal-trace propagation
./test/sdk-test.sh          # runtime SDK surface (kv.incr/cas, jobs, …)
./test/onboarding-flow.sh   # browser onboarding/auth flow via curl (no deploy/invoke)
./test/heavy-deploy-test.sh # large deploy + streaming response
./test/loadtest.sh          # multi-phase RPS benchmark (hey)
./test/atscale.sh           # multi-function deploy + isolation verification
./test/ceiling.sh <api-key> <fn-id> [base-url]  # throughput ceiling ramp
```

## Test Files

| File | What it covers |
|---|---|
| `api-smoke.sh` | Fast smoke of public REST endpoints: system health/metrics, auth/status, functions CRUD, deploy-inline, invoke, deployments, keys, routes — status-family checks, not deep coverage |
| `auth-test.sh` | Per-function `auth_mode` (none / platform_key / signed HMAC), `rate_limit_per_min` (429 + Retry-After), invalid auth_mode → 400 VALIDATION |
| `rollback-test.sh` | Version history, rollback endpoint, redeploy after rollback |
| `routes-test.sh` | Custom route registration, path-matching, method filtering |
| `secrets-test.sh` | Secret set/get, injection as env vars inside the sandbox |
| `egress-test.sh` | Per-function `network_mode` toggle (none = blocked, egress = allowed) end-to-end; runs unconditionally (no auto-skip) |
| `errors-test.sh` | 4xx/5xx shapes, SLUG codes, user-visible error messages |
| `tracing-test.sh` | Causal-trace propagation across HTTP / cron / jobs / F2F |
| `sdk-test.sh` | Runtime SDK surface — kv.incr/cas, kv.list cursor, jobs idempotency, etc. (Node + Python handlers); uses `ORVA_ENDPOINT`/`ORVA_API_KEY` or `~/.orva/config.yaml` |
| `loadtest.sh` | Multi-phase load test with `hey` (`-n`/`-c`): hello, mixed Node/Python, CPU, slow (500ms), error phases |
| `atscale.sh` | Multi-function deploy + isolation: deploy 20 mixed fns, idle-RAM baseline, hammer 5 with `hey` asserting cross-fn isolation + autoscaler scale counts; TSV to stdout |
| `ceiling.sh` | Sustained-load ramp (120s/step after a 60s warmup) to find the real throughput ceiling; emits CSV (rps/p50/p95/p99/err/mem). Positional args: `<api-key> <fn-id> [base-url]` |
| `onboarding-flow.sh` | Auth/session onboarding flow via curl: onboard → session cookie → /auth/me. Onboarding is unauthenticated only while the instance is unused; against one that has operator-minted keys or deployed functions, set `API_KEY` to an admin key → refresh (token rotation) → logout → SQLite persistence. No deploy/invoke/KV |
| `heavy-deploy-test.sh` | Large deploy + streaming chunked response validation |
| `migration-rehearsal.sh` | Upgrade rehearsal for the UUIDv7 id migration. **Self-contained** — boots the real binary against a scratch data dir it seeds itself, so unlike the rest of this directory it needs no running instance, no `API_KEY`, no nsjail and no Docker. Asserts ids move, `functions/<id>/` follows, `current` still resolves, and the source survives a GC tick (the window in which a broken migration deletes it). `test/migration-rehearsal/` holds its Go fixture builder. |

## Subdirectories

- `e2e/` — comprehensive programmatic **Python (stdlib-only)** E2E suite; spins its own fresh isolated Docker container via `cd test/e2e && python3 run.py`. Covers the full server API + CLI + AI assistant and is the source-of-truth "does everything still work as spec'd" suite. See `test/e2e/CLAUDE.md`.
- `browser/` — real-browser UI suite (Playwright over system Chrome): overflow and clipping, touch targets, accessibility, and multi-step journeys. Needs a running instance; see `test/browser/CLAUDE.md`.
- `container/` — end-to-end against a **real container on a throwaway Docker network**, with the privileges nsjail actually needs. Builds the image, brings the instance up, runs the API/sandbox and browser suites, and removes everything on exit. Unlike the rest of this directory it needs no running instance. See `test/container/CLAUDE.md`.
- `cli/` — CLI-only harnesses: build matrix, install-cli, upgrade round-trip, command-tree golden diff.
- `install/` — server-install e2e (privileged systemd-in-docker across distros + Kata flow).
- `kata-bench/` — benchmarks runc vs kata vs kata-clh (cold-start + ceiling ramp); includes `aggregate.py` + `extended-functional.sh`.
- `fixtures/` — reusable function handler sources (`node-*/handler.js`, `python-*/handler.py`) deployed by the suites.

## Notes

- Tests are additive and idempotent where possible — they create resources with unique names and clean up after themselves.
- `egress-test.sh` asserts the per-function `network_mode` toggle. Isolation is per-sandbox: nsjail's NSTUN userspace stack applies the compiled egress policy inside each worker. No host firewall is involved, and `--use_pasta` is long gone — an `egress` function needs only nsjail and `/dev/net/tun`.
- `heavy-deploy-test.sh` logs are saved to `heavy-deploy-stream.log` for inspection after the run.
