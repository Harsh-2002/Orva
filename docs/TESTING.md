# Testing Orva end to end

This is the document you read when someone hands you the Orva repo and says
"verify it works." It assumes no prior knowledge of the project and no human
available to answer questions. It covers how to stand up a testable instance,
what to exercise and in what order, what correct output looks like, what *should*
fail, how to tell a real regression from environment noise, and how to build a new
harness for a subsystem.

It is not an index of scripts. [`test/CLAUDE.md`](../test/CLAUDE.md) and
[`test/e2e/CLAUDE.md`](../test/e2e/CLAUDE.md) list the harnesses; this document is
about judgement — which layer answers which question, and what each layer
*cannot* tell you.

**Provenance.** Every command below was executed against a real instance and the
stated output observed, unless it carries a **`[UNVERIFIED]`** marker. Those
markers are load-bearing: a command that "should work" is worse than useless
here, because the whole point is that you can follow this blindly. Measured
timings come from one 8-vCPU Linux 6.12 host with warm caches — treat them as
orders of magnitude, not SLAs.

---

## 1. The shortest path to "I have verified Orva works"

### 1.1 Ten minutes: does this build and does the sandbox actually execute code

```bash
cd /path/to/Orva
make lint && make test          # go vet + go test ./...   (~2 s + ~23 s)
make build                      # → build/orva             (~5 s)
```

Then stand up a throwaway instance on a scratch data dir and invoke something.
This is the fastest path that proves the *kernel boundary* works, and it needs no
Docker and no root:

```bash
mkdir -p /tmp/orva-scratch/rootfs
ln -s /var/lib/orva/rootfs/node   /tmp/orva-scratch/rootfs/node
ln -s /var/lib/orva/rootfs/python /tmp/orva-scratch/rootfs/python
ORVA_DATA_DIR=/tmp/orva-scratch ORVA_PORT=18443 ./build/orva serve
```

(The symlinks reuse rootfs trees an existing install already built. On a machine
with no Orva install, build them first — §2.4.)

The server prints a bootstrap admin key on first boot. In a second shell:

```bash
export BASE=http://127.0.0.1:18443
export KEY=$(cat /tmp/orva-scratch/.admin-key)
export ORVA_DATA_DIR=/tmp/orva-scratch      # §2.2/§2.3 probes read this
BASE_URL=$BASE API_KEY=$KEY bash test/api-smoke.sh
```

(`api-smoke.sh` reads `BASE_URL`/`API_KEY`, not `BASE`/`KEY` — without the
prefix it exits 1 immediately with `test/api-smoke.sh: line 12: API_KEY: set
API_KEY`.)

Expected: `=== api-smoke: 20 passed, 0 failed ===`, exit 0, under a second.
That suite does one real deploy and one real invoke, so a pass means nsjail
spawned and executed user code.

### 1.2 One hour: the authoritative pass

```bash
cd test/e2e
PYTHONPATH=. ORVA_URL=$BASE ORVA_API_KEY=$KEY \
  ORVA_BIN=/abs/path/to/Orva/build/orva ORVA_REQUIRE_SANDBOX=1 \
  python3 tests/test_deploy_invoke.py
```

Expected: `ALL PASSED — 31 checks` / `RESULT pass=31 fail=0`, exit 0. That
module is the one that proves node **and** python deploy, jailed `npm`/`pip`
installs, and invoke. With `ORVA_REQUIRE_SANDBOX=1` it cannot silently skip.

Then the full suite. On a host where nested sandboxing works, the canonical
invocation is the isolated Docker mode:

```bash
cd test/e2e && python3 run.py --rebuild
```

**`[UNVERIFIED]`** — no surveyor executed a full `run.py` in isolated-Docker
mode, because it overwrites the tracked `test/e2e/CHECKLIST.md` and requires a
full image build whose duration was never measured. The isolated path
(`env.py`) is documented here from source plus verified preconditions, not from
a run. What *was* verified: on the survey host, nsjail cannot spawn inside that
container at all (§2.4), so the isolated mode is not usable there — use `--url`
against a bare-metal instance instead:

```bash
cd test/e2e && python3 run.py --url $BASE --api-key "$KEY"
```

That is also how CI runs it, and how the last committed `CHECKLIST.md` was
produced.

### 1.3 What "verified" cannot mean

Say these out loud before you claim a change is verified:

| You ran | You have NOT shown |
|---|---|
| `make test` | anything about nsjail, deploys, invokes, HTTP, or the UI |
| the shell suites | anything about arm64, installers, the CLI as a shipped artifact, or a virgin-DB onboarding flow |
| `test/e2e/run.py` **without** `ORVA_REQUIRE_SANDBOX=1` | that deploy/invoke works — the sandbox modules skip silently and the run still exits 0 |
| `install-matrix` green | that invocation works — `smoke-flow.sh` downgrades `WORKER_CRASHED`/`SANDBOX_ERROR` to warnings |
| everything on amd64 | that the build jail's seccomp policy compiles on arm64 (Kafel treats an unknown syscall name as a compile error) |

---

## 2. Prerequisites and environment bring-up

### 2.1 Host requirements

| Requirement | Why | Check |
|---|---|---|
| Go 1.26+ | the embedded Bifrost AI gateway needs it | `go version` |
| Node 24 | frontend build only — *not* the function runtime | `node --version` |
| Python 3.14 (CONTRACT §1) | CI parity. The E2E suite is stdlib-only and **ran clean on 3.13.5** — do not go install 3.14 before you can run anything | `python3 -V` |
| Linux, cgroup v2, unprivileged userns | nsjail | `mount \| grep cgroup2` |
| **nsjail at `/usr/local/bin/nsjail`** | every invocation | §2.2 |
| rootfs trees under `$ORVA_DATA_DIR/rootfs/{node,python}` | every invocation | §2.3 |
| `jq` | almost every shell suite | `command -v jq` |
| `openssl` | `auth-test.sh` HMAC signing | `command -v openssl` |
| `docker` | isolated E2E, install matrix, rootfs build | `docker version` |
| `hey` | `atscale.sh`, `ceiling.sh`, `loadtest.sh` — **hard exit 2 without it**; optional extra checks in `routes-test.sh` and `secrets-test.sh` | `command -v hey` |
| `sqlite3` **inside the container** | 2 DB-sanity checks that silently vanish otherwise | — |

On the survey host `hey` and `sqlite3` were **absent**. Everything documented
below as measured was measured without them; the checks they gate were never
observed running anywhere and may be effectively dead (§9).

### 2.2 nsjail — the hard requirement

nsjail is Google's process jailer (namespaces, seccomp, chroot, and optional
NSTUN user-mode networking). Orva spawns one nsjail process per warm sandbox
worker and one per dependency-install build step. Real argv from a live
instance:

```
/usr/local/bin/nsjail -Mo --chroot /var/lib/orva/rootfs/node \
  -R /var/lib/orva/functions/<fnID>/current:/code -T /tmp --rlimit_as max -q \
  --seccomp_string "POLICY orva { … }" --env ORVA_FUNCTION_ID=… \
  -- /usr/local/bin/node /opt/orva/adapter.js
```

For `network_mode: egress` the argv is prefixed with
`--config /var/lib/orva/firewall/policy/egress-<gen>.cfg` — which **must** be
`argv[0..1]` — plus `--user_net` and bind-mounts for `/etc/resolv.conf` and
`/etc/hosts`.

**The path is hardcoded, not resolved from `$PATH`.**
`backend/internal/config/defaults.go:42` sets
`NsjailBin: "/usr/local/bin/nsjail"` and there is no environment override.
CONTRACT §1's "nsjail on `PATH`" is imprecise — a host with nsjail at
`/usr/bin/nsjail` satisfies the documented requirement and still fails every
invocation.

**Do not trust the health field.**

```bash
curl -s $BASE/api/v1/system/health | jq .sandbox.runtime   # "ok" | "unavailable"
```

That is a bare `os.Stat` (`handlers/system.go:147-152`) and it reports `"ok"`
even when `NsjailBin` is empty. It was `"ok"` in a container where every single
invoke returned 502. **Only a real invoke proves the sandbox.**

Two distinct failure signatures, both reproduced:

| Condition | Deploy | Invoke |
|---|---|---|
| nsjail binary missing | succeeds, function reaches `active` | **503** `SANDBOX_ERROR` — `pool acquire: start nsjail: fork/exec /usr/local/bin/nsjail: no such file or directory` |
| nsjail present but cannot spawn (e.g. `/proc` overmounted in a `--pid=host` container) | succeeds, `active` | **502** `WORKER_CRASHED`; nsjail's own stderr says `buildMountTree(): Failed to mount mandatory point: '/proc'` |

Note that in both cases the server stays `healthy` and deploys still succeed.
**`status: active` is not proof the platform works.**

Manual spawn probe — the fastest way to separate "Orva is broken" from "this
host cannot jail":

```bash
nsjail -Mo --chroot $ORVA_DATA_DIR/rootfs/node -R /tmp:/code -T /tmp -q \
  -- /usr/local/bin/node --version
```

Expected: a version string. Anything else is a host problem, not an Orva bug.

### 2.3 Runtime rootfs trees

Each runtime needs a full extracted image filesystem at
`$ORVA_DATA_DIR/rootfs/{node,python}`, with the adapter and bundled SDK inside
it at `<rootfs>/opt/orva/adapter.js` and
`<rootfs>/opt/orva/node_modules/orva/`.

Build them with either:

```bash
bash scripts/build-rootfs.sh $ORVA_DATA_DIR/rootfs/node   node    # needs docker
bash scripts/build-rootfs.sh $ORVA_DATA_DIR/rootfs/python python
./build/orva setup --data-dir $ORVA_DATA_DIR                      # also does setcap
```

`orva setup --rootfs-url <base>` downloads release tarballs instead of building.

Versions **inside** the rootfs, measured by invoking a handler that prints them:
node `v24.16.0`, python `3.14.5`. The host's own node/python are irrelevant to
functions — but the host's `python3` does run the E2E harness.

Missing-rootfs signature (reproduced on a fresh data dir): deploy succeeds and
the function reaches `active`; invoke returns **503**
`pool acquire: rootfs not found at <dataDir>/rootfs/node`.

```bash
ls -d $ORVA_DATA_DIR/rootfs/node $ORVA_DATA_DIR/rootfs/python
ls $ORVA_DATA_DIR/rootfs/node/opt/orva/adapter.js
```

### 2.4 Four ways to get a testable instance

**(1) Bare-metal `orva serve` — fastest, no Docker, no root.** §1.1. Verified:
comes up in under 2 s as an unprivileged user, prints the bootstrap key, and
real sandboxed invocation works. The last full green `CHECKLIST.md` run
targeted exactly this shape (`http://127.0.0.1:18443`). Port 18443 is also the
shell suites' default `BASE_URL`.

**(2) `docker compose up -d` — host 3000 → container 8443.** `docker compose
config` validates; the file supplies everything nsjail needs (`cap_add:
SYS_ADMIN`, `cgroup: host`, `pid: host`, `/sys/fs/cgroup`, `/dev/net/tun`,
seccomp/apparmor/systempaths unconfined). **`[UNVERIFIED]` end to end** — on
the survey host the dev instance already owns port 3000, so compose was never
brought up. Given that the same `--pid=host` + `/sys/fs/cgroup` combination
broke nsjail's `/proc` mount in the E2E container on that host, do not assume
compose gives you a working sandbox there without checking with a real invoke.

**(3) The isolated E2E container (`test/e2e/env.py`) — host 8455 → container
8443.**

```bash
cd test/e2e
python3 run.py                          # build if needed, run all, write CHECKLIST.md, tear down
python3 run.py --rebuild                # force image rebuild
python3 run.py --keep                   # leave it up for debugging
python3 run.py --url URL --api-key KEY  # target an existing instance, skips Docker entirely
```

Container `orva-e2e`, volume `orva-e2e-data`, both removed on teardown. Admin
key via `docker exec orva-e2e cat /var/lib/orva/.admin-key`. **`[UNVERIFIED]`
as a full run** — see §1.2. Two traps that *were* verified:

- **`ensure_image()` silently reuses a stale image.** It returns early whenever
  the `orva:e2e` tag exists, with no staleness check. On the survey host that
  tag was **two months old**: its logs still said `firewall: nftables
  unavailable — egress filtering disabled` (code deleted in the NSTUN rewrite),
  and it served a stale embedded UI whose sidebar said "Firewall" where current
  code says "Egress". **`python3 run.py` without `--rebuild` can go fully green
  against code you deleted months ago.** Always check:
  ```bash
  docker image inspect orva:e2e --format '{{.Created}}'
  ```
- **Nested sandboxing failed in that container on the survey host.** `--pid=host`
  plus the host's `/proc` overmounts made nsjail fail
  `buildMountTree(): Failed to mount mandatory point: '/proc'`, so
  `test_deploy_invoke.py` skipped (exit 3) — and with `ORVA_REQUIRE_SANDBOX=1`
  reported `1 FAILED / 6 checks`. If you see that, use option (1).

**(4) A live/shared instance.** Read-only work is fine. Before you point any
suite at it, read §3.2.5 — one E2E module deletes *every* AI conversation on the
target, and one shell suite leaves 20 functions behind permanently.

### 2.5 Build traps that silently ship stale artifacts

These are the four ways to test something other than what you think you are
testing.

**Trap 1 — `make cli` overwrites the server binary.** `Makefile:66` and
`Makefile:97` both write `build/orva`. Verified:

```
$ make cli && ./build/orva serve --help
Error: unknown command "serve" for "orva"
$ make build     # restores the 55 MB server binary
```

`test/e2e/harness.py:35` and `run.py` both default `ORVA_BIN` to that path, so
`make build` → `make cli` → `python3 run.py` runs the CLI tests against a
binary you did not intend. **Always `make build` last**, or build the CLI
somewhere else. This collision is documented nowhere else.

**Trap 2 — `adapters-embed` (CONTRACT §3), with a nuance.**
`backend/cmd/orva/adapters/` is **tracked in git**, so a raw
`go build ./backend/cmd/orva` on a clean checkout succeeds — it just embeds the
last-committed adapter snapshot. The failure mode is not a build error, it is a
silently stale runtime adapter:

```bash
diff -r -x __pycache__ backend/runtimes/node   backend/cmd/orva/adapters/node
diff -r -x __pycache__ backend/runtimes/python backend/cmd/orva/adapters/python
# any output = stale. `-x __pycache__` is required: adapters-embed copies an
# explicit file list, so a byte-compiled runtimes/python/__pycache__ (gitignored,
# and present on any host that has run the python adapter) is a false positive.
```

**Trap 3 — `ui_dist`, and `make clean` breaking the build.**
`backend/internal/server/ui_dist/` is **tracked** and is *not* matched by
`.gitignore` (the pattern is root-anchored). `make clean` does
`rm -rf backend/internal/server/ui_dist`, which deletes tracked files and makes
the next build fail hard:

```
$ rm -rf backend/internal/server/ui_dist && go build -o /dev/null ./backend/cmd/orva
backend/internal/server/ui.go:10:12: pattern all:ui_dist: no matching files found
```

So `make clean` must always be followed by `make embed` or `make build-all`.
Staleness check that does not touch git:

```bash
(cd frontend && npm install && npm run build) && diff -rq frontend/dist backend/internal/server/ui_dist
```

Empty output means the snapshot is current. It was current at survey time.

**Trap 4 — the stale `orva:e2e` image.** §2.4, option 3.

Docs staleness has an equivalent check:

```bash
cmp docs/reference.md backend/internal/mcp/reference.md \
  && cmp docs/reference.md frontend/public/docs.md \
  && cmp docs/reference.md cli/commands/reference.md && echo "docs in sync"
```

### 2.6 Getting an API key on a fresh instance

**(a) The bootstrap admin key.** On first boot with zero API keys the server
generates `orva_<64 hex>`, inserts it with `["invoke","read","write","admin"]`,
writes `<dataDir>/.admin-key` mode 0600, and prints:

```
========================================
  BOOTSTRAP ADMIN API KEY
  orva_c9ba3ab6810db9897a6729c0414275b4e5d3acb22af501693e40cb730b54791c
  (saved at /tmp/orva-scratch/.admin-key)
========================================
```

