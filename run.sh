#!/usr/bin/env bash
# run.sh — DEV-ONLY rapid loop for host development.
#
# Rebuilds Orva locally, swaps the freshly-built binary into the installed
# prefix, and restarts the systemd service. This avoids the slow
# "rebuild Docker image → recreate container" cycle while iterating.
#
# Prerequisite: a one-time bare-metal install via scripts/install.sh, which
# lays down nsjail + language rootfs + data dir + the `orva` systemd unit at
# ${ORVA_PREFIX:-/opt/orva}/bin/orva. After that, just run this.
#
# Usage:
#   ./run.sh            # backend-only: `make build`     (fast — Go only)
#   ./run.sh full       # full:        `make build-all`  (re-embeds the Vue UI)
#   ./run.sh logs       # tail the service journal (no rebuild)
#
# For pure frontend work prefer Vite hot-reload instead of this script:
#   npm --prefix frontend run dev   # :5173, proxies /api + /auth → :8443
#
# Env:
#   ORVA_PREFIX   install prefix (default /opt/orva) — must match install.sh
#   ORVA_SERVICE  systemd unit name (default orva)
#   ORVA_PORT     health-check port (default 8443)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$REPO_ROOT"

PREFIX="${ORVA_PREFIX:-/opt/orva}"
SERVICE="${ORVA_SERVICE:-orva}"
PORT="${ORVA_PORT:-8443}"
BIN_DST="$PREFIX/bin/orva"
BIN_SRC="$REPO_ROOT/build/orva"

MODE="${1:-backend}"

# sudo only when we are not already root.
SUDO=""
if [ "$(id -u)" -ne 0 ]; then SUDO="sudo"; fi

log()  { printf '\033[36m▸ %s\033[0m\n' "$*"; }
ok()   { printf '\033[32m✓ %s\033[0m\n' "$*"; }
warn() { printf '\033[33m! %s\033[0m\n' "$*"; }
die()  { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# `logs` shortcut — just follow the journal, no rebuild.
if [ "$MODE" = "logs" ]; then
    exec $SUDO journalctl -u "$SERVICE" -f -n 100
fi

# 1. Build.
case "$MODE" in
    backend|"") log "make build (backend only)"; make build ;;
    full)       log "make build-all (re-embed UI + backend)"; make build-all ;;
    *)          die "unknown mode '$MODE' (use: <none> | full | logs)" ;;
esac
[ -x "$BIN_SRC" ] || die "expected build output at $BIN_SRC — build failed?"
ok "built $($BIN_SRC --version 2>/dev/null | head -n1 || echo orva)"

# 2. Swap the binary into the installed prefix.
if [ ! -d "$PREFIX/bin" ]; then
    warn "no install found at $PREFIX/bin"
    warn "run the one-time host install first:  sudo ./scripts/install.sh"
    warn "or run the dev binary directly:        $BIN_SRC serve"
    exit 1
fi
log "installing → $BIN_DST"
$SUDO install -m 0755 "$BIN_SRC" "$BIN_DST"

# 3. Restart the service (or tell the user how to run it manually).
if command -v systemctl >/dev/null 2>&1 && $SUDO systemctl list-unit-files "$SERVICE.service" >/dev/null 2>&1 \
        && $SUDO systemctl cat "$SERVICE" >/dev/null 2>&1; then
    log "systemctl restart $SERVICE"
    $SUDO systemctl restart "$SERVICE"
else
    warn "no '$SERVICE' systemd unit — binary swapped but not restarted"
    warn "run manually:  $BIN_DST serve"
    exit 0
fi

# 4. Wait for health.
log "waiting for health on :$PORT"
url="http://localhost:$PORT/api/v1/system/health"
for _ in $(seq 1 30); do
    if curl -fsS "$url" >/dev/null 2>&1; then
        ok "healthy — $(curl -fsS "$url" 2>/dev/null | head -c 200)"
        echo
        log "tail logs with:  ./run.sh logs"
        exit 0
    fi
    sleep 1
done
warn "service did not report healthy within 30s — check: ./run.sh logs"
exit 1
