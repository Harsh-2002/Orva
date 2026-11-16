# Inactive frozen-generation spawn factory

`NewFactory` composes the existing strict `NewPlan` with code/runtime stores and
generation IDs. It is internal, opt-in, and **has no serving or pool caller**.
No public flag, database lookup, current-path fallback, new attestation layer,
fingerprint format or relaxed validation is introduced.

## Owned inputs and repeated plans

The caller supplies a strictly prepared `secrets.PreparedArtifactSnapshot`, two
stores/generation IDs, a non-nil context and explicit `FactoryLimits`.
Construction must run while the caller exclusively owns the input snapshot.
Concurrent mutation **during** construction remains forbidden.
The owner must explicitly Close/Retire the factory and consume or Close every
issued plan; there are no finalizers or background cleanup goroutines.

Construction bounds snapshot cost before preparation/cloning, acquires an
anchor pair through `artifactfs.PreparePaths`, and calls `NewPlan` to validate,
clone and transfer it. The factory retains one detached configuration/secret
snapshot with owned string backing and maps. It retains the validated owner,
generation token/revision/epoch, concrete code/runtime identities and the
unchanged v2 artifact fingerprint. Prepared audit metadata is not retained or
promoted into authority.

Every `Prepare(ctx)`:

1. Reserves pending/starting count and bytes before acquiring or cloning.
2. Acquires a **new pair** from the same store-minted generation IDs.
3. Calls `NewPlan` on the same immutable frozen snapshot.
4. Rechecks cancellation/retirement and returns an independently owned one-use
   plan, or releases the pair and its reservation on error.

It never reloads config, secrets, policy or paths. Caller changes after
construction cannot mix a new configuration or secret snapshot with an old
generation. Environment/secret contents are still deliberately excluded from
the v2 fingerprint: their authoritative invalidation requires generation
rotation outside this package.

`Resources()` returns detached scalars from **exactly the same frozen function**
used by every plan: memory MB, CPUs, timeout, max concurrency, concurrency
policy, rate limit, network mode and SDK selection. Identity, generation and
resource observations do not consult mutable source input. These are accounting
facts, not evidence that a kernel enforces the requested limits.

## Finite prepared accounting

All limits are explicit and positive:

| Limit | Constructor ceiling |
| --- | --- |
| `MaxPreparedPlans` | 65,536 |
| `MaxPreparedBytes` | 1 GiB |
| `MaxSnapshotBytes` | 256 MiB |

The conservative logical charge for each plan (and the frozen snapshot bound)
is 4096 fixed bytes, plus eight times all retained function/generation and
environment/secret string bytes, plus 512 bytes per environment/secret map
entry. Each map also remains subject to `NewPlan`'s strict count and byte
limits. Arithmetic is bounded before addition/multiplication. The count is
reserved under the same factory mutex as the aggregate byte charge. A snapshot
which cannot fit even one prepared plan is rejected during construction.
The one base snapshot is bounded separately, not counted as a pending plan;
the configured per-factory logical bound is snapshot plus prepared capacity.

These charges conservatively budget owned config/secret data, map-copy and
fingerprint preparation work. They are **not measured heap use, an OS quota,
global admission across factories, or a reliable Go string-erasure promise**.
Factory count and active worker memory need separate future provider/pool
budgets. SDK goroutines, host-template closures, worker heap and worker-retained
environment data are not bounded by prepared-plan accounting.

A reservation stays charged while a plan is pending **or inside `Spawn`**,
including blocked start, cancellation and failure cleanup. Taking a plan,
closing a losing copied handle, or closing the factory does not release that
reservation. The single consuming `Close`, or the exit of the single consuming
`Spawn`, returns it exactly once. Successful Spawn returns prepared capacity
only after the worker has taken its independently owned concrete tree leases.
The worker retains those leases through its existing centralized post-Wait
cleanup. Prepared capacity returning does not mean the worker stopped.

