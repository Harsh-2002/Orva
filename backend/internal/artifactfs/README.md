# Inert filesystem generation ownership (Linux)

`artifactfs` owns **new** staging trees, verifies their complete contents against
host-trusted manifest expectations, renames them into private generations, and
excludes its own reclamation while a lease is held. It is not a registry that
approves arbitrary existing paths.

**Nothing serves through this package.** There is no pool, sandbox, installer,
GC, database, IAM-gate, boot, restore, or release wiring. This is not proof of
actual runtime pinning, packaged-runtime provenance, policy approval, or atomic
code/config/secrets publication. An unknown existing runtime cannot be acquired.

## Ownership and trust contract

- The host creates an empty, existing absolute directory owned by its effective
  UID with no group/other permission bits and no symlink path components.
  `New(root, limits)` takes a nonblocking exclusive `flock` on that directory.
  A second cooperating writer fails; **all nonempty state is rejected**, even
  recognizable generation names after a process restart. There is no adoption,
  recovery index, filesystem journaling or crash-durability promise.
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
starts no goroutines, timers, retries or background processes.

## Manifest version 1 and supported profile

Only typed inputs are accepted: there is deliberately no raw JSON parser,
unknown-field normalization or self-manifest producer API. Caller-owned input
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

Code symlinks must be relative and contained; runtime symlinks may also be
absolute **chroot-relative** targets. Absolute targets are validated against the
manifest tree, never resolved on the host. This intentionally narrow profile
rejects dot/dotdot segments even when lexically contained, redundant separators,
dangling links, all link chains/cycles, symlinked target parents, and links to
root/ancestors. It may reject ordinary distribution rootfs trees; compatibility
must be reviewed before extending the profile, not bypassed by adopting paths.

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

Focused offline checks:

```sh
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test -race -count=5 ./backend/internal/artifactfs
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go vet ./backend/internal/artifactfs
```

Tests use private temporary trees, not existing runtimes/versions or privileged
setup. Xattr/ACL/capability fixtures explicitly skip only when the local
filesystem/UID cannot create them; those skips are not capability-test passes.
No real-sandbox, full-suite, release or live-IAM acceptance follows from these
checks.
