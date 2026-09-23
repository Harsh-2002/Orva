# Hardware-aware concurrency optimization plan

Status: implementation in progress. Baseline: `17cfd00`, 2026-09-23. The first
resource-discovery and invocation-path slice passed isolated functional
verification; it does not establish a performance gain or full-plan completion.
Release publication remains paused pending the scheduler/admission work,
attributed capacity curves, and the operator's test of a final candidate.

## Objective and boundaries

Maximize sustainable successful invocations and useful parallel work on the
resources available to each installation, across Node, Python, and multiple
functions. Discover capacity automatically; require no worker-count, queue-size,
or concurrency tuning during normal installation. Concurrency 100 is a test
point, not an acceptance target or product limit.

Finite memory, CPU, file descriptors, deadlines, and security constraints require
bounds. Replace arbitrary capacity ceilings with resource-derived budgets and
measured feedback. Keep protocol/body limits and explicitly configured function
limits. Overload must produce a prompt, attributable response and recover without
a restart. Additional queued requests alone do not constitute an improvement.

Scope: single-node Docker and bare-metal/VM deployments, both supported runtimes,
all invocation entry points, scheduler, sandbox lifecycle, persistence, telemetry,
installation/upgrade, and documentation. Kubernetes, distributed scheduling,
external brokers, and a database replacement are outside this project.

## Evidence and uncertainties

The operator's public-URL tests on the 2-vCPU/~4-GiB VM produced:

| Clients | Requests | HTTP 200 | HTTP 429 | Client errors | Total req/s | p99 |
|---:|---:|---:|---:|---:|---:|---:|
| 10 | 1,000 | 1,000 | 0 | 0 | 537 | 72.5 ms |
| 50 | 5,000 | 5,000 | 0 | 0 | 706 | 260.5 ms |
| 100 | 10,000 | 10,000 | 0 | 0 | 729 | 362.0 ms |
| 500 | 20,000 | 19,117 | 869 | 14 | 477 | 5.18 s |

At 500 clients, successful throughput was about 456 req/s. The host recovered,
but neither the saturation resource nor the cause of the 14 client errors was
measured during that run. Post-test free memory is not evidence of peak headroom.
Aggregate p99 includes different response classes; collect success and rejection
latencies separately in the new harness.

Code and read-only instance inspection establish the following:

