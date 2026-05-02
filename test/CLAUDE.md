# test/

Shell-based integration test suite. Tests run against a **live Orva instance** — they do not start their own server. The backend must be running before executing any test.

## Running

```bash
# Run everything (writes summary to run-all-results.tsv)
./test/run-all.sh

# Individual suites
./test/api-smoke.sh        # core API round-trips
./test/auth-test.sh        # API key auth + permissions
./test/rollback-test.sh    # deploy → rollback → redeploy
./test/routes-test.sh      # custom HTTP route mapping
./test/secrets-test.sh     # secret injection into sandbox
./test/egress-test.sh      # nftables outbound allow/deny
./test/errors-test.sh      # error response shapes
./test/loadtest.sh         # sustained concurrency
./test/atscale.sh          # ramp-load scaling
./test/onboarding-flow.sh  # full user onboarding scenario
./test/heavy-deploy-test.sh # large deploy + streaming response
./test/ceiling.sh          # sandbox concurrency ceiling
```

## Config

Tests read from environment variables, falling back to the CLI config:

```bash
export ORVA_ENDPOINT=http://localhost:8443
export ORVA_API_KEY=orva_...
```

If neither is set, tests fall back to `~/.orva/config.yaml`.

## Test Files

| File | What it covers |
|---|---|
| `api-smoke.sh` | Functions CRUD, deploy, invoke, KV, cron, jobs, webhooks, fixtures, replay |
| `auth-test.sh` | API key creation/deletion, permission scopes, rate limiting |
| `rollback-test.sh` | Version history, rollback endpoint, redeploy after rollback |
| `routes-test.sh` | Custom route registration, path-matching, method filtering |
| `secrets-test.sh` | Secret set/get, injection as env vars inside sandbox |
| `egress-test.sh` | Firewall allow-list enforcement (allowed vs blocked domains) |
| `errors-test.sh` | 4xx/5xx shapes, SLUG codes, user-visible error messages |
| `loadtest.sh` | Concurrent invocations over 30s with `wrk` or `curl` |
| `atscale.sh` | Graduated load ramp; results in `atscale-results.tsv` |
| `onboarding-flow.sh` | Login → deploy → invoke → KV → clean up end-to-end |
| `heavy-deploy-test.sh` | 5 MB+ deploy + streaming chunked response validation |
| `ceiling.sh` | Confirms max-concurrent sandbox limit is enforced |
| `fixtures/` | Saved JSON payloads used as test inputs by some suites |

## Notes

- Tests are additive and idempotent where possible — they create resources with unique names and clean up after themselves.
- `egress-test.sh` requires nftables to be active on the host; it is skipped automatically if nft is absent.
- `heavy-deploy-test.sh` logs are saved to `heavy-deploy-stream.log` for inspection after the run.
