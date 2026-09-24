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
| Several independent capacity ceilings (baseline) | `pool/pool.go` previously had 256 pending/function, 1,024 global, 2-second wait; `server/server.go`: default 50 workers/function; `config/defaults.go`: host concurrency `max(200, NumCPU*64)` | Pending count and the default pool maximum now derive from resources; the host execution limiter and writer queues still need reconciliation. Preserve deliberate operator limits. |
| CPU sizing is heuristic | `pool/hostmem.go`: eight nominal worker slots per CPU, divided by declared worker CPU | Declared CPU caps do not measure actual CPU consumption or I/O wait. |
| Resource discovery assumes cgroup mount-root files | `pool/hostmem.go` reads `/sys/fs/cgroup/{cpu.max,memory.max,memory.current}` | Nested systemd/cgroup limits and ancestor constraints can be missed. |
| Production resource enforcement is degraded | Health: `rlimit_only`; service `Delegate=yes`; controllers available but `cgroup.subtree_control` empty | Establish usable delegation before using aggressive density or claiming hard resource isolation. |
| Missing cgroups also remove adaptive memory samples | `function_pool.go:release` samples only when the worker has a cgroup path | The reported effective maximum of 31 is not proof of the hardware's actual worker capacity. |
| Memory accounting can be overly conservative | `hostmem.go:availableForWorkers` subtracts all reservations from physical available memory, including reservations for already-resident workers | Model live resident memory and outstanding future growth separately; avoid double-counting while preserving headroom. |
| Admission happened after expensive preparation (baseline) | `proxy/proxy.go:Forward` read the complete body, copied/encoded it and captured before `Pool.Acquire` | Candidate now reserves daemon body memory before read and captures only admitted requests; it still serializes before worker acquisition. |
| Scheduler bookkeeping scales with traffic/history | `function_pool.go` retains arrival timestamps, copies rolling samples, sorts under `sigMu`; scaler wake evaluates/sorts all pools | Measure and replace with bounded structures and targeted scheduling. |
| Request-path work is avoidable | Proxy reads two streaming settings through SQL each invocation; invoke handler builds an unused seccomp policy; worker dispatch creates read/write goroutines and channels each request | Cache immutable settings/policy and profile transport allocation costs. |
| Timing samples were inconsistent (addressed in current candidate) | Streaming proxy recorded `DispatchEx` time at first response frame, although worker ownership continued through the stream | Worker lease is now measured centrally on release; queueing and end-to-end HTTP latency remain separate signals. |
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

- The pool's full latency sample slices allocated and copied 512 durations
  for every completed warm invocation. Bounded overwrite rings now retain the
  same newest sample windows, and snapshot percentile sorting runs outside the
  signal lock. A local full-ring benchmark measured 16.9–18.8 ns/sample with
  zero allocations; focused pool race tests pass. The candidate passed all
  29 real-sandbox E2E modules (676 checks) and a 5,000/5,000 mixed
  Node/Python direct-link 2-vCPU/2.5-GiB VM run with exact execution-row
  reconciliation and zero critical writer failures/timeouts. A controlled
  end-to-end A/B remains outstanding; shared-host state makes the observed
  191/s versus an earlier 106/s an invalid causal comparison.

- A real-schema writer benchmark corrected the misleading one-column grouped
  INSERT result. The production 18-column execution row with foreign key and
  indexes took 17.4–21.9 ms for one 200-row grouped statement versus 16.0–16.5
  ms for prepared per-row inserts in three local repetitions. Splitting the
  same transaction into four 50-row statements took 9.6–10.7 ms. The candidate
  now bounds grouped SQL to 850 bind values, retaining the 200-job transaction
  and permitting denser groups for narrow rows. End-to-end VM A/B and telemetry
  loss remain open; this benchmark is not a capacity claim. The candidate
  passed 29/29 real-sandbox E2E modules (676 checks), and a direct-link
  2-vCPU/2.5-GiB VM run returned 5,000/5,000 mixed Node/Python HTTP 200 with
  exactly 5,000 new execution rows after drain and zero critical writer
  failures/timeouts. That VM run is correctness evidence, not an A/B gain.

