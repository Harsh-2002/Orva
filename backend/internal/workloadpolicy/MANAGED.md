# Managed policies: internal publication contract

This is a tested **intermediate storage/service increment**, not the reusable
policy UI/CLI/MCP milestone or production IAM. Nothing initializes these tables
at boot, registers a route/tool, changes SDK defaults, or loads authority after
restart. No real sandbox/browser result is implied by the in-process tests.

## Host API for the next increment

Construct `NewManaged(db, registry, authority, observer)` explicitly. The registry
and authority must be the same instances used for host admission and artifact
transitions. Before construction, the test fixture initializes both canonical
schemas and loads each effective record with `AdmissionEvaluator`.
`initManagedIAMSchema` remains unexported and test-only. Initialization copies
existing inline policy JSON transactionally and idempotently. It neither empties
legacy inline grants nor translates `sdk=true` into a grant. Owners created after
initialization have no implicit source row and fail closed until explicit source
initialization. Production function-create/migration wiring is still required.

All managed mutations go through `Service.MutateManaged` with a trusted,
token-free `OperatorCredentialReference`, never one decoded from author input.
`database.IAMManagedMutation` is a **Go service input, not a public wire schema**:

| Operation | Inputs and behavior |
|---|---|
| `create` | Name and strict version-1 policy document; host creates a new UUID and immutable version 1. No owner or approval is fabricated. |
| `update` | Policy UUID, expected definition revision and strict document; creates a new immutable version. Existing pins, function revisions and authority epochs stay unchanged. |
| `attach` | Policy UUID, expected membership revision, explicit existing version, 1–25 exact owner expectations. Also performs explicit rollout for existing attachments. No follow-latest semantics. |
| `detach` | Policy UUID, expected membership revision and exact selected owner expectations. Removes only that policy source. |
| `revoke` | Policy UUID, both expected revisions and the **complete** attached-owner expectation set, at most 25. Permanently revokes the ID and disables every attachment atomically, retaining pins for inspection. |
| `delete` | Policy UUID and both expected revisions. Refuses while any attachment, including a disabled one, exists. Audit survives deletion. |
| `inline` | Exactly one owner expectation and a strict policy document. Edits the separate inline source, never the effective union. |
| `review` | Exactly one owner expectation, no document. Approves the exact recomputed aggregate against the exact current artifact; only this operation clears quarantine/review flags. |

Each owner expectation contains canonical UUID, combined revision, previous
stored approval, and expected current artifact. Internal unknown-current is the
empty string; internal unbound approval is `IAMUnboundArtifact`. Future wire
decoders must retain decimal revision/version strings, nullable artifacts and
strict duplicate/unknown/UTF-8/size/trailing-value rejection **before** lossy MCP
or AI decoding. Do not serialize the internal mutation as an inferred schema.

Definition revision advances on new version or terminal revoke. Membership
revision advances on attachment/rollout/detach/revoke, independently of version
creation. Definition update and attachment mutations CAS their own counter;
concurrent changes to the other counter are preserved, not overwritten or
spuriously rejected. Revoke/delete require both observations and the complete
affected set. Committed plan metadata reflects both current counters. No global
authority epoch exists. Every affected function advances its existing combined
revision and authority epoch once.

## Source inspection and composition

`GetIAMSources(owner)` returns the editable `InlineJSON`, separately attributed
attachments (policy UUID/name, pinned version, enabled/revoked state and retained
document), and `Record`, the effective aggregate. It verifies that recomposition
equals the stored projection. Out-of-band legacy writes therefore cause conflict,
not silent adoption. The legacy `HTTPAdapter` rejects a `NewManaged` service with
503 rather than handing its union to the old editable-inline draft contract.

`GetIAMManagedPolicy`, `GetIAMManagedVersion`, `ListIAMManagedPolicies` (100-row
pages), `ListIAMAttachedOwners` (25 maximum), and `ListIAMManagedAudit` (32 retained
digest/actor entries) are persistence inspection seams. Adapters must apply
trusted live read authentication, channel-token rejection and normal no-store
response rules before calling them. Storage methods do not authenticate reads.

