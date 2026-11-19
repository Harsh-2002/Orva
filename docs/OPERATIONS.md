# Operations runbook

The opt-in managed server binds its SQLite database to the sealed Owner
instance. It refuses databases with existing unbound workloads before
migration. In-place managed restores retain the sealed stores and validate
the archive's binding before replacing the installed database. Replace
an incompatible ready restore with a verified matching archive; do not
remove the recovery marker to force serving.

## Serving startup exits 78

Read the startup log before retrying. The daemon and recovery coordinator
take the same exclusive lock in the outer data directory. Legacy serving
follows symlinked directory paths and allows group-writable directories.
Managed startup and offline relocation require a service-owned outer
directory with no symlinked components or group/world write access.

Legacy `orva serve` requires no `.orva-serving-mode` entry and
`ORVA_SERVING_MODE` unset or exactly `legacy`. A malformed mode marker causes
refusal, not legacy fallback. Managed mode stores SQLite under `managed/orva.db` and
keeps an empty mode-0500 directory
at `orva.db` so older binaries cannot open the original path. Do not remove
the tombstone, edit the marker, or point a legacy binary at `managed/`.
Enrollment is an explicit offline server command; it is not available through
HTTP. There is no downgrade or automated interrupted-enrollment repair.

## Chat provider errors in managed mode

Dashboard Chat uses the instance's compiled egress policy. If Settings can
save a provider but model discovery or chat reports `egress policy unavailable`,
check the daemon log for the initial compile or publication error. Check that
the service account can publish policy under `managed/firewall/` and that the
managed database is readable. The daemon retries compilation while running;
Chat settings and history stay available, but no provider requests run until
there is a compiled policy. On refresh failure an existing compiled policy
remains in use. The managed dashboard and MCP catalog do not expose firewall
rule editing.

## Offline legacy enrollment

1. **Back up first.** Verify an offline recovery copy of the complete legacy
   instance, including the database, keys, runtime roots and all function
   versions. Stop every daemon and other writer, including pre-lock releases.
   Use the same instance path and service user for all steps. A stopped
   modern legacy daemon leaves the lock file the command needs; if it is
   absent, first finish a normal legacy startup and clean shutdown using the
   current, lock-aware binary. Recover any rollback journal in legacy mode.
