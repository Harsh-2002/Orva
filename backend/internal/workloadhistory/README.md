# Opt-in deleted-work metadata history

Named-source deletion additionally requires `Managed` and `Registry` bound to
the same `NewManaged` DB/Authority/Registry. Sorted owner then definition locks
precede one retirement/history transaction. It removes only the retiring member,
advances membership once and retains bounded source audit in the existing root;
surviving grants remain unchanged. Missing coordination denies attached-source
deletion. See `../workloadpolicy/OWNER_DELETION.md`; normal serving stays off.

This internal service is not installed by normal server, router or boot code.
Initialize the existing staged jobs/cron schemas, explicitly initialize
`InitHistorySchemaForFixture`, and install this service as the registry's
`DeletionHook` only in a trusted scratch composition using the same Authority.
Normal nil-hook deletion remains the legacy path. Enabled history never falls
back to legacy deletion after a preparation or archival error.

`Execute` is the strict original-raw request boundary for the opt-in HTTP adapter,
external MCP and shared AI registry. It rejects duplicate/unknown fields, invalid
UTF-8, oversized/deep or trailing JSON before typed decoding. `capacity` requires
READ and reports canonical used/reserved rows/bytes and caps as decimal strings,
90-day purge eligibility and advisory live `can_manage`. With explicit
`mcp.Deps.History`, five operation-specific tools delegate to this same service;
nil keeps the normal catalog unchanged. Service transactions still enforce
current ADMIN and explicit confirmation/review, independently of catalog visibility
and AI approval policy. Loopback tests qualify these adapters, not CLI/UI,
real-runtime cross-surface execution, browser Chat or a provider turn.

## Boundary and ordering

The shared REST/MCP function-deletion registry path holds the function mutation
lock, then prepares under the exclusive IAM gate and a SQLite transaction.
The operator reference comes from the current trusted authentication context,
not request fields, stored permissions, global actor state or SDK arguments.
Live admin and expiry/audience/session facts are checked before reservation and
again before commit. Missing/revoked credentials or capacity failure leave
function/cache/admission state unchanged and do not retire the pool.

Preparation captures pending/running target jobs and every target schedule.
Metadata is bounded and canonical: stable UUID provenance, revisions, original
status/reason/times, attempts, schedule settings and payload byte length. Payload,
environment, secrets, raw idempotency keys, error text, artifacts, generation
tokens and execution leases are not retained. No payload blob is transferred
into the preparation process. Payload hashes are not computed for old work.
Source metadata strings are bounded before reading them.

Capacity reservation, immutable source capture, progress and `deleting` status
commit together. The gate then permanently retires that UUID in this Authority.
Standing registrations are marked revoked under the gate; cancellation signals
and pool callbacks run after the gate and registry lock are released. Each
64-record archive batch flips reserved records and deletes their live rows in
one transaction. Final deletion retains the existing writer lifecycle fence,
execution cleanup and target FK cascades, and releases the root reservation.
Creator-only deletion suspends live work without changing its original UUID.

History accounting covers canonical JSON bytes and record counts for deletion
roots, tombstones and history-control audit records, including reservations.
Limits are **100,000 records / 64 MiB**, not SQLite file/index bytes or a bound on
other existing audit tables. Reserved capacity cannot be purged. Ninety days is
explicit purge eligibility, not automatic TTL or silent eviction. Purge is
bounded, explicit, admin-only, confirmed and audited in the same transaction.

Retries use persisted progress without recapture or duplicate accounting and
validate the original deletion identity/actor metadata. Interrupted cleanup stays
`deleting`; it does not expose an active half-deleted function. Raw SQL and
internal DB calls outside the configured trusted composition are not claimed to
be universally fenced. Normal boot does not load this history mode.

## Inspection and reissue

History requires live READ authority. Inspection is never executable authority.
Old IDs have no retry/resume/adopt operation. Same-name recreation does not bind
old work to the replacement UUID, and old deduplication keys are not replayed.

Reissue requires live ADMIN, `confirm:true`, `reviewed:true`, fresh payload,
complete fresh configuration and an explicit new target UUID. Current target
authority is checked under the IAM gate; the DB transaction creates a NEW job or
schedule as OPERATOR origin and appends origin-change audit atomically. It never
restores an old policy, creator grant, payload, work ID or key. Missing/purged
history denies. An SDK/channel principal cannot submit operator provenance.

The optional HTTP adapter is documented in `docs/API.md`; it is not registered
on normal servers. External MCP/shared-registry adapters are explicit opt-in;
`orva history` delegates original bounded request files/stdin to the same HTTP
contract. Optional Jobs/Cron history inspection and fresh-work review share
`HistoryInspector`; normal views leave it off. Component tests and the real
loopback CLI/MCP/HTTP checks do not qualify the real-browser/SDK cross-surface
workflow, which remains a separate gate.

## Evidence and limits

Focused tests cover request actor isolation, missing/expired/revoked credentials,
transaction-local revocation/expiry, failed deferred-FK COMMIT, capacity denial,
canonical ledger reconciliation, partial-batch retry, reserved purge refusal,
concurrent post-closure enqueue/resume/publication denial and a real child-process
exit after reservation followed by resumption. This is not power-loss/reboot
recovery acceptance or proof of every interleaving.

`TestLiveHistoryLifecycle` is explicit disposable-VM-only. Real Node/Python SDK
enqueue, configured function DELETE HTTP, metadata inspection, fresh-UUID HTTP
reissue, actual sealed managed scheduler execution and SDK/audit correlation
passed. No API or jail mock is used in that fixture. Rlimit-only fallback does
not prove hard cgroup limits.

Full cut-over remains blocked on independent acceptance of managed-policy
deletion coordination, privileged/queued-build interactions,
restore quarantine and loader behavior, complete public controls/UI, and
independent review. Restore must keep archives inspection-only and quarantine live
work under the later restore marker; this increment does not implement or qualify
that integration. Do not activate this partial composition in production.
