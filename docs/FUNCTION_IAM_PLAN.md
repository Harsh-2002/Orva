# Function IAM — design proposal and implementation gates

Status: **design approved; foundation implementation underway, IAM not activated or approved for rollout**.
Baseline: `311dd91d77aa8601489c72f51c76ec6837540e72` (16 November 2026).
This proposal consolidates independent architecture, security and UX reviews.
It is not the current API reference. Releases remain paused; no production
deployment or tag is authorized by this plan.

Review status: independent implementation-sequencing and focused security
reviews consider the revised proposal scoped design-ready for owner approval.
Owner approved proceeding with the recommended defaults on 16 November 2026.
This is not implementation verification or rollout approval. The decisions in
§2 guide implementation; runtime proof obligations remain.

## 1. Goal and current boundary

An internal Linux server composition now joins the managed pool, SDK, job/cron
admission, deployment/configuration and policy services without legacy route
fallback. In delegated disposable VMs the managed HTTP path created and
deployed both runtimes, installed their exact Factory, proved deny before
review, exercised an attached named policy, and rechecked deny after revoke.
Same-boot reopen retained durable pause and resumed into a separate inactive
generation. Restore qualification reopened sealed assets and ran both
interpreters without reviving quarantined authority. These are scoped
integration checks, not the coordinated cut-over below. Ordinary reboot with
a new kernel epoch and complete operator-surface acceptance remain separate
gates; legacy `orva serve` does not select this composition on its own.

Managed startup verifies the sealed Owner identity against SQLite before
migration; in-place managed restore checks the same binding before archive
promotion. The separate cut-over slice 3 portable restore accepts strict
managed v2 source archives offline, rebuilds only current code into a new
host-bound Owner and publishes managed mode last. Policy text remains
inspectable, but approval is cleared; route/webhook publication and standing
work do not resume. The same-Owner restore path is not relaxed.

Slice 3 scratch acceptance: `test/managed-enrollment.py` with
`ORVA_PORTABLE_VM_TEST=disposable-vm` passed in an offline `msb --no-net`
Ubuntu 26.04 guest. Two service cgroups, data directories and independently
created witnesses simulated hosts A and B. A ran both interpreters, allowed
each function's granted KV write, and dispatched a custom route and signed webhook.
B refused a truncated archive, rebuilt the current deployments, returned the
same Node/Python results, denied both KV writes, retained policy text with
quarantine/review state, and returned 404 for both exposures. A's same-Owner
replay kept its sealed stores and applied existing restore quarantine.
Explicit Node review on B re-enabled only the Node grant without republishing
either exposure.
The guest was removed. This is not a different-kernel, power-loss, or
full-release E2E qualification.

Cut-over slice 2 adds explicit Linux server-only offline enrollment. Its scope
is current supported Node/Python deployment rebuild (including npm, pip and
TypeScript) under the Owner, empty grants,
SDK-enabled policy review, standing-work suspension/quarantine, and preservation
of the legacy tree. It is not automatic activation, historical rollback, or
cross-host restore. Fault injection covers the durable layout refusal boundary.
An offline cgroup-v2 VM with real nsjail deployed and invoked both runtimes
under legacy serve, confirmed dry-run and a missing exclusion left the data
tree unchanged, enrolled and invoked the same current versions under managed
serve, and observed legacy exit 78. An explicitly excluded draft remained
uninvokable. The legacy Node SDK write succeeded before cut-over and returned
`IAM_DENIED` after it; stored policies remained empty and only the SDK-enabled
function required review. This is not approval for automatic or production
cut-over.

The real managed listener also qualifies explicit version rollout, stale
rollout refusal and detach across REST and external MCP on jailed Node and
Python. Chromium verifies the guided policy workflow and deterministic local
Chat approval; the slim CLI commits through the same REST service.

An unwired cold-recovery candidate passed a hard reboot on stable guest-local
ext4: the previous cgroup was gone, the wrong witness was refused, and both
seals reopened through the original witness. The guest overlay changed inode
identities, so that layout is not qualified. This is not daemon recovery.

The internal host-bound Owner now retires the oldest exact epoch at the
history cap, leaving the newest predecessor in place. A witness-authenticated
decision precedes unbinding and cleanup, and replay does not restore IAM
policy, approvals or grants. Version 1 still denies at the cap. An offline
disposable VM passed 12 same-boot restarts at cap 3, 13 retirement fault
points, process-exit replay on both sides of the association rename, denial
cases, and a synthetic prior-boot metadata fixture. This is not power-loss,
general interrupted append recovery, or production slice provisioning evidence.

SDK availability is normal runtime functionality. Authorization determines
which actions a function identity may perform on which resources. Replace the
coarse `sdk` switch, not the private transport or its host-owned identity.

Current implementation has a spawn-time SDK switch, caller-local KV, unrestricted
SDK-enabled target selection, per-dispatch leases, bounded streams, and shared
job/cron/span cores. SDK HTTP routes/credentials/fallback are already removed.
Current rollback restores the boolean from source snapshots. These are facts,
not the intended IAM semantics below.

Keep four authority domains distinct:

1. Operator API key/session/OAuth/MCP permissions.
2. Function workload identity and IAM policy.
3. Public ingress authentication, including webhook signatures.
4. Network egress and sandbox resource limits.

IAM is not tenancy, information-flow control, or a replacement for service input
validation. A function's own code can disclose data it legitimately receives.

## 2. Approved implementation decisions

| Question | Recommendation | Consequence |
|---|---|---|
| SDK availability | Provision channel for every compatible worker | Importing SDK does not grant access; remove switch from UI/API guidance |
| New-function rights | Empty allow list, default deny | UI offers explicit reviewed presets; no silent template grants |
| Bootstrap/background | Explicit own-KV read grants only; active lease for mutations | Module-scope writes become a documented breaking change |
| Cross-function KV | Not in first release | Existing SDK namespace remains caller-local; typed policy permits future extension |
| Internal target acceptance | Exact caller UUID + trigger allow list, default deny | Caller grant alone cannot expose a service internally |
| Execution identity | Callee executes as itself | No implicit caller delegation or blanket policy intersection |
| Grant administration | Admin only initially | Functions and ordinary `write` operators cannot edit grants |
| Privileged code/config writes | Admin required for outbound, inbound, or retained quarantined authority | Deployment, rollback, env, dependencies, secrets and trigger edits cannot become a grant bypass |
| Policy vs deployment | Separate storage and revision | Code rollback never restores IAM policy |
| Async execution | Reauthorize every attempt/firing | Revoked work is suspended/denied, not executed with historical authority |
| Restore | Quarantine workload policies and function-origin async work | Admin reviews/reapproves before restored workload authority resumes |
| Legacy migration | Review-required, no automatic broad compatibility grant | Breaking but no implicit broadening; provide preflight report before upgrade |

Alternatives deliberately not selected: automatic broad compatibility policy,
all-target wildcards, implicit access because a function is publicly invokable,
or internal OAuth. These would either preserve coarse authority or introduce
unneeded credentials/protocols. A narrower bootstrap default can be relaxed
later only through an explicit, reviewed capability design.

## 3. Policy contract

Introduce an in-process `workloadiam` package; do not overload operator
`auth.Principal`. Workload principal contains host-bound function UUID, worker
identity, optional active execution/lease, and context kind. Names are display
and resolution inputs, never policy identities. Resolve names to UUIDs before
authorization; rename preserves access and recreation under the same name does
not inherit it.

Each policy has schema version, monotonic revision and explicit allow statements.
Outbound rules and inbound acceptance belong to the same function and are
updated atomically through one admin policy endpoint, using the single combined
revision defined below as their sole normative concurrency token.
Rules union together; absence denies. Reject unknown actions, resource types,
versions, contradictory fields and malformed IDs. Bound policy bytes, statement
count and target count: proposed first-release limits are 32 KiB serialized
policy, 64 outbound statements and 256 total outbound targets/inbound callers.
Keys/prefixes use existing KV UTF-8 and character limits; never truncate to fit. All limits
are validated server-side and covered at boundary plus one-over-limit.
Use one combined revision for outbound rules, inbound acceptance and reviewed
artifact binding; every change requires expected revision. Inbound trigger
vocabulary is exactly `f2f`, `job`, `cron` (the last permits self only initially).
Each statement has exactly one typed resource and an explicit action array;
unknown/extra fields are rejected. No null-as-omitted ambiguity.

Resources are typed objects, not parsed ARN-like strings: own-KV exact key or
literal prefix; exact target function UUID; own schedule name/prefix; own active
execution. No regex, action wildcard, mutable role inheritance, or arbitrary
condition language. Context requirements are typed and fixed by action; only
KV reads can opt into bootstrap. Cleanup/stream close always remains possible
even after authority is revoked.

Presets expand to visible statements at save time. They are UI conveniences,
not globally mutable roles. Suggested presets: No platform access; Own state;
Call selected services; Enqueue selected workers; Own recurring work.

### Action/resource matrix

| SDK operation | Required rights | Resource/context |
|---|---|---|
| `kv.get` | `kv.get` | Own namespace + exact key; bootstrap only if granted |
| `kv.list` | `kv.list` | Own namespace + entire requested literal prefix; returns values |
| `kv.put` / `kv.delete` | Corresponding action | Own namespace + key; active execution |
| `kv.incr` | `kv.incr` + `kv.get` | Same key; active execution |
| `kv.cas` | `kv.cas` + `kv.get` | Same key; active execution; mismatch may disclose current value |
| `kv.batch` | All constituent rights | Preauthorize all before reads/writes; deny whole batch |
| Buffered/stream invoke | `functions.invoke` | Exact target + target accepts caller for `f2f`; active execution |
| Stream next | Current invoke/acceptance + owned handle | Same worker, lease and target; close needs ownership, not a grant |
| `jobs.enqueue` | `jobs.enqueue` | Exact target + target accepts caller for `job`; active execution |
| `crons.upsert` | `crons.upsert` | Own schedule + target self; active execution; existing cap retained |
| `spans.emit` | `spans.emit` | Own active execution only; no trace-read right |

Ordinary runtime logging remains available and bounded; it must not become an
authorization bypass for reading another execution. Best-effort span failures
remain observable host-side even if SDK convenience wrappers suppress errors.
No cross-function secrets/env API is introduced. Existing secret injection is
explicitly separate from this first IAM release.

Authorize the resource that the shared core will actually persist. In particular,
cron upsert currently trims schedule names: perform that normalization once before
matching the policy, then persist the same normalized name. Never authorize a
raw selector and mutate a different normalized selector afterward. Preserve the
25-schedule bound with a transactional count/upsert under the owner mutation
ordering; count failure denies instead of bypassing the quota. Quotas remain
distinct from IAM decisions.

First-release API examples and exact wire schema are intentionally deferred to
design freeze. Pin an operation-to-required-actions table shared by evaluator,
SDK errors and generated docs, rather than independently guessing permissions
from method names in each client.

KV prefixes are literal, case-sensitive UTF-8 byte prefixes, not paths, glob or
SQL `LIKE` patterns. Fix existing `LIKE` behavior before relying on prefixes.
List cursors bind to namespace/prefix and cannot widen a grant. Authorize a
list's complete requested range; do not fetch forbidden values then filter them.