`Usage()` reports current pending/starting plans/bytes and the fixed per-plan
charge. It acquires the factory mutex and is **not gate-observer safe**.

## Retirement and cut-over obligations

Factory copies share a monotonic retirement bit, anchor ownership and quotas.
`Retire`/`Close` prevent later preparations and release only the anchor, outside
the factory mutex. A preparation overlapping retirement either wins the
publication boundary or closes its unpublished plan. An already-issued plan
remains caller-owned until its own Close/Spawn; it can still start after factory
retirement. Factory retirement never closes issued plans, kills workers,
reclaims trees or erases still-referenced input data.

`Retired()` is a bounded atomic observation and acquires no factory mutex.
Immutable identity/resource accessors also acquire no factory mutex. None
establishes current generation, approval, credential liveness or IAM admission.

**The future host current-state and IAM invalidation must join their shared
gate before an obsolete worker can import/bootstrap user code.** Publish the
authoritative deny/inactive transition there, then perform factory retirement
and cleanup outside the gate. A factory or issued-plan reference cannot replace
that gate. This package deliberately does not try to make context cancellation,
factory retirement and kernel exec one atomic event.

Run constructor/Prepare, cloning, store acquisition and all cleanup outside
IAM, pool and registry locks. Retire/Close may acquire the factory mutex and
release paths; they are not gate-held callbacks. Providers still need bounded
factory/worker ownership, current-artifact publication and invalidation,
bootstrap admission, live policy/operator checks, runtime producer approval,
generation rotation, and fail-closed recovery/lifecycle fencing. There is no
recovery activation or serving integration here.

## Focused checks

