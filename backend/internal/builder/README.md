# Builder runtime pinning and owned output (internal opt-in)

`New(WithRuntimeBinding(provider))` captures a host-trusted runtime binding
**once at actual `Build` entry**. There is no serving constructor, queue
activation, public flag, runtime producer or current-generation provider in
this change. `New()` and builders without this option retain legacy behavior,
including mutable rootfs lookup, cache hits and nil-context test hooks.
Explicitly opting in with a nil provider, a declined/zero binding or a provider
error denies; it never selects the legacy path.

## Capture and command lifetime

The caller must exclusively own the builder configuration and function while
entry capture runs; unsynchronized concurrent mutation during capture is not
supported. Managed entry copies the builder and deep-clones the function
before invoking the provider. The
provider receives only captured function ID/language scalars, not mutable
configuration, policy or grants. DB tuning and the existing egress-policy
accessor remain live services, not frozen authority. Existing registry
environment forwarding also remains unchanged/live: nsjail inherits matching
variables by name to avoid putting registry credentials in argv.

The binding names an `artifactfs.Store` and exact runtime generation. Build
privately acquires an anchor, validates runtime kind/language, and reads the
bounded interpreter stamp from that concrete tree. Each syntax, npm/pip and
TypeScript step passes that same store/generation to `sandbox.RunBuild`, which
acquires its **own** lease. No caller receives a command's lease or release
alias. Managed tool probes accept contained relative links (including npm/npx)
but reject absolute host traversal before following links. `RootfsDir` is not
consulted in this mode.

Each command lease covers failed setup/start, the single supervisor Wait, and
scratch cleanup. The pipeline anchor lasts through Build's scratch cleanup,
including errors and cancellation. Retirement denies future acquisitions:
an already-acquired command may finish, but a subsequent step fails closed
without refreshing the provider, changing generations, or falling back.
The anchor prevents reclamation between steps, not retirement.

The optional concrete Domain is captured in the same binding and passed to
**every** command. Existing sandbox enrollment requires cpu/memory/pids and
denies invalid domains or unavailable controllers. It is placement/lifetime
metadata, not admin authorization, policy approval or current-artifact proof.

## ABI identity is not artifact authority

Version hashing still uses source-archive SHA-256 plus the interpreter stamp
(`language unstamped` if absent/empty). The stamp comes from the pinned tree;
it is intentionally distinct from the full runtime manifest identity.
Successful managed `BuildResult`s contain value-owned `RuntimeArtifact`
(generation, kind, owner, digest) and `RuntimeABI`, excluded from legacy JSON.
These record the exact runtime used for commands, not approved code or a
serving generation.

Existing `.orva-ready` cache entries carry no full-runtime provenance. Managed
builds reject those hits rather than attributing cached dependencies to today's
runtime. This also applies to a second managed build of identical input.
No cache layout, source archive, interpreter-based hash, per-function installer
cache or existing version-directory replacement algorithm was changed.

## Deliberately incomplete boundaries

**Supervisor Wait does not prove grandchildren or output writers have died.**
Domain handles are copyable and this API cannot establish exclusive per-build
ownership. Runtime-only Build therefore does **not** Fence, Check, Close or otherwise claim
ownership of a supplied Domain. Its trusted lifecycle owner remains responsible
for surviving descendants and must retain any broader lifetime leases until
real fencing completes. In particular, releasing the pipeline/command leases
after supervisor cleanup is not proof of complete runtime quiescence.

The returned legacy output directory remains writable, unsealed and subject to
the pre-existing `RemoveAll`/Rename replacement path. A runtime fingerprint
does not fix that. Do not install serving authority or reclaim writer resources
from this result. The optional legacy `BuildFunc` host hook is unchanged; it is
not made jailed or writer-fenced by runtime binding.

Remaining integration gates:

- independently authenticated runtime production/publication (the test recipe
  is not a production importer; no installed-tree adoption);
- owned source/output staging, exclusive build-domain transfer, surviving-writer
  Fence/Check, code sealing and paired runtime/code publication;
- trusted current-generation observation/revalidation and atomic policy/
  registry publication;
- queued submitter's live operator/admin reference and publication authorization;
- recovered artifact/domain inspection, reconciliation, proof and restore
  activation gates before serving.

