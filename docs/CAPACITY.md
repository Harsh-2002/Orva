# Pool Controller v2 capacity validation

## 2026-09-23 profiled concurrency follow-up (candidate, not released)

A separate disposable 2-vCPU/4-GiB smolvm guest ran the current candidate with
real nsjail sandboxes and enforced cgroup-v2 worker limits. A local-only CPU
profile of a warm Python `"ok"` handler at 100 clients showed substantial
async SQLite writer and pool-controller CPU use. The candidate consequently
prepares repeated SQL statements once per writer batch, inserts HTTP execution
outlier fields with the execution row, stores arrival rates in bounded
one-second buckets, and coalesces controller wakeups within 20 ms. These are
internal costs, not new operator concurrency settings or increased hard limits.

Sequential 30-second runs on the *same, growing* guest database ranged from
770 req/s on a repeated baseline to 964 req/s on the latest pre-UI candidate;
the latter returned 28,975/28,975 HTTP 200 at 100 clients with 246 ms client
p99. The comparison is **not controlled**: database size, WAL state and the
guest-local load generator changed the conditions between runs. It does not
prove a 25% production gain. The candidate also dropped 677 telemetry writes
in that run, so persistence pressure is not solved. The full real-sandbox E2E
suite passed 28 modules and 660 checks on that pre-UI binary. Repeatable
external-client and high-concurrency validation remain required before a
release claim.

The final rebuilt binary passed all 28 E2E modules with 664 checks. On that
candidate after the E2E suite, guest-loopback `hey` runs of
the same warm Python handler returned 5,000/5,000 HTTP 200 at 100 clients
(785 req/s, client p99 584 ms); 14,715 HTTP 200 and 5,285 HTTP 429 at
500 clients over 20,000 requests (1,167 total responses/s, mixed-status
client p99 1.14 s); and 48,423 HTTP 200 plus 1,577 HTTP 429 at 1,000
clients over 50,000 requests (1,108 total responses/s, mixed-status client
p99 1.60 s). There were no 504s or client transport failures in these three
guest-local runs. The 500-client result preceding the 1,000-client result
likely changed warm-worker state, so these are not steady-state comparative
curves. After the final run the critical writer queue drained and reported
zero timeouts, but the telemetry-drop counter stood at 69,868 and the
critical-failure counter at 3; neither counter was reset before the E2E
suite, so their increments cannot be attributed to this load phase alone.
Fixed pending-work caps and persistence throughput remain open bottlenecks.

A host-side `hey` client through smolvm's forwarded port returned 5,000/5,000
HTTP 200 at 100 clients (1,260 req/s), but at 500 clients returned only 5,402
HTTP 200 of 20,000 attempts; the rest were mostly EOF or connection-reset
errors at that forwarding path. The guest-loopback 500-client test had no
transport errors. This does not prove where the reset originates, and the
forwarded-port result is **not** an Orva throughput measurement. A direct
routed VM interface or another independently validated external path is still
needed.

The dashboard now separates worker/proxy `latency_ms` from
`response_latency_ms`, which covers the complete public invocation handler,
including admission failures and execution-record enqueue. Neither includes
client network/TLS transit. Compare response classes and client timings when
assessing overload; the previous 7/51/75 ms card cannot be read as public
end-to-end latency.

The current optimization work improves cgroup-v2 resource *discovery*:
capacity checks walk visible ancestors and account for parent and host physical
memory pressure, CPU quotas, and the effective CPU set. This does not enable missing cgroup
delegation or raise a verified throughput ceiling. Limits changed after startup
still require an Orva restart to update the initial capacity snapshot; live
reconciliation is planned separately. The historical throughput comparisons
below have not been repeated under identical external-client conditions.

## 2026-09-23 optimization work-in-progress check

A fresh 2-vCPU/4-GiB smolvm guest with the candidate binary passed the full
real-sandbox E2E suite: 28 modules, 660 checks, zero failures or skips. Its
firewall status named its **own** `100.96.0.2` interface for internal SDK calls,
not the host gateway's separate Orva instance. The firewall suite's SDK KV
test passed with RFC1918 blocking enabled.