| Finding | Evidence | Implication |
|---|---|---|
| Several independent capacity ceilings | `pool/pool.go`: 256 pending/function, 1,024 global, 2-second wait; `server/server.go`: default 50 workers/function; `config/defaults.go`: host concurrency `max(200, NumCPU*64)` | One hardware-aware controller must replace conflicting defaults. Preserve deliberate operator limits. |
| CPU sizing is heuristic | `pool/hostmem.go`: eight nominal worker slots per CPU, divided by declared worker CPU | Declared CPU caps do not measure actual CPU consumption or I/O wait. |
| Resource discovery assumes cgroup mount-root files | `pool/hostmem.go` reads `/sys/fs/cgroup/{cpu.max,memory.max,memory.current}` | Nested systemd/cgroup limits and ancestor constraints can be missed. |
| Production resource enforcement is degraded | Health: `rlimit_only`; service `Delegate=yes`; controllers available but `cgroup.subtree_control` empty | Establish usable delegation before using aggressive density or claiming hard resource isolation. |
| Missing cgroups also remove adaptive memory samples | `function_pool.go:release` samples only when the worker has a cgroup path | The reported effective maximum of 31 is not proof of the hardware's actual worker capacity. |
| Memory accounting can be overly conservative | `hostmem.go:availableForWorkers` subtracts all reservations from physical available memory, including reservations for already-resident workers | Model live resident memory and outstanding future growth separately; avoid double-counting while preserving headroom. |
| Admission happens after expensive preparation | `proxy/proxy.go:Forward` reads the complete body, copies/encodes it and performs capture before `Pool.Acquire` | A request-count queue does not bound pre-admission memory or rejected-request work. |
| Scheduler bookkeeping scales with traffic/history | `function_pool.go` retains arrival timestamps, copies rolling samples, sorts under `sigMu`; scaler wake evaluates/sorts all pools | Measure and replace with bounded structures and targeted scheduling. |
| Request-path work is avoidable | Proxy reads two streaming settings through SQL each invocation; invoke handler builds an unused seccomp policy; worker dispatch creates read/write goroutines and channels each request | Cache immutable settings/policy and profile transport allocation costs. |
| Timing samples are inconsistent | Streaming proxy records `DispatchEx` time at first response frame, although worker ownership continues through the stream | Separate time-to-first-byte, handler/worker occupancy, queueing and client response drain. |
| Worker lifecycle can add burst cost | Four concurrent spawns per pool, periodic controller tick, default recycle after 1,000 uses; prewarm counts spawning as current | Track ready separately, bound global spawn pressure and avoid synchronized replenishment. |
| Runtime protocol assumes exclusive ownership | `sandbox/worker.go` holds a worker mutex; both adapters process one frame at a time and mutate process-wide execution/trace environment | Same-interpreter multiplexing would require a new behavioral contract. |
| Python recreates event loops | `runtimes/python/adapter.py` uses `asyncio.run` on async handler/ASGI/stream paths | Evaluate a persistent loop with per-invocation context and cleanup. |
| Persistence can still block the response path | `database/async.go` allows up to 5 seconds to enqueue a critical write; error handling records the execution before writing its response | Queue pressure outside the pool can exceed admission time. This is a hypothesis for long tails, not a proven cause of the 14 errors. |
| Secondary telemetry saturated | Instance health counted 700 dropped telemetry records; no critical writer failures at inspection | Include telemetry/writer pressure in capacity tests and admission accounting. |
| Manual binary replacement is incompatible with old adapters | Candidate waited for readiness frames absent in old rootfs adapters; coordinated `setup` restored successful invocation | Binary/adapter compatibility and rollback must be tested as one upgrade. |

Package paths above are relative to `backend/internal/`; `runtimes/` is under
`backend/`, and `test/performance/` is relative to the repository root.
No production load or configuration change is part of this optimization work.

## Implementation log

- Resource discovery now walks the process's visible cgroup-v2 ancestry for
  `memory.max`, `memory.current`, `cpu.max`, and `cpuset.cpus.effective`. The
  tightest ancestor memory headroom and host physical headroom are used rather than only the leaf's
  `memory.current`; CPU slots respect both quota and the effective CPU set.
  Synthetic nested-cgroup tests cover parent pressure and CPU constraints.
- HTTP and MCP invocation no longer build a seccomp policy per request and
  discard it. Policy construction at worker spawn remains unchanged. Streaming
  settings now have a per-proxy 30-second cache instead of two SQLite reads per
  invocation; a concurrent refresh serves the previous complete setting pair.
- These are bounded preliminary changes. They do **not** establish cgroup
  delegation, dynamic limit reconciliation, or adaptive admission. The
  500-client overload result remains unresolved until a repeatable isolated
  baseline and full safety-boundary work are complete.
- KVM E2E exposed a separate, security-relevant routing bug: startup's generic
  health probe selected the host gateway's different Orva instance as the SDK
  target. Worker credentials were rejected there (401), but internal calls were
  misrouted. Selection now uses only a local interface IP or an explicit
  override, with a regression test. A fresh 2-vCPU/4-GiB KVM guest reported
  `100.96.0.2` (its own address) instead of `100.96.0.1` (the host gateway),
  passed the firewall/SDK case, and passed all 28 source E2E modules (660 checks).
  Focused Go race tests and the full host lint/test gate also passed.