Bootstrap is the host-recorded worker phase before adapter readiness is accepted.
Only `kv.get`, `kv.list`, and batches containing exclusively gets are eligible,
and each needs an explicit bootstrap grant for its complete resource range.
All puts/deletes/incr/CAS and mixed batches deny in this phase. Batch has no
bootstrap override of constituent actions. Readiness permanently ends bootstrap
for that worker, before its first invocation; an empty/forged/stale lease cannot
reopen it. Requests admitted before readiness may finish; later requests deny. This is
phase authorization, not proof that trusted module initialization generated a
request: any code in that worker has the same function identity. A denied read
raises the normal SDK permission error. Uncaught initialization errors fail
readiness with bounded diagnostics; imports alone never fail for lack of grants.
After readiness, protected operations need a current worker execution lease.
This does not isolate background tasks inside the worker: SDKs carry a current
process-wide lease and any code running during that execution can spend it.
Timers/threads are not distinct security principals. Test and document this
limitation instead of promising task-lineage isolation.

## 4. Service calls, async provenance and lifetime

A→B checks A's outbound action and B's caller/trigger acceptance. B's subsequent
B→C checks B's own policy. Public `auth_mode=none` does not grant internal
acceptance. Self-recursion also checks explicit policy/acceptance and retains
depth limits. No forwarded identity in payloads, headers or traces is trusted.

Jobs persist origin kind, creator UUID, target UUID and creation policy revisions
as provenance, not durable authority. Scope idempotency to origin/creator/target;
an old key cannot reveal another creator's job. Recheck caller permission and
target acceptance before each attempt, including delayed jobs and retries.
Missing creator/target or denial suspends/terminates without an endless retry
loop. Operator retries preserve original provenance; reissuing as operator work
is a distinct admin action and audit event. Operator-created jobs/cron need a
durable operator origin and explicit admin-owned execution authority, not an
expired API token or fabricated function identity. Revoking an operator key
revokes API access, not already accepted admin-owned schedules; deletion/disable
of that schedule/job is its operational revocation path.

Durable operator-work authorization is normative, across REST/MCP/AI/CLI:

| Operation | Operator permission | Workload inbound acceptance |
|---|---|---|
| Create/update/enable/retry/resume/reissue/adopt operator job or cron | `admin` | Bypassed as explicitly trusted operator invocation |
| Inspect history/config | Existing `read` | Not applicable |
| Disable/delete durable work | `admin` | Not applicable |
| SDK function-created job/cron | Host-bound function policy | Required; operator privileges never synthesized |

Changing existing `write`-gated job/cron mutations to admin is a breaking API/tool
change, not an incidental middleware detail. Origin is set only by host entry
point, immutable after creation except explicit audited adoption into new
authority. Operator fields in a function request are rejected, not honored.

Function-owned schedules recheck the owner's current schedule right and inbound
`cron` acceptance at each firing. Policies express self targets explicitly.
Schedule revisions prevent stale queued firings after disable/edit. Import,
backup, retry and clone must preserve or deliberately replace origin metadata;
unknown historic origin is quarantined, never interpreted as operator authority.
Scheduler revalidation is not an SDK `crons.upsert` call: it checks the owner's
standing schedule grant and current `cron` acceptance against persisted origin,
without requiring or fabricating an active execution lease. The registration
operation requires a lease; host firing validation precedes creating one.

### Async schema and state transitions

Add a separate `authority_state` (`active`, `suspended`, `quarantined`) and bounded
reason code to jobs/schedules, rather than overloading existing job execution
states (`pending`, `running`, `succeeded`, `failed`). Persist origin kind
(`function`, `operator`, `legacy_unknown`), creator function UUID or operator
principal reference, target UUID, creation revisions and authority generation.
Schedules also gain a monotonically increasing configuration revision.

- The scheduler selects only pending/due rows with active authority. Claiming
  work does not authorize execution: final admission rechecks policy, row
  authority generation and schedule revision after any worker wait.
- Authorization denial changes pending work to suspended, with a reason; it
  does not increment execution attempts or consume a retry backoff. An admitted
  execution may record its normal failure/cancellation while future retries
  remain suspended. Transient policy-store failure defers without dispatch;
  it is not an ordinary user-code retry or a permanent denial.
- Admin resume explicitly revalidates origin, target and current rights before
  reactivating pending work. Policy expansion alone does not automatically replay
  old suspended jobs or missed cron firings. Cron resumes at the next future
  scheduled time, without catch-up. Terminal jobs are not reopened by resume.
- Creator/target deletion suspends pending work with a missing-resource reason;
  restoration/recreation by name cannot reactivate it. Quarantined rows require
  explicit admin adoption/reissue; adoption records old and new provenance.
- Retry keeps origin, creator, target and idempotency scope. Admin reissue creates
  a new job identity and records its source job; no hidden elevation via retry.
- Uniqueness for new idempotency records is origin kind + creator identity +
  target UUID + key. Legacy target-only entries stay isolated in their legacy
  generation and cannot satisfy a new-origin request.

Preserve the existing target-only `idx_jobs_idempotency` unchanged. Introduce a
new `job_idempotency_v2` table with unique (origin kind, creator identity, target
UUID, raw key), expiry and job UUID. New-origin job inserts leave the old
`jobs.idempotency_key` NULL and create the v2 record in the same transaction;
lookups use v2 exclusively. Thus legacy rows/index cannot collide with new
creators and no DROP/index rewrite is needed. API serializers read the raw key
from the appropriate generation, never expose another creator's lookup. Test
concurrent equal keys from different creators, legacy collisions, expiry and
transaction rollback without orphan reservations.

#### Deleted-target retention refinement — coordinated core accepted, public controls pending

Keep existing target foreign keys and additive migration rules. Target deletion
must preserve metadata-only history for pending/running jobs and all schedules,
covering the existing explicit cleanup as well as cascades. History is not an
executable suspended row: it cannot be claimed, resumed or adopted. Preserve
original UUIDs/provenance, state/reason and bounded timing/configuration metadata,
but not payloads, secrets or environment data. Creator deletion with a surviving
target still suspends live work and retains the original creator UUID.

Use unique `(work kind, original work ID)` tombstones without resource foreign
keys. Bound the archive to 100,000 rows and 64 MiB of canonical serialized
metadata, not physical SQLite/index bytes. Ninety days is purge eligibility,
not automatic expiry. Explicit audited admin purge is required; never silently
evict history to make a function deletion fit.

Reserve capacity before marking a function deleting, using the ordered function
mutation lock, IAM gate and synchronous DB transaction. Capacity refusal leaves
function/work state unchanged. Close admission at that publication boundary;
persist reservation/progress and archive/delete each bounded batch atomically.
Retries must match existing tombstones and cannot double-charge reservations.
Purge must not consume reserved capacity. Callback signalling and process
retirement remain outside IAM/owner locks.

Explicit admin reissue supplies and reviews fresh payload/configuration and an
exact current target UUID, creates a new work ID and operator origin, and audits
the old-to-new provenance change. It never revives the archived ID, resolves a
same-name replacement automatically, or reuses an old idempotency lookup.
Inspection requires read; purge/reissue require live admin and confirmation.
Restore preserves history as inspection-only and does not restore authority.
This is a deliberate retention/privacy refinement of target-deletion suspension,
not proof that the current async candidates implement it.

For migration, a nonempty existing `enqueued_by_function_id` is evidence of
function origin but still requires review and remains suspended. An absent
creator is not proof of operator origin. Historic cron rows have no reliable
creator distinction; all become `legacy_unknown`/quarantined until adoption.
This is deliberately disruptive and must appear in preflight output. Both
existing boolean states receive empty policies; only former true functions get
the additional review-required label. New functions have empty policies without
a migration label. No historic job/cron authority silently becomes admin-owned.

## 5. Revocation and concurrency contract

Current policy is evaluated host-side on every operation, including warm workers.
No policy snapshot copied into a sandbox can serve as authority.

Use one process-local authorization RW gate in the first single-host release;
avoid multi-resource policy-lock ordering until measurements justify sharding.
Operation admission holds the read side; policy publication holds the write side.
Never retain it during pool wait, protocol I/O or function execution. For calls:

1. Resolve target UUID, acquire read side, check current caller/target policies,
   register an owned cancellable operation as waiting, then release the gate.
2. Acquire the worker using that context. Acquire read side again, reload target
   status and both policies, revalidate caller lease, and check cancellation.
   Verify that both caller worker and acquired callee worker match their own
   respective approved artifact/config revisions. Scheduled dispatch performs
   the same acquired-target check; selecting a target before rebind does not
   authorize an obsolete worker generation.
3. Atomically mark that registered operation admitted while still holding read
   side. Release gate; submit to Dispatch/DispatchEx with its cancellable context.
   This mark is the authorization linearization point, not user-code startup.
   Dispatch must refuse an already-cancelled context before protocol submission.
4. Keep registration until response/stream completion and always remove/release
   it on error. Registration indexes caller UUID and target UUID, including
   target acceptance revision, so either side's revoke can cancel it.

Policy write side waits for admitted short database transactions to finish,
commits new revisions and publishes their immutable snapshot, then marks affected
registered waiting/admitted operations cancelled before acknowledgement. Invoke
child contexts propagate cancellation into callee work and nested calls, not
unrelated invocations of that function. Cancelling a stream closes its owned
handle; close remains permitted for cleanup. Revoked work does not resume after
a later grant. A narrow implementation may cancel every registered edge touching
the changed policy, including otherwise still-allowed edges; document this
conservative behavior rather than claiming selective cancellation.

KV/job/cron bounded writes retain read-side admission through the synchronous
transaction; batch authorization happens before any read inside that transaction
and all statements use the same protected policy snapshot. No writer job may
acquire the gate while another thread holds it waiting for that writer.
Optional spans/log telemetry differs: admission authorizes emission and reserves
a bounded immutable envelope; optional persistence may happen later or be shed.
Revocation does not promise to remove telemetry already accepted for emission.

A successful revoke means no later admission uses the old revision; admitted
short DB work finished or aborted; affected queued calls/streams have been
marked cancelled. Each stream frame delivery reauthorizes and hands off its
bounded result under read-side admission; a frame handed off before revocation
may physically reach the caller afterward. No later handoff uses old authority.
Acquire frames with `NextFrame` outside the gate. Then take read side, recheck
policy/lease/registration and transfer the bounded frame to the SDK reply buffer;
drop it if denied. Release before writing the protocol reply. No socket/frame
read or blocked channel send occurs under the IAM gate; handoff uses bounded
pre-reserved storage and fails rather than waiting for a slow client.
Cancellation of already executing callee work is best effort, not rollback:
already returned bytes, stored data and external effects cannot be recalled.
Current invocation may continue non-SDK computation; its next protected SDK
operation uses current policy. Bootstrap is host-recognized and ends at readiness,
not by an invalid/stale-lease downgrade. Later unbound calls deny; code overlapping
a current execution may use that execution's lease.

