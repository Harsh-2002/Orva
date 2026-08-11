# Supported platforms

Orva runs on any Linux distro with kernel 5.10+ that ships unprivileged
user namespaces and cgroup v2. The `install-matrix` job in the consolidated
`CI` workflow (`.github/workflows/ci.yml`, harness at `test/install/`)
exercises the 6 distros below end-to-end on every pull request affecting
`scripts/install.sh`, on every `main` push, and against every published release.

End-to-end means the job does, per distro:

1. Spin up a privileged container with the distro's init system (systemd
   on most, OpenRC on Alpine).
2. Run `scripts/install.sh` for real — download binaries, create the
   service user, install the unit file.
3. Start the service and wait for the health endpoint.
4. Run the API + CLI smoke flow against the live daemon.
5. Verify uninstall preserves data; reinstall recovers state.
6. Verify the generated uninstaller (`$PREFIX/share/orva/scripts/uninstall.sh --purge`) wipes the data dir and service user.

## Distro matrix

| Distro | Init | Package manager | Status |
|---|---|---|---|
| Ubuntu 24.04 | systemd | apt | tested in CI |
| Debian 12 | systemd | apt | tested in CI |
| Alpine 3.21 | OpenRC | apk | tested in CI |
| Rocky Linux 9 | systemd | dnf | tested in CI |
| Fedora 44 | systemd | dnf | tested in CI |
| Arch Linux | systemd | pacman | tested in CI (rolling — flake-prone) |

Other distros covered by `install.sh`'s detection logic but not
exercised in CI: CentOS Stream, AlmaLinux, Amazon Linux, openSUSE
Leap/Tumbleweed, SLES, Manjaro, EndeavourOS. These hit the same code
paths as their tested cousins (Rocky for RHEL family, Arch for Manjaro,
etc.), so they should work — but if you run into trouble, please file
an issue.

## Kernel feature requirements

None of these block installation — the installer warns where it can probe,
and otherwise the feature simply stops working at invocation time:

- `kernel.unprivileged_userns_clone = 1` — preferred for nsjail's
  per-function user namespaces. On bare-metal hosts that disable or restrict
  unprivileged user namespaces, `install.sh` applies a verified, narrow file
  capability set to nsjail and configures `ORVA_DISABLE_USERNS=1`; the runtime
  still uses mount, PID, network, IPC, UTS, chroot, and seccomp isolation.