- The first external-client load run through smolvm's forwarded port passed
  5,000/5,000 Node and 5,000/5,000 Python at 100 clients, then saw connection
  resets during mixed traffic. The guest stayed healthy; the same suite run
  over guest loopback passed its mixed, CPU and overload checks (1,000 Node,
  1,000 Python, 2,500 of each mixed, 1,000 CPU, 376 success/124 queue rejection
  for the slow overload case). A repeat on the final binary had 33 retryable
  429s in 2,500 mixed Python requests despite zero transport errors, so the
  existing 100-client mixed acceptance check is **not yet consistently green**.
  The external resets implicate the forwarded path or load rig, but do **not**
  prove that Orva has no external-path bottleneck. An
  independent external generator and direct/routed comparison remain required
  before claiming a throughput gain. Scratch cleanup now retains IDs from the
  moment of creation and retries deletions after transient transport errors.

## Architecture decisions

One resource manager owns the budgets for ingress preparation, pending work,
ready/busy/spawning workers, and completion records. A per-function fair dispatcher
leases execution capacity and a ready worker together. All HTTP, custom routes,
F2F, MCP-mediated invokes, cron, jobs, webhooks and replay paths use that dispatcher.
Management/health and the internal SDK retain progress under invocation saturation.

Keep one active invocation per sandbox worker. Workers remain tied to a function,
code generation, credential set and network policy. Warm reuse across invocations
is retained; cross-function process sharing is prohibited. This preserves current
handler semantics and lets a hard timeout kill only the invocation's worker.

Automatically multiplexing arbitrary async handlers is excluded from this release.
An `async` declaration does not establish concurrency safety: handlers may block,
mutate module state, use process environment, or resist cancellation. Request-local
context helpers do not provide a security boundary. High I/O concurrency in this
plan comes from efficient, resource-budgeted warm processes and dispatch, with
per-worker runtime reuse. Record any remaining measured density ceiling explicitly.

## Implementation sequence

### 1. Establish attribution and reproducible capacity curves

- Extend `test/performance/` with explicit scratch-instance ownership and cleanup,
  structured result files, warm/cold phases, step loads, bursts, and mixed functions.
  Retain the existing functional admission harness as a regression test.
- Add independent arrival-rate tests alongside fixed-client tests. A slow server
  must not silently reduce offered traffic. Record scheduled versus actual sends
  and load-generator saturation; run the generator outside the constrained guest.
- Record ingress/body preparation, admission wait, spawn/ready, IPC, worker busy
  time, first byte, response drain, and persistence-enqueue duration separately.
  Record cancellation location and reason-coded rejection without logging secrets.
- Export aggregate/per-function counters with bounded cardinality, CPU use and
  throttling, RSS/cgroup memory, PSI, FD/PID use, pool/spawn counts and reasons,
  SQLite connection waits/commit time/WAL growth, telemetry drops and request bytes.
- Use local-only CPU, heap, mutex and blocking profiles in scratch runs. Do not
  expose unauthenticated pprof on the public instance.
- Compare direct HTTP, configured reverse proxy/TLS and the public route. Correlate
  request IDs and timing to classify the previous client timeouts before closure.

Deliverable: a baseline report attributing cost and saturation by workload, plus
repeatable scripts and a machine-readable result format. Performance hypotheses
below must be measured against it.

### 2. Correct resource discovery and enforce the safety boundary

- Add one shared resource-envelope implementation based on `/proc/self/cgroup`,
  mountinfo, visible ancestor quotas, effective CPU affinity/cpuset and memory/PID
  limits. Reconcile periodically so quota changes affect capacity without restart.
  Use process/host fallbacks only when limits are genuinely unavailable; expose
  confidence and constraints hidden by namespaces rather than inventing values.
- Initialize only Orva's delegated subtree: separate the daemon leaf from a worker
  subtree, enable available controllers on the appropriate empty parents, and
  verify child control files with a real jailed process. Respect systemd ownership
  and the no-internal-process rule. Do not walk into an unrelated writable ancestor.
- Provision the same contract through supported systemd, OpenRC and Docker paths.
  Check exact CPU/memory/PID enforcement, not merely writable directories. Use an
  aggregate worker memory/PID budget plus individual sandbox limits; keep headroom
  for the daemon, database, uploads, builders and the OS.
