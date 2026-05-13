# Orva under Kata Containers (`runtime=kata`)

**Short answer:** Orva runs end-to-end under Kata Containers. Both
hypervisors tested — QEMU (default) and Cloud Hypervisor — pass the
functional gate including outbound HTTPS from the function sandbox. This
is the answer the README has been waiting for since the gVisor pass:
nsjail composes cleanly with a runtime that exposes a real guest kernel,
which is exactly what Kata does.

This document captures the verified configuration, the measured
performance cost, and when an operator should reach for it.

---

## What we tested

| | |
|---|---|
| Host | Ubuntu 24.04, kernel 6.8.0-111-generic, x86_64, 2 CPU, 11.68 GiB |
| Docker | 29.4.1, default runtime `runc`, cgroup v2 |
| Kata | 3.30.0 (containerd-shim-kata-v2 at `/opt/kata/bin/`) |
| Hypervisors | QEMU (default config) + Cloud Hypervisor (`configuration-clh.toml`) |
| Orva image | `ghcr.io/harsh-2002/orva:v2026.05.12` |
| Date | 2026-05-13 |

Runtimes registered in `/etc/docker/daemon.json`:

```json
{
  "runtimes": {
    "kata":     { "runtimeType": "/opt/kata/bin/containerd-shim-kata-v2" },
    "kata-clh": {
      "runtimeType": "/opt/kata/bin/containerd-shim-kata-v2",
      "options": {
        "ConfigPath": "/opt/kata/share/defaults/kata-containers/configuration-clh.toml"
      }
    }
  }
}
```

Apply with `sudo systemctl reload docker` — no restart needed, existing
containers keep running.

---

## What works

For each of `--runtime=kata` and `--runtime=kata-clh`:

- **Container starts cleanly.** Daemon healthy in well under the
  default 120 s window.
- **Deploy + invoke** — happy path, identical to runc behavior.
- **Egress (`network_mode: "egress"`)** — function fetches
  `https://example.com` and returns the status. nsjail's `--user_net`
  (AF_PACKET + `/dev/net/tun`) works under both hypervisors. This was
  the highest-risk leg; especially noteworthy that **Cloud Hypervisor's
  minimal device set still includes what nsjail needs**.
- **Secrets injection** — `process.env.FOO` reads the right value from
  inside the sandbox.

Why this works (in one sentence): Kata puts a real Linux kernel under
the entire orvad container, so nsjail's seven-namespace `clone()` —
which gVisor rejected with `EINVAL` — works unchanged inside the
guest. There's no syscall translation layer to trip over.

---

## Performance cost

Measured 2026-05-13 on Ubuntu 24.04 / kernel 6.8 / 2 CPU / 11.68 GiB.
Workload: Node 24 noop handler (`async () => ({statusCode:200,body:"ok"})`).
Each runtime measured sequentially in isolation against the same
reference function. To reproduce on your host, run `bash
test/kata-bench/run.sh` — it writes per-runtime CSVs under
`test/kata-bench/<runtime>/` and a `summary.md` (both gitignored).

| Metric                          | runc    | kata (QEMU)     | kata-clh (Cloud Hypervisor) |
|---|---|---|---|
| Container-start latency         | 4.4 s   | 77.8 s          | 63.5 s                      |
| Cold-start median (10 probes)   | 22 ms   | 36 ms  (+64 %)  | 40 ms  (+82 %)              |
| Throughput @ c=25               | 598 rps | 143 rps (−76 %) | 147 rps (−75 %)             |
| Throughput @ c=100              | 577 rps | 123 rps (−79 %) | 117 rps (−80 %)             |
| Throughput @ c=200              | 602 rps | 109 rps (−82 %) | 110 rps (−82 %)             |
| Functional (baseline / egress / secrets) | ✓ / ✓ / ✓ | ✓ / ✓ / ✓ | ✓ / ✓ / ✓ |

What this is telling you, in plain terms:

- **Container-start is the biggest surprise.** It's not the QEMU/CLH
  boot itself — the dominant cost is the orvad image's rootfs being
  seeded through virtio I/O on first boot inside the guest (~2.4 GiB
  of files, single-CPU virtio-blk). QEMU pays ~78 s of this; CLH pays
  ~64 s. This is a one-time cost per `docker run`, not per invocation,
  and most of it is image-size driven — a leaner Orva image would
  shrink it for both hypervisors. For long-lived deployments it's noise.
