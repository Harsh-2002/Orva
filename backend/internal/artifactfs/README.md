# Inert filesystem generation ownership (Linux)

`artifactfs` owns **new** staging trees, verifies their complete contents against
host-trusted manifest expectations, renames them into private generations, and
excludes its own reclamation while a lease is held. It is not a registry that
approves arbitrary existing paths.

**Nothing serves through this package.** A sandbox opt-in consumer exists, but
pools do not supply it. There is no installer, GC, database, IAM-gate, boot,
restore, or release wiring. This is not proof of
actual runtime pinning, packaged-runtime provenance, policy approval, or atomic
code/config/secrets publication. An unknown existing runtime cannot be acquired.

## Ownership and trust contract

- The host creates an empty, existing absolute directory owned by its effective
  UID with no group/other permission bits and no symlink path components.
  `New(root, limits)` takes a nonblocking exclusive `flock` on that directory.
  A second cooperating writer fails; **all nonempty state is rejected**, even
  recognizable generation names after a process restart. There is no adoption,
  recovery index, filesystem journaling or crash-durability promise through
  **`New`**. The separate persistent constructors below do not change this API.
- The store and its trusted producer are the **exclusive writers** to the root
  and its trees. Ancestors must remain stable. Producers must obey reservations
  while producing, create no mounts, and close all writable descriptors/mappings,
  stop all writers, and relinquish all write access **before calling Seal**.
  This precondition includes child processes and external hardlink aliases.
- Calling `Seal` consumes producer access even on failure. A previously returned
  path string or descriptor cannot be revoked by Go: this is a host ownership
  contract, not an adversarial-producer isolation boundary. The store never
  changes sealed trees except during zero-reference reclamation. Consumers must
  use read-only mounts/access and retain their lease until **all** consumers,
  including late spawns and busy old workers, have stopped using that tree.
- Expected digests and manifests come from an independently trusted host build
  or release process, never SDK frames or a producer's self-asserted manifest.
  `Digest` only canonicalizes content; computing it from an untrusted tree does
  **not** make that tree trusted. Kind/owner are host-supplied labels, not an
  approval decision, and are not fields in the content digest.
- No cryptographically atomic concurrent-writer snapshot is claimed. Two full
  bounded walks, no-follow leaf opens, descriptor stats and change stamps detect
  ordinary replacement/change, but do not replace exclusive ownership. The
  store does not prevent privileged host tampering or a same-UID writer that
  violates this contract. No fs-verity, immutable flag, or mount pin is installed.

## API and state machine

1. `Begin(ctx, reservedBytes)` charges generation count, staging count and the
   entire reservation **before** filesystem creation. `Staging.Path()` is the
   sole producer path API. Reservation is positive, including an empty tree.
2. The producer supplies a version-1 typed `Manifest`, trusted digest, `KindCode`
   with a canonical lowercase non-nil RFC UUID owner (versions 1–8), or
   `KindRuntime` with owner exactly `node` or `python`.
3. `Staging.Seal(ctx, kind, owner, manifest, expectedDigest)` clones/validates the
   bounded manifest, verifies the tree, and atomically renames it from
   `staging-<UUIDv7>` to `generation-<UUIDv7>`. The store mints each UUID once;
   destinations are checked absent and never reused or overwritten under the
   exclusive-writer contract. No data is copied, so ownership, mode and hardlink
   inode identity survive. It returns a generation ID, **not a lease**.
4. `Acquire(generation)` grants a bounded opaque `*Lease` only for an active
   sealed record. `Path()` and `Identity()` return values; they are valid for
   consumption only until `Release()`. Release is idempotent, including copies
   of the handle. Neither a zero-value handle nor a caller-constructed Identity
   grants any authority. Lease count is reserved under the same mutex as refs.
5. `Retire(generation)` atomically prevents new acquisitions; existing leases
   remain valid. `Reclaim(generation)` requires retired state and zero refs,
   marks it nonacquirable/deleting under the mutex, and removes outside it.
   Concurrent reclaim returns `ErrBusy`; no leased tree is deleted.
6. `Staging.Abort()` cleans up an unsealed or failed stage. A failed seal cannot
   be repaired/resealed through that staging handle. Cleanup failure retains
   **all** charged capacity, leaves a failed stage or retired generation, and
   requires an explicit `Abort`/`Reclaim` retry. Removal may already be partial;
   such a generation can never become acquirable again. Deletion permission or
   filesystem failures may require host repair before a retry can succeed.
7. `Close()` immediately prevents Begin/Seal/Acquire. It never deletes a tree
   or releases leases. With owned records it returns `ErrBusy` and retains the
   directory lock: explicitly abort stages, retire generations, release leases,
   reclaim, then call Close again. Retirement/cleanup remain available closed.

