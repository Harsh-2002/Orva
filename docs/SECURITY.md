# Security model

This document explains how Orva isolates user-supplied code from the host
and from other functions. It is descriptive, not prescriptive — every
claim below is grounded in a specific file/line that you can audit.

## Staged function-IAM publication boundary (inactive)

`database.PublishWorkloadEnvironmentAsOperator` revalidates current operator
credentials and admin permission inside the same transaction as the protected
environment/generation/policy/audit mutation. It reads no bearer or token hashes.
API keys require current admin permission and valid expiry; OAuth also requires
a live client/user, client scope ceiling and exact trusted audience; sessions
require a live UUID-backed session/user and expiry. Audit actors are derived
from the checked reference, not treated as authorization. Checks occur before
mutation and commit; expiry during commit cannot be excluded.

`SaveIAMPolicyAsOperator` applies the same live checks to policy CAS, epoch and
audit. The internal `workloadpolicy` core joins ordered registry/IAM publication
with that transaction, but requires a trusted current-artifact observer which
serving has not installed. Explicit review, not ordinary replacement, clears
quarantine/review flags. Empty-policy revocation does not require usable runtime
artifacts and never activates retained grants.

These remain **not serving authorization features**. Credential UUIDs are not
authenticators. API-key REST authentication and MCP bearer resolution capture
token-free immutable credential references in a private context key; OAuth
includes its initially validated audience, never a request override. AI request
contexts inherit these values. This carries provenance, not frozen permissions;
publication still rechecks current credentials transactionally.

Legacy browser sessions do not turn cookie prefixes into references and clear
inherited operator context. After explicit session-identity schema initialization,
new sessions receive independent UUIDs transactionally. Browser authentication
and its cache carry only that stable reference and display label, with cache
validity bounded by observed session expiry. The reference is never serialized
into the session API response and does not replace commit-time live checks.
Normal boot still does not initialize that schema. Queued build capture,
authoritative production artifact publication and public IAM adapters remain
unwired. Audience-less legacy OAuth authentication remains unchanged, but supplies
no IAM publication reference. Persistence-only APIs retain their existing behavior.
See `docs/FUNCTION_IAM_PLAN.md` for the remaining cut-over gates.

The inactive `workloadspawn.Factory` retains exact code/runtime anchors and
owned config/secrets; prepared-plan quotas do not bound live workers or global
provider memory. Retirement prevents new preparations, not already-issued
execution. Current-generation denial must still publish under the IAM gate
before obsolete bootstrap code can obtain authority.

`workloadfence` separately owns a durable Linux cgroup epoch, enrolls trusted
commands before exec and mints an opaque death proof only after durable entry
closure and kernel recursive emptiness. Captured service budgets and jail-parent
capabilities constrain resource delegation and relocation within that epoch.
Trusted-host ownership and stable/no-reuse assumptions remain prerequisites.
No serving constructor or artifact recovery consumer uses this package; passing
kernel checks does not authorize recovered artifacts, reclamation or IAM grants.

Optional sandbox Domain inputs compose that epoch with Spawn, Execute and
RunBuild. Host nsjail and its jailed descendants remain inside the same domain;
requested controller failure denies rather than falling back to global placement.
Opt-in CPU quotas require exact integer-millisecond representation and use that
same validated quota in argv. Nil-domain behavior is unchanged. Independent
kernel membership and limit readback checks are not resource-exhaustion stress,
serving integration or proof that completed build outputs have no surviving writer.

The builder's internal runtime-binding option captures one runtime generation
for syntax, dependency and compiler steps, retaining a pipeline anchor and
command leases through Wait/cleanup. Full runtime identity is distinct from the
interpreter ABI used for dependency caching. Invalid/retired bindings and managed
cache hits without provenance deny rather than resolving mutable rootfs paths.
The serving constructor does not enable this option. Capturing a supplied Domain
does not confer exclusive ownership or prove all build descendants have exited.

The separate managed-output mode creates its own exclusive build Domain and
fresh artifact staging. Fence/Check must succeed before output inspection,
sealing or deletion. Failed cleanup retains staging, runtime and finite quota
ownership for explicit retry; it never returns an approved output. Host hooks,
provider-supplied Domains, dependency manifests and TypeScript deny in this
limited plain Node/Python profile. Archive parent components reject before
normalization. This does not bind stores to an instance epoch, publish current
generations, enable recovery or restore approval.

Typed child Domains additionally serialize creation and command entry against
their ancestors. Parent fencing covers descendant builds, while child fencing
does not stop sibling workers. Managed output can require this concrete
ParentDomain profile; historical raw-budget provisioning is mutually exclusive.
Attached-child pins retain partial-construction/reclaim cleanup ownership.
Trusted-host topology, no outside entrants and deliberate boot-owner wiring
remain prerequisites. Nested placement alone does not bind persistent stores
to the instance epoch or permit recovery.

Internal `artifactfs.Owner` uses one durable association record for both code
and runtime stores. Its same-boot recovery verifies the bound epochs and
Fence/Checks them before inspecting assets, then commits a new association
before exposing verified committed generations. Incomplete transitions deny;
unfinished assets remain quarantined. Unbound recovery remains inspection-only.
This does not restore policy authority, enable serving, repair interrupted
transitions or solve cold-start/reboot budget capture. See `artifactfs/OWNER.md`.

The unregistered permissions HTTP adapter authenticates a live operator and
derives its reference/capabilities server-side. Policy mutations still recheck
live admin authority in the shared transaction. The separate live scratch
fixture connects this adapter and the real UI controller to Factory-created
Node/Python workers and private-SDK KV enforcement. It is not the serving pool,
production browser login, complete SDK enforcement or production route wiring.