- The current candidate replaces the default 50-worker and universal
  1,024-worker pool caps with a fixed idle-channel ceiling derived from the
  discovered CPU slots, each function's full per-worker memory.max budget
  (minimum 16 MiB), and function
  concurrency. A positive saved `max_warm` still lowers that ceiling;
  `max_warm=0` means automatic. Unit tests cover a synthetic host whose safe
  ceiling exceeds 1,024 and the REST validation contract. This removes a
  code-level cap, not the storage bottleneck seen in the 2.5-GiB scratch VM;
  no larger-host throughput gain is claimed yet. The host execution limiter,
  writer admission, and fair dispatcher remain open work.

- A safety audit found that using recent `memory.current` p95 as a worker
  reservation allowed several quiet workers to later grow together to their
  much larger `memory.max` limits, exceeding the intended 80% aggregate
  worker budget. The candidate now reserves the full hard per-worker limit,
  sizes the idle channel from that same bound, and removes the per-request
  cgroup-memory read and sample sort that only fed unsafe admission. This is
  a containment correction, not a throughput claim. An observed-memory
  borrowing policy remains excluded until an aggregate enforced boundary
  and simultaneous-growth tests exist.
  The candidate passed 29/29 Docker sandbox E2E modules (676 checks). In the
  isolated 2-vCPU/4-GiB VM, 32 live worker cgroups summed to 3 GiB of hard
  memory limits under the approximately 3.2-GiB worker budget, and the
  1,000/100 and 5,000/250 mixed checks returned and persisted every execution.
  An alternating 5,000/250 previous–candidate–previous run yielded
  286/422/388 accepted requests per second; the baseline's movement rules
  out a causal throughput claim. Optional writer drops remained near 5,000
  per phase, so writer-aware admission is still required.
  The subsequent 50,000-request/1,000-client candidate phase returned
  49,114 HTTP 200 and 886 pre-execution HTTP 429, with exactly 49,114
  execution rows after drain. Pool rejections/timeouts were zero, while the
  critical writer queue peaked at 987/1,024, four critical enqueue timeouts
  accumulated, and 72,374 optional records dropped. No worker cgroup OOM
  occurred. This fails the large-load gate and keeps the PR draft; neither
  the memory fix nor the earlier short runs resolve SQLite pressure.

- A separate-connection PASSIVE WAL-checkpoint experiment was reverted.
  SQLite's automatic checkpoint can stall the committing writer; this
  candidate ran a background checkpoint above an 8-MiB WAL-size threshold
  while leaving the existing 10,000-page automatic checkpoint as fallback.
  Its unit/database tests passed. In the 2-vCPU/4-GiB server plus separate
  client VM, the 50,000-request/1,000-client mixed run returned 47,813 HTTP
  200 and 2,187 pre-execution storage 429 at 326 attempted/s, versus the
  preceding baseline's 50,000 HTTP 200 at 431/s. All 47,813 accepted responses
  had execution rows after drain, but the writer recorded one other critical
  enqueue timeout. The checkpoint competed for the same storage I/O and did
  not improve this workload. It was removed from the source. A future attempt
  needs I/O latency and checkpoint-duration telemetry and a disk-safe
  policy, not another unmeasured checkpoint schedule.

- Execution-index pruning also failed the VM acceptance run. Three old
  execution indexes occupied about 113 MiB of the scratch database and
  duplicate left-prefixes or have no matching Orva query. A migration test
  confirmed surviving composite-index query plans. The immediately preceding
  50,000-request/1,000-client mixed baseline returned 50,000/50,000 HTTP 200
  at 431/s and exactly 50,000 execution rows. After the index migration,
  49,402 were HTTP 200 and 598 were pre-execution storage 429 at 374 attempted/s.
  No critical write or transport failure occurred. Database growth, freed-page
  layout, and host variability prevent causal attribution of the full delta,
  but this does not meet the release gate. The migration and test were
  reverted; profile SQLite wait, page-cache behavior, and actual end-to-end
  A/B with restored database snapshots before trying another index change.

- A worker-retention experiment removed the immediate dynamic-cap prune in
  `functionPool.release`, relying on the controller's scale-down grace and
  demand-driven idle reclaim. It reduced observed spawn/kill churn, but the
  same 50,000-request/1,000-client mixed VM run returned 49,973 HTTP 200 and
  27 pre-execution storage 429s at 357 attempted/s, versus 50,000/50,000 at
  438/s on the preceding unchanged baseline run. The database grew across
  phases, so this is not a controlled throughput effect size; it nevertheless
  fails the zero-error acceptance condition. The code and unit test were
  reverted. Worker churn alone is a misleading target: resource reservations
  and shared writer headroom must be evaluated together.

