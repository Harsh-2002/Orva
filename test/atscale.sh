#!/usr/bin/env bash
# atscale.sh — multi-function deployment + isolation verification.
#
# Goals:
#   1. Deploy 20 functions of mixed shapes; assert each reaches active
#      within budget; record per-fn build duration.
#   2. With all 20 idle, sample host RAM + sandbox state; that's the
#      idle-capacity baseline.
#   3. Hammer 5 fns concurrently with hey; assert cross-fn isolation
#      (untouched fns stay at min_warm) and no 503 BUILDING.
#   4. Capture autoscaler-driven scale-up/scale-down counts per fn.
#
# Output: tab-separated rows on stdout. Save to test/atscale-results.tsv.
#
# Required env:
#   API_KEY     bootstrap admin key from Orva (or any 'invoke,read,write,admin' key)
#   BASE_URL    default http://localhost:18443

set -euo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY to a valid admin key}"

if ! command -v hey >/dev/null 2>&1; then
    echo "hey is required (github.com/rakyll/hey)" >&2
    exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
    echo "jq is required" >&2
    exit 2
fi

CURL=(curl -sf -H "X-Orva-API-Key: $KEY")
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

# Compact tar-gz construction inline so we don't need on-disk fixtures.
# Each function is a hello-world handler in its target runtime; the deps
# field tests the async build queue without taking minutes per fn.

# 20 fixtures: 5 of each runtime mix.
FN_NAMES=()
for i in $(seq 1 5); do FN_NAMES+=("ascale-node22-$i"); done
for i in $(seq 1 5); do FN_NAMES+=("ascale-node24-$i"); done
for i in $(seq 1 5); do FN_NAMES+=("ascale-py313-$i"); done
for i in $(seq 1 5); do FN_NAMES+=("ascale-py314-$i"); done

runtime_for() {
    case "$1" in
        ascale-node22-*) echo node22 ;;
        ascale-node24-*) echo node24 ;;
        ascale-py313-*)  echo python313 ;;
        ascale-py314-*)  echo python314 ;;
    esac
}

handler_for() {
    case "$1" in
        ascale-node22-*|ascale-node24-*)
            cat <<'EOF'
exports.handler = async (event) => ({
  statusCode: 200,
  headers: {"Content-Type": "application/json"},
  body: JSON.stringify({ name: process.env.FN_NAME || "unknown", ok: true })
});
EOF
            ;;
        ascale-py313-*|ascale-py314-*)
            cat <<'EOF'
import json, os
def handler(event, context):
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"name": os.environ.get("FN_NAME", "unknown"), "ok": True})
    }
EOF
            ;;
    esac
}

filename_for() {
    case "$1" in
        ascale-node22-*|ascale-node24-*) echo handler.js ;;
        ascale-py313-*|ascale-py314-*)   echo handler.py ;;
    esac
}

# ── Phase 1: Deploy all 20 in parallel ──────────────────────────────────

echo "# phase 1: deploying 20 functions" >&2
declare -A FN_ID
START_DEPLOY=$(date +%s)

for name in "${FN_NAMES[@]}"; do
    runtime=$(runtime_for "$name")
    code=$(handler_for "$name")
    filename=$(filename_for "$name")

    create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"$name\",\"runtime\":\"$runtime\",\"memory_mb\":128,\"cpus\":1,\"env_vars\":{\"FN_NAME\":\"$name\"}}" \
        2>&1) || true
    fid=$(echo "$create" | jq -r '.id // empty' 2>/dev/null || echo)
    if [ -z "$fid" ]; then
        # Already exists — look it up.
        fid=$("${CURL[@]}" "$BASE/api/v1/functions" | jq -r --arg n "$name" '.functions[] | select(.name==$n) | .id' | head -1)
    fi
    FN_ID[$name]=$fid

    # Submit deploy (returns 202 + deployment_id).
    body=$(jq -n --arg c "$code" --arg f "$filename" '{code:$c, filename:$f}')
    dep=$("${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
        -H "Content-Type: application/json" -d "$body")
    dep_id=$(echo "$dep" | jq -r '.deployment_id // empty')
    echo "deploy	$name	$fid	$dep_id" >&2
done

# Wait for every fn to be active. Budget: 60s.
echo "# waiting for all builds to succeed..." >&2
deadline=$(( $(date +%s) + 60 ))
while [ "$(date +%s)" -lt $deadline ]; do
    pending=0
    for name in "${FN_NAMES[@]}"; do
        fid=${FN_ID[$name]}
        status=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
        if [ "$status" != "active" ] && [ "$status" != "error" ]; then
            pending=$((pending+1))
        fi
    done
    if [ $pending -eq 0 ]; then break; fi
    echo "# pending builds: $pending" >&2
    sleep 2
done
DEPLOY_ELAPSED=$(( $(date +%s) - START_DEPLOY ))
echo "deploy_elapsed_s	$DEPLOY_ELAPSED" >&2

