# Named Function IAM management

These interfaces are available in the opt-in Linux managed server and in the
scratch fixtures. Default serving does not mount the IAM routes, initialize the
IAM schema, or expose the IAM tools. See [the managed API](API.md#managed-server)
and [the enrollment runbook](OPERATIONS.md) for the serving boundary and limits.

The authoritative publication service is `workloadpolicy.Service`. All HTTP,
CLI, external MCP and in-product AI operations use its shared raw management
boundary. Readers require live `read`; mutations require live `read` and `admin`,
including both checks inside the final mutation transaction. Channel tokens
never qualify. Responses are `Cache-Control: no-store`.

## Wire rules

Requests are bounded by 36 KiB, including their envelope. Policies retain their
32-KiB bound; aggregates retain 64 statements/256 targets and callers. Duplicate
keys, unknown properties, malformed UTF-8, trailing values, host/actor/capability
claims and numeric revision coercion reject before information is discarded.
Counters and versions are positive canonical signed-64-bit **decimal strings**,
not JSON numbers. List offset alone permits `"0"` and is capped at 1,000,000.
Artifacts are lowercase SHA-256 strings or explicit null. Null current artifact
means unknown; null stored approval is the internal unbound state.

An owner expectation is exactly:

```json
{
  "function_id": "019aaa00-0000-7000-8000-000000000001",
  "expected": {
    "revision": "1",
    "current_artifact": null,
    "approved_artifact": null
  }
}
```

The UUID above is a **schema illustration**, not an authoring default or an
existing resource. Obtain real UUIDs and current artifact expectations from
authenticated function inspection. Never invent a function identity for managed
policy authoring. Expansions require the exact known actual current artifact.

## HTTP operations

| Method/path | Exact request fields |
|---|---|
| GET `/api/v1/iam/policies` | Optional query `offset` decimal string |
| POST `/api/v1/iam/policies` | `name`, `policy` |
| GET `/api/v1/iam/policies/{id}` | Optional query `version`, `history=true\|false` |
| POST `/api/v1/iam/policies/{id}/update` | `definition_revision`, `policy` |
| POST `/api/v1/iam/policies/{id}/attach` | `membership_revision`, `version`, `owners` |
| POST `/api/v1/iam/policies/{id}/detach` | `membership_revision`, `owners`, `confirm:true` |
| POST `/api/v1/iam/policies/{id}/revoke` | `definition_revision`, `membership_revision`, complete `owners`, `confirm:true` |
| POST `/api/v1/iam/policies/{id}/delete` | `definition_revision`, `membership_revision`, `confirm:true` |
| POST `/api/v1/iam/policies/{id}/impact` | `operation` = attach/detach/revoke plus that operation's exact fields |
| GET `/api/v1/functions/{id}/permissions` | No query/body fields |
| PUT `/api/v1/functions/{id}/permissions` | `policy`, `expected` (without function_id wrapper) |
| POST `/api/v1/functions/{id}/permissions/review` | `expected` only |

`policy` is the existing strict schema-version-1 document. Own KV and own
execution instantiate for each attached function. Function targets/callers remain
fixed UUIDs; names and future-function wildcards are not selectors. Literal KV
prefixes intentionally include future matching keys. Managed cron selectors are
unsupported and reject; inline cron keeps its existing self-only restrictions.

Named-policy inspection returns `policy` metadata (UUID, name, decimal-string
definition/membership/latest-version counters and terminal revoked flag),
selected `version` (`number`, policy, creation timestamp), `attachments`
(owner UUID, pinned version, enabled), bounded `history`, and `can_manage`.
List returns `items`, decimal-string `offset` and `can_manage`.

Function inspection returns `function`, `host`, `inline_policy`, `attachments`,
`effective_policy`, and `history`. Host includes decimal-string combined revision
and authority epoch, nullable current/approved artifacts, quarantine/review flags,
`can_manage`, and `enforcement:"managed"` (`"scratch_opt_in"` in fixtures).
Each attachment identifies its
policy UUID/name/version and retained document, enabled/revoked state.
**Only inline_policy enters the editable inline draft. Effective policy is
read-only.** History carries digest, actor UUID/kind, timestamp and decimal-string
revision/authorityEpoch. No bearer, secret or raw lease is returned.

Mutations acknowledge only after durable commit, all-owner in-memory publication
and cancellation signalling outside owner/IAM locks. Their response contains
policy metadata and affected owner UUIDs; inline/review return a fresh source
snapshot. Impact returns before/after effective policies per exact owner and
`preview_only:true`; it is not durable approval. Mutation repeats CAS and actual
artifact checks. Invalid input is 400, unauthenticated HTTP is 401, missing live
read/admin is 403, stale state/current artifact is 409, missing resources 404.

## Versioning, reductions and limits

Creating/updating a named definition grants nothing by itself. Update creates a
new immutable version and never changes existing pins. Reviewed attachment to an
existing owner is the explicit rollout mechanism. Definition CAS is independent
of membership CAS; every affected function still uses its own combined revision
and epoch. Requests allow at most 25 owners, each policy at most 25 attached owners
across all versions, each function at most eight attached policies.

Effective access is the union of separate inline and enabled, non-revoked pinned
sources. Detach removes only its source; overlap can retain access. Global revoke
is terminal for that policy UUID, atomically disables the complete bounded
attachment set and preserves all other sources. Delete refuses any attachment,
including disabled references. Replacement requires a new policy ID and explicit
review. Source-removal-only reductions recompute from exact retained sources,
preserve approval/quarantine/review state and can tolerate explicitly unknown
current artifacts; arbitrary author-supplied "reduced unions" are not trusted.

## CLI and agent contracts

The slim and bundled clients register the same `orva iam` family:

```text
orva iam policies list [--offset 0]
orva iam policies get <policy-id> [--version N] [--history]
orva iam policies create --request @file
orva iam policies update|impact|attach|detach|revoke|delete <policy-id> --request @file
orva iam permissions get <function>
orva iam permissions put|review <function> --request @file
```

Use `@-` for stdin. Inline argv bodies are refused; requests are bounded before
HTTP. Detach/revoke/delete require the normal interactive confirmation or `--yes`
**and** explicit `confirm:true` in the file. Raw body bytes are not rewritten,
preserving duplicate-key evidence for server rejection. Payloads print as JSON
on stdout, status/errors on stderr. Names are resolved only to real functions;
policy completion suggests stable policy UUIDs with names as descriptions.

The managed MCP and Chat registries expose one shared tool
family: `list_iam_policies`, `get_iam_policy`, `create_iam_policy`,
`update_iam_policy`, `get_iam_policy_impact`, `attach_iam_policy`,
`detach_iam_policy`, `revoke_iam_policy`, `delete_iam_policy`,
`get_function_permissions`, `set_function_permissions`,
`review_function_permissions`. Every tool uses an operation-specific closed
schema. Arguments are `{ "id": "<real UUID>", "request": { ...exact fields... } }`;
list/create omit id entirely. Duplicate/unknown input is validated from original
raw arguments on **both** transports, before generic decoding can normalize it.
Catalog visibility is not authorization. Destructive tools enforce confirm:true
in code; AI approval policy remains an additional independent gate.

The dashboard enables the Policies and per-function Permissions routes only
after the authenticated capabilities response reports `managed: true`.
The guided inline editor receives only the inline source and retains its
draft, conflict and review guards.

The managed function view starts with a human-readable effective outbound and
inbound summary and attributes retained grants to their inline or pinned sources.
Approval, quarantine, review and unknown/refresh states qualify active claims.
The separate **Inline permissions** editor labels all summaries and empty states
as inline-only; an empty inline source never implies empty effective access.
Standalone guided permissions retain their existing appearance. Creating inline
permissions and creating a reusable policy are explicitly different actions.

The effective/source inspector has its own owner-correlated read snapshot,
separate from the inline draft baseline. Every local mutation invalidates current
authority immediately without aborting its write lane. Failure, including a
409 or ambiguous network outcome, renders stale/unverified data and queues an
independent GET; only a matching non-obsolete GET restores current claims.
The error is rendered before this refresh. A refreshed inspector never adopts an
unsaved inline draft or clears its CAS failure: explicit reload/discard remains
required before saving against the new baseline.

See `backend/internal/workloadpolicy/MANAGED.md` for the storage/publication
contract. Boot loading, coordinated migration, function deletion, privileged
mutation, async provenance and restore/quarantine remain cut-over obligations.
