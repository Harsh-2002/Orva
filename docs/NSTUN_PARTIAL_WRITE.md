# NSTUN TCP partial-write incident

## Root cause

The affected dependency is Google nsjail revision
`5ebcc30bef4af60d6e28f012dd8bf7b99b8b0acf`, specifically
[`nstun/tcp.cc::flush_to_host()`](https://github.com/google/nsjail/blob/5ebcc30bef4af60d6e28f012dd8bf7b99b8b0acf/nstun/tcp.cc#L295).
Its positive-write branch advances the receive-buffer offset but does not
register `EPOLLOUT` when bytes remain. Its `EAGAIN` branch does register it.
If there is no additional guest traffic, the residual bytes have no writable
wakeup and can stall until the application's timeout.

The supplied production trace requested 57,351 bytes, forwarded 49,232 bytes,
and stranded 8,119. Cairn logged an incomplete client body, Caddy an unexpected
EOF, and quick-share's S3 client a `TimeoutError`. The same installed SDK and
credentials passed a 56,288-byte PUT outside the sandbox (~66 ms), followed by
byte-identical GET and successful DELETE. A 1 KiB sandbox upload passed (~62 ms),
but the larger sandbox upload failed even with the public destination explicitly
selected while retaining HTTPS hostname verification. These are pre-fix evidence
supplied with the incident, not newly measured results. The exact trigger of the
first historical failure is unknown; short writes depend on timing and buffering.

## Correction and security

Orva retains the pinned revision and carries a reviewed patch in
`scripts/nsjail/nstun-partial-write.patch`. `scripts/patch-nsjail.sh` rejects a
different checkout revision or a patch that no longer applies. Every build path
applies it: Docker, source E2E CI, static installer candidates, and static Release
assets. `scripts/test-nsjail.sh` gates those builds before publication.

Forwarding performs at most one successful send per call, retains the exact
unsent offset, and registers writable interest whenever data remains, including
after a positive short write or `EAGAIN`. Writable interest is removed only when
the queue drains. Host-read backpressure stays paused when previously disabled.
Interrupted sends retry up to 16 times per dispatch, then yield to epoll; terminal
send/epoll errors reset and destroy the flow. A guest FIN defers host write-side
shutdown until pending bytes drain. Existing 8 MiB RX and TX hard caps and TX
pause/resume thresholds remain unchanged. No function code, DNS, credentials,
firewall rules, isolation flags, or permission boundaries are relaxed.

## Deterministic regression

Build nsjail with its normal prerequisites, apply the patch before compilation,
then run:

```bash
bash scripts/patch-nsjail.sh /path/to/nsjail-source
make -C /path/to/nsjail-source -j2
bash scripts/test-nsjail.sh /path/to/nsjail-source
```

The harness compiles the actual dependency's `tcp.cc` with its production
supporting objects; it does not duplicate the forwarding algorithm. Linker
wrappers limit successful sends or inject syscall results while real socketpairs
and epoll deliver bytes. It forces the exact positive 49,232/57,351 short write,
then relies on an epoll wakeup without additional guest input. Cases cover
paused reads, repeated short writes, appended data, `EAGAIN`/`EWOULDBLOCK`,
bounded `EINTR` retries, zero-progress and terminal sends, peer disconnect,
epoll-registration failure, the unchanged RX cap, and guest FIN with pending data.
Byte-for-byte comparisons detect truncation and duplication; bounded event loops
and a 30-second outer timeout fail stalls. The unpatched source must fail the
first short-write interest assertion.

The real TLS test is `sudo python3 test/nstun-https.py --scratch` on a disposable
provisioned VM. It starts an ephemeral HTTPS sink, trusts only its generated
certificate with hostname verification, requires TLS 1.2 or newer on client and
server, and launches the client in a real NSTUN
sandbox. It performs five sequential plus fifteen eight-client-concurrent
PUT/GET/DELETE round trips across 1 KiB, 56,288 bytes, 256 KiB, 1 MiB, and 4 MiB.
The receiver reads slowly to exercise backpressure. `--disable-userns` selects
the existing root/file-capability-compatible mode on hosts that restrict user
namespaces; all other namespaces remain enabled. This fixture uses a disposable
VM's own networking, not production DNS or egress-policy edits. CI runs it in
the source E2E job on both supported Linux architectures.

## End-to-end acceptance and deployment

Validation must include real HTTPS PUT → GET byte-for-byte equality → DELETE at
1 KiB, 56,288 bytes, several larger sizes, and quick-share's configured 4 MiB
maximum, repeated concurrently. Exercise the unchanged quick-share API and its
browser file chooser, and retrieve each returned short link. Delete only the
test-created objects and matching share/cleanup keys. Never log credentials.

Release follows `CONTRACT.md`: reviewed PR, green merge-commit verification,
dated tag, successful Release and released-artifact verification, then prune the
previous release and tag. Upgrade bare metal using the checksum-verified release
installer. Replacing only the Go binary is insufficient: nsjail must be refreshed
and the service restarted so old warm workers no longer run the old dependency.
Compare installed nsjail's SHA-256 against the new release asset, verify build
identity and service health, then repeat uploads through fresh sandbox workers.

Local validation on 2026-10-08:

| Check | Result |
|---|---|
| Unpatched real `tcp.cc`, forced 49,232/57,351 write | Failed the missing `EPOLLOUT` assertion, reproducing the defect |
| Patched Docker dependency and static installer dependency | All deterministic regression cases passed; static linkage verified |
| Real NSTUN sandbox HTTPS, sequential and eight-client concurrent | 20/20 PUT → identical GET → DELETE, all five sizes through 4 MiB |
| Disposable KVM VM, 1 vCPU / 512 MiB, verified TLS | The same 20/20 round trips passed with user, network, mount, PID, IPC and UTS namespaces enabled |
| `make lint`, `make test`, server build, workflow/shell lint | Passed |
| E2E harness unit tests | 21 passed |
| Fresh scratch Orva with the current server and corrected static nsjail | Full API/CLI/sandbox suite passed with no skipped sandbox gate |

The first local full-suite attempt had a misaddressed host mock provider and an
unsupported nested-user-namespace mount fixture. Rerunning the disposable
container with its host gateway and the same namespace mode used by source CI
passed. These fixture failures were not bypassed by weakening production.
Production upgrade and application checks are a separate release acceptance
step; local results alone are not evidence that production has been corrected.
Smolvm 1.16.2 could not reach guest-agent readiness on this host, including a
minimal configuration; a disposable QEMU/KVM VM was used for the required
pre-push isolated validation instead. These correctness checks are not a
throughput benchmark or a capacity guarantee.

## Rollback

Do not loosen sandbox security or change quick-share as a rollback. If a new
release regresses, stop new test traffic, keep application data/users/OAuth
sessions intact, and publish a reviewed revert through the same CI/release gates.
The previous release/tag is pruned under the one-active-release policy, so do not
assume its download URL remains available. If a previously verified release asset
is already retained by an operator, reinstall its matching server, nsjail, and
runtime assets using verified checksums and restart Orva; this reintroduces the
known short-write defect. Otherwise rebuild/publish from the recorded prior
commit via the normal policy. Confirm nsjail capabilities, service health, and
fresh worker execution after rollback. Never restore an older database merely to
roll back a dependency binary.