The first scratch admission harness run **inside** the guest (loopback, 100
clients, 1,000 requests per single-runtime phase) returned 1,000/1,000 HTTP
200 for both Node and Python, 2,500/2,500 per runtime in the mixed phase, and
1,000/1,000 for the CPU phase. A 500-request slow-handler overload returned
376 HTTP 200 and 124 expected HTTP 429, with no 504 or transport error. A run
on the final binary still passed each single-runtime and CPU phase but the
mixed Python phase returned 2,467 HTTP 200 and 33 retryable HTTP 429; the
harness correctly failed that phase. This is not yet a consistently passing
mixed-concurrency acceptance check.
This is functional regression evidence, **not** an external throughput result:
the load generator shared the guest's two CPUs. A separate host-side run through
smolvm's forwarded port passed 5,000/5,000 for each single runtime, then
experienced connection resets during mixed traffic while the guest remained
healthy. The reset source is not yet attributed; no before/after performance
claim is made from these runs.

> The historical measurements below predate bounded invocation admission.
> Current admission caps pending requests at 256 per function and 1,024
> globally, waits at most 2 seconds, and returns `429 INVOCATION_QUEUE_FULL`
> on saturation. The function timeout starts only when a worker is acquired.
> These older throughput numbers are not evidence that the new admission path
> meets a 100-client target; rerun an isolated load test before claiming one.

## 2026-09-23 invocation-admission validation

The revised pool was built from the working tree and run as the unprivileged
`orva` user in a disposable smolvm KVM guest with 2 vCPUs and 4 GiB RAM.
Node and Python runtime trees were copied to guest-local storage; using the
shared host mount for runtime imports produced severe artificial I/O wait.
The guest's `/dev/net/tun` permissions were set to permit the unprivileged
build jail for the dependency-install E2E checks. It had no delegated cgroup
controllers, so this measurement validates invocation concurrency and sandbox
execution, **not** per-worker cgroup CPU/memory enforcement.

The safe, self-cleaning harness is
`python3 test/performance/invocation_admission.py --scratch --url <scratch-url> --api-key <key> --extended`.
It creates uniquely named functions and deletes only those functions. The
following were observed on the guest with a 5-second function timeout:

| Scenario | Result | Wall time |
|---|---:|---:|
| Node trivial handler, 5,000 requests / 100 clients | 5,000 HTTP 200, 0 errors | 8.80s |
| Python trivial handler, 5,000 / 100 | 5,000 HTTP 200, 0 errors | 11.44s |
| Mixed Node + Python, 2,500 each / 50 clients each | 5,000 HTTP 200, 0 errors | 14.65s (slower leg) |
| CPU-bound Node, 1,000 / 100 | 1,000 HTTP 200, 0 errors | 6.89s |
| Slow 1-second Node, 500 / 100 | 314 HTTP 200, 186 HTTP 429, **0 HTTP 504** | 14.42s |

The earlier candidate, before the adapter-ready handshake, returned only
400/5,000 successful Python responses; 967 were 504 and 3,633 were 429.
The adapter was still importing after the pool handed it to a timed request,
so startup contention caused execution timeouts, killed workers, and drove a
spawn/kill loop. nsjail's default nice level 19 amplified the problem. The
new handshake waits for the adapter to load the handler before publishing a
worker, and the launch argv sets nice level 0. Bounded admission then makes
genuine overload a retryable 429 rather than a function timeout.

The same guest passed the real-sandbox deploy/invoke E2E module: 31/31 checks,
including jailed npm and pip installs. The initial attempt failed those two
dependency checks because the guest's tun device was root-only; after fixing
the *test guest* device permissions, both passed. No production instance was
used for these measurements.

This document records reproducible measurements, not estimated capacity.
Numbers are a comparison aid for this host; they are not a universal sizing
promise.

## Test rig

- Date: 2026-08-11
- Host: 4 logical CPUs, 15,999 MiB RAM
- Baseline: `a1c7ac7d` (the KV reliability merge, before Pool Controller v2)
- Candidate: `codex/pool-controller-v2`
- Runtime: native Orva, nsjail, production Node rootfs, isolated ports and
  temporary databases
- Logging: `ORVA_LOG_LEVEL=error` for both measured runs
- Handler: 10 ms async Node response, 128 MiB declared memory, 1 CPU
- Load: 15 seconds, concurrency 32, persistent HTTP connections

The host did not delegate writable cgroup controllers to this development
process, so per-worker `memory.current` sampling was unavailable. Admission
therefore correctly stayed on the declared cgroup hard bound. Cgroup parsing
and constrained-capacity arithmetic are covered by deterministic tests and
the required provisioned-Linux CI lane.

