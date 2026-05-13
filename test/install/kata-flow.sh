#!/usr/bin/env bash
# kata-flow.sh — verify Orva can run end-to-end under Kata Containers.
#
# Same 3-stage gate pattern as the deleted gvisor-flow.sh:
#   1. health endpoint responds under --runtime=$RUNTIME
#   2. deployment reaches `status=active`
#   3. invoke returns the expected body
#
# Usage:
#   bash test/install/kata-flow.sh [orva_image_tag]
#
# Env:
#   RUNTIME           Docker runtime to use. Default: `kata`. Set to
#                     `kata-clh` to test the Cloud Hypervisor variant
#                     (must be registered in /etc/docker/daemon.json).
#   ORVA_GATEWAY_PORT Host port to bind the container to.
#                     Default: auto-pick per RUNTIME (kata=28443,
#                     kata-clh=38443) so two instances can coexist.
#
# Writes a verdict to test/install/logs/kata-<runtime>-result.txt that
# downstream tooling / the docs writer can ingest.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

IMAGE="${1:-ghcr.io/harsh-2002/orva:v2026.05.12}"
RUNTIME="${RUNTIME:-kata}"
# Per-runtime default port so kata + kata-clh can coexist.
case "$RUNTIME" in
    kata)     DEFAULT_PORT=28443 ;;
    kata-clh) DEFAULT_PORT=38443 ;;
    *)        DEFAULT_PORT=48443 ;;
esac
PORT="${ORVA_GATEWAY_PORT:-$DEFAULT_PORT}"
BASE="http://localhost:$PORT"
CONTAINER="orva-${RUNTIME}-test"
RESULT_FILE="$LOGS_DIR/kata-${RUNTIME}-result.txt"

# ── Pre-flight: is the requested runtime registered? ─────────────────────
if ! docker info --format '{{json .Runtimes}}' 2>/dev/null \
        | python3 -c "import json,sys; sys.exit(0 if '$RUNTIME' in json.loads(sys.stdin.read()) else 1)"; then
    log "INFO: $RUNTIME not registered as Docker runtime — skipping"
    printf 'status: skipped\nreason: %s not in docker info\nimage: %s\n' "$RUNTIME" "$IMAGE" > "$RESULT_FILE"
    exit 0
fi
ok "Docker runtime '$RUNTIME' detected"

# ── Launch Orva under the chosen runtime ─────────────────────────────────
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker volume rm "orva-${RUNTIME}-data" >/dev/null 2>&1 || true

log "starting orva under --runtime=$RUNTIME on port $PORT"
container_start=$(date +%s.%N)
if ! docker run -d \
        --name "$CONTAINER" \
        --runtime="$RUNTIME" \
        -p "${PORT}:8443" \
        --cap-add SYS_ADMIN \
        --security-opt seccomp=unconfined \
        --security-opt apparmor=unconfined \
        --security-opt systempaths=unconfined \
        -v "orva-${RUNTIME}-data:/var/lib/orva" \
        "$IMAGE" >/dev/null; then
    fail "docker run under $RUNTIME failed at startup"
    docker logs "$CONTAINER" 2>&1 | tail -30 || true
    {
        echo "status: failed"
        echo "reason: container did not start"
        echo "runtime: $RUNTIME"
        echo "image: $IMAGE"
    } > "$RESULT_FILE"
    exit 1
fi
container_started=$(date +%s.%N)
container_start_ms=$(awk -v s="$container_start" -v e="$container_started" 'BEGIN { printf "%.0f", (e-s)*1000 }')
ok "container started in ${container_start_ms} ms"

trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true' EXIT

# ── Wait for orvad to come up ─────────────────────────────────────────────
if ! wait_for_health "$BASE" 120; then
    fail "orva failed to start under $RUNTIME — health endpoint unreachable"
    docker logs "$CONTAINER" 2>&1 | tail -40 >&2
    {
        echo 'status: failed'
        echo 'reason: daemon failed to reach healthy state'
        echo "runtime: $RUNTIME"
        echo "image: $IMAGE"
        echo '--- last 40 lines of container logs ---'
        docker logs "$CONTAINER" 2>&1 | tail -40
    } > "$RESULT_FILE"
    exit 1
fi
ok "orva daemon healthy under $RUNTIME"

# ── The real test: can we actually invoke a function? ─────────────────────
log "attempting end-to-end deploy + invoke"

ADMIN_KEY=$(read_admin_key "$CONTAINER" 30) \
    || { fail "could not read admin key"; printf 'status: partial\nreason: daemon up but admin key missing\nruntime: %s\n' "$RUNTIME" > "$RESULT_FILE"; exit 1; }

# Onboard admin user (idempotent — 409 if already onboarded).
curl -s -o /dev/null -X POST "$BASE/api/v1/auth/onboard" \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"correct-horse-battery-staple-9001"}' || true

CURL=(curl -sf -H "X-Orva-API-Key: $ADMIN_KEY")
create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d '{"name":"hello-kata","runtime":"node24","memory_mb":128}')
fid=$(echo "$create" | jq -r '.id // empty')
[[ -n "$fid" ]] || { fail "could not create function under $RUNTIME"; exit 1; }

"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n '{code:"exports.handler = async () => ({statusCode:200, body:\"hello-kata\"});", filename:"handler.js"}')" >/dev/null

# Wait active.
status=""
for _ in $(seq 1 60); do
    status=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status' 2>/dev/null || echo unknown)
    [[ "$status" == "active" ]] && break
    sleep 1
done

if [[ "$status" != "active" ]]; then
    fail "deployment did not reach active under $RUNTIME (last status: $status)"
    {
        echo 'status: failed'
        echo "reason: deployment never reached active (last: $status)"
        echo "runtime: $RUNTIME"
        echo "image: $IMAGE"
        echo '--- last 60 lines of container logs ---'
        docker logs "$CONTAINER" 2>&1 | tail -60
    } > "$RESULT_FILE"
    exit 1
fi

# Invoke.
short_id="${fid#fn_}"
body=$(curl -s -X POST -H "X-Orva-API-Key: $ADMIN_KEY" "$BASE/fn/$short_id/" -d '{}' 2>&1 || true)
http_code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "X-Orva-API-Key: $ADMIN_KEY" "$BASE/fn/$short_id/" -d '{}' 2>&1 || echo 000)

if [[ "$body" == *"hello-kata"* ]]; then
    ok "FUNCTION INVOCATION WORKS UNDER $RUNTIME ✓"
    {
        echo 'status: pass'
        echo "reason: end-to-end deploy + invoke succeeded"
        echo "runtime: $RUNTIME"
        echo "image: $IMAGE"
        echo "container_start_ms: $container_start_ms"
    } > "$RESULT_FILE"
    exit 0
fi

fail "function invocation under $RUNTIME returned unexpected output"
fail "HTTP $http_code, body: $body"
{
    echo 'status: failed'
    echo 'reason: invocation did not return expected body'
    echo "runtime: $RUNTIME"
    echo "image: $IMAGE"
    echo "container_start_ms: $container_start_ms"
    echo "http_code: $http_code"
    echo "body: $body"
    echo '--- last 80 lines of container logs ---'
    docker logs "$CONTAINER" 2>&1 | tail -80
} > "$RESULT_FILE"
exit 1