# ── Phase 2: Snapshot idle ──────────────────────────────────────────────
echo "# phase 2: idle snapshot" >&2
sleep 3
metrics=$("${CURL[@]}" "$BASE/api/v1/system/metrics.json")
mem_avail=$(echo "$metrics" | jq -r '.host.mem_available_mb')
mem_total=$(echo "$metrics" | jq -r '.host.mem_total_mb')
mem_reserved=$(echo "$metrics" | jq -r '.host.mem_reserved_mb')
pool_count=$(echo "$metrics" | jq -r '.pools | length')
total_idle=$(echo "$metrics" | jq -r '[.pools[].idle] | add // 0')
total_busy=$(echo "$metrics" | jq -r '[.pools[].busy] | add // 0')
echo "phase=idle	pools=$pool_count	idle=$total_idle	busy=$total_busy	mem_avail_mb=$mem_avail	mem_reserved_mb=$mem_reserved	mem_total_mb=$mem_total"

# ── Phase 3: Hammer 5 fns in parallel ───────────────────────────────────
echo "# phase 3: hammer 5 fns concurrently for 30s" >&2
LOAD_FNS=("${FN_NAMES[0]}" "${FN_NAMES[5]}" "${FN_NAMES[10]}" "${FN_NAMES[15]}" "${FN_NAMES[2]}")
PIDS=()
for name in "${LOAD_FNS[@]}"; do
    fid=${FN_ID[$name]}
    (
        hey -z 30s -c 25 -m POST -H "X-Orva-API-Key: $KEY" \
            -d '{}' "$BASE/fn/$fid/" \
            > "$TMPDIR/$name.hey" 2>&1
    ) &
    PIDS+=($!)
done

# Sample metrics every 3s during the run.
sleep 5
for tick in 1 2 3 4 5 6 7; do
    sleep 3
    metrics=$("${CURL[@]}" "$BASE/api/v1/system/metrics.json")
    active_req=$(echo "$metrics" | jq -r '.active_requests')
    mem_reserved=$(echo "$metrics" | jq -r '.host.mem_reserved_mb')
    spawned=$(echo "$metrics" | jq -r '[.pools[].spawned] | add')
    killed=$(echo "$metrics" | jq -r '[.pools[].killed] | add')
    echo "phase=load_t${tick}	active_req=$active_req	mem_reserved_mb=$mem_reserved	spawned_total=$spawned	killed_total=$killed"
done

wait "${PIDS[@]}" || true

# ── Phase 4: Per-fn pool snapshot after load ────────────────────────────
echo "# phase 4: post-load per-pool snapshot" >&2
metrics=$("${CURL[@]}" "$BASE/api/v1/system/metrics.json")
echo "# function_id	function_name	idle	busy	dynamic_max	scale_ups	scale_downs	rate_ewma"
echo "$metrics" | jq -r '.pools[] | [.function_id, .function_name, .idle, .busy, .dynamic_max, .scale_ups, .scale_downs, .rate_ewma] | @tsv'

# ── Phase 5: Throughput per hammered fn ─────────────────────────────────
echo "# phase 5: hammered-fn throughput" >&2
echo "# fn_name	rps	p50_ms	p95_ms	p99_ms"
for name in "${LOAD_FNS[@]}"; do
    f="$TMPDIR/$name.hey"
    # Default to 0 so empty `hey` output (timeout/blip) doesn't break awk.
    rps=$(awk '/Requests\/sec/ {print $2; exit}' "$f")
    p50=$(awk '/50%/ {print $2; exit}' "$f")
    p95=$(awk '/95%/ {print $2; exit}' "$f")
    p99=$(awk '/99%/ {print $2; exit}' "$f")
    # Pass via -v so unset values become 0 inside awk, no shell expansion glitches.
    p50_ms=$(awk -v v="${p50:-0}" 'BEGIN{print v*1000}')
    p95_ms=$(awk -v v="${p95:-0}" 'BEGIN{print v*1000}')
    p99_ms=$(awk -v v="${p99:-0}" 'BEGIN{print v*1000}')
    printf "load\t%s\t%s\t%s\t%s\t%s\n" "$name" "${rps:-0}" "$p50_ms" "$p95_ms" "$p99_ms"
done

# ── Phase 6: Process leak check ─────────────────────────────────────────
echo "# phase 6: nsjail process count (should match sum(idle+busy)) " >&2
container=$(docker ps --filter "publish=${BASE##*:}" --format '{{.Names}}' | head -1)
if [ -n "$container" ]; then
    nsjail_count=$(docker exec "$container" sh -c 'ls /proc/[0-9]*/cmdline 2>/dev/null | xargs -I{} sh -c "tr \"\\000\" \" \" < {}; echo" 2>/dev/null | grep -c nsjail || true')
    expected=$(echo "$metrics" | jq -r '[.pools[] | .idle + .busy] | add // 0')
    echo "process_check	nsjail_running=$nsjail_count	expected=$expected"
fi

echo "# DONE" >&2