- **Cold-start (per-function first-invoke) doubles vs runc.** Inside
  the already-warm guest, nsjail spawn + Node boot runs through the
  guest kernel's scheduler + virtio fs, which roughly doubles the
  ~22 ms host-native baseline to 36–40 ms. Both Kata hypervisors land
  in the same band.
- **Steady-state throughput drops to ~20–25 % of runc.** This is the
  honest number for "what's it cost to run nsjail under a hypervisor
  on a 2-CPU host." It's a chunkier tax than typical microVM
  benchmarks suggest, because Orva's per-invocation path crosses the
  guest virtio boundary twice: once for the inbound HTTP and once for
  every syscall nsjail makes setting up the sandbox. Bigger hosts
  with more vCPU per Kata guest will shrink the gap.
- **kata-clh and kata-qemu are statistically tied on throughput.**
  CLH's only measurable win is ~14 s faster container-start. Pick
  CLH for the smaller hypervisor TCB (~40 k LoC of Rust vs ~1.5 M LoC
  of C); the runtime perf difference under Orva's workload is in the
  noise.

---

## Recommended configuration

| Workload shape | Runtime |
|---|---|
| Homelab / internal tools / trusted code | `runc` (default). No reason to pay the Kata tax. |
| Untrusted third-party code, multi-tenant | `--runtime=kata-clh` (Cloud Hypervisor). Recommended default for the Kata path: ~14 s faster container-start than QEMU, identical steady-state throughput, smaller hypervisor TCB (Rust). |
| Fall back if CLH hits a device-set surprise | `--runtime=kata` (QEMU). Same numbers within noise, broader device support, longer track record. |

Kata stays opt-in via the explicit `--runtime=` flag. We don't change
Docker's default runtime, and the Orva installer doesn't touch
`/etc/docker/daemon.json`. If you want Kata, you opt in.

### How to enable

```bash
# 1. Install Kata (per https://github.com/kata-containers/kata-containers/blob/main/docs/install/README.md).
#    Confirm with:
kata-runtime kata-check
# Expect: "System is capable of running Kata Containers"

# 2. Register both Kata runtimes in /etc/docker/daemon.json
#    (existing runtimes block kept; add the two "kata*" entries).
#    kata-clh is the recommended default; "kata" is kept as a fallback.
{
  "runtimes": {
    "kata":     { "runtimeType": "/opt/kata/bin/containerd-shim-kata-v2" },
    "kata-clh": {
      "runtimeType": "/opt/kata/bin/containerd-shim-kata-v2",
      "options": {
        "ConfigPath": "/opt/kata/share/defaults/kata-containers/configuration-clh.toml"
      }
    }
  }
}
sudo systemctl reload docker

# 3. Run Orva under it. SAME flags as the standard one-liner, plus --runtime=kata-clh.
docker run -d --name orva \
  --runtime=kata-clh \
  -p 8443:8443 \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  -v orva-data:/var/lib/orva \
  ghcr.io/harsh-2002/orva:latest
```

Swap `--runtime=kata-clh` for `--runtime=kata` to use the QEMU
hypervisor instead. Both can coexist; you pick per `docker run`.

### Comparison to gVisor

gVisor was the previous answer to "more isolation around Orva." It
doesn't work — see [`docs/GVISOR.md`](GVISOR.md) for the architectural
mismatch. The short version: gVisor's user-space kernel refuses
nsjail's nested-namespace `clone()`. Kata sidesteps the issue entirely
by putting a real Linux kernel under the orvad container, where
nsjail's namespace API works exactly like on bare metal.

---

## Verify on your own host

```bash
# Confirm runtime is registered.
docker info --format '{{json .Runtimes}}' | jq 'keys' | grep -q kata

# Functional smoke (3-stage gate: health → deploy → invoke).
RUNTIME=kata     bash test/install/kata-flow.sh ghcr.io/harsh-2002/orva:v2026.05.12
RUNTIME=kata-clh bash test/install/kata-flow.sh ghcr.io/harsh-2002/orva:v2026.05.12
cat test/install/logs/kata-*-result.txt

# Extended functional pass (egress, secrets) per runtime.
RUNTIME=kata     bash test/kata-bench/extended-functional.sh http://localhost:28443 kata
RUNTIME=kata-clh bash test/kata-bench/extended-functional.sh http://localhost:38443 kata-clh

# Re-run the full benchmark (~85 min wall time on a 2-CPU host).
bash test/kata-bench/run.sh
cat test/kata-bench/summary.md
```

No CI gate. Per-release re-verification is a hand-run of the scripts
above; document tracks the most recent verified Kata version + Orva
image.
