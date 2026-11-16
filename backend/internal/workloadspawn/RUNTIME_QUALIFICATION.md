# Scratch-only packaged-runtime qualification

This is an **opt-in Linux/amd64 test harness and explicit scratch build-time
producer recipe**, not an installer, packaged-release change, arbitrary-tree
manifest API, or serving integration. The production verifier and spawn
implementation are unchanged.

## Producer and ownership boundary

The operator downloads rootfs packages and nsjail from the trusted Orva release
publisher, and obtains their SHA-256 values from its separately fetched HTTPS
`checksums.txt`. A checksum supplied by the package sender is not sufficient.
This establishes trust in that publisher/channel, not independent reproducible
build or signature verification. Package files, paths and ancestors must be
exclusively owned and stable throughout the test; do not point it at live data.

`runtime_package_linux_test.go` is a test-only producer. It authenticates each
bounded archive **before** decompression, then derives typed manifest
expectations from those authenticated archive headers and file bytes, never
from a walk of an arbitrary installed tree. It creates only fresh `New` stores
and `Begin` stages. Numeric ownership, modes, hardlinks and exact link text are
preserved. Root and parent directories must be explicit; parents must precede
children, hardlinks must reference earlier regular files. Conventional `./` and
directory trailing slashes are normalized only in archive member names.

Xattrs, unknown PAX metadata, special nodes, noncanonical paths, duplicate
entries, symlink parents and exceeded budgets fail with diagnostics. The raw
package qualification never changes package content. The separate reviewed
recipe below makes only explicit build-time omissions and source overlays; it
does not prune unknown dangling links, strip xattrs or adopt existing paths.
All producer writers and the decompressor finish before `Seal`. The unchanged
runtime profile decides symlink compatibility.

## Run inside a disposable VM

Use a fresh bounded Microsandbox (2 CPU / 4 GiB / 20 GiB), with no host mounts,
ports, credentials or private egress. Copy this checkout and a trusted Go 1.27
toolchain into it; install `zstd`, CA certificates, `curl` and (for race tests)
a C compiler **inside the guest**. The ordinary test can download Go modules in
the guest, after which focused checks can use `GOPROXY=off GOSUMDB=off`.

In `/packages`, obtain the publisher's current release:

- `rootfs-node-amd64.tar.zst`
- `rootfs-python-amd64.tar.zst`
- `nsjail-linux-amd64` (make executable after checksum verification)
- `checksums.txt`, fetched separately from the trusted release URL

Do not compute the expected package hashes from the downloaded package files.
Extract the exact corresponding entries from the trusted checksum file:

```sh
export ORVA_RUNTIME_PACKAGE_TEST=1 ORVA_RUNTIME_PACKAGE_DIR=/packages
export ORVA_RUNTIME_NODE_SHA256="$(awk '$2 == "rootfs-node-amd64.tar.zst" {print $1}' /packages/checksums.txt)"
export ORVA_RUNTIME_PYTHON_SHA256="$(awk '$2 == "rootfs-python-amd64.tar.zst" {print $1}' /packages/checksums.txt)"
export ORVA_RUNTIME_NSJAIL_SHA256="$(awk '$2 == "nsjail-linux-amd64" {print $1}' /packages/checksums.txt)"
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -count=1 -v -run '^TestPackagedRuntimeQualification$' ./backend/internal/workloadspawn
```

With opt-in absent, the qualification test explicitly skips. With opt-in set,
missing prerequisites, mismatched hashes, strict-profile rejection and nsjail
failures **fail**, never skip. It starts no Orva server or operator API.

On a compatible complete package it attempts `Seal` → `PreparePaths` →
`NewPlan` → real nsjail readiness, an execution-bound SDK call, and lazy imports
from both concrete code and runtime trees after retirement. It checks Node 26 /
Python 3.15 and holds leases through a controlled post-Wait callback before
reclamation. The metadata snapshot is synthetic host-owned test data, not a DB
transaction, current-generation proof, IAM admission, or policy approval.
For the raw qualification, the package itself must contain its coherent adapter
and private-channel SDK. The recipe qualification explicitly builds a different
artifact using the reviewed current source overlay below.

## Reviewed build-time recipe v1

`runtime_recipe_linux_test.go` is **test-only**. It accepts only the two reviewed
base package digests in the evidence table below. Those digest values must still
come through independent publisher-checksum verification before decompression.
It is not a way to bless an arbitrary installed tree or sender-provided manifest.

The recipe removes exactly fourteen packaging-only links. Every path must be
present as a symlink with the **exact** target below; a missing path, different
target or different type fails before transformation:

| Path | Exact target |
| --- | --- |
| `etc/mtab` | `/proc/mounts` |
| `etc/alternatives/awk.1.gz` | `/usr/share/man/man1/mawk.1.gz` |
| `etc/alternatives/nawk.1.gz` | `/usr/share/man/man1/mawk.1.gz` |
| `etc/alternatives/rmt.8.gz` | `/usr/share/man/man8/rmt-tar.8.gz` |
| `etc/alternatives/pager.1.gz` | `/usr/share/man/man1/more.1.gz` |
| `etc/alternatives/builtins.7.gz` | `/usr/share/man/man7/bash-builtins.7.gz` |
| `etc/alternatives/which.1.gz` | `/usr/share/man/man1/which.debianutils.1.gz` |
| `etc/alternatives/which.es1.gz` | `/usr/share/man/es/man1/which.debianutils.1.gz` |
| `etc/alternatives/which.pl1.gz` | `/usr/share/man/pl/man1/which.debianutils.1.gz` |
| `etc/alternatives/which.ja1.gz` | `/usr/share/man/ja/man1/which.debianutils.1.gz` |
| `etc/alternatives/which.de1.gz` | `/usr/share/man/de/man1/which.debianutils.1.gz` |
| `etc/alternatives/which.sl1.gz` | `/usr/share/man/sl/man1/which.debianutils.1.gz` |
| `etc/alternatives/which.it1.gz` | `/usr/share/man/it/man1/which.debianutils.1.gz` |
| `etc/alternatives/which.fr1.gz` | `/usr/share/man/fr/man1/which.debianutils.1.gz` |

These are absent-manpage alternatives plus a dynamic proc link which cannot
satisfy static manifest closure. No interpreter/runtime link is removed.
`etc/mtab` is honestly absent afterward; no fabricated mount data is installed.
All other base nodes, numeric ownership, modes, hardlinks and link text remain
unchanged except the explicitly replaced adapter files.

Source bytes are pinned by reviewed SHA-256 constants to the runtime sources at
revision `f6f2560a93f4a51840e10de72f5f008e46624336`. The current checkout must match
all hashes, including type declarations and package metadata, or the test-only
producer fails. The source files were independently compared and remain byte-identical
in the tested parent revision `3ac2f940f6fb45b3ff4fa381a8915ec7db3c270f`.
The uncommitted harness snapshot identity is recorded separately from this
runtime source revision. A revision label supplied by a sender cannot replace
the reviewed hash constants.

The overlay matches `backend/cmd/orva/setup.go`'s `sdkFiles`/`sdkDestDir`:

- Node: `adapter.js` → `opt/orva/adapter.js`; `orva.js`, `orva.d.ts`,
  `package.json` → `opt/orva/node_modules/orva/`.
- Python: `adapter.py`, `orva.py`, `py.typed` → `opt/orva/`.

Existing SDK destinations are not silently merged or overwritten. New SDK
nodes use mode 0755 (directories) / 0644 (files) and the packaged `opt/orva`
directory's UID/GID; replaced adapters retain their original UID/GID and 0644
mode. All source hashes are checked before any omission or overlay write.

The **expected final manifest is derived before Seal** from the independently
authenticated base headers/content hashes, exact omission recipe, and reviewed
source bytes. It is not learned by walking the output tree. A manifest-driven
archive writer verifies regular bytes against those expectations and emits
`runtime.tar` plus `producer.json` with base checksum, source revision/hashes,
omissions, complete final manifest, manifest digest and final archive digest.
The output is authority only as the output of this trusted build process, not
because an arbitrary archive carries its own checksum. Qualification aborts the
producer stage, imports that emitted archive into a **new** stage, checks it
against the producer's original expected manifest, and seals using those
original expectations. Extracted output is never its own source of authority.

Inside the same disposable guest, with package checksum variables set:

```sh
mkdir /fresh-recipe-output
ORVA_RUNTIME_RECIPE_TEST=1 ORVA_RUNTIME_RECIPE_OUTPUT=/fresh-recipe-output \
  GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -count=1 -v -run '^TestRuntimeRecipeQualification$' ./backend/internal/workloadspawn
```

The parent output directory must exist and per-runtime output directories must
not exist. Repeated attempts require distinct fresh output directories. This
does not enable the raw-package qualification or turn its failures into skips.

Ordinary producer regressions need no nsjail:

```sh
CGO_ENABLED=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -race -count=5 -run '^TestRuntime(Package|Recipe(Trust|Output))' ./backend/internal/workloadspawn
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go vet ./backend/internal/workloadspawn ./backend/internal/artifactfs
```

## Observed blocker — 16 November 2026

The then-current published release (`v2026.11.07`) packages were independently
downloaded and checksum-verified inside a fresh Debian trixie VM:

| Package | SHA-256 | Entries |
| --- | --- | --- |
| Node amd64 | `34c7b8f1559a55018827fef7f96a540afb8cfeba665ee89dbbe3170852284ed3` | 6219 |
| Python amd64 | `ff692cfc7da5bc142b027b34194da7653db79fe1277945c2363227eb04c56ed5` | 5791 |

Both complete extracted trees reached the unchanged `Seal` and failed with
`artifactfs: unsupported tree`. Both archives contain these proven missing
static targets, among others:

- `etc/mtab -> /proc/mounts` (the package's `proc` directory is empty)
- `etc/alternatives/awk.1.gz -> /usr/share/man/man1/mawk.1.gz`
- `etc/alternatives/pager.1.gz -> /usr/share/man/man1/more.1.gz`
- `etc/alternatives/which.1.gz -> /usr/share/man/man1/which.debianutils.1.gz`

The diagnostic only reports missing direct targets whose complete parent chain
is explicit directories, not false positives through symlinked parents.
`/proc` being mounted later by nsjail does not satisfy static manifest closure.
No link, file or metadata was repaired or removed. Both opt-in cases therefore
**failed before NewPlan/nsjail**; readiness, private SDK, concrete runtime
execution and real post-Wait retention remain **unverified**, not passed.
These older archives list adapters but no bundled SDK under `opt/orva`, which
is another producer obligation before execution qualification can succeed.

This original failed-package evidence is retained unchanged. The separate recipe
qualification above addresses the producer obligations explicitly, not through
consumer filtering or verifier relaxation. Its result must be recorded
separately; it cannot retroactively qualify those unmodified published packages.

## Recipe qualification evidence — 16 November 2026

The explicit recipe and subsequent emitted-archive import passed on a fresh
2-CPU / 4-GiB / 20-GiB Debian trixie Microsandbox:

| Runtime | Interpreter | Final entries | Final manifest SHA-256 |
| --- | --- | --- | --- |
| Node | 26.13.0 | 6210 | `9417890da9a45b1ad0c259a36c6ccb810d255169b2e6a9d776daf7cdc2122f87` |
| Python | 3.15.2 | 5779 | `d5bee5f460be359f0c556f1da033e1250e1464cafb068ee33853653d09e3f635` |

Final uncompressed deterministic archive SHA-256 values:

- Node: `6a85bea48a4ee93458ca1bb7bf071535928ee2a93ce5792670c9b1a151a0122c`
- Python: `59a66da6b75c1394efd3e7f2b8719df23b0d5628e6ea326dd8425a6090077918`

Both used concrete store-minted paths through `Seal` → `PreparePaths` →
`NewPlan` → **real nsjail**, reached adapter readiness, returned an execution-
bound SDK KV result over the private worker channel, and imported lazy code and
runtime modules after both generations were retired. Reclamation returned
`ErrBusy` with the ready worker alive and again while its real post-Wait callback
was deliberately held. After releasing that callback, both generations were
reclaimed and the fresh store closed successfully.

The Node lazy runtime import read npm's packaged `package.json`; the Python lazy
runtime import loaded `fractions`. Response versions, host-owned function/
artifact/execution attribution, nonempty execution lease and expected return
values were asserted, not inferred from process creation.

The VM reported **rlimit-only** fallback because cgroup controllers were not
delegated. This does not prove cgroup hard limits, production release packaging,
pool/serving integration, current-generation/IAM admission or crash recovery.
The source overlay and omitted links are a new trusted **scratch build artifact**,
not a claim that the published package already contains those changes.

Final checks passed: the ordinary `workloadspawn` package suite; five offline
race runs of the package/recipe producer tests; vet for `workloadspawn` and
`artifactfs`; and both real-runtime qualification subtests under `-race`.
The repeated normal/race builds emitted byte-identical producer records and
archive hashes. Malformed/unreviewed checksums, changed/missing omission targets,
unknown dangling links, source-revision mismatch and each individually changed
adapter/SDK source file were exercised as denials. Unit fixtures also round-trip
the emitted archive against the original expected manifest.

Evidence and compressed final build artifacts were copied to
`/tmp/opencode/iam-runtime-producer-v2/`; the copied compressed artifacts and
tested source archive were checksum-verified. The tested code snapshot archive
SHA-256 is `7aee3dee3212bc0f0aed4c262516ef77984e4b39ae26363420300463b8f95da0`;
the individual tested Go-source hashes match the final checkout. The owned VM
`iam-runtime-producer-v2-test` was stopped and removed. Another worker's
`iam-policy-service-qa` VM was observed and left untouched.

Copy logs/checksum provenance out before stopping and removing the VM. Report
owned-sandbox cleanup separately from test success; do not delete other workers'
sandboxes. Focused tests are not full E2E, cgroup enforcement, crash recovery or
serving-IAM acceptance.
