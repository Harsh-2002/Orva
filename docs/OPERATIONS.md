# Operations runbook

What to do when something goes wrong. Each section: symptom →
diagnosis → fix.

## Quick health check

```bash
# Is orvad responding?
curl -fsS http://localhost:8443/api/v1/system/health
# {"status":"ok"}

# What does it think the world looks like?
KEY=$(docker exec orva cat /var/lib/orva/.admin-key)
curl -s http://localhost:8443/api/v1/system/metrics.json -H "X-Orva-API-Key: $KEY" | jq
```

Five fields to look at first:

| field | sane range |
|---|---|
| `host.num_goroutines` | 50–500 idle, scales with active invokes |
| `host.mem_reserved_mb` / `host.mem_total_mb` | should stay under 80% |
| `sandbox.active` | <= `cfg.Sandbox.MaxConcurrent` (default 500) |
| `latency_ms.p99` | <= 5 × `latency_ms.p50`. If p99 is 10× p50 the pool is saturated |
| `pools[].idle` for each pool | <= `pools[].dynamic_max`. If exceeded, the pool stability fix didn't deploy |

## Common errors and what they mean

Full catalog in [ERRORS.md](ERRORS.md). The ones operators see most:

| code | what's happening | what to do |
|---|---|---|
| `429 TOO_MANY_REQUESTS` | host-wide concurrency cap hit | client should back off + retry; or raise `sandbox.max_concurrent` |
| `503 POOL_AT_CAPACITY` | this function's pool at `dynamic_max` and ctx fired waiting | raise `pool_config.max_warm` for that fn, or accept the backpressure |
| `503 MEMORY_EXHAUSTED` | host RAM at 80% reservation | scale-down idle pools, increase host RAM, or reduce per-fn `memory_mb` |
| `502 WORKER_CRASHED` | function process exited mid-request (panic, OOM kill, syntax error) | check the execution's stderr in the dashboard or `execution_logs` table |
| `504 TIMEOUT` | exceeded fn `timeout_ms` | raise it (`PUT /api/v1/functions/{id}` with `{"timeout_ms": 60000}`) or optimize the handler |
| `503 BUILD_QUEUE_FULL` | too many parallel deploys | wait + retry; queue holds 64 jobs |
| `502 BUILD_FAILED` | last build broke and there's no prior version to fall back to | check `/api/v1/deployments/<id>/logs` for the actual npm/pip error |
| `503 INSUFFICIENT_DISK` | data dir < `min_free_disk_mb` (default 500 MB) | free space, lower `versions_to_keep`, or increase `min_free_disk_mb` |
| `410 VERSION_GCD` | rollback target was pruned | redeploy the original code or rollback to a more recent hash |

## Symptom: dashboard becomes slow after a load test

**Diagnosis.** Check:

```bash
docker stats orva --no-stream
docker exec orva ps -ef | wc -l   # nsjail process count
```

If PIDs are >300 and idle is >dynamic_max, the pool over-spawned past
its cap. This was a real bug fixed in early Round-G builds — if you're
on `v2026.04.28` or later it shouldn't recur.

**Recovery.** The autoscaler will catch up within ~30 s of load
ending. If it doesn't:

```bash
docker restart orva
```

