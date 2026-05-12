#!/bin/sh
# install-cli.sh — install the standalone Orva CLI on Linux or macOS.
#
# One-liner:
#   curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install-cli.sh | sh
#
# Env vars:
#   ORVA_VERSION=vYYYY.MM.DD   pin a specific release (default: latest)
#   ORVA_CLI_PATH=/path/orva   override install destination (default: /usr/local/bin/orva,
#                              fallback $HOME/.local/bin/orva when /usr/local/bin isn't writable)
#   ORVA_INSTALL_COMPLETION=0  skip shell-completion install
#   ORVA_REPO=owner/name       override the GitHub repo (default: Harsh-2002/Orva)
#
# Re-runnable: behaves like an upgrade if the binary already exists.

set -eu

REPO="${ORVA_REPO:-Harsh-2002/Orva}"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxxx\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# ── Detect OS + architecture ────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
    linux|darwin) ;;
    *) die "this installer is for Linux/macOS only (detected: $OS); on Windows use install-cli.ps1" ;;
esac

RAW_ARCH=$(uname -m)
case "$RAW_ARCH" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $RAW_ARCH (released: amd64, arm64)" ;;
esac

log "detected: $OS/$ARCH"

# ── Resolve version ─────────────────────────────────────────────────────
# Honor a $GITHUB_TOKEN or $GH_TOKEN if present (CI/operator-supplied) to
# bypass the unauthenticated 60/hr/IP rate limit on api.github.com — the
# macOS / windows runners share IPs across heavy traffic, and a 403 from
# the API is the most common cause of "installer suddenly broken" in CI.
api_auth_header() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        printf 'Authorization: Bearer %s' "$GITHUB_TOKEN"
    elif [ -n "${GH_TOKEN:-}" ]; then
        printf 'Authorization: Bearer %s' "$GH_TOKEN"
    fi
}

if [ -n "${ORVA_VERSION:-}" ]; then
    VERSION="$ORVA_VERSION"
else
    log "fetching latest release tag from GitHub"
    auth=$(api_auth_header)
    if [ -n "$auth" ]; then
        VERSION=$(curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
            -H "$auth" \
            "https://api.github.com/repos/${REPO}/releases/latest" \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
    else
        VERSION=$(curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
            "https://api.github.com/repos/${REPO}/releases/latest" \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
    fi
    [ -n "$VERSION" ] || die "could not resolve latest tag (set ORVA_VERSION explicitly, or supply GITHUB_TOKEN for rate-limit relief)"
fi
log "version: $VERSION"

BASE="https://github.com/${REPO}/releases/download/${VERSION}"

# ── Pick install destination ────────────────────────────────────────────
if [ -n "${ORVA_CLI_PATH:-}" ]; then
    DEST="$ORVA_CLI_PATH"
elif [ -w /usr/local/bin ] || ([ "$(id -u)" -eq 0 ] && [ -d /usr/local/bin ]); then
    DEST=/usr/local/bin/orva
elif have sudo && [ -d /usr/local/bin ]; then
    DEST=/usr/local/bin/orva
else
    # Fall back to user-local — never silently elevate.
    mkdir -p "$HOME/.local/bin"
    DEST="$HOME/.local/bin/orva"
fi
log "install destination: $DEST"

# ── Download + verify checksum ──────────────────────────────────────────
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

ASSET="orva-cli-${OS}-${ARCH}"
log "downloading $ASSET"
curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
    -o "$tmp/orva" "$BASE/$ASSET"
curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
    -o "$tmp/checksums.txt" "$BASE/checksums.txt"

log "verifying SHA-256"
# Shasum tooling differs (sha256sum on Linux, shasum -a 256 on macOS).
if have sha256sum; then
    SUM_CMD="sha256sum"
elif have shasum; then
    SUM_CMD="shasum -a 256"
else
    die "no sha256sum / shasum binary; install one and re-run"
fi

(cd "$tmp" \
    && grep " ${ASSET}\$" checksums.txt | sed "s/${ASSET}/orva/" | $SUM_CMD -c -) \
    || die "checksum verification failed for $ASSET"

# ── Install ─────────────────────────────────────────────────────────────
install_binary() {
    install -m 0755 "$tmp/orva" "$DEST" 2>/dev/null
}

if ! install_binary; then
    if [ "$(id -u)" -ne 0 ] && have sudo; then
        log "elevating to root via sudo to write $DEST"
        sudo install -m 0755 "$tmp/orva" "$DEST"
    else
        die "cannot write $DEST; re-run as root or set ORVA_CLI_PATH=\$HOME/.local/bin/orva"
    fi
fi

# macOS Gatekeeper:
# curl-downloaded binaries don't carry the quarantine xattr (only browser/
# AirDrop/Mail-downloaded files do), and Go 1.16+ emits an ad-hoc darwin
# signature so a curl-installed binary executes without prompts. We strip
# the xattr defensively in case the user fetched the script via a browser
# then ran it from disk.
if [ "$OS" = "darwin" ]; then
    if have xattr; then
        xattr -d com.apple.quarantine "$DEST" 2>/dev/null || true
    fi
fi

VERSION_OUT=$("$DEST" --version 2>/dev/null || echo "orva $VERSION")
log "installed: $VERSION_OUT"

# ── Verify destination on PATH ──────────────────────────────────────────
DEST_DIR=$(dirname "$DEST")
case ":$PATH:" in
    *":$DEST_DIR:"*) ;;
    *) warn "$DEST_DIR is not on \$PATH — add it:"
       warn "  echo 'export PATH=\"$DEST_DIR:\$PATH\"' >> ~/.profile" ;;
