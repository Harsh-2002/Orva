# workloadiam foundation (inert)

No production callers, boot migrations, routes, runtime changes or activation
are included. Inert database storage is a separate foundation. Existing SDK
behavior is unchanged. This package uses only the standard library.

## Version 1 document

```json
{
  "schema_version": 1,
  "allow": [
    {
      "actions": ["kv.get", "kv.list"],
      "resource": {"own_kv": {"prefix": "config/"}},
      "context": "bootstrap"
    }
  ],
  "inbound": []
}
```

All shown top-level/statement fields are required. Empty arrays mean no authority.
Exactly one resource member is permitted:

- `own_kv`: exactly one `key` (nonempty) or `prefix` (intentionally may be empty).
- `function`: `id`, an exact canonical lowercase non-nil RFC UUID (versions 1–8).
- `own_cron`: exactly one `name` or `prefix`, plus explicit `target_id` equal to owner.
- `own_execution`: empty object, current execution only.

Inbound entries are `{"caller_id":"<UUID>","triggers":["f2f","job"]}`;
`cron` is also supported, but only for the policy owner's UUID. Duplicate actions
or triggers reject. Rules union together, without deny rules or wildcard expansion.
Every string/key is case-sensitive; KV/cron matching uses literal UTF-8 byte
prefixes, with no glob, path, SQL-LIKE or Unicode normalization. List requests must
have their entire prefix covered by one prefix grant; exact keys cannot authorize
lists. Selectors are at most 256 Unicode code points, matching current KV validation
(up to 1024 UTF-8 bytes), without truncation. Cron selectors use the same bound.

Limits: 32 KiB JSON including whitespace, 64 allow statements, 256 combined function
resources/inbound entries (counting repeated entries), 10 actions per statement,
three triggers per acceptance. Nested JSON is checked against the closed typed
shape; nulls, duplicate/unknown/case-mismatched fields, unsupported versions,
invalid UTF-8/surrogates and trailing values reject. Owner-dependent checks happen
at compile time. `Decode` is mandatory for untrusted JSON: plain `json.Unmarshal`
does not provide these guarantees.

## Exported integration seam

- `Decode([]byte) (Policy, error)`; `Validate(Policy) error`.
- `Compile(Metadata, Policy) (*Evaluator, error)` freezes all slices/pointers.
- `Metadata{OwnerID, Revision, ApprovedArtifact}` is **host-owned**, outside JSON.
  Revision is one positive combined outbound/inbound/artifact revision. Monotonic
  storage/CAS publication is the parent's responsibility, not this pure evaluator.
- `Evaluator.Allows(Context, Request)` checks outbound rights; `Accepts(UUID, Trigger)`
  checks standing inbound authority. `AllowsEdge(caller, target, context, request)`
  requires both sides and exact target identity, returning only a generic boolean.
- `RequiredActions(Operation)` pins the operation matrix. Incr/CAS require both
  mutation and `kv.get` on the **same key**. Invoke, stream open and stream next all
  require `functions.invoke`; jobs use `jobs.enqueue`, cron `crons.upsert`, spans
  `spans.emit`. Stream close is not mapped: cleanup must use host ownership checks.
- `AllowsBatch(Context, []Request)` authorizes the entire bounded (1–100) KV batch
  before accesses, allowing only existing batch get/put/delete operations. Bootstrap
  batches therefore contain only gets. Empty/unknown/oversize batches deny.

`Request.FunctionID` addresses the owning function/namespace and must match the
principal and policy owner. KV non-list and cron requests use exact selectors;
list uses a prefix. Spans additionally require `Request.ExecutionID` equal to the
live lease's execution UUID; other requests must leave it empty.

`Context` is trusted host data, never SDK input. Each protected operation checks
worker identity and approved artifact even during bootstrap. Execution requires
readiness plus a freshly validated active lease bound to this worker and execution.
Bootstrap requires `Phase=bootstrap`, `Ready=false`, an entirely absent lease,
and explicit bootstrap get/list grants. Bootstrap grants do not imply execution
grants or vice versa. The host must keep readiness monotonic and stop admitting
bootstrap requests at readiness; a pure evaluator cannot reconstruct worker history.
Active-lease background tasks have worker authority, not isolated task identity.

## Inert admission/publication coordinator

`NewAuthority(maxRegistered, initial)` owns immutable policy snapshots and a
single RW admission gate. Its zero value denies. `Do` evaluates a short effect
under read admission through completion; calls/streams must use `BeginCall`.
Jobs/cron check inbound acceptance at creation, not eventual execution authority.

`BeginCall` registers a bounded cancellable waiting edge. Always defer `Finish`,
including on acquisition errors or parent cancellation. Pool acquisition uses
`Admission.Context` outside the gate. `Admit` takes fresh caller/lease and acquired
target state, verifies both approved artifacts and original identities, then
marks the operation admitted. Dispatch and all protocol I/O happen outside.

`HandoffFrame` rechecks an admitted stream and transfers at most 256 KiB of
decoded chunk data to already-reserved storage under admission; `NextFrame`,
base64/serialization and protocol writes happen outside it. The transfer callback
must not block. Handed-off data can physically arrive after a subsequent revoke.

`Publish(expectedRevision, candidate, persist)` holds exclusive admission through
synchronous persistence and snapshot publication. Failure/conflict leaves current
authority intact. A successful change marks every registered edge touching that
owner revoked, then cancels their contexts after releasing locks and before
returning. Revoked calls keep their capacity slot until `Finish`; later grants
cannot revive them. Stream owners still must close handles on cancellation.

Live host integration must use `DoObserved`, `BeginCallObserved`,
`AdmitObserved` and `HandoffFrameObserved` so fresh worker/lease/target snapshots
are constructed exactly once inside read admission, before registration locking.
The value-based forms are conveniences for already-fixed test contexts; a cached
context does not become fresh merely because evaluation happens under the gate.
Observers are bounded atomic reads, not DB lookups or worker-lock acquisition.

`ChangeHostState` takes exclusive admission for synchronous atomic readiness or
lease-state changes. Its callback does not cancel contexts, close streams, do
I/O or acquire earlier locks. Cleanup follows after it returns. This permits
future lifecycle hooks to share the publication boundary; it does not itself
install those hooks or validate their binding. Invalid/zero coordinators reject
transitions rather than run them outside the gate.

Callbacks are trusted host code, synchronous and bounded. They cannot reenter the
coordinator, acquire earlier build/registry locks, spawn work, do protocol I/O,
or await an IAM-acquiring writer. Lock order is build/mutation, IAM gate,
registration lock (if needed), then database lifecycle/write resources. Actual
source publication is not a callback responsibility. Host-state invalidation,
policy storage and current credential validation must use the coordinated
publication boundary in future integration; this coordinator does not create it.

## Not implemented / obligations before cut-over

Live storage/admin integration, quarantine, authority epochs and async attempt/
firing checks remain outside this package. Resolve names to immutable UUIDs before evaluation. Host
integration must check target existence/availability generically, current/acquired
callee artifact binding, depth limits, stream worker/lease/target ownership and
cursor namespace/prefix binding. `Accepts` intentionally takes no fabricated lease
and makes no claim about target status. Scheduler standing outbound authorization
needs its own future explicit API; do not invent a worker lease to call `Allows`.
Only perform batch transactions after the whole decision, using the same protected
snapshot. No evaluation result grants durable authority or proves live enforcement.