- Track resident usage, pending-spawn reservations, growth allowance, daemon memory,
  queue memory and external pressure without double-counting. Use conservative
  declared bounds for unobserved workers; reduce estimates only with reliable
  measurements, safety margin and an enforced aggregate boundary. Historical p95
  alone is not a promise that arbitrary function code will stay small.
- Preserve function-level resource containment when evaluating memory borrowing.
  Parent cgroup OOM is an emergency backstop, not the normal admission algorithm:
  it can select a different worker as a victim. Keep guaranteed budgets reserved;
  reclaim borrowed idle memory and stop new admissions before that boundary. Any
  proposed observed-memory overcommit must pass adversarial simultaneous-growth
  tests and document which guarantees it changes before it can be enabled.
- Where enforcement cannot be established, report degraded capability explicitly,
  retain conservative admission, and reject functions requiring unsupported hard
  limits with a repair instruction. Installation must say which guarantee is
  missing. `--rlimit_as max` is not a memory cap; correct misleading fallback text.
- Preserve namespaces, seccomp, readonly rootfs, per-worker scoped credentials and
  fail-closed egress. Do not grant sandbox code access to the cgroup manager.

Deliverable: one tested view of available resources and enforcement, shared by
the scheduler and health UI. This is required before observed-memory overcommit.

### 3. Remove measured fixed costs on the invocation path

- Replace arrival timestamp lists with fixed time buckets; use bounded rings or
  histograms for samples. Recording must be constant-space and avoid copying the
  entire sample window. Compute quantiles off request-path locks.
- Keep an active/dirty pool set for demand updates; dispatch ready work on events.
  Avoid sorting and rescanning every deployed function for every worker shortage.
- Cache streaming settings with explicit invalidation on mutation and restore.
  Remove unused per-request seccomp construction; policy remains compiled at the
  actual spawn boundary. Preserve immediate security-setting invalidation.
- Profile JSON framing/copies and pipe operations. If material, use bounded buffer
  reuse and worker-lifetime read/write loops with explicit ownership and cancellation.
  Clear retained sensitive data, discard oversized pooled buffers, and preserve
  streaming backpressure. No zero-copy tricks that retain request buffers past use.
- Investigate synchronous access logging and duplicated activity/SSE work. Preserve
  execution/audit semantics; any reduced secondary telemetry needs visible counts,
  a documented policy and tests. Never obtain benchmark wins by silently dropping
  critical records or disabling the security path.

Deliverable: profiles show the targeted allocation/lock/SQL work removed, and
representative throughput/latency improve beyond run-to-run noise.

### 4. Introduce fair, adaptive admission and worker scheduling

- Replace default worker 50, queue 256/1,024 and CPU slot multipliers as capacity
  policy with the shared resource envelope and measured workload demand. Keep
  explicit operator caps as upper bounds. Absence of a pool override means automatic;
  preserve saved overrides and additive migration rules rather than guessing intent.
  Remove channel allocation tied to a universal worker maximum; storage grows lazily
  under byte/worker budgets. Protocol and integer-overflow guards still apply.
- Admission has two stages. Before reading a body, reserve bounded preparation
  memory and metadata capacity. Account known body size and incrementally reserve
  unknown-length bodies, including encoding/capture amplification. Authenticate and
  rate-limit before expensive work where possible, without breaking signed-body
  verification. Slow uploads cannot monopolize ready workers.
- After preparation, enqueue a request descriptor in a per-function FIFO. Schedule
  active function queues using deficit round robin with aging and bounded cost
  estimates. A hot function borrows spare capacity while other eligible functions
  receive service. Do not promise equal requests/sec for unequal-cost functions.
- At dispatch, atomically lease a ready worker, execution capacity and completion
  record capacity. Avoid holding a worker while waiting for another semaphore.
  Worker returns wake dispatch immediately; control-loop sampling is separate.
- Adapt execution concurrency through conservative probes: increase when useful
  demand exists and completed goodput improves without rising CPU throttling,
  memory/I/O pressure or excess latency; decrease under pressure or deteriorating
  goodput. Use per-generation workload baselines, hysteresis and minimum sample
  confidence. Do not compare a naturally slow function with a fast function's RTT.
