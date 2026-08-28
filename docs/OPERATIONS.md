# Operations runbook

What to do when something goes wrong. Each section: symptom →
diagnosis → fix.

> **On `sqlite3`.** The runtime image does not ship it, so
> `docker exec orva sqlite3 …` fails with `sqlite3: not found`. Run the SQL
> below from the host against the database inside the volume — for a Compose
> install that is
> `docker volume inspect orva-data` to find the mountpoint — or install
> `sqlite3` in the container yourself for a one-off. Stop orvad first for
> anything that writes: it holds the database open in WAL mode.

## Quick health check

```bash
# Is orvad responding?
curl -fsS http://localhost:8443/api/v1/system/health
# {"status":"healthy", "version": …, "database": …, "sandbox": …}
# 503 + {"status":"degraded"} if the database ping fails

# What does it think the world looks like?
KEY=$(docker exec orva cat /var/lib/orva/.admin-key)
curl -s http://localhost:8443/api/v1/system/metrics.json -H "X-Orva-API-Key: $KEY" | jq
```

Five fields to look at first:

| field | sane range |
|---|---|
| `host.num_goroutines` | 50–500 idle, scales with active invokes |
| `host.mem_reserved_mb` / `host.mem_total_mb` | should stay under 80% |
| `host.effective_memory_capacity_mb` | current memory available for one or more additional worker admissions |
| `host.effective_cpu_workers` | worker ceiling derived from the active cgroup CPU quota |
| `sandbox.active` | <= `cfg.Sandbox.MaxConcurrent` (derived: `NumCPU × 64`, floor 200) |
| `latency_ms.p99` | <= 5 × `latency_ms.p50`. If p99 is 10× p50 the pool is saturated |
| `pools[].idle + busy + spawning` | <= `pools[].effective_max`; queued should drain after a burst |

## Common errors and what they mean

Full catalog in [ERRORS.md](ERRORS.md). The ones operators see most:

| code | what's happening | what to do |
|---|---|---|
| `429 TOO_MANY_REQUESTS` | host-wide concurrency cap hit | client should back off + retry. The ceiling is derived from CPU count (`NumCPU × 64`, floor 200) and is not operator-tunable today — add CPUs to raise it |
| `503 POOL_AT_CAPACITY` | this function reached its effective host/operator ceiling and the queue deadline expired | inspect `limiting_reason`; raise `max_warm` only when it says `operator_max`, otherwise add host capacity or reduce worker limits |
| `503 MEMORY_EXHAUSTED` | host RAM at 80% reservation | scale-down idle pools, increase host RAM, or reduce per-fn `memory_mb` |
| `502 WORKER_CRASHED` | function process exited mid-request (panic, OOM kill, syntax error) | check the execution's stderr in the dashboard or `execution_logs` table |
| `504 TIMEOUT` | exceeded fn `timeout_ms` | raise it (`PUT /api/v1/functions/{id}` with `{"timeout_ms": 60000}`) or optimize the handler |
| `503 BUILD_QUEUE_FULL` | too many parallel deploys | wait + retry; queue holds 64 jobs |
| `500 BUILD_ERROR` | the deploy's build broke; the function is left in `error` status | check `/api/v1/deployments/<id>/logs` for the actual npm/pip error |
| `503 INSUFFICIENT_DISK` | data dir < `min_free_disk_mb` (default 500 MB) | free space, lower `versions_to_keep`, or increase `min_free_disk_mb` |
| `410 VERSION_GCD` | rollback target was pruned | redeploy the original code or rollback to a more recent hash |

## Symptom: dashboard becomes slow after a load test

**Diagnosis.** Check:

```bash
docker stats orva --no-stream
pgrep -c nsjail   # from the HOST: the container runs with pid: host,
                  # so `docker exec orva ps -ef` lists every process on the
                  # box, not just Orva's
```

If total workers (`idle + busy + spawning`) exceeds `effective_max`, capture
the metrics snapshot and logs: Pool Controller v2 publishes spawning before
launch specifically to prevent overlapping evaluations from over-spawning.

**Recovery.** The controller waits 30 seconds below desired capacity, then removes no
more than 20% of workers per evaluation. It should converge without dropping
busy work. If it doesn't:

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
entrypoint, or a package with no wheel for the runtime's Python (3.14).

**Fix.** Redeploy with corrected code; or rollback to the last known
good version via the Deployments view.

## Symptom: EVERY function returns `WORKER_CRASHED` right after a bare-metal install

**Diagnosis.** If *nothing* invokes (even a trivial handler) and the
`stderr` is empty, nsjail cannot start or construct its sandbox in the
service environment. Check these known causes:

