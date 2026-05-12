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

## First use

```bash
# Save your endpoint + API key to ~/.orva/config.yaml (mode 0600).
orva login --endpoint https://orva.example.com --api-key orva_…

# Everyday operations.
orva functions list
orva deploy ./my-fn --name my-fn --runtime node24
orva invoke my-fn --data '{"hello":"world"}'
orva logs my-fn --tail            # live tail via SSE
orva kv put my-fn greeting "hi"   # per-function KV
orva secrets set my-fn FOO bar    # per-function encrypted secrets
```

Config persists at `~/.orva/config.yaml`. The CLI overrides config with
the global flags `--endpoint` and `--api-key` per invocation, useful in CI:

```bash
orva --endpoint https://orva.example.com --api-key $ORVA_API_KEY \
    functions list
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
