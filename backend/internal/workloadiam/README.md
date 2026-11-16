# workloadiam foundation (inert)

No callers, storage, migrations, routes, runtime changes or activation are included.
Existing SDK behavior is unchanged. The package uses only the standard library.

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

## Not implemented / obligations before cut-over

Storage CAS/revision monotonicity, quarantine, authority epochs, current policy
publication, admission locking, cancellation and async attempt/firing checks remain
outside this package. Resolve names to immutable UUIDs before evaluation. Host
integration must check target existence/availability generically, current/acquired
callee artifact binding, depth limits, stream worker/lease/target ownership and
cursor namespace/prefix binding. `Accepts` intentionally takes no fabricated lease
and makes no claim about target status. Scheduler standing outbound authorization
needs its own future explicit API; do not invent a worker lease to call `Allows`.
Only perform batch transactions after the whole decision, using the same protected
snapshot. No evaluation result grants durable authority or proves live enforcement.