- A central, per-function FIFO round-robin dispatcher was tested and
  **reverted**. It paired a ready worker, host execution slot, function
  concurrency slot, and SQLite completion lease without holding one while
  waiting for the others. Unit and race tests passed, including a full-writer
  test showing a ready worker remained idle. But a 50,000-request/1,000-client
  mixed Node/Python run on the dedicated 2-vCPU/4-GiB server VM returned
  47,478 HTTP 200 and 2,522 Python client timeouts at 373 attempted/s.
  A Python-only 10,000-request run on that binary passed at 614/s. The
  unchanged committed baseline, rebuilt and run on the same VMs and test
  functions, returned 50,000/50,000 HTTP 200 at 438/s with no transport
  errors. Do not ship the dispatcher. Its extra five-second queue allowance
  could exceed a typical client deadline, and its mixed-workload slowdown
  needs profiling before another scheduling change. The current branch
  remains on early completion reservation.

- A discarded dispatch-boundary prototype moved HTTP/MCP completion-slot
  reservation from request entry to just after worker acquisition. The first
  version rejected immediately when no writer slot was free: on the same
  2-vCPU/4-GiB server and separate client VMs, a 50,000-request/1,000-client
  mixed run returned only 1,499 HTTP 200 and 48,501 pre-execution
  `STORAGE_BACKPRESSURE` 429 responses. Waiting up to five seconds while
  holding the ready worker produced 49,983 HTTP 200 and 17
  `INVOCATION_QUEUE_FULL` 429 responses at 408 attempted requests/s, with
  HTTP 200 p99 5.83 s. Its 49,983 successes reconciled to exactly 49,983
  new execution rows after drain; critical failures/timeouts stayed zero,
  but 17,116 activity records dropped. The prior committed candidate had
  delivered 50,000/50,000 HTTP 200 in a comparable but not controlled run.
  The prototype was reverted: moving one semaphore later without a shared
  dispatcher either creates a storage-rejection storm or parks workers and
  shifts pressure to the function queue. A single fair dispatcher must pair
  ready-worker and completion capacity without holding either while waiting.

- A second discarded prototype made each waiting HTTP/MCP request observe
  writer-slot releases, acquire a worker, try the completion slot, and return
  the worker on a collision. A deterministic unit test proved that a full
  writer no longer occupied an idle worker, but the same two-VM
  50,000-request/1,000-client mixed run returned only 39,490 HTTP 200 and
  10,510 pre-execution `STORAGE_BACKPRESSURE` 429 responses. It attempted
  267 requests/s; HTTP 200 p99 was 5.46 s. After writer drain, all 39,490
  successes reconciled to execution rows, critical failures/timeouts stayed
  zero, and 26,297 activity records had dropped. The per-request pairing
  loop was reverted. This is evidence that not holding a resource is
  insufficient: wakeups also need bounded scheduling, per-function ordering,
  and admission based on drain time rather than independent five-second
  races. The measured result does not isolate the exact CPU/lock contribution
  of wakeups; profile a real dispatcher before claiming that cause.

- A new direct-link 2-vCPU/4-GiB server plus 1-vCPU/512-MiB client sweep
  returned all HTTP 200 at 10, 50, 100, 500 and 1,000 clients, including
  50,000/50,000 at 1,000. However, post-drain SQLite row counts proved 754
  accepted invocations had no execution row, matching 754 critical enqueue
  timeouts. At 500 clients, 497 goroutines were blocked in the five-second
  post-execution enqueue; a CPU profile attributed about 41% of sampled daemon
  CPU to the serialized SQLite writer. Activity and optional telemetry also
  shed records. The current in-progress slice reserves completion capacity
  before public HTTP execution, returning attributable storage backpressure
  rather than running user code and losing its final row. Four successive
  50,000-request, 1,000-client mixed Node/Python VM runs showed exact
  accepted-response-to-execution-row reconciliation: 42,465/42,465, then
  46,085/46,085 with critical-first draining, 49,299/49,299 after increasing
  writer batches to 200, and 50,000/50,000 with a five-second pre-execution
  wait. The final run had zero transport errors, critical writer failures, or
  critical timeouts, but only 462 successful requests/s and 4,030 ms HTTP
  response p99. Activity and optional capture still shed under pressure.
  Admission still needs extension to all invocation entry points. The sweep
  is exploratory, not a controlled A/B performance result.

