# Pool Controller v2 capacity validation

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