The effective policy is inline plus enabled, non-revoked pinned versions, in
stable source order. Overlap remains visible; detach/revoke does not imply that
another source's equivalent grant disappeared. Existing total limits apply to
the aggregate: 32 KiB, 64 outbound statements, 256 targets/inbound callers.
Duplicates still consume these conservative bounds. Each policy has at most 25
distinct attached owners across **all** versions; each function has at most eight
attached policies. Disabled references count until explicitly detached.

Own KV and own execution remain owner-relative typed resources. Exact target and
caller UUIDs stay fixed; no names or future-function wildcard are introduced.
Prefixes intentionally cover future matching keys. Managed cron resources and
inbound cron acceptance currently **reject**: an attached-function self-selector
needs its own closed schema, not a fake UUID or creator-bound expansion.

## Publication and reduction

1. Preparation performs strict parsing, copies inputs, reads a consistent source
   snapshot, validates aggregate bounds and compiles candidate records outside
   owner locks and admission. Plans are opaque; returned previews are copies.
2. Acquire all affected registry mutation locks in sorted UUID order, then one
   IAM write gate. Recheck exact authoritative current-artifact observations.
3. In **one SQLite transaction**, recheck live operator authority, definition/
   membership CAS, complete revoke/delete affected sets and exact retained source
   snapshots. Persist source changes, effective records, per-owner audits and
   managed audit together; recheck credentials/expiry/cancellation before COMMIT.
4. Only after successful COMMIT install every immutable evaluator and mark all
   touching call registrations revoked. Release IAM and owner locks, signal
   cancellation callbacks, then acknowledge. Failed preparation/CAS/COMMIT
   publishes nothing and cancels nothing.

`PublishBatch` supports at most 25 owners and one persistence callback.
`PublishBatchDeferred` is the host seam for releasing earlier owner locks before
signalling contexts: its returned function **must run before acknowledgement**.
The managed service does this. Marked registrations already deny later admission
or handoff before callbacks run. Cancellation is conservative for every touching
edge, not rollback of previously returned data or external effects.

Detach, terminal revoke and replacing inline with an empty document are
source-removal-only operations. They recompute from the exact same retained
sources minus the selected source(s); callers cannot supply a purported smaller
aggregate. They preserve prior approval and quarantine/review flags, even when
current runtime state is unknown. Unknown state must be explicitly expected;
a stale nonempty artifact expectation still conflicts. Other edits require the
reviewed exact current artifact. Retaining approval is not evidence that a
worker is current: SDK current-generation checks remain separate and mandatory.

## Required next work

- Independently review this exact increment; do not integrate it as complete IAM.
- Freeze source-aware HTTP DTOs and strict shared raw decoding, then thin CLI,
  external MCP and in-product AI adapters with live read/admin gates and enforced
  destructive confirmation. The current mutation input is not that decoder.
- Add central guided named-policy UI, source-aware function attachments/effective
  access, and guarded request controllers without weakening incumbent draft rules.
- Prove real two-function Node/Python SDK enforcement through actual MCP transport,
  slim CLI and browser interactions with audit/epoch correlation.
- Implement coordinated function creation/deletion, boot loading, migration,
  privileged mutation and restore/quarantine wiring. Function deletion currently
  follows existing cascades; managed membership/deletion-audit lifecycle is not
  qualified. Async IAM, pooled generation changes and full cut-over remain separate.

Focused checks (installed toolchain/cache only):

```sh
GOPROXY=off GOSUMDB=off go test -count=1 \
  ./backend/internal/workloadiam ./backend/internal/database \
  ./backend/internal/workloadpolicy -run 'Test(PublishBatch|Managed)'
GOPROXY=off GOSUMDB=off go test -race -count=1 \
  ./backend/internal/workloadiam ./backend/internal/database \
  ./backend/internal/workloadpolicy -run 'Test(PublishBatch|Managed)'
GOPROXY=off GOSUMDB=off go vet ./backend/internal/workloadiam \
  ./backend/internal/database ./backend/internal/workloadpolicy
```