- A separate large-history read bottleneck was found on the same 1.47-million
  execution scratch database: sorting `status=success` history for a 50-row
  page took about seven seconds through the Orva HTTP API. A bounded exact
  fast path now reads the newest unfiltered page and uses it only when all
  rows are successful; a mixed page falls back to the original status-index
  query. A same-VM baseline/candidate comparison gave 7.07–7.16 s versus
  0.193–0.219 s for three warm reads, with identical totals and page-ID
  hashes. Rare-error lookup still uses the original path. This improves
  dashboard read responsiveness; it is not evidence of higher invocation
  goodput or a reason to remove an index.

- The reservation now covers inbound webhooks, replay, internal SDK calls,
  MCP tools, cron, and queued jobs as well as public HTTP. Jobs reserve before
  `ClaimDueJobs` so a full writer cannot consume a retry attempt. The final
  binary passed all 29 real-sandbox VM E2E modules and a further mixed
  50,000-request/1,000-client phase with 50,000 HTTP 200 responses, zero
  transport errors, 566 successful requests/s, 3,500 ms HTTP 200 p99, and
  exactly 50,000 new execution rows. These are exploratory results; optional
  activity/capture shedding and fixed completion-slot capacity remain gaps.
  An open-loop 650/s follow-up was invalid as a server-capacity measurement:
  the 1-vCPU client VM marked 3,958 of 20,000 arrivals unsent. A 400/s phase
  delivered all 10,000 and reconciled 10,000 rows, but its 340/s completion
  rate and 4,323 ms HTTP 200 p99 show persistent queueing. Do not claim the
  closed-loop 566/s observation as sustainable offered-load capacity.

- A grouped final-execution INSERT prototype, limited to adjacent jobs with
  the same explicit SQL, halved a synthetic one-column, 200-row SQLite
  writer-only benchmark
  (583–588 µs to 290–291 µs; 2,024 to 44 allocations). A 50,000-request
  VM run maintained exact row reconciliation and zero HTTP errors, but was
  slower end-to-end (372 successful requests/s) during substantial unrelated
  host CPU load. Keep this as a writer-path optimization hypothesis; use
  alternating A/B on an otherwise idle host before claiming overall gain.
  Grouping activity rows alone did not fix activity loss: with the earlier
  one-batch critical-priority trigger, a 50,000-request phase shed 45,840
  activity rows while still returning/persisting all 50,000 executions. The
  priority trigger is now a critical-queue three-quarter high-water mark. A
  further 50,000-request VM phase kept all 50,000 execution rows and reduced
  activity drops to 16,912, but completed at 448 successful requests/s and
  4,218 ms HTTP 200 p99. One-third of activity history still shed at this
  offered load; better observability remains a separate design problem.

- Function deletion now fences async writer commits and discards tagged
  execution jobs that arrive after the function is gone, instead of recording
  expected foreign-key failures as storage failures. Parentless capture,
  span, and structured-log rows carry function ownership for cleanup. Rapid
  firewall-policy edits exposed an independent race: runtime generation GC
  could delete a config path captured by a worker before nsjail opened it.
  Policy files now remain through the process lifetime and are pruned only
  before workers spawn on daemon startup. Focused Go and race tests pass.
  A fresh 2-vCPU/2-GiB Ubuntu smolvm guest using virtio-net passed all
  29 real-sandbox E2E modules (676 checks, no skips), including jailed npm/pip
  installs, firewall enforcement, and delete-during-invocation. Writer health
  showed zero critical failures/timeouts after E2E. Bounded guest-loopback
  load returned 10,000/10,000 HTTP 200 across Node/Python at 100 clients,
  5,000/5,000 in a mixed phase, 1,000/1,000 for CPU-bound work, and bounded
  224 HTTP 200 plus 276 HTTP 429 in an intentionally slow overload phase;
  there were no client transport errors or 504s. Pre-cleanup writer deltas
  were zero for critical failures, telemetry/activity drops, and unexpected
  deleted-function writes. This does not replace the outstanding independent
  2-vCPU/4-GiB capacity and security-boundary qualification.