Persist policy changes with expected revision (conflict on stale write), publish
the immutable in-memory snapshot before acknowledging, and bound cache entries
to live function UUIDs. Unavailable/invalid policy fails closed, distinct from an
ordinary deny. Crash recovery loads committed state before admitting work.
The lock order is build/rollback lock (if needed), function mutation lock (if
needed), IAM gate, database transaction. SDK operations take only the last two.
Policy mutation takes function mutation lock before gate; never gate then
mutation/build lock. Registry publication happens within that ordered mutation.
Cancellation signals under the gate must be nonblocking; stream teardown, pool
release and writer drain happen outside it. Cache miss loads cannot take a
reverse lock. Prove this ordering with gated queue/stream/DB-writer tests.
Database internals retain their existing lifecycle-lock→write-connection order
after entry from IAM; lifecycle/writer callbacks must never acquire IAM or
function mutation locks. The optional telemetry writer does not reauthorize on
commit/retry: it persists or sheds the immutable, already-admitted envelope,
whose decision revision is provenance. Retry cannot reinterpret the envelope
under a new identity. Thus no IAM gate is acquired while holding SQLite/writer
resources. Dispatch/DispatchEx is always outside the gate, including response
head waits and asynchronous stream setup. Session locks only protect local
handle state; copy/detach state before acquiring IAM or invoking cancellation.

Preserve the existing build-lock→mutation-lock discipline. Builds persist only
build-owned outputs against current config. Policy rows never appear in source
snapshots. Rollback, clone, import, template application and build failure cannot
publish old policy. Admin-only protection must be rechecked at commit/admission,
so a write-only deployment queued before a grant cannot later replace newly
privileged code. Policy grants and privileged mutations share this ordering.

A function is privileged if it has any outbound grant, any inbound acceptance,
or retained/quarantined authority awaiting review. Protect inbound-only services
against payload capture by ordinary code writers. Apply the predicate to all
code/config/secret/trigger mutations, deletion, replacement and restore adoption.
Clone/import creates a new UUID with empty policy, empty inbound acceptance and
no inherited durable operator work. Importing authority is a separate admin
review, not part of copying source or retaining a familiar name.

Grant activation is bound to an immutable host-owned artifact/config revision:
active deployment identity plus execution-relevant configuration and secret
version. Worker identity records that revision at spawn, not from SDK arguments.
Admin grant approval records the exact reviewed revision under mutation lock;
if it changed since review, save conflicts. Every protected operation compares
the worker's revision with the policy's approved active revision. Older busy or
retired workers cannot gain fresh authority, even though they can finish ordinary
computation. Privileged deploy/rollback/config publication requires admin review
of the new revision and explicitly rebinds the policy in the same ordered
transaction/audit event without importing snapshot grants. Old generations lose
protected admissions; the new generation uses current grants. Policy-only
reductions apply live without enabling any previously ineligible generation.
Readiness/bootstrap accesses also require the approved revision; no bootstrap
exemption for old code. No generic code-attestation framework is introduced.
Policy PUT carries both expected policy revision and expected reviewed
artifact/config revision. Compare both at commit under mutation lock and gate;
an intervening deploy, rollback or config edit returns conflict rather than
rebinding the approval to whatever code happens to be active then.

Normalize an authenticated operator context across REST sessions, keys, OAuth
and MCP: immutable principal kind/ID, credential reference and current permissions,
never the secret token. Missing/unresolvable identity denies mutations. Pass this
context to shared service-layer checks; middleware/tool registration alone is
insufficient. A queued deployment persists its submitter reference and function
authority epoch, not a bearer or frozen `admin` flag. Re-resolve current credential
validity/permissions at publication under mutation lock and admission gate.
Revoked credentials or an expired session/OAuth grant cannot publish queued code.
Synchronous upload/rollback/config changes use the same commit-time check.

Any policy/acceptance grant change increments the authority epoch. Before an
admin adds grants, show/approve the current active deployment identity and
pending-deployment list; epoch mismatch invalidates pending code publication,
even if its submitter was previously admin. The submitter must explicitly
resubmit against the new epoch. This prevents an admin grant from unexpectedly
trusting code that was still compiling. Non-admin mutations of currently
privileged functions deny even if they queued before the grant. A revoked-and-
regranted policy does not make an old epoch current again.

Secrets already injected cannot be erased by IAM revoke. Secret removal still
requires worker retirement and possibly credential rotation; document exactly
when a busy worker loses access. Do not promise IAM revocation recalls secrets.

## 6. Storage, API and compatibility

Implementation note (2026-11-18): the internal opt-in
[`workloadrecovery` fixture](../backend/internal/workloadrecovery/README.md)
composes guarded backup installation, external marker and file-backed quarantine.
Normal boot/router/restore-handler activation remains off. Its bounded process
crash and syscall-fault tests qualify only that fixture, not power loss, real
runtime lifecycle, kernel fencing or coordinated serving cutover. The normative
requirements below still govern that later cutover.

Additive SQLite tables: current policy/acceptance, bounded policy audit history,
and durable async provenance fields. Do not drop the old `sdk` column. After
cut-over it is non-authoritative; API writes to it return actionable validation
errors rather than secretly resetting scopes. REST create/update retain a
decode-only presence detector for deprecated `sdk`: any occurrence, including
false/null, returns `400 DEPRECATED_FIELD` with the IAM replacement. It is not
an active input or silently ignored unknown field. MCP rejects it at schema
validation; CLI rejects `--sdk` with replacement guidance. Test all three paths.
Document the breaking transition. Policy is retrieved separately
from source/deployment snapshots and included as read-only summary on functions.

Proposed REST surface: GET/PUT function IAM with expected revision; optional
admin evaluate/preview endpoint. Same policy shape in MCP and CLI policy-file
commands. Read access can inspect policy metadata for resources the operator can
already inspect; admin alone mutates. Unknown targets return generic deny to
functions; operators receive actionable policy validation errors without values.
Missing, inactive and unaccepted targets return the same function-facing
authorization failure. Denials expose only the caller policy revision, not the
target acceptance revision or existence. Authorized operator diagnostics can
identify which check failed under their existing resource-read authority.

Migration writes policy rows and marker in one idempotent transaction. Both
legacy boolean states start without active grants; old true rows are marked
review-required with their prior coarse capability noted, not expanded into
all current/future targets. Existing KV/source/history remain intact. Historic
function-origin work is suspended pending explicit adoption; unknown provenance
is quarantined. No network-mode inference, source scanning as authority, or
automatic regrant on restart. Provide dry-run affected-function/work report and
exportable suggested policies, with breaking upgrade notes before any release.

The supported restore entry point, not the restored database, establishes
quarantine before service admission. The application restore writes a recovery
marker outside the archive after quiescing admission and before database
replacement, then stages the database; boot reads
that marker before workers/scheduler start and commits a new quarantine epoch.
Manual recovery must create the same marker using the documented recovery
command before starting Orva. A restored database cannot reliably detect its
own rollback; directly replacing files without this procedure is outside the
quarantine guarantee. Filesystem administrators remain trusted.

Quarantine covers workload policies/inbound acceptance and all durable jobs/cron,
including operator origin. A pre-IAM backup first migrates then quarantines before
admission. Marker removal follows successful quarantine commit; interrupted
restore repeats idempotently. Partial reapproval survives normal restart, but
only individually approved policy/work revisions are active; no global clear
operation implicitly approves the rest. Test crash points before/after staging,
quarantine commit, marker removal and each reapproval, plus normal restart that
must not invent a restore or reapprove suspended work.

## 7. UX, audit and documentation

Replace SDK checkbox with a link to the dedicated Permissions page: current
stored-access summary, clearly distinguished from active authority; explicit
actions; own-KV prefix rows; target name with stable ID; caller/trigger acceptance;
revision and last-change actor. Missing/deleted targets remain visible as broken
grants, never rebound by name. Save policy separately from Deploy; source dirty
state/Reset/Discard cannot edit IAM. Non-admins see policy read-only and why code
editing is restricted. Broadening grants shows a review diff and confirmation.
Templates suggest a policy for review, never silently grant it.

The owner rejected the initial authority-sheet UX after hands-on use and chose
an AWS-style guided policy builder. Default to current stored access with one
Edit/Create action; guide service → actions → resources → permission review,
then review the complete draft before Save. Keep current policy and unsaved
changes separate. Canonical JSON belongs under Advanced, not in the required
first-use path. Retain Orva's design system and phone/desktop action parity.

The same policy inspection, validation, update, review/revocation and history
operations must be available through REST, CLI and MCP, backed by shared
server-side authorization, CAS and audit behavior. The in-product Chat UI
remains supported; the owner separately requested removing terminal `orva chat`.
External agent workflows use MCP rather than requiring a CLI chat client.

Denials: stable code, action, safe resource identity and policy revision; no keys'
values, bodies or secrets. Audit grants/revokes and bounded/sampled denial events
with dropped-event counters. Successful high-rate operations use fixed-cardinality
metrics, not unbounded per-key labels. Define retention, byte and rate limits.

Update together with implemented behavior: API/CLI help + docs, SECURITY,
ERRORS, OPERATIONS, DEPLOYMENT, TESTING, CHANGELOG, README, backend contracts;
`docs/reference.md` and its MCP/dashboard/CLI byte-identical generated copies;
`frontend/src/views/Docs.vue`, frontend `aiPrompts.js`, backend AI prompt, MCP tool
schemas/hints; public Astro `origin/web` docs and other SDK mentions. The prior
UX review's unrelated `/home/dev/firstfinger` website conclusion is discarded.
Public site branch is `web`, verified through Git. Source publication and actual
deployment verification are separate; do not claim deployed docs changed merely
because source or generated copies changed.

## 8. Staged PR and acceptance gates

### Completion scope authorized 2026-11-17

The owner authorized completing the integrated implementation, including reusable
named policies and function attachments, and delegated unresolved product choices
to independent decision review. This supersedes the earlier planning-only scope
for reusable policies; it is not evidence that they are implemented.

UI, CLI, external MCP and in-product AI must use the same authoritative service.
Acceptance includes actual create/attach/update/detach/revoke workflows, effective
access with source attribution, live enforcement, negative authorization/CAS
cases and matching audit outcomes across surfaces. The approved guided UI remains
the baseline: desktop/mobile, both themes, keyboard, loading/error/empty/read-only
and conflict states require rendered verification, not source inspection alone.

Current delivery checkpoints, not substitutes for the complete gates below:

- The isolated configuration mutation candidate adds an opt-in shared service for
  environment, execution settings and encrypted secret upsert/delete. Exact
  source/function/generation/policy/epoch/ciphertext CAS and live operator checks
  precede one durable deny-first commit. The old actual Factory is invalidated
  under IAM before ACK; cancellation/retirement/event callbacks follow outside
  owner locks. A fresh strict snapshot with the same concrete code/runtime leases
  is separately installed, then explicitly reviewed against current aggregate
  grants. Optional HTTP/MCP/shared-AI adapters are not normally registered.
  The sealed Node/Python fixture covers real public invoke/private SDK,
  failed-COMMIT host/cache preservation, corruption denial, old busy SDK denial,
  new config/secret visibility and explicit review. Independent acceptance remains
  pending. Queued deploy/rollback, source clone/import, trigger/other writers,
  complete lifecycle and boot/restore/recovery remain separate cut-over gates.
- The committed direct-Factory KV workflow has scoped independent acceptance.
- The isolated public-pool KV candidate has independent browser, correlation,
  HTTP-negative and package/race/vet acceptance. Three independent fresh-root
  generation-replacement race runs now pass. The original setup collision and
  failed third-party-bearer expectation remain recorded; the corrected probe
  proves both platform credential stripping and intentional bearer forwarding.
