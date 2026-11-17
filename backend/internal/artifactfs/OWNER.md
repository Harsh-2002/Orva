# One-instance persistent asset owner (internal opt-in)

This is a trusted-host lifecycle consumer, not serving/boot wiring, IAM approval,
restore adoption, current-generation authority or reboot recovery. Code/runtime
verifier profiles and the unbound persistent-store format remain unchanged.

## Association and trust

`NewOwner(ctx, OwnerConfig)` requires three empty private roots: owner metadata,
code and runtime. It takes all three exclusive directory locks and provisions
one root Domain under a caller-supplied process-local captured Budget. It never
captures a writable ancestor or moves any process itself. All three resource
controllers must be available. Root provisioning is privileged boot-owner work.

The owner root has a separate version-1 wrapper format:

- `owner.json`: canonical bounded association for the exact metadata-root
  device/inode, owner UUID, code/runtime root paths and device/inodes, immutable
  store limits, provisioned cgroup parent, finite epoch-history bound, and each
  exact epoch metadata UUID plus verified typed EpochReference data.
- `epochs/epoch-<UUIDv7>/epoch.json`: workloadfence's existing root-v1 records.
- `owner.intent` / `owner.json.tmp`: incomplete transition evidence. Reopening
  denies if either is present, or if any unknown file/directory is found.

No per-store epoch file is written: **one owner record binds both stores**.
Store `.artifactfs-control` stays version 1. There is no in-place adoption of
unbound stores, arbitrary trees, copied roots, swapped roles or restored records.

All directories are private, euid-owned and link-free; metadata is no-follow,
single-link, mode-0600, without xattrs. Owner JSON is capped at 64 KiB and parsed
with duplicate/unknown/null/canonical-encoding and allocation bounds. The
metadata/epoch directory enumeration is bounded too. Limits are immutable;
MaxEpochs must be 2..64. Old empty cgroups/metadata are retained, not silently
removed or reused. Reaching the history cap denies without creating another
epoch. These are logical allocation/work bounds, not filesystem quotas.

As with the underlying packages, ancestors, mount topology and privileged host
writers must be stable and trusted, with no outside entrants or inode reuse.
Neither paths, record fields, references nor flock release are proof of death.
Raw Budget provisioning remains privileged and outside Domain entry gates.

## Commit and capability publication

Before creating/mutating an instance association, the owner exclusively writes
and fsyncs `owner.intent`, then fsyncs the owner directory. Fresh stores and a
new root Domain remain private to construction. It writes owner.json.tmp by
exclusive no-follow create, exact write and file fsync; rename plus directory
fsync is the shared association commit. Held old-root proofs are freshly checked
again after that commit, before intent removal. Only after removing the intent
and fsyncing that removal can any Store/Domain reference be returned.

Any error exposes no bindings and closes constructor-owned descriptors. No
failed initializer deletes assets or cgroups. Partial directories/intent remain,
so repeated construction cannot grow an unbounded new set of unknown epochs.
A failed final unlink barrier attempts to restore the denial marker. The fault
tests model syscall failures, not actual power loss or a filesystem that lies
about persistence; complete visible committed state after a crash is distinct
from incomplete/unknown intent. No general interrupted-transition repair is
implemented.

`Bindings(ctx)` checks the still-owned association/layout/store identities and
current verified Domain reference before returning trusted internal references.
Use Code only for code Seal and Runtime only for runtime Seal; these bound roles
are enforced without changing unbound Store behavior. Every actual build must
use `OutputConfig{CodeStore: bindings.Code, ParentDomain: bindings.Domain, ...}`,
never Budget/raw Parent. RuntimeBinding uses bindings.Runtime; Factory plans
must spawn with this Domain or its typed child. These are trusted-host ownership
contracts, not confinement of a malicious host that deliberately bypasses them.
Do not independently Close the returned stores or Domain.

`PreparePaths` rejects pairs from different lifecycle owners, and bound/unbound
mixes; two unbound stores retain their previous profile. This does not authorize
an arbitrary host-selected spawn Domain. Trusted builder/runtime/spawn wiring
must still use the same returned binding triple; it is not hostile-host isolation.