Retrieve it later with `cat $ORVA_DATA_DIR/.admin-key`,
`sudo cat /var/lib/orva/.admin-key`, or
`docker exec <c> cat /var/lib/orva/.admin-key`.

> **The single most likely way to get a wall of red.** README.md and
> `test/e2e/CLAUDE.md` both document the key recipe as
> `--api-key "$(sudo cat /var/lib/orva/.admin-key)"`. On an instance that was
> onboarded through the browser, **that file does not exist** — it did not exist
> on the survey host. The substitution yields an empty string, `run.py`'s
> `resolve_admin_key()` fallback hits the same missing file and also returns
> `""`, and then **every module reports `FAIL … missing RESULT trailer`** —
> a message about the RESULT protocol with no hint that your key was blank.
> This was reproduced deliberately. If you see that symptom on every module,
> check the key before you check anything else.

**(b) Browser onboarding** (first user only):

```bash
curl -s $BASE/api/v1/auth/status                      # {"has_user":false}
curl -s -X POST $BASE/api/v1/auth/onboard -H 'Content-Type: application/json' \
     -d '{"username":"admin","password":"at-least-8-chars"}'   # 200 + Set-Cookie: session_token
```

A second call returns **409 `ALREADY_SETUP`**. The field is `username`, not
`email`. The API minimum is 8 characters; the onboarding UI enforces 10 plus
lower/upper/digit/symbol. Login is rate-limited to 10/min/IP.

**(c) A scoped key**, which is also the fastest permission test:

```bash
orva --endpoint "$BASE" --api-key "$KEY" keys create --name ci --permissions read
```

```
Created API key 019fed92-dbc9-7512-ae6b-651a90de619b
Save this key now — it will not be shown again:
orva_e0c53ede035c23c87697e51aff8a5ca9b5f07bee976dc8a458def5a9671a8177
```

**That is human text, not JSON** — three lines, and the key is on its own line.
For a parseable answer you must ask for it with the global `-o json` flag,
which every CLI command accepts (a second key, so you can compare the two
shapes side by side):

```bash
orva --endpoint "$BASE" --api-key "$KEY" keys create --name ci2 --permissions read -o json
```

```json
{
  "created_at": "2026-08-10T21:27:32.609929314Z",
  "expires_at": null,
  "id": "019fed92-dc01-7975-a051-03fd0d7f09e3",
  "key": "orva_7677225c5ecb6373d5633cdc6ce26fc3f0a24b5c8a058de042d60e7ebf32164d",
  "name": "ci2",
  "permissions": ["read"],
  "prefix": "orva_7677225"
}
```

(Pretty-printed over multiple lines; only `permissions` is folded here, for
width. Both transcripts are verbatim from one scratch instance, which is why
the two ids differ.)

> **`--endpoint` / `--api-key` are not optional decoration.** The CLI never
> reads `$BASE`/`$KEY` (or `$B`/`$K`) on its own — without those flags it uses
> `~/.orva/config.yaml`, i.e. **your everyday instance**. Worse, an *empty*
> flag value falls back to the config file **silently**: verified, `orva
> --endpoint "" --api-key "" functions list` listed the functions of the
> instance in `~/.orva/config.yaml`, exit 0, no warning. So the flags only
> protect you if the variables are actually set **in the shell that runs the
> command**. See the warning at the top of §4.

| Action | Result |
|---|---|
| read with a `read` key | 200 |
| `POST /functions` with a `read` key | **403** `{"error":{"code":"FORBIDDEN","message":"insufficient permissions, requires: write","request_id":""}}` |
| `DELETE /api/v1/keys/{id}` | 200 |
| the same key immediately after revoke | **401** `{"error":{"code":"UNAUTHORIZED","message":"invalid API key","request_id":""}}` — no cache window |

That last row is a regression guard: the auth middleware once cached keys and
only evicted on expiry, so a deleted key kept authenticating.
`test_keys.py` asserts it.

On an instance whose plaintext bootstrap key is gone (DB has keys, keyfile
deleted), nothing is printed and the plaintext is unrecoverable — issue a new
key through the dashboard or an existing session and move on.

The CLI stores its key at `~/.orva/config.yaml`:

```bash
export KEY=$(grep -o 'orva_[A-Za-z0-9_]*' ~/.orva/config.yaml | head -1)
```

### 2.7 Port map

CONTRACT §5's table is incomplete for testing. The full picture:

| Context | Port | Source |
|---|---|---|
| `orva serve` default | 8443 | `config/defaults.go` |
| Docker compose host map | 3000 → 8443 | `docker-compose.yml` |
| Frontend dev server | 5173 | `make dev` |
| Shell suites' default `BASE_URL` | **18443** | `test/*.sh` |
| `harness.ORVA_URL` default | 8443 | `test/e2e/harness.py:33` |
| **Isolated E2E container, host side** | **8455** | `test/e2e/env.py` (`ORVA_E2E_PORT`) — absent from CONTRACT §5 |
| Mock LLM | 11434 | `run.py` / `harness.py` (`MOCK_PORT`) — **collides with Ollama's default** |
| `install-matrix` distro containers | **19449–19454** | `run-distro.sh`, `19443 + NR-1` over a file with 6 comment lines; the script's own usage text says 19443+index and is wrong |
| `failure-modes.sh` / `kata-flow.sh` / kata-bench | 19999 / 28443 / 38443 / 18443 | — |

If a host runs Ollama, `start_mock()` raises `OSError: [Errno 98] Address
already in use` inside the harness *before any check runs*, so the module dies
with no RESULT trailer and `run.py` reports `missing RESULT trailer` — again
naming the protocol instead of the real cause. Set `MOCK_PORT` to something
free.

### 2.8 The 60-second smoke test

`/api/v1/system/health` proves almost nothing (§2.2). This is the smallest
sequence that proves the platform, and every step below was observed passing
against a live instance in 2.6 s total:

| # | Check | What it proves that health does not |
|---|---|---|
| 1 | `status == healthy` | baseline |
| 2 | `sandbox.runtime == ok` | the nsjail file exists — necessary, not sufficient |
| 3 | bogus key → **401** | the auth gate is live |
| 4 | `POST /api/v1/functions` → id | write path + DB |
| 5 | `deploy-inline {wait:true}` → `deployment_id` | build queue |
| 6 | poll `status == active` | the builder finished |
| 7 | `POST /fn/{id}` returns the handler's JSON | **the sandbox executed code** |
| 8 | `GET /executions?function_id=…` → `status_code 200` | the async execution writer works |
| 9 | `POST /executions/{id}/replay` returns the same body | request capture + replay |
| 10 | secret set → redeploy → handler reads `process.env` | secrets decrypt into the jail |
| 11 | `GET /functions/{id}/secrets` → names only | no plaintext leak |
| 12 | `POST /mcp tools/list` → 73 tools | MCP surface + auth |
| 13 | `GET /web/` → 200 | the embedded UI is present |

Steps 10 and 11 are the only two rows this document has not already handed you a
command for by this point — the full recipe is §4.5. In short, against a
function `<fn>` whose handler returns `{ secret: process.env.QA_SECRET }`:

```bash
orva --endpoint "$BASE" --api-key "$KEY" secrets set <fn> QA_SECRET --value "s3cr3t-value-2026"
orva --endpoint "$BASE" --api-key "$KEY" deploy ./demo --name <fn> --runtime node --follow
orva --endpoint "$BASE" --api-key "$KEY" invoke <fn> --body '{}'
# POST <fn> · 200 · 86ms
# {"secret":"s3cr3t-value-2026"}                       ← row 10
curl -s -H "X-Orva-API-Key: $KEY" "$BASE/api/v1/functions/$FID/secrets"
# {"secrets":["QA_SECRET"]}                            ← row 11: names only
```

(Setting a secret drains the warm pool, so the redeploy is not strictly required
— but redeploying is the shape the row describes, and it makes the cold start
explicit. `$FID` is the function's UUID; the CLI resolves names for you, REST
mostly but not always does — §5.1.)

Step 2 is the trap and step 7 is the gate: step 2 was `"ok"` in a container
where every invoke 502'd.

The repo's own equivalent, self-cleaning and 20 checks:

```bash
BASE_URL=$BASE API_KEY=$KEY bash test/api-smoke.sh
# === api-smoke: 20 passed, 0 failed ===
```

---

## 3. The testing layers

Four layers, each answering a different question. Reach for the cheapest one
that can answer yours.

| Layer | Answers | Runtime | Needs a server? | Needs nsjail? | Exit code |
|---|---|---|---|---|---|
| Fast host-only | "does the Go code compile, vet clean, and pass unit tests" | ~25 s | no | no | 0/1 |
| Python E2E (`test/e2e/`) | "does every documented behavior still work as spec'd" | ~60 s suite + setup | yes (or spins one) | for 3 modules | 0 pass · 1 fail · 2 setup |
| Shell suites (`test/*.sh`) | "does this one subsystem behave against a live instance" | 0.5–15 s each | yes | most | varies — see §3.4 |
| Install / CLI / release | "does the shipped artifact install, run, and upgrade" | 20 s – 3 min per leg | starts its own | no | 0/1/2 |

### 3.1 Fast host-only

```bash
make lint    # go vet ./...                 — 1.7 s, clean
make test    # go test -count=1 ./...       — 23 s, all packages ok, zero skips
```

**`make test` is not the CI command.** CI runs
`go test -count=1 -race ./...` (2 m 14 s locally). A data race passes
`make test` and reddens CI. Use the `-race` form before you claim green.

What this layer cannot tell you: anything involving nsjail, HTTP, the database
on disk, the UI, the CLI as a shipped binary, or arm64.

### 3.2 The Python E2E suite — the source of truth

28 modules under `test/e2e/tests/`, 652 static `check()` call sites (runtime
counts differ where loops live). Stdlib only.

#### 3.2.1 `run.py` — the real flag surface

`argparse` has no help strings, so `--help` is nearly useless. The complete
list:

| Flag | Effect |
|---|---|
| `--rebuild` | force `docker build` even if `orva:e2e` exists. Isolated mode only; ignored with `--url`. |
| `--keep` | skip teardown; prints the URL and the `docker rm -f` command |
| `--filter FILTER` | plain **substring** match on the filename — not a glob, not a regex. `--filter ai` matches all 7 `test_ai_*.py`. |
| `--url URL` | target an existing instance; **skips Docker entirely** (`env.py` is never imported) |
| `--api-key KEY` | key for `--url` mode; falls back to `resolve_admin_key()` |

There is no `--verbose`, `--timeout`, `--parallel`, or `--dry-run`.

> **Never run `python3 run.py --filter …` casually.** `main()` calls
> `write_checklist(results, …)` with only the filtered results, truncating the
> **committed** `test/e2e/CHECKLIST.md` to a partial record. To run one module,
> use §3.2.4 instead — that path never writes the checklist.

Modules run **strictly sequentially**, one subprocess at a time, 900 s hard
timeout each. That is load-bearing: the AI modules would trip the "one AI turn
per conversation" try-lock if run concurrently.

#### 3.2.2 The module protocol

Each module is its own process. `run.py` parses
`^RESULT pass=(\d+) fail=(\d+)( skip=(\d+))?$` against **the last non-blank
stdout line**, then classifies:

| Condition | Status | Injected detail |
|---|---|---|
| no trailer match | FAIL | `missing RESULT trailer` |
| `skip>0` + rc 3 + `fail==0` | **SKIP** | — |
| `skip>0` but rc≠3 or fail>0 | FAIL | `skip result requires exit 3 and zero failed checks` |
| rc 0 + `fail==0` + `pass>0` | **PASS** | — |
| rc 3 without `skip=1` | FAIL | `exit 3 requires RESULT skip=1` |
| rc 0 + `pass==0` | FAIL | `passing result requires at least one passed check` |

Three consequences worth internalizing:

- A line printed *after* the trailer invalidates it. So does prefixed text
  (`debug: RESULT …`).
- **Exit 2 has no specific message.** Every module starts with
  `if not c.key: return 2`, and 2 is not in the protocol, so a blank key falls
  through to `missing RESULT trailer`. Exit 2 is the most common real-world
  failure and the one the classifier names worst.
- `run.py`'s own exit codes: **0** all PASS/SKIP · **1** ≥1 FAIL · **2** setup
  failure (no docker, no modules, instance never came up).

The protocol itself is unit-tested — `test/e2e/unit/test_runner.py`, 11 tests,
run by CI *before* `make build`:

```bash
python3 -m unittest discover -s test/e2e/unit -p 'test_*.py'
# Ran 11 tests in 0.161s — OK
```

#### 3.2.3 `ORVA_REQUIRE_SANDBOX=1`

Read by exactly three modules — `test_deploy_invoke.py:17`,
`test_firewall.py:69`, `test_cli.py:154` — never by `run.py`, `harness.py`, or
`env.py` (it is simply inherited through the environment). The shared idiom:

```python
def sandbox_unavailable(reason):
    if REQUIRE_SANDBOX:
        check("sandbox invocation is available", False, reason)
        return summary()      # -> exit 1, module FAILs
    return skip(reason)       # -> exit 3, module SKIPs
```

It **adds no assertions**; it removes the escape hatch. Set it whenever the
target genuinely has nsjail and provisioned rootfs trees and you are claiming
the kernel boundary works. Do **not** set it for an API-only target or a host
that forbids nested namespaces — you will get failures that say nothing about
your change.

This matters because **a run whose sandbox modules all SKIP still exits 0**,
prints no warning, and lists nothing under `## Failure details`. The only
signal is the `- **Modules:** N passed, M failed, K skipped` line. That hole is
exactly what the flag plugs.

#### 3.2.4 The fast inner loop: one module, no CHECKLIST.md

`run.py` sets `PYTHONPATH` for its children. Run a module yourself and you must
too, because Python puts the *script's* directory (`tests/`) on `sys.path`, not
`test/e2e/`.

```bash
cd /home/dev/Orva/test/e2e
PYTHONPATH=/home/dev/Orva/test/e2e \
  ORVA_URL=http://localhost:3000 \
  ORVA_API_KEY="$(grep -o 'orva_[A-Za-z0-9_]*' ~/.orva/config.yaml | head -1)" \
  python3 tests/test_system.py
```

Add `ORVA_BIN=/abs/path/build/orva` for `test_cli.py` / `test_cli_chat.py`, and
`MOCK_HOST=127.0.0.1` (the default) for AI modules.

Real observed output:

```
== health ==      ✓ status healthy  ✓ has version  ✓ database ok
== metrics ==     ✓ metrics (prometheus) -> 200   ✓ metrics.json -> 200
== runtimes ==    ✓ runtimes listed
== storage (admin) ==  ✓ storage -> 200
ALL PASSED — 7 checks
RESULT pass=7 fail=0        EXIT=0
```

`test_functions.py` → `ALL PASSED — 13 checks` (13 runtime checks from 10 static
sites, via a 4-iteration legacy-runtime loop). `test_mcp.py` → `ALL PASSED — 51
checks`. `test_security.py` → `ALL PASSED — 8 checks`.
`git status --porcelain test/e2e/CHECKLIST.md` was empty after every one.

Two failure modes you will hit:

- missing `PYTHONPATH` → `ModuleNotFoundError: No module named 'harness'`, exit 1
- empty `ORVA_API_KEY` → `ORVA_API_KEY not set` on stderr, **exit 2**

#### 3.2.5 Safety classification — which modules must never touch a shared instance

Not documented anywhere else. Derived by reading every `finally` block, and
partially confirmed by before/after snapshots.

**Safe (read-only or name-scoped self-cleanup).** `test_system`, `test_auth`,
`test_traces`, `test_backup`, `test_functions`, `test_secrets`, `test_kv`,
`test_routes`, `test_cron`, `test_jobs`, `test_fixtures`, `test_channels`,
`test_inbound_webhooks`, `test_webhooks`, `test_keys`, `test_mcp`. Each cleans
up by literal `e2e-*` name.