- A bounded Go load generator now supports direct-VM mixed-function tests in
  closed-loop and scheduled open-loop modes. It reports offered versus sent
  traffic, transport failures, Orva error codes, and class-specific/per-function latency as
  JSON. Race-tested synthetic-server cases cover response-class attribution,
  mixed URLs, and generator saturation. On a direct two-VM scratch test with
  the current candidate, an initially cold 10,000-request/100-client mixed
  Node/Python run returned 9,846 HTTP 200 and 154 Python
  `INVOCATION_QUEUE_FULL` responses; a warm repeat returned 9,949/51, and a
  later warm repeat returned 10,000/0. Live metrics during a scheduled
  800-request/s phase showed Python with one busy worker, four spawning and
  469 queued, while the startup p95 was several seconds and queue admission
  expired at two seconds. This identifies cold-ramp time as a distinct
  bottleneck; zero telemetry drops in the initial mixed run rule out the
  SQLite writer as the cause of those first 429s. A 20,000-request,
  1,000-client scheduled phase sent all arrivals but returned 16,276 200 and
  3,724 queue 429, with no transport errors. These runs are exploratory and
  sequential, not a controlled baseline/candidate comparison.
- A later 3,000-client scheduled phase **invalidated its own capacity result**:
  the host kernel globally OOM-killed the 4-GiB server VM at 10:32:21 UTC
  (guest process RSS about 3.0 GiB). The host has 7.8 GiB and no swap, with
  other development processes active. The test guest did not survive, so no
  Orva throughput conclusion may be drawn from that phase. Stop high-client
  stress until the host has enough proven headroom or a dedicated target is
  available; preserve this failure as a load-rig safety lesson.
- The next candidate aligns cold admission with adapter startup: a pool with
  unused capacity or in-flight spawns waits for the ten-second readiness budget
  plus one scaler tick; a saturated pool keeps the two-second overload wait.
  This remains bounded by the existing resource-derived ingress and pending
  budgets, and the function execution timeout still begins only after worker
  acquisition. Unit/race tests cover both paths and a worker arriving after
  the former two-second cutoff. On a smaller 2-vCPU/2-GiB VM, the changed
  binary passed 28 real-sandbox E2E modules (660 checks, zero failures or
  skips), and a Python handler sleeping three seconds during module import
  returned its first HTTP 200 in 4.57 seconds. Because host OOM ended the
  4-GiB stress guest, no 4-GiB throughput claim is attached to this change.

- The controller's service-time signal was dispatch-only even though a worker
  remained busy through proxy response processing or streaming. All invocation
  paths now sample the full successful-acquire-to-release lease centrally in
  `Manager.Release`; failed queue admission has no service sample. This fixes
  an undersized Little's-Law demand input, but it does not raise the host's
  CPU/memory ceilings or prove a throughput gain without a safe A/B VM run.
  The changed binary passed the full 28-module/668-check real-sandbox E2E
  suite on a disposable 2-vCPU/2-GiB VM (zero failures/skips). Its nsjail
  capability fallback worked, but cgroup controllers were not delegated;
  this is a functional result, not a hard-limit or throughput result.
- The async writer's byte limits used a load-then-send-then-add sequence.
  Concurrent producers could exceed the cap, and a consumer receiving before
  the add could drive the reported count negative. The current candidate
  reserves bytes with compare-and-swap before publishing, rolls them back on
  timeout or telemetry shedding, and tests concurrent budget adherence. This
  closes a memory-safety hole; it is not a claim that telemetry throughput
  has improved.
- Writer shutdown previously let a select choose the send branch after the
  consumer had chosen its stop branch, silently stranding a critical job.
  The current candidate closes a producer stop signal first, fences in-flight
  publishers, and only then tells the consumer to drain. Concurrent-shutdown
  race tests verify every accepted critical write is applied or sent through
  the direct-write fallback.
