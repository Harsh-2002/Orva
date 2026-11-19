# One-instance persistent asset owner (internal opt-in)

This is a trusted-host lifecycle consumer, not serving/boot wiring, IAM approval,
restore adoption or current-generation authority. Code/runtime
verifier profiles and the unbound persistent-store format remain unchanged.

## Association and trust

`NewOwner(ctx, OwnerConfig)` requires three empty private roots: owner metadata,
code and runtime. It takes all three exclusive directory locks and provisions
one root Domain under a caller-supplied process-local captured Budget. It never
captures a writable ancestor or moves any process itself. All three resource
controllers must be available. Root provisioning is privileged boot-owner work.

The owner root has a separate versioned wrapper format:

- `owner.json`: canonical bounded association for the exact metadata-root
  device/inode, owner UUID, code/runtime root paths and device/inodes, immutable
  store limits, provisioned cgroup parent, finite epoch-history bound, and each
   exact epoch metadata UUID plus verified typed EpochReference data. Unbound
   owners retain version 1. Opt-in host-bound owners use version 2.
- `epochs/epoch-<UUIDv7>/epoch.json`: workloadfence's existing root-v1 records.
- `owner.intent` / `owner.json.tmp`: incomplete transition evidence. Reopening
  denies unless the exact host-bound same-boot prepared transition below matches.
- `owner.prepared`: bounded, witness-authenticated digests of the predecessor
  and successor records, written after the signed successor file is synced.
- `owner.retire`: witness-authenticated predecessor/successor digests, exact
  oldest epoch reference and metadata digest for a separate retirement commit.

No per-store epoch file is written: **one owner record binds both stores**.
Store `.artifactfs-control` stays version 1. There is no in-place adoption of
unbound stores, arbitrary trees, copied roots, swapped roles or restored records.

All directories are private, euid-owned and link-free; metadata is no-follow,
single-link, mode-0600, without xattrs. Owner JSON is capped at 64 KiB and parsed
with duplicate/unknown/null/canonical-encoding and allocation bounds. The
metadata/epoch directory enumeration is bounded too. Limits are immutable;
MaxEpochs must be 2..64. Version 1 still retains old cgroups and metadata and
denies at the cap. Version 2 uses bounded retirement. These are logical
allocation/work bounds, not filesystem quotas.

As with the underlying packages, ancestors, mount topology and privileged host
writers must be stable and trusted, with no outside entrants or inode reuse.
Neither paths, record fields, references nor flock release are proof of death.
Raw Budget provisioning remains privileged and outside Domain entry gates.

## Commit and capability publication

Before creating an instance or appending an epoch, the owner exclusively writes
and fsyncs `owner.intent`, then fsyncs the owner directory. Fresh stores and a
new root Domain remain private to construction. It writes owner.json.tmp by
exclusive no-follow create, exact write and file fsync; rename plus directory
fsync is the shared association commit. Held old-root proofs are freshly checked
again after that commit, before intent removal. Only after removing the intent
and fsyncing that removal can any Store/Domain reference be returned.

Any error exposes no bindings and closes constructor-owned descriptors.
Initial creation never deletes assets or cgroups. Partial directories/intent remain,
so repeated construction cannot grow an unbounded new set of unknown epochs.
A failed final unlink barrier attempts to restore the denial marker. The fault
tests model syscall failures, not actual power loss or a filesystem that lies
about persistence; complete visible committed state after a crash is distinct
from incomplete/unknown intent.

For host-bound same-boot recovery only, a synced `owner.prepared` permits
reconciliation before or after the successor record's rename. With the
witness and all root locks held, the opener verifies an exact one-epoch
append, verifies exact fencing or typed removal for the candidate and all
current-boot roots, and verifies prior-boot associations as usual. It completes and rechecks the commit
before clearing its markers. The candidate goes into history; it is never
reopened for entry. Recovery must still provision a fresh live epoch within
the original cap before any bindings are returned.

An incomplete append without a complete authenticated prepared record still
denies. Ambiguous epoch identities and unknown layouts also deny. This is
not first-create repair or cold append replay.

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
   limit/layout identity, then opens all associated root epochs for version 1
   or verifies every epoch in the final boot run for version 2. Exact live
   groups reopen with `OpenRecoveringUnderBudget`; verified-removed epochs
   enter history without reopening. Both paths require the authenticated
   reference to match the durable epoch. The host-bound owner also verifies
   each prior-boot reference before skipping any previous run.
2. Fence/Checks the exact old roots under that valid captured budget. Ordinary
   append records intent first; a full history completes retirement before
   starting append. The root contains every typed build/worker descendant.
   No recovered tree is activated, removed or reclaimed before this proof.
3. Reads the stores in inspection mode **after fencing**, using the unchanged
   bounded persistent reader and full strict verification of committed/retired
   trees. Role mismatches deny. No observed-tree manifest is blessed as authority.
4. Completes any prepared append or retirement, and frees one history slot if
   required. It creates a fresh root Domain and commits its single association
   to the same two verified stores before privately reconstructing acquirable
   committed records. Their generation/kind/owner/digest identities stay exact.
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

## Opt-in cold recovery

