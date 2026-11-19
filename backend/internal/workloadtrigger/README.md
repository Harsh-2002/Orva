# Opt-in public exposure mutations

Internal candidate; not registered by normal boot or the normal serving router.
Constructing `Service` requires the existing Database, Registry and Authority.
The exposure schema must be explicitly initialized by a scratch fixture; no
environment switch or migration activates it. Legacy handler/MCP dependencies
remain nil-off and retain their previous contracts.

The same service owns inbound-webhook create/update/delete and custom-route
create/rebind/delete across the optional REST handlers, external MCP and shared
in-process AI registry. The latter is registry dispatch, not a verified Chat
provider/approval workflow. Read/list/sign permissions are unchanged.

The MCP/shared-AI adapter preserves the legacy canonical `InboundWebhookView`
projection, including function label and nested relative trigger URL. Only
create returns the top-level signing secret; update returns the masked view,
delete the legacy operation result, and routes the legacy route result. The
display-label lookup follows successful authorization/commit, requires no new
READ capability and never gates the ACK. No request origin or actor is cached.
The common core and REST response envelopes remain unchanged.

## Authority and ordering

Bounded preparation resolves literal UUID owners and the existing route mapping
outside the gate. A rebind locks both old and new owners in lexical UUID order,
then checks the installed evaluator metadata under IAM and rechecks exact
durable source snapshots, owner status/version, mapping and exposure version
inside one write transaction. Missing owner/source/reference/evaluator denies;
there is no legacy fallback and no name adoption. No automatic CAS retry occurs.

WRITE or ADMIN can mutate a truly empty, review-free owner. Any outbound grant,
inbound SDK acceptance, retained attachment grant (even disabled/revoked), review
requirement or quarantine requires current ADMIN for **every** affected owner.
READ is independently required for inspection, not implicitly for mutation.
The unchanged CONFIG28 source-aware predicate is reused. Credentials are
token-free references from trusted auth only. API-key permissions/expiry,
session/user/expiry and OAuth grant/client/scope/audience/expiry are rechecked in
the mutation transaction, including a final pre-commit check.

Events run after owner/IAM/DB locks are released. Failed COMMIT produces no
mapping/version/audit/event publication. Audit is a separate immutable log,
retained to 32 entries per owner, with current actor, revision/epoch and a digest
of public mapping identifiers; it contains no signature secret or request body.
Per-key monotonic versions retain tombstones to reject delete/recreate ABA.

## Exposure is not SDK acceptance

The plan's epoch rule applies to policy/acceptance **grant changes**. These
operations modify public HTTP exposure, not `Policy.inbound` or grant sources.
They therefore preserve policy revision, authority epoch, approved artifact,
review/quarantine, the current Factory and already-admitted SDK authority.
They neither mint SDK grants nor reapprove/rehash code or injected secrets.

A pure exposure edit does not invalidate queued code solely by epoch: it has not
changed which authority the pending code would receive. A grant/acceptance edit
still advances the canonical epoch and invalidates prepared exposure requests.
Future queued publication must still compare that epoch and recheck live
credentials/current source-aware privilege under the same owner/IAM/DB order.
This package does **not** close the queued code/status/rollback/recovery gates.
It makes no claim that the legacy queued writer is production IAM-safe.

## Optional wire contract

See `docs/API.md`'s staged exposure section. Requests use exact decimal **strings**
for `version`, `policy_revision` and `authority_epoch`; no floating conversion.
Unknown/duplicate fields, invalid UTF-8/unpaired UTF-16, null fields, oversize
requests and operator/artifact claims are rejected. Maximum body: 32 KiB.

`StartManaged` initializes the opt-in schema and mounts route-only REST
inspection and mutations. The same InvokeHandler selects an exact or longest
published prefix with one version/owner/method-attested lookup. Publication
does not authorize invocation on its own. Raw rows and restored mappings are
not dispatched. Restore preserves mappings and CAS counters but clears their
publication; inspection distinguishes these states. No inbound webhook, managed
MCP/Chat registration, or legacy route list is added here.

Inbound ownership is immutable. Create generates a fresh UUID and signature
secret outside the IAM gate and returns the secret once, preserving the existing
response envelope. Get/list/update do not serialize the secret. Rotation remains
delete-and-recreate; no author-supplied secret field or secret export is added.

This draft's test evidence and limitations are in the external HANDOFF. Passing
focused tests is not independent acceptance, complete IAM or cut-over approval.
