# Explicit managed Factory pools (internal, opt-in)

Normal serving construction is unchanged: `ManagedAuthority == nil` uses the
existing registry/current-path/secrets behavior. No serving constructor,
operator flag, route, schema initializer or SDK default enables managed mode.

An internal host can supply one fixed Authority in `ManagerConfig`, then
`InstallFactory` a concrete, exclusively owned `workloadspawn.Factory`. Successful
installation transfers lifecycle ownership; do not independently close/retire
that Factory. Missing/uninstalled owners deny, without legacy fallback.

Candidates are built before lifecycle/admission locks. Pool memory reservations,
CPU units, concurrency semaphore/policy and worker spawns derive from the same
Factory ResourceFacts. Only non-security warm-count/idle tunables come from the
pool-config database row. Spawn bypasses mutable registry/current paths and
`SecretsLookup`: Factory preparation and concrete code/runtime leases supply
the frozen configuration, environment, secrets and identity. Prepare, Spawn,
readiness I/O and cleanup occur outside admission/pool locks. Egress still needs
a compiled policy; SDK-enabled factories require the real private host.

Installation/removal order is manager lifecycle mutex → old pool mutex →
Authority gate. Under the gate, only bounded in-memory generation publication,
active/retired transitions and pool registration occur. The immutable observer
map is bounded to 256 owners. Factory retirement/Close and idle-worker Kill run
after releasing the gate and pool locks. Late old spawns check retirement before
return; existing pool publication also checks its closing state. Acquisitions
retain their exact pool pointer, so releasing an old acquisition cannot park it
in a replacement pool. Managed reaping also performs Kill outside its pool lock.

`CurrentArtifact`/`IsCurrentArtifact` and `TargetState` are gate-held observers:
bounded atomic/immutable facts only, no DB, filesystem or worker mutex. Target
state checks the supplied acquired worker's original identity, readiness,
liveness and current active artifact, not policy existence. Worker readiness/
lease/death transitions use this Authority's hook configured before import.

The SDK host **must** receive `WithIAMCurrentArtifact(manager.IsCurrentArtifact)`
alongside `WithIAM(theSameAuthority)`. Current caller admission is then checked
inside short-operation/call/frame admission, so retained old policy cannot
authorize an obsolete worker after replacement. `WithIAMTargetState` can use
`manager.TargetState`; this does not itself install an F2F invoker or claim
complete SDK coverage. Omitting the new current-artifact option retains existing
staged SDK behavior; explicitly providing nil denies.

The public Proxy reads timeout/header facts from the actual managed
AcquireResult, never a fresh function row, while leaving nil-mode behavior
unchanged. Other public authentication/status decisions remain the existing
InvokeHandler behavior. The scratch fixture seeds those records from the same
prepared configuration.

## Qualification scope

`test/live-iam/README.md` describes the real UI → HTTP policy Service → public
InvokeHandler/Proxy → Manager → Factory → nsjail private-SDK KV journey. The
external workloadspawn integration fixture uses test-only exports of existing
strict recipe helpers, avoiding a pool/workloadspawn/SDK test import cycle.
No generic runtime parser/import API is exported in shipping code.

Real generation-replacement probes cover delayed old import not being acquired,
retained-policy old-worker denial immediately after replacement, exact
generation-bound double-release safety, target-state checks, frozen resource/
config/secret inputs and explicit approval of the new artifact.

This is not coordinated production deployment, secret/config/status mutation,
queued work, persistent current authority, recovery, boot, full asynchronous/
streaming/F2F SDK, or hard resource-limit acceptance. InstallFactory is not a
transactional/durable writer framework or a replacement for the future ordered
production mutation service. All such writers must join the same publication/
admission boundary before production registration.
