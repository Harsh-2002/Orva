# Pool Controller v2 capacity validation

An experimental dispatch-boundary reservation was **reverted**. On a
2-vCPU/4-GiB server VM with a separate client VM, immediately rejecting after
worker acquisition gave 1,499 HTTP 200 and 48,501 pre-execution storage 429s
for 50,000 mixed Node/Python requests at 1,000 clients. Waiting up to five
seconds while holding the worker gave 49,983 HTTP 200 and 17 function-queue
429s (408 attempted requests/s; HTTP 200 p99 5.83 s). After drain, its
49,983 successes matched 49,983 execution rows; critical failures/timeouts
were zero, but 17,116 activity records dropped. The committed candidate
retains early completion reservation while a shared worker/storage dispatcher
is designed. These runs do not prove a throughput gain over that candidate;
the fixed 1,024 writer slots and activity/capture shedding remain open limits.

The current unreleased candidate measures `service_p95_ms` over each complete
worker lease rather than stopping at the adapter's first response frame. This
corrects the controller's service-time input but has not yet been benchmarked
against the prior build in a safely isolated mixed-function capacity run.
The changed binary passed 28 E2E modules and 668 checks with real nsjail
invocations required on a disposable 2-vCPU/2-GiB Ubuntu smolvm guest; no
module failed or skipped. That guest used the verified nsjail file-capability
fallback but reported `rlimit_only` because cgroup controllers were not
delegated, so this pass does not validate hard per-worker cgroup limits.
The next writer-safety change makes its critical and best-effort telemetry
byte ceilings atomic under concurrent enqueues. A job now reserves its
estimated bytes before entering a channel and releases that reservation if
the enqueue times out or is shed. This prevents queue-memory overshoot; it
does not increase SQLite's measured sustainable write rate or recover
telemetry already dropped under overload.
Writer shutdown also fences queue publishers before the final drain. Critical
records accepted just as shutdown begins can no longer be stranded behind the
consumer's exit; late critical calls use the direct-write fallback while the
database remains open. This does not add power-loss durability beyond the
existing acknowledged-in-memory contract.
The bounded writer now keeps a job's byte reservation through commit and
transient retry, rather than releasing it merely when the consumer takes it
out of the channel. A retry is copied before clearing its old batch storage;
the prior aliasing path could zero the SQL and arguments of an accepted batch.
The health `*_queue_bytes` counters therefore include in-flight work; channel
depth alone cannot prove that it is safe to delete a just-loaded test function.
A 100-client, 10,000-invocation Node/Python scratch run on the prior writer
binary returned 10,000 HTTP 200 but the harness's immediate function deletion
was followed by 34 critical failures and 883 dropped telemetry records. The
same harness, changed to wait for writer drain before deleting functions,
returned another 10,000 HTTP 200 with **zero new critical failures before
cleanup** and 1,029 new telemetry drops; the critical-failure total did not
increase after deletion. The secondary-write loss is real under this load;
the initial critical-failure increment was a test-cleanup race, not evidence
that ordinary successful invocations lost their completion records.
After the retry-alias and in-flight reservation fix, the same 2-vCPU/2-GiB
scratch VM passed all 28 real-sandbox E2E modules (668 checks, no failures or
skips). Three sequential 10,000-request Node/Python harness runs at 100
clients each returned every HTTP 200; pre-cleanup critical-failure deltas
were 0/0/0 and telemetry-drop deltas were 0/113/0. The first cleanup still
added three critical failures because client response completion preceded the
handler's final enqueue; waiting for `active_requests=0` as well as writer
bytes to drain kept that total unchanged in the following two runs. Activity
rows missing from the middle run were 55 of 10,200 expected (including
warm-up), confirming some drops affected the operator activity feed, not just
optional replay capture. This is an undelegated 2-GiB functional/load probe,
not a controlled A/B or the 2-vCPU/4-GiB capacity target.