Cancellation before Begin reserves nothing. If cancellation/Close is observed
after Begin creates a directory, it makes one cleanup attempt and returns a
non-nil staging handle **with the error** so failed cleanup can be retried.
Cancellation during Seal denies publication but keeps capacity charged. If
cancellation/Close is observed after rename, Seal returns the nonacquirable
retired generation ID **with the error** for explicit Reclaim. Always inspect
those cleanup handles/IDs on errors. Context cancellation/deadline errors are
preserved; other errors are sanitized `ErrInvalid`, `ErrIntegrity`,
`ErrUnsupported`, `ErrCapacity`, `ErrRetired`, `ErrClosed`, `ErrBusy`, or `ErrIO`.
Cancellation does not interrupt a kernel filesystem operation already underway.

`PreparePaths(codeStore, codeGeneration, runtimeStore, runtimeGeneration)` acquires
both correctly typed leases and releases the first if the second fails. Its
`Paths()` and idempotent `Release()` cover the pair; no caller-supplied path or
digest can substitute for a lease. Acquisition is **not** atomic across the two
stores and does not prove current function/artifact generation or IAM approval.
Future publication must revalidate its own authoritative generation under its
ordered locks. Run preparation and cleanup outside IAM/pool locks. The package
starts no goroutines, timers, retries or background processes through these store
APIs. The separate lifecycle owner synchronously invokes workloadfence's bounded
drain polling; it installs no background recovery or retry loop.

`PreparedPaths.Transfer()` atomically consumes a pair and all copied handles,
returning a new owner. Old handles expose empty paths/identities, cannot transfer
again, and cannot release the new owner's leases. `Identities()` returns detached
store-minted labels, not policy approval. Transfer and Release serialize against
one another.

The sandbox's internal opt-in `ExecConfig.VerifiedPaths` consumes the pair,
validates function/runtime ownership and uses its concrete code/runtime paths
instead of legacy mutable paths. Spawn failures release it; successful workers
retain it through readiness, retirement, lazy imports and process termination
until centralized post-Wait cleanup. One-shot execution retains it through
process completion. Nil preserves legacy behavior; serving pools do not supply
it. Host runtime-endpoint probes support contained relative links only and reject
absolute endpoint links before following them; this is narrower than runtime
manifest link support. Generation/config/secrets approval and actual packaged
runtime compatibility remain separate obligations.
Worker `waitDone` closes before exit callbacks, channel/cgroup cleanup and lease
release. Kill/Quit or `waitDone` therefore cannot be used as a lease-release fence;
the store continues to reject reclamation while references remain.

## Manifest version 1 and supported profile

Only typed producer inputs are accepted: there is no producer JSON parser,
unknown-field normalization or self-manifest producer API. The persistent-store
reader below parses only its own bounded control records. Caller-owned input
must not be concurrently mutated while passed to Digest/Seal. Digest sorts an
owned entry slice by bytewise path and hashes `encoding/json.Marshal` of the
version-1 structs, compact JSON without newline. Field order is:

- Manifest: `version`, `entries`.
- Each Entry: `path`, `type`, `mode`, `uid`, `gid`, `size`, `sha256`,
  `link_target`, `hardlink_group`. All fields are emitted, including zero/empty.

Root `.` is an explicit directory entry. Every other path is canonical relative
UTF-8, with no NUL, backslash, absolute path, duplicate, empty/dot component or
escape. Entries cover **every** node, including empty directories; extra/missing
nodes deny. Type is `directory`, `file`, or `symlink`; other types deny. POSIX
mode is numeric `0000..07777` (includes sticky/setuid/setgid); UID/GID are numeric.
Directories/symlinks have size zero and empty hash. Files have exact size and
lowercase SHA-256; only symlinks have `link_target`. All other unused fields must
be empty. Timestamps are not manifest authority, but mtime/ctime are change
stamps during verification.

Regular hardlinks are supported only when **all** inode names are inside the
tree (`st_nlink` equals observed count). For a multi-name group every member's
`hardlink_group` is its lexically first member path; single-name files use empty.
Every name is hashed/charged, conservatively counting repeated hardlink bytes.
Hardlinked symlinks are rejected.

Code symlinks remain relative and contained: dot/dotdot segments, redundant
separators, all link chains and symlinked target parents deny.

Runtime symlinks additionally support contained relative dot/dotdot segments,
link chains, symlinked target parents and absolute **chroot-relative** targets.
After all expected entries have been validated and matched, resolution consumes
components against the manifest tree only; it never follows host filesystem
links. Each encountered link is expanded before processing the remaining
components, including `..`; absolute expansions reset to the manifest root.
For example, if `a/p -> /other/deep`, `a/p/../file` names `other/file`, not
`a/file`. A packaged-style `bin -> usr/bin`, `usr/bin/pager ->
/etc/alternatives/pager`, `etc/alternatives/pager -> /bin/more` chain resolves to
the manifest's `usr/bin/more`. Exact original link text remains in the digest
and is not rewritten on disk.

