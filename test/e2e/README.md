# Orva E2E suite

Comprehensive, programmatic end-to-end tests for the whole Orva platform —
server API, CLI, and the AI assistant — run against a freshly-built, isolated
Docker container. No real AI provider key needed (a mock LLM drives the agentic
loop). See **[CLAUDE.md](./CLAUDE.md)** for the full technical guide.

## Quickstart

```bash
cd test/e2e
python3 run.py                 # build fresh image → isolated container → run all → CHECKLIST.md → teardown
python3 run.py --rebuild       # rebuild the image first (after code changes)
python3 run.py --keep          # leave the container running for debugging
python3 run.py --filter ai     # run only matching modules

# Against an existing instance (skip Docker):
python3 run.py --url http://127.0.0.1:8443 --api-key "$(sudo cat /var/lib/orva/.admin-key)"

# Make real nsjail deploy/invoke mandatory (used by GitHub Actions):
ORVA_REQUIRE_SANDBOX=1 python3 run.py --url http://127.0.0.1:8443 --api-key "$(sudo cat /var/lib/orva/.admin-key)"
```

Requirements: `docker`, `python3`, and `build/orva` (`make build`) for CLI tests.

Results land in **`CHECKLIST.md`** (regenerated every run). Adding a scenario:
copy a `tests/test_*.py`, use the `harness` helpers, clean up after yourself —
`run.py` auto-discovers it. The suite is meant to grow on every change.
