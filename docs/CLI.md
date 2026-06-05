# Orva CLI

The `orva` CLI is a slim HTTP client for any Orva server. Same Cobra command
surface as the daemon binary's CLI side, but ~70 % smaller because it doesn't
carry the server (no SQLite driver, nsjail, MCP server, OAuth, embedded UI,
or runtime adapters).

Released for **Linux + macOS + Windows × amd64 + arm64** on every Orva tag.

---

## Install

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
    ORVA_VERSION=v2026.05.06 sh
```

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
orva login --endpoint https://orva.example.com --api-key orva_…

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
orva functions list -o json | jq -r '.[].name'
orva logs greeter -o json | jq '.[] | select(.status=="error")'
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

`orva login --test` verifies the credentials against the server's
`/system/health` before writing them to disk, so a typo fails loudly
instead of saving a broken config:

```bash
orva login --endpoint https://orva.example.com --api-key orva_… --test
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
orva deploy ./src --name greeter --runtime node --watch
```

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
# long-lived handlers). No client timeout while streaming.
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
Values are JSON, capped at 64 KB.

> The CLI exposes only `get / put / list / delete`. Atomic counters
> (`incr`) and compare-and-swap (`cas`) live on the internal SDK path —
> they require a per-process internal token the CLI does not hold — so
> use them from inside a function via the runtime SDK.

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
orva jobs retry job_…
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

After a successful restore the server exits cleanly so its supervisor
(systemd / `docker restart: unless-stopped`) reopens the new files.
The CLI sees a connection reset — that's the expected happy-path
signal. Reconnect in ~5 seconds.

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
orva diff greeter --from dep_…01 --to dep_…07

# Strip color for log capture or pipe to `less -R` for paging.
orva diff greeter --no-color | tee greeter.diff
orva diff greeter | less -R

# Structured output for scripting (file list + raw before/after blobs).
orva diff greeter --from dep_…01 --to dep_…07 -o json | jq '.files[].path'
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

### API keys

```bash
# Long-lived bearer for CI / a script / an AI agent.
orva keys create --name ci-deploy --permissions invoke,write
# Auto-expiring key (0 = never).
orva keys create --name temp-agent --permissions invoke --expires-in-days 30
orva keys list
orva keys revoke key_…                    # prompts; pass --yes to skip
```

### Channels (curated MCP toolboxes)

```bash
# Bundle N functions under a name + a static bearer token. Presenting
# that token at /mcp exposes only those functions as MCP tools.
orva channels create --name customer-support \
    --description "Tools the support agent can use" \
    --functions lookup-user,refund,resend-receipt

orva channels show customer-support      # prints the bearer token to share
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
orva system db-stats      # on-disk breakdown
orva system storage       # storage / VACUUM breakdown
orva system vacuum        # compact orva.db (briefly blocks writes)
```

### Deployment history + rollback

Every deploy or rollback creates a deployment record (status,
content-addressed `code_hash`, append-only build log).

```bash
# Audit what shipped when.
orva deployments list greeter
orva deployments get dep_01J…

# Read or live-stream a build log.
orva deployments logs dep_01J…
orva deployments logs dep_01J… --follow

# Roll back. Bare form undoes the last code change; or pin a deployment
# id / content hash. Prompts for confirmation — pass --yes to skip.
orva rollback greeter
orva rollback greeter dep_01J…
orva rollback greeter --code-hash 9f8e7d…
```

### Fixtures (saved request presets)

A fixture bundles a method, sub-path, headers, and body under a name so
you can replay a request without retyping it — the same presets the
dashboard's Test pane stores and the `test_function_with_fixture` MCP
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
orva traces list                       # recent root spans
orva traces list --fn greeter --limit 20
orva traces get tr_01h…                 # full span tree
orva traces baseline greeter            # rolling p95/p99/mean latency
```

### Egress firewall

The allow/block list applied to sandboxes running with
`network_mode=egress`. Rules are a CIDR, a hostname, or a `*.wildcard`.
Built-in rules can be toggled but not deleted; custom rules are fully
editable. Mutations take effect on the next sandbox spawn.

```bash
orva firewall list
orva firewall add 10.0.0.0/8 --label "internal net"
orva firewall add '*.metadata.google.internal'        # type auto-detected
orva firewall add example.com --type hostname --disabled
orva firewall enable 7
orva firewall disable 7
orva firewall delete 7                                 # prompts; --yes to skip
orva firewall resolve example.com                      # re-resolve hostnames now
```

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

Per-function warm-sandbox tuning. Always pass `--fn`. Only the fields you
specify change; the rest keep their current values.

```bash
orva pool get --fn greeter
orva pool set --fn greeter --min-warm 2 --max-warm 20 --scale-to-zero
orva pool set --fn greeter --idle-ttl 300 --target-concurrency 4
```

### Background jobs + webhook deliveries

```bash
orva jobs get job_…                       # inspect one job
orva webhooks deliveries sub_… -o json    # delivery history for a subscription
orva webhooks retry del_…                 # retry a failed delivery
```

