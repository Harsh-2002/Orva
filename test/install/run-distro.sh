#!/usr/bin/env bash
# run-distro.sh — drive the full install → smoke → uninstall lifecycle on one
# distro inside a privileged Docker container.
#
# Usage:
#   bash test/install/run-distro.sh <distro>
#
# Where <distro> is a row in test/install/distros.tsv (ubuntu24, debian12,
# alpine321, rocky9, fedora44, arch).
#
# Outputs:
#   test/install/logs/<distro>-install.log
#   test/install/logs/<distro>-smoke.log
#   test/install/logs/<distro>-uninstall.log
#   test/install/results-YYYYMMDD.tsv (appended)

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

if [[ $# -lt 1 ]]; then
    cat >&2 <<EOF
Usage: $0 <distro>

Available distros:
$(awk -F '\t' '/^[^#]/ {printf "  - %s\n", $1}' "$DISTROS_TSV")

Env vars (optional):
  ORVA_VERSION=vYYYY.MM.DD    pin a release tag (default: latest; only the current release is published)
  ORVA_KEEP_CONTAINER=1       skip teardown (for debugging)
  PORT_OFFSET=20              host port = 19443 + index in distros.tsv
EOF
    exit 1
fi

DISTRO="$1"
KEEP="${ORVA_KEEP_CONTAINER:-0}"
INSTALLER_PATH="${ORVA_INSTALLER_PATH:-$REPO_ROOT/scripts/install.sh}"
[[ -s "$INSTALLER_PATH" ]] || die "server installer missing or empty: $INSTALLER_PATH"

# Derive a stable host port per-distro so the matrix can run in parallel.
PORT_BASE=19443
PORT_IDX=$(awk -F '\t' '/^[^#]/ {if ($1=="'"$DISTRO"'") print NR-1; }' "$DISTROS_TSV" | head -1)
[[ -z "$PORT_IDX" ]] && die "distro '$DISTRO' not in $DISTROS_TSV"
PORT=$((PORT_BASE + PORT_IDX))
BASE_URL="http://localhost:${PORT}"

INSTALL_LOG="$LOGS_DIR/${DISTRO}-install.log"
SMOKE_LOG="$LOGS_DIR/${DISTRO}-smoke.log"
UNINSTALL_LOG="$LOGS_DIR/${DISTRO}-uninstall.log"

CONTAINER="orva-test-${DISTRO}"

trap '[[ "$KEEP" == "1" ]] || cleanup_container "$CONTAINER"' EXIT

# ── 1. Bring up the container + init ──────────────────────────────────────
start_distro_container "$DISTRO" "$PORT" >/dev/null
wait_for_init "$CONTAINER" 60 || die "init failed"

# ── 2. Copy install.sh and run it for real (not dryrun) ───────────────────
log "copying server installer into $CONTAINER: $INSTALLER_PATH"
docker cp "$INSTALLER_PATH" "$CONTAINER:/root/install.sh"

log "running install.sh inside $CONTAINER (log → $INSTALL_LOG)"
INSTALL_ENV=()
[[ -n "${ORVA_VERSION:-}" ]] && INSTALL_ENV=(-e "ORVA_VERSION=$ORVA_VERSION")

# One retry on failure — apt/dnf mirrors flake (HTTP 520, transient
# DNS, etc.). A second attempt with a small backoff usually clears it.
install_attempt() {
    docker exec "${INSTALL_ENV[@]}" "$CONTAINER" sh /root/install.sh >"$INSTALL_LOG" 2>&1
}

if ! install_attempt; then
    warn "install.sh failed on first attempt — retrying after 10s (mirror flake?)"
    sleep 10
    if ! install_attempt; then
        fail "install.sh exited non-zero (twice) — see $INSTALL_LOG"
        tail -40 "$INSTALL_LOG" >&2
        exit 2
    fi
fi
ok "install.sh completed"

# ── 3. Start the service and wait for health ─────────────────────────────
lookup_distro "$DISTRO"
if [[ "$DISTRO_INIT" == "systemd" ]]; then
    log "starting systemd unit"
    docker exec "$CONTAINER" systemctl enable --now orva \
        || die "systemctl enable --now orva failed"
    docker exec "$CONTAINER" systemctl is-active orva >/dev/null \
        || die "orva.service did not become active"
    ok "orva.service active"

    unit=$(docker exec "$CONTAINER" systemctl cat orva)
    # The daemon must hold no capabilities of its own: egress is enforced inside
    # each nsjail (NSTUN), and an ambient set could not survive execve of a
    # binary carrying file capabilities anyway.
    [[ "$unit" != *"AmbientCapabilities="* ]] \
        || die "orva.service still grants ambient capabilities to the daemon"
    # The bounding set must remain a superset of nsjail's file capabilities, or
    # execve(nsjail) itself fails with EPERM.
    [[ "$unit" == *"CapabilityBoundingSet=CAP_SYS_ADMIN CAP_NET_ADMIN CAP_SETUID CAP_SETGID CAP_NET_BIND_SERVICE"* ]] \
        || die "orva.service bounding set would prevent nsjail exec"
    [[ "$unit" == *"Environment=ORVA_DISABLE_USERNS="* ]] \
        || die "orva.service is missing the detected userns mode"
    ok "orva.service sandbox capability set"
else
    log "starting OpenRC service"
    docker exec "$CONTAINER" rc-update add orva default \
        || warn "rc-update add reported error"
    docker exec "$CONTAINER" service orva start \
        || die "service orva start failed"
    ok "orva openrc service started"
fi

if ! wait_for_health "$BASE_URL" 120; then
    docker exec "$CONTAINER" sh -c 'journalctl -u orva --no-pager -n 80 2>/dev/null || tail -80 /var/log/orva.log 2>/dev/null' >&2 || true
    die "orva did not respond on $BASE_URL"
fi

# ── 4. Smoke flow (API + CLI + browser) ───────────────────────────────────
log "running smoke flow against $BASE_URL (log → $SMOKE_LOG)"
if ORVA_BROWSER_LEG="${ORVA_BROWSER_LEG:-1}" \
       bash "$HERE/smoke-flow.sh" "$BASE_URL" "$CONTAINER" "$DISTRO" >"$SMOKE_LOG" 2>&1; then
    ok "smoke flow passed"
else
    fail "smoke flow failed — see $SMOKE_LOG"
    tail -40 "$SMOKE_LOG" >&2
    exit 3
fi

# ── 5. Uninstall verification ─────────────────────────────────────────────
log "running uninstall flow (log → $UNINSTALL_LOG)"
if bash "$HERE/uninstall-flow.sh" "$CONTAINER" "$DISTRO" "$BASE_URL" >"$UNINSTALL_LOG" 2>&1; then
    ok "uninstall flow passed"
else
    fail "uninstall flow failed — see $UNINSTALL_LOG"
    tail -40 "$UNINSTALL_LOG" >&2
    exit 4
fi

# ── 6. Append to summary ───────────────────────────────────────────────────
date_tag=$(date -u +%Y%m%d)
SUMMARY="$HERE/results-${date_tag}.tsv"
[[ -f "$SUMMARY" ]] || printf 'distro\tstatus\tinstall_log\tsmoke_log\tuninstall_log\n' >"$SUMMARY"
printf '%s\tpass\t%s\t%s\t%s\n' "$DISTRO" "$INSTALL_LOG" "$SMOKE_LOG" "$UNINSTALL_LOG" >>"$SUMMARY"

ok "$DISTRO: install + smoke + uninstall all passed"