- The isolated buffered-call candidate has scoped independent acceptance for
  both Node/Python directions. Twelve calls correlate public/SDK/target/child
  records and policy audits: eight denied calls have no child execution rows;
  four allowed calls have exactly one. This proves handler-dispatch denial, not
  that acquiring a cold target cannot import module code. KV regression passes.
  Exact verified source is integrated. Fresh UID-1001 server/UI builds,
  canonical lint/tests, six-package race tests, frontend lint (one unchanged
  Editor warning), 106 Node tests, 240 component tests and scratch UI build
  passed in a disposable VM; its removal was verified. Stream acceptance followed
  separately, as recorded below.
- Reusable-policy semantics have independent decision review as recorded below.
  Management parity, production lifecycle wiring, async provenance and
  boot/restore qualification remain incomplete.

No push, tag, release, production/website deployment or production testing is
authorized by this completion request. Source-level coordinated cut-over still
requires the complete acceptance gates; a working scratch workflow alone does
not authorize activating a partial API.

### Reusable-policy decisions — staged core and management surfaces accepted

- Named definitions have immutable versions. Attachments pin a version; editing
  creates a new version without changing existing attachments. Impact preview
  and explicit reviewed rollout replace pins on selected functions.
- Inline source, attachments and compiled effective authority are separate.
  Effective authority is the allow-only union with source attribution. Removing
  one source must expose residual grants rather than falsely claiming access
  was revoked. The compiled union is never an editable inline draft.
- Bound the first implementation to 25 distinct attached functions per policy
  across all versions, eight named policies per function and 25 owners per
  operation, in addition to existing aggregate policy limits. Over-limit input
  rejects; no hidden partial rollout or automatic batching.
- Multi-owner changes require sorted function mutation locks and one IAM
  publication boundary around a single durable transaction. Recheck the entire
  affected set, live operator, CAS and reviewed artifact expectations before
  commit. Publish every effective snapshot together; failed commits publish
  nothing and cancel no registrations. A loop over single-owner publication is
  not atomic rollout.
- Global revoke is terminal for the named policy ID and removes only its
  contribution from every attached owner in that bounded batch. Existing inline
  and other-policy authority remains. Reuse per-owner admission/cancellation
  rather than add a global authority dimension. Replacement requires a new
  policy and explicitly reviewed attachments.
- Detach/revoke with unavailable current artifacts may only remove known
  sources, recomputing a provable subset while preserving the prior approval.
  This is not permission to submit an arbitrary edited policy under a
  reduction label. If subset proof is unavailable, deny without claiming
  successful revocation.
- Own-storage/execution selectors resolve to each attached function. Exact
  foreign targets retain UUID identity; prefixes explicitly cover future
  matching resources, never future functions. No roles, nested policies,
  arbitrary template parameters or deny language are added.
- UI, REST, slim CLI, external MCP and in-product AI share the same service,
  strict input decoding, live authorization, CAS and audit behavior. Actual
  two-function workflows and negative/adversarial cases must prove equivalence.

### Coordinated acceptance gates

1. **Design freeze:** independent consolidated review; resolve recommendations
   in §2 and pin schemas/limits, error/state vocabulary and lock ordering.
2. **Policy foundation:** pure evaluator, inert additive storage, migration
   preflight and protected code-write primitives. Until the complete cut-over,
   no public policy mutation route/tool is registered and no boot migration
   activates IAM rows. Tests prove empty or populated inert rows cannot bypass
   existing enforcement. Foundation PRs must not expose a half-enforced API.
3. **Synchronous enforcement:** universal channel, own KV including prefix/query
   fixes and atomic batch, calls/streams, live policy checks/cancellation.
4. **Async lifecycle:** provenance, idempotency isolation, delayed/retry/firing
   checks, quarantine/restore, generic deny/audit and resource deletion cleanup.
5. **Authoring surfaces:** CLI/MCP/AI policy controls and incumbent UI, policy-save
   separation, templates, admin/readonly/error/mobile states. Docs ship per PR.
6. **Cut-over qualification:** Node/Python real sandbox and browser acceptance,
   migration/reboot/restore and privileged-deploy races, model-facing docs tests,
   public `web` documentation change and branch-specific build checks.

Stages 3–6 are integration branches, not independently activated migrations on
`main`. One final cut-over PR atomically enables IAM migrations, universal
channel, synchronous/async enforcement, authoring interfaces and removal of old
boolean semantics. The build must never offer an active IAM API while scheduler
or channel operations still bypass it. Branch-level CI/tests support review;
the cut-over PR and its exact merge commit are the release-candidate boundary.
Old deployment snapshots containing `sdk` are tested both ways: neither true
nor false changes current policy/channel availability when promoted.

Each merge requires scoped independent Vouch evidence and exact-commit CI; verify
the merge commit on `main`, not just the PR. Use disposable verification trees
and sandboxed services; no tests against production. No tags/releases without
separate owner approval. Keep `web` as the website branch.

Minimum scenario matrix:

- No grants/import works; every action denied; valid exact grants succeed.
- `%`, `_`, case, Unicode, empty prefix, forged/reused cursor; no range leakage.
- Mixed-authority batch leaves zero mutations/results; CAS/incr lack read denied.
- A→B→C service authority; caller/inbound combinations; self-depth cap.
- Rename vs delete/recreate, inactive targets, queued target deletion.
- Revoke during pool wait/open/next, nested streaming, cancellation, cleanup cap.
- Module read allowed only with explicit bootstrap right before readiness;
  absent/stale leases deny afterward, while active-lease background overlap is
  explicitly not isolated from the handler.
- Job enqueue/retry/delay/reissue, creator deletion, cron edit/disable/revoke.
- Write-only create/grant/deploy/rollback/env/deps/secrets/import/clone attempts.
- Gated build and policy update ordering; failures, stale revisions, crash reload.
- Acquired/queued targets across deployment/rebinding; both worker revisions
  checked, including scheduler targets. Revoked submitter credentials prevent
  publication of queued builds at commit.
- Inbound-only and quarantined protection; old busy code cannot gain fresh grants.
- Optional writer retries vs revoke; no database/IAM lock inversion or lost audit.
- Import-time timer overlapping a handler demonstrates worker, not task, authority.
- Legacy true/false migration/reboot; source rollback leaves policy unchanged.
- Restore pre-IAM and IAM backups; no pre-approval work; preserved source/data.
- Manual restore marker and interrupted/partial reapproval; direct file-copy limit.
- Node/Python API behavior, UI keyboard/mobile/admin-readonly, CLI/MCP roundtrip.
- Example code really runs; all embedded references match; `web` has no stale
  boolean/HTTP/egress claims; live state distinguished from source state.

## 9. References and non-goals

### Implementation record

#### Spawn binding refinement — scoped design reviewed, not implemented

The current pool captures function configuration but spawns through `current`
and fetches secrets afresh. Neither a captured function row nor a fresh
function-ID lookup proves the actual worker artifact. Before connecting pooled
IAM, implement and independently verify this refinement:

- Maintain a durable, host-generated artifact-generation UUID per function.
  Rotate it transactionally with execution-relevant code/config changes and
  secret mutations, not cosmetic metadata edits. Keep policy revision separate.
  Secret rows and their generation must be read in the same snapshot; do not
  derive a public secret fingerprint from plaintext or put secret values in
  audit records. Existing map-only secret lookup is insufficient.
- Capture one immutable spawn snapshot: owner UUID, concrete validated
  `versions/<code_hash>` directory, authored/run entrypoints, execution-relevant
  function config, exact injected secret snapshot and generation, and trusted
  runtime/rootfs/adapter identity. Clone referenced maps. Unknown or mismatched
  components deny IAM; never fall back to `current` or a new function-ID lookup
  while stamping an old pool closure.
- Derive the host artifact identifier from that generation and runtime identity.
  A provider must return the complete snapshot actually used to spawn, not an
  approval string alongside independently refreshed config or secrets. Runtime
  identity must remain stable for its lifetime and change with packaged runtime
  or adapter content; interpreter version alone is not enough.
- Capture/publish using build/rollback lock (when applicable), function mutation
  lock, IAM gate, pool lifecycle, then synchronous DB resources. Never acquire
  the function mutation lock from inside `poolMu`. Slow snapshot reads and
  preparation occur before gate admission and are revalidated before publish.
  Secret writes must join this boundary across REST/MCP/internal callers.
- Publish active/current-generation observations and approved policy snapshots
  together. Busy old workers retain their original artifact and cannot inherit
  approval for a different generation. Retired late spawns never become usable.
  Restore quarantine remains independent and mandatory.

Required proofs: deploy/rollback racing an old spawn; secret mutation without
mixed-generation injection; code/config/secret changes invalidating old approval;
runtime/adapter identity changes; and mutation/pool lock ordering plus retired
spawn rejection. This is a proposed implementation refinement, not existing
storage behavior or proof of a production-authoritative generation service.

Source-only independent architecture review found this refinement scoped
design-ready subject to these publication contracts; no new owner decision is
needed:

- Policy approval compares expected policy revision and expected reviewed
  artifact against the authoritative current generation/runtime identity under
  mutation lock plus IAM gate. Storage's `ExpectedApprovedArtifact` compares the
  previous policy approval only; it is not the current-artifact check. Privileged
  mutations commit function generation, explicit policy rebind and audit together.
- All secret mutation callers share one ordered service operation. Exact
  snapshots fail on any read/decryption error, unlike the current best-effort
  getter that skips corrupt rows. Persist ciphertext plus secret/artifact
  generation together; publish and retire obsolete pools before acknowledging.
- The committed generation and concrete version path are authoritative, never
  `current`. Failed DB commits publish nothing. A post-commit cache/activation
  failure closes protected admission until reconciliation; it cannot restore an
  old approval over changed inputs. Filesystem preparation stays outside the gate.
- Runtime identity names the actual tree throughout spawn. Sampling a digest and
  later resolving a mutable path is insufficient: pin the tree or exclude
  replacement through an explicit stop/drain protocol. Identical-content rollback
  still creates a fresh generation; restore invalidates prepared snapshots and
  requires quarantine/reapproval.
- Bound prepared snapshot count/bytes and stale retries. Concrete code/runtime
  lifetimes must exclude replacement/GC while owned. Memory/CPU accounting derives
  from the same snapshot as worker spawn. Release secret-map references on
  retirement/failure/completion without promising reliable erasure of Go strings.
  Detach/mark retirement under locks, then cancel/kill/perform I/O outside them.
  Import-time bootstrap must deny obsolete generations before readiness, not rely
  solely on rejecting a late spawn from the idle pool.

The next internal seam is prepare → validate/commit → publish: prepare immutable
owned inputs outside pool/IAM locks, validate the expected identity under ordered
locks, atomically commit related durable changes, then publish bounded host state
and policy together. Serving wiring remains absent until the full proof gates.

#### Verified stages and current limits