- **`/proc` overmount.** `journalctl -u orva` shows nsjail
  `Failed to mount mandatory point: '/proc'`. Caused by the systemd
  unit's `ProtectKernelTunables=true` overmounting `/proc/sys`, which
  blocks nsjail's procfs mount inside its user namespace. The shipped
  unit (`scripts/install.sh`) no longer sets this; if you have an older
  unit, remove the `ProtectKernelTunables=true` line and
  `systemctl daemon-reload && systemctl restart orva`.
- **cgroup controllers not delegated.** When systemd doesn't delegate
  the cgroup v2 controllers to the service (constrained/cloud VMs),
  Orva now logs `cgroup v2 controllers not delegated; per-sandbox
  memory/pid/cpu caps disabled (rlimit-only fallback)` at startup and
  runs functions **without** hard per-sandbox memory caps rather than
  crashing. Older builds crashed every worker here — upgrade to fix.
- **nsjail capabilities excluded by systemd.** If the API returns
  `SANDBOX_ERROR` immediately and nsjail produces no stderr, inspect
  `systemctl cat orva`. The bounding set must retain every capability the
  installed nsjail binary carries as a file capability — `CAP_SYS_ADMIN`,
  `CAP_NET_ADMIN`, `CAP_SETUID`, `CAP_SETGID`, `CAP_NET_BIND_SERVICE` —
  otherwise Linux rejects the setcap nsjail binary at `execve` before it can
  log. These are **nsjail's** requirements, not orvad's: `CAP_NET_ADMIN` is
  what lets nsjail configure the TUN interface for `--user_net`
  (`network_mode: egress`). orvad itself administers no host networking, so
  dropping the capability from the bounding set does not "turn off the
  firewall" — it breaks sandbox spawn outright. Re-running the current
  bare-metal installer refreshes the unit safely on upgrades.
- **`/dev/net/tun` missing.** If only `network_mode: egress` functions fail
  (and `none` functions run fine), nsjail cannot open the TUN device.
  `modprobe tun` on bare metal; pass `--device /dev/net/tun` in Docker.

Quick confirmation that nsjail itself works on the host:
`sudo -u orva nsjail -Mo --chroot /var/lib/orva/rootfs/node -T /tmp -- /usr/local/bin/node --version`
should print the Node version.

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
Builds are bounded by `build_timeout_seconds` (default 300). That knob now
covers `npm install` and `pip install` as well as `tsc` — previously only
`tsc` was capped, so a hung registry connection could hold a build slot
indefinitely. If a build is genuinely wedged, restart orvad to kill the
child:

```bash
docker restart orva
```

The deployment row does **not** need fixing by hand any more: on boot Orva
fails any deployment abandoned in `queued` or `building`, with
`error_message = "abandoned: the server restarted while this build was in
progress"`. Nothing reconciled those rows before, so they sat as a spinner
that never resolved.

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
sqlite3 /var/lib/orva/orva.db \
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
sqlite3 /var/lib/orva/orva.db \
  "UPDATE system_config SET value='3' WHERE key='versions_to_keep'"

# The GC runs on a ticker (gc_interval_seconds, default 300) and does NOT run
# at startup, so restarting does not force a pass — it delays the next one by
# a full interval. Lower the interval and wait, or just wait.
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

Still there if the volume is intact (mode 0600) — the keyfile is the only
persisted plaintext copy, and Orva re-inserts its database row from it on boot
if the row ever goes missing.

**If the keyfile is genuinely gone**, the plaintext is unrecoverable: the
database stores only its SHA-256. Sign in to the dashboard with your operator
account and mint a fresh key from Settings → API keys (or
`POST /api/v1/keys`). Deleting the keyfile alone does **not** make Orva
generate a new one — it regenerates only when the database has no keys at all.

> **Do not delete the `users` and `sessions` rows to force re-onboarding.** An
> older version of this page suggested it; it destroys your accounts and does
> not recover anything. `POST /api/v1/auth/onboard` refuses with `401` once the
> instance shows any sign of use (operator-minted keys or deployed functions),
> so you end up with no user, no session, and no way back in short of restoring
> a backup.

## Upgrading from an nftables-based build

Nothing to clean up. Egress filtering is now compiled per sandbox and loaded
by nsjail, so no build creates a host firewall table any more — there is no
migration step, no service reconfiguration, and no leftover state on a normal
upgrade.

The one exception applies to hosts that ran the **last nftables-based build**.
Its release and tag have since been pruned, so that version is no longer
installable, and on
bare-metal systemd it created `table inet orva_firewall` while running. A clean
`systemctl stop` (which the installer performs on upgrade) removes it. If that
daemon was instead SIGKILLed, OOM-killed, or lost to a hard reboot, the table
survives — the new build no longer manages nftables, so nothing will clear it
for you. Check and delete it by hand once:

```bash
sudo nft delete table inet orva_firewall
```

Orva never creates that table again, so a "table not found" error here just
means there was nothing to remove.

## Upgrading across the UUIDv7 id migration

The first boot of a build containing this migration rewrites every storage
id — including `functions.id` — to a UUIDv7. A function's code lives at
`<dataDir>/functions/<id>/`, and every path in the server is built from the
database id, so the directories have to move with the ids.

They do, and the two halves cannot drift: the old→new map is committed
inside the same transaction as the rewrite, and `ReconcileFunctionDirs`
renames from that record on boot. It is idempotent and resumable, so a crash
between the commit and the rename is repaired on the next start rather than
leaving code stranded under a name nothing will look up. The build GC also
refuses to sweep orphaned function directories while a rename is
outstanding.

**Back up before upgrading.** The migration is one-way: there is no down
migration, and the ids are freshly generated rather than derived, so they
cannot be recomputed.

**Rehearse it if you want certainty.** `bash test/migration-rehearsal.sh`
boots the real binary against a data dir it seeds with legacy ids, a channel
binding, soft references and function code on disk, then asserts that every
id moved, every directory followed, `current` still resolves, and the source
survived a GC tick — which is the window in which a broken migration deletes
it. It needs neither nsjail nor Docker, so it runs anywhere the binary
builds.

**If boot fails with `failed to reconcile function directories after id
migration`:** your function code is intact on disk. The message names the
cause — almost always a permissions or disk-space problem under `functions/`.
Fix that and restart; the rename resumes where it stopped. Do **not** delete
anything under `functions/` to "clean up".

**If boot fails with `integrity check failed`** on an older build, the
migration refused to commit and rolled back, so the database is untouched and
unmigrated. Upgrade to a build containing this fix and start again.

## Symptom: nsjail cgroups accumulating under the delegate

**Diagnosis.** Workers are SIGKILLed, so nsjail never runs its own cleanup
and the `NSJAIL.<pid>` cgroup directory it created is left behind. Beyond
disk, every spawn scans that directory and reads `cgroup.procs` per entry to
find its own, so an accumulation slows cold starts and eventually makes
cgroup resolution time out — at which point memory sampling stops producing
samples and the autoscaler permanently over-reserves.

**Fix.** Orva reclaims them in three places, and you should not need to
intervene:

- when a worker is reaped, if its cgroup path was resolved;
- on every GC tick, for the ones it was not — nsjail names the directory
  after the jailed *child's* pid, so finding it requires a scan that gives up
  after 500ms, and under churn a worker is often gone before that completes;
- at startup, for anything a previous process left behind.

The sweep only removes directories that accept an `rmdir`, which a cgroup
still holding a task refuses with `EBUSY` — so a live sandbox's cgroup is
never at risk.

**Verify.** The steady-state count should equal live workers:

```bash
# the delegate Orva resolved (see the --cgroupv2_mount arg on any nsjail proc)
ls /sys/fs/cgroup/<delegate> | grep -c '^NSJAIL\.'
pgrep -P "$(pgrep -f '/orva serve')" -f nsjail | wc -l
```

Between GC ticks the first number runs ahead; after a tick they converge. If
they stay far apart, check that orvad can actually write to the delegate —
on bare metal that is what `Delegate=yes` in the unit provides, and without
it every removal fails with `EACCES` and is skipped.

## Backup and restore

`orva backup download` writes an archive containing the SQLite database,
every deployed function version, the secrets master key, and the bootstrap
admin API key. It is written mode `0600` — treat it as the credential it is.

`orva backup restore` replaces the live data directory and then **exits with
status 70** so the supervisor restarts the process against the restored
files. That is deliberate: it used to exit 0, and systemd's
`Restart=on-failure` reads 0 as "finished successfully" and leaves the
service down — a successful restore was a self-inflicted outage on bare
metal. Docker's `restart: unless-stopped` masks it, which is why it went
unnoticed.

The unit the installer ships now carries:

```ini
Restart=on-failure
RestartForceExitStatus=70
```

**If your unit predates this**, add that line — or re-run `install.sh`, which
rewrites the unit — or your server will stay down after a restore. Check
with:

```bash
systemctl cat orva | grep RestartForceExitStatus
```

## Logs

```bash
# orvad stdout
docker logs orva --tail 200 -f

# function stderr
sqlite3 /var/lib/orva/orva.db \
  "SELECT execution_id, stderr FROM execution_logs ORDER BY rowid DESC LIMIT 5"

# build logs
sqlite3 /var/lib/orva/orva.db \
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
