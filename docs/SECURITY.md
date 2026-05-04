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
(`internal/sandbox/sandbox.go:154`) creates a new user namespace where
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
  -d '{"name":"whoami","runtime":"node22","memory_mb":128,"cpus":1}'

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
"-Mo",                          // mount mode: ownership-mapped userns
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

The `-Mo` flag puts the function in a user namespace where it appears
to have UID 0 but holds **zero capabilities outside that namespace**.
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
  ~250 syscalls modern Node/Python need; blocks the rest. Notably
  blocks: `mount`, `umount`, `pivot_root`, `unshare`,
  `clone(CLONE_NEWUSER)`, `bpf`, `kexec_load`, `init_module`,
  `delete_module`, `iopl`, `swapon`, `reboot`, `setns`, `userfaultfd`.
- **`strict`**: tightens `default` further by blocking many `*at` calls
  and most ioctls. Use for high-trust untrusted code.
- **`permissive`**: blocks only the unambiguously dangerous syscalls
  (`init_module`, `kexec_load`, etc.). Use during dev when you need to
  run something that the default policy refuses (e.g., a custom runtime
  that calls `unshare`).

The policy is compiled by `BuildSeccompPolicy` (seccomp.go:136) and
passed inline; it covers ~150 syscall denials in the default config.

## Resource limits

Per-function caps are enforced in two places:

- **Per-process**: `--rlimit_as max` (sandbox.go:158) caps virtual
  address space at the worker's per-cgroup memory budget.
- **Per-cgroup** (when cgroup v2 delegation is available, sandbox.go:170-194):
  - `memory.max` at **1.5×** the declared `memory_mb`. The 0.5×
    headroom lets the kernel reclaim via PSI pressure before OOM-killing.
    The 1.5× factor matches the autoscaler's per-worker admission budget.
  - `pids.max` at the configured `MaxPids` (default 64).
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
that NATs out via the host. **Host network interfaces are still not
exposed**; the function can dial outbound but can't see (or be seen by)
other tenants on the same node. Use this for handlers that talk to
Stripe, OpenAI, your DB, or any external API.

Switching the toggle drains the warm pool so the next invocation
respawns with the new mode within seconds. The Functions list in the UI
shows an "egress" badge on rows that have it on, so operators can audit
at a glance which functions can talk to the network.

Future modes (`egress+allowlist` / `private`) would extend the same
field without another schema migration.

### Egress blocklist (firewall)

When a function is in `egress` mode, a global blocklist applies on top:
specific IPs/CIDRs/hostnames that no function can reach regardless of
the per-function toggle. The list is managed entirely from the UI at
`/web/firewall` (or via `POST /api/v1/firewall/rules`) — there is no
config file. Rules live in the `egress_blocklist` SQLite table and
orvad re-applies them via nftables on every change + every 10 s tick.

**Coexisting with existing nftables rules.** orvad creates and owns
exactly one nftables table named `inet orva_firewall`. It does **not**
touch any other table on the host. If you already run nftables for
your own purposes (host firewall, fail2ban, k8s CNI), our rules sit
alongside yours — Linux's nftables evaluates every matching rule in
the OUTPUT chain regardless of which table they're in.

```bash
sudo nft list ruleset
# Look for:
#   table inet orva_firewall { ... }
# next to whatever else you're running.
```

orvad will rebuild only its own table on each refresh; your tables are
untouched. The bare-metal install script (`scripts/install.sh`) detects
existing nftables config at install time and prints an info line so
you know they coexist.

**When nftables is unavailable.** If `nft` isn't installed or the
kernel can't load `nf_tables` (rare modern distros, OpenWrt, BSD), the
install script logs a warning and the firewall feature degrades:

- Per-function `network_mode: egress` still works — functions still get
  an isolated net namespace + nstun userspace networking.
- The Firewall page still loads; you can manage rules in the DB.
- The page shows an amber **"nftables unavailable"** banner.
- **No packets are filtered** — host-level iptables / cloud security
  group / VPC firewall is your fallback.

This is intentional: orva will run regardless of host firewall state,
and the operator gets a clear yes/no signal at install time.

## What Orva itself runs as

The **orvad process inside the orva container** runs as UID 0 inside
the container. This is intentional and required: nsjail needs
`CAP_SYS_ADMIN` to set up the user namespace, mount namespace, and
cgroup hierarchy that isolate the function. Without those caps, the
sandbox can't be constructed and there's no isolation at all.

The container itself is launched with:

```
--cap-add SYS_ADMIN
--security-opt seccomp=unconfined
--security-opt apparmor=unconfined
--security-opt systempaths=unconfined
```

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
flags at sandbox spawn time (sandbox.go:204-209). Each sandbox sees
only its own function's secrets in its environment; the secret
material never leaves orvad and is never readable from any sandbox
filesystem.

> "Can a function read another function's code?"

No. Each function's code lives at
`<dataDir>/functions/<id>/versions/<hash>/`. Only the active version's
directory is bind-mounted into the sandbox at `/code`, and the bind is
read-only. There is no shared `/functions/` mount.

> "What happens if a function tries to fork-bomb?"

cgroup `pids.max` (default 64) caps the process tree. Spawning past
that limit fails with `EAGAIN` inside the sandbox. The orvad scheduler
also tracks per-pool memory reservations and refuses to admit new
workers when host memory budget is exhausted (see
`internal/pool/hostmem.go`).

## Reading further in the code

- `internal/sandbox/sandbox.go` — nsjail invocation
- `internal/sandbox/seccomp.go` — syscall policy
- `internal/sandbox/limiter.go` — host-wide concurrency cap
- `internal/pool/hostmem.go` — memory admission control
- `internal/proxy/proxy.go` — request → sandbox bridge

If you find a security issue please open a private security advisory
on the GitHub repository (Settings → Security → Advisories) rather
than a public issue.
