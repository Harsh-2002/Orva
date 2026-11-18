# Opt-in configuration publication

This Linux service is not constructed by normal boot. Its HTTP adapter is not
registered by the normal router. It requires the same registry/Authority/managed
pool, explicit generation and managed-policy schemas, a strict secrets manager,
and trusted concrete sealed code/runtime bindings for every enrolled owner.
Legacy configuration and secret handlers remain unchanged.

Environment, complete execution settings and secret upsert/delete share three
explicit steps:

1. Prepare owned configuration, strict decrypted secrets and concrete leases
   outside owner/IAM locks. Under owner lock → IAM gate → SQLite transaction,
   recheck the full function, generation, policy/epoch and exact source/secret
   rows, and resolve the live token-free operator reference. Commit configuration,
   generation rotation, policy/epoch advancement, review-required deny and audit
   together. Only after commit, invalidate the old actual Factory and publish
   the committed cache row. Release locks before cancellation, retirement,
   lease release or event callbacks, all before acknowledging success.
2. An administrator explicitly installs a newly prepared Factory from the new
   strict durable snapshot and the same concrete code/runtime bindings. The
   complete durable/source snapshot and live operator are rechecked under owner
   lock and gate. Pool installation remains deny-only. Failure leaves authority
   denied; repeat inspection/preparation, never reuse a failed stale candidate.
3. Use the existing source-aware `workloadpolicy.Service.Review` with the new
   **installed** exact artifact and current policy expectations. It reviews the
   aggregate current sources without replacing inline policy by the union.
   Quarantine persists through mutation and installation; only explicit review
   can clear it. A durable record/stored approval is never a current Factory.

Any outbound or inbound grant, retained attachment grant (even disabled/revoked),
quarantine or review-required state requires live ADMIN. Empty, unquarantined,
review-free authority preserves ordinary WRITE without requiring READ. This
write operation does not return the environment, ciphertext or secret names.
Managed policy read/review operations retain their existing requirements.
Credential validity, OAuth client/token scopes and audience, and session/user
existence/expiry are resolved by the existing transaction-time operator checks.

The adapter accepts bounded strict JSON, decimal-string revisions and full exact
expectations. It never accepts actor identities, approval booleans, store paths,
runtime/code leases or secret snapshots from an author. Errors are generic.

The optional MCP and shared in-process AI registry call `Service.Execute`, using
the same decoders and service methods as HTTP. They are absent unless the host
supplies `mcp.Deps.Configuration`. Delete-secret requires `confirm:true` and
carries the destructive annotation. In-process registry dispatch is not a
claim about browser Chat or a public provider turn.
Separate READ-only metadata inspection supplies exact expectations and actual
current Factory/code/runtime digests, not environment, ciphertext or secret
names. The live workflow obtains CAS through that HTTP inspection rather than
using database reads to manufacture an operator request. Writes do not gain a
READ requirement merely because inspection exists.

Every mutation compares original source documents and attachment pins, including
disabled/revoked retained grants, rather than editing an effective union.
Single-owner configuration writes do not mutate definitions or memberships;
managed attach/revoke writers already take this same owner lock before their
definition lock. Unchanged immutable named versions therefore need no new
definition-lock path here. Ciphertext corruption denies preparation, including
attempts to delete the corrupt entry; there is no implicit repair/bypass API.

## Limits and remaining gates

Busy workers may finish ordinary computation with old environment and injected
secrets still in memory. They cannot use protected SDK access after mutation
acknowledgment, and cannot be approved through the old current observer.
Revocation cannot erase a secret already read or exfiltrated: retire affected
workers and rotate external credentials when required.

This is not queued deploy/rollback, clone/import, trigger or runtime-upgrade
coordination. All those writers, complete lifecycle, boot/reboot/restore,
recovery and production activation remain separate gates. Do not construct this
service in normal serving until those writers share the publication boundary.
The runtime fixture proves only its explicitly enrolled sealed Node/Python
scratch workflow, not production packaging, cgroup hard limits or recovery.

## Qualification

Use `workloadspawn/RUNTIME_QUALIFICATION.md` to provision and authenticate the
public packages inside a fresh bounded disposable VM. Set that harness's package
directory/checksum variables and a fresh `ORVA_RUNTIME_RECIPE_OUTPUT` directory.
Then explicitly opt in with `ORVA_CONFIG_MUTATIONS=disposable-vm` and run
`go test -count=1 -v -run '^TestLiveConfigurationEnvironment$' ./backend/internal/workloadspawn`
(repeat with `-race`). Missing packages/checksums or runtime failures are fatal
when opted in; omission skips the live fixture. The test now covers environment,
settings, secret upsert/delete, failed COMMIT, corrupt ciphertext, callback
reentry, external MCP and trusted-context AI registry dispatch for both runtimes.
It does not export credentials, execution leases, database files or payloads.
