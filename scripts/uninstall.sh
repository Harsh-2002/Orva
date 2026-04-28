#!/bin/sh
# Orva uninstaller. Removes the binary install, the systemd/OpenRC unit,
# and (with --purge) the data directory.

set -eu

PREFIX="${ORVA_PREFIX:-/opt/orva}"
DATA_DIR="${ORVA_DATA_DIR:-/var/lib/orva}"
SERVICE_USER="orva"
PURGE=0

for a in "$@"; do
    case "$a" in
        --purge) PURGE=1 ;;
        -h|--help)
            cat <<EOF
Usage: uninstall.sh [--purge]

  --purge   also remove $DATA_DIR (functions, deployments, secrets, sessions)
EOF
            exit 0
            ;;
    esac
done

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
have() { command -v "$1" >/dev/null 2>&1; }

if [ "$(id -u)" -ne 0 ]; then
    if have sudo; then exec sudo sh "$0" "$@"; else echo "must run as root" >&2; exit 1; fi
fi

if have systemctl && [ -f /etc/systemd/system/orva.service ]; then
    log "stopping + disabling systemd unit"
    systemctl stop orva 2>/dev/null || true
    systemctl disable orva 2>/dev/null || true
    rm -f /etc/systemd/system/orva.service
    systemctl daemon-reload
fi

if [ -f /etc/init.d/orva ]; then
    log "removing OpenRC unit"
    /etc/init.d/orva stop 2>/dev/null || true
    have rc-update && rc-update del orva default 2>/dev/null || true
    rm -f /etc/init.d/orva
fi

log "removing $PREFIX"
rm -rf "$PREFIX"

if [ "$PURGE" = "1" ]; then
    log "removing $DATA_DIR (--purge)"
    rm -rf "$DATA_DIR"
    if id -u "$SERVICE_USER" >/dev/null 2>&1; then
        log "removing user $SERVICE_USER"
        userdel "$SERVICE_USER" 2>/dev/null || deluser "$SERVICE_USER" 2>/dev/null || true
    fi
else
    log "preserved $DATA_DIR (re-run with --purge to remove)"
fi

log "done"