- cgroup v2 — required for per-function memory / CPU limits.
- `/dev/net/tun` (the `tun` kernel module) — required by nsjail's
  `--user_net`, i.e. by every function with `network_mode: egress`, and
  therefore by the egress policy that filters those functions. Without the
  device they fail to spawn; `network_mode: none` functions are unaffected.
  Load it with `modprobe tun`; in Docker pass `--device /dev/net/tun` (the
  shipped compose file and `install.sh`'s compose output already do).
  **No `nftables` / `nf_tables` is needed.** The egress policy is compiled
  into each sandbox's own network namespace by nsjail and never touches host
  firewall state — see [`SECURITY.md`](SECURITY.md#sandbox-egress-policy).

## gVisor (runsc) compatibility

**Not supported.** End-to-end testing on 2026-05-13 (gVisor
`release-20260504.0`, both `ptrace` and `kvm` platforms) confirmed
that Orva's daemon starts under runsc but function invocation fails
with `WORKER_CRASHED`. nsjail's per-function sandbox setup needs
nested-namespace `clone(CLONE_NEW…)` which gVisor's user-space kernel
rejects with `EINVAL`. This is architectural, not a bug — gVisor
intentionally doesn't expose nested-namespace primitives.

Full reproduction + alternatives: [`docs/GVISOR.md`](GVISOR.md).

## Architecture support

- **amd64** — first-class. Released binaries built for amd64.
- **arm64** — first-class. Released binaries built for arm64. Works on
  Raspberry Pi 4/5 (with a 64-bit OS) and on Apple Silicon via Docker
  Desktop.

32-bit architectures are not supported.

## Standalone CLI matrix (different from the server)

The slim `orva` CLI ships separately for cross-platform installs that
don't need the daemon. See [`docs/CLI.md`](CLI.md) for install one-liners
and `orva upgrade` details.

| OS | amd64 | arm64 | Asset name | Install path |
|---|---|---|---|---|
| Linux | ✓ | ✓ | `orva-cli-linux-{amd64,arm64}` | `/usr/local/bin/orva` |
| macOS | ✓ | ✓ | `orva-cli-darwin-{amd64,arm64}` | `/usr/local/bin/orva` |
| Windows | ✓ | ✓ | `orva-cli-windows-{amd64,arm64}.exe` | `%LocalAppData%\Programs\orva\orva.exe` |

CI: `.github/workflows/ci.yml` runs every released CLI asset natively on
Ubuntu 24.04, macOS 26, and Windows 2025/11 Arm (amd64 + arm64) after every
release and weekly.

## Running the matrix locally

```bash
# Single distro
bash test/install/run-distro.sh ubuntu24

# Full matrix (sequential — ~35 min)
for d in ubuntu24 debian12 alpine321 rocky9 fedora44 arch; do
  bash test/install/run-distro.sh "$d"
done
```

Requires Docker on a Linux host with `--privileged` allowed and
`/sys/fs/cgroup` mountable. Logs land in `test/install/logs/`.

### Nested-container limitation

The harness uses systemd-in-docker to simulate a bare-metal install.
That covers `install.sh` itself end-to-end (package install, service
user, systemd unit, adapter setup), but the inner nsjail sandbox can
hit a kernel-level restriction in nested containers: `mount("/", "/",
MS_PRIVATE)` returns `EACCES` when called from a non-root user even
with ambient `CAP_SYS_ADMIN`, because the outer Docker container's
mount namespace blocks downgrades.

In practice this surfaces as `WORKER_CRASHED` on every function
invocation under the harness. The smoke flow detects this signature
and reports it as a warning, not a failure — it does NOT reproduce on
real bare-metal Linux or on a fresh VM. For full end-to-end invocation
coverage, run install.sh on an actual VM (Vagrant, QEMU, Hetzner,
Lima, Multipass, etc.).

### Install lifecycle gaps fixed

End-to-end passes on Ubuntu 24 and ARM64 bare metal surfaced several bugs in
`install.sh` that have since been fixed:

1. `nsjail` was installed at `/opt/orva/bin/nsjail`, but the daemon's
   default `NsjailBin` is `/usr/local/bin/nsjail` (matches the Docker
   image). The installer now puts a copy at both paths.
2. `nsjail` was documented as static, but is actually dynamically
   linked against `libprotobuf` and `libnl-route-3` / `libnl-3`. The
   installer now resolves and installs the right runtime libraries
   per-distro (e.g. `libprotobuf32t64` on Ubuntu 24, `libprotobuf32`
   on Debian 12, `protobuf` on Fedora/Alpine/Arch).
3. The language adapters (`adapter.js` / `adapter.py`) were never
   written into the downloaded rootfs trees, so every invocation
   crashed with `read frame: EOF`. The installer now runs
   `orva setup --skip-nsjail` after the rootfs download to populate
   them.
4. The systemd unit lacked `Delegate=yes`, so `nsjail`'s per-sandbox
   cgroup v2 setup couldn't create child cgroups. Added.
5. The systemd capability bounding set retained only `CAP_SYS_ADMIN`, while
   the installed nsjail binary also carries `CAP_SETUID`, `CAP_SETGID`,
   `CAP_NET_ADMIN`, and `CAP_NET_BIND_SERVICE`. Linux therefore rejected the
   nsjail `execve` with `EPERM`, producing an immediate `SANDBOX_ERROR` with
   no child stderr. The shipped unit now retains all nsjail file capabilities
   in its bounding set. `CAP_NET_ADMIN` is part of that set because **nsjail**
   needs it to create and configure the TUN interface for `--user_net`
   (`network_mode: egress`) — not because orvad programs a host firewall. It
   no longer does: the egress policy is compiled per sandbox and loaded by
   nsjail itself, so orvad requires no network-administration capability of
   its own. On hosts where AppArmor or container policy blocks unprivileged
   user namespaces, the installer also selects nsjail's setcap fallback
   automatically.