- The named-policy management surfaces passed independent
  functional REST/slim-CLI/external-MCP/private-SDK checks, including pinned
  versions, reviewed rollout, residual-source detach/revoke and stale-owner
  rollback. Initial source-aware UI acceptance was blocked: both runtime captures showed
  ALLOWED through an enabled managed attachment while the empty inline card
  claimed no platform actions were allowed. Hidden effective JSON and an
  inline-only banner did not fix that contradictory summary. A separate
  correction added human-readable effective authority and explicitly scoped
  inline editing. Registry-only dispatch tests likewise did not establish
  actual AI-turn approval/resume. Both gaps received the follow-up checks below;
  verifier browser/services/VM cleanup was verified.
  Follow-up verification closed the original summary contradiction, but
  reproduced a second blocker: after concurrent revocation and a stale save
  returning 409, the SDK denied access while the effective card still described
  the prior grant as applying "now." Draft retention and disabled Save passed;
  they do not make that effective snapshot current. Conflict/ambiguous-failure
  states must explicitly invalidate authority freshness and restore current
  claims only from an owner-correlated authoritative response.
  Independent re-verification of the final conflict correction now closes that
  409 blocker: real Node/Python revocation produced SDK denial and stale-save
  409; authority became explicitly unverified immediately, then fresh GET showed
  no grants while the unsaved draft remained and Save stayed disabled. All
  106 unit and 269 component tests, lint and both builds passed. Final evidence
  uses a 36-file safe allowlist; rejected raw events containing unverified
  `generation.Token` fields were privately quarantined, not published or assumed
  harmless. Browser/services/VM cleanup was verified.
  The separate test-only deterministic-provider AI-turn increment now has
  independent normal and race acceptance: actual Manager/Runner/Bifrost/shared
  registry/service publication waits for approval, resumes exactly once, and
  rejects replay, rejected approval, false destructive confirmation, revoked
  pending credentials, forced read-only writes and strict-input violations.
  Its initially interrupted race run remains recorded; a completed follow-up
  passed and removed its disposable snapshot. This does not establish browser
  Chat, public-provider compatibility, actual worker identity in the AI fixture
  or production activation.
  The accepted surfaces, both UI corrections and AI-turn test are integrated.
  Independent merge-critical source review preserved accepted calls/session
  behavior. Fresh UID-1001 server/UI/slim-CLI builds, canonical lint/tests,
  policy/MCP/AI/CLI race checks, database regressions, 106 unit and 270 component
  tests, and both scratch UI builds passed. Separate real race-enabled calls
  and managed fixture modes passed; simultaneous flags were not qualified.
  The combined VM was removed. Normal router/boot/catalog activation and full
  E2E/cut-over remain off and unaccepted.

- Jobs44's timezone fixture and browser/distinct-function holds are closed by
  independent completion QA: all six timezone normal/race checks, hostile SDK
  boundaries, real Node↔Python jobs and retained-worker replacement denial pass.
  Thirty-one browser checks verify both real UI resumes, target SDK writes,
  immutable function provenance and one admin audit each. The earlier database
  race timeout remains recorded; the unfiltered rerun passed with an adequate
  15-minute budget. Older author no-output timeouts remain cause-unproved.
  Jobs is integrated. The verifier's test-only CLI
  environment-auth overlay is retained instead of credential argv.
  Cron CORE24 and final formatted CONTROL37 have scoped independent admission,
  real firing, schema-precision and four browser/runtime-combination acceptance.
  The cron verifier's initial wrong-cwd host launch failed isolation and was
  excluded; isolated reruns supplied acceptance. Aggregate jobs/cron integration
  now passes independent merge-critical source review and fresh combined checks:
  UID-1001 server/UI/slim-CLI builds, canonical lint/tests, unfiltered affected
  package race checks including database (611 seconds), 106 unit and 283
  component tests, and four scratch UI builds. Race-enabled real self-target
  and cross-runtime jobs and all four cron browser/runtime combinations passed.
  Only the existing Editor lint warning remains; the owned VM was removed.
  The first aggregate attempt failed the error-catalog scanner, which omitted
  actual SDK `IAM_DENIED` emissions. Its log remains; the scanner fix includes
  positive/negative regression cases, and the complete rerun passed.
  A new combined managed-publication/standing-job unit regression passed normal
  and three race runs: failed CAS cancels nothing, detach/revoke cancels before
  ACK outside owner/IAM locks, and residual inline grants remain effective.
  Full lifecycle, restore and coordinated production activation remain
  unaccepted; no production activation follows from these tests.

- Isolated HISTORY28 has independent scoped normal/race/vet, SQLite fault/auth
  and real Node/Python deletion/archive/fresh-reissue acceptance. The verifier
  rebuilt against the final source and observed new target SDK effects and
  operator-origin audits; old IDs and same-name recreation did not resurrect
  work. Its VM/worktree were removed and 42 safe evidence files hash-checked.
  The subsequent managed-deletion coordination initially failed its exact-byte
  Node lifecycle with `SQLITE_BUSY` during fixture schema staging after invocation
  had begun asynchronous writes; Python and independent SQLite CAS/fault/
  concurrency probes passed. The original failure is retained. A two-test-file
  correction stages schema/source setup before invocation and applies bounded
  per-connection SQLite pragmas. Independent re-verification passed three fresh
  dual-runtime processes (six subtests) without weakening lifecycle assertions.
  History, coordination and the fixture correction are integrated alongside the
  accepted Jobs44 timezone correction and controls, not silently substituted in
  earlier frozen-source verdicts.
  Independent merge-critical source review passed. Fresh combined UID-1001
  server/UI/slim-CLI builds, canonical lint/tests and full affected-package race
  checks passed, including database (623 seconds). Race-enabled real coordinated
  Node/Python history, distinct-function jobs and cron lifecycles passed; the
  combined VM was removed. That core-only run did not qualify public history
  controls; their later acceptance is recorded separately below. Restore,
  broader lifecycle and full E2E/cut-over remain unaccepted.

- History adapters/CLI/UI, the live controls fixes, advisory cron descriptions
  and the real-stream fixture now have scoped independent acceptance and are
  integrated. The combined 49-path snapshot passed clean locked frontend checks
  (110 unit and 312 component tests), UID-1001 server/slim-CLI builds, canonical
  lint/tests and eight affected-package race suites. All five scratch builds
  passed; only the existing Editor warning remains. Real Node/Python history
  profiles each passed 60 checks, including fresh operator-origin SDK effects,
  current-actor purge audits, canonical/reserved ledger reconciliation, resolved
  day/night themes, KeepAlive restoration and visible cron wording.
  KV/calls/streams correlation verifiers passed, stream controls were enabled,
  and no fresh handoff after revocation acknowledgement was observed. Separate
  managed-mode execution passed. Terminal UID-1001 verification matched all
  1,036 source hashes/modes; orchestration exited zero and cleanup was verified.
  Own runner permission/PATH and hidden-responsive-locator failures remain
  recorded; no candidate assertions were removed. The external purge-audit
  probe's corrected `actor.ID` path and stronger browser regression are retained
  in the repository. Screenshots with fixture payloads are excluded from safe
  evidence. Real oversized stream frames remain unit-only qualification.
  Normal serving/router/catalog/boot activation, restore, full E2E and broader
  accessibility/resource stress are not accepted by this staged integration.

- Span admission and immutable decision-revision provenance have now passed
  independent combined qualification on the integrated SDK regression baseline.
  Canonical lint/tests, unfiltered affected normal/race/vet, the full database
  race suite (612 seconds), actual SQLite retry/accounting probes and sealed
  Node/Python spans normally and under race passed. All 1,045 source entries
  and modes matched; the owned VM was removed. Deciding owner/revision remains
  private immutable queue metadata, not a durable span column or SDK claim.
  The supported-Python initialization and cron-fixture contention corrections
  also passed combined qualification and are committed separately.
  Queued-build submitter capture, remaining writers and boot/restore are still
  integration or cut-over gates. None of these
  scoped results activates normal serving or establishes full IAM acceptance.

- Configuration publication now has independent combined acceptance on the
  integrated span/SDK baseline. Exact source/ciphertext/epoch CAS, live
  source-aware operator checks, failed-COMMIT preservation, commit-to-Factory
  invalidation and separate fresh installation/review passed. Real Node/Python
  configuration and span workflows passed normally and under race, alongside
  independent adversarial probes. Canonical checks, unfiltered affected suites
  and the full database race (712 seconds) passed. Fresh locked frontend checks
  passed 110 unit and 312 component tests, lint and production compilation.
  All 1,056 source entries matched and owned resources were removed. Optional
  adapters remain inactive in normal serving; queued code, other writers,
  recovery, browser Chat and full lifecycle/cut-over remain separate gates.

- Conditional browser-session UUID capture has scoped independent acceptance.
  Once the explicit identity schema is installed, new session rows receive
  independent UUIDs transactionally; fresh and cached browser auth and AI
  contexts retain token-free provenance. Legacy-schema/NULL-ID behavior stays
  unchanged and clears inherited references. Full database/server normal/race
  and vet passed; independent focused identity/cache/revocation/commit-time
  denial checks passed, with its verification worktree removed. An initial
  missing ignored UI asset prevented server compilation; reusing that asset
  enabled tests, not a fresh UI build. Normal boot initialization, public IAM
  routing and full browser IAM acceptance remain separate gates.

- The isolated reusable-policy core has scoped independent acceptance for
  immutable versions, pinned attachments, separate inline sources, independent
  definition/membership CAS and bounded all-or-none multi-owner publication.
  Real SQLite probes verified 25-owner failed-COMMIT rollback, credential
  revocation/expiry, cancellation, exact-source/membership interleavings and
  reentrant cancellation callbacks. Full three-package normal/race/vet checks
  passed; the owned verification worktree was removed. Source-aware management
  surfaces, real SDK workflows, boot/restore and function-deletion integration
  are not established by this core acceptance. The core and conditional session
  capture are integrated; combined canonical lint/tests and focused four-package
  race checks passed. Existing ignored UI assets were reused for Go compilation,
  not claimed as a fresh UI build. The verification worktree was removed;
  cleanup inventory initially failed from its deleted cwd, then directory and
  Git-registration absence were verified from the main tree.

- The first live visible KV milestone now connects the unchanged approved UI,
  real request controller, authenticated HTTP adapter, policy transaction and
  Authority to actual Node/Python private-SDK workers. Independent browser
  journeys passed deny → UI grant → allow → UI revoke → deny on the same worker
  and artifact, matching audit revisions; phone/desktop captures had no browser
  errors or overflow and were shown in the conversation. Stale response/CAS/
  credential and controller lifecycle tests passed. This uses direct Factory
  dispatch in an explicitly labelled scratch shell, not serving-pool or full
  IAM integration. Production routes, boot schemas and SDK defaults remain off.
  The exact accepted patch is integrated. Fresh UID-1001 actual server/UI build,
  canonical lint/tests, frontend lint/tests and separate live-entry build passed;
  the combined-check VM was removed. This does not extend acceptance to the
  ongoing serving-pool integration.

- The internal two-store Owner now binds code/runtime roots to instance epochs
  with one durable commit barrier. Exact same-boot fencing gates committed-asset
  recovery; incomplete state denies and unfinished assets remain quarantined.
  Independent normal/race, physical pipelines, crash recovery, adversarial
  probes and final source checks passed, with the owned VM removed. The race
  crash replay was rerun manually after its driver timeout, not counted as a
  green original run. Canonical lint/tests passed on the exact overlay.
  ACL validation, cold-start
  budget capture, reboot, interrupted-transition repair, serving, restore and
  policy/current-generation publication remain open.

- Typed nested Domains now gate child creation/entry against every ancestor,
  preserve cleanup pins and integrate with actual managed Build through the
  mutually exclusive ParentDomain profile. Independent physical race tests,
  real Node/Python bridges, root-fencing of active builds/workers and partial
  construction/reclaim probes passed. The review also reproduced the old
  UID-sensitive artifactfs test failure; the separately accepted test-only fix
  is committed as 4902f2b without changing production behavior. Combined
  canonical lint/tests passed on the exact combined snapshot. Boot wiring, durable store association,
  recovery, resource stress and serving IAM remain outside this acceptance.

