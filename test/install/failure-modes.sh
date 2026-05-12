#!/usr/bin/env bash
# failure-modes.sh — narrow set of failure / degraded-mode checks against
# the install script. Runs on Ubuntu 24 by default.
#
# Covered:
#   1. `--cli-only` install — drops only the CLI, no daemon / unit / user.
#   2. Reinstall idempotency — re-running install.sh on a populated data dir
#      preserves the function created between the two runs.
#
# Out of scope here: cgroup v2 absent, userns disabled, broken kernel —
# those require custom kernel builds.
#
# Usage: failure-modes.sh [distro]   default: ubuntu24

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

DISTRO="${1:-ubuntu24}"
CONTAINER="orva-failmode-${DISTRO}"
PORT=19999
BASE="http://localhost:$PORT"

PASS=0; FAIL=0
expect() {
    local label="$1" expected="$2" actual="$3"
    if [[ "$actual" == "$expected" ]]; then
        ok "$label  ($actual)"; PASS=$((PASS+1))
    else
        fail "$label  expected '$expected' got '$actual'"; FAIL=$((FAIL+1))
    fi
}

trap '[[ "${ORVA_KEEP_CONTAINER:-0}" == "1" ]] || cleanup_container "$CONTAINER"' EXIT

# ╭──────────────────────────────────────────────────────────────────────╮
# │ Test 1: --cli-only install                                           │
# ╰──────────────────────────────────────────────────────────────────────╯
log "── Test 1: --cli-only install (no daemon / no unit / no user)"

start_distro_container "$DISTRO" "$PORT" >/dev/null
wait_for_init "$CONTAINER" 60 || die "init failed"

docker cp "$REPO_ROOT/scripts/install.sh" "$CONTAINER:/root/install.sh"

if docker exec "$CONTAINER" sh /root/install.sh --cli-only >"$LOGS_DIR/failmodes-cli-only.log" 2>&1; then
    ok "install.sh --cli-only exited 0"; PASS=$((PASS+1))
else
    fail "install.sh --cli-only failed — see $LOGS_DIR/failmodes-cli-only.log"
    tail -30 "$LOGS_DIR/failmodes-cli-only.log" >&2
    FAIL=$((FAIL+1))
fi

out=$(docker exec "$CONTAINER" sh -c '[ -x /usr/local/bin/orva ] && echo present || echo gone')
expect "/usr/local/bin/orva installed" "present" "$out"

out=$(docker exec "$CONTAINER" sh -c '[ -f /etc/systemd/system/orva.service ] && echo present || echo gone')
expect "systemd unit NOT installed (cli-only)" "gone" "$out"

out=$(docker exec "$CONTAINER" sh -c '[ -d /opt/orva ] && echo present || echo gone')
expect "/opt/orva NOT created (cli-only)" "gone" "$out"

out=$(docker exec "$CONTAINER" sh -c 'id -u orva >/dev/null 2>&1 && echo present || echo gone')
expect "orva user NOT created (cli-only)" "gone" "$out"

# Cleanup container so test 2 starts fresh.
cleanup_container "$CONTAINER"

# ╭──────────────────────────────────────────────────────────────────────╮
# │ Test 2: reinstall idempotency                                        │
# ╰──────────────────────────────────────────────────────────────────────╯
log "── Test 2: reinstall idempotency (data preserved across overwrite)"

start_distro_container "$DISTRO" "$PORT" >/dev/null
wait_for_init "$CONTAINER" 60 || die "init failed"

docker cp "$REPO_ROOT/scripts/install.sh" "$CONTAINER:/root/install.sh"

# First install.
docker exec "$CONTAINER" sh /root/install.sh >"$LOGS_DIR/failmodes-install1.log" 2>&1 \
    || die "first install failed"
docker exec "$CONTAINER" systemctl enable --now orva
wait_for_health "$BASE" 90 || die "first orva did not come up"

ADMIN_KEY=$(read_admin_key "$CONTAINER" 30)
[[ -n "$ADMIN_KEY" ]] || die "could not read admin key"

curl -s -o /dev/null -X POST "$BASE/api/v1/auth/onboard" \
    -H 'Content-Type: application/json' \
    -d '{"email":"admin@orva.test","password":"correct-horse-battery-staple-9001"}' || true

create=$(curl -sf -H "X-Orva-API-Key: $ADMIN_KEY" -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d '{"name":"fail-idem","runtime":"node24"}')
fid=$(echo "$create" | jq -r '.id // empty')
[[ -n "$fid" ]] || die "could not create marker function"
ok "created marker function fail-idem ($fid)"

# Stop the daemon, run install.sh again on top of the populated data dir.
docker exec "$CONTAINER" systemctl stop orva
docker exec "$CONTAINER" sh /root/install.sh >"$LOGS_DIR/failmodes-install2.log" 2>&1 \
    || { fail "reinstall failed"; FAIL=$((FAIL+1)); }

docker exec "$CONTAINER" systemctl start orva
wait_for_health "$BASE" 60 || die "reinstalled orva did not come up"

# The marker function should still be there.
ADMIN_KEY=$(read_admin_key "$CONTAINER" 30)
list=$(curl -sf -H "X-Orva-API-Key: $ADMIN_KEY" "$BASE/api/v1/functions" | jq -r '.[].name' 2>/dev/null || echo "")
if echo "$list" | grep -qx "fail-idem"; then
    ok "marker function survived reinstall"; PASS=$((PASS+1))
else
    fail "marker function did not survive reinstall (list: $list)"
    FAIL=$((FAIL+1))
fi

# ╭──────────────────────────────────────────────────────────────────────╮
# │ Summary                                                              │
# ╰──────────────────────────────────────────────────────────────────────╯
echo
echo "=== failure-modes: $PASS passed, $FAIL failed ==="
[[ "$FAIL" -eq 0 ]]
