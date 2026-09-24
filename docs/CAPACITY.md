# Pool Controller v2 capacity validation

## 2026-09-24 writer-path measurement instrumentation

The writer now exposes per-priority cumulative counters for normal-path batch
attempts, committed jobs, SQLite connection acquisition time, statement time,
commit time, and writer-submit-to-commit time/samples at `/metrics`. The counters
are diagnostic instruments, **not** a measured throughput improvement. Compare
counter deltas over identical load windows to distinguish writer wait from
SQL/commit work; the submit-to-commit measurement includes admission wait and
the latter two.
Savepoint failure recovery is excluded from committed-job and queue-wait
samples, so reconcile accepted HTTP responses with execution rows and monitor
critical failure counters separately. No pool cap or SQLite durability setting
was changed on the basis of the isolated insert microbenchmark alone.

### Direct-VM writer saturation profile (same date)

A later direct-link run used a 2-vCPU/4-GiB smolvm server with its disk-backed
database (about 1.45 million existing executions), a separate 1-vCPU/512-MiB
client VM, and two already-deployed Node/Python functions. At 250 closed-loop
clients, 20,000 requests took 98.36 seconds: 19,953 returned HTTP 200 and 47
returned pre-execution `STORAGE_BACKPRESSURE` 429; there were no transport
errors or 504s. After writer drain, a timestamp-and-function query found
exactly 19,953 new successful execution rows. The 429s are real capacity
pressure, not lost post-execution records.

During that run the critical writer committed 19,967 jobs in 195 batch
attempts, with 85.884 seconds cumulative statement time, 9.115 seconds commit
time, and 0.032 seconds connection wait. The additional jobs include
non-final-row critical work, so committed-job count is not the HTTP-200 count.
Its submit-to-commit total was 98,121 seconds across jobs (about 4.9 seconds
per job); this is an **asynchronous persistence delay**, not the public HTTP
response latency. Critical failures and timeouts stayed at zero. Activity
and optional telemetry dropped 12,067 and 30,898 records respectively under
this overload, so the run does not meet the plan's ordinary-load telemetry
gate.

A 30-second loopback-only CPU profile collected during the same run had 7.70
seconds of on-CPU samples. The writer run/commit call stack accounted for 5.08
seconds cumulative (66%); SQLite's B-tree insertion for 4.45 seconds (58%),
with page reads in that call tree accounting for 3.02 seconds (39%). These
call stacks overlap; the percentages must not be added. After the run the
database held 1,470,051 executions and was 1.55 GB on disk; nine explicit
execution indexes occupied about 1.06 GB by `dbstat`. Index maintenance and
page reads are therefore concrete hypotheses, **not** permission to prune
indexes without a same-snapshot A/B plus read-query-plan and correctness
checks. The earlier index-pruning attempt in this document was reverted.

The repeated production-schema microbenchmark on the current host measured
9.31–9.85 ms per 200-row batch for prepared per-row inserts and 8.29–9.43 ms
for grouped inserts across three runs. The ranges overlap; grouped inserts
used about 308 versus 3,024 allocations per batch but more allocated bytes.
This differs from the earlier run below and reinforces that the isolated
benchmark cannot establish an end-to-end gain or justify a new batch limit.

A read-only `EXPLAIN QUERY PLAN` audit on the same 1,470,051-row scratch
database found that `idx_executions_function` serves baseline seeding and
per-function history, `idx_executions_started` serves global history and
retention, and `idx_executions_trace_id` finds trace members on this snapshot
(`idx_executions_trace_parent` can do so if the former is absent). Trace
member ordering still needs a temporary B-tree because it normalizes mixed
timestamp formats with `julianday(replace(...))`; status-filtered history
also needs a temporary sort. Thus apparent left-prefix overlap does not
prove an index is removable without changing read cost. The production
schema is unchanged. A scratch-only same-snapshot write/read comparison is
still required before proposing index changes; the additive-only migration
contract separately rules out shipping an unapproved destructive drop.

