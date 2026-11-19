# Optional workload epoch enrollment

`ExecConfig.Domain` and `BuildConfig.Domain` accept a concrete host-owned
`*workloadfence.Domain`. No serving constructor, pool, builder caller, boot
operation, environment switch, public API, budget capture or recovery consumer
supplies it. Nil retains legacy argument construction, placement, resource
fallback, process handling and cgroup discovery.

For non-nil domains, Spawn, Execute and RunBuild prepare an owned jail-parent
capability requiring **cpu, memory and pids**. Missing/closed/recovering/invalid
domains and unavailable controllers deny before process start; there is no
rlimit-only or unenrolled fallback. The private capability supplies every
`--cgroupv2_mount` and is passed back to Domain.Start for entry-gate revalidation.
No caller-supplied cgroup parent/path or arbitrary Start callback exists.

The host nsjail command is enrolled before exec in the epoch's supervisors leaf.
NsJail's per-child `NSJAIL.<pid>` groups are created under that same epoch's
jails subtree. Opt-in argument construction and worker discovery never invoke
the legacy global delegate. NsJail's parent-side relocation remains **inside**
the already enrolled epoch; it is not an unenrolled post-Start PID-write fallback.
The domain owner must outlive all commands and their descendants.

Memory remains 1.5 times MemoryMB, PID limits retain MaxPids, and positive worker
MaxCPUs retains nsjail bandwidth enforcement. MaxCPUs=0 explicitly means no CPU
cap; builds still have no CPU-cap setting. Delegating cpu does not itself impose
a quota. Opt-in rejects invalid/overflowing memory/PID values and CPU limits
outside nsjail's millisecond resolution/range rather than silently omitting a
requested limit. Existing defaults remain; nil-domain validation is unchanged.
There is no lifetime-only opt-in profile that drops configured resource limits.

All preparation and Start calls must occur outside IAM, registry, pool and DB
locks. The domain owns only entry serialization, not authorization. A prepared
path does not grant durable approval; Domain.Start checks its opaque binding
under the same gate used by fencing. SDK ExtraFiles, process-group attributes
and inherited environment are preserved. Worker Wait, OnExit, SDK teardown and
verified-path release retain their existing centralized post-Wait ordering.
Execute/RunBuild use exactly one Wait after a successful domain Start, retaining
their CommandContext cancellation and result/output contracts. Cancellation or
host-process Wait alone is not a whole-epoch death proof; Domain.Fence is.

This does not connect fence proofs to artifact recovery/reclamation or activate
serving IAM. Verified runtime/code leases and generation approval remain separate
contracts. The physical tests here use publisher-verified runtime bundles plus
the source adapters/SDK; they do not establish artifact-store approval.

## Focused tests

`domain_linux_test.go` includes ordinary denial/verified-path failure tests and
explicitly gated physical tests. Physical checks require:

```text
ORVA_SANDBOX_DOMAIN_TEST=1
ORVA_FENCE_TEST_SCRATCH=disposable-vm
ORVA_SANDBOX_ROOTFS=/runtimes
```

Run them only inside a newly owned disposable VM with checked public nsjail and
Node/Python bundles. Its explicit guest-root fixture delegates controllers,
captures the non-root test service budget, moves **only the test process** into
an owned daemon leaf, verifies the anchor empty, then creates domains through
the public captured-budget API. The test binary must execute directly, without
a Go driver or waiting shell occupying that service budget. No host mounts,
ports, credentials, shared instance, or production resources are involved.

The physical scenarios inspect host nsjail membership, real NSJAIL child and
grandchild membership, memory/PID/CPU control readback, SDK traffic, centralized
worker exit, domain fence emptiness, one-shot/build cancellation, failed-Start
FD cleanup and explicit missing-controller denial across all startup paths.
Fixture children use pipes because the minimal runtime profile has no
`/dev/null`; the tests do not add a device mount to broaden that profile.

Keep the legacy delegate disabled in the test guest using an invalid explicit
legacy override, so compatibility tests cannot move the test process through
legacy setup. This is a test invocation using an existing environment setting,
not a new production flag. Run ordinary package tests and vet offline, then the
race-compiled binary directly with the physical-test variables:

```sh
go test -count=1 ./backend/internal/sandbox
go test -race -c -o /sandbox.test ./backend/internal/sandbox
go vet ./backend/internal/sandbox
# Via msb exec, without a waiting guest shell:
env -i PATH=/usr/local/go/bin:/usr/bin:/bin HOME=/tmp \
  ORVA_CGROUPV2_MOUNT=/invalid-legacy-delegate \
  ORVA_SANDBOX_DOMAIN_TEST=1 ORVA_FENCE_TEST_SCRATCH=disposable-vm \
  ORVA_SANDBOX_ROOTFS=/runtimes /sandbox.test -test.v
```

Record skips explicitly: an ordinary invocation skips physical cases rather
than proving them. Limit readback is not OOM/throttling/PID-exhaustion stress
qualification. No full-repository, distribution, production, live-IAM or
recovery acceptance follows from these package checks.