- A bounded 10,000-invocation Node/Python load pass on the 2-vCPU/2-GiB
  scratch guest returned 10,000 HTTP 200 and no client errors, but health
  after immediate harness cleanup showed 883 telemetry drops and 34 critical
  failures. Repeating with a writer-drain wait **before** function deletion
  returned another 10,000/10,000 HTTP 200, zero *new* critical failures before
  cleanup, and 1,029 new telemetry drops; the critical-failure total stayed
  at 34 after deletion. This attributes the first run's critical failures to
  cleanup racing accepted execution inserts, while confirming that even this
  100-client load still sheds secondary records. Review found an independent
  definite data-loss bug: transient commit retries could alias the original
  batch, which the writer cleared before appending the retry. The current
  candidate copies retries before clearing and holds byte reservations through
  in-flight/retry work. Tests pin the alias and stalled-batch accounting cases.
  The changed binary then passed 28/28 real-sandbox E2E modules (668 checks)
  and three consecutive 10,000-request, 100-client Node/Python scratch runs
  with all HTTP 200. Pre-cleanup critical-failure deltas were 0/0/0 and
  telemetry-drop deltas were 0/113/0. A further harness correction waits
  for `active_requests=0` before writer drain, because the client can read
  the response before final execution-row enqueue; two runs after that
  correction added no cleanup failures. The middle run missed 55/10,200
  expected function activity rows, so optional-write prioritization remains
  architectural work, not a completed optimization.

- A direct **inter-VM** link now supplies an independent load generator, so
  smolvm's host port-forward resets are outside the measured request path.
  On a fresh 2-vCPU/4-GiB server running `d5a1fd6`, a Python `"ok"` handler
  returned 5,000/5,000 HTTP 200 at 100 clients (1,079 req/s), then 13,004
  HTTP 200 and 6,996 HTTP 429 at 500 clients (20,000 requests, 1,673 total
  responses/s). A later 50,000-request/1,000-client run returned 31,385 HTTP
  200 and 18,615 HTTP 429 at 1,366 total responses/s. No client transport
  errors were reported. After the first 500-client phase, dropped telemetry
  had risen to 19,358, while critical failures/timeouts stayed zero. This
  validates the direct network rig and establishes an exploratory baseline;
  the growing database and warm state still preclude a controlled A/B claim.
- The next candidate replaces fixed pending counts with a memory/FD-derived
  budget and reserves body memory before public HTTP reads (including a
  conservative unknown-length charge). It defers replay capture until worker
  acquisition. Unit tests cover resource scaling, per-function headroom,
  pressure/recovery and charge accounting. Its two-VM stress result is still
  measured on the two-VM link: at 500 clients it returned 19,057 HTTP 200
  and 943 HTTP 429 over 25.04 s versus the baseline's 13,004/6,996 over
  11.96 s; at 1,000 clients it returned 48,832/1,168 over 68.70 s versus
  31,385/18,615 over 36.60 s. The candidate accepts much more work but
  delivers fewer *successful requests per second* and higher latency, so it
  is **not a performance win** on its own. The server remained healthy and
  critical writer failures/timeouts were zero; telemetry still dropped.
  This motivates reducing per-invocation IPC and write cost before changing
  admission again. The next slice replaces per-frame stdout reader goroutines
  with one reader per warm worker. On the same candidate VM after that change,
  20,000/20,000 requests at 500 clients completed in 15.11 s (1,323 req/s,
  client p99 484 ms); 50,000/50,000 at 1,000 clients completed in 45.36 s
  (1,102 req/s, client p99 1.53 s), with no 429, 504, or transport errors.
  The database grew between runs, so these are exploratory observations, not
  an isolated causal estimate. During the 1,000-client run, best-effort
  telemetry drops grew by 49,407 while critical write failures/timeouts
  remained zero. SQLite persistence is the remaining measured bottleneck.
  A 50-row insert microbenchmark found multi-row SQL only about 2% faster
  than the current prepared-per-row transaction, so it does not justify
  replacing per-row error isolation with a more complex write shape. The
  current candidate also passed all 28 real-sandbox E2E modules (664 checks,
  no skips) and host `go vet`, Go tests and focused Go race tests. A warm
  Node handler returned 5,000/5,000 HTTP 200 at 100 clients on the direct
  two-VM link. Mixed-function sustained load and telemetry durability remain
  open acceptance work.

