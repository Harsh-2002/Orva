# Internal policy-management core

The unregistered HTTP adapter accepts an optional authenticated `Targets`
metadata source. Only operators with read permission reach it; its bounded
ID/name list becomes resolved picker metadata, never grants or approval. The
embedding host must filter visibility and active/current status. Nil retains
the prior empty list. Duplicate, invalid or more than 256 entries fail closed
before mutation. The scratch calls fixture supplies actual database metadata
only for its current managed generations; no serving caller enables this seam.

This package is not registered with REST, MCP, AI, CLI or the serving constructor.
It does not initialize schemas, load authority at boot, attest runtime trees or
activate IAM. Public adapters must not be enabled before coordinated cut-over.

`Replace` and `Review` join the existing registry mutation lock, IAM publication
gate and operator-authenticated policy transaction. Persistence failure or
stale expectations publish nothing and cancel no registered calls. Successful
publication installs the immutable evaluator and conservatively cancels touching
call registrations before acknowledgement.

Credential references originate in trusted authentication, never policy JSON,
tool arguments, actor labels or a frozen permission bit. The database rechecks
live admin credentials in the policy/audit transaction. Public adapters still
owe their normal authenticated read/admin gates, wire validation and consistent
error mapping.

`ObserveArtifact` must read authoritative, pinned current code/config/secrets and
runtime facts under the same admission gate; it must not infer activity from
policy existence, stored approval or a fresh function-ID lookup. It performs
bounded atomic reads only. All updates use that same gate; no I/O, worker/pool
lock or earlier registry/build lock is acquired by the observer. No serving
observer is implemented here. `Known` in a test fixture is a trusted observation
contract, not proof of real pooled-artifact binding.

Replacement compares expected policy revision and stored approval transactionally,
and the expected reviewed current artifact under admission. Replacement preserves
review-required and quarantine flags. Only explicit `Review` can clear them,
approving the unchanged stored policy against the exact current artifact.
Old workers do not inherit authority bound to a different artifact.

A complete empty-policy replacement can revoke even when current artifacts are
unavailable. It retains the old approval token, grants no authority, preserves
quarantine/review state and still requires live admin authorization. Unknown
current state must be represented explicitly by an empty expected current
artifact; a nonempty stale expectation cannot bypass the observer.

Policy preparation reads are outside admission. The ordered commit revalidates
revision/approval and operator authority in the same transaction; all coordinated
mutations must use the shared registry and authority instances. This package
does not refresh function configuration by calling `Registry.Set`, which would
perform an unrelated second persistence operation.

Tests use the canonical staged database schema and real SQLite transactions,
with synthetic host artifact observations. They are not API, browser, scheduler,
restore, real-runtime or serving IAM acceptance.

## Internal wire decoder

`DecodeReplacement` accepts exactly `policy` and `expected`; `DecodeReview`
accepts only `expected`. The expected object requires `revision`,
`current_artifact` and `approved_artifact`. Revisions are canonical positive
decimal strings bounded by signed 64-bit storage, never JSON numbers. Artifacts
are lowercase 64-hex strings or explicit JSON null: null current means unknown,
and null stored approval maps internally to the unbound sentinel. Expectations
are CAS observations, not author-supplied approval.

Duplicate keys (including escaped aliases), unknown host metadata, malformed
UTF-8, trailing values and missing/null required objects deny. Policy keeps its
independent 32-KiB bound; the management envelope adds at most 4 KiB. Depth and
token work are bounded before constructing the request. Decoded policy bytes
are owned canonical JSON. This pins a decoder seam, not a registered REST/MCP
contract or a claim that ordinary MCP decoding retains duplicate keys.

The Vue page's internal event uses camelCase expectation fields.
`frontend/src/api/permissions.js` explicitly maps those to snake_case without
coercing revision strings or adding host authority claims.

## Unregistered HTTP adapter (scratch KV and buffered calls)

`HTTPAdapter` is reusable but is **not installed in the production router**.
The live scratch fixture alone binds GET/PUT
`/api/v1/functions/{id}/permissions` and POST
`/api/v1/functions/{id}/permissions/review`.

The caller must provide a trusted, live credential resolver. The adapter rejects
missing/channel/invalid operator references; all reads require explicit `read`,
mutations require both `read` and `admin`. The fixture resolves actual API-key
hashes/expiry from its scratch database on every request, without a cache.
Mutation commits additionally recheck live credentials inside `Service`'s
database transaction. This does not change normal REST or browser-session auth.

GET and mutation acknowledgements return a freshly read, camelCase snapshot
validated by `normalizeSnapshot`: function identity, decimal-string revision
and epoch, nullable current/approved artifacts, review/quarantine flags,
server-derived capabilities, stored policy, and at most 32 actual audit records
(digest/credential UUID/kind/time, never bearer material or historical policy).
Optional target metadata comes from a trusted, read-scoped host callback outside
admission. UUIDs, labels, duplicates and the 256-target bound are validated;
the callback owns visibility filtering. Nil retains an empty target list.
Metadata is advisory, never approval or admission authority. Responses are `no-store`.
`active` describes this adapter's enforced scratch host, **not** the production
instance. Only install it in a host whose SDK and transition hooks share this
Authority and whose observer describes the actual pinned Factory.

Invalid wire requests return 400, read/admin denial 403, stale CAS 409,
unavailable current artifacts 409, and internal read failures 500. The strict
decoders remain authoritative; no caller can submit actor or capability fields.
Review takes only expectations, never a replacement policy.

The controller queues refresh during mutation, cancels obsolete requests,
discards stale successes and errors (including owner A/B/A), and publishes
`saving=false` on a separate Vue tick before an acknowledgement snapshot. Errors
reach the page before queued refresh results. Unmount/deactivation cancel both
lanes; reactivation reloads. The presentational page and its guard tests remain
unchanged.

See `test/live-iam/README.md` for the explicit real-runtime scratch workflow.
It exercises actual managed pools and two-sided buffered calls, not production
pool activation or full SDK cut-over. Streams remain unqualified by this fixture.

## Named managed policies (intermediate increment)

`NewManaged` and `MutateManaged` add immutable versions, pinned attachments,
separate inline sources and bounded all-owner publication without activation.
The legacy HTTP adapter refuses this mode until its draft contract becomes
source-aware. See [MANAGED.md](MANAGED.md) for the host contract, caps, reduction
proof, cancellation ordering, tests and unfinished adapter/lifecycle gates.

`ManagedHTTPAdapter` now supplies explicit source-aware DTOs to the scratch
named-policy shell. `ExecuteManaged` is shared by that adapter and the optional
external/in-process MCP family; neither is production-registered. Strict raw
decoding, operation-specific schemas and live read/admin checks precede
inspection/publication. See `docs/IAM_MANAGEMENT.md` and
`test/live-iam/MANAGED.md` for the wire and actual-runtime workflow contracts.
