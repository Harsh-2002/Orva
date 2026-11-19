# Surviving-process fence foundation (Linux, inert)

`workloadfence` owns a newly created cgroup-v2 epoch, enrolls trusted host commands
at process creation, and can close entry and prove the entire epoch unpopulated.
Sandbox and managed-builder consumers are internal opt-ins; there are **no
serving, pool, boot, GC, or IAM consumers**. The internal artifactfs lifecycle
owner is a separate proof-gated same-boot asset-recovery consumer.
A proof alone does not authorize artifact recovery, activation, deletion,
reclamation, policy approval, or current-generation admission. Recovered
unbound artifact stores remain inspection-only.

## Ownership and prerequisites

- Supply an existing empty absolute metadata directory owned by the effective
  UID, mode exactly 0700, with no symlink components. It must be on a durable
  filesystem supporting directory `flock`, file fsync, atomic same-directory
  rename, and directory fsync. Files are owned by this package alone.
- Supply an existing host-owned writable cgroup-v2 parent explicitly delegated
  to this owner. Ordinary `Create` requires a **strict child of this process's own non-root
  service/container cgroup**, as resolved from `/proc/self/cgroup` under the
  verified `/sys/fs/cgroup` mount. Root, the daemon group itself, ancestors,
  siblings, traversal, and ambiguous/namespace-root membership deny. A writable
  ancestor is not an approved budget. The supplied parent must remain inside
  that budget; Start/Fence/Check revalidate it. This path constraint supplements,
  but does not mint, the host's explicit ownership/delegation approval.
  `CreateUnderBudget` below is a separate explicit captured-budget path; ordinary
  Create's ancestor/sibling/root denials are unchanged.
  The package never selects a writable ancestor, moves the daemon, enables
  ancestor controllers, or scans/kills unrelated processes.
  Unsupported lifetime delegation or clone-into-cgroup fails closed, without a
  post-Start PID-write fallback. Linux must support `cgroup.kill` and clone3
  `CLONE_INTO_CGROUP`; the tested kernel is 6.12.113.
- Lifetime fencing does **not** require cpu, memory or pids resource controllers.
  Create enables none. Explicit jail-parent preparation below can enable only
  requested controllers inside the owned epoch and makes no hard-limit claim. Existing
  sandbox rlimit-only behavior is unchanged; that is separate from whether
  kernel process-domain membership and death can be proved.
- The metadata and cgroup ancestors, mount topology, namespace, and privileged
  host writers are trusted and must remain stable. No outside writer may alter,
  rename, delete, recreate, import, restore, or reuse an epoch or its control
  records. Never grant workload code access to the host cgroup filesystem,
  metadata, or descriptors. Same-UID/privileged hostile host mutation is not an
  isolation boundary this package can enforce.
- `Start` accepts **trusted host commands only**, not SDK/user-provided commands
  for direct host execution. Such commands must not relocate themselves or
  descendants outside their epoch, hand processes to an outside supervisor, or
  grant outside processes access to the epoch. Fork/exec alone inherits cgroup
  membership; privileged cgroup migration does not. This is not an nsjail or
  general hostile-command sandbox.
- The durable random epoch, kernel boot ID, metadata-root identity, parent
  identity and cgroup identity are checked together. Caller-supplied expected
  paths, inode numbers, released flock, PID absence, SDK EOF, and kill intent
  alone never constitute proof. Inode numbers can be recycled: same-boot
  inode equality is meaningful only under exclusive ownership and **no
  deletion/recreation/reuse**. A reboot, missing group, changed identity, or
  ambiguous record denies same-boot recovery; there is no reboot adoption or
  inode-reuse recovery.

## API and ordering

1. `Create(root, parent)` takes a nonblocking exclusive directory flock and
   rejects nonempty metadata. It creates a random `epoch-<32 lowercase hex>`
   domain with an empty `supervisors` leaf and a `jails` subtree. No resource
   controllers are enabled. This split allows future controller delegation on
   the empty epoch/jails parents without violating the no-internal-process rule.