The next candidate separates activity from optional replay/log/span writes.
Execution records still use bounded critical backpressure; activity has its
own bounded, non-blocking lane selected fairly with critical work, while
optional telemetry is read only when neither higher-priority channel is
waiting. Health now exposes `activity_queue_depth`, `activity_queue_bytes`,
and `dropped_activity` (included in `dropped_telemetry`). This prevents a
full optional-capture channel from directly displacing activity, but it does
not promise zero activity loss if the activity lane or SQLite itself saturates.
The 55 missing rows above are from the *prior* binary. With the lane split,
a disposable 2-vCPU/2-GiB Ubuntu smolvm guest ran 5,000 Node and 5,000 Python
requests at 100 clients, 2,500 per runtime in the mixed phase, 1,000
CPU-bound requests, and a 500-request overload phase. The ordinary phases
returned HTTP 200 for every request; overload returned 216 HTTP 200 and 284
bounded HTTP 429. Before cleanup, critical failures, total best-effort drops,
and activity drops were all zero. The same guest then passed 28 real-sandbox
E2E modules with zero failures or skips. This is a functional regression
check, **not** a controlled A/B or the 2-vCPU/4-GiB capacity target. Its
cgroup controllers were not delegated, so hard per-worker memory enforcement
was not validated. After the E2E suite's create/invoke/delete flows, writer
health showed four critical execution-row failures: a function was deleted
after a request returned but before its final async execution insert committed,
so SQLite rejected the insert's function FK. The current candidate adds a
writer commit fence around function deletion, tags execution-related jobs with
function ownership, and removes pre-parent child rows by function id. Jobs
whose function was deleted before commit now increment
`deleted_function_writes`, not `critical_failures`. The four-failure run was
on the *prior* binary. The rebuilt candidate passed a delete-during-invocation
regression and the complete 29-module/676-check real-sandbox E2E suite on a
fresh 2-vCPU/2-GiB Ubuntu smolvm guest using virtio-net. After E2E,
`critical_failures` and `critical_timeouts` were zero; nine deliberate
post-deletion jobs were counted separately. A bounded guest-loopback load
returned 5,000/5,000 Node and 5,000/5,000 Python HTTP 200 at 100 clients,
then 2,500/2,500 HTTP 200 per runtime in mixed traffic and 1,000/1,000
HTTP 200 for CPU-bound work. An intentionally slow 500-request phase returned
224 HTTP 200 and 276 contract-compliant HTTP 429, with no 504 or transport
errors. Before cleanup, critical failures, dropped telemetry/activity, and
unexpected deleted-function writes were all zero. These are functional and
bounded-load checks on a 2-GiB VM, not a controlled 4-GiB capacity curve.
The first scratch VM used smolvm's TSI networking: its own DNS worked, but
nested build-jail DNS failed with `EAI_AGAIN`; virtio-net made jailed npm/pip
installs and the firewall E2E pass. The first VM was deleted after diagnosis.

## 2026-09-23 direct 2-vCPU/4-GiB persistence-pressure finding

A fresh 2-vCPU/4-GiB Ubuntu smolvm server and separate 1-vCPU/512-MiB
load-generator VM used a private virtio-net link, not host port forwarding.
Both Node and Python functions returned a constant HTTP 200. Closed-loop
mixed-function observations on the unreleased `2af901b` build were:

| Clients | Requests | HTTP 200 | Client errors | Successful req/s | HTTP 200 p99 |
|---:|---:|---:|---:|---:|---:|
| 10 (cold warm-up) | 200 | 200 | 0 | 107 | 1,626 ms |
| 50 | 5,000 | 5,000 | 0 | 335 | 328 ms |
| 100 | 10,000 | 10,000 | 0 | 509 | 463 ms |
| 500 | 20,000 | 20,000 | 0 | 461 | 3,764 ms |
| 1,000 | 50,000 | 50,000 | 0 | 429 | 5,135 ms |

The 50,000/50,000 HTTP result **did not mean 50,000 durable execution
records**. After all 85,200 responses across the sweep and writer drain,
SQLite contained 84,446 rows for the two test functions: exactly 754 missing,
matching `writer.critical_timeouts=754`. Health also counted 1,028 dropped
activity records and 77,794 dropped optional telemetry records. Under 500
clients, a goroutine snapshot found 497 HTTP handlers blocked in the
post-execution five-second critical enqueue. A 20-second CPU profile attributed
about 41% of sampled daemon CPU to the async writer, mostly SQLite
statement/commit work. During mixed load, the Python pool queued work while
the Node pool held idle workers and both reported `memory_capacity` despite
substantial guest memory headroom. These are measured code/storage bottlenecks,
not evidence that the 4-GiB hardware was exhausted.

The next candidate reserves a critical completion slot **before** a public
function runs and returns `429 STORAGE_BACKPRESSURE` if no slot is available
within a bounded wait. Its correctness and goodput are tested below; the table
above is a baseline, not a claimed gain.

The same two functions were then retested on successive unreleased builds in
the same VMs, each with 50,000 requests at 1,000 closed-loop clients:

| Writer/admission candidate | HTTP 200 | Pre-execution 429 | Missing execution rows | 200/s | HTTP 200 p99 |
|---|---:|---:|---:|---:|---:|
| Reserved slot, 50-row batches, 2-second wait | 42,465 | 7,535 | 0 | 408 | 3,467 ms |
| Critical-first drain, 50-row batches, 2-second wait | 46,085 | 3,915 | 0 | 456 | 3,433 ms |
| Critical-first drain, 200-row batches, 2-second wait | 49,299 | 701 | 0 | 507 | 3,187 ms |
| Critical-first drain, 200-row batches, 5-second wait | 50,000 | 0 | 0 | 462 | 4,030 ms |
| Final all-entrypoint candidate, same settings after full E2E | 50,000 | 0 | 0 | 566 | 3,500 ms |

Every candidate had zero transport errors, critical writer failures, and
critical writer timeouts. For each phase, the post-drain SQLite row-count
increase exactly matched its HTTP 200 count. This fixes the measured silent
execution-record loss for the public HTTP path at this load. The longer wait
eliminated pre-execution rejections here, but increased response latency and
did **not** establish higher sustainable throughput. Activity and optional
capture still shed records when their queues saturated; those records are not
covered by the execution-row guarantee. The database grew across phases and
host contention varied, so these are exploratory observations, not a
controlled performance regression gate.

The final candidate also passed the full 29-module real-sandbox E2E suite in
this VM with `ORVA_REQUIRE_SANDBOX=1`, plus race-enabled database, handler,
and scheduler unit tests. The final closed-loop phase again reconciled exactly
25,000 Node and 25,000 Python responses to the same number of new rows.

Scheduled-arrival follow-up on that binary was not a throughput victory:

| Target arrivals/s | Scheduled | Attempted | Unsent by client | HTTP 200 | Completion rate | HTTP 200 p99 |
|---:|---:|---:|---:|---:|---:|---:|
| 650 | 20,000 | 16,042 | 3,958 | 16,042 | 447/s | 3,435 ms |
| 400 | 10,000 | 10,000 | 0 | 10,000 | 340/s | 4,323 ms |

Both phases had zero transport errors and exact accepted-response-to-row
reconciliation. The 650/s phase is **client-limited** because it could not
send every scheduled arrival; it says nothing reliable about server capacity
at that offered rate. The 400/s phase delivered its arrivals but completed
after the schedule ended, so queueing persisted. The SQLite writer and
duplicated activity/capture writes remain the central throughput bottleneck.

An experimental grouped final-execution INSERT reduced a local writer-only
synthetic one-column, 200-row transaction benchmark from 583–588 µs and
about 70 KiB/2,024
allocations to 290–291 µs and about 18 KiB/44 allocations. The matching
50,000-request VM run still returned 50,000 HTTP 200s with exactly 50,000
new execution rows, but only 372 successful requests/s and 4,850 ms HTTP
200 p99. The host had substantial unrelated CPU load and the database had
grown, so this run does **not** establish an end-to-end improvement; a
controlled alternating A/B is still needed. A duplicate-row unit test
confirmed that a grouped-statement failure falls back to per-row isolation
without losing its valid neighbors or leaking completion reservations.
Grouping the activity INSERT as well preserved all 50,000 execution rows in
another 50,000-request VM run (478 successful requests/s, 3,523 ms p99), but
the then-current priority rule deferred activity whenever the critical queue
held one batch. It shed 45,840 activity rows, about 92% of the run's requests.
That is not an acceptable observability result. Moving activity deferral to
the critical queue's three-quarter high-water mark cut activity drops to
16,912 in a further 50,000-request phase; all 50,000 responses were HTTP 200
with exactly 50,000 new execution rows, 448 successful requests/s and
4,218 ms HTTP 200 p99. This is an observability trade-off, not a demonstrated
throughput gain; about one-third of activity records still shed at this
load, and optional capture remains heavily lossy.

The guest reported `rlimit_only`, so these tests do not qualify hard per-worker
cgroup enforcement. No alternating A/B or open-loop comparison has yet been
made.

## 2026-09-23 independent two-VM follow-up (unreleased candidate)

### Mixed-function and scheduled-arrival follow-up

