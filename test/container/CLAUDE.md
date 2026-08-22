# test/container/

End-to-end verification against a **real Orva container on a throwaway Docker
network**. Unlike `test/` (which needs a live instance you supply) this harness
builds the image, brings up its own container, runs the suites, and removes
everything on exit.

```bash
sudo -v                      # the harness shells out to sudo docker
bash test/container/run.sh
```

## Why it exists

nsjail needs `CAP_SYS_ADMIN`, unconfined apparmor/seccomp, a **host cgroup
namespace** and a **host PID namespace**. A developer box frequently cannot give
it those, and when it cannot, *every* invocation fails with `WORKER_CRASHED` for
reasons that have nothing to do with the code under test — which is
indistinguishable from a real regression until you check the host.

The container is given exactly the privileges `docker-compose.yml` grants in
production, so a sandbox failure here is a real failure. `ORVA_REQUIRE_SANDBOX=1`
is set, which makes a skipped sandbox a hard error: a green run cannot be a run
that quietly never entered nsjail.

## The two flags people forget

Both are in `docker-compose.yml` and both are load-bearing:

- **`--cgroupns=host`** — nsjail sets `memory.max` / `cpu.max` per function and
  needs a writable, properly-delegated cgroupfs. A private read-only view
  silently degrades to `rlimit_as` with no CPU throttling.
- **`--pid=host`** — the `NSJAIL.<pid>/cgroup.procs` writes reference PIDs in the
  **host's** namespace. Without it every spawn fails with
  `Couldn't write '2' bytes to file '/sys/fs/cgroup/orva-sandboxes/NSJAIL.<pid>/cgroup.procs': No such file or directory`,
  which reads like a cgroup-mount problem and is not one.

## Isolation

A fresh network (default `orva-e2e-net`) is created per run and removed on exit,
along with the container and its volume. The port is published on **127.0.0.1
only**, so the instance is not reachable off-box and cannot collide with a
deployment on another network. Nothing here touches an existing network or
container.

## Build note

The image is built with `--network=host`. A daemon whose default `bridge`
network has been removed fails a normal build with `network bridge not found`,
and buildkit refuses a user-defined network (`not supported by buildkit`). It
affects the build only, not the running container.

## Files

| File | Purpose |
|---|---|
| `run.sh` | build → network → container → health → nsjail namespace check → suites → cleanup |
| `rollback_e2e.py` | TypeScript entrypoint split + rollback promotion model, asserted through real invocations |
| `seed.py` | creates one of every resource so the browser suite measures populated lists |

## What `rollback_e2e.py` covers

Every assertion is about what the sandbox actually executed, not about what a
row says — the defects it covers were invisible to unit tests because they only
surfaced when a worker tried to run the file it had been handed.

- a TypeScript function deploys, and its first deploy is **v1** (not v2 —
  `functions.version` is a mutation counter, deployment numbers come from the
  deployment sequence)
- `entrypoint` stays the authored `handler.ts`; `run_entrypoint` holds
  `dist/handler.js`
- `GET /source` returns TypeScript **as authored**, not compiled output
- re-deploying succeeds, proving the validator checks the source `.ts`
- rollback **promotes** an existing deployment: no new version row, the live
  marker moves, and the promoted revision is the one that answers
- **the legacy-snapshot defect**: a deployment snapshot written before
  `run_entrypoint` existed is reproduced by stripping the field, then rolled
  onto. The invocation must return 200 — restoring that absence verbatim points
  a compiled TypeScript version back at its `.ts` source, which Node cannot
  execute
- rolling back to the version already live is refused as a no-op

## Empty lists hide controls

`seed.py` runs before the browser suite, and it is not cosmetic. A list view
renders its row controls only when it has rows, so anything that only exists
inside a row is invisible to a measuring harness until something is there to
measure. A KV key button that came out 102x15 -- well under the 44px touch
floor -- survived several complete, green runs for exactly that reason: no run
had ever put a key in the store.

Seeding is best-effort and idempotent: an already-existing resource (409) counts
as seeded, and a resource that will not create is reported but does not stop the
suite. It creates a function and a real deployment, a KV entry (plus a
string-typed one, which is the shape that used to change type on save), an API
key, a cron schedule, a job, an inbound webhook using a signature format the
browser could never sign, an outbound subscription, a channel, a firewall rule,
and one invocation so Invocations, Traces and Activity have rows.

## Relationship to the other suites

`test/e2e/` (Python) remains the authoritative full-API suite and spins its own
container. This harness is narrower and exists for defects that need a working
sandbox plus database surgery the API does not expose. `test/browser/` runs
against the same container when `ORVA_E2E_BROWSER=1` (the default).
