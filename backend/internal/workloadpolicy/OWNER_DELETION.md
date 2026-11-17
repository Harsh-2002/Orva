# Managed membership at history deletion

This internal opt-in extension of the History28 registry hook adds no route,
loader, migration or generic lifecycle framework. Normal construction stays off.
Set the history service's `Managed` and `Registry` to the same `NewManaged`
service/Registry/DB/Authority composition; binding mismatch denies. Without the
coordinator, history deletion refuses attached named sources rather than letting
the final FK cascade silently remove them.

The retiring owner's mutation lock is held on entry. A bounded owned plan reads
exact source and definition/membership/version/revocation CAS facts outside the
IAM gate. Only this owner's sources change, so surviving owners need no locks or
evaluator publication. Acquire affected definition locks in sorted UUID order
next. All managed mutations use the same order: sorted affected owners, sorted
definitions, IAM gate, DB. No owner lock is acquired after a definition lock or
from inside the gate.

One history-reservation transaction validates the plan and current administrator,
reserves exact canonical metadata capacity, removes only this owner's pinned
references, advances each affected membership once, appends `owner_delete` with
the shared managed-audit writer, and commits progress/deleting status. Only after
commit does Authority retire the UUID. Definition locks release before returning
the cancellation callback; Registry releases the owner lock before cancellation
or pool callbacks. Survivor inline/overlapping grants and authority epochs do not
change. Disabled/revoked references are removed while preserving terminal state.

The existing capacity-accounted deletion root retains the owner UUID/revision/
epoch, policy UUID, pin/enabled facts, membership before/after and audit digest.
It has no function FK and survives final function cascades until explicit
eligible admin purge. No policy body, artifact/generation value, payload,
environment, key or lease is added. The root remains bounded by 8 KiB and the
existing global canonical-byte/row ledger; ordinary managed-audit retention
remains 32 entries per definition.

Capacity/preflight/credential/COMMIT refusal rolls back everything without
retirement. Archive retries require the same root identity and zero remaining
attachments; they never recapture, double-advance or duplicate audit. Concurrent
mutations serialize or return ordinary CAS/missing-owner denial, without an
automatic grant retry. Same-name new UUIDs require explicit new grants.

This is a trusted internal lock/publication contract, not universal protection
against arbitrary Go/SQL bypass. Boot/restore, privileged/queued-build paths,
public history controls/UI and independent acceptance remain separate gates.