## Same-boot recovery, in order

`OpenOwnerRecovery(ctx, config)`:

1. Holds the owner and both store locks, checks exact association/root/role/
   limit/layout identity, then opens the associated root epochs through the
   narrow `OpenRecoveringUnderBudget` constructor. Every fresh verified
   Reference must equal its stored association data. It rejects wrong boot,
   missing/replaced groups, corrupt records and mixed/unknown state.
2. Persists a transition intent and Fence/Checks the exact old roots under that
   valid captured budget. The root contains every typed build/worker descendant.
   No recovered tree is activated, removed or reclaimed before this proof.
3. Reads the stores in inspection mode **after fencing**, using the unchanged
   bounded persistent reader and full strict verification of committed/retired
   trees. Role mismatches deny. No observed-tree manifest is blessed as authority.
4. Creates a fresh root Domain and commits its single association to the same
   two verified stores before privately reconstructing acquirable committed
   records. Their generation/kind/owner/digest identities stay exact.
5. Stable committed records with no temporary metadata alone become acquirable.
   Retired, staging, publish/delete intents and pending metadata stay quarantined,
   charged and unmodifiable. `Quarantined()` returns store-role-labelled detached observations only;
   there is no quarantine-cleanup or automatic stage-activation API in this slice.

A durable committed asset may have existed before its producer received an
acknowledgement. Recovering that verified asset does not make it a current
deployment, restore a publication acknowledgement or approve its use.

The unbound `NewPersistent` / `OpenRecovering` contracts are unchanged:
unbound recovery always remains inspection-only. There is no exported recovered
mode switch, caller-supplied fenced boolean, alternate verifier or proof callback.
Private reconstruction is reachable only through this complete owner protocol.
Restored policies/grants/review flags are neither read nor compiled.

### Budget recapture prerequisite

Ordinary OpenRecovering still requires a strict child of current membership.
The budget-aware variant accepts only a live process-local CaptureBudget
capability and never restores Start. It does not manufacture an ancestor capture
from a record. After controllers are enabled, the no-internal-process rule can
prevent a cold replacement daemon from entering the old internal service budget
to capture it. Boot placement/recapture is **not solved here**. Cross-process
handoff tests capture the replacement's budget before delegation and move only
their own test processes. They do not establish production cold-start recovery.

## Shutdown and remaining gates

`Owner.Close(ctx)` denies new Begin/Seal/Acquire first, then Fence/Checks its root.
Outstanding leases (including factory/prepared/runtime anchors), live producer
stages, in-flight store operations, or attached child pins keep ownership busy.
Clean up producers/children explicitly, then retry. No IAM/pool lock participates.
Store locks are frozen together before release; a root Close busy result restores
cleanup access without dropping owner/store locks. Stale references remain denied
after successful owner shutdown. Committed assets are retained for recovery,
not deleted by Close.

After successful root detach, an internal terminal shutdown phase permits
descriptor-close retries without reopening or re-fencing a disposed Domain.
Detached stores remain denied throughout, including when a close error occurs
after the kernel has already released a descriptor.

Host producers must obey the existing exclusive-writer contract and cooperate
with shutdown: no API can revoke a previously returned path or host descriptor.
Always retain writable staging until producer work stops, then explicitly Abort.
Recovered quarantine has no live producer handle and remains charged on disk.

Lock order is owner serialization, independent store mutex operations, then
Domain work outside store mutexes. Root-to-leaf Domain gates never call a store
or owner callback. Store filesystem work remains outside store mutexes. Contexts
bound Domain gate acquisition/drain work, not a kernel filesystem syscall already
in flight. Owner-method serialization is synchronous; waiting for another owner
operation can outlast a waiting caller's deadline.

Before serving activation: independently qualify the complete real-runtime
crash/handoff matrix, design cold-start budget placement, durable deployment-to-
generation publication, restore-marker quarantine and policy reconstruction,
operator recovery procedures and any reviewed history/quarantine reclamation.
No public flag, boot caller, pool integration or approval restoration is present.
