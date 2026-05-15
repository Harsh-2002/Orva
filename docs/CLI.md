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
orva deploy ./my-fn --name my-fn --runtime node24
orva invoke my-fn --data '{"hello":"world"}'

# 4. See what happened.
orva logs my-fn --tail
```

Everything past this point is detail. The full command surface, common
workflows, and scripting patterns are in the sections below.

---

## Configuration

The CLI reads its endpoint + API key from three sources, in order of
precedence (highest wins):

1. **Command-line flags** — `--endpoint` and `--api-key` on every
   invocation. Useful in CI where you pass `$ORVA_API_KEY` from a
   secret store.
2. **Environment variables** — `ORVA_ENDPOINT` and `ORVA_API_KEY`. Set
   once per shell; every `orva` invocation in that shell picks them up.
3. **Config file** — `~/.orva/config.yaml` on Linux/macOS,
   `%USERPROFILE%\.orva\config.yaml` on Windows. Written by
   `orva login` with mode `0600`. Plain YAML:

   ```yaml
   endpoint: https://orva.example.com
   api_key: orva_a1b2c3...
   ```

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
files and point at them:

```bash
ORVA_CONFIG=~/.orva/staging.yaml orva functions list
```

---

## Common workflows

### Deploy a function

```bash
# Default: handler.js / handler.py based on runtime.
orva deploy ./my-fn --name greeter --runtime node24

# TypeScript: server compiles at deploy time. CLI auto-detects when
# both tsconfig.json and a .ts file are present.
orva deploy ./my-fn-ts --name greeter --runtime node24

# Python: pick the runtime explicitly.
orva deploy ./py-fn --name greeter --runtime python314
```

### Invoke + debug loop

```bash
# Send a JSON payload, see the body + duration + status.
orva invoke greeter --data '{"name":"Ada"}'

# Per-call timeout (useful for slow downstreams).
orva invoke greeter --data '{}' --timeout-ms 60000

# Tail logs while invoking from another terminal.
orva logs greeter --tail

# Drill into a specific execution.
orva logs greeter --exec-id 019df200-7b00-7e00-9c00-aab1cd2e3f40
```

### Per-function state (KV)

```bash
orva kv put greeter visits '{"count":0}'      # JSON value
orva kv put greeter cache:home '"hello"' --ttl 3600
orva kv list greeter --prefix cache:
orva kv get greeter visits
orva kv delete greeter visits
```

### Per-function secrets

```bash
orva secrets set greeter STRIPE_KEY sk_live_…
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
orva backup download -o /backups/orva-$(date +%F).tar.gz

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

### Routes (custom URLs)

```bash
# /webhook/stripe → fn_…
orva routes set --pattern /webhook/stripe --fn stripe-handler
orva routes list
orva routes delete /webhook/stripe
```

### API keys

```bash
# Long-lived bearer for CI / a script / an AI agent.
orva keys create --name ci-deploy --permissions invoke,write
orva keys list
orva keys revoke key_…
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
orva activity --tail
orva activity --tail --source mcp        # MCP-only firehose
```

### System diagnostics

```bash
orva system health        # version, uptime, sandbox stats
orva system metrics       # JSON snapshot used by the dashboard
orva system db-stats      # on-disk breakdown
orva system vacuum        # compact orva.db (briefly blocks writes)
```

---

## Command reference

Every subcommand at a glance. Run `orva <cmd> --help` for full flags.

| Command | What it does |
|---|---|
| `orva login` | Save endpoint + API key to `~/.orva/config.yaml` |
| `orva functions list / get / create / delete` | Function lifecycle |
| `orva deploy <path>` | Build + deploy a function from a directory |
| `orva invoke <name>` | Run a function once and print the response |
| `orva logs <name> [--tail \| --exec-id]` | Execution history, live tail, or single-row drill-down |
| `orva kv get / put / list / delete` | Per-function key/value store with optional TTL |
| `orva secrets set / list / delete` | Per-function encrypted secrets (AES-256-GCM) |
| `orva cron create / list / update / delete` | Per-function schedules with timezone support |
| `orva jobs enqueue / list / retry / delete` | Durable background queue with idempotency |
| `orva keys create / list / revoke` | Long-lived API keys |
| `orva channels create / show / rotate / add-functions / remove-functions / delete` | MCP toolbox bundles |
| `orva routes set / list / delete` | Custom URL → function mappings |
| `orva webhooks create / list / test / delete` | Outbound system-event subscriptions |
| `orva webhooks inbound …` | Inbound signed-POST triggers (GitHub, Stripe, etc.) |
| `orva backup download / restore` | Point-in-time snapshot + restore |
| `orva activity [--tail \| --source X]` | Audit log: every API call, CLI command, MCP invoke |
| `orva system health / metrics / db-stats / vacuum` | Diagnostics + maintenance |
| `orva upgrade` | Self-update from the latest GitHub release |
| `orva completion <shell>` | Emit a completion script (see below) |
| `orva --version` | Build identity (matches `/api/v1/system/health`) |

Every command honors the global `--endpoint` / `--api-key` flags and
the env-var / config-file fallbacks documented in **Configuration**
above.

---

## Best practices

**Scripting with JSON output.** Most subcommands print pretty JSON.
Pipe through `jq` for fields you care about:

```bash
# Function id by name.
fid=$(orva functions list | jq -r '.functions[] | select(.name=="greeter").id')

# Did the last invoke succeed?
orva invoke greeter --data '{}' | jq -e '.statusCode < 400' > /dev/null \
    && echo "ok" || echo "failed"
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
orva backup download -o /tmp/pre-deploy.tar.gz \
    && orva deploy ./big-refactor --name greeter --runtime node24
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
        --name greeter --runtime node24
```

**Backup retention.** A typical homelab keeps 7 daily + 4 weekly +
12 monthly. Cron the CLI:

```cron
0 3 * * *  /usr/local/bin/orva backup download \
           -o /var/backups/orva/orva-$(date +\%F).tar.gz
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
| Size | ~12 MB | ~20 MB |
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