- Managed-output work now drives actual Build through fresh private staging,
  an exclusive build Domain and Fence/Check before inspection, Seal or Abort.
  Unresolved cleanup retains runtime/stage ownership and bounded capacity for
  explicit retry. Independent writer-fencing and real plain Node/Python
  Build→Seal→Factory→worker checks passed, but raw contained `..` archive paths
  normalized rather than rejected. The corrected producer rejects raw parent
  components; parent race suites under both umasks and canonical lint/tests
  pass. Corrected independent rejection/compatibility/metadata checks under both
  umasks, normal/race suites, vet and fresh physical Node/Python pipelines also
  pass, with original HOLD evidence retained and the owned VM removed.
  That initial qualification excluded dependency manifests and TypeScript and
  did not cover serving, durable publication, current authority or recovery.
  Host build hooks remain denied.

- Later managed serving, enrollment and portable restore extend that profile
  to npm/pip installs under the compiled egress policy and network-disabled
  TypeScript compilation. They use a pinned runtime and fence the build Domain
  before verifying the full output. Portable rebuilds use the retained,
  digest-bound source input, not the dependency or compiler output. Host build
  hooks remain denied. The seal limits accepted output; they are not a live
  aggregate disk quota during a build.
  The real-nsjail disposable-VM fixture in `test/managed-enrollment.py` ran
  pinned npm `semver`, pip `idna` and TypeScript through legacy deployment,
  enrollment, and new managed upload/install. Each invocation checked the
  expected body; TypeScript also checked the authored and executed paths.
  The fixture then rebuilt all six dependency deployments under a second
  Owner, verified source-only backup archives, and exercised same-Owner
  replay and quarantine. VM egress was deny-by-default, allowing DNS and
  resolved registry IPv4 `/32` addresses only on TCP 443. The first attempt
  exposed an invalid build-domain metadata entry; moving the retained source
  outside that metadata namespace fixed it. The dependency-free enrollment
  and restore fixture also passed in a separate no-network VM. This is not
  full installer or Docker E2E coverage.

- Internal builder runtime binding now captures one concrete generation at Build
  entry, retains a pipeline anchor and leases each command through cleanup.
  Syntax/install/compiler steps do not reselect mutable runtimes; full identity
  is recorded separately from ABI cache hashing and unproven managed cache hits
  deny. Independent normal/race suites, repeated adversarial probes, vet and
  real Node/npm/TypeScript and Python/pip pipelines passed; the VM was removed.
  Parent canonical checks exposed a fixture-mode mismatch under umask 0077.
  The fixture now explicitly sets the permissions declared by its manifest;
  verifier rules are unchanged and the original failure is retained. Parent
  race runs under both umasks and canonical lint/tests now pass. Independent
  fixture metadata, normal/race tests, repeats and vet also pass under both
  umasks, with exact implementation hashes unchanged and worktree removed.
  No serving constructor enables it.
  Captured Domain placement is not exclusive build ownership, writer fencing
  or sealed-code/current-generation publication.

- Optional sandbox Domain integration now enrolls Spawn, Execute and RunBuild
  with verified same-epoch jail placement and required-controller denial.
  Independent real-nsjail tests passed worker/SDK, one-shot/build descendants,
  cancellation, FD/lease ownership and fencing. Review found CPU truncation;
  exact millisecond conversion now rejects unrepresentable requests and real
  readback for 1.001 CPUs is `1001000 1000000`. Independent physical race runs,
  compatibility/vet and parent canonical lint/tests passed. One unchanged
  historical diagnostic retains a fixture-only failure because correct rejection
  now precedes its old unconditional no-error check; that red evidence and the
  replacement independent denial oracle are both retained. Serving, build-output
  writer fencing, pinned build runtimes, resource stress and recovery remain open.

- A fresh UID-1001 lifecycle-candidate build combined the independently reviewed
  factory, real-runtime harness and resource-capable fence with the accepted
  policy/context/stream baseline. Actual UI/server build, canonical lint/tests
  and frontend lint/tests passed; the owned VM was removed. Its frozen manifest
  explicitly excludes ongoing sandbox Domain integration. This is not full
  API/sandbox E2E, migration/restore or serving-IAM acceptance.

- The inactive frozen-generation factory now owns bounded config/secrets and
  concrete code/runtime anchors, issues same-generation one-use spawn plans,
  and exposes resource facts from the same frozen inputs. Prepared count/byte
  reservations remain charged through Spawn completion and release exactly
  once. Independent full package race tests and quota/lease/retirement probes
  passed, with source hashes and worktree cleanup verified. Those tests use a
  fake jail. Independent normal/race qualification now also passed with two
  real Node and two real Python workers, frozen inputs despite source mutation,
  distinct worker identities, quota release, retirement, private SDK/lazy imports
  and post-Wait leases; original runtime controls passed and the VM was removed.
  Current
  generation/bootstrap admission, provider budgets and pool wiring remain absent.

- Fresh frozen-candidate qualification builds the actual UI/server and runs as
  UID 1001, excluding concurrent factory and sandbox-integration work. Its first
  build/lint passed but canonical tests failed in the minimal Node image:
  Python-backed fake-jail fixtures had no `python3` interpreter. The original
  failures are retained; Python was installed only inside the disposable VM.
  The unchanged canonical `make lint`/`make test` and frontend lint/tests then
  passed, including 228 component checks. The owned candidate VM was removed.
  This accepts the frozen snapshot, not concurrent factory/jail-parent work,
  E2E behavior or serving IAM.

- `workloadpolicy` now joins registry mutation ordering, immutable IAM
  publication and the live-operator policy transaction. Replacement preserves
  quarantine/review flags; explicit review approves unchanged policy against
  exact current-artifact expectations. Total empty-policy revocation remains
  possible when current runtime facts are unavailable. Independent real-SQLite
  tests passed, including stale artifact during lock wait, queued revocation,
  cancellation and deferred COMMIT failure. Its current-artifact observer is a
  trusted atomic host contract, not an installed or attested serving provider.
  No public adapter or boot initializer is registered.

- Internal management decoders require exact authorable fields and positive
  signed-64-bit decimal revision strings, with nullable SHA-256 expectations.
  They reject duplicate/unknown host fields, coercion, malformed input and
  exceeded bounds. Shared token-free operator context captures authenticated
  API-key identity and MCP OAuth's validated stored audience; AI request
  contexts inherit it. Independent VM tests/probes passed for this decoder and
  capture slice. Browser-session UUID capture, queued-build capture and
  end-to-end management-tool wiring remain absent; cookie prefixes and
  audience-less OAuth do not become publication credentials.

- Staged streaming SDK admission now checks the acquired worker before binding
  or dispatch, retains registration through the stream and reauthorizes bounded
  handoffs. Independent testing found a nil-byte handoff followed by an
  oversized dynamic error. The corrected implementation serializes the actual
  SDKError before handoff and returns only the admitted copy; the unchanged
  overflow probe now yields a bounded generic denial. Repeated package race
  tests/vet and independent boundary/lease/lock probes pass. Real pooled
  artifact binding, nested-runtime IAM and serving remain separate gates.

- A scratch-only runtime producer authenticates reviewed release bases, checks
  fourteen exact packaging-link omissions and overlays hash-pinned current SDK
  sources. Emitted archives are imported into fresh stages and sealed against
  original producer expectations. Node/Python real nsjail readiness, private
  SDK, lazy imports and post-Wait lease retention were independently reproduced
  normally and under race detection. That review found a blocking FIFO input
  open before file-type checks; the producer now opens no-follow/nonblocking
  and rejects nonregular files. Corrected independent verification passed the
  unchanged FIFO witness promptly, 100 race repetitions of negative inputs and
  fresh normal/race Node/Python execution. Emitted archives and producer records
  matched previous hashes exactly; non-root artifactfs regressions passed and
  the owned VM was removed.
  This is not published runtime packaging or serving qualification.

- `workloadfence` adds a package-only durable cgroup epoch and pre-exec entry
  wrapper, serializes closure against starts, and mints opaque proof only after
  durable close plus kernel recursive emptiness. Worker and independent
  original suites passed real-kernel race repetitions. Independent additional
  probes/vet passed, including copied-handle closure, fresh owner-bound proofs,
  recovery ownership and durable-state denial; the owned VM was removed.
  Host ownership/no-reuse and in-epoch trusted command
  relocation remain prerequisites. Legacy nsjail's global cgroup parent is not
  integrated; no artifact recovery consumer or reboot adoption is enabled.

- The fence now has owned jail-parent capabilities and a process-local service
  budget captured from the exact current non-root cgroup before daemon-leaf
  relocation. Ordinary Create ancestor/sibling/root denials remain. Independent
  current-source race repetitions and adversarial probes passed controller
  readback, descendant fencing, copied capabilities, stale state and mount-alias
  rejection, with mount/subtree/VM cleanup verified. This is not resource-stress,
  real nsjail sandbox integration, boot wiring or recovery acceptance.

- Completion work after the approved UI now includes an internal
  `SaveIAMPolicyAsOperator` transaction: live credential validation, policy CAS,
  authority-epoch advancement and derived audit actor commit together, with a
  second credential check before commit. Existing persistence-only saves reuse
  the same transaction helper. This is not current-artifact verification or a
  serving management API; shared ordered publication and trusted request
  identity capture remain required. Parent targeted database race tests and vet
  pass. Independent targeted/compatibility race checks, vet and adversarial
  probes passed, including deferred-FK COMMIT failure, trigger-driven credential
  revocation, queued expiry/cancellation and stored-approval CAS. The full
  database race suite and parent canonical `make lint`/`make test` passed.
  The first test run exposed test-only scalar-function registration after
  opening SQLite; registration now precedes opening the fixture, with the
  original failing output retained.

- The inert spawn fingerprint now uses the v2 domain and excludes the generic
  function row version. Description-only edits increment that version, so it
  could not identify execution content without violating the cosmetic-edit
  exemption. Explicit execution fields, code/runtime store identities and
  durable generation/revision/epoch remain fingerprinted; positive row-version
  validation remains. Environment/secret invalidation still requires
  authoritative transactional generation rotation. The exact wire-format oracle
  is updated to v2, not removed; parent full package race tests and vet pass.
  Independent fingerprint verification passed targeted race checks, 32
  additional cases and vet, including cosmetic stability, positive-version
  rejection and execution/store/generation input sensitivity. This accepts only
  fingerprint construction, not authoritative current-artifact publication.

- `PublishWorkloadEnvironmentAsOperator` joins live operator revalidation with
  environment/generation/policy/audit publication in one transaction. It checks
  current API-key permissions/expiry, OAuth token and client scopes/revocation/
  audience/user/expiry, or live UUID-backed session/user/expiry; actor provenance
  is derived, not accepted as permission. References must be captured by trusted
  authentication and never supplied by function authors. Expiry is checked
  before mutation and commit; expiry during commit itself cannot be excluded.
  The original persistence-only method retains its behavior. Serving capture,
  live IAM/pool publication and normal session UUID writes remain unwired.
  Focused race tests and vet pass; independent acceptance is pending.
  Independent probes found mutation-time cancellation rolled back but omitted
  the operator-denial sentinel. Mutation/commit cancellation now preserves both
  denial and context identity. A trigger-driven cancellation regression checks
  complete rollback; twenty cancellation race repetitions and vet pass.
  Independent acceptance at `bd9fddc` passed: original full database race
  (472.695 s), original/fixed vet, unchanged cancellation repro and fixed
  cancellation/rollback/CAS probes. Full database race was not repeated after
  the narrow cancellation fix. Strict OAuth whitespace/duplicate-scope parsing
  remains a fail-closed compatibility consideration before cut-over. Original
  verification used task-owned transient systemd isolation; those units and
  namespaces were removed. Fixed checks used ordinary offline worktrees.