- Account CPU time separately from wall time so sleeping/I/O workloads may overlap
  while CPU-heavy work avoids excessive runnable processes. Latency feedback is one
  signal, not a complete capacity estimator. Do not force an artificial low-capacity
  measurement window on live traffic just to obtain a baseline.
- Queue budget is constrained by retained bytes and estimated drain time at the
  observed completion rate. Use a separate bounded startup deadline based on ready
  workers, pending spawns and observed startup cost. The execution timeout starts at
  dispatch. Never extend the client's deadline or leave an unlimited queue.
- Cancellation removes queued work promptly and never executes it later. Rejected
  work returns a reason-coded 429 and a retry hint before user code starts. Spawn
  failures, adapter mismatch and handler timeouts retain distinct errors; an adapter
  startup deadline must not be remapped into generic queue overload.
- Integrate F2F dependency progress: callers retain worker memory while waiting;
  child work gets bounded resource-backed progress allowance with chain attribution.
  Detect/reject cycles or unavailable capacity promptly. Never bypass host budgets
  or trust a forged depth header. Cover fan-out and self-invocation explicitly.
- Use the same resource ledger for background jobs, builders and prewarm demand.
  Bound background CPU/PID/memory pressure while retaining their progress.

Deliverable: the host serves the maximum demonstrated useful work within its
resource/latency envelope; cold functions and management remain responsive under
hot-function load, and limits explain their active resource constraint.

### 5. Improve warm-worker lifecycle and runtime efficiency

- Treat spawning, ready, busy and retiring as separate states. Prewarm completion
  requires a ready handshake. Keep demand-triggered startup responsive, but cap
  aggregate spawning from measured CPU/memory pressure across all pools.
- Retain warm workers according to reuse probability, startup cost and memory
  pressure. Reclaim idle capacity fairly; inactive functions should not each pin
  a default process indefinitely. Preserve explicit minimums and report impossible
  aggregate minimums rather than oversubscribing to satisfy them.
- Investigate recycle cost separately. Replace synchronized 1,000-use churn with
  staggered replenishment and measured memory-growth/health signals if benchmarks
  demonstrate a benefit. Keep hard lifetime/health backstops and immediate retirement
  on generation, secret, permission or network-policy changes.
- Python: benchmark a persistent `asyncio.Runner`/event loop for async handlers and
  ASGI, with a fresh invocation context, cancellation/cleanup of leftover tasks,
  async-generator shutdown, executor accounting and correct streaming. Sync handlers
  retain their direct path. Test clients bound to an event loop across invocations.
- Node: preserve module/connection reuse, bound stream buffers and pipe queues, and
  remove demonstrated adapter overhead. Do not introduce threads per request or
  treat `node:vm`/worker threads as a replacement security boundary.
- Separate full worker occupancy from first-byte and network drain time when
  feeding scaling. Slow clients and streams must stay memory-bounded and visible.

Deliverable: reduced cold-start/churn cost and lower CPU/allocation overhead without
changing supported handler behavior, per-invocation attribution or timeout isolation.

### 6. Make persistence pressure part of capacity management

- Preserve SQLite's serialized batched writer. Measure statement preparation,
  transaction sizes, commit latency, read-connection waits and checkpoint stalls.
  Tune bounded batch size/time from measurements; keep critical work ahead of
  optional telemetry and avoid unbounded batches or a new writer per request.
- Reserve critical completion-record space before execution. On storage pressure,
  reduce admissions before running side-effecting code; do not return a retryable
  pre-execution error after a function already ran. Completion uses its reservation,
  not a new independent five-second enqueue wait. Expose commit failures explicitly.
- Define acknowledged-in-memory versus persisted execution records accurately.
  Preserve current durability behavior; exactly-once execution and power-loss-proof
  logging are not claimed. Keep cancellation, shutdown drain and full-disk behavior
  explicit. Audit byte reservation atomics and in-flight batch accounting.
- Keep request capture and secondary logs bounded by bytes. Expose any shedding
  in health/metrics and evaluate ordinary-load telemetry loss as a regression.

