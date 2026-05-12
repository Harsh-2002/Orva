#!/usr/bin/env bash
# uninstall-flow.sh — verify uninstall.sh + reinstall data preservation +
# --purge wipes everything. Called by run-distro.sh after smoke-flow.sh.
#
# Usage: uninstall-flow.sh <container> <distro> <base_url>

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

CONTAINER="${1:?usage: $0 <container> <distro> <base_url>}"
DISTRO="${2:?}"
BASE_URL="${3:?}"

lookup_distro "$DISTRO"

PASS=0; FAIL=0
expect() {
    local label="$1" expected="$2" actual="$3"
    if [[ "$actual" == "$expected" ]]; then
        ok "$label  ($actual)"
        PASS=$((PASS+1))
    else
        fail "$label  expected '$expected' got '$actual'"
        FAIL=$((FAIL+1))
    fi
}

# ── 1. Default uninstall ─────────────────────────────────────────────────
log "running uninstall.sh (default mode — preserves data)"
docker exec "$CONTAINER" sh /opt/orva/share/orva/scripts/uninstall.sh \
    || die "uninstall.sh exited non-zero"

# /opt/orva removed
out=$(docker exec "$CONTAINER" sh -c '[ -d /opt/orva ] && echo present || echo gone')
expect "/opt/orva removed" "gone" "$out"

# /var/lib/orva preserved (default mode)
out=$(docker exec "$CONTAINER" sh -c '[ -d /var/lib/orva ] && echo present || echo gone')
expect "/var/lib/orva preserved" "present" "$out"

# Service unit gone
if [[ "$DISTRO_INIT" == "systemd" ]]; then
    out=$(docker exec "$CONTAINER" sh -c '[ -f /etc/systemd/system/orva.service ] && echo present || echo gone')
    expect "systemd unit removed" "gone" "$out"
else
    out=$(docker exec "$CONTAINER" sh -c '[ -f /etc/init.d/orva ] && echo present || echo gone')
    expect "openrc unit removed" "gone" "$out"
fi

# Health endpoint should be unreachable (service stopped). curl's
# %{http_code} returns 000 (or repeated zeros when retried) on connect
# failure; any 0-prefixed string == unreachable.
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$BASE_URL/api/v1/system/health" 2>/dev/null || echo 000)
case "$code" in
    000|0*|502|503|7|28) ok "health endpoint unreachable after uninstall ($code)"; PASS=$((PASS+1)) ;;
    *) fail "health endpoint still reachable after uninstall: $code"; FAIL=$((FAIL+1)) ;;
esac

# ── 2. Reinstall + verify data round-trip ─────────────────────────────────
log "reinstalling to verify /var/lib/orva data survives"
INSTALL_ENV=()
[[ -n "${ORVA_VERSION:-}" ]] && INSTALL_ENV=(-e "ORVA_VERSION=$ORVA_VERSION")
if ! docker exec "${INSTALL_ENV[@]}" "$CONTAINER" sh /tmp/install.sh >>"$LOGS_DIR/${DISTRO}-reinstall.log" 2>&1; then
    fail "reinstall failed — see $LOGS_DIR/${DISTRO}-reinstall.log"
    FAIL=$((FAIL+1))
else
    if [[ "$DISTRO_INIT" == "systemd" ]]; then
        docker exec "$CONTAINER" systemctl start orva || true
    else
        docker exec "$CONTAINER" service orva start || true
    fi

    if wait_for_health "$BASE_URL" 60; then
        # Pull functions list — the smoke flow left at least hello-api
        # behind (hello-cli's build may have errored out in nested
        # containers, but its row should still exist in the DB if the
        # deploy reached the queue).
        admin_key=$(read_admin_key "$CONTAINER" 30)
        if [[ -n "$admin_key" ]]; then
            list=$(curl -s -H "X-Orva-API-Key: $admin_key" "$BASE_URL/api/v1/functions" || echo '{"functions":[]}')
            # Response shape: {"functions":[{name, ...}], "total": N}
            count=$(echo "$list" | jq -r '.functions[]? .name // empty' 2>/dev/null | grep -cE '^hello-(api|cli)$' || true)
            if [[ "$count" -ge 1 ]]; then
                ok "$count function(s) survived uninstall round-trip"
                PASS=$((PASS+1))
            else
                fail "no functions survived uninstall + reinstall (got: $list)"
                FAIL=$((FAIL+1))
            fi
        else
            fail "could not read admin key after reinstall"
            FAIL=$((FAIL+1))
        fi
    else
        fail "orva did not come back up after reinstall"
        FAIL=$((FAIL+1))
    fi
fi

# ── 3. --purge uninstall ─────────────────────────────────────────────────
log "running uninstall.sh --purge"
docker exec "$CONTAINER" sh /opt/orva/share/orva/scripts/uninstall.sh --purge \
    || die "uninstall.sh --purge exited non-zero"

out=$(docker exec "$CONTAINER" sh -c '[ -d /var/lib/orva ] && echo present || echo gone')
expect "/var/lib/orva removed by --purge" "gone" "$out"

out=$(docker exec "$CONTAINER" sh -c 'id -u orva >/dev/null 2>&1 && echo present || echo gone')
expect "orva service user removed by --purge" "gone" "$out"

# ── Summary ──────────────────────────────────────────────────────────────
echo
echo "=== uninstall-flow [$DISTRO]: $PASS passed, $FAIL failed ==="
[[ "$FAIL" -eq 0 ]]