2. Prepare the same [host cgroup delegation and witness](DEPLOYMENT.md#opt-in-managed-daemon-supervised-setups-only)
   as for managed serving. Provision the witness once with
   `orva setup --managed-witness PATH` if absent. Do **not** run
   `setup --managed-layout provision` on the legacy instance. Keep
   `ORVA_SERVING_MODE` unset, or set it to `legacy` for the enrollment commands.
   If launching from an existing `orva.daemon` leaf, explicitly set
   `ORVA_MANAGED_WORKLOAD_PARENT` to its sibling `orva.workers`.
3. Run `orva managed enroll --dry-run --plan /safe/path/orva-enrollment.json`.
   Keep the plan outside the data directory. The dry run copies SQLite and
   its WAL, if present, and vacuums only in private scratch space. Review
   flags, revision digests, and job/cron counts. An enrollable result means
   the source fits the offline build profile, not that a jailed syntax
   check or installed-runtime verification already ran.
4. From the delegated service cgroup, run `orva managed enroll --apply
   --plan /safe/path/orva-enrollment.json --confirm`. Supply a separate
   `--exclude UUID` for every unsupported function you elect to leave inactive.
   Node and Python with current successful deployments can use npm or pip
   dependencies, and Node can use TypeScript. Enrollment rebuilds from the
   projected source and manifests, not legacy `node_modules`, pip-installed
   files, or compiler output. An unsupported plan row gives the specific
   reason, such as legacy invocation egress, an ambiguous pip install, or
   TypeScript output mixed with source. Installers need the configured
   registries reachable from the build jail under the compiled policy.
   Installed rootfs trees must include
   the current SDK-channel adapters and pass the sealed runtime verifier.
   The sealed copy omits inert dangling manual-page
   links in `/etc/alternatives` from slim Debian runtimes; the installed tree
   stays unchanged. Drift requires a new dry run, not an edited plan.
5. After successful publication, start `orva serve` from the same delegated
   service layout. If `ORVA_SERVING_MODE` is set, change it to `managed`.
   Current enrolled deployments are reconstructed from sealed artifacts;
   policy and standing work start without imported grants. Explicitly excluded
   functions are inactive. Reconfigure legacy exposures and review standing
   work before depending on them.

The whole original `functions/` tree remains under
`legacy-functions.enrolled-<operation>` for offline inspection. Managed
serving and rollback never use that tree; older deployments require an
explicit rebuild. If apply fails after durable intent, leave the data, marker,
tombstone, and Owner evidence intact. Both serving paths refuse incomplete
layouts. Retain the failed tree for supervised inspection and recover from the
verified pre-enrollment backup in a separate instance directory; do not retry
by deleting or editing marker files.

## Fresh managed provisioning

Stop existing daemons and use a new data directory with no legacy workload,
database, keys or restore residue. Prepare the [cgroup delegation and
witness location](DEPLOYMENT.md#opt-in-managed-daemon-supervised-setups-only).
Run these explicitly as the daemon user, with the same `ORVA_DATA_DIR` and
managed environment that the daemon will use:

1. `orva setup --managed-witness /etc/orva/host-witness/witness` creates the
   witness once. The witness's parent directory must be private (`0700`) and
   owned by the daemon user. If its grandparent is not writable by that user,
   have the host administrator create the private parent first. Use the
   configured path if different.
2. Install both trusted runtime trees in `rootfs/node` and `rootfs/python`
   under the instance data directory, with the current Orva adapters and SDKs.
   Use the verified release installer or image artifacts; ordinary
   `orva setup` can install the matching adapters into pre-positioned trees.
   Do not use source trees writable by other users. Stop all writers to
   those trees before importing them. Then run `orva setup --managed-layout
   provision` to publish provisioning intent; the layout command does not
   itself install rootfs trees.
3. From the prepared cgroup, run `orva serve`. In provisioning mode it creates
   the Owner only if the three private roots are empty, then exits without
   opening a database or listener.
4. From either the same service's replacement cgroup or the retained
   `orva.daemon` leaf (with explicit `ORVA_MANAGED_WORKLOAD_PARENT`), run
   `orva setup --managed-layout publish`. It verifies and closes the exact
   Owner before explicitly publishing managed mode. If you set
   `ORVA_SERVING_MODE`, update it from `provisioning` to `managed` only after
   publication; leaving it unset is simpler.
5. Run `orva serve` again in that service layout. Before opening the listener,
   it checks and seals each installed language without a recorded selection or
   explicit generation override. It commits each validated seal in the managed
   database before trying the next language, so a failure in the second language
   leaves the first recorded. A retry after sealing but before that language's
   commit can reuse a verified seal that exactly matches the installed tree.
   Later starts check the interpreter and adapter in each selected generation
   (recorded default or explicit override), not in the mutable installed tree.

If a selected generation is not in the bound runtime store, startup refuses
and names its language and generation. It does not clear the record or seal a
replacement, even if an installed tree is available. An explicit override
must name an already-sealed, usable generation for that language and does not
change the recorded default. Do not edit SQLite or delete sealed stores or the
Owner to try to repair a missing generation.

If neither language has a usable generation and neither tree is installed
(for example after `setup --skip-rootfs`), managed serving exits 78. Install
the trusted runtime trees and retry `orva serve`; do not delete the Owner or
edit the mode marker. One configured or installed runtime is sufficient for
startup, but builds in the other language remain unavailable. A present
untrusted, oversized, or invalid runtime tree that would need to be sealed
causes startup to refuse rather than silently accepting it. A capacity refusal
names the language being sealed. Ownership and writable-mode refusals identify
the offending path and suggest correcting its owner or mode. Entries must be
owned by root or the daemon user; group/other-writable entries are refused
except for symlinks' mode bits and empty sticky `tmp` and `var/tmp` directories.
The sealed copy preserves directory sticky bits. Problems such as extended
attributes or unreadable metadata do not get a `chown`/`chmod` hint.
Reinstall trees from the verified installer or image rather than unpacking a
release tarball as another user.

SIGTERM closes and drains HTTP, then closes the managed instance (including
its scheduler), then fences and closes its Owner. Startup or shutdown refusal
exits 78, never the restore restart status 70. On the same boot, the daemon
fences intact groups or proves that both the old parent and its group were
removed. Do not delete individual stale cgroups to force a start: a missing
group under its original parent still denies, as do missing old groups with
no authenticated parent identity and witness mismatches. Before restarting a
host-bound install first booted with an older binary that lacks parent
identities, preserve exact groups until a verified cold recovery or until
bounded same-boot retirement removes the older references. Incomplete fresh provisioning
requires supervised inspection, not an automatic retry with new stores.

Uninstall deletes the standard native witness, even when retaining data;
the sealed stores then become **unrecoverable on that host**. For externally
managed witnesses or volumes the operator must explicitly remove the witness
as part of uninstall. Never copy a witness to preserve recoverability after
uninstall.

## Internal restore-quarantine fixture

`workloadrecovery` is an explicit Linux scratch composition, **not** the current
public restore handler or legacy boot path. It requires trusted stopped/drained
admission before writing a durable external marker and replacing any DB files.
Its startup transaction migrates empty legacy authority, quarantines all durable
work origins and policy/inbound authority, fails abandoned builds, and removes
the marker only after durable commit. Same-operation replay is idempotent;
normal restart does not reset individual reviews.

Managed restore also unpublishes custom routes. Stored mappings and their
version counters remain inspectable. Use a fresh route inspection and explicitly
republish each desired mapping through the managed CAS API; this is separate
from installing and reviewing the target workload.

There is no shipped recovery CLI or production activation flag in this change.
For internal manual fixtures, `PrepareManual` must precede stopped-instance file
copying and `CompleteManual` must acknowledge a verified complete install before
startup. Direct file copying without preparation is outside the quarantine
guarantee. The old restore's DB rollback does not roll back replaced keys/code;
an interrupted installation must remain closed rather than claiming full rollback.
See [the recovery state machine and limits](../backend/internal/workloadrecovery/README.md).

## Internal configuration publication workflow

No production upgrade or activation is required. The opt-in `workloadconfig`
service requires initialized artifact/managed sources and enrolled concrete
code/runtime stores; never point it at mutable current paths or infer a Factory
from a durable generation row. An explicitly installed queued deployment in
those stores becomes the pair used by later configuration mutations. A queued
publication awaiting its own install cannot be installed through configuration.
Rollback, clone/import, triggers, runtime upgrades, boot and restore are separate.

In an explicit scratch composition, first inspect exact function/generation and
policy/source expectations. Publish the environment/settings/secret mutation:
it acknowledges **review-required DENY**, not a rebind. Old current Factories are
invalidated before success; old busy workers lose SDK authority while possibly
retaining injected values in memory. Next explicitly install a newly prepared
Factory using the fresh complete expectations, then explicitly review the new
installed artifact and current aggregate sources through the managed-policy
service. Mutation/installation cannot clear quarantine.

On conflict, inspect again and get a new deliberate decision; do not silently
retry with refreshed revisions. A failed transaction leaves old host/cache and
durable state unchanged. If installation fails after a successful mutation,
remain denied and retry fresh preparation/installation; do not approve the old
artifact or edit rows to bypass review. Interrupted in-process cleanup, reboot
and restore recovery are separate gates, not proven by this procedure.
For sensitive removed/changed secrets retire affected workers and rotate the
external credential as needed; IAM does not erase old memory or exfiltrated data.
## Internal managed queued-build recovery (not serving activation)

The optional publication queue refuses legacy function directories and never
changes a `current` link. Keep sealed stores outside legacy GC/cache/version
namespaces. Do not enable it on normal serving construction or mix writers.
Publication success means inactive review-required DENY; explicit fresh Factory
Install and separate source-aware Review are still necessary. No public Install
endpoint or boot/restore loader is added.

Accepted jobs survive request cancellation. Full/stopping queues fail recorded
work and remove private archives; normal restart abandonment remains fail-only,
not autoresume. Fixture `AbandonWorkloadDeployments` is explicit and idempotent.
Sealed-but-unpublished output stays bounded and non-authoritative for inspection;
there is no automatic GC, replay or restore adoption. Retain a live service with
pending pins after storage failure. After process loss use store inspection only,
not fabricated leases/approval. A failed post-commit installation leaves DENY;
retry Install from a fresh exact snapshot before reviewing the current Factory.
See `backend/internal/builder/README.md` for the full failure ordering and limits.

## Internal history fixture recovery limits

Normal boot does not initialize or activate deleted-work history. In the
explicit scratch composition, a failed capacity preflight leaves the function
active; a committed reservation followed by interrupted cleanup leaves durable
`deleting` progress. Retry through the same configured registry hook with fresh
trusted admin authentication, never by editing rows or disabling history to
fall back to legacy deletion. Each bounded archive/live-cleanup batch commits
atomically. Child-process fault resumption is tested, not power-loss/reboot
recovery. Ninety days is purge eligibility only: explicit confirmed/admin purge
is audited and cannot consume reservations. Full capacity refuses new deletion;
there is no silent eviction. Restore remains a separate gate: archives must stay
inspection-only and live work must be quarantined, not replayed. See the
history package README for unqualified cut-over obligations.

## Internal IAM jobs fixture

No production upgrade action is required for this inactive increment. Do not
call its explicit schema initializer or wire its constructors in normal serving.
The fixture's Jobs view distinguishes stored execution status from authority:
suspended/quarantined work is not runnable. Regrant is not resume. An admin can
explicitly resume one eligible pending job after revalidating its original
creator/target; retry never adopts it as operator work.

The independent cron core uses separate explicit initialization/options. Its
separate controls fixture is not a production route/catalog activation. Explicit
admin HTTP/CLI/MCP/shared-AI resume and the CronJobs view delegate to the service,
require inspected decimal CAS revisions and preserve source identity. Resume revalidates
original authority and schedules only a future cron time. Do not clear authority
fields or infer operator origin from old names. Regrant is not resume. Preserve
the same target-deletion/restore HOLD as jobs; cascade deletion is not retained
suspension history.

In the staged view, Enabled describes stored configuration, not permission to
execute. A stale snapshot blocks mutation and does not display authority as
current. On 409, refresh and review; do not retry automatically with replaced
revisions. A failed refresh retains the prior snapshot as stale and leaves the
mutation error visible. Read-only users can inspect source/reason/revisions but
cannot resume, edit, disable or delete.

Legacy function hints stay suspended for review; unknown origins stay
quarantined. There is no adoption/restore procedure in this slice. Do not clear
authority fields with SQL or edit a row to bypass review. Target-deletion history
retention remains unresolved because the legacy FK cascades job rows; missing
UUIDs fail closed, and recreation by name does not grant authority.

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
| `429 INVOCATION_QUEUE_FULL` | the memory/FD-derived pending budget or pre-body memory reserve was exhausted, or a request waited 2 seconds at a saturated pool (up to 12 seconds while workers can still start) | retry with backoff and jitter; inspect `queued`, `spawning`, `cold_start_p95_ms`, `queue_wait_p95_ms`, `effective_max`, `limiting_reason`, and host memory. Sustained pressure requires more host capacity or faster functions |
| `429 STORAGE_BACKPRESSURE` | the execution-record writer cannot reserve a completion slot before the function starts | retry with backoff and jitter; inspect writer queue depth, critical timeouts, activity drops, disk latency, and SQLite checkpoint/vacuum activity. No function code ran for this response |
| `429 TOO_MANY_REQUESTS` | legacy host-wide concurrency-cap response | client should back off + retry |
| `503 POOL_AT_CAPACITY` | legacy pool-capacity response | inspect `limiting_reason`; normal HTTP queue expiry now returns `429 INVOCATION_QUEUE_FULL` |
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
With no override, the pool maximum is CPU/memory-derived; `max_warm=0` means
automatic. A positive saved override can still be the limiting reason after
an upgrade. Raise or clear it only when telemetry says `operator_max`;
`cpu_capacity`, `memory_capacity`, and `function_concurrency` need different
remedies.

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
entrypoint, or a package with no wheel for the runtime's Python (3.15).

**Fix.** Redeploy with corrected code; or rollback to the last known
good version via the Deployments view.

If **every** invocation waits for a cold worker and returns HTTP 429
`INVOCATION_QUEUE_FULL` after a manual binary-only upgrade (one that bypassed
the `orva upgrade` rootfs guard with `--force` or an unstamped rootfs), check
the runtime adapter version before tuning concurrency. A server that expects the
adapter's startup `ready` frame cannot use an older rootfs adapter that does
not emit it. For an existing bare-metal rootfs, run the **new binary's**
`orva setup --skip-nsjail --data-dir /var/lib/orva` to refresh its embedded
adapters and SDK, then restart Orva. Prefer the normal installer for upgrades:
it refreshes the binary, rootfs, and adapters together. The Docker entrypoint
refreshes adapters on every container start and replaces a volume's runtime rootfs
whenever its `.orva-runtime` interpreter stamp or `.orva-rootfs-revision`
content revision differs from the image's. A missing volume revision triggers
a one-time refresh, so bytecode-only updates reach existing volumes too.
Matching identities skip the copy. The interpreter stamp remains unchanged for
content-only updates, preserving dependency-cache identity.

During function deletion, completion records for that function may still be
queued after the API has returned. The writer now discards those records and
increments `writer.deleted_function_writes` rather than reporting an SQLite
foreign-key failure. This counter should rise only alongside deliberate
function deletion; `writer.critical_failures` remains the storage-failure
signal. Parentless captured requests, spans, and structured logs for the
deleted function are removed in the same delete transaction.

## Symptom: some functions fail right after a runtime upgrade

An Orva release can move a function runtime to a newer Node.js major or Python
minor version. Afterwards some functions fail while others keep working:

- a function that imports a compiled package at the top of the handler returns
  `502 WORKER_CRASHED` — the worker dies before it reports ready. The package's
  `ImportError` (Python) or `Module did not self-register` /
  `was compiled against a different Node.js version` (Node) is in the
  execution's stderr;
- a Node function that requires a native addon *inside* the handler returns
  `500` with that same message.

Functions that are plain code, pure Python or pure JavaScript, or that use a
stable ABI (`abi3`, N-API) are unaffected.

**Cause.** Dependencies are installed into the function's version directory, so
the binaries there were built for the old interpreter.

**Fix.** Redeploy the function from its source. The version hash covers the
runtime, so unchanged source is rebuilt on the new runtime instead of reusing the
old directory. Rolling back to a deployment that was built before the upgrade
brings the old, incompatible dependencies back. If the rebuild fails with pip's
"no matching distribution", the package has no wheel for the new Python yet;
pin an older version that does, or wait for the package to publish one.

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
- **cgroup controllers not delegated.** When the service/container has no
  writable `cpu`, `memory` and `pids` delegation, or its visible membership
  is `/`, Orva logs `cgroup v2 controls unavailable; per-sandbox
  memory/pid/cpu caps disabled (rlimit-only fallback)` with a reason and
  runs functions **without** hard per-sandbox caps rather than crashing.
  Check `systemctl cat orva` for `Delegate=yes` and inspect the service's
  `cgroup.controllers`. Orva creates its own daemon and worker leaves only
  below that service cgroup; it will not claim the host root as a delegate.
- **nsjail capabilities excluded by systemd.** This applies to the capability
  fallback only. When the installer's probe selects user namespaces, the unit
  ships `CapabilityBoundingSet=` empty, `NoNewPrivileges=yes`, and nsjail
  carries no file capabilities. In fallback mode, inspect `systemctl cat orva`:
  the bounding set must retain every capability the installed nsjail binary
  carries as a file capability — `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`,
  `CAP_SETUID`, `CAP_SETGID`, `CAP_NET_BIND_SERVICE` — otherwise Linux rejects
  the setcap nsjail binary at `execve` before it can log. These are **nsjail's**
  requirements, not orvad's: `CAP_NET_ADMIN` is what lets nsjail configure the
  TUN interface for `--user_net` (`network_mode: egress`). orvad itself
  administers no host networking, so dropping the capability from the bounding
  set does not "turn off the firewall" — it breaks sandbox spawn outright.
  Re-running the current bare-metal installer refreshes the unit safely on
  upgrades.
- **`/dev/net/tun` missing.** If only `network_mode: egress` functions fail
  (and `none` functions run fine), nsjail cannot open the TUN device.
  `modprobe tun` on bare metal; pass `--device /dev/net/tun` in Docker.

Quick confirmation that nsjail itself works on the host:
`sudo -u orva nsjail -Mo --chroot /var/lib/orva/rootfs/node -T /tmp -- /usr/local/bin/node --version`
should print the Node version.

If it instead reports `setGroupsDeny failed`, `Couldn't initialize user
namespace`, or cannot write `/proc/<pid>/setgroups`, the host has blocked the
nsjail user-namespace sequence even when `unshare` or the sysctl appears to
work — **or the binary is carrying file capabilities**, which breaks the same
sequence. Test the capability-free form, because `cp` does not preserve file
capabilities:

```bash
cp /usr/local/bin/nsjail /tmp/nsjail-nocap && chmod 0755 /tmp/nsjail-nocap
sudo -u orva /tmp/nsjail-nocap -Mo --chroot /var/lib/orva/rootfs/node -T /tmp \
  -- /usr/local/bin/node --version
```

If that prints the Node version, the host supports user namespaces and the
installer will select them on the next run; re-run it to clear the stale file
capabilities and rewrite the unit. Otherwise re-run the current installer: it
tests both modes as `orva` and persists the verified fallback. To diagnose an
intentional override, test the fallback:

```bash
sudo -u orva /usr/local/bin/nsjail -Mo --disable_clone_newuser \
  --chroot /var/lib/orva/rootfs/node -T /tmp -- /usr/local/bin/node --version
```

Do not run orvad as root to work around this. The service account remains
unprivileged; only nsjail carries the narrow capability set needed to construct
the sandbox. Check Settings → Build info or `orva system health` for the active
sandbox mode and any `rlimit_only` warning.

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

**If the hash *is* there and rollback still says `VERSION_GCD`**, the data
directory came from a backup taken by a binary that predates this fix. Those
archives dropped every dotfile under `functions/`, including the `.orva-ready`
marker that tells Orva a version is complete — so the code is on disk and
reports as gone, and the version GC stops pruning that function entirely
(`versions_to_keep` silently does nothing). Restoring the archive again with a
current binary repairs the markers on the way in. To repair a data directory in
place instead:

```bash
docker exec orva sh -c '
  for d in /var/lib/orva/functions/*/versions/*/; do
    h=$(basename "$d")
    [ ${#h} -eq 64 ] || continue          # skip build scratch, <hash>.tmp.<rand>
    [ -e "$d/.orva-ready" ] && continue   # already complete
    [ -n "$(ls -A "$d")" ] || continue    # empty: nothing to vouch for
    printf %s "$h" > "$d/.orva-ready"
  done'
```

The marker's content is the version's own directory name.

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

## Symptom: a function shows status `deleting`

A function delete is chunked and takes as long as the function has data (about
11,000 rows per second was measured; a function with two million executions
takes a few minutes). While it runs the function shows `deleting`, does not
invoke or fire cron/jobs, and answers `409` to edits. If the `DELETE` call timed
out in a proxy, the delete is still running: it is detached from the request,
bounded at 30 minutes. If a restart or an error interrupted it, the function
stays `deleting`; the server resumes it at the next start, and repeating
`DELETE /api/v1/functions/{id}` resumes it immediately. Look for
`resumed interrupted function delete` or `resume function delete failed` in the
server log.

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

## Symptom: sandbox uploads stall while small uploads work

The NSTUN revision shipped before the partial-write correction could acknowledge
guest data but strand the remainder of a successful short host-socket write.
This can cause S3 upload timeouts and incomplete request bodies even when DNS,
credentials, and the storage service are working. See
[NSTUN_PARTIAL_WRITE.md](NSTUN_PARTIAL_WRITE.md) for evidence, the reproducible
dependency fix, regression coverage, upgrade verification, and rollback.
Do not weaken egress policy or change function credentials to work around it.

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

### Managed mode: portable recovery

`orva backup download -f /safe/path/orva-managed.tar.gz` uses the managed
backup endpoint, which requires read and admin authority. Keep the archive private: it contains SQLite,
available encryption/bootstrap keys, and current Node/Python source inputs.
The v2 file hashes detect corruption, not the identity of the producer;
confirm only archives received through a trusted channel. No sealed trees,
Owner records, cgroup epochs, runtime rootfs trees or witness are exported.

On the destination, stop all Orva writers and prepare a fresh, service-owned
instance directory with locally installed Node/Python rootfs trees, current
adapters, nsjail, and [delegated cgroups](DEPLOYMENT.md#opt-in-managed-daemon-supervised-setups-only).
Independently provision B's external witness. Run as the daemon user and set
its location in the service environment if using a non-default path:

```bash
export ORVA_DATA_DIR=/srv/orva-b
export ORVA_MANAGED_WITNESS_PATH=/private/b/witness
orva setup --managed-witness "$ORVA_MANAGED_WITNESS_PATH"
```

Keep the archive outside the data directory and the witness outside the data
and configured backup roots.
You can start with an unopened provisioning marker, but not initialized Owner
stores. Unset `ORVA_MANAGED_NODE_GENERATION` and
`ORVA_MANAGED_PYTHON_GENERATION` so boot uses the newly sealed runtime IDs.

```bash
orva managed restore --data-dir /srv/orva-b --archive /safe/path/orva-managed.tar.gz
orva managed restore --data-dir /srv/orva-b --archive /safe/path/orva-managed.tar.gz --confirm
```

The first command verifies the archive, destination layout, witness and local
runtime prerequisites without changing the destination; it does not run a
build or prove the destination can create cgroups. Run the confirmed command
as the daemon user from its delegated cgroup. It rebuilds the exact current
managed deployments, preserves active/inactive and pause state, reseals local
runtimes, and records the previous Owner in the SQLite restore audit. The
source tarballs come from the verified, retained build inputs (or a projection
of authored files for older seals). Installed dependencies and compiled output
are not copied. On the destination, npm and pip use the restored policy and
DNS settings with the local runtime toolchains; registries must be reachable
and packages still available. Pin dependency versions or use lockfiles to
reduce drift. Historical deployment rows remain for inspection,
but their old sealed outputs are unavailable on B. Explicitly excluded
enrollment entries remain inactive and their legacy source is not carried.

Managed mode is published last. Start the daemon with `ORVA_SERVING_MODE`
unset or set to `managed`. Direct invocation of previously active functions
can run, but all policies require fresh review; protected SDK and standing
work is denied, and custom routes and inbound webhooks are unpublished.
Review policy, jobs, cron and exposures separately before re-enabling them.
If confirmation was interrupted, preserve the archive and destination
evidence, including the staging directory, intent or tombstone. Both boot
paths refuse an incomplete layout with exit 78. Do not edit or remove control
files to retry; use a separate fresh destination for a new restore attempt.

To replay on A with the same witness, Owner and sealed outputs still present,
stop the daemon and use `orva managed restore --same-owner --archive
/safe/path/orva-managed.tar.gz --confirm`. This uses guarded same-Owner recovery
and its existing quarantine; it does not rebuild or overwrite sealed stores.
The legacy HTTP upload and online restart path below do not apply to managed mode.

### Legacy mode

`orva backup download` writes an archive containing the SQLite database,
every deployed function version, the secrets master key, and the bootstrap
admin API key. It is written mode `0600` — treat it as the credential it is.

A version directory is captured whole, dotfiles included. Older archives
skipped them, which lost both the `.orva-ready` completeness marker and any
`.env` / `.npmrc` / `.python-version` the author deployed — `orva deploy` packs
hidden *files*, it only skips hidden *directories*. Restore recreates a missing
marker, so an old archive comes back usable; files that were never archived
cannot be recovered from it, so **re-take any backup you are keeping as a
restore point.**

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

**Alpine / OpenRC.** OpenRC has no per-exit-status restart filter, so the
equivalent is running the daemon under a supervisor. The shipped init script
sets `supervisor="supervise-daemon"`, which respawns on **any** unexpected
exit — a superset of `Restart=on-failure`, and it covers exit 70. A script
predating this used `start-stop-daemon`, which never respawns: a successful
restore left the host down exactly as an un-patched systemd unit did. Re-run
`install.sh` to pick it up, and check with:

```bash
grep supervise-daemon /etc/init.d/orva
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

## SDK channel cut-over

Upgrade the server and installed runtime adapters/SDK together. SDK-enabled
functions require the `orva-sdk-proto:1` adapter capability and inherited worker
channel; an incompatible adapter is an actionable spawn failure, not a reason
to enable outbound networking. Use the normal installer/setup procedure above
to refresh adapters. Existing SDK permission grants survive the upgrade.

The SDK HTTP route families, worker credentials/base URL and
`ORVA_INTERNAL_API_BASE` override are removed. Private-network block rules no
longer carry an Orva HTTP exception. Do not restore a network exemption to fix
an SDK error: check the function's `sdk` permission and installed adapter first.
Public HTTP invocation and operator APIs retain their existing authentication.
