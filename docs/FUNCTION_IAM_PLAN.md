# Function IAM — design proposal and implementation gates

Status: **design approved for staged implementation; not implemented or approved for rollout**.
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
Keys/prefixes use existing KV byte limits; never truncate to fit. All limits
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

Replace SDK checkbox with a Permissions section: effective summary; explicit
actions; own-KV prefix rows; target name with stable ID; caller/trigger acceptance;
revision and last-change actor. Missing/deleted targets remain visible as broken
grants, never rebound by name. Save policy separately from Deploy; source dirty
state/Reset/Discard cannot edit IAM. Non-admins see policy read-only and why code
editing is restricted. Broadening grants shows a review diff and confirmation.
Templates suggest a policy for review, never silently grant it.

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

- Design approval recorded on the feature branch; no rollout authorized.
- Literal KV cursor-range foundation added without changing active list callers.
  Race-enabled tests cover case, `%`/`_`, Unicode composition, NUL, scalar-boundary
  ranges, exclusive cursors, namespace separation and expiry. Generated range
  property checks compare with Go literal-prefix matching. This helper is not
  authorization; policy enforcement must precede its use at cut-over.
- Pure policy parser/evaluator foundation is in progress. No IAM migration,
  route/tool, channel default or scheduler enforcement has been activated.

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