The subsequent internal managed-pool fixture uses the actual public invoke,
Proxy and acquired-worker paths for KV and two-sided buffered calls. Frozen
Factory facts supply resources, secrets, identity and acquired timeout/header
configuration. Shared-gate current-generation checks deny obsolete workers even
when their old policy remains approved; generation-bound release cannot return
one to a replacement pool. Independent delayed-import/replacement race runs and
cross-runtime browser denial/grant/revocation journeys passed. Normal serving
construction remains unchanged. This is not production activation, complete
mutation-writer coverage, stream/async acceptance or hard-resource enforcement.

## Threat model

Orva is **single-tenant**: one operator, many functions, possibly many
end-users hitting those functions. The platform's security goals,
ordered by importance:

1. A function cannot read or write **another function's data**
2. A function cannot read or write the **host filesystem** outside its
   own `/code` mount and its private `/tmp`
3. A function cannot **escalate to host root**, even though it appears to
   run as UID 0 inside its sandbox (more on this below)
4. A function cannot **exhaust host resources** beyond its declared
   memory / CPU / pid limits
5. A function cannot make **arbitrary network calls** when network mode
   is `none` (default — isolated net namespace, loopback only). Functions
   that need outbound HTTPS opt in by setting `network_mode: egress`
6. A function cannot obtain **Orva's own operator credentials** from the
   request that invoked it (see [What reaches user code from the
   request](#what-reaches-user-code-from-the-request))
7. A caller cannot **forge Orva's own signals to a function** — the
   `x-orva-*` namespace is server-set, so `webhook.parse().verified` and
   the function-to-function recursion cap mean what they claim to
8. A function's **response cannot act as the dashboard** — same-origin
   output carries a restrictive CSP so a reflected-input function is not a
   path to the control plane (see [What reaches the browser from your
   function](#what-reaches-the-browser-from-your-function))

Orva is **not** designed to defend against:
- A malicious operator running on the same host (they own the keys)
- A kernel-level zero-day (no seccomp filter is bulletproof against
  kernel exploits — see `nsjail` upstream's stance)
- Side-channel attacks (Spectre / Rowhammer / cache timing) — these are
  out of scope for any container-grade isolation

## Sandbox SDK identity

SDK authority is an explicit per-function `sdk` permission, independent of
outbound networking. New functions default to disabled. Existing egress
functions are enabled once during migration; explicit revocation survives
restart. Receiving a function call does not grant the callee SDK access.

An enabled worker receives one private inherited socketpair endpoint. The host
binds the function ID to the connection; request arguments cannot select another
identity. Each dispatch receives a random lease, revoked when the invocation
ends. Function calls, jobs, cron declarations and spans require that live
execution; KV also works at module scope using the connection's function ID.
A stale lease on KV is treated as module scope, not as another execution.

Permission changes retire existing workers: idle workers stop, while in-flight
invocations retain their authority until completion/reaping. New work uses the
new permission. User environment and secrets cannot inject the reserved channel
settings or retired legacy credential variables.

SDK calls never use HTTP. No `ORVA_INTERNAL_TOKEN` or `ORVA_API_BASE` is issued,
the internal SDK HTTP routes are removed, and `/api/v1/jobs` only accepts normal
operator authentication. The channel cannot authenticate to operator APIs.
SDK-enabled functions require a compatible installed adapter; upgrade the
adapters with the server rather than expecting an HTTP fallback.

Streams are connection/lease-scoped, limited to eight per worker channel, and
cancelled at lease end or channel close. Frames are capped at 4 MiB, with 32
in-flight calls and bounded pending replies. Function-call depth is host-derived
and capped at eight. KV is function-namespaced with bounded page/batch replies.

## Credential classes and what revocation reaches

Orva has three independent credential classes. Each has its own lifetime and
its own place to revoke it:

| Class | Grants | Revoke at |
|---|---|---|
| **API keys** | full instance management | Settings → API keys |
| **Channel tokens** | MCP only, invoke only, over a named set of functions — and they bypass those functions' `auth_mode` by design | Channels page |
| **OAuth grants** | issued to a connected application (claude.ai, ChatGPT) through the consent screen | Settings → Connected applications |

**Deleting an API key does not revoke channel tokens or OAuth grants created
with it, and that is deliberate.** They are separate credentials somebody
consented to, not delegations of the key — the same model GitHub and Google
use. Minting a channel requires `admin`, so only a top-privilege key could have
created one in the first place. Cascading would also be unable to help on any
instance that upgraded into the feature, since existing rows record no
minting key.

Channel-token expiry is optional: a channel created without one never expires.

If you believe an API key was compromised, delete the key **and** review the
Channels page and Settings → Connected applications, removing anything you do
not recognise. Revoking a key takes effect at once on every surface; you do not
need to restart.

## What reaches user code from the request

Your handler receives the inbound HTTP request as `event.headers`. Orva's own
credentials are removed first — otherwise invoking a function would hand it
the credential that authorized the invocation. One function,
`proxy.SanitizeForwardedHeaders`, does this for every path that reaches a
sandbox: `/fn/{id}`, custom routes, and inbound webhooks.

| header | reaches your handler? |
|---|---|
| `X-Orva-API-Key` | **no** — dropped |
| `X-Orva-Internal-Token` | **no** — dropped |
| `Proxy-Authorization` | **no** — dropped |
| `Cookie` | yes, **minus `session_token`**; your own cookies survive |
| `Authorization` | yes, **unless** it carries an `orva_`-prefixed credential |
| any `X-Orva-*` header | **no** — the whole namespace is dropped, including `X-Orva-Timestamp` and `X-Orva-Signature`. The server sets the ones a handler needs (`x-orva-trigger`, `x-orva-execution-id`, …) itself, afterwards, so a caller cannot forge one |
| everything else | yes |

`session_token` is the dashboard session: `Path=/`, full control-plane
authority, and accepted as invoke auth. The dashboard's own Test/Invoke button
sends it with credentials, so forwarding it would leak operator authority to
sandboxed code on the most ordinary UI action there is.

A third-party `Authorization` **is** forwarded — a function doing its own JWT
verification needs it. Only Orva-issued credentials (every one of which is
`orva_`-prefixed) are withheld.

The `x-orva-` namespace is the server talking to its own adapter, so an
inbound one is dropped wholesale and the server sets what it needs afterward.
Two of those headers are load-bearing and were previously caller-writable:

- **`x-orva-trigger`** is what `orva.webhook.parse(event).verified` is
  derived from, and this document's own guidance tells handlers to gate on
  it. A caller who could set it on a direct `/fn/` request passed that check
  with no HMAC ever computed. It is also what the shipped cron template
  branches on to decide whether to delete KV rows.
- **`x-orva-call-depth`** seeds `ORVA_CALL_DEPTH`, which the SDK forwards on
  nested invokes and the function-to-function endpoint compares against its
  cap. A negative value made that comparison false forever, so recursion was
  unbounded — consuming a worker and an instance-wide sandbox-limiter slot at
  every level.

The whole prefix is dropped rather than a denylist of those two, so a header
added to the namespace later cannot inherit the problem. The call-depth
endpoint additionally clamps a negative value, because the cap is the only
thing bounding that recursion.

> This set is deliberately **not** the same as `redactHeaders` in the same
> file. That one governs what replay capture refuses to persist — it also
> covers `Authorization`, because a captured request is stored. This table
> governs what enters the sandbox. A future credential header belongs in both.

A handler's returned headers **replace** the platform's on non-OPTIONS
responses, so a function can widen its own `Access-Control-Allow-Origin` past
`ORVA_CORS_ORIGINS`. That is a boundary fact, not a convenience: the
platform's CORS setting does not constrain what a function chooses to answer.

Two exceptions. `Content-Security-Policy` and `X-Content-Type-Options` are
stamped **after** the handler's headers and cannot be overridden (see [What
reaches the browser from your
function](#what-reaches-the-browser-from-your-function)). And hop-by-hop and
framing headers a handler returns — `Content-Length`, `Content-Encoding`,
`Transfer-Encoding`, `Connection`, `Keep-Alive`, `TE`, `Trailer`, `Upgrade`,
`Proxy-Authenticate`, `Proxy-Authorization` — are **dropped** rather than
relayed, because they describe the adapter's transfer, not ours. A handler
proxying a gzipped upstream used to return the compressed `Content-Length`
alongside a body the adapter had already decoded, and the response was
truncated.

## What reaches the browser from your function

Function output is served from the same origin as the dashboard and the API,
and the dashboard session cookie is `Path=/` with `SameSite=Lax`. `auth_mode`
defaults to `none`, so a public function that reflects any part of its input
would otherwise be same-origin XSS: script in that response could drive
`/api/v1/keys` with the operator's cookie attached.

Every function response therefore carries:

| header | value |
|---|---|
| `Content-Security-Policy` | `sandbox allow-scripts allow-forms allow-popups allow-downloads; frame-ancestors 'none'; base-uri 'none'` |
| `X-Content-Type-Options` | `nosniff` |

**The control is the origin, not the content.** `sandbox` without
`allow-same-origin` places the document in an *opaque* origin, so script in a
function's response cannot read the dashboard's origin or issue credentialed
same-origin requests to `/api/v1/*`. Scripts, forms, popups and downloads are
all still allowed, because functions legitimately serve interactive pages —
Orva's own guestbook template posts a form and runs an inline script.

`allow-scripts` **without** `allow-same-origin` is the safe pairing; granting
both together would let a document remove its own sandbox.

These headers are applied **after** the handler's own, so a function cannot
opt itself out. What this costs an HTML function is same-origin state:
`document.cookie`, `localStorage` and `sessionStorage` are unavailable in an
opaque origin. Keep state server-side (Orva's KV store) rather than in the
browser.

`frame-ancestors 'none'` also means function output cannot be embedded in an
iframe elsewhere.

Orva also strips hop-by-hop and framing headers (`content-length`,
`content-encoding`, `transfer-encoding`, …) from the handler's response,
because the adapter copies every header off its `fetch` Response. A handler
proxying an upstream would otherwise return the compressed `content-length`
alongside a body the adapter had already decoded, and the response would be
truncated.

## What's between user code and the host

```
host kernel
  └─ docker container "orva"             ← UID 0 inside container; needs CAP_SYS_ADMIN
      └─ orvad (Go server)                  to construct sandboxes
          └─ nsjail process                ← unshare(CLONE_NEWUSER) drops effective caps
              └─ user namespace
                  ├─ chroot to runtime rootfs (read-only)
                  ├─ /code bind-mount     ← read-only, function-private
                  ├─ tmpfs /tmp           ← private, wiped on worker exit
                  ├─ cgroup v2: memory + CPU + pids
                  ├─ seccomp filter       ← ~150 syscalls blocked
                  └─ user code (node / python)
```

The gap between layers is enforced by the Linux kernel, not by Orva
code. Orva configures the boundaries and trusts the kernel to keep
them.

## What "running as root inside the sandbox" actually means

User code's UID depends on the mode the installer verified for the host, and
**neither mode is root on the host.**

nsjail's `-Mo` flag (`internal/sandbox/sandbox.go`, `buildArgs()`) creates a new
user namespace. Without explicit mappings nsjail maps the invoking account to
the same UID *inside* the namespace, so a bare-metal install shows the service
UID (`orva`, commonly 999) and a Docker install — whose daemon is root in the
container — shows 0. The value is not the boundary: every capability set is
dropped, and actions that cross the namespace boundary (writing a host-root
file, mounting outside the chroot, signalling a host PID) are denied because
the function holds no capabilities outside its namespace.

On hosts that cannot create an unprivileged user namespace, the installer falls
back to nsjail's file capabilities and `--disable_clone_newuser`. There is no
UID translation in that mode: the sandbox runs as the service account, and the
boundary is the mount, PID, network, IPC and UTS namespaces, the chroot, seccomp,
and a zero effective capability set. The installer probes the user namespace
with a **capability-free** nsjail first, because a binary carrying file
capabilities cannot create one (its `setgroups` setup is denied); capabilities
are applied only when that probe fails.

Verify it yourself:

```bash
# Deploy a fn that introspects /proc/self/status
KEY=$(docker exec orva cat /var/lib/orva/.admin-key)

curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions \
  -d '{"name":"whoami","runtime":"node","memory_mb":128,"cpus":1}'

FID=$(curl -s -H "X-Orva-API-Key: $KEY" http://localhost:8443/api/v1/functions \
  | jq -r '.functions[] | select(.name=="whoami") | .id')

curl -X POST -H "X-Orva-API-Key: $KEY" -H 'content-type: application/json' \
  http://localhost:8443/api/v1/functions/$FID/deploy-inline \
  -d '{"code":"const fs=require(\"fs\");module.exports=async()=>{return fs.readFileSync(\"/proc/self/status\",\"utf8\").split(\"\\n\").filter(l=>l.startsWith(\"Cap\")||l.startsWith(\"Uid\"));}"}'

curl -X POST -H "X-Orva-API-Key: $KEY" \
  http://localhost:8443/fn/${FID} -d '{}'
```

Expected output (the meaningful lines):

```
Uid: 0  0  0  0               ← Docker: daemon is root in the container.
                              ← Bare metal: the service UID (e.g. 999).
CapInh: 0000000000000000      ← 0: no inheritable caps
CapPrm: 0000000000000000      ← 0: no permitted caps
CapEff: 0000000000000000      ← 0: the control — nothing effective
CapBnd: 00000000002014c0      ← may be non-zero: inherited; inert (below)
CapAmb: 0000000000000000      ← 0: nothing ambient
```

`CapPrm`, `CapEff`, `CapAmb`, and `CapInh` are zero in every mode. `CapBnd`
depends on the deployment: empty when the installer selected user namespaces
(the unit's `CapabilityBoundingSet` is emptied), and the service/container
bounding set in the capability fallback. An inherited bounding entry is inert —
the sandbox runs with `NoNewPrivs=1` and neither runtime rootfs contains a
setuid binary — but it is not zero, so `CapEff` is the field to read.

In user-namespace mode the service unit keeps `NoNewPrivileges=false`. systemd
implements `ProtectSystem` as a read-only mount, and combining it with NNP makes
nsjail's own read-only bind inside the user namespace fail with EPERM — deploys
break while invokes keep working. The empty bounding set and the absence of file
capabilities are the controls in that mode; nsjail still sets `NoNewPrivs` on
the sandboxed process itself.

## Filesystem isolation

Configured at `internal/sandbox/sandbox.go:154-160`:

```go
"-Mo",                          // standalone-once mode; userns by default
"--chroot", rootfs,             // pivot_root into the runtime's rootfs
"-R", cfg.CodeDir + ":/code",   // read-only bind mount of the function's code
"-T", "/tmp",                   // private tmpfs at /tmp
```

- The runtime rootfs (`/var/lib/orva/rootfs/<runtime>/`) is the only
  filesystem visible. It contains the language runtime + standard libs.
- `/code` is a read-only bind mount of the function's code directory
  (`<dataDir>/functions/<id>/current` → symlink to `versions/<hash>/`).
  Read-only means the function cannot write back to its own code dir,
  which prevents persistent compromise of the deployment artifact.
- `/tmp` is a fresh tmpfs per spawn — wiped when the worker exits or is
  reaped. Functions can write here freely; nothing escapes.
- No other host paths are visible. sysfs and the host cgroup tree are absent
  from the chroot. `/proc` is **not** absent: nsjail mounts a fresh, read-only
  procfs scoped to the sandbox's own PID namespace, so a handler can read
  `/proc/self/*` and see its own processes — and nothing outside them. That is
  deliberate; language runtimes need it. Anything a handler learns there is
  about itself, not about the host.

## Capability dropping

By default nsjail puts the function in a user namespace; the UID inside is the
invoking account (0 for the Docker daemon, the service UID on bare metal), and
it holds **zero capabilities outside that namespace**. On a bare-metal host that
blocks unprivileged user namespaces, the installer uses nsjail file capabilities
for setup and starts it with `--disable_clone_newuser`; nsjail drops privileges
before running the adapter, while the remaining mount, PID, network, IPC, UTS,
chroot, and seccomp boundaries stay enabled. The installer probes the user
namespace with a capability-free binary first, applies file capabilities only
if that probe fails, and always probes with the actual `orva` service account,
binary, and rootfs; running the daemon as root is neither required nor an
accepted workaround.
nsjail does not need to call `prctl(PR_SET_NO_NEW_PRIVS)` separately —
the user namespace gives the same effect for cross-namespace operations.

Inside the namespace, the function's `CapEff` reads as `0`
(`/proc/self/status`). This means even calls that the kernel checks
against effective caps (`CAP_NET_ADMIN`, `CAP_SYS_PTRACE`,
`CAP_SYS_MODULE`, `CAP_SYS_BOOT`, etc.) all fail.

## Seccomp filter

`internal/sandbox/seccomp.go` defines a Kafel-syntax policy passed to
nsjail via `--seccomp_string` (sandbox.go:198). Three policies ship:

- **`default`** (used by all functions unless overridden): allows the
  curated syscalls modern Node/Python need and returns `EPERM` for any
  syscall outside the allowlist. Notably
  blocks: `mount`, `umount`, `pivot_root`, `unshare`,
  `clone(CLONE_NEWUSER)`, `bpf`, `kexec_load`, `init_module`,
  `delete_module`, `iopl`, `swapon`, `reboot`, `setns`, `userfaultfd`.
- **`strict`**: tightens `default` further by blocking many `*at` calls
  and most ioctls. Use for high-trust untrusted code.
- **`permissive`**: extends `default` with the outbound networking syscall
  set. It remains an allowlist and continues to deny privileged kernel
  operations such as `mount`, `bpf`, and module loading.

The policy is compiled by `BuildSeccompPolicy` and passed inline with
`DEFAULT ERRNO(1)`. Returning an error rather than killing the process lets
Node and Python fall back when an optional facility such as io_uring is
unavailable. Linux seccomp's `LOG` action is deliberately not used as a
default because it records an event but still permits the syscall.

## Resource limits

Per-function caps are enforced in two places:

- **Per-process**: `--rlimit_as max` preserves the service user's existing
  hard address-space limit; it is a fallback, not the declared function
  memory budget.
- **Per-cgroup** (when cgroup v2 delegation is available — the
  `cgroupv2Delegate()` branch of `sandbox.go`'s `buildArgs()`):
  - `memory.max` at **1.5×** the declared `memory_mb`. The 0.5×
    headroom lets the kernel reclaim via PSI pressure before OOM-killing.
    The 1.5× factor matches the autoscaler's per-worker admission budget.
  - `pids.max` at the configured `MaxPids` (default 32).
  - `cpu.max` via `cgroup_cpu_ms_per_sec` (e.g., `cpus: 0.5` →
    500 ms of CPU per 1000 ms wall — fractional CPU as bandwidth, not
    affinity, so the scheduler can load-balance freely).

When cgroup controllers are not delegated, Orva deliberately refuses to pass
invalid cgroup arguments to nsjail and falls back to `rlimit_as`. It reports
`sandbox.resource_limits: "rlimit_only"` plus a warning in health and Settings.
`--rlimit_as max` only preserves a hard limit the service user already had, and
the shipped systemd unit sets `LimitAS=infinity` — so on an undelégated host
there is **no hard memory bound**; the pool's admission accounting is the only
brake. Delegate cgroup v2 controllers for a real memory, pid, and CPU boundary.

Automatic detection stays inside the daemon's own service/container cgroup.
Orva moves only itself into `orva.daemon`, enables the delegated controllers on
the now-empty parent, and puts nsjail children under `orva.workers`. It does
not climb to a writable host ancestor or create worker groups outside the
service/container budget. If the visible cgroup is `/`, contains another
process, or lacks writable child limit files, it reports `rlimit_only` instead
of claiming hard enforcement. `ORVA_CGROUPV2_MOUNT` is an explicit operator
override and must point at an already delegated worker subtree **inside that
same service/container cgroup**; a host-root or sibling override is rejected.
The Docker entrypoint first moves its known `tini` parent and CLI bootstrap
helper into a sibling `orva.supervisor` leaf inside that same container cgroup;
otherwise they would occupy the parent and prevent controller enablement.

The host-wide concurrency cap (`cfg.Sandbox.MaxConcurrent`, see the
`TOO_MANY_REQUESTS` error) is enforced at the Go layer in
`internal/sandbox/limiter.go` — sandbox spawns wait or fail-fast there
before any nsjail process is created.

## Network isolation

`network_mode: none` (default for new functions) sets up the function in
a fresh network namespace with **no interfaces other than loopback**.
Outbound HTTP from a handler fails with `ENETUNREACH` — DNS, TCP, UDP all
blocked. This is the safe default; flip a function to egress only if it
genuinely needs to call out.

`network_mode: egress` — opt-in per function. Adds nsjail's
`--user_net` flag, which gives the sandbox a userspace TCP/UDP stack
(the bundled NSTUN backend) that NATs out via the host. **Host network
interfaces are still not exposed**; the function can dial outbound but
can't see (or be seen by) other tenants on the same node. Use this for
handlers that talk to Stripe, OpenAI, your DB, or any external API. Every
egress worker also carries the compiled egress policy described below —
it cannot spawn without one.

Switching the toggle drains the warm pool so the next invocation
respawns with the new mode within seconds. The Functions list in the UI
shows an "egress" badge on rows that have it on, so operators can audit
at a glance which functions can talk to the network.

Future modes (`egress+allowlist` / `private`) would extend the same
field without another schema migration.

### Sandbox egress policy

When a function is in `egress` mode, an instance-wide blocklist applies
on top: IPs/CIDRs/hostnames that no function can reach regardless of the
per-function toggle. The list is managed from the dashboard's **Egress
controls** page (`/web/firewall`), or via `POST /api/v1/firewall/rules` /
`orva firewall add` — there is no config file. Rules live in the
`egress_blocklist` SQLite table.

Enforcement is **per sandbox**, inside nsjail. `internal/firewall`
compiles the enabled rows into nsjail NSTUN `user_net { rule4 / rule6 }`
rules, writes them to an immutable generation file under
`<dataDir>/firewall/policy/egress-<gen>.cfg`, and every egress worker is
spawned with that file as `--config` (argv[0..1], before any other flag —
nsjail's config loader overwrites everything set earlier). There is no
host firewall table, no `nft` invocation, and no packet filter outside the
sandbox's own network namespace.
Published generation files stay available until the next daemon startup so a
worker that captured an older path can still load its policy; stale files are
pruned before new workers can spawn.

**Rule order is a security control.** NSTUN is default-ALLOW and
first-match-wins, so the compiler emits carve-outs before rejects:

1. nsjail's own NSTUN gateway (`10.255.255.1`, `fc00::1`) — it lives
   inside `10.0.0.0/8`, so a private-network reject would otherwise cut
   the guest off from its own default route.
2. The configured DNS resolvers, port 53, UDP + TCP.
3. The operator's blocklist, as REJECT rules — routed by address family, so
   an IPv4 target becomes a `rule4` and an IPv6 target a `rule6`. A hostname
   rule contributes a `/32` per resolved v4 answer and a `/128` per v6 one.

The policy is a **blocklist in both families**: there is no wholesale IPv6
deny. A destination you block by IPv4 address is not automatically blocked
over IPv6 — if a service is reachable on both, block both, exactly as you
would with any other pair of addresses.

Every address is re-parsed and canonicalised by Orva before it reaches
the config file, and anything ambiguous is refused rather than emitted.
That is not defensive politeness: NSTUN's rule compilation is **fail-open**
— an out-of-range prefix yields a zero mask, and a zero mask matches every
address. A malformed rule in nsjail does not become a stricter rule, it
becomes a wildcard.

**Fail-closed.** If no policy has compiled successfully, an egress sandbox
refuses to start (`sandbox.ErrEgressPolicyMissing` — the invocation fails
rather than running unfiltered; see [`ERRORS.md`](ERRORS.md) for the slug).
`status.enforced: false` is the signal to watch for. If a *recompile* fails,
the last known-good generation stays in force and the status reports
`policy_stale: true` with `last_compile_error`. A malformed config is fatal
to nsjail (exit 255), so a corrupt policy also fails closed.

**Policy is fixed at worker start.** NSTUN reads its rules once, at spawn.
Editing a rule publishes a new generation and retires the warm egress
pools so the next spawn picks it up; an in-flight worker keeps the exact
generation it started with. Recycles are rate-limited to one per 60 s, so
a flapping DNS answer behind a hostname rule cannot turn into a cold-start
storm. Expect a few seconds — and a cold start — between saving a rule and
seeing it applied.

Where to look: `GET /api/v1/firewall/rules` returns a `status` object with
`backend` (always `nstun`), `enforced`, `policy_generation`,
`policy_rule_counts`, `policy_stale`, and
`unenforced_rules`. The old `nftables_available` field is **gone**, with no
compatibility alias — see [`API.md`](API.md#firewall-status).

**What changed from the nftables-based build.** This replaced a host-wide
`nft` implementation. The differences are behavioural, not cosmetic:

- **Scope narrowed.** The old build installed a host table
  (`inet orva_firewall`) with an `output` hook, which filtered *every*
  process in the daemon's network namespace. The new policy only exists
  inside each egress sandbox.
- **The daemon is filtered separately.** So that narrowing does not become
  a hole, orvad's own outbound connections are checked at the dialer
  against the same compiled rule set, in the same order — outbound
  webhooks, builder package installs (`npm install` / `pip install`), and
  anything a cron trigger causes. There is no SDK control-plane address
  exemption: SDK operations use the private worker channel.

  The AI gateway is the exception: the embedded Bifrost gateway owns its own
  transport and exposes no hook to install Orva's dialer, so Orva checks a
  provider endpoint against the same compiled rule set **before** the turn
  rather than at dial time. A destination the operator has blocklisted is
  refused there, naming the subsystem —
  `ai-gateway: destination blocked by egress policy: tcp 10.1.1.20:11555`.

  Bifrost's own dialer then independently re-resolves at dial time and
  **unconditionally** refuses link-local (`169.254.0.0/16`, `fe80::`) and
  unspecified addresses. That is not configurable and is what closes the
  cloud-metadata rebinding case.

  RFC1918 is the one part Orva decides. Bifrost refuses private destinations
  unless `AllowPrivateNetwork` is set, and Orva sets it **only when the
  operator's own configured base URL is itself private** — an IP literal in a
  private range, or a hostname that resolves entirely to private addresses.
  For a public provider (`api.openai.com`) it stays off, so a public hostname
  can never be rebound onto a private address mid-turn. This is what makes a
  LAN-hosted endpoint — ollama, vLLM, an internal gateway — reachable at all;
  before it, Bifrost's blanket default silently overrode the operator's
  explicit configuration, and the refusal surfaced as nothing more useful than
  "failed to execute HTTP request to provider API". The operator's blocklist
  remains the authority either way: block `10.0.0.0/8` and the preflight
  refuses a LAN provider even though the flag would have permitted the dial.

  Residual risk: a **public** address the operator has blocklisted could still
  be reached by rebinding within a turn, because the preflight resolves earlier
  than the dial. Changing provider configuration requires admin, and an
  attacker holding the provider hostname's DNS already receives the API key.
- **Blocked destinations now report `ECONNREFUSED`**, not `EHOSTUNREACH`.
  Handlers (and log-scraping alerts) that string-match the old errno need
  updating.
- **Wildcard rules are unsupported and rejected.** Creating or enabling a
  `*.example.com` rule fails with `400 VALIDATION`. Packets carry
  addresses, not names. The old build silently resolved only the domain
  apex — it blocked `example.com` and none of its subdomains while the UI
  claimed otherwise. Rows that already exist are left exactly as they are
  (their `enabled` flag is never rewritten), excluded from the compiled
  policy, and listed in `status.unenforced_rules` with a reason. Use a CIDR
  or an exact hostname instead.
- **SDK traffic has no network exemption.** The inherited channel works in
  `network_mode: none` and is unaffected by private-network block rules.
  Egress sandboxes have no automatic exception for Orva's HTTP address. The
  operator's private-network blocklist applies to that destination like any
  other private service; there is no `ORVA_INTERNAL_API_BASE` override.

**Sharp edge — the RFC1918 suggestions now also apply to orvad.**
Because the daemon is filtered by the same rules, enabling the shipped
`suggested` rules — `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`,
`100.64.0.0/10` — blocks **orvad's own** outbound calls to those ranges as
well as your functions'. That breaks, for example:

- an internal npm or PyPI mirror used at build time,
- a LAN-hosted LLM endpoint configured as an AI provider,
- an outbound webhook target on your private network.

The data model is a pure blocklist: there is no allow-exception rule you
can add on top. The remedy is a **narrower CIDR** — block the subnet you
actually want denied instead of the whole RFC1918 range (e.g.
`10.42.0.0/16` rather than `10.0.0.0/8`). All four suggestions ship
**disabled** — opt in deliberately, and check that nothing orvad needs lives
in the range first. The two `default` rules that ship *enabled*
(`169.254.0.0/16`, `fd00:ec2::254/128` — cloud metadata) are addresses
nothing on the platform needs.

**Honest note on the previous behaviour.** Egress enforcement in the nftables
build was **a silent no-op on any host without the `nftables` package or
without `CAP_NET_ADMIN`** — the rules were stored, the UI listed them, and
nothing filtered packets. It had
**never** worked on Alpine/OpenRC at all: the shipped OpenRC service
granted no capabilities, so orvad could not program nftables there under
any configuration. Availability also gated the manager's poll loop, which
meant operator DNS changes stopped reaching sandboxes on those hosts too,
while boot-time behaviour made it look like they had applied.

The NSTUN policy has no such host dependency: it needs `/dev/net/tun`
(already required by `network_mode: egress` itself) and nothing else. If you
relied on the Egress controls page for filtering on a host in either
category, treat this release as the point where it started working — not as a
change to a working feature.

## What Orva itself runs as

The **orvad process inside the orva container** runs as UID 0 inside
the container. This is intentional and required: nsjail needs
`CAP_SYS_ADMIN` to set up the user namespace, mount namespace, and
cgroup hierarchy that isolate the function. Without those caps, the
sandbox can't be constructed and there's no isolation at all.

The container itself is launched with:

```
--pid host
--cgroupns host
--cap-add SYS_ADMIN
--security-opt seccomp=unconfined
--security-opt apparmor=unconfined
--security-opt systempaths=unconfined
--device /dev/net/tun
```

`--pid host` + `--cgroupns host` let nsjail enroll each sandbox PID in the
host cgroup hierarchy (per-function `memory.max` / `cpu.max`); without them
sandbox spawn fails. On the `kata-clh` runtime the guest kernel provides this
delegation, so those two are not needed there.

`--device /dev/net/tun` exists for nsjail's `--user_net`: it opens the TUN
device and configures the interface inside each sandbox's fresh network
namespace. It is not used to program a host firewall — orvad does not touch
host network configuration at all.

`NET_ADMIN` is deliberately **not** granted. nsjail creates the TAP device
inside its own user namespace, where it already holds the capability, so the
container never needs it. Verified by running the sandbox with `SYS_ADMIN` and
the TUN device only. The one exception is forcing `ORVA_DISABLE_USERNS=1`
inside a container: that removes the user namespace, and `TUNSETIFF` then
requires real `CAP_NET_ADMIN` from the initial namespace.

These apply **only to the orva container**, not to functions. Within
the container, only the orvad process and its child nsjails benefit;
user functions inherit nothing through nsjail's capability drop.

If you want to run **orvad itself** as a non-root UID inside its
container, that's a separate hardening exercise (rootless Docker is the
standard path). Tracking issue / future work — let us know if this
matters for your deployment.

## Common questions

> "Can someone with a write API key redirect another function's traffic?"

No. Two independent guards. A custom route may not be created under a prefix
Orva serves itself — `/api/`, `/auth/`, `/web/`, `/_orva/`, `/fn/`, `/mcp/`,
`/webhook/` — and the REST endpoint and the MCP `set_route` tool validate
against the same list. Separately, a direct `/fn/{id}` call resolves its
function from the path and never consults the route table at all, so even a
pre-existing catch-all `/*` route cannot shadow it.

This matters because `auth_mode` and the per-function rate limit are evaluated
against the **resolved** function. If a route could redirect `/fn/{id}`, a
request aimed at a `platform_key`-gated function would be served by whatever
function the route named — including one with `auth_mode: none`.

> "What stops a caller from getting a fresh rate-limit bucket per request?"

Every limiter Orva has — per-function `rate_limit_per_min`, the login
brute-force throttle, and the OAuth dynamic-registration cap on
`POST /register` — buckets on one shared identity, and by default that is the
**TCP peer address**, which cannot be forged over a completed handshake.
`X-Forwarded-For` and `X-Real-IP` are ignored unless the operator sets
`ORVA_TRUSTED_PROXY=true`. They used to be trusted unconditionally, so a caller
who sent a different `X-Forwarded-For` on each request had no invoke limit and
no login throttle at all.

With the flag on, Orva reads the **rightmost** `X-Forwarded-For` entry — the
one your proxy appended — not the leftmost. Everything to the left was supplied
by the client, so a leftmost read leaves the same bypass in place one hop
further in. The flag is an assertion that the peer is always your proxy, so
pair it with `ORVA_HOST=127.0.0.1` or a firewall; a client that can reach Orva
directly while it is set can still choose its own bucket.

> "I saw `WARNING: Running pip as the 'root' user' in the build log
> earlier. Was my function being installed as root?"

The pip command runs **inside the orva container** during the build
phase, NOT inside your function's sandbox. pip sees `getuid() == 0`
because orvad is UID 0 inside the container; the warning is pip
complaining about *its own* environment, not anything about your code.
Suppressed in current builds via `--root-user-action=ignore`. Your
function still runs in the user-namespace-isolated sandbox at runtime.

> "Can a function read another function's secrets?"

No. Secrets are decrypted by orvad and passed into the sandbox's environment
at spawn time (`sandbox.go`, `buildArgs()`). Each sandbox sees only its own
function's secrets; the secret material never leaves orvad and is never
readable from any sandbox filesystem.

> "Can a user on the host read a function's secrets?"

Not from the process list. Values travel in the nsjail child's environment and
argv carries only the variable *name* — `--env SECRET`, which nsjail resolves
from its own environment. This matters because `/proc/<pid>/cmdline` is
world-readable and the shipped compose runs `pid: host`, so anything in argv is
visible to every local user for as long as the call runs.
`/proc/<pid>/environ` is owner-only (mode 400), so the value is readable only
by the user orvad runs as, and by root. Earlier builds passed
`--env KEY=VAL` in argv and also logged the full argv at debug level.

> "Can a function read another function's code?"

No. Each function's code lives at
`<dataDir>/functions/<id>/versions/<hash>/`. Only the active version's
directory is bind-mounted into the sandbox at `/code`, and the bind is
read-only. There is no shared `/functions/` mount.

> "What happens if a function tries to fork-bomb?"

cgroup `pids.max` (default 32) caps the process tree. Spawning past
that limit fails with `EAGAIN` inside the sandbox. The orvad scheduler
also tracks per-pool memory reservations and refuses to admit new
workers when host memory budget is exhausted (see
`internal/pool/hostmem.go`). It reserves each worker's full cgroup
`memory.max` budget, not its recent RSS percentile: every worker could grow
at once. Public HTTP invocations additionally reserve a
conservative portion of daemon memory **before** reading their bodies; unknown
lengths are charged as if they reached the configured body cap. The pending
request count also scales with the detected memory/FD envelope, with a share
kept for another function. These are daemon overload guards, not substitutes
for per-sandbox cgroup limits; when cgroup delegation is unavailable, health
still reports the weaker `rlimit_only` boundary.

> "What can the in-product AI assistant do, and where do its provider
> keys live?"

The assistant (the dashboard's **AI** section) is gated behind the
`admin` permission — every `/api/v1/ai/*` route requires it (the
dashboard session resolves to admin; a non-admin API key gets 401/403).
It operates the instance through the **same** operator tools the MCP
server exposes, so it can do anything an admin can do via the API, and
nothing more. Two gates sit in front of every mutation: the
per-conversation **approval policy** (`all_writes` / `destructive_only`
/ `auto`), which can pause a write for human approval before it runs,
and a code-enforced `confirm=true` requirement on destructive tools.

Conversations are a **shared operator space**: because the feature is
single-operator and admin-only, all admin credentials see the same
conversation list (they are not isolated per credential). BYO provider
API keys are encrypted at rest with the same AES-256-GCM cipher as
function secrets, are decrypted only in orvad at request time, and are
never returned by the API or echoed into the chat (asserted by the e2e
suite). The system prompt also instructs the model never to print
secret/key/token values it encounters while operating the instance.

A provider's `base_url` is intentionally unrestricted (it may point at a
local endpoint such as Ollama or LM Studio — a first-class homelab use
case). Since configuring a provider is admin-only, the SSRF surface this
opens is an admin-only one and is accepted by design.

## Layered isolation: what does and doesn't compose

Orva already gives you defense in depth on a single host: a Docker
container around orvad, nsjail-per-function inside it, seccomp + cgroup
limits inside nsjail. Operators sometimes ask about wrapping the whole
stack in another isolation layer.

**Hypervisor-class (Kata Containers, Firecracker, full VMs).** Compose
cleanly with Orva — they put a real Linux kernel under the entire
orvad container, so nsjail's namespace API works unchanged inside the
guest. Cost is a guest kernel per Orva instance.

Kata is the configuration we've verified end-to-end (2026-05-13, Kata
3.30.0). Both QEMU and Cloud Hypervisor work; recommend
`docker run --runtime=kata-clh …` (smaller Rust-based hypervisor TCB,
~14 s faster container-start, throughput identical to QEMU within
noise). Orva itself is unchanged underneath. Full operator guide
including measured performance cost: [`docs/KATA.md`](KATA.md).

**gVisor (`runsc`) — does NOT work.** End-to-end testing on 2026-05-13
(gVisor `release-20260504.0`, both `ptrace` and `kvm` platforms)
confirmed that nsjail's per-function sandbox setup needs
`clone(CLONE_NEW…)` for seven namespaces, and gVisor's user-space
kernel rejects the combination with `EINVAL`. The Orva daemon starts
under runsc and the HTTP API is reachable, but every function
invocation fails with `WORKER_CRASHED`. This is architectural, not a
bug — gVisor doesn't expose nested-namespace primitives, and nsjail
can't run without them. Full reproduction + alternatives in
[`docs/GVISOR.md`](GVISOR.md).

## Known advisory: GO-2026-6911 (mcp-go redirect SSRF)

`github.com/mark3labs/mcp-go` follows cross-origin redirects in its MCP
*client* transport and forwards configured headers and OAuth tokens to the
redirected host. Every version is affected and there is no fixed release
(upstream issue mark3labs/mcp-go#1188, fix PR #1190, unmerged as of
2026-11-07).

Orva is not exposed. The module is reached only through the embedded Bifrost
gateway's MCP client, which Bifrost builds only when it is given an `MCPConfig`
or `AddMCPClient` is called. Orva passes neither and dispatches its own tools
in-process; provider calls use Bifrost's HTTP stack, not mcp-go.
`llm.TestGatewayHasNoMCPManager` fails the build if the gateway ever builds an
MCP manager.

`govulncheck` still reports the finding because its call graph cannot see that
condition, so the CI gate (`scripts/govulncheck-gate.py`) carries one expiring
exception for this ID. It fails on any other call-reachable finding, when a fix
becomes available, and on 2026-12-07. Delete the exception when a fixed mcp-go
is required by a Bifrost release that builds with this tree.

## Reading further in the code

- `internal/sandbox/sandbox.go` — nsjail invocation
- `internal/sandbox/seccomp.go` — syscall policy
- `internal/sandbox/limiter.go` — host-wide concurrency cap
- `internal/pool/hostmem.go` — memory admission control
- `internal/proxy/proxy.go` — request → sandbox bridge

If you find a security issue please open a private security advisory
on the GitHub repository (Settings → Security → Advisories) rather
than a public issue.

## Internal managed Function IAM increment (inactive)

The staged named-policy store/service is not registered in production and its
schema is not initialized at boot. Immutable versions are attached by explicit
pin, never followed automatically. Effective authority is a union of separately
retained inline and managed sources. Detach and terminal global revoke remove
only their own contribution; overlapping grants can remain.

The internal service validates at most 25 affected owners, takes sorted mutation
locks followed by the IAM gate, rechecks live admin and source/definition/
membership CAS in one transaction, then publishes every owner together. Failed
COMMIT changes neither live authority nor cancellations. Source-only reductions
preserve approval and quarantine/review state and can proceed with explicitly
unknown current artifacts; they never approve different code. Cancellation
callbacks run after IAM and owner locks are released and before acknowledgement.

This does not qualify migration/restore, function deletion, privileged deployment,
async enforcement or real SDK/UI/CLI/MCP behavior. The legacy inline-only HTTP
adapter refuses managed mode. The precise internal contract and remaining gates
are in `backend/internal/workloadpolicy/MANAGED.md`.