- Explicit `artifactfs.NewPersistent` adds bounded canonical metadata and ordered
  fsync/intent/tree-rename/commit publication. `OpenRecovering` verifies recognized
  committed content and inspects interrupted states, retaining accounting.
  Recovered stores cannot acquire, publish, retire or reclaim trees: releasing
  the old process lock does not prove old workers are dead. No approval or
  activation is recovered. The strict memory-only `New` behavior remains intact.
  Package race repetitions and vet pass; independent crash-state/durability
  review is pending. Fault injection is not real power-loss or live-worker
  integration acceptance.
  Independent review of `31ee397` found that failed verification could leak an
  oversized unverified manifest into cleanup metadata, blocking later recovery.
  Manifest authority now enters durable state only after verification; an
  interrupted Abort retains an untyped reservation/delete intent instead.
  Added a regression for failed Seal plus permission-denied cleanup; twenty
  package race repetitions and vet pass. Independent re-verification is pending.
  Independent corrected acceptance at `8aa6146` passed the unchanged real
  permission-error reproduction, twenty race repetitions and extra probes,
  including 220 subprocess-exit crash cases, 800 concurrent lifecycles and
  180 failure-stage cases. These are process/fault tests, not actual power-loss
  durability or surviving-worker-fence acceptance. No recovery activation exists.

- Stable browser-session UUID references are implemented as an inert additive
  schema/read seam. The explicit test-only initializer backfills independent
  host UUIDs in 128-row chunks with transaction-wide rollback, preserving valid
  existing IDs. It never loads cookie values during backfill. Token-free identity
  records can be resolved by bearer or UUID with live DB user/expiry checks and
  generic errors. Boot, login writers, middleware, actor/cache identity and
  permissions remain unchanged; legacy NULL-ID rows deny these new lookups.
  No hash-derived/prefix identity or new role model is introduced. Live trusted
  principal flow and commit-time authorization integration remain required.
  Independent scoped acceptance passed full database race (360.947 s), vet and
  focused/overlay probes. The first canonical run failed the pre-existing writer
  timing-ratio gate; that failure remains recorded. Owner-approved PR #176
  replaced the ratio assertion with committed inter-batch progress and monolithic
  negative controls, retaining absolute responsiveness thresholds and diagnostic
  ratios. Independent full database race and 75 idle/bursty-CPU invocations passed.
  Fresh combined session snapshot `78f2717` passed lint, canonical tests and all
  1,226 real-sandbox compatibility checks. PR #177 and merge commit `2996354`
  passed CI; task-owned test resources were removed. This does not activate
  session UUID writes or live authorization.

- The next inactive composition, `workloadspawn`, freezes a prepared generation/
  config/secrets snapshot with opaque code/runtime leases into a one-use spawn
  plan. Its host fingerprint includes durable generation and actual content/
  store identities plus non-secret execution configuration, never plaintext
  environment/secret values. Frozen generation maintenance must cover their
  changes before this identifier can be authoritative. Constructor validation
  precedes lease transfer; Spawn/Close consumption is shared across copied
  handles. Host-owned environment fields override supplied values. No pool,
  approval, current-generation or provenance service is installed.
  Direct tests now cover actual manifest/store-generation fingerprint changes,
  stable inputs, owner rejection without lease consumption, private-channel SDK
  attribution and Close/Spawn ownership races. Five package race repetitions
  and vet pass. Independent scoped acceptance at `b9c2dce` passed race, vet and
  44 validation/fingerprint subcases. PR #175 and its merge commit `cc13008`
  passed CI; the frozen candidate passed canonical tests and all 1,226 sandbox
  compatibility checks. Fake jails do not qualify real pinned-runtime execution,
  pool admission or live generation authority.

- Internal opt-in verified-path consumption now transfers opaque code/runtime
  pairs into sandbox execution, checks owner/runtime labels and chooses the
  concrete leased paths. Old copied preparer handles cannot release transferred
  leases. Failure releases ownership; workers retain leases until centralized
  post-Wait cleanup, not pool release or kill request. One-shot execution also
  owns its pair through completion. Nil preserves serving behavior. Host endpoint
  probes conservatively allow contained relative links only, rejecting absolute
  links before host traversal. Five refined artifactfs/sandbox race repetitions
  and vet pass; independent review and real-runtime/approval integration remain
  required. Fake-jail tests are not nsjail pinning or live IAM acceptance.
  Independent scoped acceptance at `33154ad` passed ten artifactfs/sandbox race
  repetitions, vet and extra ownership/one-shot/endpoint probes. `waitDone`
  precedes exit callbacks and lease release; Kill/Quit/waitDone are not completed
  cleanup fences. Real nsjail/pinned-package and generation-approval integration
  remain unaccepted.

- Stopped-container metadata inventory of the local artifactfs test image
  (`19e0ad4ad621…`) found 6,530 Node and 6,689 Python entries, including 156/160
  direct symlink chains, relative dotdot targets and symlinked target parents.
  This is metadata evidence, not content/provenance/xattr acceptance. The owned
  stopped container was removed.
- A runtime-only manifest resolver now expands chains and parent links in
  chroot-relative component order, including contained dotdot and absolute
  targets. It enforces 40-hop, component/path/step and cycle bounds without host
  symlink resolution. Code artifacts keep the original strict profile. Missing
  targets, escapes, file parents and directory cycles still deny. Package race
  tests/vet pass; independent acceptance and real-runtime profile qualification
  remain separate gates. Genuine dangling links and metadata blockers persist.
  Independent scoped resolver acceptance at `3f47321` passed 20 race repetitions,
  vet and 560 additional overlay subtest passes, including exact 40/41-hop
  boundaries and graph/component-order cases. Actual packaged content/provenance/
  metadata and runtime-consumer integration remain unverified.
  A static package test binary also passed five complete runs as root in a
  disposable network-disabled Debian microVM. Capability-xattr rejection ran
  and passed there (it had skipped on the host). The VM skipped its ACL fixture;
  host-focused ACL tests passed. The VM was removed. These complementary fixture
  checks do not establish actual packaged runtime metadata compatibility.

- The inert Linux `artifactfs` module owns new private staging trees, verifies
  complete bounded typed manifests against a trusted expected digest, atomically
  seals generations, and issues opaque code/runtime lifetime leases. Retirement
  stops acquisition; zero-reference reclamation and failed-cleanup retries retain
  accounting. Existing paths cannot be adopted. Exclusive trusted writers are a
  precondition, not hostile-writer isolation or release provenance. Its strict
  symlink/no-xattr/no-special-file profile may reject existing runtime trees.
  There is no crash recovery, packaging/installer/GC/sandbox serving integration
  or actual IAM runtime pinning yet. Five final package race repetitions and vet
  pass; capability-fixture coverage is limited by test-user permissions.
  Independent scoped acceptance at `bf848d5` passed 20 package race repetitions,
  vet and 20 overlay-probe repetitions for reservations, reclaim/release races,
  path/tamper/link/xattr and partial-pair failures. Capability fixtures remained
  skipped; hostile writers, mounts, crash recovery and existing distribution
  runtime compatibility were not accepted. Verification worktree cleanup is complete.

- The first durable publication seam is environment-only and fail-closed:
  `PublishWorkloadEnvironment` checks expected function version, artifact
  revision/epoch/generation and policy revision/stored approval, then commits
  environment, fresh generation, advanced policy counters, review-required state
  and bounded audit together. Retained policy/approval and quarantine are
  preserved, not rebound or cleared. Environment input is owned and bounded at
  1,024 entries/8 MiB JSON. No serving caller, live admin revalidation, host-state
  publication or pool retirement is installed. It is not active authorization.

- `PrepareArtifactSnapshot` composes the one-transaction generation/config/
  ciphertext read with strict decryption and owned output, checking cancellation
  before and after preparation. Errors expose no partial data. It runs outside
  IAM/pool locks and supplies no code/runtime path, approval or current-state
  guarantee; publication must revalidate generation. Serving callers remain
  absent. Focused race/vet and independent verification are separate gates.

- Inert artifact-generation storage now provides fresh UUID generations on
  create/rotation, exact revision/epoch/generation CAS, atomic explicit secret
  mutation plus rotation, and one-transaction reads of generation/config/secrets.
  Snapshot allocation is capped at 1,024 secret rows, 12 MiB combined ciphertext
  and key bytes, and 8 MiB environment JSON before loading those values. Missing
  metadata and malformed inputs deny without partial snapshots. Initialization
  remains test-only; legacy CRUD does not rotate generations. This is not yet
  authoritative for admission or atomic config/policy publication.
  Independent review caught lossy environment JSON decoding (null values and
  invalid UTF-8 could become altered strings). The snapshot now requires a
  duplicate-free string object, valid UTF-8 and paired Unicode escapes, rejecting
  malformed input rather than normalizing it. Added regressions and three focused
  race repetitions pass; independent re-verification remains required.

- Strict secret snapshot decoding is implemented as an inert preparation helper:
  canonical owner, consistent rows, unique valid keys, caller-selected bounded
  count/bytes, ciphertext pre-decode ceiling and all-or-nothing decryption.
  Empty snapshots avoid key loading. Existing serving secret lookup is unchanged;
  generation/rows must still come from one transactional DB snapshot. Package
  race tests and vet are required separately from independent acceptance.

- A staged buffered-F2F seam registers observed caller/target admission before
  pool acquisition and checks the actual acquired worker before execution binding
  or dispatch. A separate trusted target-state observer must supply current
  active/generation facts through bounded gate-held reads; missing observers deny.
  Failed admission releases the acquired worker healthy and finishes registration.
  Current pool spawns have no reviewed artifact revision, so pool-backed IAM
  calls still deny. Successful pool-backed authorization, genuine queued state
  changes, post-dispatch revocation and queued self-call cancellation remain
  unproved. Fake-jail helper tests are not end-to-end F2F IAM acceptance. Streams
  and other unintegrated operations still deny; serving options remain absent.

- Worker dispatch now refuses cancellation already observed at entry, after
  mutex acquisition or before lease publication/submission. Early refusal does
  not write frames, revoke another lease or poison a reusable worker. Focused
  tests cover cancellation/deadline, mutex wait, dead-worker precedence and
  reuse. This prerequisite does not make cancellation atomic with starting a
  dispatch; admission remains the plan's logical start boundary.

- Internal opt-in SDK-host KV enforcement is implemented for testing only.
  `WithIAM(nil)` denies; omission keeps legacy behavior. Fresh host identity and
  lease observations join the admission gate through DB completion. Whole-batch
  authorization precedes the transaction; literal listing binds bounded opaque
  cursors to namespace/prefix and reauthorizes every page. Unintegrated protected
  operations deny, except ownership-checked stream cleanup. No serving constructor
  enables this partial path; `IAM_DENIED` is its generic internal SDK denial.
  A full SDK-host race run passed after the test-policy correction; the first
  five-repeat command exceeded its 240-second shell budget (one run took 80s).
  Five complete race repetitions and vet subsequently passed. Independent KV
  acceptance at `a1e9b20` passed the full scoped race suites and additional
  authorization/cursor probes. Full real-sandbox E2E nevertheless failed Node
  span persistence once; the isolated unchanged module then passed 50 checks.
  This remains an overall compatibility blocker, not a dismissed flaky test.
