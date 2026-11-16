# Inactive workload spawn composition (Linux)

`NewPlan` joins a `secrets.PreparedArtifactSnapshot` (normally returned by the
transactional preparation service) with store-minted `artifactfs.PreparedPaths`.
It validates and clones configuration, environment and secrets, then atomically
transfers the pair. Constructor failure leaves the caller's pair untouched.
Preparation inputs must not be concurrently mutated during construction.
The preparation struct is not opaque: accepting it does not authenticate its
origin or prove that the database transaction actually happened.

The domain-versioned SHA-256 artifact revision covers owner UUID, durable
generation UUID/revision/epoch, function version and source code hash, both
store generations and manifest digests, and non-secret execution configuration.
The source code hash and code manifest digest have different meanings and need
not match. Environment/secret names and values are excluded entirely; their
future authoritative invalidation depends on transactional generation rotation.
Each environment/secret map is independently bounded to 1024 entries and 8 MiB
of key/value bytes, matching existing secret snapshot ceilings. No reliable
erasure of Go strings is promised.

`Spawn` consumes the plan once, including on failure/cancellation; copies share
the same consumption state. `Close` releases only unused plans. A successful
sandbox spawn transfers concrete tree lifetimes to the worker's post-Wait
cleanup. Old caller handles and concurrent Close cannot release those leases.
No pool reference accounting or readiness admission is added.

Function-controlled execution fields and environment come only from the frozen
snapshot. Secrets overlay configuration; host identity, limits and entrypoint
overlay secrets. Case-insensitive SDK descriptor/protocol and legacy HTTP
credential/discovery variables are stripped, with no HTTP fallback. SDK false
disables the template handler; SDK true requires a host handler and the existing
adapter capability check. Neither environment claims nor SDK claims mint plans.

The template remains **trusted host input** for nsjail, PID limits, seccomp,
firewall/DNS paths, SDK handler, lifecycle hook and exit callback. Its seccomp
policy must already be compiled for the frozen function's network mode, not
inferred from SDK access. This package cannot verify a supplied policy's
provenance or semantics. Environment, language, identity, artifact, resource
limits, legacy code/rootfs paths and verified paths from the template cannot
override the plan. Spawn failures expose generic errors, not underlying
diagnostics potentially containing input values. The frozen limits here are
function memory, CPU and timeout; template PID/seccomp/host limits remain trusted
host configuration, not fields authenticated by this fingerprint.

## Not IAM approval or production authority

There are no serving callers, migrations, pool changes, or operator APIs here.
Legacy mutation paths do not maintain the staged generation. A plan can be
internally consistent but stale; it does not prove current configuration,
approved policy, runtime compatibility, or trusted manifest/import provenance.
Coordinated mutation services must rotate generations for every relevant
code/config/environment/secret change, revalidate prepared inputs, commit policy
and audit changes, publish current-generation observations and retire pools
under the approved lock order. Restore quarantine, bootstrap admission before
imports, admin authorization, current-policy checks and post-commit failure
reconciliation remain separate acceptance gates. Do not wire this partial seam
into serving or treat possession of a plan as authorization.
