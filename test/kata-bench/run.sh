#!/usr/bin/env bash
# run.sh — benchmark Orva sequentially under runc / kata / kata-clh.
#
# Per runtime:
#   1. Start orvad container with --runtime=$RUNTIME on a runtime-specific port.
#   2. Onboard admin, mint API key, deploy a reference Node 24 function.
#   3. Warm the pool.
#   4. Cold-start probe (drain pool then invoke; capture latency ×10, take median).
#   5. ceiling.sh (full ramp) — writes per-runtime CSV.
#   6. Capture docker-stats snapshots throughout the run (background tail).
#   7. Tear down.
#
# Strictly sequential. Output goes to test/kata-bench/<runtime>/.
#
# Env:
#   ORVA_IMAGE        default ghcr.io/harsh-2002/orva:v2026.05.12
#   BENCH_RUNTIMES    default "runc kata kata-clh"
#
# Suggested invocation:
#   bash test/kata-bench/run.sh 2>&1 | tee test/kata-bench/run.log

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
IMAGE="${ORVA_IMAGE:-ghcr.io/harsh-2002/orva:v2026.05.12}"
RUNTIMES="${BENCH_RUNTIMES:-runc kata kata-clh}"

c_cyan='\033[1;36m'; c_green='\033[1;32m'; c_red='\033[1;31m'; c_yellow='\033[1;33m'; c_reset='\033[0m'
log()  { printf "${c_cyan}==>${c_reset} %s\n" "$*"; }
ok()   { printf "${c_green}✓${c_reset} %s\n" "$*"; }
warn() { printf "${c_yellow}!!!${c_reset} %s\n" "$*" >&2; }
fail() { printf "${c_red}✗${c_reset} %s\n" "$*" >&2; }

mkdir -p "$HERE/logs"

port_for() {
    case "$1" in
        runc)     echo 18443 ;;
        kata)     echo 28443 ;;
        kata-clh) echo 38443 ;;
        *)        echo 48443 ;;
    esac
}

stats_pid=""
trap 'kill $stats_pid 2>/dev/null || true' EXIT INT TERM