esac

# ── Shell completion (best-effort) ──────────────────────────────────────
if [ "${ORVA_INSTALL_COMPLETION:-1}" = "1" ]; then
    SHELL_NAME=$(basename "${SHELL:-}" 2>/dev/null || true)
    case "$SHELL_NAME" in
        bash)
            # Try the user-writable bash-completion path; fall back to a
            # snippet the user can source from their rc file.
            for d in "$HOME/.local/share/bash-completion/completions" "$HOME/.bash_completion.d"; do
                if mkdir -p "$d" 2>/dev/null && [ -w "$d" ]; then
                    "$DEST" completion bash > "$d/orva" 2>/dev/null \
                        && log "bash completion → $d/orva" && break
                fi
            done
            ;;
        zsh)
            # Compsys looks at fpath; ~/.zsh/completions is a common convention.
            ZSH_COMP_DIR="${ZDOTDIR:-$HOME}/.zsh/completions"
            if mkdir -p "$ZSH_COMP_DIR" 2>/dev/null; then
                "$DEST" completion zsh > "$ZSH_COMP_DIR/_orva" 2>/dev/null \
                    && log "zsh completion → $ZSH_COMP_DIR/_orva (add fpath=($ZSH_COMP_DIR \$fpath) to your .zshrc if not already)"
            fi
            ;;
        fish)
            FISH_COMP_DIR="$HOME/.config/fish/completions"
            if mkdir -p "$FISH_COMP_DIR" 2>/dev/null; then
                "$DEST" completion fish > "$FISH_COMP_DIR/orva.fish" 2>/dev/null \
                    && log "fish completion → $FISH_COMP_DIR/orva.fish"
            fi
            ;;
        *)
            log "shell completion: skipped (unknown shell '$SHELL_NAME')"
            log "  manual install: orva completion {bash|zsh|fish|powershell} > <path>"
            ;;
    esac
fi

cat <<EOF

══════════════════════════════════════════════════════════════════════
  orva CLI $VERSION installed → $DEST
══════════════════════════════════════════════════════════════════════

  Next:
    orva login --endpoint https://orva.example.com --api-key <key>
    orva functions list
    orva upgrade               # in-place self-update from GitHub

  Config persists at ~/.orva/config.yaml (mode 0600).

EOF
