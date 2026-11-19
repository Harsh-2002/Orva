# Admission-closed recovery fixture (Linux, opt-in only)

This internal coordinator drives real `backup.ArchiveTo` /
`RestoreFromGuarded` installation and file-backed SQLite migration/quarantine.
It is **not installed by `serve`, normal migration, REST restore, MCP or CLI**.
The existing `RestoreFrom` and public handler retain their legacy behavior.
There is no production feature flag, remote administration route or supported
operator recovery command in this patch. Do not start normal serving to try it.

## Host boundary

`Open(dataDir, Host)` requires an existing owned, non-group/world-writable
directory, no symlinked ancestors, and the same outer data-directory lock as
legacy `serve`. Absent mode selects the legacy fixture layout; durable managed
mode requires the tombstone and selects `managed/`. `OpenManaged` additionally
requires the mode to match the verified Owner. Managed startup and restore
must use the bound methods with that Owner. Control files are no-follow,
owner-only, single-link regular files. Startup
rejects linked SQLite DB/WAL/SHM paths before opening SQLite. Filesystem
administrators remain trusted: the lock is not a sandbox against concurrent
privileged replacement of the owned directory.

Offline relocation is an explicit `servingmode` helper, not a serving transition
or enrollment. It refuses SQLite sidecars and existing managed destinations.
It atomically exchanges an existing legacy DB with an empty directory tombstone
and syncs both directories before publishing managed mode, after moving any
database keys. An interruption before exchange leaves the legacy layout intact;
one after exchange can leave both startup paths closed pending intervention.

Every operation calls trusted `Host.Quiesce` and verifies
`Host.AdmissionClosed`. The host must close admission, drain/stop workers,
schedulers and queued/running builds, close DB handles and establish whatever
external process fencing its lifecycle requires. These callbacks run outside
IAM gates and DB locks. They are a precondition, not something a marker proves.

`Start` reads the external marker before opening SQLite. Only after migration,
durable quarantine and durable marker removal does it call the separately
required current-artifact reconstruction callback; only successful reconstruction
with admission still closed permits the start callback. A failed start must keep
admission closed (the coordinator also requests quiescence on that failure).
This does not validate/rebind an old Factory or grant restored artifact approval.
`PrepareSealedDeployment` consumes one exact installation snapshot after that
barrier. It checks the source-aware policy's deny state, reads configuration and
ciphertext from the same snapshot, and verifies the concrete code/runtime
leases from the reopened Owner against that deployment. It returns an uninstalled
Factory and a deny evaluator; callers still own the admission barrier and the
separate install/review flow.
`PrepareCurrentDeployment` is for normal same-boot restart without a new
restore operation. It may return an existing approved evaluator only when its
approval matches the exact reconstructed Factory; it never rewrites approval
or enables paused owners.

`StartManaged` uses `StartBound`: it compares the verified Owner instance
identity against SQLite before opening the writer. A populated unbound
database or a nonempty legacy `functions/` tree is refused, not migrated
in place. `RestoreBound` verifies the staged SQLite binding and refuses
mutable function trees before moving installed files. If an incompatible
`ready` restore was installed through an older workflow, a validated
same-owner archive can replace it without removing the admission marker.
These are in-place recovery controls. They do not archive the sealed stores,
enroll legacy functions, or fence non-cooperating daemon processes.

## Marker and interruptions

The root `.workload-recovery` contains only version 1, a fresh operation UUIDv7
and one state. Its canonical JSON is at most 256 bytes. Writes use an exclusive
temporary file, file fsync, atomic rename and parent-directory fsync.
Archive entries and manifest paths cannot supply the reserved marker, traverse
outside staging, or introduce links in guarded mode.

| State/boundary | Restart behavior |
|---|---|
| Before marker durability | No DB replacement has begun; failed operation never admits work |
| `staging` | Startup denies; explicit archive retry retains the same operation UUID |
| `installing` | Startup denies, including partial multi-file failure; explicit retry or trusted manual completion required |
| `ready` | Installation fsync completed; startup commits quarantine for this operation |
| Quarantine committed, marker present | Replay reads the same epoch; does not advance revisions or append audits again |
| Marker unlinked, parent sync unfinished | This call denies; restart sees either absence or the same replayable marker |
| Removal durably synced | Normal restart preserves individually reviewed policies and suspended/quarantined work |