- A disposable 2-vCPU/4-GiB smolvm with real nsjail and cgroup-v2 limits
  identified the async SQLite writer and per-arrival pool-controller work as
  prominent CPU costs under a warm Python handler. The writer now prepares
  each SQL text once per batch; HTTP execution baseline fields are inserted
  with the row rather than updated afterward. Arrival history is sixty
  one-second counters and scaler wakeups coalesce within 20 ms. Unit tests
  cover bounded history, batch failure recovery, baseline persistence, and
  wake timing. A 100-client sequential candidate run achieved 28,975/28,975
  HTTP 200 over 30 seconds, but 677 telemetry writes were still dropped.
  Database growth and a guest-local generator make that throughput observation
  exploratory, not a controlled before/after claim. The pre-UI candidate
  passed 28 real-sandbox E2E modules (660 checks). Full final-binary and
  external-client stress validation remains open.
- The final rebuilt candidate again passed all 28 isolated real-sandbox E2E
  modules (664 checks). Guest-loopback load returned 5,000/5,000 success at 100 clients;
  14,715 success/5,285 retryable 429 at 500 clients (20,000 requests); and
  48,423 success/1,577 retryable 429 at 1,000 clients (50,000 requests),
  without 504 or transport errors. The writer drained but telemetry drops
  accumulated to 69,868 across the guest's lifetime. These data identify
  fixed pending-work caps and writer throughput as remaining concerns, not a
  performance victory: the generator shared the two guest CPUs, the database
  grew between runs, and baseline counters were not reset for each phase.
- A host-side generator through smolvm's forwarded port succeeded at 100
  clients (5,000/5,000, 1,260 req/s), but at 500 clients it received only
  5,402 HTTP 200 of 20,000 attempts, with the remainder mostly EOF/reset
  errors. The same guest handled 500 clients on its loopback without
  transport errors. The forwarding path is not yet trustworthy for capacity
  comparisons; obtain a direct routed path before attributing these resets.