### AI assistant (`orva chat`)

The same AI assistant as the dashboard's **AI** sidebar, in the terminal. It can
operate your instance end-to-end (list/deploy functions, read logs, manage
secrets, …). Providers, API keys, the default model, and the approval policy are
configured in the web UI under **Settings → AI**; the CLI uses that saved
selection.

```bash
# Interactive streaming REPL (banner shows the active provider/model).
orva chat
#   slash commands: /help /model /thinking /new /clear /yolo /exit
#   Ctrl-C aborts the current turn; Ctrl-D exits.

# One-shot — prints the reply to stdout and exits (pipe-friendly).
orva chat -p "list my functions and their status"
echo "what failed today?" | orva chat -p @-

# Per-session overrides (don't change the saved default):
orva chat --model gpt-4o --thinking deep -p "summarize recent errors"
```

Write/destructive tools pause for a `[y/N]` approval per the server's policy;
reads and invokes run freely. In non-interactive use (piped), a tool that needs
approval **fails closed** unless you pass `--auto-approve`. On a terminal the
reply is rendered as markdown; piped, it's plain text (`--raw` forces plain).

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
| `orva functions list / get / create / delete` | Function lifecycle |
| `orva deploy <path> [--watch]` | Build + deploy a function from a directory |
| `orva invoke <name>` | Run a function once and print the response |
| `orva logs <name> [--follow \| --exec-id]` | Execution history, live tail, or single-row drill-down |
| `orva deployments list / get / logs` | Deployment history + per-deploy build logs |
| `orva rollback <fn> [id \| --code-hash]` | Roll back to a prior deployment |
| `orva kv get / put / list / delete` | Per-function key/value store with optional TTL |
| `orva secrets set / list / delete` | Per-function encrypted secrets (AES-256-GCM) |
| `orva fixtures list / get / save / delete / test` | Reusable request presets per function |
| `orva cron create / list / update / delete` | Per-function schedules with timezone support |
| `orva jobs enqueue / list / get / retry / delete` | Durable background queue with idempotency |
| `orva keys create / list / revoke` | Long-lived API keys (`--expires-in-days`) |
| `orva channels create / show / rotate / add-functions / remove-functions / delete` | MCP toolbox bundles |
| `orva routes set / list / delete` | Custom URL → function mappings |
| `orva webhooks create / list / test / delete / deliveries / retry` | Outbound system-event subscriptions |
| `orva webhooks inbound …` | Inbound signed-POST triggers (GitHub, Stripe, etc.) |
| `orva traces list / get / baseline` | Distributed traces + latency baselines |
| `orva firewall list / add / enable / disable / delete / resolve` | Egress firewall rules |
| `orva dns get / set` | Sandbox DNS config |
| `orva pool get / set` | Per-function warm-pool autoscaler config |
| `orva backup download / restore` | Point-in-time snapshot + restore |
| `orva diff <name> [--from --to] [-o json] [--no-color]` | Git-style unified diff between two past deployments |
| `orva activity [--follow \| --source X]` | Audit log: every API call, CLI command, MCP invoke |
| `orva system health / metrics / db-stats / storage / vacuum` | Diagnostics + maintenance |
| `orva chat [-p MSG]` | Chat with the Orva AI assistant — interactive REPL, or one-shot with `-p` |
| `orva docs [--raw]` | Render the Orva reference documentation in the terminal |
| `orva upgrade` | Self-update from the latest GitHub release |
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
ORVA_VERSION=v2026.05.15 \
  curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.sh | sh
```

**Match server + CLI versions.** Mismatched binaries usually work, but
new commands (like the v0.6 `orva backup`) require both sides up to
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
orva upgrade --check        # is there a newer release?
orva upgrade                # download + atomically replace the running binary
orva upgrade --force        # reinstall the latest even if versions match
```

How it works: the command queries the GitHub releases API, picks the
asset matching your OS/arch, verifies its SHA-256 against the release's
`checksums.txt`, and replaces the running binary atomically (rename
trick on Windows, unlink-and-replace on Unix-likes).

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
have `orva serve`, `orva setup`, or `orva init`. Those live in the
server binary at `/opt/orva/bin/orva` after a `scripts/install.sh`
deployment.

If you've installed both on the same Linux box, the standalone CLI at
`/usr/local/bin/orva` takes precedence (because PATH usually puts
`/usr/local/bin` first). Both binaries expose the same CLI surface for
talking to a remote server, so behavior is identical from the user's
perspective; the slim CLI is just smaller.

| | Slim CLI (`/usr/local/bin/orva`) | Server binary (`/opt/orva/bin/orva`) |
|---|---|---|
| Linux | ✅ (amd64, arm64) | ✅ (amd64, arm64) |
| macOS | ✅ (amd64, arm64) | ❌ (nsjail is Linux-only) |
| Windows | ✅ (amd64, arm64) | ❌ |
| Size | ~20 MB | ~55 MB |
| `orva serve` | ❌ | ✅ |
| `orva setup` | ❌ | ✅ |
| `orva init` | ❌ | ✅ |
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