## Focused checks

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -race -count=1 ./backend/internal/builder ./backend/internal/sandbox
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go vet ./backend/internal/builder ./backend/internal/sandbox
```

`runtime_test.go` exercises actual `Builder.Build` syntax/install/tsc wiring
with fake-jail subprocesses, source/config/provider mutation, binding/language/
domain denial, ABI/hash metadata, cache rejection, cancellation and retirement.
`sandbox/build_runtime_pin_test.go` checks independent command ownership,
retirement while running, cancellation/reap/cleanup, start failures and tool
link containment. Existing package tests cover nil/legacy behavior.
These are not real nsjail or cgroup-enforcement proofs.

## Qualification — 16 November 2026

On base `c62ff5823c3d0c7f7ba68c3037616f81b50a3616` plus this six-file overlay,
the final-source package normal/race suites, three focused race repetitions
and vet passed offline in an owned 2-CPU / 4-GiB / 20-GiB Microsandbox.
Real `Builder.Build` also passed normally and under race detection:

- Node **26.13.0**: syntax, local npm file dependencies/postinstall, genuine
  **TypeScript 5.9.3** compilation and verified emitted typed JavaScript.
- Python **3.15.2**: syntax and installation of a local pure-Python wheel.

A guest-only bridge reused the accepted `workloadspawn` test recipe helpers
unchanged to produce fresh strictly sealed runtime generations. The independent
recipe archives retained their existing SHA-256 values:
Node `6a85bea48a4ee93458ca1bb7bf071535928ee2a93ce5792670c9b1a151a0122c`,
Python `59a66da6b75c1394efd3e7f2b8719df23b0d5628e6ea326dd8425a6090077918`.
Publisher rootfs/nsjail checksums and the compiler's registry SHA-512 integrity
were verified in the guest. Actual install steps used only local fixtures under
a reject-all NSTUN policy. No production producer was added or relaxed.

Evidence, exact commands, the guest-only bridge, returned runtime identities/
source archive hashes and full producer manifests are retained at
`/tmp/opencode/iam-builder-runtime/`. The copied tested-source archive SHA-256 is
`0f3183318ce584ae41bd3f690e3d5fcf761d2f4c44cf57c08697da162b15ed62`;
the final Go-file manifest matches the tested copy. This README's qualification
record was appended afterward without changing tested Go files.

The real runs used **nil Domain / rlimit-only**, not delegated cgroup
enforcement. Domain forwarding and invalid-domain denial are unit-tested;
exclusive per-build ownership/fencing is still missing. Full `make lint`,
`make test` and sandbox-required full E2E remain separate checks.

The remaining gates are visible in existing source: `Queue.Submit` stores
`BuildJob` without a live operator reference and `runJob` uses a background
build context; `Build` still writes `.orva-ready` and replaces mutable version
directories; `workloadpolicy.Service` still requires a trusted current-state
observer; `artifactfs.OpenRecovering` remains inspection-only; and
`workloadfence.Domain.Fence`/`Check` require ownership/proof not supplied by
this binding. None is activated or bypassed here.

## Owned managed output — separate opt-in

`New(WithRuntimeBinding(provider), WithManagedOutput(owner))` selects a distinct
actual `Build` path. Construct `owner` with `NewOutputOwner(OutputConfig)` from
a trusted code store, one ownership profile below, and a fresh empty private
metadata root.
No serving constructor or queue enables it. Nil owner, absent runtime binding,
invalid budgets/limits, provider-supplied Domain, `BuildFunc`, and syntax hooks
deny; host hooks are never invoked in this mode.

Exactly one ownership profile is required:

- `ParentDomain: instance` is the typed profile for future production consumers.
  Budget and raw Parent must be absent. Actual managed Build calls
  `instance.NewChild(ctx, privateMetadata)`; it does not mkdir or infer a cgroup
  parent. All build commands enter the child's supervisors and relocate only
  within its owned jails, transitively beneath the instance. Root Fence prevents
  new builds/starts even through a retained OutputOwner.
- Historical `Budget` plus raw `Parent` remains an internal compatibility
  profile, with no ParentDomain. It does not establish instance ancestry and
  must not be used by future serving/recovery wiring. Mixed or missing profiles
  deny; there is no fallback between them.

### Deliberately narrow production profile

The owner approved a **plain Node/Python, no-dependency, no-TypeScript** first
slice. Root `package.json`, `requirements.txt`, or `tsconfig.json` denies before
any command, even if the manifest is empty. Entrypoints must be plain
`.js`/`.cjs`/`.mjs` or `.py`. No npm/pip/npx process runs here.
The bounded data-only archive extractor refuses links, special entries,
extended metadata, duplicates and over-budget inputs. Archive UID/GID do not
authorize host chown; newly produced files belong to the host producer.
Actual output UID/GID, POSIX modes, link text and hardlink relationships are
observed without rewriting or filtering when constructing expectations.

This restriction matters: existing `RunBuild` grants writable code and unlimited
file size, not an aggregate disk quota. A post-fence scan cannot retroactively
enforce a byte reservation during arbitrary install scripts. Until a reviewed
storage-quota boundary exists, installing/compiling third-party code is not part
of this managed profile. The pinned interpreter's syntax-only tools are trusted
not to execute author code or make unbounded writes. This is not a general
disk-quota or hostile-runtime containment claim.

### Exclusive epoch and staging ownership

The output owner reserves job count and bytes **before** the provider call.
Build copies builder configuration and only its consumed function identity/
runtime/entrypoint scalars (not unused environment/description maps or strings)
under that admission, then captures the runtime once through the
accepted runtime path. An arbitrary Domain returned by that provider is rejected,
never fenced. Instead, Build creates fresh private metadata and a typed
`ParentDomain.NewChild` Domain. Only the legacy profile creates a raw child
cgroup parent and `CreateUnderBudget` Domain. No Domain, proof,
stage handle, writable descriptor or staging path is exported by the output API.
The host must provision delegation and move only its own daemon into a leaf;
production code does not capture budgets or alter ancestor controls for it.
Keep code-store/metadata namespaces outside legacy GC-swept directories.
Trusted provider/logger callbacks must cooperate with cancellation and return;
admission bounds their outstanding jobs, not arbitrary host callback execution.

The source is extracted into **new** `CodeStore.Begin` staging under explicit
compressed/decompressed archive, header/entry, path, total-file and per-file
bounds. No legacy `versions/`, current symlink, cache entry, `.orva-ready` or
installed tree is adopted, overwritten or used as code authority. The
no-install syntax command uses the same private Domain and pinned runtime.
No legacy adapter wrapper or ready marker is needed or written.

After the command returns—or build context/finite build timeout expires—the
owner uses a separate bounded cleanup context for **Fence and Check on that
exact private epoch**. It also waits for the command runner to finish. Only then
may its private producer inspect the completed stage, compute bounded typed
manifest expectations and call strict `Stage.Seal(KindCode, functionID, ...)`.
The producer accepts no arbitrary path or caller-supplied "fenced" flag. It
checks the opaque proof before and after inspection. The store independently
verifies the complete tree. Unsupported links/xattrs/special nodes deny, without
stripping them or changing the verifier. The runtime anchor survives fencing,
stage cleanup and metadata/cgroup cleanup; no supervisor-Wait writer proof is
substituted.

### Failure and bounded explicit cleanup

Errors and cancellation return **no BuildResult**. Before Stage.Abort/removal or
runtime-anchor release, cleanup closes entry, fences and checks all writers.
If no Domain could be created, no Start capability or build writer ever existed.
If a fence/check/runner-completion or later cleanup fails, the output owner
retains the stage/returned failed-seal generation, runtime lease, Domain/proof,
private metadata, and logical reservation. The error includes `ErrOutputPending`.
`Usage()` exposes bounded active/pending counts and reserved bytes; new work
denies with `ErrOutputCapacity` when active plus pending slots/bytes are full.

`RetryCleanup(ctx)` is explicit, serialized per pending job and bounded by the
configured cleanup timeout. It never seals or promotes quarantined output.
Once proof and command completion are available, it aborts/reclaims and removes
only its own private groups/metadata, then releases the runtime anchor and slot.
Typed builds use `ParentDomain.ReclaimChild` for fresh proof/identity/emptiness
checks and bounded child-only cgroup removal, retaining ancestor pins until
success. Only then are metadata and the runtime anchor released. A partial
NewChild failure returning a child remains charged/quarantined; missing durable
evidence never becomes a proof. After successful fencing, partial cleanup
retries continue only that already-closed private ownership; they never reopen
entry or manufacture a new epoch. Host repair may be needed for integrity or
permission failures.

OutputOwner copies share one state. Retain the owner/Builder until `Close`
succeeds; Close denies new work and returns `ErrOutputPending` while any job
remains. There are no cleanup finalizers, automatic background retry loops,
GC-based reclamation, or process-restart adoption. Limits bound one owner's
logical prepared/cleanup state, not global jail counts, disk usage or kernel
resource enforcement. Surviving owners must explicitly retry; after process
loss, persisted metadata/stores require separate host inspection/recovery.

### Result and remaining boundaries

A successful managed `BuildResult` records `CodeArtifact`, `RuntimeArtifact`,
`RuntimeABI`, `SourceArchiveSHA256`, `AuthoredEntrypoint`, and resolved
`Entrypoint`. `CodeHash` retains the archive+ABI fingerprint input; it is **not**
the sealed code digest. `CodeArtifact.Digest` is that separate full manifest
identity. New metadata is excluded from legacy JSON. Managed `VersionDir` and
`ImageTag` remain empty and `Cached` is false. The caller consumes the exact
generations via store-acquired leases; retirement may deny that later acquisition.
Successful sealed generations remain under store ownership until explicit
retirement/reclamation, not under the output owner's active-job quota.

Neither a successful build, the closed epoch proof, nor either artifact digest
is policy approval, current-generation authority, a source/config/secrets
transaction, or a serving grant. Durable output/runtime mapping, queued
submitter live authorization, registry/policy publication, serving/pool cut-over,
and power-loss/restore reconciliation and activation are still separate gates.
Stable trusted ancestor directories, exclusive cooperative host ownership,
no outside writable aliases/mounts, and no reuse/tampering of live metadata,
stage or epoch paths are prerequisites—not defenses against a privileged host.

### Owned-output qualification — 17 November 2026

This is a new five-file builder acceptance scope on
`8985523155b0d32ae805fd196be84e2c179ddd62`, not an inferred extension of the
earlier runtime-only result. Artifactfs, workloadfence, Sandbox.RunBuild,
runtime pinning helpers, and strict runtime recipe functions were unchanged.
In a fresh 2-vCPU / 4-GiB / 20-GiB guest with Go 1.27.3, the final Go source passed:

- all builder/sandbox/workloadspawn package tests normally and under race
  detection, plus vet, offline under explicit env-i allowlists;
- physical managed-output tests under umasks 022 and 077 and three race
  repetitions: pre-exec same-private-epoch membership, frozen input capture,
  delayed-grandchild fencing before Seal, error/cancel fencing before Abort,
  fence-failure quarantine with retained runtime lease/quota, eight concurrent
  cleanup retries, capacity races, bad binding/budget denial, unsupported
  link/xattr denial, no legacy replacement, and no-install/hook rejection;
- actual Node 26.13.0 and Python 3.15.2 Build → private Domain Fence/Check →
  strict fresh code Seal → Factory → real worker, under both ordinary umasks
  and race detection. The workers used the exact returned code/runtime pair,
  executed SDK/lazy imports after retirement, and retained both leases during
  deliberately blocked post-Wait cleanup.

The independently authored fixture code manifests matched the builder's sealed
digests in every run: Node
`e0f14915b2fb81aee0eb4fb71930ee18aa6ee97f83d52bd77edf82d54a889121`,
Python `c97bc06093cfda393807a6f2c2c1355731357111b2f73654d189f7542d9b2a03`.
Runtime recipe archive hashes remained exactly the earlier authenticated recipe
values; no installed-tree adoption or verifier/profile relaxation was used.
Unlike the earlier runtime-only qualification, build and worker epochs here
used explicitly delegated cpu/memory/pids domains. This proves the tested
enrollment/fencing paths, not a CPU/memory overload or global-capacity guarantee.
The SDK remained an identity fixture, not live IAM admission.

Exact commands, final source manifests, full runtime and authored code manifests,
returned identities/fingerprint inputs and physical logs are retained under
`/tmp/opencode/iam-managed-output/`. The verified tested-source snapshot SHA-256
is `716fdcc0830ed195e0cb87d85e7d0820701ae04d14bef99a2fbe79f31e9615c9`;
its Go hashes match the final checkout. This qualification text was appended
afterward without editing tested Go files. The task-owned guest and all its
explicit test budgets were removed; the final sandbox listing was empty.

Full repository make lint/test, build-all and sandbox-required full E2E were
not run. Installation/TypeScript output, durable publication/live submitter
authorization, current-generation/IAM/serving cut-over and restore activation
remain outside this result.