2. Before exposing the domain, it persists a version-1 `epoch.json`: write an
   exclusively created mode-0600 temporary file, fsync, rename, directory fsync.
   The canonical record binds the exact boot/root/parent/group identities.
   Initializer failure leaves state for investigation, never automatic removal;
   it may leave an unassociated empty cgroup, but cannot have started a process.
3. `Domain.Start(ctx, cmd)` serializes command Start under the domain's private
   entry gate. It rechecks identity and metadata, checks closed/cancellation
   state, then uses Go `SysProcAttr.UseCgroupFD/CgroupFD` to enroll the **host
   command itself before exec** in `supervisors`. It preserves other attributes,
   rejects an existing cgroup binding, restores the command's original attribute
   pointer, and exports no raw cgroup FD. An optional owned JailParent is
   revalidated under the same gate. Callers exclusively own the command during
   Start; they retain responsibility for exactly one `Wait`.
4. `Fence(ctx)` permanently closes entry, then acquires that same gate. A Start
   already inside the gate may finish; Fence waits for it before killing.
   Prepared/queued/future Starts cannot enter after closure, including through
   copied handles. No cancellation or retry reopens entry.
5. Fence validates the exact domain and durable metadata, persists `closed:true`
   with the same fsync protocol, writes the epoch's `cgroup.kill`, and waits for
   the kernel's recursive `cgroup.events populated=0`. It rechecks identity
   before minting an opaque `*Proof`. The domain gate stays held through this
   sequence, so no old prepared binding can start after the observed emptiness.
6. `Check(ctx, proof)` accepts only a package-minted proof for that same still-owned
   domain, with matching durable closed metadata, current identities and fresh
   kernel emptiness. A zero proof, foreign domain, or closed ownership handle
   denies. Proofs are not serializable authority.
7. For unattached roots, `Close()` closes entry and releases descriptors/flock, without killing,
   deleting, reclaiming, or certifying anything. It is idempotent across copied
   handles. A new owner must use `OpenRecovering`, not Create.
8. `OpenRecovering(root, parent)` requires the exact same-boot durable identities,
   takes the flock, and **always disables Start**, even if the crashed owner's
   record was open. It may Fence and mint a new proof; an old proof is not
   transferable to the new owner.

Run all Start/Fence/Check/Close work **outside IAM gates, pool locks, registry
mutation locks, and database transactions**. This package's entry gate orders only
process entry and fencing; it is not the IAM authorization gate. Context bounds
gate acquisition and drain polling, but cannot interrupt a kernel filesystem
operation or Go `cmd.Start` already in progress. Close has no context and waits
for such work; it is not a bounded process-kill operation.

## Typed nested epochs

`instance.NewChild(ctx, metadataRoot)` creates a fresh exclusive Domain directly
under the instance's **owned jails**, never under a caller-supplied cgroup path.
It requires cpu/memory/pids delegation, preparing only owned epoch/jails nodes.
The empty internal parents keep the kernel's no-internal-process rule intact;
host commands enter each child's supervisors leaf. Nesting is limited to four
edges below the root, with at most 1024 attached descendants per root, including
quarantined partial creations. These are ownership bounds, not workload quotas.

All descendant Start/Prepare/NewChild operations acquire gates root-to-leaf and
verify registered ancestry, boot, pinned metadata/parent/group identities,
mount identity, and durable records. Cleanup uses the same gate order but permits
closed ancestors. Root Fence closes entry before waiting on gates, durably
closes the root and kills recursively. No previously prepared/copied child or
grandchild can start afterward. Child Fence affects only that child's subtree,
not its siblings or ancestor. Context cancellation releases acquired gates;
the finite filesystem operations and cmd.Start remain non-interruptible.
Root-to-leaf serialization includes bounded enumeration and cleanup; this is
not intended as a high-throughput serving scheduler.