- Investigation confirmed a Node `trace.span` fire-and-forget submission race
  with deterministic socketpair regressions. Its isolated fix awaits bounded
  channel ACK/failure while preserving wrapped results/errors and best-effort
  storage. Independent acceptance at `2d9cd5a` passed Node/Python channel tests
  and queue/timeout probes. Full sandbox qualification and exact-head CI remain
  required; this mechanism does not definitively attribute the earlier timeout.

- Design approval recorded on the feature branch; no rollout authorized.
- Literal KV cursor-range foundation added without changing active list callers.
  Race-enabled tests cover case, `%`/`_`, Unicode composition, NUL, scalar-boundary
  ranges, exclusive cursors, namespace separation and expiry. Generated range
  property checks compare with Go literal-prefix matching. This helper is not
  authorization; policy enforcement must precede its use at cut-over.
- Pure `workloadiam` parser/evaluator foundation implemented. Its package README
  pins the version-1 document and host integration seam; example documents are
  future IAM inputs, not accepted by current function APIs. Race-enabled package
  tests and vet pass. Independent evaluator acceptance at `a7d5478` passed race
  tests, vet, decoder fuzzing (754,097 executions), strict nested-field probes,
  source-alias mutation checks and README example execution. This accepts only
  pure policy decisions, not trusted host-context construction or enforcement.
- Independent literal-KV verification passed the full database race suite and
  vet, including an EXPLAIN check proving bounded function/key index lookup.
- No IAM boot migration, route/tool, channel default or scheduler enforcement
  has been activated.
- Inert storage implements strict policy persistence, revision/approval CAS,
  signed-range exhaustion checks, digest/actor audit retention and read-only
  legacy preflight. Its schema initializer is called only by tests. The stored
  approval comparison is not yet a live deployment/config check; service gates
  and filtering review-required/quarantined rows remain mandatory.
  Independent storage acceptance at `a6895e8` passed the full database race
  suite, vet and additional concurrent Get/Save/Delete and rollback probes.
- Inert admission/publication coordination registers waiting calls, rechecks
  acquired targets, authorizes bounded stream handoff and cancels affected work
  outside locks. It remains unconnected to workers/shared cores/storage. Parent
  review aligned its decoded chunk cap with the existing 256 KiB SDK limit.
  Independent acceptance at `a6895e8` passed ten race-enabled repetitions and
  seven additional concurrency probes, including synchronous cancellation-entry
  reentry and the marked-but-unsignalled revocation window.
- `make lint` and `make test` passed on the inert foundation candidate. These
  checks do not qualify migration, real runtime IAM or rollout; those gates
  await integration.
- Host worker/caller observation now carries immutable spawn identity and
  optional trusted artifact revision, monotonic readiness and the caller's
  original lease status. It preserves current SDK routing and stale-KV behavior;
  fresh observation/invalidation is not yet serialized under the IAM gate.
  Independent verification and integration qualification remain separate gates.
  The review's channel-less observation edge is explicitly classified as
  `no_channel`, not bootstrap/execution; only identity/readiness are observable
  there. This preserves the channel-less wire path and denies accidental use
  of incomplete metadata as authority.
  Independent host-identity and channel-less delta reviews accepted this seam;
  they do not verify real runtime IAM admission.
- A storage-to-admission snapshot helper validates retained records and compiles
  empty authority for quarantine/review-required flags without losing combined
  revision metadata. Focused race tests pass; no runtime loader uses it yet.
  Its independent focused follow-up passed five race repetitions and vet.
- Observed admission entrypoints construct fresh host snapshots under the read
  gate. `ChangeHostState` provides the matching exclusive atomic transition
  seam; neither installs lifecycle hooks or activates IAM. Parent race/vet
  checks pass, including observer-panic lock cleanup. Live host integration must
  use observed entrypoints, not cached context values.
- Optional worker lifecycle hooks now cover readiness, lease replacement/end
  and death atomics, with cancellation/session cleanup after the hook returns.
  Nil hooks preserve current behavior. A joined test binds real sandbox worker
  observation methods to the coordinator and proves bootstrap closure, live
  execution admission, stale-lease rejection and no replacement-execution
  upgrade. This is an in-process/fake-worker test, not live SDK enforcement.

### Dedicated permissions UI staging (2026-11-16)

`FunctionPermissions.vue` and its permission-specific components implement an
independent policy draft over a validated internal snapshot. They are not routed
or connected to a production API. Preview never widens capabilities, and save
or review events carry only policy and expected revision/artifact values.

Independent negative testing rejected the initial candidate despite passing
ordinary tests: metadata bounds/actor references, ABA conflict handling, review
retry, sparse arrays and loading/error empty-state copy needed correction.
Regression cases now cover those failures; the original independent probes are
retained in task evidence rather than weakened. Dirty conflicts preserve old
expectations until explicit discard/reload. Server CAS and authorization remain
mandatory; this advisory model is not an enforcement boundary.

Parent isolated Node 26 checks passed frontend lint/tests, the app build and an
explicit HTML entry build importing the otherwise-unreferenced page. The fixed
candidate passed 40 synthetic render cases across both themes and
375/390/768/1440px without horizontal overflow or browser errors, plus explicit
save/revocation, ABA/review-retry and phone History reachability checks.
Independent behavioral re-review first found three further ordering failures:
clean/adopted revision rollback and a matching snapshot preceding a delayed
conflict error. The corrected candidate passed all 30 unchanged independent
probes and four additional ordering probes, plus 98 Node and 181 repository
component tests in an independent disposable Node 26 VM. Parent canonical lint
and tests, explicit page build, and browser reproductions also passed.
Behavioral acceptance is scoped to the staged seam and its documented future
controller ordering/correlation contract, not live IAM. Visual review found no blockers;
effective touch regions, complete accessibility, real Layout/auth lifecycle,
live API integration and runtime IAM acceptance are separate gates.

The owner subsequently rejected the authority-sheet workflow as confusing.
The guided replacement now starts with stored access and explicit Edit/Create,
then service → actions → resources → permission review, followed by a separate
whole-policy changes review and Save. Canonical JSON is read-only under Advanced.
Fresh visual review required compact editing context, task-focused scrolling,
accurate stored-versus-draft copy and removal of the ambiguous Done exit; the
reviewer scored all three material findings resolved after recapture.

Independent behavior review caught six builder defects despite passing baseline
tests: implicit scope broadening, implicit all-key access, selected-action loss,
foreign caller cron acceptance, premature Save callbacks and busy discard.
A later check caught editing a retained foreign schedule target. All were
corrected without silently changing stored targets or tightening Go's structural
policy format. Unsupported retained entries are described honestly and cannot
be reauthored through the guided wizard.

Final component/model acceptance passed 67 independent checks, all 34 prior
state guards, 22 original model checks, and 105 Node / 211 repository component
tests. The old diagnostic witness asserting defective foreign-target behavior
was retained and remains red as expected; it is not an acceptance requirement.
Parent lint, production and explicit-page builds passed; 48 rendered workflow
states across both themes and 375/768/1440px showed no horizontal overflow or
browser errors. Additional browser negatives verified explicit scope intent,
startup correction, Done protection and the foreign schedule-target block.
This accepts the staged workflow, not live routes, controller correlation,
operator authentication, CLI/MCP policy controls or runtime enforcement.

The owner approved the guided appearance and requested flat sidebar navigation
plus responsive/keyboard QA without redesign. Independent browser testing
covered 336 state/viewport/theme checkpoints, then found picker Escape/focus,
sub-44px effective targets and overlapping stacked picker targets. These were
fixed in the shared controls: keyboard opening enters the menu, Escape restores
the trigger, fixed centered hit targets retain visible size tiers, and stacked
picker options take actual 44px row height. The touch probe now accounts for
padding-box pseudo-element positioning instead of overcounting border widths.
The flat drawer also closes with Escape and contains Tab/Shift-Tab focus.

Final independent live retest passed 76/76 assigned checks in both themes,
including actual edge-hit ownership. Isolated frontend validation passed lint
(one pre-existing Editor attribute-order warning), 106 Node tests, 228 component
tests and production build. Canonical `make lint`/`make test` and four browser
probe regression tests passed. Parent browser checks passed 13 service/caller
journey checks at both 390px and 1440px, plus nine Advanced/clipboard/error
checks. Error recovery models the future controller's required error clearing;
the synthetic save controller is not a production integration.

Evidence is retained locally under `/tmp/opencode/iam-full-ui-qa/` and
`/tmp/opencode/orva-flat-sidebar-qa/`. Prior HOLD reports and failing diagnostic
witnesses remain in the record. This is scoped Chromium preview acceptance,
not exhaustive WCAG, assistive-technology, Safari, production Layout/auth,
runtime IAM or full API/sandbox E2E acceptance. No release or production
deployment occurred.

Source integration map at baseline:

- `sandbox/sdkchan.go:dispatch`, `sandbox/sdkapi.go:SDKCaller/ExecInfo`: identity,
  context-phase classification and lease validation.
- `sdkhost/host.go:Handle`, `sdkhost/kv.go`, `sdkhost/invoke.go`,
  `server/handlers/sdkcore.go`: action/resource resolution and shared operations.
- `server/handlers/internal_invoke.go`, `scheduler/scheduler.go`: final call
  admission, async execution, streaming cancellation and provenance checks.
- `database/functions.go`, `database/deployments.go`, `database/migrate_sdk.go`:
  compatibility and policy/snapshot separation.
- `registry/registry.go`, `builder/queue.go`, REST/MCP function mutations:
  serialization and prevention of stale grant publication.
- `pool/pool.go`, `sandbox/worker.go`: universal channel and injected-secret
  lifetime, distinct from per-operation authorization.
- `server/middleware_auth.go`, `auth/principal.go`, MCP tool registration:
  operator grant administration and resource-specific privileged-code checks.

Repository-relative paths above are under `backend/internal/`. Reviews establish
source seams, not runtime IAM enforcement. The initial UX scout did not verify
the public-site branch; parent verification used `git ls-tree origin/web` and
`git show origin/web:src/pages/docs.astro`. Website checks should include the
existing `scripts/check-claims.mjs` gate, Astro typecheck/lint and build in an
isolated dependency environment.

OWASP Authorization Cheat Sheet: least privilege, default deny, every-request
validation. RFC 9396 illustrates structured action/resource authorization but
does **not** prescribe an internal OAuth implementation here.

- https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html
- https://www.rfc-editor.org/rfc/rfc9396.html

Deferred: arbitrary cross-function KV, general delegation/token exchange, tenant
RBAC inheritance, external policy engines, distributed enforcement, scoped
secret fetching and information-flow tracking. Revisit with concrete use cases,
not by weakening the initial service/identity boundary.

Acceptance note (19 November 2026, inbound webhooks): the managed listener also
serves attested, publication-gated `/webhook/{id}` and mounts inbound-webhook REST
from the same exposure service. Unit and HTTP tests and the disposable-VM
`TestManagedServingLive/inbound-webhook` run (real Node function, bad signature
refused, restore unpublishes, explicit republish) passed. Managed MCP/Chat route
and webhook tools, browser/CLI surfaces and production cut-over remain open.