**`test_security.py` is *best-effort* cleanup, not reliable cleanup.** It is
mostly read-only, but its M4 leg creates `e2e-sec-bigdeploy` and pushes a 7 MB
`deploy-inline` at it. The `finally` (lines 68-73) does delete the function —
inside `try: … except Exception: pass`, and **the return value of `c.delete()`
is discarded**. `c.delete()` returns a *status code* and never raises on 4xx,
so a delete the server refuses — plausibly because the 7 MB build is still in
flight when the module reaches its `finally`, though the module records nothing
that would tell you — is indistinguishable from a delete that worked. After a
**fully green** run — `ALL PASSED — 8 checks` — `e2e-sec-bigdeploy` was still on
the instance, status `error`. Treat it as leaving one function behind and sweep
by name afterwards.

**`test_ai_edit.py` is destructive to unrelated data.** Its `finally`
(lines 114-116) iterates `GET /api/v1/ai/conversations` and deletes **every
conversation on the instance**, not only the ones it created. Pointing it at a
real instance wipes the user's entire AI chat history. This directly violates
`test/e2e/CLAUDE.md`'s own rule.

**Conversation leaks.** `test_ai_chat`, `test_ai_advanced`, `test_ai_perms`,
`test_cli_chat` create conversations and contain **zero** conversation-delete
call sites. They clean up their functions and leave the conversation and
message rows.

**All 7 AI modules mutate the shared `ai_settings` singleton** (provider, model,
approval policy, `max_tool_iterations`). `remove_mock_provider()` restores a
snapshot on the normal path; a crash between configure and `finally` leaves the
instance pointed at a dead `http://127.0.0.1:11434/v1` provider. On an instance
holding a real provider key this also means real money, so use a throwaway.

**`test_firewall.py` writes into the data dir** — it hides the compiled NSTUN
policy to prove fail-closed. It is guarded well: `instance_files()` walks
candidate data dirs and **refuses any whose `.admin-key` does not byte-match the
key under test** ("A real Orva, but not the one under test. Never touch it."),
and its `finally` restores the policy file *first*, then rule states, then DNS.

**`test_cli.py`** ends with `orva functions delete --yes` and runs a real
deploy+invoke; it is safe by name but puts sandbox load and execution rows on
the target.

#### 3.2.6 `CHECKLIST.md`

A tracked file, regenerated by `write_checklist()` on every full run. It
carries a UTC `Last run`, the `Target`, module and check tallies, a
`✅ PASS / ❌ FAIL / ⚠️ SKIP` table, and a `## Failure details` section
listing FAILs only.

**Skips never appear under Failure details and never affect the exit code.**
The `Modules:` line is the only place to catch them.

The committed copy is stale: it records `2026-08-04`, `26 passed`,
`537 checks`, target `external instance http://127.0.0.1:18443` — but 28
modules exist on disk. `test_mcp.py` (51 checks) and `test_security.py`
(8 checks) have never appeared in a committed run even though both pass.
Do not read `CHECKLIST.md` as the coverage inventory; `ls test/e2e/tests/` is.

### 3.3 The mock LLM — testing the AI agent with no provider key

`test/e2e/mock_llm.py` is a `ThreadingHTTPServer` speaking OpenAI-compatible
*streaming* chat-completions. Because Bifrost lets a provider point at any
`base_url`, configuring an Orva `openai` provider at
`http://<MOCK_HOST>:<port>/v1` makes the **real** agentic loop — approval gate,
in-process tool dispatch against the real instance, result feed-back, final
answer — run deterministically.

The conversation content is the program. Dispatch precedence:

1. Any message with `role == "tool"` in history wins over everything → final
   answer `"Done. The tool ran and returned a result."`, `finish_reason: stop`.
   This is what terminates the loop.
2. Otherwise, on the **last** user message:
   - `CALL2 <toolA> <argsA> || <toolB> <argsB>` → two tool_calls in one turn,
     index 0/1, ids `call_0`/`call_1`
   - `CALL <tool> <json-args>` → one tool_call (first space splits name from
     args; the rest of the line is passed through verbatim as `arguments`)
   - anything else → a plain text reply

Tool calls are emitted in **two chunks each** (open, then an arguments delta),
deliberately exercising the client's accumulation path. Text is chunked at 24
characters. All of the above was verified by curling the mock directly.

```bash
cd test/e2e && python3 mock_llm.py 11500
curl -s http://127.0.0.1:11500/v1/models   # {"object":"list","data":[{"id":"gpt-4o",…}]}
```

Two things to know when wiring it up:

- `run.py` sets `MOCK_BIND=0.0.0.0` **unconditionally**, including in `--url`
  local mode where loopback would suffice.
- In `--url` mode `MOCK_HOST` is hardcoded to `127.0.0.1`, overwriting whatever
  you set. Pointing `--url` at a **remote** instance therefore configures a
  provider at that box's loopback, and the AI modules fail in a way that looks
  like a provider bug. There is no flag to override it.

### 3.4 The shell suites

Sixteen scripts under `test/`, all of which run against an **already-running**
instance (only `loadtest.sh` starts its own, destructively). **None of them are
executed by CI** — CI only `shellcheck`s them (`ci.yml:197-205`). A
syntax/quoting regression *will* redden CI; a behavioral one will not.

Default `BASE_URL` is **18443**, so on any other port you must export it.

```bash
export BASE_URL=http://127.0.0.1:18443            # a scratch instance (§1.1)
export API_KEY=$(cat /tmp/orva-scratch/.admin-key)
```

> **Point these at a throwaway instance, not the one you actually use.** Unlike
> the CLI, every suite here honors `BASE_URL`/`API_KEY` (except
> `tracing-test.sh`, which reads a different pair — below), so retargeting costs
> nothing. Three of them are not read-only. `atscale.sh` contains **zero
> `DELETE` calls** and leaves 20 functions behind permanently — verify it
> yourself in one second, `grep -c DELETE test/atscale.sh` → `0` — and it is a
> **member of `run-all.sh`**, so the umbrella inherits that litter. `sdk-test.sh`
> leaves `sdk-test-noop` by design. `egress-test.sh` mutates the **global**
> `egress_blocklist` mid-run and retires every warm egress pool on the instance.
> If you only have a live instance, read the `run-all.sh` detail below before
> running the umbrella at all.

All rows below were measured against a live instance:

| Script | Proves | Measured | Verdict line | Self-clean |
|---|---|---|---|---|
| `api-smoke.sh` | 20 status-family checks across system/auth/functions/deploy/invoke/deployments/secrets/keys/routes | **0.45 s** | `=== api-smoke: 20 passed, 0 failed ===` | yes (route dies by FK cascade) |
| `auth-test.sh` | per-function `auth_mode` none/platform_key/signed (incl. tampered body + stale timestamp) and `rate_limit_per_min` → 429 | **0.80 s** | `auth-test: pass=11 fail=0` | yes |
| `rollback-test.sh` | deploy A→B, rollback row shape (`source=rollback`, `parent_deployment_id`), code reverts, roll-forward, no-op rollback → 400 | **4.6 s** | `rollback-test  pass=9  fail=0` | yes |
| `routes-test.sh` | exact route, `/prefix/*` rewriting, reserved-prefix rejection, method filter 405/200, direct invoke coexists | **2.95 s** | `routes-test  pass=6  fail=0` (7 with `hey`) | yes |
| `secrets-test.sh` | secret round-trip, values visible as env inside the sandbox, redeploy drops a deleted secret | **4.8 s** | `secrets-test  pass=6  fail=0` (8 with `hey`+`sqlite3`) | yes |
| `egress-test.sh` | `none` blocked → `egress` reaches example.com → back to `none` re-isolates; a hostname blocklist rule genuinely REJECTs and deleting it restores reachability | **11.5 s** | `=== egress-test: 15 passed, 0 failed ===` (19 with `ORVA_CONTAINER`) | yes — `trap cleanup EXIT` |
| `errors-test.sh` | 8 error contracts: 413, 502 `WORKER_CRASHED`, 504 `TIMEOUT`, 404, 405, `POOL_AT_CAPACITY`, 409 `NOT_ACTIVE`, 400 on `status=building` | **6.0 s** | `errors-test  pass=8  fail=0` | yes |
| `build-cache-test.sh` | per-function dep-cache lifecycle: cold build, warm rebuild, purge → 200, idempotent purge, purge refused for non-functions, deploy-after-purge | **6.5 s** (warm npm) | `build-cache: 10 passed, 0 failed` | yes |
| `heavy-deploy-test.sh` | async build queue: POST returns <500 ms with a `deployment_id`, real pip build, SSE stream emits, garbage requirements → `failed` while the function stays `active` | **14.3 s** (warm pip) | `heavy-deploy-test  pass=12  fail=0` | yes, but writes a log file (§9) |
| `tracing-test.sh` | http root span + `X-Trace-Id`, F2F parent linkage, W3C `traceparent` honored, replay = fresh trace, outlier + `baseline_p95_ms` | **10.2 s** | ANSI `✓`/`✗` then `PASS: 10` / `FAIL: 0` | yes — trap + pre-run sweep |
| `sdk-test.sh` | Python `kv.incr`/`kv.cas`/`trace.span`/`log.info`; Node `kv.putMany`/cursor `kv.list`/`kv.getMany`/`jobs.enqueue` idempotency | **3.2 s** | `sdk-test: PASS=13 FAIL=0` | **no** — leaves `sdk-test-noop` by design |
| `onboarding-flow.sh` | on a **virgin DB only**: onboard → session cookie → `/auth/me` → session auth → 409 re-onboard → refresh rotates → old token revoked → logout invalidates | **0.03 s** (skip path) · **0.28 s** for all 13 checks on a virgin DB | `skip  (users already exist…)` · on a virgin DB `onboarding-flow  pass=13  fail=0` | leaves its user |
| `run-all.sh` | umbrella over 11 suites | **49.9 s** | `test/run-all-results.tsv`; exit 1 if any row is `fail` | inherits — **including `atscale.sh`'s 20 permanent functions** |
| `atscale.sh` | **nothing is asserted** — deploys 20 fns and dumps metrics TSV | exit 2 without `hey` | none | **no — leaves 20 functions forever** |
| `ceiling.sh` | sustained-load ramp, emits CSV | exit 2 without `hey` | CSV is the product; **exit 0 always** | n/a |
| `loadtest.sh` | intends 6 fixture deploys + phases A–G | not run — destructive | none | destroys `~/.orva/orva.db*` |

**Two suites are `hey`-gated and it is not installed on the survey host**, so
`atscale.sh` and `ceiling.sh` were **`[UNVERIFIED]` past their guard**: their
real durations, their TSV/CSV content, and the claim that `atscale.sh` leaves 20
functions behind (read from the file — it contains zero `DELETE` calls) were
never confirmed by execution.

**Exit-code discipline.** `rollback-test.sh`, `routes-test.sh`,
`secrets-test.sh`, `errors-test.sh`, `heavy-deploy-test.sh` and
`onboarding-flow.sh` use `exit $FAIL` — **the exit code is the failure count.**
Never compare to 1; compare to 0.

**`sdk-test.sh`'s exit code is meaningless.** Its verdict lives in
`trap 'echo …; [ "$FAIL" -eq 0 ]' EXIT`, and bash does not propagate an EXIT
trap's last-command status to the script's exit code. Reproduced in isolation
(`FAIL=1` in the trap → script exits 0) and in situ (`PASS=0 FAIL=15` exited
**7**, the status of the last cleanup `curl`). Parse the
`sdk-test: PASS=n FAIL=m` line instead.

#### `run-all.sh` in detail

> **Run this against a scratch instance.** Its last member is `atscale.sh`,
> which deploys `ascale-node-1..10` + `ascale-py-1..10` and **never deletes
> them** (§9). On a host without `hey` that never happens — `atscale.sh` exits 2
> at its guard before deploying anything, which is exactly what the artifact
> below shows and exactly what has been masking this. Install `hey` and the same
> `bash test/run-all.sh` permanently litters whatever `BASE_URL` names.
> `egress-test.sh` additionally mutates the global `egress_blocklist` while it
> runs. Neither is a reason to skip the umbrella — it is a reason to give it a
> throwaway target.

Runs, in order: `secrets`, `routes`, `heavy-deploy`, **`build-cache`**,
`onboarding-flow`, `errors`, `rollback`, `egress`, `auth`, `tracing`,
`atscale` — 11 suites. (`test/CLAUDE.md` and `docs/CONTRIBUTING.md` both omit
`build-cache-test.sh` from that list.) Deliberately excluded: `api-smoke.sh`
(pure overlap), `sdk-test.sh` (different env contract and an unreliable exit
code), `loadtest.sh` (structurally incompatible — it kills whatever holds 8443
and deletes `~/.orva/orva.db*`), `ceiling.sh` (needs two positional args the
umbrella cannot supply).

Mechanics: `set -uo pipefail` (deliberately not `-e`), each child captured, the
verdict taken from the child's exit status, and column 3 is **the last line of
the child's output** — which frequently contains tabs, so rows have 3–5 fields.
**Column 2 ∈ {pass, fail} is the only stable field.**

A real artifact, from `BASE_URL=http://localhost:3000 bash test/run-all.sh`
(exit 1, 49.9 s), tabs shown as `|`:

```
secrets-test.sh|pass|secrets-test|pass=6|fail=0
routes-test.sh|pass|routes-test|pass=6|fail=0
heavy-deploy-test.sh|pass|heavy-deploy-test|pass=12|fail=0
build-cache-test.sh|pass|build-cache: 10 passed, 0 failed
onboarding-flow.sh|pass|skip|(users already exist; cannot test the onboarding bootstrap)
errors-test.sh|pass|errors-test|pass=8|fail=0
rollback-test.sh|pass|rollback-test|pass=9|fail=0
egress-test.sh|pass|=== egress-test: 15 passed, 0 failed ===
auth-test.sh|pass|auth-test: pass=11 fail=0
tracing-test.sh|fail|curl: (7) Failed to connect to localhost port 8443 after 0 ms
atscale.sh|fail|hey is required (github.com/rakyll/hey)
```

That artifact exposes both structural traps at once (and, in row 11, the `hey`
guard that is the only thing standing between this umbrella and 20 permanent
functions on `localhost:3000`):

- **`onboarding-flow.sh` recorded `pass` while asserting nothing.** It exits 0
  with `skip` on any instance that already has a user — which is every real
  instance. Only a virgin DB exercises its 13 real checks, and it costs exactly
  one extra `./build/orva serve` to get one:

  ```bash
  mkdir -p /tmp/orva-virgin/rootfs
  ln -s /var/lib/orva/rootfs/node   /tmp/orva-virgin/rootfs/node
  ln -s /var/lib/orva/rootfs/python /tmp/orva-virgin/rootfs/python
  ORVA_DATA_DIR=/tmp/orva-virgin ORVA_PORT=18444 ./build/orva serve &
  BASE_URL=http://127.0.0.1:18444 bash test/onboarding-flow.sh
  ```

  All 13 pass — measured, not inferred (**0.28 s**, exit 0):

  ```
  ok	/api/v1/auth/status returns has_user=false on fresh DB
  ok	/api/v1/auth/onboard → 200
  ok	session_token cookie set
  ok	cookie ~7d expiry
  ok	/api/v1/auth/me returns user
  ok	/api/v1/auth/me returns expires_at
  ok	/api/v1/auth/status returns has_user=true after onboard
  ok	session auth grants /api/v1/* access
  ok	second /api/v1/auth/onboard → 409
  ok	/api/v1/auth/refresh → 200
  ok	refresh issued a different token
  ok	old token revoked after refresh
  ok	logout invalidates session

  onboarding-flow	pass=13	fail=0
  ```

  The script is still single-shot: it never deletes the user it creates, so the
  second run against that same data dir takes the `skip` path. Throw the data
  dir away and start over. The `/web/onboarding` **UI** leg (§4.16) is a
  separate, still-`[UNVERIFIED]` claim — this covers the `/auth/*` backend only.
- **`tracing-test.sh` recorded `fail` for targeting the wrong port.** It reads
  `ORVA_ENDPOINT`/`ORVA_API_KEY` (default 8443) and does **not** read
  `BASE_URL`/`API_KEY` at all, while `run-all.sh` exports only the latter pair.
  Inside the umbrella it always targets 8443. Worse than a hard failure: on a
  host that *does* have something on 8443, it silently tests a different
  instance while reporting pass. Run it standalone — and pass **both** vars:
  with `ORVA_ENDPOINT` set but `ORVA_API_KEY` unset it falls back to
  `~/.orva/config.yaml`'s key, which authenticates against *that* endpoint, not
  yours, and the run dies in 0.07 s with `could not create function
  trace_chain_c` — a message that never mentions auth.