Malformed, linked, oversized or unsupported markers deny before SQLite opens.
Errors never clear a pending marker to resume service. The underlying legacy
backup install is **not a multi-file atomic transaction**: its rollback attempts
only the DB, not already replaced keys/code. `installing` therefore cannot be
automatically treated as complete or rolled back. Leftover staging/temporary
files are not adopted or granted reclamation authority by this package.

For a stopped-instance manual fixture, call `PrepareManual`, retain its operation
UUID, perform and verify the complete file installation while still stopped,
then call `CompleteManual` with that exact UUID. Completion fsyncs installed
files/directories and writes `ready`. Preparation alone intentionally cannot
admit anything. These are internal service operations, not shipped CLI commands.
Direct DB/file copying without this marker procedure is outside the guarantee.

## One SQLite recovery transaction

`Database.ApplyRecoveryForFixture` is explicit, never a normal boot migration.
The first transaction initializes additive policy/managed/async schemas and its
migration record. Both legacy SDK boolean values receive empty allow/inbound
authority. Legacy true is review-required; a separate bounded-per-owner boolean
note records the prior coarse capability without expanding grants. Existing
policy documents, inline/named sources, attachments, approvals, IDs, KV, source
and inspection history are retained, never inferred from code or names.
Historic function-enqueued jobs are suspended; unknown jobs/cron are quarantined.
Subsequent normal starts do not repeat legacy provenance conversion.

An external restore operation also quarantines every policy/inbound acceptance
and every durable job/cron, including terminal and operator-origin rows.
Per-owner revisions/authority epochs and work generations advance, cron config
revisions advance and next-run/admission revisions clear. Work status, payload
and original provenance are not rewritten. Queued/building deployments become
failed, never resumed or rebound to current authority.

The transaction uses SQLite `synchronous=FULL`, commits one operation/epoch with
all policy/work changes and audits, and is followed by a parent-directory sync.
An actual deferred-COMMIT failure rolls back policy/work/audit/epoch changes;
the external marker remains. Only the last committed operation is remembered;
the host must serialize recovery and never replay an older administrator-held
marker over a later operation. A fresh restore always uses a new host UUID.

Limits: 10,000 functions/policies; 100,000 jobs, cron rows and abandoned builds
per category; at most 200,000 filesystem entries during installed-tree sync.
Exceeding a transaction limit denies rather than batching a partially open
recovery. Signed-64-bit revision/generation/epoch exhaustion denies before
arithmetic. Policy audit retains its existing 32 records per owner. Recovery
adds metadata-only `restore_quarantine` rows to the existing work audit tables
and keeps the newest 32 such rows per live work ID; other audit actions are
unchanged. The actor is `system` with the external operation UUID, not a
credential, bearer or archived payload. This creates no executable history.

`RecoveryPreflight` reads the same store without author-code inspection or IAM
initialization and returns bounded function/legacy-true/work/stale-build counts
and the suggested empty review policy. Counts beyond supported caps cause an
error; this is not an unbounded export facility.

## Evidence and limits

Focused tests use real file-backed SQLite, real snapshot/archive/guarded restore,
real deferred foreign-key COMMIT failure, process exits at durable marker and
commit/removal boundaries, and named fsync syscall fault seams. They cover
idempotent replay, normal restart after individual reapproval, all work origins,
retained sources/approvals, metadata-only history, malformed/linked controls,
work audit retention, counter/capacity denial, manual completion and callback
ordering. Error-return seams and process crashes are **not power-loss tests**.

The delegated same-boot test can optionally use the hash-pinned Node/Python
runtime fixtures in `ORVA_STARTUP_SEALED_RUNTIME_DIR`. It reopens the sealed
stores, runs both interpreters in nsjail under the new Owner domain, and
checks that the private SDK host rejects the retained stored grant. Missing or
mismatched sealed pairs prevent the start callback. This is controlled fixture
dispatch, not an HTTP listener, normal server boot, or a scheduler run.

Hardware durability, ordinary reboot, GC/adoption authority, legacy migration
into sealed stores and full serving cut-over are not established here.