The new `test/performance/loadgen` binary was built and run from a separate
2-vCPU/1-GiB client VM against a 2-vCPU/4-GiB server VM over their private
link. It reports `unsent` arrivals as client-rig failure, separately from
HTTP status and transport errors. Node and Python functions were both trivial
warm handlers returning HTTP 200. A cold-ramp 10,000-request mixed run at
100 clients had 9,846 successes and 154 Python queue 429s; two sequential
repeats had 9,949/51, then 10,000/0. Writer telemetry drops were zero after
the first run. The scheduler's live snapshot during later load showed Python
with one busy worker, four spawning workers and 469 queued while spawn p95
was several seconds: the fixed two-second queue wait can expire before cold
workers become ready. Once warm, the same 100-client mixed workload can pass.
The next local candidate changes that mismatch: a pool that can still grow or
is spawning waits up to its ten-second adapter-readiness budget plus one
scaler tick, while a fully occupied pool retains the two-second overload
wait. A focused race-tested unit case confirms a worker arriving after two
seconds is acquired. The changed binary passed all 28 real-sandbox E2E
modules (660 checks, no failures or skips) on a smaller 2-vCPU/2-GiB scratch
VM. Four optional trace-detail checks were absent because this fresh instance
had no trace to inspect. A Python handler with a three-second module-import
sleep returned its first HTTP 200 in 4.57 seconds — a direct real-sandbox
check of waiting beyond the former two-second cutoff. This proves functional
cold admission, **not** 4-GiB throughput or sustained mixed-function capacity;
do not attribute the observations above to this change.

A scheduled 800 requests/s, 20,000-arrival run with 1,000 client slots sent
all 20,000 arrivals and returned 16,276 HTTP 200 and 3,724
`INVOCATION_QUEUE_FULL` responses, with no transport errors. This is **not**
a sustainable-throughput claim: the server was warming/recycling workers and
the two functions did not receive equal service. Another phase at 3,000
client slots triggered a **global host OOM kill of the server VM**, not a
normal Orva rejection. The Linux journal recorded `libkrun VM` PID 485558
killed at 10:32:21 UTC with about 3.0 GiB resident. That phase is invalid as
a capacity measurement and should not be repeated on this 7.8-GiB/no-swap
development host while other workloads are active. The scratch VMs were
stopped; no production instance was used.

The load generator and Orva server ran in separate disposable smolvm guests
on a direct virtual network. The server had 2 vCPUs, 4 GiB RAM, real nsjail
and cgroup-v2 worker enforcement; the client had 2 vCPUs and 1 GiB RAM. This
avoided both a generator sharing the server's CPU quota and the unreliable
host port-forward path. The function was a warm Python handler returning
`"ok"`. The database was persistent and grew across sequential phases;
therefore these are exploratory capacity results, not controlled A/B proof.

| Build and phase | HTTP 200 | HTTP 429 | Transport errors | Total req/s | Client p99 |
|---|---:|---:|---:|---:|---:|
| `d5a1fd6`, 20,000 requests, 500 clients | 13,004 | 6,996 | 0 | 1,673 | 816 ms mixed |
| Candidate with resource-aware ingress, before lifetime reader, 20,000, 500 | 20,000 | 0 | 0 | 849 | 1.44 s |
| Candidate with lifetime reader, repeated 20,000, 500 | 20,000 | 0 | 0 | 1,323 | 484 ms |
| `d5a1fd6`, 50,000, 1,000 | 31,385 | 18,615 | 0 | 1,366 | 1.17 s mixed |
| Candidate with lifetime reader, 50,000, 1,000 | 50,000 | 0 | 0 | 1,102 | 1.53 s |

Successful throughput at 1,000 clients rose from about 858/s to 1,102/s,
while total response throughput fell because the baseline quickly rejected
many requests. All client p99 values include connection/network time and
must not be compared with dashboard handler latency. In the last candidate
phase, best-effort telemetry drops increased by 49,407; critical failures
and timeouts remained zero. The profile still attributes about one third of
CPU samples to the async SQLite writer. This candidate is not a complete
persistence or capacity solution, and the results are not a universal sizing
claim.

The candidate also passed 28 real-sandbox E2E modules (664 checks, zero
failures or skips) on the server VM, including Node and Python deploy/invoke,
and a separate warm Node run returned 5,000/5,000 HTTP 200 at 100 clients
over the direct inter-VM link. These functional results do not close the
mixed-function or persistence-throughput acceptance work.

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
> The current candidate derives pending-request limits from the detected
> memory and file-descriptor envelope, waits 2 seconds at a saturated pool
> (up to 12 seconds while the pool can still grow or is spawning), and returns
> `429 INVOCATION_QUEUE_FULL`
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
  same request rate can require different worker counts. Service p95 measures
  the full worker lease, from acquisition through response processing or
  streaming to release; it excludes queueing and worker startup.

Raise `max_warm` only when `limiting_reason=operator_max`. CPU or memory
limits require host capacity or smaller function limits; increasing the
operator ceiling cannot override the effective host maximum.

`max_warm` also sizes the pool's idle-worker storage, so it is capped at
**1024** — a larger value is rejected with a 400 rather than clamped
silently. `effective_max` remains the live host/operator ceiling, recomputed
each tick from *observed* memory use; `max_warm` is only its upper bound.