```bash
ORVA_ENDPOINT=$BASE_URL ORVA_API_KEY=$API_KEY bash test/tracing-test.sh  # PASS: 10 / FAIL: 0
ORVA_ENDPOINT=$BASE_URL ORVA_API_KEY=$API_KEY bash test/sdk-test.sh      # then delete sdk-test-noop
```

**`run-all.sh` cannot go green on a host without `hey`** — `atscale.sh` exits 2
and the umbrella records `fail`.

#### What the shell suites cannot tell you

They have **no nsjail-availability skip anywhere**. The only `skip` paths are
egress fail-closed (needs `ORVA_CONTAINER`), onboarding (users exist), the
secrets DB sanity check (no `sqlite3`), and the errors `POOL_AT_CAPACITY` step
(pool PUT rejected). On a host without nsjail they simply go red — unlike
`test/e2e/`, which has a real skip protocol. That claim is inferred from the
absence of any nsjail probe in the code, **`[UNVERIFIED]`** by running on an
nsjail-less host.

Several suites use **fixed, non-unique resource names**, so concurrent runs on
one instance collide and an interrupted run leaves squatters:
`routes-test.sh` (`/webhooks/stripe`, `/customer/*`, `/restricted/post-only`),
`errors-test.sh` (`/errs/method-only`), `tracing-test.sh`
(`trace_chain_a/b/c`, `trace_outlier`), `sdk-test.sh` (`sdk-test-noop`),
`atscale.sh` (`ascale-*`), and `egress-test.sh`'s firewall rule (`example.com`).
Everything else uses `$$`.

`egress-test.sh` mutates the **global** `egress_blocklist` and republishes the
policy generation, retiring every warm egress pool on the instance for under a
minute. Fine on a test instance; it is not read-only.

After all 12 runnable suites plus a full `run-all.sh`, a before/after snapshot
of the live instance's functions, routes, keys, firewall rules and jobs came
back identical, and the content-addressed `policy_generation` returned to the
same hash.

### 3.5 Install, CLI, and release harnesses

These test the **shipped artifact**, not your branch. `test/install/native-engine.sh:107`
says so in a comment: *"This script installs the PUBLISHED binary, so it cannot
gate a merge."*

| Harness | Tests | Safe on a dev machine? |
|---|---|---|
| `test/install/run-distro.sh <distro>` | `install.sh` → systemd/OpenRC unit → smoke flow → uninstall → reinstall (data survives) → `--purge`, per distro | **Yes but privileged + slow.** Everything happens inside `--privileged` containers, trap-torn-down. Verified: `ORVA_BROWSER_LEG=0 bash test/install/run-distro.sh ubuntu24` → exit 0, ~4 min including image pull, host port 19449. |
| `test/install/matrix.sh` | unprivileged installer checks: shellcheck, `sh -n` under each distro's own `/bin/sh`, `--help`, two `--dry-run`s, bad-flag rejection | **Yes — the only genuinely dev-safe script in that directory.** `passed=31  failed=0` over its full default image list; that number is arithmetic, not a constant — see below. Its own header claims the opposite (§9). |
| `test/install/native-engine.sh` | installs Orva **on the host** and deploys+invokes Node and Python with no advisory path | **NO. Never run this on a machine you care about.** It runs `install.sh --bare-metal --yes --start` as root, regenerates `/etc/systemd/system/orva.service` (dropping any hand-added `Environment=`), restarts the unit, and writes into the live data dir. `[UNVERIFIED]` — deliberately not executed. |
| `test/cli/build-matrix.sh` | 6-target cross-build + size ceiling | Yes. `=== build-matrix: 19 passed, 0 failed ===`; 14–16 MB per target |
| `test/cli/command-tree.sh` | slim CLI ↔ server command-surface parity | Yes. 17.5 s, `slim CLI commands: 107` == `server CLI commands: 107` |
| `test/cli/install-cli-test.sh [distro]` | `install-cli.sh` download + checksum + install of the **published** CLI | Yes, unprivileged Docker. 26.5 s, 19 passed / 0 failed, installed `orva v2026.08.05`, binary **21 MB** |
| `test/cli/upgrade-test.sh` | `orva upgrade` round-trip from the previous release | Yes — but **currently vacuous**, see below |
| `test/release/download-verified-asset.sh <asset> <dest>` | fetch a release asset and SHA-256 it against that release's `checksums.txt`, no fail-open | Yes, read-only. `verified release asset install-cli.sh from v2026.08.05` |
| `test/kata-bench/*` | Orva under kata-qemu / kata-clh vs runc | `[UNVERIFIED]` — no kata runtime registered, `hey` missing, and the scripts' pinned image tag no longer exists (§9) |

**`matrix.sh`'s check count is a formula, not a number.** It is
`1 + 5 × (images that run)`: one distro-independent `shellcheck` pass, then per
image `sh -n` / `--help` / `--dry-run --bare-metal` / `--dry-run --cli-only` /
bad-flag-rejected. The image list is **hardcoded in `matrix.sh`**, not read from
`distros.tsv` — six entries (`debian:stable-slim`, `ubuntu:24.04`,
`alpine:latest`, `fedora:latest`, `almalinux:9`, `archlinux:latest`) — and any
positional arguments replace it wholesale. So the full default list gives
`1 + 5×6 = 31`, and a three-image subset gives `1 + 5×3 = 16`. Do not treat
either as the pass criterion; the criterion is `failed=0`. Two wrinkles:
`REAL_CLI=1` adds a sixth per-image check (a real `--cli-only` install with
checksum verification), making it `1 + 6×images`; and an image whose *first*
check fails takes a `continue`, contributing **one failure and zero passes**
rather than five failures.

Wall-clock is dominated by image pulls, not by the checks: **69.6 s** on one run
and **18.6 s** on a repeat with all six images already in the local Docker
cache. Observed on the repeat:

```
== summary ==
passed=31  failed=0
```

**Two size-ceiling facts belong together.** `CLI_SIZE_LIMIT_MB=28` is shared by
`build-matrix.sh` (locally built with `-s -w` → 14–16 MB) and
`install-cli-test.sh` (the *published* binary → 21 MB measured). Real headroom
against the shipped artifact is ~7 MB, not ~13 MB: a dependency that adds 8 MB
passes `build-matrix` and fails `install-cli-test`.

**`upgrade-test.sh` is a no-op right now.** The repo has exactly one published
release, so the "previous release" resolve yields nothing and the script exits 0
in 0.47 s with a warn. The `cli-upgrade` CI job's 18 s "success" is that skip.
**A green `cli-upgrade` is currently zero evidence that the upgrader works.**

**Local-vs-CI image divergence:** `install-cli-test.sh` derives its base image
from `test/install/distros.tsv` unless `ORVA_CLI_TEST_IMAGE` is set. For
`ubuntu24` that is `jrei/systemd-ubuntu:24.04` locally while CI uses
`ubuntu:24.04`. To reproduce CI exactly:

```bash
ORVA_CLI_TEST_IMAGE=ubuntu:24.04 bash test/cli/install-cli-test.sh ubuntu24
```

**What a green `install-matrix` does not prove.** `smoke-flow.sh` downgrades
`WORKER_CRASHED`, `SANDBOX_ERROR`, `NOT_ACTIVE`, and `secrets set`/`kv put`
failures to `warn` + a **PASS increment**, because nsjail's
`mount("/","/",MS_PRIVATE)` returns EACCES inside systemd-in-docker. In the
verified ubuntu24 run, `orva secrets set` and `orva kv put` both failed and the
job still passed. So:

- `install-matrix` green + `native-engine` red = a real invoke/build regression
- `install-matrix` red = an install/packaging/service regression
- "smoke flow passed" never means "invocation works"

`ORVA_REQUIRE_SANDBOX` is honored **only** by the three `test/e2e/` modules — no
install, CLI, or kata harness reads it.

---

## 4. Real user journeys

This is the part that matters. Each journey below was executed end to end; the
outputs are transcripts, not illustrations. `$B` is the base URL and `$K` an
admin key throughout.