The scratch-only `test/performance/sqlite_index_ab.py` harness uses
[SQLite's backup API](https://www.sqlite.org/backup.html) to make two consistent,
temporary copies of that VM database. It can omit indexes from **the candidate
copy only**, or compare a write-connection cache setting. A compiled Go test
binary can then call Orva's actual `asyncWriter.commit` on both copies,
alternating 200-row batches with the production SQL and UUIDv7/trace/span ID
shapes. It verifies exact new-row counts and reports wall time, SQL time,
commit time, and physical `read_bytes`/`write_bytes`. Sampled
function/global/status/trace/retention query plans are also captured.

The initial Python-driver run reported 4.82 ms with all indexes versus
3.49 ms without `idx_executions_trace_id` and
`idx_executions_parent_span_id`; its trace/span IDs were incorrectly
UUID-shaped, so that result is **withdrawn**. A first actual-driver run then
appeared to show 423 ms versus 33 ms per 200-row batch, and a 256-MiB cache
experiment appeared to show 541 ms versus 36 ms. Those apparent wins were
also **invalid**: reversing which copy was made last reversed both results.
An identical-schema, identical-cache control proved the artifact. With the
baseline copied first it took 581 ms median and read about 3.3 MiB from disk
per batch, versus 41 ms and almost no disk reads for the candidate copied
last. With copy order reversed, the unchanged baseline took 39 ms and the
unchanged candidate took 721 ms, the latter reading about 4.4 MiB per batch.
The later copy was resident in the guest page cache; changing indexes or
SQLite cache size was not the cause of these large differences. All copies
were discarded and the 1,470,051-row source was unchanged.

Two subsequent **unchanged-binary**, direct-link mixed Node/Python runs used
the same 2-vCPU/4-GiB sandbox-required server VM and separate 1-vCPU/512-MiB
client VM, with 20,000 requests at 250 closed-loop clients each. The first
returned 20,000 HTTP 200 in 64.66 s (309/s; HTTP-200 p99 2.18 s); the second
returned 20,000 HTTP 200 in 34.98 s (572/s; p99 1.12 s). Neither had a
transport error, storage 429, critical write failure, or critical timeout.
After each writer drain, read-only per-function counts increased by exactly
10,000 Node and 10,000 Python successful execution rows. These are successive
runs on a growing database, not a baseline/candidate comparison.

The server process physically read about 429 MiB during the first run and
120 MiB during the second, while writing about 1.00 and 1.01 GiB. Its
delegated cgroup's file cache grew from about 143 MiB before the first run to
594 MiB after it and 735 MiB after the second; major faults rose by 2,291,
then 84. The critical writer spent 55.68/26.32 s in statements and
8.11/6.37 s in commits; connection wait remained negligible. This supports
page-cache residency and disk I/O as **material contributors** to the large
throughput swing, not proof that they are the only limits or that a larger
SQLite connection cache/index change would help. The pre-run row-count query
itself warmed some pages, so even the first phase was not a cold-cache test.
Activity lost 9,037/8,509 records and total best-effort telemetry lost
27,852/27,438 under these loads. Both optional lanes reached their 1,024-job
channel limits during the first run. Exact critical-row accounting therefore
passes, but the telemetry/ordinary-load and controlled-performance gates do
not. No schema or cache policy was changed.

An optional-telemetry scheduling experiment then made that lane eligible
whenever both higher-priority queues were below three-quarter occupancy,
instead of only when both were empty. A 5,000-request/100-client
baseline–candidate–baseline sequence on the same running VMs produced all
HTTP 200 in 22.50/14.72/12.61 s; total best-effort drops were
5,974/5,367/5,387 and activity drops 2,051/1,820/1,629. The later
unchanged baseline was faster than the candidate and its drop count was
essentially the same. The three phases added exactly 7,500 successful rows
per test function after drain. Database growth,
cache warming, and other run-order effects remain uncontrolled, but this
short test supplies no defensible benefit for changing critical-write
priority. The experiment was **reverted**; persistent optional loss remains
an open admission/storage-accounting problem.

Do **not** use this short two-copy probe to justify index pruning or a larger
cache. A valid next comparison needs controlled filesystem-cache residency,
sustained read/write traffic on restored snapshots, and independent direct-VM
HTTP load with critical-row reconciliation and telemetry accounting. The
earlier live-server index-pruning regression remains the release evidence;
the production schema and cache policy are unchanged.

### Success-history read path on the same scratch database

The existing `GET /api/v1/executions?status=success&limit=50` query used the
status index to find about 1.47 million matching rows, then sorted them to
return 50. Read-only SQLite probes on the 2-vCPU/4-GiB guest measured 2.63 s
for that sort, versus 0.21 ms to read the newest 50 directly through the
`started_at` index. Forcing that latter index for **all** statuses would be
wrong: only 19 rows had `status=error`, and a forced `started_at` scan took
66.5 s to find them versus 14.7 ms on the status index.

The candidate therefore probes only the newest unfiltered page when listing
successful executions with no date/search filter or offset. If every row on
that bounded page succeeded, it is exactly the requested page; if not, the
old filtered query runs. No index, migration, or result contract changes.
On the same VM and database, the immediately preceding `ba4d310` server
binary took 7.07–7.16 s across three warm HTTP reads; the candidate took
0.193–0.219 s across three. An alternating baseline/candidate check found
identical totals (1,470,032) and identical SHA-256 hashes of the 50 returned
execution IDs. The count query still scans the success index and accounts
for much of the candidate's remaining ~0.2 s. These are history-page
measurements, **not** invocation throughput or writer-capacity gains.

A new benchmark uses the production 18-column final-execution INSERT, foreign
key, and current execution indexes instead of a one-column toy table. On the
same local host, three 200-row batch repetitions measured prepared per-row
INSERT at 16.0–16.5 ms/batch and one grouped 200-row INSERT at 17.4–21.9
ms/batch. Capping grouped statements to 850 bound values (four 50-row
statements per transaction) measured 9.6–10.7 ms/batch in three runs; 25-row
groups were 10.8–12.0 ms and 100-row groups 14.9–17.0 ms in two exploratory
runs each. The change retains the 200-job transaction batch and allows narrow
rows to group more densely. This isolates indexed write-shape cost on one host;
it does **not** establish an HTTP throughput gain, disk-independent optimum,
or a reason to increase admissions before a controlled VM comparison.
The changed binary also passed all 29 real-sandbox E2E modules (676 checks,
no failures or skips). In a direct-link 2-vCPU/2.5-GiB scratch server plus
512-MiB client VM, 1,000 and then 5,000 mixed Node/Python requests at 100
closed-loop clients all returned HTTP 200 with no transport errors. The
5,000-request phase took 47.18s (106 successful/s); after writer drain,
exactly 5,000 new status-200 execution rows appeared, with zero critical
writer failures or timeouts. This is a functional validation; shared-host
storage and database growth make it an unsuitable throughput comparison.

The next candidate removes allocation/copy work from each full pool service,
queue-wait and spawn sample window. A full-ring local microbenchmark records
service samples in 16.9–18.8 ns with zero allocations (three 1-second runs),
and focused pool race tests pass. Percentiles still use the newest 512 service
and queue samples or 256 spawn samples; their sorting now runs after releasing
the signal mutex. The changed binary passed all 29 real-sandbox E2E modules
(676 checks, no failures or skips). In a separate direct-link 2-vCPU/2.5-GiB
scratch server and 512-MiB client, 5,000 mixed Node/Python requests at 100
closed-loop clients returned 5,000 HTTP 200 with no transport errors in
26.12s (191 successful/s). After drain, all 5,000 had status-200 execution
rows; critical writer failures and timeouts remained zero. The earlier
unchanged VM phase was 106/s, but storage/cache and database state changed,
so this is **not** a controlled throughput gain. The guest test binary was
removed, both scratch VMs stopped, and server memory restored to 4 GiB.

The first scratch boot against an existing 1.4-GiB database had no HTTP
listener after 2m35s. `EXPLAIN QUERY PLAN` for baseline warmup showed an
index lookup by execution status followed by a temporary B-tree for a
whole-history `ROW_NUMBER` ordering. Baseline seeding now reads at most ten
sample-windows per function through the existing `(function_id, started_at
DESC)` index; the replacement query plan showed an index search by
`function_id` without a temporary sort. The replacement boot reached health
rapidly on the same VM,
but the second boot had warmer guest/host caches, so these observations do
not establish a controlled startup speedup. A regression test covers the
bounded recent-window semantics.

On the 2-vCPU/2.5-GiB scratch server with a separate 512-MiB client VM,
5,000 mixed Node/Python requests at 100 closed-loop clients all returned
HTTP 200 in 23.08s (217/s); p50/p95/p99 were 105/1,243/1,647ms. Critical
writer failures and timeouts stayed zero. Over that window, 5,005 normal
critical jobs committed in 44 batches; connection acquisition increased
0.011s, SQL statement time 23.816s, and commit time 1.991s. The cumulative
enqueue-to-commit sum rose about 24,333s across those jobs (~4.9s/job),
which can outlast the HTTP response because the final row commits
asynchronously. Activity dropped 1,969 rows and total best-effort telemetry
dropped 5,870. This is one diagnostic phase on a smaller guest, **not**
a 4-GiB controlled A/B or evidence that SQLite itself is the universal
bottleneck. It does show that raising worker/queue ceilings on this guest
would increase an already saturated storage backlog.

The same running 2.5-GiB guest then received 10,000 mixed requests at 500
closed-loop clients. It returned 9,676 HTTP 200 and 324 pre-execution
`STORAGE_BACKPRESSURE` 429 responses in 64.24s (150.6 successful/s), with no
transport errors. HTTP 200 p50/p95/p99 were 2,942/4,789/5,602ms. A read-only
query over execution IDs newer than the pre-run maximum found exactly 9,676
status-200 rows for the two test functions after writer drain. Critical
failures/timeouts remained zero; activity drops increased by 5,230 and all
best-effort drops by 13,665. During the run all three writer queues filled,
while guest memory still had headroom. The writer's critical SQL-statement
time increased by 57.81s and commit time by 5.78s, versus only 0.014s of
connection acquisition. This shows the current scratch storage/write path
cannot sustain that offered load; it does **not** identify whether SQLite
index work, guest block I/O, or the underlying shared host disk dominates
inside `ExecContext`. Raising queue or worker counts would hide the pressure
temporarily, not increase sustainable throughput.

An attempted smolvm `block_io=async` comparison did not produce a usable
load phase: after the scratch guest restart, even an idle health request timed
out twice while the daemon showed negligible CPU and I/O progress. The VM
was stopped, returned to its original synchronous block-I/O mode and 4-GiB
configuration, and both scratch machines were left stopped. No Orva result
is inferred from this failed infrastructure experiment.

After the resource-derived pool-cap candidate was built, a fresh 2.5-GiB
scratch-VM boot with no saved pool overrides reported `effective_max=20`
for each test function and successfully ran 1,000/1,000 mixed requests at
100 clients (234/s). Its critical writer drained with zero failures/timeouts.
A subsequent 5,000-request/100-client run on the same binary and VM returned
all HTTP 200 at 129/s, p50/p95/p99 168/1,882/3,868ms. Critical statement
time reached 40.17s, commit time 3.38s, and connection wait only 0.009s;
critical failures/timeouts remained zero, but activity drops reached 2,345.
Across a `/proc/stat` sample window encompassing this load, iowait rose by
2,288 of 10,770 aggregate CPU jiffies (~21%); `/proc/diskstats` for the
guest's `vda` added 32.95s of I/O-busy time. The sample window includes
lead-in and writer drain, so these are diagnostic indicators, not exact
fractions of the 38.85s client run. Throughput varied materially from the
earlier 217/s phase, and the two-core pool ceiling was below both removed
constants. No larger-host pool-throughput improvement is claimed.

## 2026-09-23 live worker-churn diagnosis

The same isolated 2-vCPU/4-GiB server and separate 512-MiB client VM drove
30,000 mixed Node/Python requests at 500 closed-loop clients on an unchanged
server: all 30,000 returned HTTP 200 (286 successful/s; p50 1,576 ms,
p95 2,701 ms, p99 4,145 ms). A 20-second guest `/proc/stat` interval was
97.8% non-idle/non-iowait/non-steal, so CPU was near saturation. The daemon's
own Go CPU profile contained 5.34 CPU-seconds in that interval, about 13% of
two-core capacity; SQLite was prominent inside that profile, but it does not
explain all guest CPU use. The remaining CPU includes sandboxed runtimes and
kernel work, whose exact fractions were not measured. The VM used real nsjail
with `ORVA_REQUIRE_SANDBOX=1`, but its cgroup controllers were not delegated
(`rlimit_only`), so hard per-worker cgroup enforcement remains unverified here.

Node-only/Python-only/Node-only 10,000-request runs at 500 clients, with the
same unchanged daemon, returned all HTTP 200 at 400/433/320 successful/s.
This spread makes a runtime-specific throughput claim unsafe. Pool metrics
during the last Node run reported 1,008 cumulative Node spawns and 989 kills
across the daemon's preceding runs, far beyond the 1,000-use recycling limit's
expected churn. Code inspection found two extra churn paths: release killed a
healthy worker immediately when a fluctuating `dynamicMax` fell below the
current total, bypassing the controller's 30-second scale-down grace; and a
queued function could reclaim idle workers from another *active* function
merely because that donor was above its configured minimum.

The first candidate removed release-path capacity pruning. In a restarted
Node-only 10,000-request run it returned all 200 at 821/s with 31 spawns and
zero kills. A baseline restart between candidate runs returned all 200 at
486/s with 211 spawns and 181 kills. Repeating the first candidate returned
all 200 at 457/s with 31 spawns and zero kills. The churn reduction repeated,
but throughput did not: restart, shared-host pressure, and growing SQLite
storage were not controlled. The first candidate then returned 20,000/20,000
mixed HTTP 200 at 500 clients and 332/s, with 193 total spawns and 162 kills
across the two function pools.

The second candidate also protects an active donor's desired capacity and
never reclaims from a donor with its own queue. Its restarted 20,000-request
mixed run returned 20,000 HTTP 200, no 429 or transport errors, at 635/s;
p50/p95/p99 were 608/1,374/2,111 ms. It spawned 83 workers and killed 52
across the two pools. This is strong evidence of less worker churn, not yet a
controlled throughput gain: the two mixed runs were sequential on a shared
host and the database/cache state changed. A 50,000-request, 1,000-client
repeat with execution-row reconciliation and full real-sandbox E2E was still
required. After its 20,000-request run and writer drain,
critical failures/timeouts were zero, but 26,818 optional telemetry writes
had been dropped, including 8,259 activity records. The churn fix does not
make best-effort activity lossless under SQLite saturation.

The same second candidate then completed 50,000 mixed requests at 1,000
closed-loop clients in 95.28 seconds: all 50,000 HTTP 200, no 429 or
transport errors, 525 successful/s, and p50/p95/p99 of 1,764/3,040/3,439 ms.
After all writer queues drained, a read-only SQLite query for the run's
start-time window found exactly 25,000 status-200 execution rows for each
function. Critical writer failures and timeouts remained zero. Cumulative
telemetry drops rose to 99,087, including 31,778 activity drops, so the
operator activity feed is incomplete under this load. The host's available
memory stayed above 1.6 GiB at the monitored point; this validates the
scratch VM's functional capacity, not a hardware-independent throughput
guarantee or full E2E compatibility.

An immediate repeat on the same running candidate returned another
50,000/50,000 mixed HTTP 200 at 1,000 clients in 77.44 seconds (646/s),
with p50/p95/p99 of 1,457/2,358/3,523 ms. After drain, a separate read-only
time-window query again found exactly 25,000 status-200 execution rows per
function; critical failures/timeouts remained zero. Throughput differed by
23% between the two repetitions, reinforcing that the shared host/cache
state matters. Cumulative telemetry/activity drops reached 167,922/51,867;
the second run therefore did not solve best-effort data loss. Cumulative pool
spawns/kills reached 188/169 for Python and 218/206 for Node, so some worker
turnover remains despite protecting active donor capacity.

After the load runs, the current candidate passed the complete isolated Docker
E2E suite: 29 modules, 676 checks, zero failures or skips, with
`ORVA_REQUIRE_SANDBOX=1`. The first local run had failed four invocation
modules because `test/e2e/env.py` omitted `systempaths=unconfined`, a flag
already present in the production compose and documented `docker run` commands.
Inside that test container Docker masked `/proc/kcore`, making nsjail's mandatory
procfs mount fail before the adapter started. Adding the missing test-container
flag made real Node/Python invocation, firewall, CLI, and deletion-race modules
pass. The production sandbox continues to mount a scoped procfs; no sandbox
weakening was shipped.

An additional 2026-09-23 diagnostic rules out two tempting but incomplete
explanations for the low cold-run rate. A 10,000-row, 200-row-batch execution
INSERT probe using Orva's actual pure-Go SQLite driver completed at 10,860
rows/s on the host's 934,202-execution snapshot and 6,130 rows/s directly
inside a 2-vCPU/2-GiB scratch VM on its existing ~1.2-GiB database. The
probe used the same execution columns, foreign keys, WAL/NORMAL mode, 64-MiB
connection cache, and 200-row grouped transaction shape; it did **not** run
HTTP workers, activity/capture writes, or the rest of Orva. Database sizes
and cache state differed, so this is an isolation probe, not a throughput
ratio to apply to the product. Both offline rates are far above warm mixed
HTTP success rates; the driver and virtual disk alone cannot account for
all of the end-to-end gap. The probe deleted its 10,000 test rows after its
measurement. A separate attempt to drop six trace-related indexes from a
million-row offline copy exceeded a three-minute safety timeout. No index
migration or reduced-index throughput result came from that attempt.

To test whether best-effort writes were consuming the critical writer, a
temporary scratch-only build stopped draining activity/capture during live
traffic, but still drained accepted queued work on shutdown. On the same
2-GiB VM at 500 closed-loop mixed clients, normal Orva first returned
18,311/20,000 HTTP 200 and 1,689 pre-execution storage 429 at 140
successful/s; the critical-only diagnostic then returned 20,000/20,000 at
317/s; restoring the **unchanged normal writer** returned 20,000/20,000 at
367/s. The reversal beats the diagnostic, so the large apparent first-to-
second gain is not attributable to suppressing activity. This does not make
activity loss acceptable: the normal first phase dropped over 10,000
activity records under pressure. The critical-only code was reverted, and
the final normal binary passed database tests. These 2-GiB phases are
diagnostics only, **not** the 4-GiB capacity qualification.

The same shared test host OOM-killed the scratch server VM during an offline
database-copy experiment at 16:20 UTC. The failed VM phase supplies no
performance result; the temporary ~2.8-GiB database copies were deleted and
can be recreated from the retained 939-MiB snapshot. Host memory headroom
must be checked before further 4-GiB VM stress. The remaining investigation
is concurrent worker/request-path CPU, memory and I/O behavior during live
traffic—not an assertion that SQLite alone is the hardware ceiling.

Further 2026-09-23 writer tracing split each committed critical batch into
bulk-SQL construction, prepare, execute, close, and commit time. Three
successive 20,000-request mixed Node/Python phases at 1,000 clients on the
same running 2-vCPU/4-GiB VM returned respectively 9,586/20,000 HTTP 200
(10,414 pre-execution storage 429; 100 successful/s), 15,296/20,000 HTTP
200 (4,704 storage 429; 186 successful/s), and 20,000/20,000 HTTP 200
(352 successful/s). Restarting **only Orva**, not the VM, then returned
20,000/20,000 at 391 successful/s. In the first 80 logged critical batches,
7,482 jobs spent 74.6 s in SQLite `ExecContext`, 0.45 s in preparation, and
3.9 s in commit; after the Orva-only restart, the first 100 batches put
9,288 jobs through `ExecContext` in 13.8 s, with 0.41 s prepare and 4.5 s
commit. The timing code was temporary and has been removed. SQL string
construction/preparation is not the dominant cost in these runs. Their
strong warmup effect is consistent with guest/host page-cache and shared
virtual-disk variation, but this experiment does not isolate those causes.
The physical test host itself exposes a rotational-flagged QEMU virtual disk;
Orva's test VM adds another storage virtualization layer. Do not infer a
hardware-independent Orva capacity from these results.

Two more timing-build A–B–A probes on the already warm VM failed to justify
simple writer tuning. Raising only the writer connection's SQLite page-cache
ceiling from 64 to 256 MiB yielded 20,000/20,000 HTTP 200 at 409/s,
between the unchanged 64-MiB runs at 391/s and 451/s. Raising the writer
batch ceiling from 200 to 400 yielded 20,000/20,000 at 460/s, versus the
adjacent 200-row runs at 451/s and 445/s. Each phase grew the database, and
the shared disk conditions were uncontrolled; neither experiment demonstrates
a reproducible throughput gain or fixes cold-storage backpressure. Both
changes and the temporary cache-budget unit test were reverted. The first
two cold phases did drop optional activity/capture rows, but writer health
reported no critical commit failures. Further work should isolate indexed
execution-write I/O and storage working-set growth, not cache SQL text or
raise memory/batch ceilings based on a single warm result.

On 2026-09-23, a verified 934,202-execution SQLite snapshot was used for
repeat runs in the 2-vCPU/4-GiB server VM, driven by a separate client VM at
1,000 closed-loop clients. An unchanged, disk-backed server run returned
46,682 HTTP 200 and 3,318 pre-execution `STORAGE_BACKPRESSURE` 429 responses
for 50,000 mixed Node/Python attempts (264 attempts/s). A temporary
transaction-timing build, started from that same snapshot with the two saved
functions checked before load, returned 50,000 HTTP 200 at 273 attempts/s,
with zero critical writer failures or timeouts. It dropped 21,541 activity
and 70,209 total best-effort records under saturation. Earlier disk-backed
runs returned 50,000 HTTP 200 at 431–438/s. These results establish large
run-to-run throughput variation, not a stable 1,000-client capacity figure;
the timing build and host load also differed, so none is a controlled
performance win over another.

The temporary timing build logged every tenth critical batch and every batch
over 200 ms. Across 277 logged batches containing 46,164 jobs, cumulative
time was 92.9 s inside SQL statement preparation/execution and 44.3 s in
commit, versus 0.4 s acquiring the write connection; the slowest logged
batch took 2.05 s. This is a biased sample of slow batches, not a full-run
profile, but it directs the next investigation toward SQLite statement and
commit I/O rather than connection-pool wait. The instrumentation was removed
after the run. A proposed RAM-backed database diagnostic did not complete:
copying the 939-MiB snapshot into guest tmpfs exhausted the shared 8-GiB
test host, whose OOM killer stopped the scratch VM. No RAM-backed throughput
claim is valid. The disk-backed database recovered on restart and both test
functions invoked successfully. Do not use guest tmpfs for this comparison
on this host without first reserving adequate host memory.

A separate SQLite PASSIVE background-checkpoint candidate was **reverted**.
SQLite runs the automatic checkpoint on the committing writer thread; the
candidate added a second connection that checkpointed at an 8-MiB WAL-size
threshold while retaining the existing 10,000-page automatic checkpoint as
fallback. Its unit and database tests passed, including shutdown idempotence
and preservation of committed rows. On the same two-VM 50,000-request,
1,000-client mixed test it returned 47,813 HTTP 200 and 2,187 pre-execution
storage 429s at 326 attempted/s (312 successful/s), versus the preceding
baseline's 50,000 HTTP 200 at 431/s. After drain, the 47,813 accepted
responses matched 47,813 execution rows; however writer health also recorded
one critical enqueue timeout on an ancillary write. Background checkpoint I/O
competed with, rather than relieved, this saturated single-node workload.
The code and test were reverted. Do not disable automatic checkpoints or
promise a WAL/throughput benefit from this experiment.

An execution-index pruning candidate was **reverted**. Three indexes were
removed from the scratch VM database: two were left-prefix duplicates of
composite indexes, and one (`parent_span_id` alone) had no matching Orva
query. They occupied about 113 MiB of an 820-MiB database; representative
trace and newest-execution queries still used composite indexes after
migration. But a 50,000-request/1,000-client mixed test immediately before
the change returned 50,000 HTTP 200 at 431/s with 50,000 matching rows,
whereas the post-migration test returned 49,402 HTTP 200 and 598
pre-execution storage 429s at 374 attempted/s. There were no transport errors
or critical writer failures. The larger database, freed-page layout, and
shared host conditions prevent attributing the whole difference to indexes,
but the candidate failed the zero-error gate. The migration and its test were
reverted; do not claim that a smaller index set improves this workload.

A separate worker-retention candidate was also **reverted**. It let a healthy
worker whose host reservation already existed park above a transiently lower
effective cap, leaving scale-down to the controller's grace period. On the
same two-VM 50,000-request/1,000-client mixed test it reduced observed worker
churn but returned 49,973 HTTP 200 and 27 pre-execution
`STORAGE_BACKPRESSURE` 429s at 357 attempted requests/s (HTTP 200 p99
4.81 s). The prior committed binary had returned all 50,000 at 438/s in its
preceding phase. Because the database and host conditions changed between
runs this is exploratory, not an exact A/B effect size, but the candidate
failed both zero-error and throughput checks. Fewer cold starts by themselves
are not a valid optimization if retained reservations obstruct other pools
or the writer. The change and its unit test were reverted.

A bounded central worker/writer-slot pairing dispatcher was tested and
**reverted** on 2026-09-23. It passed targeted unit and race tests, and a
Python-only 10,000-request/1,000-client run returned 10,000 HTTP 200 at
614/s. The mixed Node/Python 50,000-request/1,000-client run returned
47,478 HTTP 200 and 2,522 client timeouts (all on Python), at 373 attempted
requests/s. After drain, 25,000 Node and 22,505 Python execution rows were
present for that phase; 27 timed-out Python requests had nevertheless run.
The unchanged committed binary, rebuilt and tested on the same two VMs and
functions, returned 50,000/50,000 HTTP 200 with no transport errors at 438/s
and 3.07 s overall HTTP 200 p99. Thus the pairing dispatcher regressed
mixed-workload fairness and throughput; unit correctness was not enough.
Its extra five-second queue allowance also exceeded the 15-second client
deadline and is unsuitable as an overload policy. Keep early completion
reservation until a replacement beats this controlled baseline. This test
does not establish whether lock contention, worker wakeups, or changing
worker availability caused the regression; that requires profiling.

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

A follow-up per-request pairing loop was also **reverted**. It waited for
writer-slot notifications without occupying a worker, then acquired a worker
and tried to reserve a completion slot, returning the worker on a collision.
The same two-VM 50,000-request/1,000-client mixed test returned 39,490 HTTP
200 and 10,510 pre-execution storage 429s, at 267 attempted requests/s and
5.46 s HTTP 200 p99. After drain, 39,490 execution rows matched the accepted
responses; critical failures/timeouts were zero, but 26,297 activity rows
had dropped. A unit test for the no-worker-held invariant passed, but this
load result disqualifies the loop as an improvement. A bounded central queue
must replace independent per-request wake-and-retry races.

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

`max_warm=0` (the default without an override) uses an automatic maximum
derived from host CPU slots, the 16-MiB minimum per-worker memory reservation,
and function concurrency. A positive `max_warm` can only lower that bound.
The fixed idle-worker channel is sized to the derived bound, so even an
enormous configured value cannot allocate an enormous channel. Existing
positive overrides remain in place on upgrade; set them to `0` to opt into
automatic capacity. `effective_max` remains the live ceiling, recomputed
each tick from observed memory use and current host headroom. This removes a
code-level 50/1,024 cap on larger hosts; the two-core storage-limited scratch
run above does not prove a throughput gain from the change.

## 2026-09-24 scoped cgroup enforcement check

In the disposable 2-vCPU/4-GiB smolvm guest, a candidate server launched
inside its own delegated cgroup created `orva.daemon` and `orva.workers` beneath
that group. Actual nsjail child cgroups exposed `memory.max`, `pids.max`, and
`cpu.max`; a temporary 80-MiB Node function allocating 512 MiB returned 502
and incremented the worker subtree's `memory.events:oom_kill` from 0 to 1.
The same proof passed with the daemon running as an unprivileged service user,
using the installer's existing `ORVA_DISABLE_USERNS=1` fallback because this
guest denied `/proc/<pid>/setgroups` under user namespaces. The probe creates
and deletes only its own function. This establishes hard-memory containment in
the scoped guest, not throughput, CPU throttling, PID enforcement, systemd
service installation, or Docker regression. A following disposable Docker
container did pass 20/20 real TypeScript deploy/invoke/rollback assertions,
remained healthy with `docker exec` working, reported `cgroup_v2`, and returned
502 with worker-subtree `oom_kill` 0→1 in the same memory probe. The Docker
entrypoint placed `tini` and its CLI helper in `orva.supervisor` inside the
container cgroup to satisfy the kernel's no-internal-process rule. Native
systemd and comparative throughput remain open gates.

An expanded disposable cgroup probe then exercised the kernel limits directly
in both Docker and the same 2-vCPU/4-GiB guest. A Node handler configured for
0.25 CPU completed with HTTP 200 and increased its own jailed child's
`cpu.stat:nr_throttled` by 10 in Docker and 8 in the guest. A Python handler
attempted 64 fork-like clones without exec or extra pipes; it completed with
HTTP 200 and increased that child's `pids.events:max` by 33 in each setup.
These are enforcement proofs, **not** sustainable throughput or fairness
measurements. The arm64 branch of the PID probe and the native systemd service
remain unexecuted locally. An initial Node child-process probe was invalid:
nsjail's default 32-open-file limit produced `EMFILE` before `pids.max` was
reached. That low per-worker FD limit is a separate workload-density hypothesis
to measure under safe aggregate FD accounting, not evidence of a PID cap.