Attached child `Close` returns `ErrBusy` and closes its entry **without**
releasing its pins/flock. Parent Close likewise denies new entry and returns
ErrBusy while any child remains. The owner must Fence/Check the child, retain
all build/runtime/staging reservations, then call
`parent.ReclaimChild(ctx, child, proof)`. Reclaim accepts only the exact direct
child and its fresh, still-owned closed proof, requires no attached children,
rechecks kernel emptiness and removes only that child's cgroup tree. Traversal
is limited to 2048 groups, 2048 entries per directory and depth 16. A failed
partial removal retains ownership for retry; successful removal detaches the
child and releases ancestor accounting. It never removes metadata or artifacts.
The host consumer removes its private metadata only after successful reclaim.
Reclaim is idempotent across copies of the same child/proof, not transferable.

Root version-1 record bytes are unchanged. Children use version 2 with exact
instance epoch/root, immediate parent epoch/metadata/group, and depth. The
lineage excludes mutable closed flags so ancestor fencing does not invalidate
child cleanup. `Reference(ctx)` returns verified owned association **data only**,
not proof or start/approval authority. Generic OpenRecovering rejects version-2
children; there is no child recovery/adoption constructor or store-association
loader.

On NewChild failure before cgroup creation, no child is returned. Failure after
creation returns a **nonnil closed child plus error**: retain both its metadata
and reservation. Missing/ambiguous durable records may remain quarantined and
unreclaimable; the API never invents a proof or silently releases parent pins.
Such quarantine can block future parent preparation when topology is incomplete.
Root Fence remains available, but is not permission to discard child ownership.

These guarantees require trusted host writers, stable topology and no outside
entrants. Raw Budget APIs remain privileged root boot-owner provisioning: they
can mutate under a trusted host and are **not** constrained by a nested Domain's
gate. Do not hand them to child consumers or infer generic ancestor permission.
No package can protect against privileged host migrations or forged metadata.

### Remaining recovery/publication obligations

Unbound persistent stores carry no embedded Domain association. The separate
artifactfs lifecycle owner now supplies a single wrapper record for both stores
and narrowly proof-gated same-boot committed-asset recovery; see its OWNER.md.
It does not make an unbound inspection record authoritative or bind an asset
generation to current function/config/policy publication.

Serving publication must separately order exact generation/current-state
identities, all worker/build ancestry and crash intents with closed/fenced
ownership. The loader must verify boot and stable topology and obtain fresh
kernel proof, never adopt by path/inode. Root proof alone, a reference or
released flock is not artifact-store authority. Reboot recovery, serving
activation, policy restoration and general quarantine reclamation remain absent.

## Failure and strict metadata handling

Reads open no-follow/nonblocking, require an euid-owned exact mode-0600
single-link regular file, and cap reads at 4096 bytes. Canonical typed JSON
reencoding rejects unknown/duplicate fields, omitted fields, null/wrong types,
noncanonical encodings and invalid UTF-8. Format, lowercase hex epoch, boot-ID
shape and nonzero inode identities are checked. Unknown names, pending temporary
records, links, FIFOs, oversize metadata, changed directory mode/identity,
corruption and unsupported filesystem deny without deleting or repairing data.
These checks supplement exclusive ownership, not adversarial concurrent-writer
atomicity or protection from a trusted host restoring forged records.

Cancellation/deadline or I/O failure yields **no proof**, with entry still closed.
No kill occurs before a successful durable closed commit. Cancellation before
the entry gate does not imply descendants have stopped. Retry can complete an
ordinary cancelled/failed kill. A visible closed rename after directory-fsync
failure is accepted only as the exact monotonic closed transition and is
persisted/fsynced again before kill/proof. Failed file-fsync or rename leaves the
temporary record intact; retries/reopen deny rather than discard it. An operator
must investigate such state; there is no metadata repair or cleanup API.
The package never removes metadata or artifact data. In addition to typed-child
`ReclaimChild`, the wider artifact owner can remove an exact empty root via
`RemoveRetiredRootUnderBudget` only after authenticated retirement commits.
That cleanup-only API does not fence or prove death. Constructor failure never
removes evidence automatically.

