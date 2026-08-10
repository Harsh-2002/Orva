# Security model

This document explains how Orva isolates user-supplied code from the host
and from other functions. It is descriptive, not prescriptive — every
claim below is grounded in a specific file/line that you can audit.

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

Orva is **not** designed to defend against:
- A malicious operator running on the same host (they own the keys)
- A kernel-level zero-day (no seccomp filter is bulletproof against
  kernel exploits — see `nsjail` upstream's stance)
- Side-channel attacks (Spectre / Rowhammer / cache timing) — these are
  out of scope for any container-grade isolation

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

User code sees `getuid() == 0`. **It is NOT root on the host.**

The mechanism is `CLONE_NEWUSER` + UID mapping. nsjail's `-Mo` flag
(`internal/sandbox/sandbox.go`, `buildArgs()`) creates a new user namespace where
inside-UID 0 is mapped to host-UID 65534 (`nobody`). All capability
checks happen against the user-namespace's UID, but actions that cross
the namespace boundary (writing to a file owned by host-root,
mounting a filesystem outside the chroot, sending a signal to a host
PID) are denied because the function has zero effective capabilities
**outside** its namespace.

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
Uid: 0  0  0  0
Gid: 0  0  0  0
CapInh: 0000000000000000      ← 0: no inheritable caps
CapPrm: 0000000000000000      ← 0: no permitted caps
CapEff: 0000000000000000      ← 0: no effective caps
CapBnd: 0000000000000000      ← 0: nothing in the bounding set
CapAmb: 0000000000000000      ← 0: nothing ambient
```

UID 0 with **all 64 capability bits cleared**. The function can do
nothing root-on-the-host could do.

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
- No other host paths are visible. The procfs, sysfs, and cgroup
  filesystems are masked by the chroot.

## Capability dropping

By default nsjail puts the function in a user namespace where it appears to
have UID 0 but holds **zero capabilities outside that namespace**. On a
bare-metal host that blocks unprivileged user namespaces, the installer uses
nsjail file capabilities for setup and starts it with
`--disable_clone_newuser`; nsjail drops privileges before running the adapter,
while the remaining mount, PID, network, IPC, UTS, chroot, and seccomp
boundaries stay enabled.
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

**Rule order is a security control.** NSTUN is default-ALLOW and
first-match-wins, so the compiler emits carve-outs before rejects:

1. nsjail's own NSTUN gateway (`10.255.255.1`, `fc00::1`) — it lives
   inside `10.0.0.0/8`, so a private-network reject would otherwise cut
   the guest off from its own default route.
2. The **control plane**: orvad's internal SDK address, exact host, exact
   port, TCP only (see the bug fix below).
3. The configured DNS resolvers, port 53, UDP + TCP.
4. The operator's blocklist, as REJECT rules — routed by address family, so
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
`policy_rule_counts`, `policy_stale`, `control_plane_allow`, and
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
  webhooks, builder package installs (`npm install` / `pip install`), the
  AI gateway's provider calls, and anything a cron trigger causes. Loopback
  and the control plane are always exempt so orvad cannot cut off its own
  internal calls.
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
- **A silent breakage is fixed.** Enabling any RFC1918 suggestion used to
  break `orva.kv`, `orva.jobs`, and function-to-function `orva.invoke`,
  because the internal SDK base handed to sandboxes is deliberately a
  *non-loopback* address (from inside the jail, `127.0.0.1` is the jail's
  own loopback) and is normally RFC1918. A narrow control-plane ALLOW —
  exact address, exact port, TCP — now precedes the blocklist. If that
  address cannot be determined at startup, the policy refuses to compile
  rather than shipping one that breaks the SDK.

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

> "I saw `WARNING: Running pip as the 'root' user' in the build log
> earlier. Was my function being installed as root?"

The pip command runs **inside the orva container** during the build
phase, NOT inside your function's sandbox. pip sees `getuid() == 0`
because orvad is UID 0 inside the container; the warning is pip
complaining about *its own* environment, not anything about your code.
Suppressed in current builds via `--root-user-action=ignore`. Your
function still runs in the user-namespace-isolated sandbox at runtime.

> "Can a function read another function's secrets?"

No. Secrets are decrypted by orvad and injected as `--env KEY=VAL`
flags at sandbox spawn time (`sandbox.go`, `buildArgs()`). Each sandbox sees
only its own function's secrets in its environment; the secret
material never leaves orvad and is never readable from any sandbox
filesystem.

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
`internal/pool/hostmem.go`).

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

## Reading further in the code

- `internal/sandbox/sandbox.go` — nsjail invocation
- `internal/sandbox/seccomp.go` — syscall policy
- `internal/sandbox/limiter.go` — host-wide concurrency cap
- `internal/pool/hostmem.go` — memory admission control
- `internal/proxy/proxy.go` — request → sandbox bridge

If you find a security issue please open a private security advisory
on the GitHub repository (Settings → Security → Advisories) rather
than a public issue.