Use a fresh 2-CPU / 4-GiB / 20-GiB Microsandbox (no host mounts, credentials,
ports or private egress), or existing offline tooling without installing host
dependencies:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -count=1 ./backend/internal/workloadspawn
CGO_ENABLED=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -race -count=5 -run '^TestFactory' ./backend/internal/workloadspawn
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go vet ./backend/internal/workloadspawn
```

Factory regressions use store-minted leases and the existing **fake jail** for
wire, identity, frozen environment/resource binding and post-Wait lifetime
checks. They do not themselves qualify real Node/Python execution or cgroup
enforcement. The four runtime producer/qualification files are unchanged.
The starting-quota witness pauses a controlled context check after plan Take
and before launch; it is not an experiment blocking a real kernel exec syscall.

## Qualification record — 16 November 2026

The ordinary package suite, five offline focused factory race runs and vet
passed in a fresh 2-CPU / 4-GiB / 20-GiB Debian trixie Microsandbox. The tested
source was committed base `3ac2f940f6fb45b3ff4fa381a8915ec7db3c270f` plus the
uncommitted factory/plan overlay and frozen runtime tests. Separate file hashes
identify the exact tested code; no later concurrent parent changes were
silently substituted into that snapshot.

Checks cover immutable config/secret capture, resource-to-spawn binding, v2
fingerprint stability and code/runtime/generation changes, count/byte rollback,
partial acquisition, constructor rejection, paused starting reservations,
cancellation, copied Close/Spawn races, retire/prepare races, and post-Wait
lease retention. Owned Go-string backing and coverage of retained function
string/reference fields are checked explicitly.

This factory qualification used the fake jail only. The prior real Node/Python
runtime recipe qualification does **not** become real-runtime factory or pool
acceptance. Full E2E, kernel resource enforcement, serving/provider integration,
current-generation admission and recovery gates remain outside this result.

## Real-runtime factory qualification — 16 November 2026

This is a **new acceptance scope**, not an extension of the earlier fake-jail
verdict by inference. `factory_runtime_qualification_linux_test.go` adds an
opt-in real factory test. `runtime_qualification_linux_test.go` shares only its
fresh-store, authenticated runtime recipe/seal and authored-code/seal setup.
Its existing plan execution assertions remain intact. The factory implementation,
plan reservation hook, factory unit tests, strict package parser and explicit
producer recipe were unchanged from their accepted source hashes.

In a fresh 2-CPU / 4-GiB / 20-GiB Debian trixie Microsandbox, independently
publisher-checksummed base packages and nsjail produced the same explicit
runtime recipe artifacts described in `RUNTIME_QUALIFICATION.md`. Each runtime
then used fresh sealed code/runtime generations through `NewFactory`, two
`Prepare` calls, and **two real nsjail workers**:

- Node **26.13.0** and Python **3.15.2** both passed normally and under `-race`.
- Original config/secret maps, function fields and generation metadata were
  mutated between preparations. Both handlers still returned the original
  config/secret values and host-owned identity.
- Two distinct worker UUIDs and live nsjail PIDs shared one artifact fingerprint,
  code/runtime generation pair and frozen resource descriptor per factory.
- Handler-observed memory MB, timeout, entrypoint, function identity and SDK
  protocol matched the frozen inputs despite template/source mutation. CPU and
  concurrency descriptor scalars were checked, not asserted as kernel limits.
- Pending quota was charged for both issued plans and returned one reservation
  at each Spawn return while the workers remained caller-owned and alive.
- Factory retirement released its anchors and denied new preparations. The
  second already-issued plan still spawned after factory and store retirement.
- Both handlers made execution-bound private-channel SDK calls and lazy
  code/runtime imports after retirement. Tree reclamation remained `ErrBusy`
  with worker/issued-plan owners and with real post-Wait callbacks blocked.
  Releasing those callbacks allowed final reclamation and store Close.

Authored factory code provenance:

| Runtime | Handler source SHA-256 | Code manifest SHA-256 |
| --- | --- | --- |
| Node | `a44888154cffb1f9d1ba569e16fd8e4acf60616c07ed5bbea5ae572b5e7fdd57` | `417f67953572503206be975955b5cc53b9903c98647dd9c4f4181cf42800f0ac` |
| Python | `ca83711542ad3f83bd819c47c7a00ac49b6e59cb60eec2d5e870a420e1f806ab` | `967c7c4e3bd3f89a160c491c32e0da3229f663cbb047bb1541135ea4c7d44808` |

Run inside the disposable guest after the verified package setup documented in
`RUNTIME_QUALIFICATION.md`, with a distinct fresh recipe output parent per run:

```sh
mkdir /fresh-factory-output
ORVA_FACTORY_RUNTIME_TEST=1 ORVA_RUNTIME_RECIPE_OUTPUT=/fresh-factory-output \
  GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -race -count=1 -v -run '^TestFactoryRuntimeQualification$' ./backend/internal/workloadspawn
```

The ordinary package suite, five focused factory race runs and vet also passed.
The refactored original positive recipe qualification passed under race
detection; its raw-package negative control still rejected **both** unmodified
published runtimes at strict Seal. No positive or negative assertion was
weakened. Historical accepted files/hashes and failed-package evidence remain
separate from this new source manifest.

Evidence, exact commands, new code/recipe provenance and the tested source
snapshot are under `/tmp/opencode/iam-factory-runtime/`. The base was
`21a20461f180ec6856b2091a35c5588ae095ddad` plus the exact accepted factory overlay
and this test/helper overlay, not concurrent parent changes.

The SDK handler was an **identity-checking fixture**, not actual IAM admission.
The resource-fence path remained legacy/nil and the VM reported **rlimit-only**;
no cgroup hard-limit proof follows. Current authority, bootstrap/pre-exec gate
integration, provider/pool ownership budgets, recovery activation, full E2E and
production deployment remain unqualified by this result.

The copied tested-source snapshot SHA-256 is
`b77ae5100af1f592eff15e2dd662b4b75dfb4f985a6075719152c1973f0f0697`;
the tested Go-file hashes match the final checkout. The task-owned
`iam-factory-runtime-test` VM was stopped and removed after evidence capture;
the final sandbox listing was empty. No host dependencies, production access,
ports, host mounts, commits or pushes were used.