## Before and after

| Metric | Baseline | Controller v2 | Change |
|---|---:|---:|---:|
| Successful requests | 17,183 | 15,978 | — |
| Failed requests | 0 | 0 | — |
| Throughput | 1,144.24 req/s | 1,063.80 req/s | **-7.03%** |
| Client latency p95 | 49.77 ms | 52.23 ms | +2.46 ms |
| Server service p95 | 37 ms | 39 ms | +2 ms |
| Queue-wait p95 | not exposed | **0.006 ms** | new signal |
| Cold-start rate | 0.402% | **0.200%** | -50.2% |
| Workers spawned | 69 | **32** | -53.6% |
| Workers killed during measured load | 37 | **0** | eliminated |
| Effective ceiling | 32 | 32 | unchanged |
| Capacity timeouts | not exposed | **0** | new signal |

The throughput gate allows at most a 10% regression; the measured 7.03%
decrease passes. The controller trades that bounded difference for half the
cold-start rate and removes the baseline's spawn/kill churn under the same
load. No worker exceeded the 32-worker effective CPU ceiling.

After correcting the unobserved-memory fallback, a separate 5-second
concurrency-32 check produced 1,205.28 req/s with zero failures. It reserved
6,144 MiB for 32 workers (32 × the 192 MiB declared cgroup hard bound), with
`queued=0`, `spawning=0`, and zero capacity timeouts after the run. Once
`memory.current` samples exist, admission uses observed worker memory p95,
clamped to the declared bound.

## KV control measurement

The same isolated candidate instance processed 1,000 concurrent atomic KV
increments:

| Metric | Result |
|---|---:|
| Final value | 1,000 |
| KV errors | 0 |
| KV timeouts | 0 |
| Cumulative increment latency | 2,338.098 ms |
| Mean database increment latency | 2.338 ms |

This confirms the pool changes did not disturb the KV reliability contract or
SQLite atomic-increment path.

## Controller invariants validated

Automated tests cover:

- exact stable, burst, and immediate-pressure formulas at 70% utilization;
- scale-to-zero only after the configured no-demand TTL;
- `spawning` publication before launch and at most four concurrent starts per
  function, including repeated evaluations;
- 30 seconds continuously below desired capacity before scale-down;
- no more than 20% shrink per evaluation and idle workers only;
- cgroup memory headroom and pending-reservation admission;
- declared memory as the safe fallback before an observed p95 exists;
- migration of legacy pool rows, removal of `target_concurrency`, preservation
  of values and foreign-key cascade behavior;
- rejection of stale configuration with `400 VALIDATION` migration guidance;
- scale-to-zero configuration normalization in the database and REST surface;
- deployment/policy generation retirement without crossing workers between
  generations.

The global scheduler rotates its starting function each evaluation. When
memory or CPU admission fails, it first reclaims an idle worker above the
configured minimum from the largest borrowing pool. Busy workers and active
configured minimums are never reclamation candidates. CPU admission is global
across pools, weighted by each function's declared CPU limit, and bounded to
eight I/O-overlap worker slots per effective cgroup CPU.

## Operational interpretation

Use these Pool Controller v2 signals together:

- `queued` and `queue_wait_p95_ms` show user-visible pressure.
- `spawning` distinguishes cold-start work from a stuck queue.
- `desired_workers` is the demand result; `effective_max` is the actual
  host/operator ceiling.
- `host.effective_memory_capacity_mb` is the live admission budget after
  cgroup headroom and worker reservations; `host.effective_cpu_workers` is
  the ceiling derived from the active CPU quota.
- `limiting_reason` says whether the active bound is stable demand, burst
  demand, immediate pressure, configured minimum, idle TTL, operator maximum,
  function concurrency, CPU capacity, or memory capacity.
- `cold_start_p95_ms` and `service_p95_ms` explain why two functions with the
  same request rate can require different worker counts.

Raise `max_warm` only when `limiting_reason=operator_max`. CPU or memory
limits require host capacity or smaller function limits; increasing the
operator ceiling cannot override the effective host maximum.

`max_warm` also sizes the pool's idle-worker storage, so it is capped at
**1024** — a larger value is rejected with a 400 rather than clamped
silently. `effective_max` remains the live host/operator ceiling, recomputed
each tick from *observed* memory use; `max_warm` is only its upper bound.