> **⚠ Read this before you run a single command in this section.**
>
> **The `orva` CLI does not read `$B` or `$K`.** It reads
> `~/.orva/config.yaml`, which on a developer machine points at the instance you
> actually use. Exporting `B` and `K` protects the `curl` lines in this section
> and **nothing else**. Followed literally with bare `orva …`, §4.7 plants a
> `* * * * *` cron that fires forever and §4.11 mutates the **global**
> `egress_blocklist` — on your real instance.
>
> So every CLI line below carries the flags explicitly:
>
> ```bash
> orva --endpoint "$B" --api-key "$K" <command>
> ```
>
> Two things that make this less safe than it looks, both verified:
>
> - **Empty values fall back silently.** `orva --endpoint "" --api-key ""
>   functions list` does not error — it lists the functions of the instance in
>   `~/.orva/config.yaml` and exits 0. So the flags only protect you when `B` and
>   `K` are set **in the same shell that runs the command**. If your shell (or
>   your agent harness) does not carry environment between commands, substitute
>   the literal URL and key instead of trusting the variables.
> - **An alias or a wrapper function has the same hole**, for the same reason:
>   it lives in one shell. That is why the flags are spelled out on every line
>   here rather than hidden behind one definition.
>
> Bring up a scratch target rather than aiming at anything you care about
> (§1.1 — §4.1's transcript, §2.6(c)'s key output, §4.5's secrets round-trip and
> §4.7/§4.11's cron and firewall commands were all re-run on exactly this shape):
>
> ```bash
> mkdir -p /tmp/orva-doccheck/rootfs
> ln -s /var/lib/orva/rootfs/node   /tmp/orva-doccheck/rootfs/node
> ln -s /var/lib/orva/rootfs/python /tmp/orva-doccheck/rootfs/python
> ORVA_DATA_DIR=/tmp/orva-doccheck ORVA_PORT=18446 ./build/orva serve &
>
> export B=http://127.0.0.1:18446
> export K=$(cat /tmp/orva-doccheck/.admin-key)
> ```
>
> Kill that server and `rm -rf /tmp/orva-doccheck` when you are done.
>
> Confirm what you are pointed at **before the first mutation**. No CLI command
> echoes its endpoint, so check by content: on a fresh scratch instance
> `orva --endpoint "$B" --api-key "$K" functions list` shows an empty list,
> while your real instance shows your functions.

### 4.1 Deploy, invoke, logs, executions

```bash
mkdir demo && cat > demo/handler.js <<'EOF'
exports.handler = async (event) => ({ ok: true, echo: JSON.parse(event.body || '{}') });
EOF
orva --endpoint "$B" --api-key "$K" deploy ./demo --name demo-node --runtime node --follow
orva --endpoint "$B" --api-key "$K" invoke demo-node --body '{"hello":"world"}'
orva --endpoint "$B" --api-key "$K" logs demo-node
orva --endpoint "$B" --api-key "$K" executions list --function demo-node
```

`deploy --follow` on a not-yet-existing function prints **six** status lines and
then a JSON block — reproduced verbatim, nothing elided:

```
Function "demo-node" not found, creating...
Created function 019fed93-2c46-734c-9df2-1c1a3f480a0f
Deploying to function 019fed93-2c46-734c-9df2-1c1a3f480a0f...
Streaming build logs for deployment 019fed93-2c49-7cab-90ec-e5897003e0fe — Ctrl-C to stop.
Build succeeded.
Deploy submitted (deployment 019fed93-2c49-7cab-90ec-e5897003e0fe)
{
  "deployment_id": "019fed93-2c49-7cab-90ec-e5897003e0fe",
  "function_id": "019fed93-2c46-734c-9df2-1c1a3f480a0f",
  "status": "queued"
}
```

Two traps in that tail. `Deploy submitted` and `"status": "queued"` are printed
**after** `Build succeeded.` — they describe the submission, not the outcome, so
a `grep -q queued` says nothing about whether the build worked. And the status
lines go to **stderr** while the JSON goes to stdout, so a bare
`orva … deploy … --follow | jq .status` sees only the JSON block. Exit code is
the verdict: 0 on success, 1 on build failure (§4.4).

Then `invoke`:

```
POST demo-node · 200 · 84ms
{"ok":true,"echo":{"hello":"world"}}
```

(The response body has **no trailing newline**, so the next shell prompt lands
on the same line.)

The headers are where the interesting assertions live — a selection, CORS and
`Content-Type`/`Date`/`Content-Length` elided:

```
HTTP/1.1 200 OK
X-Orva-Cold-Start: false
X-Orva-Duration-Ms: 1
X-Orva-Execution-Id: 019fed93-52e1-70b4-a5c1-a2f617557ce8
X-Request-Id: 019fed93-52e0-7a56-8d01-ba13477cc66e
X-Trace-Id: tr_3907e03a6efd0685ad654a51e2795b53
```

**Handler contract gotchas, all confirmed live:**

- `event.body` is the **raw string**, never parsed. Forget `JSON.parse` and you
  get `{"echo":"{\"via\":\"curl\"}"}`.
- The return value **is** the JSON body. `{status, body}` is not unwrapped. For
  a non-200 use the AWS shape `{statusCode, headers, body}` — returning
  `statusCode: 418` produced `HTTP 418` with body `{"teapot":true}`.
- The CLI flag is `--body`; `--data` does not exist.
- `orva deploy` takes a **directory**, not a file.

**Endpoint shapes — two of these are easy to get wrong:**

| Want | Endpoint |
|---|---|
| executions for a function | `GET /api/v1/executions?function_id=<uuid>&limit=N` — **`/functions/{id}/executions` is 404** |
| one execution | `GET /api/v1/executions/{exec_id}` |
| handler stderr/stdout | `GET /api/v1/executions/{exec_id}/logs` |
| the captured request | `GET /api/v1/executions/{exec_id}/request` |
| the **build** log | `GET /api/v1/deployments/{deployment_id}/logs` — not an executions route |

**Log capture surprise.** Handler `stdout` is the frame-protocol channel, so
both adapters reroute user output to stderr
(`runtimes/python/adapter.py:40 sys.stdout = sys.stderr`). A python handler that
prints to both yields:

```json
{"execution_id":"…","log_entries":[],
 "stderr":"qa-py stdout log line\nqa-py stderr log line\n","stdout":""}
```

`stdout` is **always empty**. The dashboard drawer is honest about it:
*"Stderr (stdout is the response body, not stored)"*.

### 4.2 Dependency installs, and proving they ran in the jail

```bash
echo '{"name":"d","type":"module","dependencies":{"nanoid":"5.0.7"}}' > demo/package.json
orva --endpoint "$B" --api-key "$K" deploy ./demo --name dep-node --runtime node --follow
```

The build log streams `added 1 package in 735ms` and is retrievable at
`GET /api/v1/deployments/<id>/logs`. Invoke returns
`{"ok":true,"id":"q0ff_YDRRE2sziCMFZVa9","dep":"nanoid@5.0.7"}`.

Three independent proofs the install ran **inside nsjail**:

1. The server log — the only place that states it outright:
   ```json
   {"level":"INFO","msg":"build step starting","cmd":"/usr/local/bin/npm",
    "language":"node","network_mode":"egress","egress_policy_gen":"c67d3c1e47612fe3"}
   ```
2. A per-function build cache materializes at
   `$ORVA_DATA_DIR/build-cache/<fnID>/{npm,pip}` with `npm/_cacache` populated.
   Per-function by design — a shared cache would be a poisoning vector.
3. Failure text names the in-jail path:
   `A complete log of this run can be found in: /tmp/npm-logs/…` — `/tmp` is the
   jail's throwaway tmpfs.

**pip runs `--only-binary=:all: --quiet`, so a successful pip install emits NO
build log**: `GET /api/v1/deployments/<id>/logs` → `{"logs": null}`. Do not
assert on pip build-log content. Assert that `import <pkg>` works at invoke
time — verified with `idna==3.7` →
`{"ok": true, "idna_version": "3.7", "encoded": "xn--bcher-kva.de"}`.

A function with no dep file runs no build step at all
(`build succeeded … duration_ms: 5`).

### 4.3 TypeScript

```bash
# dir contains tsconfig.json + handler.ts + package.json{devDependencies:{typescript:"5.5.4"}}
orva --endpoint "$B" --api-key "$K" deploy ./ts --name ts-fn --runtime node --follow
```

```
Detected TypeScript project, using entrypoint "handler.ts"
added 1 package in 2s
compiled to dist/handler.js
Build succeeded.
```

Post-deploy `GET /api/v1/functions/{id}` reports `entrypoint = dist/handler.js`
with `runtime = node`. On disk, `<dataDir>/functions/<id>/current/` holds
`handler.ts`, `tsconfig.json`, `node_modules/typescript`, and `dist/handler.js`.
Redeploy keeps the rewritten entrypoint — the validator checks the source `.ts`
(CONTRACT §9, confirmed).

### 4.4 Break a deploy, roll back, verify

A failed deploy must leave the previous version serving. Verified with a
nonexistent npm package:

```
npm error 404 Not Found - GET https://registry.npmjs.org/orva-nonexistent-package-xyz-999
Error: build failed: install dependencies: npm install failed: exit status 1
invoke -> {"version":"v1-good"}      # still the last good build
orva deploy --follow exit code = 1   # 0 on success — CI-safe
```

`version` does **not** increment on a failed deploy — it is 1 at create plus 1
per *successful* deploy. The failed deployment row carries an empty `code_hash`.

```bash
orva --endpoint "$B" --api-key "$K" deployments list qa-rollback
# ID                     STATUS     PHASE  CODE_HASH     CREATED              DURATION
# 019fed55-b464-…        succeeded  done   16c7772ee6e7  2026-08-10 20:20:44  2ms
# 019fed55-4c83-…        failed     done   -             2026-08-10 20:20:18  1172ms
# 019fed55-2bdc-…        succeeded  done   0feb690b6949  2026-08-10 20:20:09  7ms

orva --endpoint "$B" --api-key "$K" rollback qa-rollback 019fed55-2bdc-765e-b636-6b7d4a551b52 --yes
# rolled "qa-rollback" back to 0feb690b6949 (deployment 019fed55-c8e2-…, version 4)
orva --endpoint "$B" --api-key "$K" invoke qa-rollback --body '{}'   # {"version":"v1-good"}
orva --endpoint "$B" --api-key "$K" diff qa-rollback
# -export default async function handler(event) { return { version: "v2-good" }; }
# +export default async function handler(event) { return { version: "v1-good" }; }
```

A rollback creates a **new** deployment row reusing the old `code_hash` —
history is append-only. `orva rollback <fn>` with no id means "undo the last
code change"; `--code-hash` pins a content hash. **Secrets are not versioned**
and keep current values.

The same ground is covered by `test/rollback-test.sh` (9 checks, 4.6 s), which
additionally asserts `source=rollback` and `parent_deployment_id` on the row and
that a no-op rollback returns 400 VALIDATION.

### 4.5 Secrets

```bash
orva --endpoint "$B" --api-key "$K" secrets set <fn> QA_SECRET --value "s3cr3t-value-2026"   # --value, NOT positional
orva --endpoint "$B" --api-key "$K" secrets list <fn>
curl -H "X-Orva-API-Key: $K" $B/api/v1/functions/$FID/secrets   # {"secrets":["QA_SECRET"]}
```

A handler reading `process.env.QA_SECRET` returned the value — secrets reach the
sandbox — and the API only ever returns key names. Write-only, AES-256-GCM at
rest under `<dataDir>/.master.key`.

Setting or deleting a secret drains the warm pool, so the next invoke is a cold
start. Budget for that in timing assertions.

**`ORVA_INTERNAL_TOKEN` is visible to user code.** A handler printing
`process.env` sees it. It is the SDK's credential for `/api/v1/_kv/` and
`/api/v1/_internal/`, and the auth middleware accepts it on `/api/v1/*` when it
matches exactly — so a function can reach the control plane as an authenticated
principal. That is a design fact, not a regression, but a QA suite should assert
the blast radius rather than assume isolation.

### 4.6 Custom routes

```bash
orva --endpoint "$B" --api-key "$K" routes set /qa-route/hello --fn <fn> --methods GET,POST
curl -X POST -d '{"via":"route"}' $B/qa-route/hello     # 200, no API key needed
```

Custom routes bypass the API-key middleware entirely — per-function `auth_mode`
is the only gate. With `auth_mode=platform_key` the same route returned **401**.
Wrong method → **405**. Unmapped sibling path → **404**.

### 4.7 Cron

```bash
orva --endpoint "$B" --api-key "$K" cron create --fn <fn> --expr '* * * * *' --payload '{"src":"cron"}'
# Created cron schedule 019fed59-ea18-703e-9c80-bd5f4c40f429 (* * * * *)
orva --endpoint "$B" --api-key "$K" cron list
# … UTC  true  NEXT RUN 2026-08-10 20:26:00  LAST STATUS ok
```

After it fires, the execution row carries `"trigger":"cron"`. The scheduler tick
is 30 s, so allow ~90 s for a `* * * * *` schedule before declaring failure.

**Always clean up a `* * * * *` cron** — it keeps invoking forever, and nothing
in the platform expires it:

```bash
orva --endpoint "$B" --api-key "$K" cron delete <schedule-id> --yes
# Deleted cron schedule 019fed95-ead9-7ac8-a00a-1c048c5d2c17
```

### 4.8 Background jobs

From inside a handler: `await jobs.enqueue('<fn>', {…})` →
`{"id":"019fed59-…","replayed":false}`.

```
orva --endpoint "$B" --api-key "$K" jobs get <id>
  status      succeeded
  attempts    1/3
  payload     "eyJmcm9tIjoicWEtZjJmIn0="      # base64
```

The execution row carries `"trigger":"job"`. Jobs tick every 5 s with
concurrency 8. CLI: `orva jobs enqueue --fn X --data '…' [--at RFC3339]
[--idempotency-key K] [--max-attempts N]`.

### 4.9 KV — and the SDK's two hard prerequisites

```js
const { kv, context, log } = require('orva');
exports.handler = async () => {
  const n = await kv.incr('qa-counter', 1);
  await kv.put('qa-last', { n });                 // put, NOT set
  return { counter: n, back: await kv.get('qa-last'),
           keys: (await kv.list({ prefix: 'qa-' })).keys };
};
```

→ `{"ok":true,"counter":3,"roundtrip":{"n":3},"keys":["qa-counter","qa-last"],"sdk":"0.6.0"}`

Operator view: `orva … kv list <fn>`, `orva … kv get <fn> qa-counter` (the `…`
is the `--endpoint`/`--api-key` pair, as everywhere in this section), and
`GET /api/v1/functions/{id}/kv` → entries with `value` and `size_bytes`.

**Two failure modes worth their own test cases, both hit during the survey:**

1. **`network_mode` must be `egress`.** The SDK reaches orvad over HTTP. With
   the default `none`:
   `HTTP 500 {"error":"Internal function error","message":"request failed: fetch failed"}`.
   The builder emits `builder.SDKNoneWarning` on such a deploy (it predicts
   `ENETUNREACH`; the observed text is `fetch failed` — recognizable, not
   greppable). Fix with
   `PUT /api/v1/functions/{id} {"network_mode":"egress"}` and expect a cold
   start on the next invoke.
2. **The bundled `orva` module is CommonJS-only; ESM handlers cannot import
   it.** `runtimes/node/adapter.js:30-33` patches `Module._nodeModulePaths`,
   which is the **CJS** resolver only — Node's ESM loader ignores it. Verified:
   `import { kv } from 'orva'` dies at load with
   `Cannot find package 'orva' imported from /code/handler.js`, surfaced as
   **502 `WORKER_CRASHED`**. `require('orva')` works. Meanwhile
   `builder/sdk_scan.go` recognizes `from 'orva'` as a valid SDK import and only
   warns about `network_mode`, so the platform tells you the ESM import is fine
   when it can never resolve.

The API is `kv.{get,put,delete,list,incr,cas}` plus `get_many`/`put_many`/
`delete_many` in Python. **There is no `kv.set`** — calling it gives
`kv.set is not a function` → 500.

### 4.10 Function to function

```js
const res = await invoke('callee-fn', { from: 'f2f' });   // 2nd arg is the PAYLOAD
return { callee_status: res.status ?? res.statusCode, depth: context.callDepth };
```

→ `{"ok":true,"callee_status":200,"depth":0,"trace":"tr_97982d36…"}`

The callee's execution row has `"trigger":"f2f"` and **shares the caller's
`trace_id`** — that is how you assert causal propagation. Signature is
`invoke(name, payload, {timeoutMs})`; 404 → `OrvaError`, 507 → call depth
exceeded. Needs `network_mode: egress` like every SDK call.

All four triggers — `cron`, `http`, `job`, `f2f` — were confirmed in one
function's execution list.

### 4.11 Egress enforcement

The highest-value journey, because it is the security boundary.

> `egress_blocklist` is **instance-global**, not per-function: steps 3 and 4
> change the compiled policy for *every* sandbox on the target and retire its
> warm egress pools. This is the journey that most needs the scratch instance
> from the top of §4 — and the reason every line carries `--endpoint`/`--api-key`.

```bash
# 1. baseline: default network_mode=none
orva --endpoint "$B" --api-key "$K" invoke egress-probe --body '{}'
# {"reachable":false,"error":"Error: getaddrinfo EAI_AGAIN example.com"}

# 2. enable egress
curl -X PUT -H "X-Orva-API-Key: $K" -d '{"network_mode":"egress"}' $B/api/v1/functions/$FID
orva --endpoint "$B" --api-key "$K" invoke egress-probe --body '{}'
# {"reachable":true,"status":200,"ms":102}

# 3. block it
orva --endpoint "$B" --api-key "$K" firewall add example.com --label "qa-temp"
# added firewall rule 687 (hostname example.com)
orva --endpoint "$B" --api-key "$K" invoke egress-probe --body '{}'
# {"reachable":false,"error":"AggregateError","ms":260}

# 4. unblock — do not skip this step
orva --endpoint "$B" --api-key "$K" firewall delete 687 --yes
orva --endpoint "$B" --api-key "$K" invoke egress-probe --body '{}'
# {"reachable":true,"status":200,"ms":100}
```

**Assert on the two signatures separately** — they are different mechanisms:
`network_mode: none` gives a **DNS failure** (`EAI_AGAIN`, no resolver
reachable); `egress` + blocklist gives a **connection refused**
(`AggregateError` from `fetch`'s happy-eyeballs; ECONNREFUSED per address
family underneath).

Policy mechanics on disk, under `$ORVA_DATA_DIR/firewall/policy/`:

- Generations are **content-addressed** — `egress-<16 hex>.cfg` with a `current`
  symlink. Adding a rule re-pointed `current` at a file that was already on disk
  from an earlier identical policy; deleting it pointed back. **The generation
  file's mtime is not the change time — the symlink's is.**
- The compiled config is allow-then-reject, first-match-wins: carve-outs for the
  NSTUN gateway (`10.255.255.1/32`), the control plane (exact host **and** port,
  TCP only), and DNS (`1.1.1.1:53`, `8.8.8.8:53`) all precede the REJECT rules.
- Hostname rules resolve to IPs at compile time. `orva firewall` refuses
  wildcards — the policy filters addresses, so a name pattern can never be
  enforced.
- `orva --endpoint "$B" --api-key "$K" firewall resolve example.com` prints the enforced generation and the
  expanded addresses.

**Timing gotcha that will burn you.** Pool retirement on a generation change is
asynchronous. One invocation immediately after `firewall delete` still came back
blocked (8 ms — a not-yet-retired warm worker); the next was reachable. Give it
a beat, or force a cold start by `PUT`ting any config field, before asserting a
flip.

`test/egress-test.sh` covers this ground in 11.5 s / 15 checks and restores
everything via `trap cleanup EXIT`. Its **fail-closed leg** — compiled policy
hidden on disk, no egress worker may start — requires `ORVA_CONTAINER` to name a
*docker* container whose `.admin-key` matches, so it is **`[UNVERIFIED]`** on a
systemd install. The equivalent assertions live in `test_firewall.py` (76 static
checks, 73 observed in CI).

### 4.12 MCP — operator key and channel token

```bash
curl -s -X POST $B/mcp -H "Authorization: Bearer $K" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

The response is **SSE-framed even for POST** (`event: message\ndata: {…}`) —
parse accordingly.

Operator key → **73 tools**, `"cacheScope":"private"`, `"ttlMs":0`, with **no
`initialize` handshake and no `Mcp-Session-Id`** (stateless transport). The
private cache scope is a real security property: a `"public"` scope would let an
intermediary serve one principal's catalog to another.

```bash
orva --endpoint "$B" --api-key "$K" channels create my-bot --functions fnA,fnB -o json
# {"prefix":"orva_chn_3e952ae","token":"orva_chn_3e952aecc0eea5363ff88a0c66544519", …}
```

| Check | Result |
|---|---|
| `tools/list` with the channel token | **exactly 2 tools**, snake_cased per bundled function |
| calling an operator tool (`list_functions`) | `-32602 unknown tool "list_functions"` |
| the channel token at `/api/v1/functions` | **401** — channel tokens have zero management authority |
| deleting the channel | the token immediately 401s |

The channel tool's input schema is a strict envelope: `method` is **required**
and `body` must be `{type:'json',json:{…}}`, `{type:'string',string:'…'}`, or
`{type:'empty'}`. A bare object gives
`unexpected additional properties ["from"]`; a string gives
`has type "string", want one of "null, object"`. A correct call returns
`{status_code, headers, body (string), execution_id, cold_start, duration_ms,
stderr?}` in both `content[0].text` and `structuredContent`.

`get_orva_docs` returned **75,565 bytes** with **0** unsubstituted `{{ORIGIN}}`
placeholders.

`test_mcp.py` covers all 13 sections (51 checks) including per-principal
catalogs and revocation.

### 4.13 The AI assistant, end to end, with no provider key

**Do not do this on a shared instance.** `ai_settings` is a singleton row; a
real instance may hold a real provider key, real conversations, and a custom
system prompt. Use a throwaway container.

```bash
cd test/e2e && python3 mock_llm.py 11500 &

curl -X POST $B/api/v1/ai/providers -H "X-Orva-API-Key: $K" -H 'Content-Type: application/json' \
  -d '{"provider":"openai","label":"manual-mock","api_key":"test",
       "base_url":"http://host.docker.internal:11500/v1","enabled":true}'
curl -X PUT $B/api/v1/ai/settings -H "X-Orva-API-Key: $K" -H 'Content-Type: application/json' \
  -d '{"provider":"openai","model":"gpt-4o","thinking_level":"off",
       "approval_policy":"auto","max_tool_iterations":10}'

# FIELD IS "content", NOT "message"
curl -sN -X POST $B/api/v1/ai/chat -H "X-Orva-API-Key: $K" \
  -H 'Content-Type: application/json' -H 'Accept: text/event-stream' \
  -d '{"content":"CALL list_functions {}"}'
