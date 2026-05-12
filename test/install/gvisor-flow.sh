#!/usr/bin/env bash
# gvisor-flow.sh — verify the README claim that Orva can run inside a
# gVisor (`runsc`) Docker runtime. Skipped silently if runsc isn't a
# registered Docker runtime on this host.
#
# This is the most likely failure point: gVisor's user-space kernel
# may filter or partially emulate the syscalls nsjail relies on
# (unshare, mount, pivot_root, setns). The leg captures the actual
# behaviour rather than asserting success.
#
# Usage: gvisor-flow.sh [orva_image_tag]
#   Default tag: ghcr.io/harsh-2002/orva:latest

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

IMAGE="${1:-ghcr.io/harsh-2002/orva:latest}"
PORT=28443
BASE="http://localhost:$PORT"
CONTAINER="orva-gvisor-test"
RESULT_FILE="$LOGS_DIR/gvisor-result.txt"

# ── Pre-flight: is runsc registered? ─────────────────────────────────────
if ! docker info --format '{{json .Runtimes}}' 2>/dev/null | grep -q '"runsc"'; then
    log "INFO: gVisor (runsc) not registered as Docker runtime — skipping"
    printf 'status: skipped\nreason: runsc not in docker info\n' > "$RESULT_FILE"
    exit 0
fi
ok "gVisor (runsc) detected"

# ── Launch Orva under runsc ──────────────────────────────────────────────
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker volume rm orva-gvisor-data >/dev/null 2>&1 || true

log "starting orva under --runtime=runsc"
if ! docker run -d \
        --name "$CONTAINER" \
        --runtime=runsc \
        -p "${PORT}:8443" \
        --cap-add SYS_ADMIN \
        --security-opt seccomp=unconfined \
        --security-opt apparmor=unconfined \
        --security-opt systempaths=unconfined \
        -v orva-gvisor-data:/var/lib/orva \
        "$IMAGE" >/dev/null; then
    fail "docker run under runsc failed at startup"
    docker logs "$CONTAINER" 2>&1 | tail -30 || true
    printf 'status: failed\nreason: container did not start\n' > "$RESULT_FILE"
    exit 1
fi

trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true' EXIT

# ── Wait for orvad to come up ─────────────────────────────────────────────
if ! wait_for_health "$BASE" 90; then
    fail "orva failed to start under gVisor — health endpoint unreachable"
    docker logs "$CONTAINER" 2>&1 | tail -40 >&2
    {
        echo 'status: failed'
        echo 'reason: daemon failed to reach healthy state'
        echo '--- last 40 lines of container logs ---'
        docker logs "$CONTAINER" 2>&1 | tail -40
    } > "$RESULT_FILE"
    exit 1
fi
ok "orva daemon healthy under gVisor"

# ── The real test: can we actually invoke a function? ─────────────────────
log "attempting end-to-end deploy + invoke (this is where nsjail-in-gvisor will break if it does)"

ADMIN_KEY=$(read_admin_key "$CONTAINER" 30) \
    || { fail "could not read admin key"; printf 'status: partial\nreason: daemon up but admin key missing\n' > "$RESULT_FILE"; exit 1; }

# Onboard (idempotent).
curl -s -o /dev/null -X POST "$BASE/api/v1/auth/onboard" \
    -H 'Content-Type: application/json' \
    -d '{"email":"admin@gvisor.test","password":"correct-horse-battery-staple-9001"}' || true

CURL=(curl -sf -H "X-Orva-API-Key: $ADMIN_KEY")
create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d '{"name":"hello-gvisor","runtime":"node24","memory_mb":128}')
fid=$(echo "$create" | jq -r '.id // empty')
[[ -n "$fid" ]] || { fail "could not create function under gVisor"; exit 1; }

"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n '{code:"exports.handler = async () => ({statusCode:200, body:\"hello-gvisor\"});", filename:"handler.js"}')" >/dev/null

# Wait active.
status=""
for _ in $(seq 1 60); do
    status=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status' 2>/dev/null || echo unknown)
    [[ "$status" == "active" ]] && break
    sleep 1
done

if [[ "$status" != "active" ]]; then
    fail "deployment did not reach active under gVisor (last status: $status)"
    {
        echo 'status: failed'
        echo 'reason: deployment never reached active'
        echo "final status: $status"
        echo '--- last 60 lines of container logs ---'
        docker logs "$CONTAINER" 2>&1 | tail -60
    } > "$RESULT_FILE"
    exit 1
fi

# Invoke.
short_id="${fid#fn_}"
body=$(curl -s -X POST -H "X-Orva-API-Key: $ADMIN_KEY" "$BASE/fn/$short_id/" -d '{}' 2>&1 || true)
http_code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "X-Orva-API-Key: $ADMIN_KEY" "$BASE/fn/$short_id/" -d '{}' 2>&1 || echo 000)

if [[ "$body" == *"hello-gvisor"* ]]; then
    ok "FUNCTION INVOCATION WORKS UNDER GVISOR ✓"
    {
        echo 'status: pass'
        echo "reason: end-to-end deploy + invoke succeeded"
        echo "image: $IMAGE"
    } > "$RESULT_FILE"
    exit 0
else
    fail "function invocation under gVisor returned unexpected output"
    fail "HTTP $http_code, body: $body"
    {
        echo 'status: failed'
        echo 'reason: invocation did not return expected body'
        echo "http_code: $http_code"
        echo "body: $body"
        echo '--- last 80 lines of container logs ---'
        docker logs "$CONTAINER" 2>&1 | tail -80
    } > "$RESULT_FILE"
    exit 1
fi
