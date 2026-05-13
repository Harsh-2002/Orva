# Orva under gVisor (`runsc`)

**Short answer:** Orva does **not** work under gVisor. The daemon starts, the
HTTP API is reachable, but every function invocation fails with
`WORKER_CRASHED`. Two independent platforms (`ptrace` and `kvm`) reproduce
the same failure on the same kernel call.

This is a fundamental incompatibility between the way Orva sandboxes each
function (nsjail) and the way gVisor sandboxes a container (`runsc` — a
user-space kernel that selectively re-implements Linux syscalls).

---

## What we tested

| | |
|---|---|
| Host | Ubuntu 24.04, kernel 6.8.0-111-generic, x86_64, 2 CPU, 11.68 GiB |
| Docker | 29.4.1, default runtime `runc`, cgroup v2 |
| gVisor | `release-20260504.0` |
| Orva image | `ghcr.io/harsh-2002/orva:v2026.05.12` |
| Date | 2026-05-13 |

Runtimes registered with the Docker daemon:

```json
{
  "runtimes": {
    "runsc":     { "path": "/usr/bin/runsc" },
    "runsc-kvm": { "path": "/usr/bin/runsc", "runtimeArgs": ["--platform=kvm"] }
  }
}
```

The host has `/dev/kvm` readable + writable, so both platforms were
exercisable. Default `runsc` uses the `ptrace` platform; `runsc-kvm`
uses gVisor's KVM-backed platform.

For each platform we ran:

1. `docker run --runtime=<runtime> ... ghcr.io/harsh-2002/orva:v2026.05.12`
2. Waited for `/api/v1/system/health` to return 200.
3. Onboarded an admin user, minted an API key.
4. Created a Node 24 function and deployed an inline `exports.handler`.
5. `POST /fn/<id>/` and inspected the response.

---

## What works

- The Orva container **starts** under both `runsc` and `runsc-kvm`.
- The Go HTTP server, SQLite store, MCP server, OAuth server, dashboard,
  and all REST endpoints are reachable.
- Function **deployments succeed** — the build pipeline runs to completion
  and the function row reaches `status=active`.
- Everything that doesn't actually execute user code in nsjail works fine.

The Docker entrypoint logs one informational warning that doesn't gate
anything:

```
>> WARN: cannot create cgroup at /sys/fs/cgroup/orva-sandboxes — nsjail CPU/memory limits disabled
   (requires cgroup: host and - /sys/fs/cgroup:/sys/fs/cgroup:rw)
```

This is the entrypoint's existing cgroup-delegate probe failing because
gVisor's cgroup view is a virtual one. nsjail would fall back to host-wide
rlimits — not the failure mode that breaks invocation.

---

## What doesn't work

Function invocation. Every call to `POST /fn/<id>/` returns:

```json
{"error":{"code":"WORKER_CRASHED","message":"function worker exited unexpectedly"}}
```

Manually running nsjail inside the gVisor-wrapped container with the same
flags the daemon would use shows the exact cause:

```
$ docker exec orva-under-runsc \
    /usr/local/bin/nsjail -v -Mo --chroot /var/lib/orva/rootfs/node24 \
                          -T /tmp -- /usr/local/bin/node --version

[D] runChild():535 Creating new process with clone flags:
    CLONE_NEWNS|CLONE_NEWCGROUP|CLONE_NEWUTS|CLONE_NEWIPC|
    CLONE_NEWUSER|CLONE_NEWPID|CLONE_NEWNET
[W] runChild():556 clone(flags=…) failed: Invalid argument
[E] standaloneMode():299 Couldn't launch the child process
$ echo $?
255
```

`clone(2)` returns `EINVAL` ("Invalid argument") on the seven-namespace
clone nsjail needs to construct its sandbox. The same `nsjail --version`
runs fine under default `runc` on the same host with the same image.

Both `--platform=ptrace` and `--platform=kvm` fail at the same call with
the same errno. So this isn't a platform-specific quirk — it's gVisor's
sentry refusing the nested-namespace request.

---

## Why this happens (root cause)

gVisor is a **user-space kernel**: the `runsc` sentry intercepts every
syscall a sandboxed process makes and re-implements it. By design, gVisor
does not expose host kernel primitives to the container — instead it
emulates a subset of Linux that it can prove safe.

Some primitives are intentionally out of scope, and nested namespace
creation is one of them. The gVisor documentation calls this out:

> gVisor only supports a small subset of namespaces and may not support
> further nesting.

nsjail's design is the opposite. It assumes a fully-featured Linux kernel
and uses `clone(CLONE_NEW*)` extensively to build per-function isolation:
new mount, user, PID, network, IPC, UTS, and cgroup namespaces are
created on every invocation, and the function code runs inside that
freshly-built namespace world.

Put the two together and you get a clean architectural mismatch:

- gVisor doesn't implement enough of `clone(CLONE_NEW…)`.
- nsjail can't run without it.
- No flag on either side resolves it.

This is not a bug in Orva, nsjail, or gVisor — it's a documented
limitation of the runsc design surfacing in a workload that needs the
opposite of what gVisor offers.

---

## What to do instead

If your threat model genuinely needs more isolation than nsjail on bare
Linux provides, the alternatives that play well with nsjail's namespace
needs are:

- **A VM boundary.** Firecracker microVMs (`/dev/kvm`) wrap the whole
  Orva instance with a hypervisor — nsjail keeps its full namespace API
  inside the guest kernel. The cost is a full guest kernel per Orva
  instance (~5-20 MB RAM overhead).
- **Stricter nsjail config.** Most of nsjail's defenses are already
  active in Orva (network namespace, seccomp policy, read-only chroot,
  cgroup v2 limits). For higher assurance, tighten the seccomp policy
  further (`backend/internal/sandbox/seccomp.go`) or bind-mount the
  rootfs read-only.
- **Per-tenant Orva instances.** Run a separate Orva container per
  trust zone instead of multi-tenanting one Orva instance. The
  container boundary plus nsjail-per-function gives two independent
  layers without needing gVisor or a hypervisor.

Running Orva itself under gVisor will not help and will instead break
the inner sandbox entirely.

---

## Reproduce

Anyone with gVisor installed and registered as a Docker runtime can
reproduce this in ~3 minutes. The check is intentionally a hand-run
docker invocation rather than a checked-in test script — there's no
point automating a verification of a known architectural mismatch.

```bash
# 1. Confirm runsc is registered:
docker info --format '{{json .Runtimes}}' | grep -q runsc || \
    echo "install gVisor first: https://gvisor.dev/docs/user_guide/install/"

# 2. Bring up Orva under runsc:
docker run -d --name orva-gvisor-test \
    --runtime=runsc \
    -p 28443:8443 \
    --cap-add SYS_ADMIN \
    --security-opt seccomp=unconfined \
    --security-opt apparmor=unconfined \
    --security-opt systempaths=unconfined \
    -v orva-gvisor-data:/var/lib/orva \
    ghcr.io/harsh-2002/orva:latest

# 3. Wait for the API:
until curl -fsS http://localhost:28443/api/v1/system/health >/dev/null; do sleep 1; done

# 4. Reproduce the clone() rejection directly with nsjail:
docker exec orva-gvisor-test \
    /usr/local/bin/nsjail -v -Mo \
        --chroot /var/lib/orva/rootfs/node24 \
        -T /tmp -- /usr/local/bin/node --version 2>&1 | tail -5
# Expected:
# clone(flags=CLONE_NEWNS|CLONE_NEWCGROUP|CLONE_NEWUTS|CLONE_NEWIPC|
#             CLONE_NEWUSER|CLONE_NEWPID|CLONE_NEWNET) failed: Invalid argument
# Couldn't launch the child process
# main():404 Returning with 255

# 5. Cleanup:
docker rm -f orva-gvisor-test && docker volume rm orva-gvisor-data
```

To re-test if gVisor publishes nested-namespace support in a future
release, this same invocation is the canonical check. There's nothing
Orva-specific about the failure — any tool that needs
`clone(CLONE_NEWUSER|CLONE_NEW…)` will see the same `EINVAL`.

---

## Status

- README's "supported configuration" claim has been removed
  (2026-05-13). The README now points here instead.
- `docs/SECURITY.md` cross-references this document under "layered
  isolation: what does and doesn't compose."
- The previous CI harness (`test/install/gvisor-flow.sh`,
  `.github/workflows/install-e2e.yml`'s `gvisor` job, gated on
  `HAS_GVISOR`) was removed alongside this writeup. There's no value
  in burning CI minutes re-verifying an architectural incompatibility
  on every release. If gVisor's behavior changes, follow the
  "Reproduce" section above by hand.