## Owned jail-parent extension (requires new independent acceptance)

The original package-only acceptance is a historical snapshot; its preserved
hashes/logs do not cover this extension or nested epochs. Root records are
unchanged. Create additionally pins its newly created `jails` directory until Close.
Recovered domains do not reconstruct live path authority and cannot prepare or
start jails.

`PrepareJails(ctx, controllers...)` returns an opaque `*JailParent`. The only
accepted controller names are `cpu`, `memory`, `pids`, each at most once; the
request and returned capability have constant-size owned state. An empty request
means lifetime-only. Preparation holds the domain entry gate, rejects
closed/recovering/disposed domains, and validates exact epoch/metadata/jails
identities and domain type. It rejects unknown epoch children and unknown jail
directory names. Typed registered child epochs are recognized by pinned identity;
other expected child names are canonical `NSJAIL.<positive uint32>`
labels, not proof of their provenance; exclusive trusted host writers remain a
precondition. Enumeration is capped at 2048 entries per inspected directory,
including kernel files; this intentionally bounds the number of accepted jails.
The shared pinned descriptor is never exported; preparing capabilities does not
allocate more persistent descriptors.

For a resource request, availability is read from the epoch, not inferred from
writability or host-root controls. The package enables the union of explicitly
requested controllers on **only the epoch and its jails subtree**, and verifies
kernel readback. It never enables controllers on the supervisors leaf, service
parent, ancestor, daemon or global worker subtree. Missing delegation returns
`ErrUnsupported`, without changing existing lifetime authority or falling back
to fewer requested controllers. Partial/unverifiable kernel writes permanently
close entry without undoing or deleting state; Fence remains available for
cleanup. Successful delegation is not proof of per-jail limits: the eventual
consumer must set and verify those and independently test enforcement.

`parent.Path()` is a value for trusted host argument construction, **not durable
approval or permission to enter a cgroup directly**. Pass the capability to
`domain.Start(ctx, cmd, parent)`. Start rechecks ownership, pinned identity,
kernel domain/controller state and names under its existing entry/fence gate.
Detected jail identity/topology/controller corruption closes entry permanently;
it is not repaired by retrying preparation.
Foreign/zero/nil capabilities, replacement, unexpected changes, post-Fence or
post-Close bindings deny. Copies share the same domain lifetime; a previously
prepared path cannot bypass a subsequently closed Start. Code using this API
must not start the command by another route or substitute a different
`--cgroupv2_mount` after argument construction.

## Captured service budget

Linux's no-internal-process constraint prevents a populated non-root daemon
cgroup from distributing memory to children. Ordinary Create still requires a
parent below current membership; it does not infer a service ancestor after the
daemon moves into a leaf.

`CaptureBudget()` accepts **no path or persisted identity**. Before a trusted
boot owner moves the daemon, it captures the exact current non-root unified
cgroup from `/proc/self/cgroup`, verifies its ownership and cgroup-v2 filesystem,
and pins that cgroup, the `/sys/fs/cgroup` mount, and both cgroup/mount namespaces.
The process-local capability binds PID, kernel boot ID, path, device/inode
identities, and the mount's kernel `mnt_id` from bounded fdinfo. No raw descriptor
is exported. Root/ambiguous membership, wrong identities and unsupported mount
identity deny; there is no writable-ancestor scan or caller-string anchor.
Both the captured budget and each supplied parent must belong to that exact
mount; a nested bind mount cannot substitute a foreign cgroup at a lexical child.

