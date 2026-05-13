# Supported platforms

Orva runs on any Linux distro with kernel 5.10+ that ships unprivileged
user namespaces and cgroup v2. The 6 distros below are the ones the
`install-e2e` workflow (`test/install/`) exercises end-to-end on every
push affecting `scripts/install.sh`.

End-to-end means the workflow does, per distro:

1. Spin up a privileged container with the distro's init system (systemd
   on most, OpenRC on Alpine).
2. Run `scripts/install.sh` for real — download binaries, create the
   service user, install the unit file.
3. Start the service and wait for the health endpoint.
4. Run the API + CLI smoke flow against the live daemon.
5. Verify uninstall preserves data; reinstall recovers state.
6. Verify `uninstall.sh --purge` wipes the data dir and service user.

## Distro matrix

| Distro | Init | Package manager | Status |
|---|---|---|---|
| Ubuntu 24.04 | systemd | apt | tested in CI |
| Debian 12 | systemd | apt | tested in CI |
| Alpine 3.21 | OpenRC | apk | tested in CI |
| Rocky Linux 9 | systemd | dnf | tested in CI |
| Fedora 41 | systemd | dnf | tested in CI |
| Arch Linux | systemd | pacman | tested in CI (rolling — flake-prone) |

Other distros covered by `install.sh`'s detection logic but not
exercised in CI: CentOS Stream, AlmaLinux, Amazon Linux, openSUSE
Leap/Tumbleweed, SLES, Manjaro, EndeavourOS. These hit the same code
paths as their tested cousins (Rocky for RHEL family, Arch for Manjaro,
etc.), so they should work — but if you run into trouble, please file
an issue.

## Kernel feature requirements

The install script warns (but does not block) when these are missing.
Without them, nsjail's isolation degrades:

- `kernel.unprivileged_userns_clone = 1` — required for nsjail to
  construct per-function user namespaces.
- cgroup v2 — required for per-function memory / CPU limits.
- `nf_tables` kernel module — required for the egress firewall feature.
  Without it, the daemon runs fine; the firewall UI shows "degraded".

## gVisor (runsc) compatibility

**Not supported.** End-to-end testing on 2026-05-13 (gVisor
`release-20260504.0`, both `ptrace` and `kvm` platforms) confirmed
that Orva's daemon starts under runsc but function invocation fails
with `WORKER_CRASHED`. nsjail's per-function sandbox setup needs
nested-namespace `clone(CLONE_NEW…)` which gVisor's user-space kernel
rejects with `EINVAL`. This is architectural, not a bug.

Full reproduction + alternatives: [`docs/GVISOR.md`](GVISOR.md).

The `gvisor` CI leg (`HAS_GVISOR` repo variable) and the
`test/install/gvisor-flow.sh` script still exist so we can re-verify
on every gVisor release. Flip `HAS_GVISOR=true` once gVisor publishes
nested-namespace support and this verdict gets a fresh data point.

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

CI: `.github/workflows/cli-e2e.yml` runs the full install on Ubuntu 24,
macOS 14, and Windows 2022 every push and weekly.

## Running the matrix locally

```bash
# Single distro
bash test/install/run-distro.sh ubuntu24

# Full matrix (sequential — ~35 min)
for d in ubuntu24 debian12 alpine321 rocky9 fedora41 arch; do
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

### Install lifecycle gaps fixed in 2026-05

The end-to-end pass on Ubuntu 24 surfaced three production bugs in
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
