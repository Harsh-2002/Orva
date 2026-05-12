# install-cli.ps1 — install the standalone Orva CLI on Windows.
#
# One-liner:
#   irm https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.ps1 | iex
#
# Env vars:
#   $env:ORVA_VERSION = "vYYYY.MM.DD"   pin a specific release
#   $env:ORVA_CLI_DIR = "<path>"        override install directory
#                                       (default: $env:LOCALAPPDATA\Programs\orva)
#   $env:ORVA_REPO    = "owner/name"    override the GitHub repo
#                                       (default: Harsh-2002/Orva)
#
# Re-runnable: behaves like an upgrade if orva.exe already exists.

$ErrorActionPreference = "Stop"

$Repo = if ($env:ORVA_REPO) { $env:ORVA_REPO } else { "Harsh-2002/Orva" }

function Log($msg)  { Write-Host "==> $msg" -ForegroundColor Cyan }
function Warn($msg) { Write-Warning $msg }
function Die($msg)  { Write-Host "xxx $msg" -ForegroundColor Red; exit 1 }

# ── Detect architecture ─────────────────────────────────────────────────
switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $Arch = "amd64" }
    "ARM64" { $Arch = "arm64" }
    default { Die "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE) (released: amd64, arm64)" }
}
Log "detected: windows/$Arch"

# ── Resolve version ─────────────────────────────────────────────────────
if ($env:ORVA_VERSION) {
    $Version = $env:ORVA_VERSION
} else {
    Log "fetching latest release tag from GitHub"
    $latest = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
    $Version = $latest.tag_name
    if (-not $Version) { Die "could not resolve latest tag (set ORVA_VERSION explicitly)" }
}
Log "version: $Version"

$Base = "https://github.com/$Repo/releases/download/$Version"

# ── Pick install location ───────────────────────────────────────────────
$InstallDir = if ($env:ORVA_CLI_DIR) { $env:ORVA_CLI_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\orva" }
$ExePath = Join-Path $InstallDir "orva.exe"
Log "install destination: $ExePath"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# ── Download + verify ───────────────────────────────────────────────────
$Asset = "orva-cli-windows-$Arch.exe"
$Tmp   = New-TemporaryFile
$TmpExe = "$($Tmp.FullName).exe"
Remove-Item $Tmp.FullName -Force

Log "downloading $Asset"
Invoke-WebRequest -Uri "$Base/$Asset" -OutFile $TmpExe -UseBasicParsing

Log "verifying SHA-256"
$ChecksumsRaw = (Invoke-WebRequest -Uri "$Base/checksums.txt" -UseBasicParsing).Content
$WantLine = ($ChecksumsRaw -split "`n") | Where-Object { $_ -match [regex]::Escape($Asset) + '$' } | Select-Object -First 1
if (-not $WantLine) { Die "checksum entry for $Asset missing from checksums.txt" }
$Want = ($WantLine -split '\s+')[0].ToLower()
$Got = (Get-FileHash -Algorithm SHA256 $TmpExe).Hash.ToLower()
if ($Want -ne $Got) { Die "checksum mismatch: want=$Want got=$Got" }
Log "checksum OK"

# ── Move into place ─────────────────────────────────────────────────────
# If orva.exe is currently running (e.g. a self-upgrade), Move-Item will
# fail with a sharing violation. The user should re-run after the running
# process exits.
try {
    Move-Item -Force -Path $TmpExe -Destination $ExePath
} catch {
    Die "could not move orva.exe into place ($($_.Exception.Message)). Close any running orva process and re-run."
}

# Critical UX patch: Invoke-WebRequest attaches Mark-of-the-Web (Zone.Identifier
# ADS) to downloaded files, which triggers Windows SmartScreen's "Windows
# protected your PC" dialog on first run. Unblock-File deletes the ADS and
# fully suppresses the prompt — this is the single highest-value step for
# unsigned binaries on Windows.
try {
    Unblock-File -Path $ExePath -ErrorAction Stop
} catch {
    Warn "Unblock-File failed: $($_.Exception.Message). If Windows blocks orva.exe on first run, click 'More info' -> 'Run anyway'."
}

# ── PATH (user scope) ───────────────────────────────────────────────────
# User-scope PATH avoids needing UAC elevation. The change takes effect
# for new processes; existing terminals need a restart.
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    $NewPath = if ($UserPath) { "$UserPath;$InstallDir" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    Log "added $InstallDir to user PATH — restart your terminal for it to take effect"
} else {
    Log "$InstallDir already on user PATH"
}

# ── Shell completion (PowerShell only, best-effort) ─────────────────────
if ($env:ORVA_INSTALL_COMPLETION -ne "0") {
    try {
        $CompletionScript = & $ExePath completion powershell 2>$null
        if ($CompletionScript -and $PROFILE) {
            $ProfileDir = Split-Path $PROFILE -Parent
            New-Item -ItemType Directory -Force -Path $ProfileDir | Out-Null
            $CompletionFile = Join-Path $ProfileDir "orva-completion.ps1"
            $CompletionScript | Out-File -FilePath $CompletionFile -Encoding utf8
            Log "powershell completion -> $CompletionFile"
            Log "  add to your profile:  . `"$CompletionFile`""
        }
    } catch {
        # Non-fatal — completion is a nicety, not a requirement.
    }
}

$VersionOut = try { & $ExePath --version 2>$null } catch { "orva $Version" }
if (-not $VersionOut) { $VersionOut = "orva $Version" }

Write-Host @"

══════════════════════════════════════════════════════════════════════
  $VersionOut installed -> $ExePath
══════════════════════════════════════════════════════════════════════

  Next (restart your terminal first, so PATH picks up the new dir):
    orva login --endpoint https://orva.example.com --api-key <key>
    orva functions list
    orva upgrade            # in-place self-update from GitHub

  Config persists at $env:USERPROFILE\.orva\config.yaml.

"@