Deliverable: database pressure degrades admission predictably rather than creating
unattributed invocation tails; existing critical-write durability tests remain green.

### 7. Upgrade compatibility, documentation and release

- Version the adapter protocol/readiness capability. Detect stale or incompatible
  adapters before declaring invocation readiness, with an actionable error.
- Package server + adapters/SDK as one compatible installation generation. Stage,
  validate and switch them while stopped; retain a complete rollback generation.
  Docker with an existing volume and bare-metal upgrades must both refresh adapters.
  Use `CGO_ENABLED=0` for deployable binaries and preserve build identity fields.
- Add upgrade notes explaining that raw binary replacement requires the matching
  setup step until the coordinated updater is available. Never silently leave the
  web UI healthy while every function waits for an unsupported readiness frame.
- Update `CAPACITY`, `ARCHITECTURE`, `SECURITY`, `OPERATIONS`, `DEPLOYMENT`, `SUPPORT`,
  `TESTING`, `ERRORS`, API/reference as affected, subsystem instructions and changelog
  with each code change. Update dashboard metrics/help, rendered docs, AI prompts if
  handler claims change, embedded copies, and affected website-branch content.
- Review and verify each implementation slice locally; run full isolated VM/Docker
  acceptance before pushing. Review PR, wait for main-commit verification, deploy a
  rollback-ready candidate for the operator's tests, then publish under CONTRACT.md.
  The existing unpublished date tag must not be reused for different code without
  explicit tag handling; prefer a new valid dated tag if the implementation lands
  later. No old release/tag pruning before released-artifact CI succeeds.

## Validation and completion criteria

Hardware profiles: 1 vCPU/1 GiB, 2/4, 4/8 and 8/16 where the host can actually
provide them; additionally nested cgroups, fractional CPU quota, reduced cpuset,
live quota changes and a constrained-memory Docker deployment. Use smolvm for
repeatable guests, and a fully delegated systemd guest for enforcement tests.
An emulated or undelegated setup cannot stand in for hard-limit validation.

| Workload | Required observations |
|---|---|
| Node/Python/TypeScript trivial response, warm and cold | Adapter/IPC floor, successful req/s, latency by stage, startup cost |
| CPU-bound, sync I/O and async I/O | CPU versus wait behavior, scaling and context-switch cost |
| 1, 10 and 100 functions; one hot plus cold/low-rate functions | Per-function progress, aggregate goodput, idle-memory efficiency |
| Streams, slow readers, slow uploads, large/chunked bodies | Bounded buffers and admission memory; management responsiveness |
| Cron/jobs/webhooks/MCP/replay mixed with HTTP; F2F fan-out/cycles | Shared accounting, fairness, deadline and dependency behavior |
| Memory growth, PID explosion, CPU loop, crash, malformed frames, log flood | Enforced containment, reason-coded failure, no cross-function data access, bounded noisy-neighbor disruption |
| Deploy/rollback/secret rotation/egress revocation during load | Correct generation, credentials and network policy after transition |
| SQLite contention/checkpoints/full disk, telemetry overload | No silent critical loss, prompt backpressure, bounded memory |
| Warm restart, old-adapter upgrade, failed install and rollback | Real invocation readiness and complete restoration |

For repeatable performance comparisons, warm up for 60 seconds, measure for 120
seconds and repeat at least three times with baseline/candidate order alternated.
Run both client-count sweeps (including 10/50/100/250/500/1,000) and offered-rate
sweeps around measured sustainable goodput. These are experimental points, not
product limits. Include at least a 30-minute mixed-workload soak, burst/recovery
cycles, and a burst after idle-scale-down. Keep dataset, network, logging/capture,
resource enforcement, runtime assets and storage identical across comparisons.

Required gates:

1. Demonstrated goodput or latency improvement beyond measurement noise in the
   targeted workloads; no unexplained repeatable >10% regression in another workload
   at the same offered load. Publish the curves and resource cost, not a universal
   concurrency/RPS promise. Acceptance constants are test tolerances, not runtime caps.