For a **fresh** owner only, set `OwnerConfig.Host` to an `OwnerHostConfig`.
Pre-provision a dedicated local witness file with 32 cryptographically random
bytes encoded as 64 lowercase hex characters followed by one newline. The file
must be a mode-0600 single-link regular file in an euid-owned mode-0700 directory,
without symlinks or xattrs. Supply the complete, explicit list of existing backup
roots (`[]string{}` if none). The witness must be outside them and the three
owner/store roots. Do not include the witness or a reachable alias in any backup,
clone, VM snapshot, migration or restore. There is no automatic witness creation,
rotation, replacement, adoption or version-1 upgrade.
Use `orva setup --managed-witness /absolute/private-directory/witness` as
the daemon user to create it without running the ordinary setup steps.
The flag refuses an existing file, including a symlink; it never rotates it.

The witness is held under an exclusive nonblocking lock for the owner's entire
lifetime. Version 2 pins its device/inode and authenticates the canonical whole
association with HMAC-SHA-256 keyed by the witness; no witness bytes are written
to the association. Each boot ID must occur in exactly one contiguous run. The
old record, all prior metadata and the witness are checked before a cold
transition may write intent or recover either store. Prior metadata is checked
again before clearing intent. The fresh epoch's boot comes from the kernel-
verified Domain reference; no boot ID is accepted from configuration.

`OpenOwnerColdRecovery` accepts only an existing version-2 owner whose every
recorded epoch validates through `ValidatePriorBootUnderBudget`. That API checks
each exact old record against the current live captured budget and rejects the
current boot. It does not open an old cgroup or mint a Domain or fence proof.
If the final run is from this boot, use `OpenOwnerRecovery`. Both entry points use
the same append intent, commit barrier and store verification. Cold recovery
can replay authenticated retirement; it does not repair incomplete append state.
`OpenOwnerForBoot` chooses these paths after checking the witness MAC and
durable instance ID under the owner lock. Provisioning accepts only three
empty private roots.

For new host-bound epochs the witness MAC also covers the parent device and
inode. The typed removal path reads and matches `epoch.json` under its lock
and checks both paths on the pinned live cgroup-v2 mount. It accepts an exact
parent and group only for normal fencing, or both identities gone as removal
evidence. The kernel rejects renames and will not remove a cgroup with
processes or children; 64-bit kernfs IDs include a generation, so a recreated
path is not the old group. A missing/replaced group with the original parent
present still denies. Version 1 and older host-bound references without the
authenticated parent identity always require exact same-boot fencing.

The witness is not hardware attestation or rollback detection. Host placement
and backup-root completeness are trusted provisioning facts. Privileged host
writers, namespace/mount substitution, filesystem aliases not represented by
the configured roots, cloned witness/identity, and rollback of exact in-place
state are outside this proof. Witness and existing asset filesystem identities
must remain stable across reboots; changed device/inode values deny.

## Bounded epoch retirement

At `MaxEpochs`, a host-bound recovery retires exactly the oldest associated
epoch before appending a new one. The newest predecessor is always retained.
The rule also applies if reconciling a prepared append fills the last slot.
The opener must validate prior-boot and removed same-boot history and hold a
fresh `Fence` + `Check` proof for each reopened current-boot root.

After validation, it syncs `owner.retire` and its directory, then commits
`owner.json` without that epoch using the usual synced temp file and rename.
Before the commit, replay requires the exact predecessor and repeats its
validation. After the commit, replay accepts only the exact successor and the
recorded cleanup residue. It checks and removes the same-boot empty cgroup
tree first, then unlinks the exact metadata record and directory, syncs the
directory removals, and removes and syncs the journal. In this authenticated
post-commit cleanup phase an already absent group needs no cleanup. For a
verified-removed epoch, a replacement group under a replacement parent is
left untouched. Prior-boot cgroups are never looked up or removed.

No bindings are returned until retirement and the fresh append finish. Sealed
code/runtime and staged or retired store entries are unchanged; no grants or
policy are restored. A same-boot restart may retain the parent for exact
fencing or remove the whole parent subtree and create it again at its path.

## Budget recapture prerequisite

Ordinary OpenRecovering still requires a strict child of current membership.
The budget-aware variant accepts only a live process-local CaptureBudget
capability and never restores Start. It does not manufacture an ancestor capture
from a record. After controllers are enabled, the no-internal-process rule can
prevent a cold replacement daemon from entering the old internal service budget
to capture it. The managed daemon can recapture an already delegated budget
when the trusted launcher places it in the exact owned `orva.daemon` leaf;
that budget is scoped to the sibling `orva.workers` only. The cold entry point
still requires the same delegated parent path and an actual new kernel boot.
The tagged
`TestOwnerActualReboot` fixture passed on guest-local ext4: an enrolled child
was present before hard stop, the new boot had no old cgroup, a foreign witness
was rejected, and both exact seals reopened under cold then same-boot recovery.
The guest overlay changed inode identities and was refused. Neither result
qualifies daemon boot placement, power-loss durability or clone detection.

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

Before daemon cut-over, qualify cold-start budget placement and real-runtime
handoff, and define operator recovery for incomplete append state and asset
reclamation. The cold opener is not selected by `orva serve` and does not
itself restore policy approval.
