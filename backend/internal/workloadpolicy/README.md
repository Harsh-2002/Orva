# Internal policy-management core

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

The staged Vue page's internal event uses camelCase expectation fields; its
future controller must explicitly map these snake_case wire names without
coercing revision strings or adding host authority claims.