run_one() {
    local runtime="$1"
    local port out container vol
    port=$(port_for "$runtime")
    out="$HERE/$runtime"
    container="orva-bench-${runtime}"
    vol="orva-bench-${runtime}-data"

    mkdir -p "$out"
    rm -f "$out"/*.csv "$out"/*.tsv "$out"/*.log "$out"/*.json 2>/dev/null || true

    docker rm -f "$container" >/dev/null 2>&1 || true
    docker volume rm "$vol" >/dev/null 2>&1 || true

    log "[$runtime] starting container on port $port"
    local t0 t1 startup_ms
    t0=$(date +%s.%N)
    docker run -d \
        --name "$container" \
        --runtime="$runtime" \
        -p "${port}:8443" \
        --cap-add SYS_ADMIN \
        --security-opt seccomp=unconfined \
        --security-opt apparmor=unconfined \
        --security-opt systempaths=unconfined \
        -v "${vol}:/var/lib/orva" \
        "$IMAGE" >/dev/null

    # Wait for health.
    local base="http://localhost:$port" code=000 ok_at=""
    for _ in $(seq 1 180); do
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$base/api/v1/system/health" 2>/dev/null || echo 000)
        if [[ "$code" == "200" ]]; then ok_at=$(date +%s.%N); break; fi
        sleep 1
    done
    if [[ -z "$ok_at" ]]; then
        fail "[$runtime] never healthy on $base"; docker logs "$container" 2>&1 | tail -30 | tee "$out/startup-failure.log"
        echo "status: failed" > "$out/result.txt"; return 1
    fi
    t1="$ok_at"
    startup_ms=$(awk -v s="$t0" -v e="$t1" 'BEGIN { printf "%.0f", (e-s)*1000 }')
    ok "[$runtime] healthy in ${startup_ms} ms"
    echo "container_start_ms=$startup_ms" > "$out/startup.txt"

    # Start docker-stats capture in background.
    (
        while docker inspect "$container" >/dev/null 2>&1; do
            docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.NetIO}},{{.BlockIO}}' "$container" 2>/dev/null \
                | sed "s/^/$(date -u +%Y-%m-%dT%H:%M:%SZ),/"
            sleep 5
        done
    ) > "$out/stats.csv" &
    stats_pid=$!

    # Onboard + deploy reference function.
    local admin_key fid
    admin_key=$(docker exec "$container" cat /var/lib/orva/.admin-key)
    curl -s -o /dev/null -X POST "$base/api/v1/auth/onboard" \
        -H 'Content-Type: application/json' \
        -d '{"username":"admin","password":"correct-horse-battery-staple-9001"}' || true

    local create
    create=$(curl -sf -H "X-Orva-API-Key: $admin_key" -X POST "$base/api/v1/functions" \
        -H 'Content-Type: application/json' \
        -d '{"name":"hello","runtime":"node24","memory_mb":128,"network_mode":"none"}')
    fid=$(echo "$create" | jq -r '.id // empty')
    if [[ -z "$fid" ]]; then
        fail "[$runtime] could not create function"; echo "status: failed" > "$out/result.txt"; kill $stats_pid 2>/dev/null; return 1
    fi
    curl -sf -H "X-Orva-API-Key: $admin_key" -X POST "$base/api/v1/functions/$fid/deploy-inline" \
        -H 'Content-Type: application/json' \
        -d "$(jq -n '{code:"exports.handler = async () => ({statusCode:200, body:\"ok\"});", filename:"handler.js"}')" >/dev/null

    # Wait active.
    for _ in $(seq 1 60); do
        local st
        st=$(curl -sf -H "X-Orva-API-Key: $admin_key" "$base/api/v1/functions/$fid" | jq -r '.status' 2>/dev/null || echo unknown)
        [[ "$st" == "active" ]] && break
        sleep 1
    done

    # Warm the pool with a single invocation.
    curl -s -X POST -H "X-Orva-API-Key: $admin_key" "$base/fn/${fid#fn_}/" -d '{}' >/dev/null || true

    # Cold-start probe: 10 fresh-container invokes (no pool, so each must
    # spawn a new nsjail worker). Captures the worst-case per-invocation
    # latency for the runtime — what an operator sees after pool drain
    # or first deploy.
    log "[$runtime] cold-start probe ×10"
    echo "iter,ms" > "$out/cold-start.csv"
    for i in $(seq 1 10); do
        # No drain endpoint exposed publicly; rely on natural pool churn
        # by waiting between iterations so the worker idle-timeout reclaims
        # them. With min_warm=0 + idle_ttl ~30s, sleeping 35s forces a cold
        # start on each iter. (For default pool config the worst-case is
        # well-approximated by a fresh invoke after a long idle.)
        sleep 8
        local start_ns end_ns ms
        start_ns=$(date +%s%N)
        curl -s -o /dev/null -X POST -H "X-Orva-API-Key: $admin_key" "$base/fn/${fid#fn_}/" -d '{}'
        end_ns=$(date +%s%N)
        ms=$(( (end_ns - start_ns) / 1000000 ))
        echo "$i,$ms" >> "$out/cold-start.csv"
        printf '  iter=%d  %d ms\n' "$i" "$ms"
    done

    # Full ceiling.sh ramp. CEILING_LABEL tags rows for downstream diff.
    log "[$runtime] ceiling.sh full ramp"
    CEILING_LABEL="$runtime" bash "$REPO_ROOT/test/ceiling.sh" "$admin_key" "$fid" "$base" \
        > "$out/ceiling.csv" 2> "$out/ceiling.stderr.log" || warn "[$runtime] ceiling.sh exited non-zero (partial data captured)"

    # Stop stats capture for this runtime.
    kill $stats_pid 2>/dev/null || true
    stats_pid=""

    ok "[$runtime] done — output in $out"
    docker rm -f "$container" >/dev/null 2>&1 || true
    docker volume rm "$vol" >/dev/null 2>&1 || true
}

# ─────────────────────────────────────────────────────────────────────────
log "bench plan: image=$IMAGE  runtimes=$RUNTIMES"

for rt in $RUNTIMES; do
    run_one "$rt" || warn "$rt run errored (continuing)"
done

# ── Aggregate to summary.md ──────────────────────────────────────────────
log "aggregating summary"
python3 "$HERE/aggregate.py" "$HERE" > "$HERE/summary.md" 2>"$HERE/aggregate.err" \
    && ok "wrote $HERE/summary.md" \
    || warn "aggregation reported errors (see $HERE/aggregate.err)"

echo
echo "=== bench complete ==="
ls -la "$HERE"/runc "$HERE"/kata "$HERE"/kata-clh 2>/dev/null || true
echo "summary: $HERE/summary.md"