Both profiles reject dangling/missing targets, files used as parents (even before
`.` or `..`), links resolving to root/ancestors, and directory traversal cycles,
including cycles through resolved chains or symlinked parents. Runtime resolution
also rejects `..` at root rather than clamping it, repeated resolution states and
over-budget expansions. Invalid UTF-8, NUL and backslash deny. Empty components
still deny: repeated or trailing `/` is unsupported; the lone `/` is parsed as
root but a link resolving there still denies. No distribution-rootfs acceptance
follows: genuinely dangling targets (for example removed manpages),
xattrs/capabilities and other profile blockers remain unsupported. A missing
direct target alone is not dangling-link proof when its parents may be links.
Compatibility must be reviewed, not bypassed by adopting existing paths. This
extension activates no runtime consumer.

Every node, including root and symlinks, must have **no xattrs**: Linux
`llistxattr` is used without following links; ACLs, capabilities, and other xattrs
deny. Filesystems that cannot enumerate xattrs deny. FIFOs, devices and sockets
deny before any content read. Trees must be traversable/readable by the store
UID. Mounts and concurrent mutation are excluded by the producer contract, not
detected as a hostile-mount boundary. Sparse layout, ACLs, capabilities, inode
flags and packaged-release provenance are not supported/preserved claims.

## Finite limits and verification

All constructor limits are explicit, positive and immutable after construction:

| Limit | Hard constructor ceiling |
| --- | --- |
| Generations (staging + sealed + failed + deleting) | 1,048,576 |
| Live leases | 1,048,576 |
| Concurrent staging, including failed stages | generation limit |
| Tree entries, including root | 1,048,576 |
| Path / link target bytes | 4,096 each |
| Manifest bytes | 64 MiB |
| Aggregate reserved logical file bytes | 1 PiB |
| Individual file bytes | aggregate byte limit |

These are ceilings, not recommended defaults. Choose much smaller operational
budgets. Manifest preflight conservatively budgets 200 fixed bytes plus six times
the input string bytes per entry (and 32 header bytes), **before** cloning or
serialization; it can reject a compact manifest smaller than ManifestBytes.
The actual serialized size is checked too. Directory enumeration reads at most
128 names per batch; file hashing checks context at each 32-KiB chunk and denies
growth beyond the stat size/reservation. A second complete hash/metadata walk
must match the first. Metadata, directory overhead and producer-time disk writes
are not byte quotas; entry/path/manifest limits bound metadata work separately.

Runtime resolution permits at most 40 symlink expansions per link. At each step,
the resolved path plus pending components must fit `PathBytes` (including
separators), and their combined component count must not exceed `Entries`.
Target text independently fits `LinkBytes`. A total step ceiling of
`41 * PathBytes` bounds component processing; at most 40 bounded resolution
states are retained. These conservative budgets may reject an ultimately short
target whose intermediate expansion is too large. The existing directory-edge
cycle check uses fully resolved destinations and remains bounded by tree entries.

Focused offline checks:

```sh
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test -race -count=20 ./backend/internal/artifactfs
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go vet ./backend/internal/artifactfs
```

Tests use private temporary trees, not existing runtimes/versions or privileged
setup. Xattr/ACL/capability fixtures explicitly skip only when the local
filesystem/UID cannot create them; those skips are not capability-test passes.
No real-sandbox, full-suite, release or live-IAM acceptance follows from these
checks.

## Explicit persistent foundation (not serving recovery)

The separate internal [one-instance owner](OWNER.md) binds both persistent stores
and enables narrowly proof-gated same-boot committed-asset recovery. It does not
change these unbound constructors or make their inspection records authoritative.

`NewPersistent(root, limits)` initializes **only an empty root**, under the same
private-directory/exclusive-writer contract and nonblocking exclusive `flock`.
It creates `.artifactfs-control/format.json` with version 1. Control metadata is
a sibling of `staging-<UUIDv7>` and `generation-<UUIDv7>`, never part of a content
manifest. `New` still rejects every nonempty root, including this layout.
A failed initializer can leave an incomplete layout; neither constructor adopts
or repairs it automatically.

Live persistent stores retain the ordinary lease API, with additional ordering:

1. `Begin` charges capacity and atomically persists a `staging` reservation
   **before** creating its staging directory, then fsyncs the root. An I/O error
   returns a staging cleanup handle and keeps its reservation charged.
2. `Seal` fully verifies the typed, owned manifest, fsyncs every regular file and
   directory child-first, and verifies again. It does not chmod, chown, copy or
   rewrite content. All filesystem work runs outside the store mutex.