```

Exact SSE sequence observed with `approval_policy: auto`:

```
event: conversation      data: {"id":"019fed5f-…","title":"CALL list_functions {}"}
event: message_start     data: {"message_id":"…","role":"assistant"}
event: message_end
event: tool_call         data: {"name":"list_functions","call_id":"call_0","requires_approval":false,…}
event: tool_result       data: {"call_id":"call_0","status":"succeeded","result":{…}}
event: message_start
event: delta             data: {"text":"Done. The tool ran and r"}
event: delta             data: {"text":"eturned a result."}
event: message_end
event: done              data: {"conversation_id":"019fed5f-…"}
```

With `approval_policy: all_writes` and `CALL create_function {…}` the stream
emits `event: tool_call {"requires_approval":true,"id":"…"}` then
`event: awaiting_approval` and **pauses**. Resume with:

```
POST /api/v1/ai/tool-calls/{tool_call_id}/approve     (or /reject)
```

`/api/v1/ai/conversations/{id}/approve` is a 404 — the routes are under
`tool-calls`.

Sending `{"message": …}` instead of `{"content": …}` returns
**400 `content is required`**.

`tests/test_ai_chat.py` against a throwaway container plus a host-side mock
returned `ALL PASSED — 23 checks`, including "provider key is not exposed via
chat SSE or conversation detail" and "write tool reject recorded as rejected".

**A real divergence surfaced by the approve path:** the MCP `create_function`
tool is **strict** where REST is lenient — `{"name":"ai-made","runtime":"node"}`
was rejected with `description is required: pass a one-sentence summary…`, while
the same body via `POST /api/v1/functions` returns 201. Validation tests must
use genuinely invalid input, not merely incomplete input.

CLI equivalent: `orva --endpoint "$B" --api-key "$K" chat` (REPL, `/help
/model /thinking /new /clear /yolo /exit`) or the same with `chat -p "…"` for a
one-shot. Both write conversations and drive `ai_settings` on whatever endpoint
they reach — the flags are not optional here either.

### 4.14 Streaming responses

```js
exports.handler = async function* (event) {
  for (let i = 1; i <= 5; i++) { yield `chunk-${i}\n`; await new Promise(r => setTimeout(r, 200)); }
};
```

```
$ orva --endpoint "$B" --api-key "$K" invoke qa-stream --body '{}' --stream
chunk-1 … chunk-5
POST qa-stream · 200 · 1.07s (streamed)
```

Headers: `Transfer-Encoding: chunked`, `X-Orva-Streaming-Enabled: 1`,
`X-Orva-Stream-Keepalive-Seconds: 15`, `X-Orva-Ttfb-Ms: 0`.

**Assert on the timestamps, not the body.** A buffered response has an
identical body. `curl -N` with per-line timestamps showed
`20:28:27.691 / .895 / 28.098 / .300 / .499` — genuine ~200 ms incremental
delivery.

### 4.15 Replay from the invocations log

```bash
EID=$(curl -s -H "X-Orva-API-Key: $K" "$B/api/v1/executions?function_id=$FID&limit=1" \
      | jq -r .executions[0].id)
curl -s -H "X-Orva-API-Key: $K" "$B/api/v1/executions/$EID/request"
```

```json
{"method":"POST","path":"/","body":"{\"via\":\"curl\"}","captured_at":1786393082469,
 "truncated":false,
 "headers":{"accept":"*/*","content-type":"application/json",
            "user-agent":"curl/8.14.1","x-orva-api-key":"[REDACTED]"}}