The pool resets cleanly. The function code, deployments, secrets, and
sessions all survive (they're in the DB and on disk).

## Symptom: function exits immediately with no logs

**Diagnosis.** The handler probably has a top-level syntax error or
the adapter couldn't load it.

```bash
KEY=$(docker exec orva cat /var/lib/orva/.admin-key)
EXEC_ID=$(curl -s -H "X-Orva-API-Key: $KEY" "http://localhost:8443/api/v1/executions?function_id=019df200-7b00-7e00-9c00-aab1cd2e3f40&limit=1" | jq -r '.executions[0].id')
curl -s -H "X-Orva-API-Key: $KEY" "http://localhost:8443/api/v1/executions/$EXEC_ID/logs"
```

Look at the `stderr`. If empty, the worker died before writing —
common causes: missing dependency (typo in `requirements.txt`), wrong
entrypoint, runtime version mismatch (`requirements.txt` references a
package that doesn't have a 3.13 wheel).

**Fix.** Redeploy with corrected code; or rollback to the last known
good version via the Deployments view.

## Symptom: deploys stuck in `building` forever

**Diagnosis.**

```bash
KEY=$(docker exec orva cat /var/lib/orva/.admin-key)
curl -s -H "X-Orva-API-Key: $KEY" "http://localhost:8443/api/v1/system/metrics.json" | jq '.build_queue'
# {"pending": <N>, "workers": 2}
```

If `pending > 0` for more than a few minutes, the build worker is
stuck on `npm install` or `pip install`. Most common: a dep with a
network call to a slow / blocked registry, or a missing wheel forcing
a source build.

**Fix.** `docker logs orva | tail -100` shows the npm/pip stderr.
Stuck builds time out at the Go context level (no explicit cap today;
planned). Restart orvad to kill the stuck child:

```bash
docker restart orva
```

The deployment row stays in `building` state forever — manually mark
it failed:

```sql
docker exec orva sqlite3 /var/lib/orva/orva.db \
  "UPDATE deployments SET status='failed', error_message='killed-by-operator' WHERE id='019df210-1234-7000-8000-deadbeef0001'"
```

## Symptom: rollback fails with `VERSION_GCD`

**Diagnosis.** The version archive at `versions/<hash>/` has been
pruned by the GC. The DB row is preserved (deployment audit trail) but
the on-disk artifact is gone.

```bash
docker exec orva ls /var/lib/orva/functions/<fn-id>/versions/
```

If the `<hash>` you're trying to roll back to isn't there, it's gone.

**Fix.** Either:

- Roll back to a different hash that's still archived (the API
  response includes `details.available_hashes`).
- Redeploy the original code from your source-of-truth (git, etc.).

**Prevent.** Raise `system_config.versions_to_keep`:

```sql
docker exec orva sqlite3 /var/lib/orva/orva.db \
  "UPDATE system_config SET value='10' WHERE key='versions_to_keep'"
```

The active hash is **always** kept regardless.

## Symptom: lots of `INSUFFICIENT_DISK` errors

**Diagnosis.**

```bash
docker exec orva df -h /var/lib/orva
docker exec orva du -sh /var/lib/orva/functions/*
```

The version archive is the usual culprit — Python deps are heavy.

**Fix.**

```bash
# Lower retention
docker exec orva sqlite3 /var/lib/orva/orva.db \
  "UPDATE system_config SET value='3' WHERE key='versions_to_keep'"

# Force a GC pass (bounce orvad — GC runs at startup)
docker restart orva
```

Or move the data dir to a bigger volume.

## Symptom: clipboard buttons in the UI silently fail

**Diagnosis.** You're accessing the dashboard over plain HTTP from a
LAN IP (e.g. `http://192.168.1.10:8443`). Browser Clipboard API
silently rejects writes outside HTTPS or `localhost`.

**Fix.** Put a TLS terminator in front (see [DEPLOYMENT.md](DEPLOYMENT.md))
or access via `localhost`/`127.0.0.1` from the host itself.

## Symptom: containers won't start (auth flicker, redirected to /onboarding)

**Diagnosis.** Browser localStorage shows `orva.hasUser=false` from a
prior failed `/api/v1/auth/status` call.

**Fix.** Hard refresh (Ctrl+Shift+R). If that doesn't clear it, open
devtools → Application → localStorage → delete `orva.hasUser`.

## Symptom: I lost the bootstrap admin key

```bash
docker exec orva cat /var/lib/orva/.admin-key
```

Still there if the volume is intact (mode 0600). If genuinely lost
(file deleted, no backup):

```sql
-- inspect users
docker exec orva sqlite3 /var/lib/orva/orva.db "SELECT id, username FROM users"

-- reset admin user's password (you need to know SHA-256 hashing)
-- easier path: nuke users + sessions, re-onboard:
docker exec orva sqlite3 /var/lib/orva/orva.db "DELETE FROM users; DELETE FROM sessions"
# refresh dashboard → routes to /onboarding
```

## Logs

```bash
# orvad stdout
docker logs orva --tail 200 -f

# function stderr
docker exec orva sqlite3 /var/lib/orva/orva.db \
  "SELECT execution_id, stderr FROM execution_logs ORDER BY rowid DESC LIMIT 5"

# build logs
docker exec orva sqlite3 /var/lib/orva/orva.db \
  "SELECT seq, stream, line FROM build_logs WHERE deployment_id='019df210-1234-7000-8000-deadbeef0001' ORDER BY seq"
```

## When all else fails

```bash
# Stop, snapshot, restart
systemctl stop orva
tar -czf /tmp/orva-debug-$(date +%s).tar.gz /var/lib/orva
systemctl start orva
```

The tarball is small, includes the SQLite DB (which has full audit
trail of executions + deployments), and is reproducible — open an
issue with it attached.