`CreateUnderBudget(budget, root, parent)` requires a live package-minted
capability and a strict descendant of its exact captured budget. The daemon's
current membership must still be that budget or a descendant of it. Mount,
namespaces, boot, PID, pinned budget identity and current path identity are
rechecked at creation and through the Domain's normal Start/Prepare/Fence/Check
identity verification. A daemon leaf and an epoch parent may therefore be
siblings **inside the same captured budget**, without authorizing any path
outside it. An ordinary `CaptureBudget` made in a leaf cannot reconstruct or
mint its ancestor's authority.

The daemon's separate `CaptureServingBudget` accepts only a configured
`orva.workers` parent and verifies either exact initial budget membership or
membership in the immediate owned `orva.daemon` sibling leaf. It returns a
budget scoped to that worker parent, not arbitrary ancestors or siblings.

The trusted boot owner must exclusively own the service budget and its stable
ancestors, move **only its own daemon** to a known owned leaf, verify the
captured budget has no direct processes, and explicitly arrange resource
delegation before creating resource domains. This package performs **none** of
those moves, empty-budget boot operations or service-parent controller writes.
PrepareJails still modifies only owned epoch/jails descendants and fails when
requested delegation is unavailable. Stable mount topology/namespaces and
no deletion/recreation/inode reuse remain trusted-host preconditions; the
capability is not a defense against a privileged host violating them.

Budget copies share one state. At most 1024 domains may retain that state.
`Budget.Close` prevents later creation reservations through every copy;
already-retained creations may complete. Existing domains keep independent
entry/fence lifetimes and pinned references: Close returns ErrBusy until these
references are released, and the last release disposes the pins. Budget closure
is **not** worker death, process revocation, or permission to reclaim artifacts.
The domain gate may acquire the budget mutex for bounded identity checks; budget
methods never acquire domain/IAM/pool locks or call user callbacks.

No durable budget schema changes are made. No capability can be restored from a
database/path/record. `OpenRecoveringUnderBudget` retains a valid freshly
captured capability and verifies a same-boot root; it never restores Start or
child adoption. Ordinary OpenRecovering remains inspection/fence-only under
its original current-membership constraint. No serving boot owner is installed.
Restart placement, recapturing a
service budget after a crash, and production delegation remain integration
obligations, not acceptance established by this process-local API.
See the kernel's [no-internal-process constraint](https://docs.kernel.org/admin-guide/cgroup-v2.html#no-internal-process-constraint).

### Prior-boot validation (no recovery)

`ValidatePriorBootUnderBudget(ctx, budget, root, parent, expected)` is a
read-only check for a trusted boot owner holding the wider durable ownership
association. It takes the exact old metadata directory's nonblocking flock.
Only an unambiguous root-v1 record matching every `EpochReference` field and
the actual metadata directory's device/inode can pass. A copied, restored or
renumbered metadata root is not a match. Both open and closed old records may
pass; the closed bit is not evidence of process death.

The budget must come from a fresh capture in the current process. Its boot ID
must match the live kernel and differ from the durable record's ID. The current
parent must be a valid strict descendant on the captured mount; its identity,
the budget's live checks and the exact durable record are checked again before
success. Old cgroup paths and device/inodes are not current authority and are
not opened. This comparison assumes the same trusted host/kernel lineage; it
cannot distinguish a reboot from moving storage to a different machine or VM.
Do not use it for cross-host, imported, restored or ambiguous state.

Success returns no `Domain`, `Proof`, or recovery authority. The check does
not fence, write, activate or reclaim anything, change the existing same-boot
constructors, or wire any recovery path. Fixture tests do not qualify a real
reboot.

## Sandbox integration contract (internal opt-in only)

Every nsjail **host command**, every worker/build child, and all descendants must
remain inside the same durable instance epoch, from before their first exec
through termination. The supervisor belongs in `supervisors`; nsjail-created
child cgroups must be rooted under that epoch's `jails` subtree (or an equivalent
owned in-epoch layout). Its `--cgroupv2_mount` / effective cgroup parent **must be
inside that epoch**. Both worker and build startup paths need this invariant.