```

**The API key header is redacted in the capture.** Assert that — it is a real
security property, not cosmetics.

```bash
curl -s -X POST -H "X-Orva-API-Key: $K" "$B/api/v1/executions/$EID/replay"
```

Returns the handler's response and creates a **new** execution row whose
`replay_of` points at the original. Replays of replays are allowed. Replaying an
execution with no captured request → 404.

### 4.16 The dashboard

**The UI is served under `/web/`, not `/`** — `GET /` 302s. Sidebar routes:
Overview `/web/`, Chat `/web/ai`, Functions `/web/functions`, Schedules
`/web/cron`, Jobs `/web/jobs`, Activity `/web/activity`, Invocations
`/web/invocations`, Traces `/web/traces`, Keys `/web/api-keys`, Channels
`/web/channels`, **Egress** `/web/firewall`, Settings `/web/settings`, Docs
`/web/docs`.

(An older build labels that item "Firewall" — a fast way to spot a stale image.)

The click-through that earns its time, and what each screen proves:

1. **`/web/onboarding`** (virgin DB only) — the strength meter enforces 10 chars
   plus lower/upper/digit/symbol and keeps *Create account* disabled until
   satisfied, while the API floor is 8. Landing on `/web/` with a session cookie
   proves the whole session path.
2. **`/web/`** — Functions count, In flight, Invocations, Cold-start %, p50/p95/p99,
   host RAM, Builds, Sandbox activity, and **Warm pools (N)** with a per-pool rps
   sparkline. If Warm pools reads 0 after an invoke, the pool never spawned.
3. **`/web/invocations`** — the single richest screen. Columns
   `Time | Function | Status | Cold | HTTP | Duration | Trace | ID`, filters,
   select-all + bulk delete. **Click any row** for a drawer containing the
   captured Request (with `x-orva-api-key: [REDACTED]`), a
   `Save as fixture →` button, `Stderr (stdout is the response body, not
   stored)`, and a `Replay` button — journeys 4.1, 4.15 and fixtures in one
   place.
4. **`/web/settings`** — Build info (Version / Commit / Built / Image) must match
   `/api/v1/system/health`; AI providers; Storage + **Compact database**
   (`VACUUM`); Change password; Log out.
5. **`/web/functions/<name>`** — the Editor/Test pane, with `/deployments`,
   `/diff`, `/kv`, `/inbound-webhooks` as sub-routes.
6. **`/web/docs`** — the hand-maintained view. *Copy as Markdown* fetches
   `/web/docs.md` and substitutes `{{ORIGIN}}` **client-side**.

**Cosmetic bug to expect, not chase:** opening the drawer for an execution with
no stderr fires `GET /api/v1/executions/{id}/logs` → 404 and logs a red
`API Error` in the browser console. That is the normal case for a silent
handler.

---

## 5. Negative testing

A feature that fails *wrongly* is a bug even when the happy path passes. Every
row below was observed.

### 5.1 Invoke and function lifecycle

**Every error body is wrapped in an `error` envelope** — `{"error":{"code":…,
"message":…}}`, with some paths adding a (frequently empty) `request_id`
alongside. The bodies below are pasted from the wire, not paraphrased: a `jq`
assertion written against `.code` gets `null`; the path is `.error.code`.

| Case | Expected |
|---|---|
| `POST /fn/<unknown-uuid>` | 404 `{"error":{"code":"NOT_FOUND","message":"function not found"}}` |
| `POST /fn/<name>` | **404**, same body — the invoke path is UUID-only |
| `GET /api/v1/functions/<name>` | **404**, same body — REST GET resolves UUID only; `DELETE` and most other id-taking routes accept a name via `resolveFnID` |
| `orva … functions get <name>` | **works** — the CLI resolves names client-side. Never infer REST behavior from CLI behavior. |
| `{"runtime":"rust"}` | 400 `{"error":{"code":"VALIDATION","message":"unsupported runtime: rust"}}` |
| `{"runtime":"node24"}` and every other legacy versioned id | 400 `{"error":{"code":"VALIDATION","message":"unsupported runtime: node24"}}` — latest-stable only |
| `{}` (no name) | 400 `{"error":{"code":"VALIDATION","message":"name is required"}}` |
| `{not json` | 400 `{"error":{"code":"INVALID_JSON","message":"invalid request body"}}` |
| `PUT {"status":"building"}` | 400 `VALIDATION` (field whitelist) |
| invoking a function that is not `active` | 409 `NOT_ACTIVE` |
| execution with no stderr → `/logs` | 404 `execution logs not found` — normal, not a bug |

### 5.2 Handler failure modes

| Scenario | Expected |
|---|---|
| `throw new Error('…')` | **500** `{"error":"Internal function error","message":"…"}` |
| `return {statusCode:418,…}` | **418** with the AWS-shape body |
| `process.exit(3)` | **502** `WORKER_CRASHED` with `hint: check stderr in the latest execution log; common causes: process.exit, OOM, syntax error in handler` |
| exceed `timeout_ms` | **504** `TIMEOUT` with `details.timeout_ms`, in ~timeout+0.1 s |
| 8 MB request body | **413** `PAYLOAD_TOO_LARGE` (the JSON cap is 6 MB) |
| ~7 MB `deploy-inline` | **not** 413 — the deploy reader is deliberately exempt from the JSON cap (`test_security.py` M4) |
| pool saturated under contention | `POOL_AT_CAPACITY` |
| exceed `memory_mb` | **`[UNVERIFIED]`** — see below |

**`memory_mb` and `cpus` are not enforced on a bare-metal host without cgroup
delegation.** Confirmed two ways on the survey host: the server logs
`cgroup v2 controllers not delegated; per-sandbox memory/pid/cpu caps disabled
(rlimit-only fallback)`, and no `--cgroup_mem_max` appears in the nsjail argv.
An allocation loop hit the 504 timeout instead of OOMing. OOM and CPU-throttle
tests are only meaningful in the Docker image (which bind-mounts
`/sys/fs/cgroup`) or on a host where systemd genuinely delegates the
controllers — the unit had `Delegate=yes` and it still was not delegated.

### 5.3 Auth and authorization

| Case | Expected |
|---|---|
| no auth / bogus key on `/api/v1/*` | 401 |
| `X-Orva-Internal-Token: bogus` on `/api/v1/functions`, `/keys`, `/executions` | **401** — the fail-open bypass is closed on this branch (`subtle.ConstantTimeCompare`, then fall through to normal auth). This is `test_security.py`'s H1 and it was reproduced by hand. |
| `/metrics` with no auth | **200** — intentional, bypasses the auth middleware |
| `auth_mode: platform_key`, no key | 401 `this function requires an Orva session cookie or X-Orva-API-Key header` |
| `auth_mode: "nonsense"` | 400 `invalid auth_mode: nonsense (allowed: none, platform_key, signed)` |
| `rate_limit_per_min: 2`, 4 requests | 200, 200, **429 with `Retry-After: 60`**, 429 |
| signed mode: tampered body, or a stale timestamp | rejected (`auth-test.sh` asserts both) |
| read-only key on a write route | 403 `FORBIDDEN` |
| a revoked key, immediately | 401 |
| a read-only key anywhere under `/api/v1/ai/*` | rejected — the REST middleware admin-gates the whole namespace, *and* the tool catalog is permission-scoped; either layer suffices |

### 5.4 Webhooks, channels, MCP

| Case | Expected |
|---|---|
| inbound webhook with a correct HMAC-SHA256 hex signature | 200 + handler output |
| wrong or missing signature | **401** `SIGNATURE_INVALID` |
| a `secret` you supply on webhook create | **ignored** — the server generates its own and returns it once |
| `DELETE /api/v1/inbound-webhooks/{id}` | 404 — the route is nested: `DELETE /api/v1/functions/{fn_id}/inbound-webhooks/{id}` |
| `GET /mcp`, `DELETE /mcp` | **405** |
| `POST /mcp` with no auth | 401 + `Www-Authenticate: Bearer realm="orva", resource_metadata="/.well-known/oauth-protected-resource"` |
| `POST /mcp` missing `_meta.clientCapabilities` | JSON-RPC `-32602` |
| channel token at any `/api/v1/*` | 401 |
| a deleted channel's token | 401 immediately |
| cron `--expr 'not a cron'` | 400 `invalid cron_expr: expected exactly 5 fields, found 3` |

### 5.5 Deletion cascades

Deleting a function must take its dependents with it. Verified: its inbound
webhooks 404, its executions disappear from `/api/v1/executions` (total went
45 → 24), and its `functions/<id>/` and `build-cache/<id>/` directories are
removed from disk. Routes cascade too — `routes.function_id` has
`ON DELETE CASCADE`, which is why `api-smoke.sh` never explicitly deletes the
route it creates and the route still vanishes.

---

## 6. Writing a new test

### 6.1 Which layer

| If you are testing | Put it in |
|---|---|
| a pure Go function, a parser, a policy compiler | a Go unit test next to the code (`t.Run` subtests, `-race`) |
| a REST/CLI/MCP/AI behavior, an error contract, a permission boundary | **`test/e2e/tests/test_*.py`** — this is the default answer |
| a subsystem you want to poke ad hoc against a running instance while developing | `test/*.sh` — but know CI will never run it |
| the installer, the service unit, the shipped CLI binary, the release assets | `test/install/` or `test/cli/` |
| throughput or capacity | `test/ceiling.sh` / `test/kata-bench/` — data collection, not a gate |

Default to the Python suite. It is the only layer CI executes as a behavior
test, it has a real skip protocol, and its modules are self-contained processes.

### 6.2 The module template

```python
#!/usr/bin/env python3
"""One-line statement of what this module proves."""
import sys
from harness import OrvaClient, section, check, summary, skip

NAME = "e2e-myfeature"          # unique, e2e-prefixed, used for cleanup

def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2

    fn_id = ""
    try:
        section("create")
        fn = c.post("/api/v1/functions", {"name": NAME, "runtime": "node",
                                          "description": "e2e fixture"})
        fn_id = fn["id"]
        check("create -> id", bool(fn_id))

        section("behavior")
        code, body = c.req("GET", f"/api/v1/functions/{fn_id}", expect=range(200, 599))
        check("get -> 200", code == 200, f"got {code}")

        section("validation")
        check("bogus runtime rejected",
              c.status("POST", "/api/v1/functions",
                       {"name": NAME + "-bad", "runtime": "rust"}) >= 400)
    finally:
        if fn_id:
            c.delete(f"/api/v1/functions/{fn_id}")

    return summary()

if __name__ == "__main__":
    sys.exit(main())
```

`run.py` discovers it automatically — `sorted(glob("tests/test_*.py"))`. There
is nothing to register.

### 6.3 The harness surface

| Call | Behavior |
|---|---|
| `c.req(method, path, body=None, expect=(200,201,204))` | the core. Returns `(code, parsed)`. **Raises `AssertionError` if the code is not in `expect`** — pass `expect=range(200,599)` to inspect any status. `HTTPError` is caught and its body read, so 4xx/5xx are inspectable. 60 s timeout. |
| `c.get/post/put(path[, body])` | body only; raise on non-2xx |
| `c.delete(path)` | returns the **status code** |
| `c.status(method, path, body=None)` | code only, never raises |
| `c.stream(path, body, timeout=90)` | POST + SSE parse → `[(event, data), …]` |
| `c.chat(content, conversation_id=None)` | → `(frames, conv_id)` |
| `c.approve(row_id)` / `c.reject(row_id)` | drive the approval gate |
| `events_of / has_event / first_tool_call` | frame helpers |
| `CLIRunner().run(*args, input_text=None)` | → `(rc, stdout, stderr)`; appends `--endpoint`/`--api-key`. With `input_text=None` it forces `stdin=DEVNULL` so a developer's TTY never changes behavior vs a CI runner. `TimeoutExpired` → rc 124. |
| `latest_execution_stderr(client, function_name=…)` | resolves name→id, walks recent executions, returns the first non-empty stderr. **Call it before cleanup** so a failure names the real sandbox error instead of just `WORKER_CRASHED`. |
| `start_mock` / `configure_mock_provider` / `remove_mock_provider` | the mock-LLM lifecycle; the configure helper snapshots pre-existing settings exactly once per client so repeated calls with different approval policies do not capture already-mutated state |

`section()` is cosmetic. `check(name, cond, detail="")` prints `✓`/`✗`,
increments the counters, and **returns `cond`** so it can gate follow-up work.
`summary()` prints the trailer and returns 0/1. `skip(reason)` prints
`RESULT pass=0 fail=0 skip=1` and returns **3**.

### 6.4 Non-negotiable conventions

1. **Unique, prefixed resource names.** Every existing module uses a literal
   `e2e-*` constant and cleans up by that name.
2. **Always clean up, in a `finally`.** And clean up **only what you created** —
   `test_ai_edit.py` is the counterexample that deletes every conversation on
   the instance.
3. **`return summary()`, `sys.exit(main())`.** The trailer must be the last
   non-blank stdout line.
4. **Skip via `skip(reason)` and exit 3**, only for a genuinely absent
   capability. If your module can be gated by `ORVA_REQUIRE_SANDBOX`, use the
   `sandbox_unavailable()` idiom from §3.2.3 verbatim so the flag turns the skip
   into a hard failure.
5. **At least one check must pass** or `run.py` fails the module. There is no
   "zero-check pass".
6. **Never print after the trailer.**
7. **Validation tests must use genuinely invalid input.** REST
   `create_function` fills defaults; only the MCP tool is strict.
8. **New feature or endpoint → it is not done until it has a module here.**

For a **shell** suite, mirror the conventions that the good ones already
follow: read `BASE_URL`/`API_KEY`, use `$$` in resource names, install a
`trap cleanup EXIT`, print a machine-readable last line, and either
`exit $FAIL` or `exit 0/1` — but pick one and say which in the header, because
callers currently have to know per-script (§3.4).

### 6.5 Reuse the fixtures instead of writing another handler

`test/fixtures/` holds nine single-file handler directories. Pick by behavior:

| Need | Fixture |
|---|---|
| trivial 200 | `node-hello` / `python-hello` (currently unreferenced — revive them, do not add a third hello) |
| request parsing with a real 400/201 branch | `node-api` |
| CPU pressure | `node-cpu` / `python-compute` |
| bounded latency (500 ms) | `node-slow` |
| timeout behavior (10 s sleep) | `slow-node` — note the default `timeout_ms` is 15000, so lower the per-function timeout |
| exact-body determinism | `python-data` / `python-compute` (seeded) |
| error-rate / retry paths | `python-error` — deliberately fails 20% of the time; never assert a single invocation succeeds |

Beware `node-slow` (500 ms) versus `slow-node` (10 s): near-identical names,
opposite word order, very different behavior.

Every fixture is a **zero-dependency** deploy — no `package.json`, no
`requirements.txt` — so none of them exercise the build jail. For that path,
model on `native-engine.sh`, which writes `semver@7.6.3` and `urllib3==2.2.3`.

Only inline handler source when the API takes a **code string**
(`deploy-inline`), which cannot consume a directory. If you must add a fixture,
add a reference from a suite **in the same change** — three of the nine are dead
precisely because that step was skipped.

---

## 7. Triage: reading a failure

### 7.1 The first four questions

1. **Did the sandbox spawn?** Invoke anything and look at the status: 503
   `SANDBOX_ERROR` = the jailer or the rootfs is missing (host problem);
   502 `WORKER_CRASHED` = it spawned and died (could be either).
2. **Is your key real?** Every module reporting `missing RESULT trailer` is a
   blank key until proven otherwise (§2.6).
3. **Are you testing the binary/image you think you are?** §2.5 — four traps.
4. **Did you point it at the right port?** `tracing-test.sh` ignores `BASE_URL`;
   the E2E container is on 8455; the shell default is 18443.

### 7.2 Where the evidence lives

| Evidence | Where |
|---|---|
| server logs, systemd | `journalctl -u orva -n 200` |
| server logs, Docker | `docker logs <container> --tail 200` |
| **build** log for a deploy | `GET /api/v1/deployments/{deployment_id}/logs` |
| **handler stderr** for an invoke | `GET /api/v1/executions/{execution_id}/logs` (`stdout` is always empty — §4.1) |
| the captured request | `GET /api/v1/executions/{execution_id}/request` |
| nsjail's own argv | `ps -ef | grep nsjail` on the host |
| compiled egress policy | `$ORVA_DATA_DIR/firewall/policy/current` (a symlink — its mtime is the change time) |
| build-jail proof | `journalctl -u orva | grep 'build step starting'` |
| E2E per-module detail | `test/e2e/CHECKLIST.md` → `## Failure details` |
| shell umbrella detail | `test/run-all-results.tsv`, column 2 |
| install harness detail | `test/install/logs/<distro>-{install,smoke,uninstall,reinstall}.log` |

Inside a Python module, `latest_execution_stderr()` is the right way to surface
sandbox stderr into the failure message — call it **before** cleanup.

### 7.3 Reading `CHECKLIST.md`

- `❌ FAIL` rows are the only ones that affect the exit code.
- `⚠️ SKIP` rows count `0/0`, never appear under Failure details, and **do not
  fail the run**. Read the `- **Modules:** N passed, M failed, K skipped` line
  every time.
- The `Target:` line tells you what was actually tested. If it says `external
  instance …` you did not exercise the isolated-Docker path.
- The `detail` for a FAIL is the last 4 lines containing `✗`, ANSI-stripped. If
  a module produced no `✗` lines, it falls back to stderr — that is the shape of
  a crash rather than an assertion failure.
- Protocol errors name themselves: `missing RESULT trailer`,
  `exit 3 requires RESULT skip=1`, `skip result requires exit 3 and zero failed
  checks`, `passing result requires at least one passed check`.

### 7.4 Documented flake modes

These are known, understood, and **not** regressions.

**F1 — GitHub API 403 on the server installer.** `scripts/install.sh`'s
`resolve_version()` calls `api.github.com/.../releases/latest` with **no auth
header and no token support** (unlike `install-cli.sh`, which honors
`GITHUB_TOKEN`). Each distro leg triggers it twice — install plus reinstall —
so a full matrix makes 12 unauthenticated calls from one runner IP against a
60/hr/IP cap, and neither `run-distro.sh` nor `uninstall-flow.sh` forwards a
token into the container. CI pins `ORVA_VERSION` on release/schedule/dispatch
runs, so **the exposure today is on push/PR runs**, not post-release.

*Signature:* the log ends with `==> resolving latest release from GitHub` →
`curl: (22) … error: 403` → `could not resolve latest release tag`.
*It is rate limiting iff* the failure is at the API resolve, **before** any
asset download or checksum; the fresh install on that same distro already
printed `install.sh completed`; and other distro legs completed the identical
flow. *A real regression instead shows* a checksum mismatch, a 404 on an
asset, `orva.service did not become active`, a `wait_for_health` timeout, or
`✗` lines from `smoke-flow.sh`.
*Action:* `gh run rerun <id> --failed` (a fresh runner gets a fresh IP), or
locally `ORVA_VERSION=v2026.08.05 bash test/install/run-distro.sh <distro>`.

> Correction to a widely-held belief: `arch` and `alpine321` are **not**
> `continue-on-error` legs. `grep -n continue-on-error .github/workflows/*.yml`
> returns nothing. All six distro legs are hard gates; `fail-fast: false` only
> means siblings keep running.

**F2 — CLI upgrade round-trip, expected-red by construction.**
`upgrade-test.sh` installs the **second-newest** release and runs *that old
binary's* `orva upgrade`. Any upgrader bug fixed in release *N* is still present
in *N-1*, so the leg stays red until the fixed release becomes the baseline — it
self-resolves at the next release. A red `cli-upgrade` right after a release
that touched `cli/commands/upgrade.go` is not a regression. **And right now it
is worse than red: it is vacuous** (§3.5). Verifying the upgrader today cannot
go through this script; build the fixed CLI and run `orva upgrade --force` by
hand.

**F3 — nested-container nsjail.** Structural, not intermittent. See §3.5 for
how to read `install-matrix` versus `native-engine`.

**F4 — package-mirror flake.** `run-distro.sh` already retries `install.sh` once
after a 10 s backoff, because apt/dnf mirrors return HTTP 520 and transient DNS
errors. Both attempts dying with the *same* package-manager error is mirror
flake; attempt 2 failing *differently* is real.

**F5 — dead pinned container image.** `kata-flow.sh` and both `kata-bench`
scripts default to `ghcr.io/harsh-2002/orva:v2026.05.12`, which returns
`manifest unknown`. GHCR appears to carry only `:latest`. Override with
`ORVA_IMAGE=ghcr.io/harsh-2002/orva:latest`.

**F6 — egress-test's single public host.** `egress-test.sh` stakes its
mandatory-reachability leg on `example.com` plus in-sandbox DNS. Commit
`53b3f515` fixed exactly this class of flake in the Python suite ("stop betting
the egress-enforcement proof on one public IP" — an amd64 runner timed out on
one address while arm64 passed) by rotating four candidate targets using literal
IPs. That fix was never carried over to the shell script. A single red
`egress-test.sh` reachability check with everything else green is a network
flake; re-run before investigating.

**F7 — asynchronous pool retirement.** §4.11. An assertion immediately after a
policy or config change can read a stale warm worker. Not a bug.

### 7.5 Flake or regression?

| Symptom | Read it as |
|---|---|
| every E2E module `missing RESULT trailer` | blank API key, not a protocol bug |
| one module exits 2 | blank key for that module (exit 2 has no dedicated message) |
| `test_deploy_invoke` / `test_firewall` SKIP and the run still exits 0 | the host cannot nest sandboxes — set `ORVA_REQUIRE_SANDBOX=1` only when it genuinely can |
| a sandbox module FAILs with "sandbox invocation is available: false" | you set `ORVA_REQUIRE_SANDBOX=1` on a host that cannot jail |
| `tracing-test.sh` fails with `Failed to connect to … 8443` | wrong env var, not a tracing defect |
| `atscale.sh` exits 2 | `hey` is not installed |
| `onboarding-flow.sh` passes instantly | it skipped; it asserted nothing |
| green run after code changes in isolated Docker mode | suspect a stale `orva:e2e` image before believing it |
| `install-matrix` green but invocation broken in the field | expected — smoke flow soft-fails invokes |
| a CI `e2e` failure on arm64 only | very likely a Kafel seccomp-name problem; the syscall table differs and an unknown name is a *compile* error |

---

## 8. CI

### 8.1 What actually gates a merge

**Nothing mechanical.** `main` has **no branch protection** and **no rulesets**;
there are no required status checks. The only enforcement point in the entire
system is `release.yml`'s `gate` job at tag time, which polls

```
GET /repos/$REPO/actions/workflows/ci.yml/runs?head_sha=$SHA&event=push&branch=main
```

and refuses to build unless that run concluded `success`. Traps in that query,
all load-bearing:

- **`event=push&branch=main` only.** A green **PR** run does not satisfy it.
- **`ci.yml` only** — literally `check ci.yml hard`, one call, in
  `release.yml`'s gate. **CodeQL is never consulted**, and CodeQL is real: commit
  `070e6142` fixed a high-severity CodeQL finding. It also only runs on PRs
  targeting `main`, so **stacked PRs get no security scan** — and even on a PR
  that does scan, a red CodeQL cannot stop a release.
- `ci.yml`'s concurrency group is `ci-<ref>-<event>` with
  `cancel-in-progress: true`. Two quick pushes to `main` cancel the first run;
  tagging that first commit fails the gate with `concluded 'cancelled'`, and the
  fix is a manual CI re-run, not a retag.

**On "exactly two workflows" — CONTRACT §7 is right; the Actions tab is not the
same list.** CONTRACT §7 is a policy about *workflow files this repo authors*,
and it holds: `.github/workflows/` on this branch contains exactly `ci.yml` and
`release.yml`. The Actions sidebar shows more than that, and none of the extras
contradicts the contract — a `deploy.yml` that exists only on branch `web`, plus
GitHub-managed features that are not workflow files here at all (CodeQL default
setup, Dependency Graph, Dependabot — configured by `.github/dependabot.yml` —
and pages-build-deployment).

The point that *does* matter is the one above: the gate reaches exactly one of
them. Anything not named `ci.yml` — CodeQL most of all — is advisory at release
time no matter how red it is. Do not "fix" this by adding a third workflow;
fixing it means either teaching the gate to consult CodeQL's check runs or
accepting, explicitly, that CodeQL is advisory.

### 8.2 Which jobs run when

| Job | push `main` | PR | release / dispatch | Sunday |
|---|---|---|---|---|
| `agent-docs` (AGENTS.md symlink mirror) | ✅ always | ✅ always | – | – |
| `lint` (actionlint + shellcheck + `dash -n`) | if code | if code | – | – |
| `go` (vet, **`-race` test**, build+version assert, govulncheck) | if code | if code | – | – |
| `ui` (`npm ci`, `npm audit`, lint, build) | if code | if code | – | – |
| `docker` (no-cache build + health smoke on 18443) | if code | if code | – | – |
| **`e2e` (amd64 + arm64)** | ✅ **always, no path filter** | ✅ **always** | `source`/`all` | – |
| `cli-unit`, `cli-cross-build` | ✅ | if `cli` | `cli`/`all` | – |
| `cli-install-{linux,macos,windows}` | ✅ | if `cli_installers` | ✅ | 04:00 |
| `cli-upgrade` | ✗ | ✗ | ✅ | 04:00 |
| `install-matrix` (6 distros) | ✅ | if `server_install` | ✅ | 03:00 |
| `native-engine` (amd64 + arm64) | ✅ | if `server_install` | ✅ | 03:00 |
| `released-container` | ✗ | ✗ | ✅ | 03:00 |

Two consequences: a **docs-only PR still runs both `e2e` legs** (the job has no
path filter and no `needs:`), and `suite=all` on a `workflow_dispatch` is **not
everything** — `lint`, `go`, `ui`, `docker` are gated on push/pull_request and
are unreachable by dispatch.

Only 6 of ~26 job instances on a main push test **your branch's code**: `go`,
`ui`, `docker`, both `e2e` legs, and `cli-unit`/`cli-cross-build`. Every
installer job installs the latest *published* release.

### 8.3 The `e2e` job — the whole sandbox story

The only job that builds this branch and runs it against a real nsjail with
`ORVA_REQUIRE_SANDBOX=1`, on an amd64 × arm64 matrix. Steps:

1. harness protocol unit tests (`test/e2e/unit/`)
2. `make build`
3. build nsjail from source at a **pinned commit** and
   `setcap cap_sys_admin,cap_setuid,cap_setgid,cap_net_admin,cap_net_bind_service=eip`
4. `scripts/build-rootfs.sh` for node and python, then `orva setup`
5. preflight: getcap, userns sysctls, **hard-fail if `/dev/net/tun` is missing**
   (after `modprobe tun`), and a direct nsjail spawn probe
6. `ORVA_DISABLE_USERNS=1 ./build/orva serve &` (Ubuntu 24.04 runners restrict
   unprivileged user namespaces)
7. `: > test/e2e/CHECKLIST.md` — so a committed checklist can never impersonate
   diagnostics — then `run.py --url http://127.0.0.1:8443 --api-key "$ADMIN_KEY"`

The arch matrix exists because this is the only place the build jail's seccomp
profile is compiled by **Kafel**, and aarch64's syscall table omits several
legacy non-`*at` calls — Kafel treats an unknown name as a *compile* error, so a
wrong entry takes every arm64 dependency build down rather than degrading it.
The Go unit tests assert the repo's own allowlist beliefs on both arches; only
the arm64 `e2e` leg proves Kafel actually compiles the names.

Real numbers from a CI log: nsjail build 35 s, rootfs 12 s, **E2E suite 61 s**,
job total 2 m 12 s, result `28 passed, 0 failed, 0 skipped` / `647 checks`.

**`env.py` is dead code in CI.** CI always passes `--url`, so the isolated-Docker
path — the one CONTRACT §6 calls authoritative — is exercised **only locally**.
A regression in `env.py` is invisible to CI.

### 8.4 Reproducing a CI leg locally

| CI job | Local equivalent | Where it differs |
|---|---|---|
| `agent-docs` | inline the loop from `ci.yml` | none — pure git. 8 `CLAUDE.md` dirs must each mirror an `AGENTS.md` at mode 120000 |
| `lint` | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`; then the `dash -n` + `shellcheck` invocations verbatim from `ci.yml:185-205` | shellcheck version drift; actionlint shells out to shellcheck if present |
| `go` vet | `make lint` | CI runs `make embed` first, which **rewrites tracked files** |
| `go` test | **`go test -count=1 -race ./...`** | `make test` omits `-race` |
| `go` vuln | `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...` | a new advisory reddens CI with zero code change |
| `ui` | `cd frontend && npm audit --audit-level=moderate && npm run lint && npm run build` | CI uses **`npm ci`** (lockfile-strict); `make ui` uses `npm install` and may rewrite the lockfile. `npm audit` is the classic green-locally-red-in-CI leg |
| `docker` | `docker build -t orva:ci . && docker run -d -p 127.0.0.1:18443:8443 …` then poll health | CI uses `no-cache: true, pull: true`. Neither passes `--build-arg VERSION`, so both report `dev`/`unknown` — only `release.yml` stamps identity. The first 2–3 health curls fail with `Empty reply`; the 30× loop is required |
| `e2e` | §1.2 | CI uses a **host** nsjail with `setcap` + `ORVA_DISABLE_USERNS=1`; local Docker mode keeps user namespaces and `--cap-add SYS_ADMIN`. Different nsjail configuration. `make build` embeds the **committed** `ui_dist`, so the E2E job serves the committed UI snapshot |
| `cli-unit` | `go vet ./cli/... ./internal/...` && `go test ./cli/commands/ -count=1` | CI adds `-v` |
| `cli-cross-build` | `bash test/cli/build-matrix.sh && bash test/cli/command-tree.sh` | the qemu-aarch64 `--version` leg is a `warn`, never a failure, and is skipped without qemu |
| `install-matrix` | `ORVA_BROWSER_LEG=0 bash test/install/run-distro.sh ubuntu24` | verified: exit 0, ~4 min, host port **19449** |
| `cli-install-*` macOS/Windows | **impossible locally** — inline pwsh/bash in `ci.yml`, no script | `[UNVERIFIED]` |
| `native-engine`, `released-container` | do not run locally | destructive / needs ghcr login |

### 8.5 What CI never runs

**Zero `test/*.sh` suites execute in CI.** They are `shellcheck`ed only. The
`docker image smoke` job does two inline `curl` calls — it does not call
`api-smoke.sh`. Also never executed anywhere in CI:
`test/install/matrix.sh`, `test/install/failure-modes.sh`,
`test/install/kata-flow.sh`, all of `test/kata-bench/`, and
`test/nstun-policy-bench.py` (which, being Python, escapes even the
`test/*.sh` shellcheck glob and is neither linted nor run).

So the entire "shell suite against a live instance" tier that CONTRACT §6 and
`docs/CONTRIBUTING.md` present as part of the gate is, mechanically,
documentation. Run it by hand or it does not happen.

---

## 9. Known-stale and known-broken

Short, honest, and current as of this writing. Verify before relying on any of
it; fix rather than route around where you can.

**Harnesses that lie or cannot pass**

| Item | Problem |
|---|---|
| `test/loadtest.sh` | Broken **and** destructive. `fuser -k 8443/tcp` kills whatever holds 8443; `rm -f ~/.orva/orva.db*` destroys the default-datadir DB; `HEY=~/go/bin/hey` with no guard; `orva=./orva` points at a gitignored repo-root binary; and `grep -q "deployed"` can never match because the CLI prints `Deploy submitted (deployment …)`, so all 6 deploys report `✗ FAILED` even when they succeed. It reads no env vars, so it cannot be retargeted. **`[UNVERIFIED]` — deliberately never executed.** |
| `test/atscale.sh` | Its header claims it asserts budget, isolation, and no-503; the file contains **no check or assert of any kind** and no verdict. It has **zero `DELETE` calls** and permanently leaves 20 functions (`ascale-node-1..10`, `ascale-py-1..10`) — and it is a member of `run-all.sh`, so the umbrella litters any instance it is pointed at. |
| `test/sdk-test.sh` | Exit code is meaningless (§3.4). Also `[ "$x" -ge 2 ]` on a possibly-empty value emits a raw bash error instead of a clean fail. |
| `test/tracing-test.sh` | Ignores `BASE_URL`/`API_KEY`, so `run-all.sh` cannot drive it (§3.4). Its header documents `T3: Job propagation` and `T4: Cron is a root trace`; **neither exists in the file** — it runs T1, T2, T5, T6, T7. Two docs repeat the fiction. |
| `test/onboarding-flow.sh` | Reports `pass` while asserting nothing on any non-virgin instance — i.e. inside `run-all.sh`, always. Its 13 checks are real and all pass on a virgin DB (verified, §3.4), but it never deletes the user it creates, so it is single-shot even there. |
| `test/install/failure-modes.sh` | Cannot pass. It does `jq -r '.[].name'` on `/api/v1/functions`, which returns an **object** `{"functions":[…],"total":N}` — jq errors, the marker check always fails, and it reports "marker function did not survive reinstall". Its sibling `uninstall-flow.sh` uses the correct `jq -r '.functions[]? .name'`. Its onboard call also posts `{"email":…}` where the API requires `username`. Nobody noticed because CI lints it and never runs it. |
| `test/install/matrix.sh` | The one genuinely dev-safe script in that directory, documented as the opposite: its header calls itself "the default gate in CI and locally" and references an `install-test.sh` that does not exist. CI never invokes it, and it is not marked executable (mode **664** — `bash test/install/matrix.sh` works, `./test/install/matrix.sh` does not). |
| `test/ceiling.sh` | Parses `orva_host_mem_free_mb`, a metric that no longer exists (the exposition has `orva_host_mem_{total,available,reserved}_bytes`), and the value is discarded anyway. Its `mem_mb` column needs docker and a published port, so on a systemd install it is a constant 0. |
| `test/kata-bench/*`, `test/install/kata-flow.sh` | Default to `ghcr.io/harsh-2002/orva:v2026.05.12` → `manifest unknown` (F5). `extended-functional.sh`'s header advertises five legs; the body implements three. |
| `test/api-smoke.sh` | Line 47-48 prints `ok  POST /functions {network_mode:egress}  HTTP 201` by passing literal `201 201` into `expect_code` — it validates the response field and never looks at the HTTP status. The printed status is fabricated. |
| `test/heavy-deploy-test.sh` | Hardcodes an absolute developer path for its evidence file (`/home/dev/Orva/test/heavy-deploy-stream.log`), swallowed by `|| true`, so on any other machine it is silently not written. Its "SSE log captured" check only asserts size > 30 B; the captured file contained solely the terminal `event: succeeded` frame, so it does not prove build-log streaming. |
| `test/e2e/CHECKLIST.md` | Two modules behind the tree (§3.2.6). |
| `test/fixtures/` | `node-hello`, `python-hello`, `slow-node` have zero references repo-wide. Only `loadtest.sh` — the script that cannot run — consumes fixtures at all. |

**Documentation that is wrong**

| Doc | Claim | Reality |
|---|---|---|
| `docs/RUNTIMES.md` | dependency installs run "on the host (not in the sandbox)" | exactly backwards — every install runs inside nsjail via `sandbox.RunBuild`; the server log proves it (§4.2) |
| CONTRACT §1 | nsjail "on `PATH`" | the path is hardcoded to `/usr/local/bin/nsjail` with no override |
| CONTRACT §4, CLAUDE.md | `frontend/public/docs.md` is "served at `/docs.md`" | `GET /docs.md` is 404; it is `/web/docs.md` |
| CONTRACT §5 | port table | omits the E2E container's host port 8455 |
| CONTRACT §6, `docs/CONTRIBUTING.md` | `test/run-all.sh` is part of the gate | no CI job runs it or any of its members |
| CLAUDE.md | the installer/CLI E2E suites "all run on PRs" | on PRs they are path-filtered and normally skip; `cli-upgrade` and `released-container` never run on push or PR at all |
| `test/CLAUDE.md`, `docs/CONTRIBUTING.md` | the `run-all.sh` member list | both omit `build-cache-test.sh`, which the umbrella does run |
| `test/CLAUDE.md` | "outbound isolation depends on the host firewall/pasta" | architecturally stale — egress is per-sandbox nsjail NSTUN with an immutable compiled policy; no host table is ever created |
| `docs/CONTRIBUTING.md` | "`auth-test.sh` — API key scopes, session expiry, OAuth flows" | it tests per-function `auth_mode` and rate limiting; none of those three |
| `test/e2e/CLAUDE.md` | the CLI coverage confirms "slim-CLI ↔ full-server parity" | `harness.py` defaults `ORVA_BIN` to the **full server** binary; parity is actually proven by `test/cli/command-tree.sh` |
| README, `test/e2e/CLAUDE.md` | the `sudo cat /var/lib/orva/.admin-key` key recipe | that file does not exist on a browser-onboarded instance (§2.6) |
| `docs/CAPACITY.md` | "`secrets-test.sh` ✓ 8/8", "`routes-test.sh` ✓ 7/7", "`onboarding-flow.sh` ✓ 13/13" | today's real counts are 6 and 6 without `hey` and a `sqlite3`-equipped container. The `13/13` is the one that holds — but only on a virgin DB, a precondition CAPACITY.md does not state; on any instance with a user it is 0 (§3.4). Check counts are environment-dependent, so any absolute number in prose is a future lie |
| `orva init` | writes `orva.yaml` and says "run: `orva serve --config orva.yaml`" | dead. `orva serve` has no `--config` flag and nothing in the repo ever reads that file |
| the dev instance's stored AI system prompt | "Node 22/24 … Python 3.13/3.14" | two generic runtimes, latest-stable only. DB state, possibly an operator override |

**One entry withdrawn from that table.** An earlier revision listed CONTRACT §7
and CLAUDE.md's "exactly two workflows" as wrong, on a count of seven registered
workflows. That was comparing two different things and the contract is right:
§7 is a policy about **workflow files this repo authors**, and
`.github/workflows/` contains exactly `ci.yml` and `release.yml`. The count of
seven reached a `deploy.yml` that exists only on branch `web` plus four
GitHub-managed features that are not workflow files here (CodeQL default setup,
Dependency Graph, Dependabot, pages-build-deployment). The substantive point
survives intact and belongs to `release.yml`, not to the contract: the gate
queries `ci.yml` alone, so a red CodeQL — which has already caught a
high-severity issue (`070e6142`) — cannot stop a release. See §8.1.

**Things nobody has been able to verify**

`python3 run.py` in isolated-Docker mode · a cold `docker build -t orva:e2e .`
duration · `loadtest.sh` at all · `atscale.sh` / `ceiling.sh` past their `hey`
guard · `egress-test.sh`'s fail-closed leg · the `/web/onboarding` **UI** leg
(the `/auth/*` backend behind it *is* now verified — all 13 `onboarding-flow.sh`
checks on a virgin DB, §3.4) ·
the two `sqlite3`-gated checks · cold-cache dependency-install timings ·
shell-suite behavior on an nsjail-less host · the 18443 default against a real
Docker deployment · `native-engine.sh` · all of `test/kata-bench/` · the macOS
and Windows CLI installer legs · a real `orva upgrade` round-trip (only one
release exists) · `memory_mb`/`cpus` enforcement · anything on arm64 · an
actual SKIP verdict observed end to end.

---

## 10. Quick reference

**Environment**

```bash
# A scratch instance (§1.1/§4). Point B at your daily instance only for
# read-only work — most of this document writes.
export B=http://127.0.0.1:18446
export K=$(cat /tmp/orva-doccheck/.admin-key)   # or: grep -o 'orva_[A-Za-z0-9_]*' ~/.orva/config.yaml | head -1
export BASE_URL=$B API_KEY=$K                   # shell suites; the CLI ignores all four
```

**The CLI reads none of those.** `orva` uses `~/.orva/config.yaml` unless you
pass `--endpoint "$B" --api-key "$K"` — and an *empty* value for either flag
falls back to the config file silently (§4). Every `orva` line in this document
carries both flags for that reason.

**Commands, with what a pass looks like**

| Command | Measured | Pass looks like |
|---|---|---|
| `make lint` | 1.7 s | silent, exit 0 |
| `make test` | 23 s | all packages `ok` |
| `go test -count=1 -race ./...` | 2 m 14 s | the CI form |
| `make build` | 5 s | `build/orva`, ~55 MB (run this **last**) |
| `python3 -m unittest discover -s test/e2e/unit -p 'test_*.py'` | 0.2 s | `Ran 11 tests … OK` |
| `bash test/api-smoke.sh` | 0.45 s | `=== api-smoke: 20 passed, 0 failed ===` |
| `bash test/auth-test.sh` | 0.80 s | `auth-test: pass=11 fail=0` |
| `bash test/errors-test.sh` | 6.0 s | `errors-test  pass=8  fail=0` |
| `bash test/rollback-test.sh` | 4.6 s | `rollback-test  pass=9  fail=0` |
| `bash test/routes-test.sh` | 2.95 s | `routes-test  pass=6  fail=0` |
| `bash test/secrets-test.sh` | 4.8 s | `secrets-test  pass=6  fail=0` |
| `bash test/egress-test.sh` | 11.5 s | `=== egress-test: 15 passed, 0 failed ===` — mutates the **global** `egress_blocklist` while it runs |
| `bash test/build-cache-test.sh` | 6.5 s | `build-cache: 10 passed, 0 failed` |
| `bash test/heavy-deploy-test.sh` | 14.3 s | `heavy-deploy-test  pass=12  fail=0` |
| `ORVA_ENDPOINT=$B ORVA_API_KEY=$K bash test/tracing-test.sh` | 10.2 s | `PASS: 10` / `FAIL: 0` |
| `ORVA_ENDPOINT=$B ORVA_API_KEY=$K bash test/sdk-test.sh` | 3.2 s | `sdk-test: PASS=13 FAIL=0` — **ignore the exit code**; delete `sdk-test-noop` after |
| `bash test/run-all.sh` | 49.9 s | `run-all-results.tsv`, no `fail` in column 2 — **scratch instance only**: its `atscale.sh` member leaves 20 functions behind wherever `BASE_URL` points (§3.4) |
| `cd test/e2e && PYTHONPATH=. ORVA_URL=$B ORVA_API_KEY=$K python3 tests/test_system.py` | ~1 s | `RESULT pass=7 fail=0` |
| `cd test/e2e && … ORVA_REQUIRE_SANDBOX=1 python3 tests/test_deploy_invoke.py` | ~30 s | `ALL PASSED — 31 checks` |
| `cd test/e2e && python3 run.py --url $B --api-key $K` | ~60 s + setup | `28 passed, 0 failed, 0 skipped` |
| `bash test/cli/build-matrix.sh` | 5 s – 2 min | `=== build-matrix: 19 passed, 0 failed ===` |
| `bash test/cli/command-tree.sh` | 17.5 s | `=== command-tree: PASS ===`, 107 == 107 |
| `ORVA_CLI_TEST_IMAGE=ubuntu:24.04 bash test/cli/install-cli-test.sh ubuntu24` | 26.5 s | `19 passed, 0 failed` |
| `ORVA_BROWSER_LEG=0 bash test/install/run-distro.sh ubuntu24` | ~4 min | exit 0; smoke/uninstall both `passed` |
| `sh test/release/download-verified-asset.sh install-cli.sh /tmp/x` | seconds | `verified release asset … from vYYYY.MM.DD` |
| `bash test/install/matrix.sh` | 18.6 s warm / 69.6 s cold | `passed=31  failed=0` for its six default images — the count is `1 + 5×images` (§3.5) |
| `BASE_URL=http://127.0.0.1:18444 bash test/onboarding-flow.sh` (virgin DB) | 0.28 s | `onboarding-flow  pass=13  fail=0` |

**Never do these**

| Don't | Because |
|---|---|
| `python3 run.py --filter …` | truncates the committed `CHECKLIST.md` to partial results |
| run `test_ai_edit.py` against a shared instance | deletes **every** conversation on it |
| run `native-engine.sh` on a machine you use | installs over `/opt/orva`, regenerates the service unit, restarts it |
| run `loadtest.sh` anywhere | kills whatever holds 8443 and deletes `~/.orva/orva.db*` |
| `make cli` after `make build` | overwrites the server binary at the same path |
| `make clean` without `make embed` | deletes tracked `ui_dist/` and breaks the build |
| trust `sandbox.runtime == "ok"` | it is a bare `os.Stat` |
| trust a green `run.py` in isolated mode without `--rebuild` | the image may be months old |
| trust `$?` from `sdk-test.sh` | it is the last cleanup curl's status |
| compare `$?` to 1 for the `exit $FAIL` suites | the code is the failure *count* |
| run any `orva` command without `--endpoint`/`--api-key` | it silently targets `~/.orva/config.yaml` — your real instance (§4) |
| run `run-all.sh` (or `atscale.sh`) against an instance you care about | zero `DELETE` calls; 20 functions stay forever |

**Error slugs you will meet**

| Slug | HTTP | Means |
|---|---|---|
| `SANDBOX_ERROR` | 503 | nsjail or the rootfs is missing — host problem |
| `WORKER_CRASHED` | 502 | the worker spawned and died — check execution stderr |
| `TIMEOUT` | 504 | exceeded `timeout_ms` |
| `PAYLOAD_TOO_LARGE` | 413 | over the 6 MB JSON cap (deploy-inline is exempt) |
| `POOL_AT_CAPACITY` | 503 | pool saturated |
| `NOT_ACTIVE` | 409 | the function is not `active` |
| `VALIDATION` | 400 | bad field value |
| `INVALID_JSON` | 400 | unparseable body |
| `FORBIDDEN` | 403 | key lacks the permission |
| `SIGNATURE_INVALID` | 401 | inbound-webhook HMAC mismatch |

Full catalog: [`docs/ERRORS.md`](ERRORS.md).
