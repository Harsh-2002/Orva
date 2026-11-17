# Opt-in jobs lifecycle

This package is internal and inactive in normal serving construction. Explicit
`InitAsyncSchemaForFixture`, SDK-host `WithIAM` + `WithAsyncIAM`, scheduler
`WithAsyncIAM`, and opted-in HTTP/MCP dependencies are required together. A nil
configured service denies, never falls back to legacy enqueue. It is not a
production feature flag. Jobs-only scheduler construction still disables cron.
Cron requires its own explicit options below; adoption/reissue, boot migration,
restore and production cut-over remain pending.

## Authority and ordering

SDK enqueue uses only the actual leased caller/current managed artifact. Both
outbound `jobs.enqueue` and target `job` acceptance are required. The synchronous
SQLite insert and scoped idempotency reservation share that read-gate interval
and transaction. Request fields cannot supply origin or operator identity.

Scheduled execution uses typed standing provenance, never a fabricated execution
lease. It registers before pool wait, rechecks before claim, and checks current
grants and the actual acquired target generation again before recording an attempt.
The persisted row generation and active/pending state are CAS conditions. DB/frame
I/O and worker waits are never hidden in host-state observers. Only bounded
transactions run as admission effects. Publication marks touching registrations
revoked under the gate and signals cancellation after releasing locks.

Creation revisions are immutable provenance. Separate admission revisions are
conservative invalidation guards: any touching policy revision change makes old
work require explicit admin revalidation, even if permissions are later regranted.
A revision is never itself a grant. This intentionally also suspends otherwise-
allowed pending work after an unrelated policy edit. Suspension does not increment
attempts or consume user-code retry backoff. Transient DB/pool failures defer before
dispatch. An admitted failure/cancellation records its normal failure; revoked
future retries remain suspended. Effects already produced are not rolled back.

Admin resume is for one suspended pending job with current original-origin rights.
It does not reopen terminal or quarantined jobs. Retry is only for failed active
jobs, rechecks original rights, retains origin/key scope and resets attempts.
Operator work is explicitly admin-owned; later credential expiry does not revoke
accepted work, but every management mutation rechecks live admin authority inside
its transaction. UI metadata is advisory; neither hidden buttons nor a cached MCP
catalog authorizes a mutation.

## Storage and limits

The explicit initializer only adds job columns, v2 idempotency storage and audit.
The legacy `idx_jobs_idempotency` is unchanged. New jobs leave its key NULL;
`job_idempotency_v2` uniquely keys origin, creator identity, target UUID and raw key.
Expired reservations are replaced transactionally; the job retains its original
raw key separately so expiry cannot erase retry provenance. Legacy hints are review-only:
known function hints stay suspended, unknown origin stays quarantined.

Creator deletion invalidates matching nonterminal jobs by UUID. The existing
target FK still cascades job deletion. The fixture therefore does **not** claim
retained missing-target history: stale claims/final admission fail closed when
the row or original creator/target UUID is gone. Same-name recreation cannot
authorize the old identity. Changing target-deletion retention requires separate
design/verification, not a destructive migration hidden here.

Payload is capped at 1 MiB, idempotency keys at 256 UTF-8 bytes, retry budget at
100 and the idempotency window at seven days. Reasons use a closed bounded set.
Waiting/admitted operations share the Authority's bounded registration budget.

## Verification

`TestLiveAsyncJobs` is explicit disposable-VM-only. It requires the independently
checksum-verified runtime recipe inputs and an actual slim CLI binary through
`ORVA_ASYNC_CLI`. It exercises real Node/Python SDK producers, SQLite delayed jobs,
managed pooled SDK targets, revoke/regrant/resume, a real occupied-pool wait,
and authenticated HTTP/CLI/external-MCP controls. It exports no credentials,
database or raw execution lease.

Focused storage/authority/service tests cover origin/idempotency isolation,
expiry/rollback/CAS, current rights and deletion/name recreation. None of these
tests proves reboot/restore, cron, cgroup hard limits, or production activation.

## Independent cron core fixture

`InitCronSchemaForFixture`, SDK-host `WithIAM` + `WithCronIAM`, and scheduler
`WithCronIAM` opt in independently of jobs. No normal constructor or boot caller
uses these options. There are no staged cron HTTP/CLI/MCP/AI or UI controls yet.
The shared service is the only admin management seam in this increment.

Registration requires the real live SDK execution, current caller artifact,
an exact schedule request covered by its own-cron name/prefix grant, self target
and self `cron` acceptance. Origin is host-owned; SDK identity/authority claims
are rejected. The 25-named-schedule cap and upsert are checked in one transaction.
Upsert preserves creation provenance and cannot resume suspended/quarantined
work or rebind an old admission revision after regrant.

Firing uses standing authority, never a fabricated SDK lease. Claim advances
the exact due row into the future before pool wait. A critical execution-writer
slot is reserved before acquisition; wait does not occupy a host execution slot.
Final admission checks standing rights, the actual acquired managed target and a
row CAS on authority generation, configuration revision, enabled/state and claimed
next-run value. No user-code dispatch occurs on a stale edit/disable or denial.
Post-admission completion results also CAS the admitted revision and run timestamp,
so an old completion cannot overwrite an edit/resume. Ordinary response, 5xx and
dispatch-error paths submit through the same reserved critical completion writer.

Denial suspends with a bounded reason and clears next-run time; policy expansion
does not resume or catch up. `MutateCron(..., "resume")` requires live admin in the
commit transaction, revalidates the original origin and records current admission
revisions. It schedules only the next future cron time, increments authority and
configuration generations, and never changes creation provenance. Other edits
cannot implicitly resume work. Config edits/disables are CAS mutations. Accepted
operator schedules outlive credential revocation; subsequent management still
requires current admin (not read-plus-admin). Historical rows all quarantine:
neither a name nor a missing creator is proof of operator origin.

The legacy target FK still cascades schedule deletion. Missing UUIDs deny final
admission; retained missing-target history and restore/adoption remain cut-over
HOLDs. A same-name recreation never authorizes the original UUID.

`TestLiveCronIAM` reuses the sealed runtime/code/managed-pool jobs qualification
helpers. It is gated by `ORVA_LIVE_CRON=disposable-vm` and the same independently
verified runtime recipe variables. `WithCronPollIntervalForFixture` only shortens
the fixture poll interval; it does not alter the standard parser or future-time
semantics. The normal scheduler interval remains thirty seconds.
