# Builder runtime pinning (internal opt-in)

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
ownership. Build therefore does **not** Fence, Check, Close or otherwise claim
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