- The dashboard's new `response_latency_ms` is a separate rolling ring for
  the complete public invoke handler. It includes rejection and record-enqueue
  time but not network transit; the older `latency_ms` retains its proxy/worker
  meaning. This corrects the previous misleading use of the shorter metric
  as a public response-time figure.

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
  **Implemented in the current candidate:** auto-detection is confined to the
  process's own non-root cgroup, and `orva.daemon` / `orva.workers` are created
  there. A 2-vCPU/4-GiB smolvm guest verified actual nsjail `memory.max`,
  `pids.max`, `cpu.max`, and an `oom_kill` delta for both root and unprivileged
  daemon launches. The unprivileged guest needed the installer's already
  supported `ORVA_DISABLE_USERNS=1` capability fallback because that guest
  denied `/proc/<pid>/setgroups`. A disposable Docker container passed 20/20
  real deploy/invoke/rollback assertions, reported `cgroup_v2`, remained
  healthy with `docker exec` working, and showed a worker-subtree `oom_kill`
  delta of 0→1 under the same probe. An expanded disposable probe then
  exercised actual CPU throttling and PID exhaustion in Docker and the same
  2-vCPU/4-GiB guest: the jailed child's `cpu.stat:nr_throttled` increased by
  10/8 and `pids.events:max` by 33/33, respectively. Native systemd,
  arm64 PID execution, and aggregate resource accounting remain open; this is
  not phase completion.
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
  The rejected prototype above fixes the concrete coordination rule: with the
  scheduler lock held, choose an eligible request and ready worker, then try
  a completion lease; if unavailable, leave both queued/idle and wait for a
  writer-capacity event. Do not turn one failed try into an HTTP 429. On a
  lease, transfer all three ownerships together and dispatch outside the
  lock. Worker returns, writer lease releases, new arrivals, and cancellations
  all wake the dispatcher; after every wake it drains all currently feasible
  pairs before sleeping. Recheck state before sleep so a released lease cannot
  be lost between the check and the notification. A bounded pending-byte and
  drain-time budget, not the writer's channel count, determines rejection.
  Preserve the lease through final-row enqueue/commit and return it exactly
  once on cancellation, worker-start failure, function deletion, and shutdown.
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
  **Current evidence:** a direct-VM 20,000-request/250-client mixed run produced
  19,953 successful responses/rows and 47 pre-execution storage 429s. The
  critical writer spent 85.9 seconds in SQL statements, 9.1 seconds committing,
  and 0.03 seconds acquiring connections; a simultaneous CPU profile attributed
  66% of sampled CPU to the writer call stack, dominated by SQLite B-tree
  insertion/page reads. Nine explicit execution indexes occupied about 1.06 GB
  at 1.47 million rows. This points to a same-snapshot index/write A/B with
  read-query-plan checks, not a blind checkpoint or batch-size change. A
  read-only plan audit confirmed that baseline seeding, function/global
  history and retention use distinct execution indexes; trace ordering and
  status-filtered history still create temporary sort trees. Index drops
  remain excluded from production by the additive-only migration contract.
  The first short backup-copy A/B was invalid: its Python fixture used the
  wrong trace/span ID shapes, and both its apparent index win and a subsequent
  actual-driver/cache win reversed with copy order. Identical-copy controls
  gave 581 ms versus 41 ms per 200-row batch in one order, then 39 ms versus
  721 ms in the other. The slow copy physically read 3–4 MiB per batch while
  the warm copy read almost nothing. The harness now records disk-I/O deltas,
  accepts either copy order, and reconciles actual-driver inserted rows.
  Control filesystem-cache residency and run sustained same-snapshot/HTTP A/B
  before any schema or cache policy proposal; no performance gain from either
  candidate is established. Two unchanged-binary 20,000-request/250-client
  direct-VM phases then returned all 20,000 HTTP 200 with exact execution-row
  reconciliation, but their goodput changed from 309/s to 572/s as physical
  reads fell from about 429 MiB to 120 MiB and major faults from 2,291 to
  84. Critical statement time fell from 55.68 to 26.32 s, while writes stayed
  near 1 GiB and optional telemetry dropped over 27,000 records in each phase.
  This reinforces the storage/cache hypothesis and invalidates one-off RPS
  comparisons; it does not establish a safe index or cache-size optimization.
  A proposed optional-lane scheduling relaxation also failed an alternating
  baseline–candidate–baseline check: 5,000-request/100-client phases had
  5,974/5,367/5,387 total best-effort drops while the unchanged return
  baseline was faster than the candidate. The code was reverted; improving
  telemetry requires measured total write demand and admission, not assuming
  that idle-looking priority selection is spare SQLite capacity.
  An open-loop sweep on the unchanged 2-vCPU/4-GiB direct-VM setup then
  showed why the controller must use live feedback: two 10,000-request
  400/s phases each returned and persisted all 10,000 executions, but one
  lost 1,506 optional records and the other lost none. A 300/s phase had no
  loss; a 500/s phase returned/persisted all 10,000 while shedding 172
  activity and 3,105 total best-effort records. These are exploratory
  sequential phases on a growing database, not a fixed sustainable-rate
  curve. One separate 500/s phase ended when the foreground VM command's
  per-request-log output pipe closed (SIGPIPE, cgroup OOM count zero); its
  partial 7,620 HTTP-200 responses are excluded from capacity comparisons.
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
The direct-VM load generator can now opt into same-origin writer observation:
it samples queue and retained-byte peaks, reports counter deltas only after
in-flight drain, and refuses to treat missing telemetry or a reset counter as
zero. A 1,000-request scratch phase found a 6.16-second writer drain after a
2.67-second client run despite all HTTP 200, and a 5,000-request phase
recorded full optional queues and best-effort drops. Row counts are still
checked independently because committed writer jobs include other work.

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
- [SQLite's appropriate-uses guide](https://www.sqlite.org/whentouse.html):
  one database file has one active writer, while local single-node storage is
  a strong fit. More CPU and more admitted workers do not by themselves make
  indexed writes on that file scale linearly; keep SQLite and measure the
  writer/storage working set instead of claiming an unlimited write rate.
- [SQLite PRAGMA reference](https://www.sqlite.org/pragma.html):
  `cache_size` is a suggested connection page-cache size and `mmap_size` a
  mapped-I/O limit, not guarantees that the relevant index pages remain
  resident. The reversed-copy controls above are required before attributing
  a result to either setting.
- [k6 open and closed workload models](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/open-vs-closed/):
  closed-loop clients reduce offered traffic as latency rises, motivating independent
  arrival-rate tests alongside the operator's fixed-concurrency `hey` measurements.

External sources support the mechanisms and tradeoffs. The implementation choices
and expected benefits above are Orva-specific proposals, not benchmark guarantees
derived from those sources.