When nsjail resource cgroup flags are enabled, its effective cgroup parent must
be inside the epoch; the future adapter must enable and verify the required
controllers in that owned subtree. Without resource cgroup flags, children
naturally inherit their enrolled host command's domain; no relocation is needed.

The sandbox's legacy global `orva.workers` mount is **not compatible**:
nsjail relocating a jailed child there escapes this fence even though the host
nsjail command was enrolled correctly. Do not enable this package by wrapping
Start alone. A future adapter must own argument construction and cgroup parent
selection, prevent raw-FD reentry, join every startup path, and prove real nsjail
relocation/lifecycle behavior before it can consume proofs. The sandbox now
provides an internal Domain opt-in for this owned placement path; serving pools
still do not enable it. The lifecycle owner is a separate internal asset consumer,
not boot or serving activation.

## Focused verification

Kernel tests require an explicit, exclusively owned disposable-VM subtree;
unset variables skip them without touching host cgroups. Never point them at a
shared/production cgroup. A fresh guest-root fixture delegates cpu/memory/pids
from the guest's cgroup root to the test service budget. The test binary alone
must occupy that budget (no go driver or waiting shell there). Resource fixtures
capture it, move only the test process to an owned daemon leaf, check the anchor
empty, then explicitly enable budget/fixture-parent controllers. They restore
membership/controller state and remove only their own directories afterward.
The root-capture negative moves only the test process temporarily to the
disposable guest's root; production CaptureBudget never moves anything.
Run from the copied package directory:

```sh
env -i PATH=/usr/local/go/bin:/usr/bin:/bin HOME=/tmp GOCACHE=/tmp/gocache \
  GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GO111MODULE=off \
  go test -race -c -o /workloadfence.test .
# Execute directly via msb exec, not through a waiting guest shell:
env -i PATH=/usr/local/go/bin:/usr/bin:/bin HOME=/tmp \
  ORVA_FENCE_TEST_PARENT=/sys/fs/cgroup/microsandbox-workload/iam-budget-tests \
  ORVA_FENCE_TEST_SCRATCH=disposable-vm /workloadfence.test -test.v -test.count=20
env -i PATH=/usr/local/go/bin:/usr/bin:/bin HOME=/tmp GOCACHE=/tmp/gocache \
  GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GO111MODULE=off go vet .
```

The stdlib-only package can be copied alone into the guest; module-off execution
avoids unrelated repository dependency/toolchain downloads. Tests use real
kernel cgroups for enrollment, nested child/grandchild death, populated-to-empty
proof, entry/fence races, deadline/cancellation closure, crash/reopen, and
identity/corruption denials. A deleted target-cgroup kernel rejection proves there is no
unenrolled fallback. Private fault injection exercises file-fsync/rename/
directory-fsync handling with real descendants; it is **not** an actual
power-loss or storage-hardware durability experiment.

New budget tests verify real cpu/memory/pids delegation, exact per-child
memory.max/pids.max/cpu.max values, enrolled helper plus grandchild membership,
and kernel-empty fencing. These are controller installation/readback and
lifetime proofs, **not** OOM/CPU-throttling/PID-exhaustion enforcement stress
tests or real nsjail evidence. Other cases cover actual root capture rejection,
actual out-of-budget process movement/path replacement, nested foreign
bind-mount rejection, copied/closed/zero
capabilities, constructor-reservation release and concurrent Close/Create.
Wrong boot/inode/mount/namespace/PID cases alter expected binding fields and
must deny; they are not actual reboot, remount or namespace-transition tests.

The crash-owner helper skips direct top-level invocation but runs as a real
subprocess of the crash/reopen test. Unkillable D-state timeout, actual
unsupported-clone3 kernels, namespace/mount changes, hostile host writers,
production nsjail, real artifacts, full repository tests/E2E, and serving IAM
acceptance are not established by these focused checks.