3. It writes a complete `publish_intent` record by exclusive temporary-file
   creation, write, file fsync, rename and control-directory fsync; renames the
   stage to its generation; and fsyncs the root.
4. It atomically writes/fsyncs a `committed` record by the same protocol
   **before** publishing active state in memory. A failure after tree rename
   returns the generation ID for explicit `Reclaim`, preserving the actual
   generation path and all accounting. Cancellation/Close deny publication;
   already-written intent or commit evidence remains for inspection. Cancellation
   racing an in-progress metadata commit is not an atomic cancellation fence.
5. `Retire` first denies new leases, then persists `retired`; a failed durable
   write stays denied and is retryable. `Abort`/`Reclaim` persist `delete_intent`
   before tree removal, fsync the root, unlink metadata, and fsync the control
   directory. Any failure retains the entire reservation until an explicit
   cleanup retry succeeds, even if a prior removal was partial.

The canonical compact JSON record emits, in order: `version`, `generation`,
`kind`, `owner`, `digest`, `reserved`, `phase`, `manifest`. A staging reservation
has empty kind/owner/digest and manifest `{"version":0,"entries":[]}`; sealed
records carry the complete canonical version-1 manifest and content digest.
Only public content metadata is stored: no environment, secret values, config
snapshots, approval, policy grants or worker identities. Record names and their
embedded generation must agree on a canonical UUIDv7.

Record reads use no-follow, nonblocking opens and descriptor stats; require
euid-owned mode-0600 single-link regular files without xattrs; and bound size
before reading. The control directory is euid-owned mode 0700 without xattrs.
The JSON reader rejects duplicate/unknown fields, null anywhere, invalid UTF-8,
noncanonical encodings, invalid identities/phases, and digest mismatch. It bounds
depth, object keys, token count and entry-array count before struct allocation,
then requires byte-identical canonical reencoding. Existing root/path ownership
and stable-ancestor assumptions still apply; this is not hostile-host isolation.

Besides the existing logical reservation/count/entry limits, **all live
persisted manifests together** fit `Limits.ManifestBytes`, not one such budget
per generation. Metadata has an additional fixed budget of at most 1024 bytes
per generation plus the 128-byte format-record cap. At most one temporary record
per generation is recognized; recovery bounds total primary+temporary metadata
to twice the manifest-plus-fixed-overhead budget (plus format). Counts and byte
sums are checked without unchecked aggregate multiplication/addition overflow.
These are conservative work/allocation bounds, not physical disk quotas.

### Quarantine-only reopening

`OpenRecovering(ctx, root, limits)` requires the recognized versioned layout,
holds the exclusive directory lock, validates all control records and root names,
and completely verifies **every committed or retired tree before registering any
catalog record**. Unknown files, missing committed trees, metadata links,
corruption, unsupported committed content and exceeded bounds fail closed.
Failure performs no cleanup, repair, legacy adoption or `RemoveAll`.

Staging reservations (including reservation-before-mkdir), `publish_intent`
before/after generation rename, and `delete_intent` partial removals are retained
and charged but never activated or resumed. Incomplete staging/deleting contents
are deliberately not traversed; unsupported producer nodes remain quarantined.
Canonical pending `.json.tmp` records are recognized and checked against the
primary transition when both exist; malformed/partial temporary JSON fails
closed rather than being silently discarded. A missing or malformed format
record also fails closed.

`SnapshotRecovery()` returns detached identity/phase/reservation/manifest records
with staging/generation presence and pending-metadata observations, **no
authoritative paths**. It is nil on ordinary live stores. The recovered store
blocks **Begin, Acquire, PreparePaths, Retire, Reclaim and Abort**, regardless of
whether its record says committed, retired or deleting. There is no activation
switch, approval flag, lifecycle-fence argument, auto-GC or recovery mutator.
`Close` may release a recovered store's process lock with catalog records still
present because that store cannot grant leases or mutations.

**A released flock does not prove old workers dead.** A worker surviving its
daemon may still hold the old tree; this package neither discovers nor fences
it. A future lifecycle service must establish that boundary independently
before any activation or cleanup API is considered. Recovered verification is
not policy approval, runtime provenance/packaging, current-generation proof,
atomic code/config/secrets publication or permission to serve.

Package-private fault seams test writes, fsyncs, renames and removals, including
intent-before-rename, rename-before-commit, committed-record/directory-fsync
failure, explicit cleanup retries and mocked surviving old-worker access.
Passing these tests demonstrates the implemented **fsync protocol and
fail-closed states**, not a power-loss/filesystem hardware durability experiment.
There is still no boot, pool, GC, database, installer or runtime-packaging wiring.
