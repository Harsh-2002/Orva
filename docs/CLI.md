# Orva CLI

On the internal managed server only, `orva functions create --managed --name
NAME --runtime node|python` creates a fresh unbound function. Do not combine
`--managed` with legacy SDK or resource flags. `orva deployments install
DEPLOYMENT_UUID` installs the exact published generation without approving
policy, releasing standing work, or changing deployment history. There is no
legacy fallback for either command.

The `orva` CLI is a slim HTTP client for any Orva server. Same Cobra command
surface as the daemon binary's CLI side, but ~70 % smaller because it doesn't
carry the server (no SQLite driver, nsjail, MCP server, OAuth, embedded UI,
or runtime adapters).

Released for **Linux + macOS + Windows × amd64 + arm64** on every Orva tag.

---

## Install

The Linux server binary additionally provides `orva setup --managed-witness
PATH` (create a witness only, refusing replacement) and `orva setup
--managed-layout provision|publish` (explicit fresh-instance layout steps).
Neither flag enrolls legacy workloads. See the [supervised provisioning
procedure](OPERATIONS.md#fresh-managed-provisioning).

### Offline legacy enrollment (Linux server binary only)

```sh
orva managed enroll --dry-run --plan /safe/path/orva-enrollment.json
orva managed enroll --apply --plan /safe/path/orva-enrollment.json --confirm
```

`--dry-run` is the default. The command locks the stopped instance selected
by `--data-dir` (default `ORVA_DATA_DIR`), prints a canonical JSON plan, and
optionally creates a new `--plan` file outside that instance. Applying
requires an unchanged plan and explicit confirmation. Repeat `--exclude
FUNCTION_UUID` for each unsupported function you elect to leave inactive.
See the [offline procedure](OPERATIONS.md#offline-legacy-enrollment) before
making changes. This command is not part of the shared slim CLI command tree.

### Linux + macOS

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.sh | sh
```

The installer downloads the right binary for your OS/arch, verifies its
SHA-256 against `checksums.txt`, and installs to `/usr/local/bin/orva`
(falling back to `$HOME/.local/bin/orva` when `/usr/local/bin` isn't
writable and `sudo` isn't available).

Pin a specific version:

```bash
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.sh | \
    ORVA_VERSION=vYYYY.MM.DD sh
```

> Orva keeps **one published release at a time** — the current one. Any older
> version 404s once it is superseded, which is why the example above is a
> placeholder rather than a real tag. Use `ORVA_VERSION` to pin within a
> rollout, not as a long-term hold; omit it to take the current release.

### Windows (PowerShell)

```powershell
irm https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.ps1 | iex
```

Installs to `%LocalAppData%\Programs\orva\orva.exe` and adds the directory
to your user PATH. Restart your terminal so PATH picks up the new entry.

### Manual download

If you'd rather not pipe a script into your shell, grab the right binary
directly from the [releases page](https://github.com/Harsh-2002/Orva/releases/latest):

| Platform | Asset |
|---|---|
| Linux amd64 | `orva-cli-linux-amd64` |
| Linux arm64 | `orva-cli-linux-arm64` |
| macOS Intel | `orva-cli-darwin-amd64` |
| macOS Apple Silicon | `orva-cli-darwin-arm64` |
| Windows amd64 | `orva-cli-windows-amd64.exe` |
| Windows arm64 | `orva-cli-windows-arm64.exe` |

Rename to `orva` (or `orva.exe`), `chmod +x` (Unix-likes), drop into your
PATH.

---

## First-run security prompts

The release binaries are not code-signed (yet — see "Code signing" below).
The installer scripts apply the standard mitigations automatically; you
only need the manual fallback if you downloaded a binary by hand.

### macOS

When the installer runs, `curl`-fetched files don't carry the Gatekeeper
quarantine attribute, and Go's compiler emits an ad-hoc darwin signature
that satisfies the on-launch policy. **No first-run prompt is expected.**

If you downloaded a binary in a browser (or extracted it from a `.zip`
that carries the quarantine attribute), strip it yourself:

```bash
xattr -d com.apple.quarantine /usr/local/bin/orva
```

On macOS 15 Sequoia and later, the right-click → Open shortcut no longer
works for command-line binaries. If Gatekeeper does block the binary,
remove the xattr as above, or open **System Settings → Privacy & Security**,
scroll to the bottom, and click "Open Anyway" next to the orva entry.

### Windows

PowerShell's `Invoke-WebRequest` attaches Mark-of-the-Web (MoTW) to
downloaded files, which triggers SmartScreen's "Windows protected your
PC" dialog on first run. The installer calls `Unblock-File` to strip
MoTW immediately after download; **no first-run prompt is expected.**

If `Unblock-File` failed (some EDR products block it), you'll see the
SmartScreen dialog. Click **More info → Run anyway** once; subsequent
runs are unobstructed. Or strip MoTW manually:

```powershell
Unblock-File "$env:LocalAppData\Programs\orva\orva.exe"
```

---

## Quickstart

```bash
# 1. One-time: save endpoint + API key to ~/.orva/config.yaml (mode 0600).
#    Prefer the stdin form: a key passed on the command line is visible to
#    every local user via `ps` and lands in your shell history.
printf %s "$ORVA_KEY" | orva login --endpoint https://orva.example.com --api-key -

#    (--api-key orva_… still works, with that caveat.)

# 2. Verify connectivity.
orva system health

# 3. Deploy + invoke a function.
orva deploy ./my-fn --name my-fn --runtime node
orva invoke my-fn --body '{"hello":"world"}'

# 4. See what happened.
orva logs my-fn --follow
```

Everything past this point is detail. The full command surface, common
workflows, and scripting patterns are in the sections below.

---

## Global flags

These persistent flags apply to **every** command (and every
subcommand). They sit alongside the connection flags documented under
[Configuration](#configuration) below.

| Flag | Default | What it does |
|---|---|---|
| `-o, --output <table\|json>` | `table` | Output format. `table` is human-readable; `json` is machine-readable. Lists and `get`-style commands honor it. |
| `-q, --quiet` | off | Suppress status/progress messages (which go to **stderr**). Data on stdout is unaffected. |
| `--no-color` | auto | Disable ANSI color. Also honored via the `NO_COLOR` env var, and auto-disabled when stdout is not a TTY. |
| `-y, --yes` | off | Skip the confirmation prompt on destructive operations (delete / revoke / rotate / restore). **Required** for those ops on a non-TTY (CI, pipes), where they otherwise refuse rather than block. |

### Output contract — stdout vs stderr

The CLI splits its two kinds of output across the two streams:

- **stdout** — the *data*: function response bodies, list rows, JSON
  blobs. This is the only thing you pipe.
- **stderr** — *status*: progress lines, success confirmations, timings,
  prompts.

That means `orva <cmd> | jq` (or `> file`, or `| grep`) is always clean —
no status chatter ever lands on stdout. `-q/--quiet` silences the stderr
side entirely for fully scripted runs. Combine with `-o json` for
structured piping:

```bash
orva functions list -o json | jq -r '.functions[].name'
orva logs greeter -o json | jq '.executions[] | select(.status=="error")'
```

Non-2xx HTTP responses and local errors always set a non-zero exit code,
so `set -e` / `&&` chains behave.

---

## Configuration

The CLI reads its endpoint + API key with full **precedence** (highest
wins):

```
flags  >  environment  >  config file  >  default
```

1. **Command-line flags** — `--endpoint` and `--api-key` on every
   invocation. Useful in CI where you pass `$ORVA_API_KEY` from a
   secret store.
2. **Environment variables** — `ORVA_ENDPOINT` and `ORVA_API_KEY`. Set
   once per shell; every `orva` invocation in that shell picks them up.
   `ORVA_CONFIG` overrides the path to the config file (default
   `~/.orva/config.yaml`); a leading `~` is expanded.
3. **Config file** — `~/.orva/config.yaml` on Linux/macOS,
   `%USERPROFILE%\.orva\config.yaml` on Windows. Written by
   `orva login` with mode `0600`. Plain YAML:

   ```yaml
   endpoint: https://orva.example.com
   api_key: orva_a1b2c3...
   ```

Each field resolves independently — e.g. `ORVA_API_KEY` set in the
environment overrides the key in the config file while the endpoint
still comes from the file.

Examples of the precedence in action:

```bash
# CI: explicit flags override config, no shell state polluted.
orva --endpoint $URL --api-key $KEY functions list

# Local shell: env vars beat the config file, useful for switching
# between dev/staging quickly.
export ORVA_ENDPOINT=https://dev-orva.example.com
export ORVA_API_KEY=orva_dev_…
orva functions list                       # hits dev
unset ORVA_ENDPOINT ORVA_API_KEY
orva functions list                       # falls back to config (prod)
```

Multiple environments without juggling env vars? Drop separate config
files and point `ORVA_CONFIG` at them:

```bash
ORVA_CONFIG=~/.orva/staging.yaml orva functions list
```

`orva login --test` verifies the credentials against an authenticated
endpoint (`/api/v1/runtimes`) before writing them to disk, so a typo'd,
revoked, or expired key fails loudly instead of saving a broken config. A
valid key that lacks the `read` permission is still accepted — the server
authenticated it, it just cannot read:

```bash
printf %s "$ORVA_KEY" | orva login --endpoint https://orva.example.com --api-key - --test
```

---

## Common workflows

### Deploy a function

```bash
# Default: handler.js / handler.py based on runtime.
orva deploy ./my-fn --name greeter --runtime node

# TypeScript: server compiles at deploy time. CLI auto-detects when
# both tsconfig.json and a .ts file are present.
orva deploy ./my-fn-ts --name greeter --runtime node

# Python: pick the runtime explicitly.
orva deploy ./py-fn --name greeter --runtime python

# Watch the build: stream build logs over SSE, wait for completion, and
# exit non-zero if the build fails (great for CI gates).
orva deploy ./src --name greeter --runtime node --follow
```

### Function configuration and SDK access

`functions create` defaults to `sdk: false`. Pass `--sdk` (or `--sdk=true`)
to allow the function to use the Orva SDK for KV, function-to-function calls,
jobs, crons, and spans. This is independent of `--network-mode`: SDK access
does not enable outbound requests, and enabling egress does not enable the SDK.

```bash
orva functions create --name counter --runtime node --sdk
orva functions update counter --sdk=false
orva functions update counter --sdk=true
orva functions update counter --network-mode egress
```

`functions update` sends only explicitly supplied flags. Omitting `--sdk`
preserves the current value; use `--sdk=false` to revoke SDK access. These
commands send a JSON boolean, not a string. `functions get counter` includes
the current `sdk` value. The dashboard exposes the same setting as **Allow
Orva SDK** in the editor's Settings panel.

### Invoke

`orva invoke` runs a deployed function over HTTP. The response **body**
goes to stdout (pretty-printed on a TTY, raw bytes when piped); status
and timing go to stderr. A non-2xx HTTP response exits non-zero.

```bash
# Send a JSON body inline.
orva invoke greeter --body '{"name":"Ada"}'

# Body from a file, or from stdin with @-.
orva invoke greeter --body @payload.json
echo '{"name":"Ada"}' | orva invoke greeter --body @-

# Pipe the clean body straight to jq.
orva invoke greeter --body '{"name":"Ada"}' | jq .

# Non-default method + custom headers (repeat -H for more).
orva invoke greeter -X GET -H 'X-Trace: 1' -H 'Accept: application/json'

# Invoke a custom route instead of /fn/<id>.
orva invoke --route /webhooks/stripe --body @event.json

# Stream the response chunk-by-chunk as it arrives (generators,
# long-lived handlers). No total cap while streaming, but a 45s idle
# deadline: a handler that emits nothing for 45s is cancelled client-side.
orva invoke chat --body @prompt.json --stream

# Print the status line + response headers to stderr for debugging.
orva invoke greeter --body '{}' -i

# Per-call timeout in ms (0 = client default of 120s; ignored with --stream).
orva invoke greeter --body '{}' --timeout-ms 60000
```

`invoke` flags at a glance:

| Flag | Meaning |
|---|---|
| `-d, --body <v>` | Request body: inline string, `@file`, or `@-` for stdin |
| `-X, --method <m>` | HTTP method (default `POST`) |
| `-H, --header <h>` | Add a `'Key: Value'` header (repeatable) |
| `--route <path>` | Invoke a custom route path instead of `/fn/<id>` |
| `--stream` | Stream the response as it arrives (no client timeout) |
| `-i, --include` | Print response status line + headers to stderr |
| `--timeout-ms <n>` | Per-call timeout in ms (ignored with `--stream`) |

> **Renamed flag:** the old `--data` is now `-d/--body`.

### Logs

```bash
# Recent executions (table). Add -o json for scripting.
orva logs greeter
orva logs greeter -o json | jq .

# Follow new executions live over SSE (Ctrl-C to stop).
orva logs greeter --follow            # -f for short

# Drill into a specific execution's stdout/stderr.
orva logs greeter --exec-id 019df200-7b00-7e00-9c00-aab1cd2e3f40
```

> **Renamed flag:** the old `--tail` is now `-f/--follow`.

### Per-function state (KV)

```bash
orva kv put greeter visits --value '{"count":0}'         # JSON value
orva kv put greeter cache:home --value '"hello"' --ttl 3600
orva kv put greeter config --value @config.json          # value from file
orva kv list greeter --prefix cache:
orva kv get greeter visits
orva kv delete greeter visits
```

`kv put --value` accepts an inline string, `@file`, or `@-` (stdin).
Keys must be non-empty UTF-8 and are capped at 256 characters. Values must be
valid JSON and are capped at 64 KiB. Omitting `--ttl` preserves an existing
expiry (and creates persistent new keys); `--ttl 0` removes expiry; a positive
TTL sets or refreshes it. Negative TTLs are rejected.

Atomic counters and compare-and-swap are available too:

```bash
orva kv incr <fn> <key> --by 1 --ttl 3600
orva kv cas  <fn> <key> --expected '{"n":1}' --new '{"n":2}' --ttl 3600
```

`cas` takes JSON for both `--expected` and `--new`; pass `null` to `--expected`
to insert only if the key is absent.

### Per-function secrets

```bash
orva secrets set greeter STRIPE_KEY --value sk_live_…
orva secrets set greeter STRIPE_KEY --value @key.txt   # value from a file (or @- for stdin)
orva secrets list greeter                     # names only — values stay server-side
orva secrets delete greeter STRIPE_KEY
```

Secrets ride along with the function as `process.env.STRIPE_KEY` /
`os.environ["STRIPE_KEY"]` inside the sandbox.

### Schedules + background jobs

#### Managed workload pause and resume (optional server composition)

`orva functions pause <function-uuid> --request FILE` and
`orva functions resume <function-uuid> --request FILE` require the opt-in
server routes. Names are not accepted. Review the exact UUID `generation` and
decimal-string `policy_revision` and `authority_epoch` from the separately
enabled configuration inspection first. Put those values and `confirm: true`
in the request file (or use `--request -` for stdin). The CLI forwards the
original JSON and prints a JSON acknowledgement. It does not read or refresh
CAS values, retry, follow redirects, or fall back to legacy status changes. `--yes` skips the
CLI prompt in unattended use; it does not supply `confirm: true`.

Pause records durable intent. Resume requires live ADMIN and only publishes
an inactive review-required generation: execution and standing work have not
resumed. Fresh Factory installation and explicit source-aware review remain
separate. See [the API contract](API.md#managed-workload-pause-and-resume-opt-in-only).

#### Deleted-work history (optional server composition)

`orva history list|get|capacity|purge|reissue` delegates to the same strict
history HTTP service as optional MCP tools and history controls. Normal servers
do not register that service: an unavailable-route error is expected, not an
invitation to resume archived work through the live jobs API.

```sh
orva history list                           # first 100 metadata entries
orva history capacity                       # decimal-string use/reservations/caps
orva history get --request selector.json
orva history list --request page.json       # target_id / after:{kind,id} / limit
orva history reissue --request fresh.json --yes
orva history purge --request purge.json --yes
orva history get --request - < selector.json
```

All output is JSON on stdout, including the default output mode. Status is on
stderr; `--quiet` silences it. `--request` is an original UTF-8 JSON file, or `-`
for stdin, bounded to 1 MiB + 8 KiB. Get, purge and reissue require it; list and
capacity default to `{"limit":100}` and `{}`. Filename completion suggests JSON
files, never resolves a historical name into a new UUID. Duplicate/unknown
fields still reach the strict server validator unchanged. No automatic retry
or reconstruction from archived metadata occurs.

READ permits inspection. Purge/reissue require live ADMIN plus interactive
confirmation or `--yes`; that CLI confirmation does not insert or override the
request's mandatory `confirm:true` or `reviewed:true`. A job reissue request
contains `source:{kind:"job",id}`, an exact NEW `target_id`, fresh `payload`,
`scheduled_at` and `max_attempts`. Cron requires fresh `name`, `expression`,
`timezone` and explicit `enabled` instead. The service creates a NEW
OPERATOR-origin work ID with old-to-new audit, never resumes/adopts an archive.
Purge selects exact kind/UUIDs with `confirm:true`; 90 days is eligibility, not
TTL. Reservations cannot purge. See `docs/API.md` for the request contract.

The opt-in IAM cron fixture adds `orva cron resume <id> --fn <owner-uuid>
--config-revision <decimal> --authority-generation <decimal>`. It requires current
admin and resumes the original authority at the next future fire, without
catch-up or adoption. Providing owner UUID and exact revisions lets an admin-only
credential mutate without requiring read; omitting `--fn` performs a read-authorized
owner lookup. Inspection remains read-gated.
A function name in `--fn` also requires read for name resolution; only the UUID
fast path skips that lookup.

`cron update` and `cron delete` accept the same revision flags, required by the
explicit IAM fixture. They never replace the supplied CAS with a fresh lookup.
Delete retains the normal `--yes`/interactive confirmation convention and sends
`confirm=true` to the staged endpoint. Use `cron list -o json` to inspect authority,
source and revisions without rounding. Normal-serving resume is still a 404:
these routes are fixture-only until coordinated cut-over.

The CLI also has `orva jobs resume <id>` for the explicit IAM jobs fixture only.
It requires live admin authority and revalidates the job's original creator and
target; it never adopts function work or reopens terminal/quarantined jobs.
Normal serving has no resume route yet (404 is expected). The fixture also
requires admin for enqueue/retry/delete; this is not yet a normal API gate change.
Use `jobs get/list -o json` to inspect authority state, reason and source metadata.

```bash
# Cron: fire greeter at 09:00 IST every day.
orva cron create --fn greeter --expr "0 9 * * *" --tz Asia/Kolkata \
    --payload '{"task":"daily-roundup"}'

# Enqueue a job with idempotency (safe for retries).
orva jobs enqueue --fn send-welcome \
    --data '{"to":"ada@example.com"}' \
    --idempotency-key welcome:ada@example.com \
    --idempotency-window 86400

orva jobs list --status failed
orva jobs retry <job-uuid>
```

### Backup + restore

The single-file snapshot includes the DB, every deployed function
version, the secrets master key, and the bootstrap admin key — restore
on a fresh host and the install boots up byte-faithful.

```bash
# Download a snapshot. Default filename: orva-backup-<RFC3339>.tar.gz.
orva backup download
orva backup download -f /backups/orva-$(date +%F).tar.gz

# Restore. --yes is mandatory; the bare command refuses with a prompt.
orva backup restore /backups/orva-2026-05-15.tar.gz --yes
```

`backup download` also works against a managed instance with admin authority
and writes a managed v2 archive. The online `backup restore` command is legacy
only. With the **server binary** on a stopped destination, run
`orva managed restore --archive FILE [--data-dir DIR]` for a dry run and add
`--confirm` to rebuild into a fresh local Owner. Add `--same-owner` only when
replaying on the original instance with its sealed stores intact. See the
[managed backup and restore runbook](OPERATIONS.md#managed-mode-portable-recovery).

After a successful restore the server exits with status **70** so its supervisor
(systemd / OpenRC / `docker restart: unless-stopped`) reopens
the new files. The CLI sees a connection reset — that's the expected happy-path
signal. Reconnect in ~5 seconds.

> ⚠️ **Non-zero on purpose.** It used to exit 0, and systemd's
> `Restart=on-failure` reads 0 as "the job finished" and leaves the service
> **down** — so a successful restore was an outage on bare metal. Docker's
> `restart: unless-stopped` hides this. The shipped unit now carries
> `RestartForceExitStatus=70`; a unit created before that needs the line
> added, or re-run `install.sh`. On Alpine/OpenRC the equivalent is
> `supervisor="supervise-daemon"` in `/etc/init.d/orva` — `start-stop-daemon`
> never respawns, so the same restore left the host down there too. Check with

> The downloaded archive is written mode `0600`, because it contains the
> database, every function version, the secrets master key and the bootstrap
> admin key.

> ⚠️ Backup archives contain `keys/master.key`. Treat the file as
> sensitive (encrypted disk, S3 + SSE, etc.). Same posture as a password
> manager export.

### Compare past versions

`orva diff` produces a git-style unified diff between two past
**succeeded** deployments. Without `--from` / `--to`, defaults pick the
currently-active deployment as the *to* side and the most recent earlier
deployment with a different `code_hash` as the *from* side — so a
no-arg invocation almost always shows the last meaningful code change.

```bash
# Default: previous distinct version → active. ANSI-colored on TTY.
orva diff greeter

# Pin both sides explicitly (deployment IDs come from the dashboard's
# Versions modal or `orva functions get greeter` deployment history).
orva diff greeter \
    --from 019df210-1234-7000-8000-deadbeef0001 \
    --to   019df210-1234-7000-8000-deadbeef0007

# Strip color for log capture or pipe to `less -R` for paging.
orva diff greeter --no-color | tee greeter.diff
orva diff greeter | less -R

# Structured output for scripting (file list + raw before/after blobs).
orva diff greeter \
    --from 019df210-1234-7000-8000-deadbeef0001 \
    --to   019df210-1234-7000-8000-deadbeef0007 \
    -o json | jq '.files[].path'
```

The unified output skips `node_modules` / `__pycache__` and TypeScript
compiled output — only the handler source + dependency manifest
(`package.json` or `requirements.txt`) are diffed.

### Routes (custom URLs)

The path is a **positional** argument; `--fn` names the target function.
Prefix routes end in `/*`, and `--methods` restricts the HTTP verbs
(default `*`).

```bash
# /webhooks/stripe → stripe-handler
orva routes set /webhooks/stripe --fn stripe-handler
orva routes set /api-proxy/* --fn proxy --methods GET,POST
orva routes list
orva routes delete /webhooks/stripe
```

On a **managed** server (`/api/v1/iam/capabilities` reports `managed: true`)
the mapping is a versioned public exposure, and the command behaves
differently in three ways:

- `--fn` must be the function's exact lowercase UUID. Names are not resolved
  and no other spelling (uppercase, braces, unhyphenated) is accepted — get
  the id from `orva functions list -o json`.
- Every write reads the mapping and its owner authority first and sends that
  `version` + `expected` pair unchanged (`GET /api/v1/exposures`). `--expected-version`
  replaces the version token with one you supply, which is how you deliberately
  fail the CAS; the authority tokens always come from the read.
- A `409` means the mapping or its owners changed. Nothing is retried: inspect
  the current row (the command prints it) and run again. `routes list` adds
  `VERSION` and `PUBLISHED` columns and reports a truncated list at 256 rows.

```bash
# Managed: exact owner UUID, and an explicitly known version token.
orva routes set /hooks/stripe \
    --fn 0190000a-0000-7000-8000-000000000001 \
    --expected-version 7
orva routes list -o json | jq '.routes[] | select(.published == false)'
```

### Inbound webhook triggers

One function, one signature format, one URL at `/webhook/<trigger-id>`.
Create prints the plaintext signing secret to stdout **once**; nothing can
recover it afterwards, so capture it in the same step. Rotation is delete and
recreate.

```bash
orva webhooks inbound list payments
orva webhooks inbound create payments --name github-deploys --format github
orva webhooks inbound update payments <trigger-id> --active=false
orva webhooks inbound delete payments <trigger-id> --yes
```

`update` changes only the flags you pass (`--name`, `--format`, `--header`,
`--active`). On a managed server the owner and trigger must be exact lowercase
UUIDs, the trigger's version and owner authority are read immediately before
the write, a `409` is never retried, and `--expected-version` overrides the
version token just read. Managed `list` adds `VERSION` and `PUBLISHED` columns.

`orva webhooks inbound test` signs locally with `--secret` and POSTs to the
trigger URL, so it works on a managed server too — but only for a *published*
trigger, and the secret must be the one captured at create time.

### API keys

```bash
# Long-lived bearer for CI / a script / an AI agent.
orva keys create --name ci-deploy --permissions invoke,write
# Auto-expiring key (0 = never).
orva keys create --name temp-agent --permissions invoke --expires-in-days 30
orva keys list
orva keys revoke <key-uuid>                    # prompts; pass --yes to skip
```

### Channels (curated MCP toolboxes)

> **Requires an `admin` key.** A channel token is a long-lived bearer
> credential whose tools bypass the target function's `auth_mode`, so minting
> one is gated like minting an API key. This changed: it used to need only
> `write`, which let a write-scoped key issue itself a credential that
> outranked it.


```bash
# Bundle N functions under a name + a static bearer token. Presenting
# that token at /mcp exposes only those functions as MCP tools.
orva channels create customer-support \
    --description "Tools the support agent can use" \
    --functions lookup-user,refund,resend-receipt

orva channels show customer-support      # prefix only; the token is shown
                                         # once at create, and again on rotate
orva channels rotate customer-support    # invalidates the old token
```

### Activity stream

```bash
# Recent rows.
orva activity --limit 100

# Live tail — every API call, CLI command, MCP tool invoke, webhook delivery.
orva activity --follow                    # -f for short
orva activity --follow --source mcp       # MCP-only firehose
```

### System diagnostics

```bash
orva system health        # version, uptime, sandbox stats
orva system metrics       # JSON snapshot used by the dashboard
orva system storage       # on-disk breakdown (orva.db, WAL, functions/, total)
orva system db-stats      # SQLite page detail — free pages a VACUUM would reclaim
orva system vacuum        # compact orva.db (briefly blocks writes)
```

### Deployment history + rollback

Every deploy creates a deployment record (status,
content-addressed `code_hash`, append-only build log).

```bash
# Audit what shipped when.
orva deployments list greeter
orva deployments get 019df210-…-deadbeef0001

# Read or live-stream a build log.
orva deployments logs 019df210-…-deadbeef0001
orva deployments logs 019df210-…-deadbeef0001 --follow

# Roll back. Bare form undoes the last code change; or pin a deployment
# id / content hash. Prompts for confirmation — pass --yes to skip.
orva rollback greeter
orva rollback greeter 019df210-…-deadbeef0001
orva rollback greeter --code-hash 9f8e7d…
```

With the optional managed-deployment fixture, pass an exact deployment UUID,
not `--code-hash`. An unqualified CLI rollback can still select a concrete UUID
client-side. Managed promotion keeps current configuration and stays inactive
until a separate host installation and policy review; the CLI reports that
state rather than implying the function is ready.

### Fixtures (saved request presets)

A fixture bundles a method, sub-path, headers, and body under a name so
you can replay a request without retyping it — the same presets the
dashboard's test workbench saves under **Saved requests**
(`/web/functions/<name>/test`) and the `test_function_with_fixture` MCP
tool reads.

```bash
orva fixtures list greeter
orva fixtures save greeter happy-path --method POST --body '{"name":"ada"}'
orva fixtures save greeter search --method GET --path /search --query 'q=hi'
orva fixtures get greeter happy-path
orva fixtures test greeter happy-path | jq .    # run the fixture
orva fixtures delete greeter happy-path
```

`fixtures save --body` accepts inline / `@file` / `@-` like `invoke`.

### Traces

Every execution is a span; spans sharing a `trace_id` form a causal tree.

```bash
orva traces list                       # recent trace-wide summaries
orva traces list --fn greeter --limit 20
orva traces get tr_01h…                 # full span tree
orva traces baseline greeter            # rolling p95/p99/mean latency
```

### Egress policy (`orva firewall`)

> **Requires an `admin` key** for every subcommand, reads included. Egress
> blocklists and sandbox DNS are instance-wide security state, so they are
> gated like backup and key minting. This changed: the surface used to be
> `read`/`write`.


The blocklist applied to every sandbox running with `network_mode=egress`.
A rule is a **CIDR** (or bare IP) or an **exact hostname**; hostnames are
resolved to addresses on a timer. Built-in rules can be toggled but not
deleted; custom rules are fully editable.

Mutations take effect on the **next** spawn: the policy is loaded by nsjail
once at worker start, so saving a rule publishes a new policy generation and
retires the warm egress workers (rate-limited to one recycle per 60 s). Expect
a few seconds and a cold start.

```bash
orva firewall list
orva firewall add 10.42.0.0/16 --label "internal net"
orva firewall add example.com --type hostname --disabled
orva firewall enable 7
orva firewall disable 7
orva firewall delete 7                                 # prompts; --yes to skip
orva firewall resolve example.com                      # re-resolve hostnames now
```

**Wildcard patterns are not supported.** `orva firewall add '*.corp.com'`
fails with `400 VALIDATION`, as does enabling an existing wildcard row: packet
rules match addresses, not DNS names. Any wildcard row already in the table is
left as-is, excluded from the compiled policy, and reported under
`unenforced_rules` in the status output. Use a CIDR or an exact hostname.

**`orva firewall add 10.0.0.0/8` also blocks orvad itself.** The daemon's own
outbound calls are filtered by the same rules, so a broad RFC1918 block cuts
off internal package mirrors, LAN-hosted AI providers, and internal webhook
targets. There is no allow-exception to add on top — block a narrower CIDR
instead. See [`SECURITY.md`](SECURITY.md#sandbox-egress-policy).

### Sandbox DNS

The DNS config handed to egress-enabled sandboxes. Servers are literal
resolver IPs; host overrides pin a name to an IP via `/etc/hosts`.

```bash
orva dns get
orva dns set --server 1.1.1.1 --server 8.8.8.8 --search corp.internal
orva dns set --host db.internal=10.0.0.5 --host cache=10.0.0.6
orva dns set --server "" --search ""                   # reset to defaults
```

### Warm-pool autoscaler

> `idle_ttl_seconds` applies **only** when `scale_to_zero` is enabled — it is
> the grace period before dropping the last worker, not a general idle-recycle
> timer. With the default `scale_to_zero: false` it has no effect.


Per-function warm-sandbox tuning. Always pass `--fn`. Only the fields you
specify change; the rest keep their current values.

```bash
orva pool get --fn greeter
orva pool set --fn greeter --min-warm 2 --max-warm 20 --scale-to-zero
orva pool set --fn greeter --idle-ttl 300
```

### Background jobs + webhook deliveries

```bash
orva jobs get <job-uuid>                       # inspect one job
orva webhooks deliveries <subscription-uuid> -o json    # delivery history for a subscription
orva webhooks retry <delivery-uuid>                 # retry a failed delivery
```

### AI access

The in-product AI assistant remains available in the dashboard under **AI**.
External AI agents can use Orva through the MCP endpoint; the CLI does not
provide a chat client.

### Reference docs (`orva docs`)

```bash
orva docs            # render the full Orva reference, paged through $PAGER
orva docs --raw      # raw markdown (for grep / redirect)
orva docs | grep -i webhook
```

`orva docs` ships the same reference the dashboard and the AI assistant use,
embedded in the binary — no network needed.

---

## Command reference

Every subcommand at a glance. Run `orva <cmd> --help` for full flags.

| Command | What it does |
|---|---|
| `orva login [--test]` | Save endpoint + API key to `~/.orva/config.yaml` |
| `orva functions list / get / create / update / delete / purge-cache` | Function lifecycle. `delete` waits for the server with no client timeout, since a function with a large history can take minutes; Ctrl-C stops waiting, not the delete, and re-running the command resumes one the server left `deleting` |
| `orva deploy <path> [--follow]` | Build + deploy a function from a directory |
| `orva invoke <name>` | Run a function once and print the response |
| `orva logs <name> [--follow \| --exec-id]` | Execution history, live tail, or single-row drill-down |
| `orva deployments list / get / logs` | Deployment history + per-deploy build logs |
| `orva rollback <fn> [id \| --code-hash]` | Roll back to a prior deployment |
| `orva kv get / put / list / delete / incr / cas` | Per-function key/value store with optional TTL, atomic counters, and compare-and-swap |
| `orva executions list / get / logs / delete / prune / replay` | Execution records across functions; `prune` is bulk-destructive and needs `--older-than`, `--status`, or `--function` |
| `orva secrets set / list / delete` | Per-function encrypted secrets (AES-256-GCM) |
| `orva fixtures list / get / save / delete / test` | Reusable request presets per function |
| `orva cron create / list / update / delete` | Per-function schedules with timezone support |
| `orva jobs enqueue / list / get / retry / delete` | Durable background queue with idempotency |
| `orva keys create / list / revoke` | Long-lived API keys (`--expires-in-days`) |
| `orva channels create / show / rotate / add-functions / remove-functions / delete` | MCP toolbox bundles |
| `orva routes set / list / delete` | Custom URL → function mappings. On a managed server the owner is an exact UUID and each write sends the inspected version/authority tokens (`--expected-version` overrides the version); no automatic retry after 409 |
| `orva webhooks create / list / test / delete / deliveries / retry` | Outbound system-event subscriptions |
| `orva webhooks inbound list / create / update / delete / test` | Inbound signed-POST triggers (GitHub, Stripe, etc.). Managed servers take exact owner/trigger UUIDs and CAS tokens; `create` prints the secret once |
| `orva traces list / get / baseline` | Distributed traces + latency baselines |
| `orva firewall list / add / enable / disable / delete / resolve` | Sandbox egress policy rules (CIDR or exact hostname) |
| `orva dns get / set` | Sandbox DNS config |
| `orva pool get / set` | Per-function warm-pool autoscaler config |
| `orva backup download / restore` | Point-in-time snapshot + restore |
| `orva diff <name> [--from --to] [-o json] [--no-color]` | Git-style unified diff between two past deployments |
| `orva activity [--follow \| --source X]` | Audit log: every API call, CLI command, MCP invoke |
| `orva system health / metrics / db-stats / storage / vacuum` | Diagnostics + maintenance |
| `orva docs [--raw]` | Render the Orva reference documentation in the terminal |
| `orva upgrade [--check] [--force] [--data-dir DIR]` | Self-update from the latest GitHub release |
| `orva completion <shell>` | Emit a completion script (see below) |
| `orva --version` | Build identity (matches `/api/v1/system/health`) |

Every command honors the global flags — `--endpoint` / `--api-key`,
`-o/--output`, `-q/--quiet`, `--no-color`, `-y/--yes` — and the env-var /
config-file fallbacks documented in **Configuration** above.

---

## Best practices

**Scripting with JSON output.** Pass `-o json` and pipe through `jq`.
Because data goes to stdout and status to stderr, the pipe is always
clean:

```bash
# Function id by name.
fid=$(orva functions list -o json | jq -r '.[] | select(.name=="greeter").id')

# Invoke and inspect the body directly (the body IS stdout).
orva invoke greeter --body '{}' | jq .

# Use the exit code: non-2xx HTTP → non-zero exit.
orva invoke greeter --body '{}' > /dev/null && echo ok || echo failed
```

**Exit codes.** `orva` exits non-zero on transport errors, HTTP 4xx/5xx
responses from the server, and any local validation failure (missing
required flag, malformed JSON, etc.). CI scripts can rely on a simple
`set -e` or `&& / ||` chain.

**Idempotent re-runs.** Most "create" operations refuse to clobber:
`orva keys create --name ci-deploy` twice produces an error, not a
duplicate key. `orva functions create` is similar. Use `delete` first
or treat the 409 as "already exists" in your script.

**Never echo `--api-key` into logs.** Pass it via env var or stdin so
it doesn't end up in shell history or CI logs:

```bash
ORVA_API_KEY=$(vault read -field=key kv/orva/ci) orva functions list
```

**Backup before destructive ops.** The flow is fast enough to run
inline:

```bash
orva backup download -f /tmp/pre-deploy.tar.gz \
    && orva deploy ./big-refactor --name greeter --runtime node
# If the deploy goes sideways:  orva backup restore /tmp/pre-deploy.tar.gz --yes
```

**CI pattern — separate config-free invocations.** Don't `orva login`
from CI; pass `--endpoint` + `--api-key` per command so there's nothing
to leak between jobs:

```yaml
# .github/workflows/deploy.yml
- name: deploy
  env:
    ORVA_API_KEY: ${{ secrets.ORVA_API_KEY }}
  run: |
    orva --endpoint https://orva.example.com deploy ./fn \
        --name greeter --runtime node
```

**Backup retention.** A typical homelab keeps 7 daily + 4 weekly +
12 monthly. Cron the CLI:

```cron
0 3 * * *  /usr/local/bin/orva backup download \
           -f /var/backups/orva/orva-$(date +\%F).tar.gz
0 4 * * 0  find /var/backups/orva -mtime +90 -delete
```

**Self-update in CI.** `orva upgrade` is fine for an interactive shell;
in CI, pin a version with the installer so reproducible builds stay
reproducible:

```bash
ORVA_VERSION=vYYYY.MM.DD \
  curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.sh | sh
```

**Match server + CLI versions.** Mismatched binaries usually work, but
new commands (like `orva backup`) require both sides up to
date. Confirm with:

```bash
orva --version                              # local CLI
orva system health | jq '{version, commit}' # remote server
```

---

## Shell autocompletion

The CLI's `completion` subcommand emits a script for every major shell.
The installer scripts try to drop it in the right location for your shell
automatically; if that didn't take, do it manually:

### Bash

```bash
orva completion bash > ~/.local/share/bash-completion/completions/orva
# Or, system-wide:
sudo orva completion bash > /etc/bash_completion.d/orva
```

### Zsh

```bash
mkdir -p ~/.zsh/completions
orva completion zsh > ~/.zsh/completions/_orva
# Add to ~/.zshrc if not already present:
#   fpath=(~/.zsh/completions $fpath)
#   autoload -U compinit && compinit
```

### Fish

```bash
orva completion fish > ~/.config/fish/completions/orva.fish
```

### PowerShell (Windows)

```powershell
$completionDir = Split-Path $PROFILE -Parent
orva completion powershell > "$completionDir\orva-completion.ps1"
# Add to your profile (open with: notepad $PROFILE)
. "$completionDir\orva-completion.ps1"
```

---

## Auto-update (`orva upgrade`)

```bash
orva upgrade --check        # is there a newer release? (also warns if a real run would be refused)
orva upgrade                # download + atomically replace the running binary
orva upgrade --force        # reinstall the latest even if versions match
orva upgrade --data-dir /srv/orva   # non-default install location
```

How it works: the command queries the GitHub releases API, picks the
asset matching your OS/arch, verifies its SHA-256 against the release's
`checksums.txt`, and replaces the running binary atomically (rename
trick on Windows, unlink-and-replace on Unix-likes).

**Server binaries upgrade themselves, not into the CLI.** The Linux server
build registers every client subcommand, `upgrade` included, so it can be run
on the server box. It fetches the server asset `orva-<os>-<arch>`, never the
slim `orva-cli-<os>-<arch>` — installing the CLI over a server would delete
`orva serve` and the service would never start again. Server assets are
published for **linux only**; on any other platform a server build refuses
rather than falling back to the CLI asset. This replaces the binary and
nothing else, so restart the service afterwards:

```bash
sudo orva upgrade
sudo systemctl restart orva        # OpenRC: rc-service orva restart
```

Re-run `install.sh` instead when the runtime adapters, rootfs or the service
unit also need refreshing — `orva upgrade` touches none of them.

**The binary and the runtime rootfs are one unit.** `install.sh` records the
release it installed in `rootfs/<runtime>/.orva-rootfs-version`, and the guard
compares that stamp — not the interpreter stamp — with the release being
installed. A server build refuses the
upgrade when an installed runtime rootfs names a different release than the one
being installed, because the adapter protocol moves with the binary and a
mismatch makes every invocation time out. Every stamped runtime is checked — a
current Node rootfs cannot hide a stale Python one. The data directory comes
from `--data-dir`, then `ORVA_DATA_DIR`, then the record `install.sh` writes to
`/etc/orva/install.conf`, then the systemd/OpenRC unit it wrote, then
`/var/lib/orva`. The refusal names every stale runtime and tells you to re-run
`install.sh`; `--force` replaces the binary anyway. `orva upgrade --check`
applies the same test and prints a `runtime rootfs is behind (…)` line instead of
recommending `orva upgrade` when a real run would be refused. A rootfs with no
stamp — the container image, where `entrypoint.sh` refreshes it — is allowed.

If the install path is not writable (e.g. `/usr/local/bin` on a system
where you installed without `sudo`), `orva upgrade` exits non-zero
with a hint:

```
install location not writable: /usr/local/bin/orva
hint: re-run with `sudo orva upgrade` if the binary lives in a system path like /usr/local/bin
```

It **does not** silently elevate. This is deliberate — re-execing under
sudo from a downloaded binary surprises users and breaks CI scripts.

To upgrade-by-reinstall instead of `orva upgrade`, re-run the installer
one-liner. The installer is idempotent.

---

## Uninstall

```bash
# Linux + macOS
sudo rm /usr/local/bin/orva
# or, if installed to ~/.local/bin:
rm $HOME/.local/bin/orva
```

```powershell
# Windows
Remove-Item "$env:LocalAppData\Programs\orva\orva.exe"
# Optional: remove the directory from user PATH via
# [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
```

Config at `~/.orva/config.yaml` (or `$env:USERPROFILE\.orva\config.yaml`
on Windows) persists across reinstalls. Delete it manually if you want
a fully clean slate.

---

## Slim CLI vs server binary

The `orva` you install via `install-cli.sh` / `install-cli.ps1` is the
**slim CLI build** — it can talk to any remote orvad, but it doesn't
have `orva serve`, `orva setup`, or `orva managed enroll`. Those live in the
server binary at `/opt/orva/bin/orva` after a `scripts/install.sh`
deployment.

If you've installed both on the same Linux box they compete for the same path
rather than ordering themselves: the server installer symlinks
`/usr/local/bin/orva` at `/opt/orva/bin/orva`, and the CLI installer writes a
real binary there. **Whichever ran last owns the path** — there is no PATH
precedence between them.

That matters only for the server-only subcommands: both binaries expose the
same surface for talking to a remote server, so day-to-day behaviour is
identical, but `serve`, `setup` and `managed enroll` are server-only. Run
`orva --help` if you are unsure which one you have — the slim CLI does not list
them. The slim build is just smaller.

| | Slim CLI (`/usr/local/bin/orva`) | Server binary (`/opt/orva/bin/orva`) |
|---|---|---|
| Linux | ✅ (amd64, arm64) | ✅ (amd64, arm64) |
| macOS | ✅ (amd64, arm64) | ❌ (nsjail is Linux-only) |
| Windows | ✅ (amd64, arm64) | ❌ |
| Size | ~20 MB | ~55 MB |
| `orva serve` | ❌ | ✅ |
| `orva managed enroll` | ❌ | ✅ |
| `orva setup` | ❌ | ✅ |
| All other subcommands | ✅ | ✅ |

---

## Code signing — current status and plan

Release binaries today are **unsigned**. The mitigations above (xattr
strip on macOS, `Unblock-File` on Windows) cover the common cases
without paying for code-signing certificates.

We'll revisit when usage justifies the cost. Concrete revisit triggers:
- 3+ "Windows blocked your installer" reports in a single month.
- 500+ installs per week across all platforms.

Planned path when triggered:
1. **Windows**: apply to [SignPath Foundation](https://signpath.org/)'s
   free OSS signing program (Windows OV cert; ~2-week onboarding).
2. **macOS**: enroll in the Apple Developer Program ($99/yr) for
   notarization.
3. **EV cert** later if SmartScreen reputation never accrues from OV
   alone.

Until then, the documented xattr / `Unblock-File` workflow is the
supported path.
# Staged `orva iam` commands

`orva iam policies` provides list/get/create/update/impact/attach/detach/revoke/
delete; `orva iam permissions` provides get/put/review. The commands require an
explicitly activated compatible scratch endpoint; normal serving routes remain
off. Mutations read bounded exact JSON from `--request @file` or `@-`, never inline
argv. Destructive operations require normal confirmation plus `confirm:true`.
All payloads use JSON stdout. See [IAM_MANAGEMENT.md](IAM_MANAGEMENT.md) for exact
request shapes, decimal counters, pinned versions and source-aware inspection.