2. No platform-caused 5xx/client timeouts below measured sustainable capacity on the
   controlled direct path. Classify every public-path timeout separately. Above
   capacity, memory/FD/PID use stays bounded, rejection is attributable and timely,
   and lowering load restores baseline performance without intervention.
3. Deterministic fairness tests prove progress for eligible function queues within
   a scheduler round under a fake resource model; real tests demonstrate low-rate
   traffic and control-plane progress during a hot-function burst. Do not promise
   progress where physical memory cannot fit even one worker.
4. Unit/race tests cover budget conservation, exactly-once lease release, cancel vs
   dispatch races, stale generations, failed spawn recovery, shutdown, fake-clock
   controller stability, missing/stale metrics and all cgroup-discovery layouts.
5. Security tests prove enforcement and cross-function separation under saturation;
   no weaker seccomp/namespace/egress/auth setting is allowed to explain a speedup.
6. Real sandbox source E2E, Docker persistence/restart, root/non-root installation,
   supported installer matrix, both architectures and coordinated upgrade/rollback
   pass. Unit tests alone do not qualify the change for release.
7. Critical completion accounting reconciles after drain. Ordinary-load telemetry
   loss is zero; overload shedding is explicit. No accumulating workers, goroutines,
   credentials, cgroups, FDs or retained request buffers after repeated bursts.

CI should keep deterministic scheduler/resource/race/compatibility tests and a
bounded real-sandbox concurrency/fairness smoke within `ci.yml`. Large comparative
benchmarks remain reproducible isolated qualification runs; hosted-runner RPS noise
must not create brittle universal throughput assertions. No extra workflow is needed.

This project is complete when these gates pass and measured capacity curves explain
the remaining hardware/runtime/storage limits. It establishes a durable optimization
and regression framework; it cannot establish that no future workload will reveal
another optimization opportunity.

## Research supporting the design

- [Linux cgroup v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html):
  hierarchical CPU/memory/PID control informs effective-resource discovery and
  enforcement. Visible leaf settings alone need not describe the effective budget.
- [systemd delegation](https://systemd.io/CGROUP_DELEGATION/): `Delegate=yes` makes
  controllers available but does not enable them; subtree ownership and the
  no-internal-process rule explain the production setup gap and proposed layout.
- [Linux PSI](https://www.kernel.org/doc/html/latest/accounting/psi.html): contention
  and stall time provide resource-pressure signals beyond current free memory.
- [Envoy adaptive concurrency](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/adaptive_concurrency_filter):
  feedback from latency can adjust concurrency. Its documented calibration effects
  and control-scope requirements argue for workload-specific signals and unified
  admission rather than copying its defaults verbatim.
- [Node 24 worker threads](https://nodejs.org/docs/latest-v24.x/api/worker_threads.html):
  threads target CPU parallelism; asynchronous I/O does not benefit from adding a
  thread per operation. This supports measuring runtime overhead before adding threads.
- [Node async context](https://nodejs.org/api/async_context.html) and
  [Python contextvars](https://docs.python.org/3.14/library/contextvars.html):
  request-local context tools help attribution, but do not isolate arbitrary global
  state or make concurrent user handlers safe automatically.
- [Python 3.14 runners](https://docs.python.org/3.14/library/asyncio-runner.html):
  `asyncio.Runner` supports repeated calls on one loop; its lifecycle informs the
  proposed reuse experiment and cleanup tests.
- [SQLite WAL](https://sqlite.org/wal.html): concurrent readers coexist with a
  serialized writer; checkpoint behavior supports batching and explicit storage
  pressure measurement instead of adding write connections.
- [k6 open and closed workload models](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/open-vs-closed/):
  closed-loop clients reduce offered traffic as latency rises, motivating independent
  arrival-rate tests alongside the operator's fixed-concurrency `hey` measurements.

External sources support the mechanisms and tradeoffs. The implementation choices
and expected benefits above are Orva-specific proposals, not benchmark guarantees
derived from those sources.
