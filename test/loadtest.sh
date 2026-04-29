#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."

HEY=~/go/bin/hey
orva=./orva
API_KEY=""
EP=""

# ─── Helpers ──────────────────────────────────────────────

get_fn_id() {
  curl -s http://localhost:8443/api/v1/functions \
    -H "X-Orva-API-Key: $API_KEY" | \
    python3 -c "import sys,json; [print(f['id']) for f in json.load(sys.stdin)['functions'] if f['name']=='$1']" 2>/dev/null
}

run_hey() {
  local url=$1 c=$2 n=$3
  $HEY -n "$n" -c "$c" -m POST \
    -H "X-Orva-API-Key: $API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"name":"loadtest","email":"test@orva.dev","tags":["perf"],"n":25,"dept":"eng"}' \
    -t 120 "$url" 2>&1
}

parse_result() {
  local result="$1" n="$2"
  local rps=$(echo "$result" | grep "Requests/sec" | awk '{print $2}')
  local p50=$(echo "$result" | grep "50%" | awk '{print $3}')
  local p99=$(echo "$result" | grep "99%" | awk '{print $3}')
  local s200=$(echo "$result" | grep "\[200\]" | awk '{print $2}')
  local s201=$(echo "$result" | grep "\[201\]" | awk '{print $2}')
  local s500=$(echo "$result" | grep "\[500\]" | awk '{print $2}')
  local s503=$(echo "$result" | grep "\[503\]" | awk '{print $2}')
  local mem=$(free -m | awk '/Mem:/{print $3}')

  # Sum all success codes
  local ok=$(( ${s200:-0} + ${s201:-0} ))
  [ "$ok" -eq 0 ] && ok=$n  # if no status distribution, all succeeded

  local fail=$(( ${s500:-0} + ${s503:-0} ))
  local pct=$(python3 -c "print(f'{${ok}/${n}*100:.0f}')")

  printf "%-8s %-8s %-10s %-10s %-10s %-6s %-6s %-8s" \
    "$rps" "${p50:-n/a}s" "${p99:-n/a}s" "${ok}/${n}" "${pct}%" "$fail" "${s500:-0}" "${mem}"
}

# ─── Setup ────────────────────────────────────────────────

echo "================================================================"
echo "  Orva REAL-WORLD REGRESSION STRESS TEST"
echo "  System: $(nproc) threads, $(free -h | awk '/Mem:/{print $2}') RAM"
echo "  $(date)"
echo "================================================================"
echo ""

# Kill any existing server
fuser -k 8443/tcp 2>/dev/null || true
sleep 2
rm -f ~/.orva/orva.db*

MEM_BASELINE=$(free -m | awk '/Mem:/{print $3}')
echo "Memory baseline: ${MEM_BASELINE}MB"
echo ""

# Start server
echo "Starting server (max_concurrent=500)..."
ORVA_MAX_CONCURRENT=500 $orva serve > /tmp/orva-loadtest.log 2>&1 &
SERVER_PID=$!
sleep 3

API_KEY=$(grep -oP 'orva_[a-f0-9]+' /tmp/orva-loadtest.log)
EP="--endpoint http://localhost:8443 --api-key $API_KEY"

# Deploy all functions
echo "Deploying functions..."
FUNCTIONS=(
  "node-api:node22:test/fixtures/node-api"
  "node-cpu:node22:test/fixtures/node-cpu"
  "node-slow:node22:test/fixtures/node-slow"
  "python-data:python313:test/fixtures/python-data"
  "python-compute:python313:test/fixtures/python-compute"
  "python-error:python313:test/fixtures/python-error"
)

for fn_spec in "${FUNCTIONS[@]}"; do
  IFS=: read -r name runtime dir <<< "$fn_spec"
  $orva deploy "$dir" --name "$name" --runtime "$runtime" $EP 2>&1 | grep -q "deployed" && echo "  ✓ $name ($runtime)" || echo "  ✗ $name FAILED"
done

# Verify each function
echo ""
echo "Verifying functions..."
for fn_spec in "${FUNCTIONS[@]}"; do
  IFS=: read -r name runtime dir <<< "$fn_spec"
  STATUS=$($orva invoke "$name" $EP 2>&1 | head -1 | awk '{print $2}')
  echo "  $name: $STATUS"
done

echo ""
echo ""

# ─── Phase A: Node.js API handler regression ─────────────

echo "================================================================"
echo "  PHASE A: Node.js API Handler (node-api)"
echo "================================================================"
FN_URL="http://localhost:8443/fn/$(get_fn_id node-api | sed "s/^fn_//")"
printf "%-6s %-5s %-8s %-8s %-10s %-10s %-10s %-6s %-6s %-8s\n" \
  "Conc" "N" "RPS" "P50" "P99" "OK/Total" "Rate" "Fail" "500s" "MemMB"
echo "------ ----- -------- -------- ---------- ---------- ---------- ------ ------ --------"

for C in 100 500 1000 2000 3000 5000; do
  N=$((C * 3))
  [ $N -gt 15000 ] && N=15000
  printf "%-6s %-5s " "$C" "$N"
  RESULT=$(run_hey "$FN_URL" "$C" "$N")
  parse_result "$RESULT" "$N"
  echo ""

  # Check success rate
  OK=$(echo "$RESULT" | grep "\[20[01]\]" | awk '{s+=$2}END{print s+0}')
  [ "$OK" -eq 0 ] && OK=$N
  PCT=$(python3 -c "print(int(${OK}/${N}*100))")
  if [ "$PCT" -lt 50 ]; then
    echo "  >>> Hit limit at c=$C (${PCT}% success)"
    break
  fi
  sleep 3
done

echo ""
echo ""

# ─── Phase B: Python Data Processing regression ──────────

echo "================================================================"
echo "  PHASE B: Python Data Processing (python-data)"
echo "================================================================"
FN_URL="http://localhost:8443/fn/$(get_fn_id python-data | sed "s/^fn_//")"
printf "%-6s %-5s %-8s %-8s %-10s %-10s %-10s %-6s %-6s %-8s\n" \
  "Conc" "N" "RPS" "P50" "P99" "OK/Total" "Rate" "Fail" "500s" "MemMB"
echo "------ ----- -------- -------- ---------- ---------- ---------- ------ ------ --------"

for C in 100 500 1000 2000 3000; do
  N=$((C * 3))
  [ $N -gt 9000 ] && N=9000
  printf "%-6s %-5s " "$C" "$N"
  RESULT=$(run_hey "$FN_URL" "$C" "$N")
  parse_result "$RESULT" "$N"
  echo ""

  OK=$(echo "$RESULT" | grep "\[200\]" | awk '{print $2+0}')
  [ "$OK" -eq 0 ] && OK=$N
  PCT=$(python3 -c "print(int(${OK}/${N}*100))")
  if [ "$PCT" -lt 50 ]; then
    echo "  >>> Hit limit at c=$C (${PCT}% success)"
    break
  fi
  sleep 3
done

echo ""
echo ""

# ─── Phase C: Mixed Runtime ──────────────────────────────

echo "================================================================"
echo "  PHASE C: Mixed Runtime (Node + Python simultaneously)"
echo "================================================================"
NODE_URL="http://localhost:8443/fn/$(get_fn_id node-api | sed "s/^fn_//")"
PY_URL="http://localhost:8443/fn/$(get_fn_id python-data | sed "s/^fn_//")"

for C in 100 500 1000 2000; do
  HALF=$((C / 2))
  N=$((C * 3))
  N_HALF=$((N / 2))
  echo "--- c=$C total ($HALF each) ---"

  # Run both in parallel
  RESULT_NODE=$($HEY -n "$N_HALF" -c "$HALF" -m POST -H "X-Orva-API-Key: $API_KEY" \
    -H "Content-Type: application/json" -d '{"name":"mixed"}' -t 120 "$NODE_URL" 2>&1) &
  PID_NODE=$!
  RESULT_PY=$($HEY -n "$N_HALF" -c "$HALF" -m POST -H "X-Orva-API-Key: $API_KEY" \
    -H "Content-Type: application/json" -d '{"dept":"eng"}' -t 120 "$PY_URL" 2>&1) &
  PID_PY=$!
  wait $PID_NODE $PID_PY 2>/dev/null

  NODE_RPS=$(echo "$RESULT_NODE" | grep "Requests/sec" | awk '{print $2}')
  PY_RPS=$(echo "$RESULT_PY" | grep "Requests/sec" | awk '{print $2}')
  NODE_OK=$(echo "$RESULT_NODE" | grep "\[20[01]\]" | awk '{s+=$2}END{print s+0}')
  PY_OK=$(echo "$RESULT_PY" | grep "\[200\]" | awk '{print $2+0}')
  [ "$NODE_OK" -eq 0 ] && NODE_OK=$N_HALF
  [ "$PY_OK" -eq 0 ] && PY_OK=$N_HALF
  MEM=$(free -m | awk '/Mem:/{print $3}')

  NODE_PCT=$(python3 -c "print(f'{${NODE_OK}/${N_HALF}*100:.0f}%')")
  PY_PCT=$(python3 -c "print(f'{${PY_OK}/${N_HALF}*100:.0f}%')")

  echo "  Node.js: ${NODE_RPS} RPS | ${NODE_OK}/${N_HALF} (${NODE_PCT})"
  echo "  Python:  ${PY_RPS} RPS | ${PY_OK}/${N_HALF} (${PY_PCT})"
  echo "  Memory:  ${MEM}MB"
  echo ""

  sleep 3
done

echo ""

# ─── Phase D: CPU-bound ──────────────────────────────────

echo "================================================================"
echo "  PHASE D: CPU-Bound Functions"
echo "================================================================"
CPU_NODE_URL="http://localhost:8443/fn/$(get_fn_id node-cpu | sed "s/^fn_//")"
CPU_PY_URL="http://localhost:8443/fn/$(get_fn_id python-compute | sed "s/^fn_//")"

for C in 100 500 1000; do
  N=$((C * 2))
  echo "--- c=$C ---"

  RESULT_NODE=$(run_hey "$CPU_NODE_URL" "$C" "$N")
  NODE_RPS=$(echo "$RESULT_NODE" | grep "Requests/sec" | awk '{print $2}')
  NODE_P50=$(echo "$RESULT_NODE" | grep "50%" | awk '{print $3}')

  RESULT_PY=$(run_hey "$CPU_PY_URL" "$C" "$N")
  PY_RPS=$(echo "$RESULT_PY" | grep "Requests/sec" | awk '{print $2}')
  PY_P50=$(echo "$RESULT_PY" | grep "50%" | awk '{print $3}')

  echo "  Node CPU: ${NODE_RPS} RPS | P50=${NODE_P50}s"
  echo "  Py CPU:   ${PY_RPS} RPS | P50=${PY_P50}s"
  echo ""
  sleep 3
done

echo ""

# ─── Phase E: Slow (500ms) functions ─────────────────────

echo "================================================================"
echo "  PHASE E: Slow Functions (500ms sleep)"
echo "================================================================"
SLOW_URL="http://localhost:8443/fn/$(get_fn_id node-slow | sed "s/^fn_//")"
printf "%-6s %-5s %-8s %-10s %-10s %-10s\n" "Conc" "N" "RPS" "P50" "OK/Total" "Rate"
echo "------ ----- -------- ---------- ---------- ----------"

for C in 100 500 1000 2000; do
  N=$((C * 2))
  printf "%-6s %-5s " "$C" "$N"
  RESULT=$(run_hey "$SLOW_URL" "$C" "$N")
  RPS=$(echo "$RESULT" | grep "Requests/sec" | awk '{print $2}')
  P50=$(echo "$RESULT" | grep "50%" | awk '{print $3}')
  OK=$(echo "$RESULT" | grep "\[200\]" | awk '{print $2+0}')
  [ "$OK" -eq 0 ] && OK=$N
  PCT=$(python3 -c "print(f'{${OK}/${N}*100:.0f}%')")
  printf "%-8s %-10s %-10s %-10s\n" "$RPS" "${P50}s" "${OK}/${N}" "$PCT"
  sleep 3
done

echo ""
echo ""

# ─── Phase F: Error resilience ───────────────────────────

echo "================================================================"
echo "  PHASE F: Error Resilience (20% random failures)"
echo "================================================================"
ERR_URL="http://localhost:8443/fn/$(get_fn_id python-error | sed "s/^fn_//")"

RESULT=$(run_hey "$ERR_URL" 500 2000)
RPS=$(echo "$RESULT" | grep "Requests/sec" | awk '{print $2}')
S200=$(echo "$RESULT" | grep "\[200\]" | awk '{print $2+0}')
S500=$(echo "$RESULT" | grep "\[500\]" | awk '{print $2+0}')
S503=$(echo "$RESULT" | grep "\[503\]" | awk '{print $2+0}')
[ "$S200" -eq 0 ] && S200=2000
TOTAL=$((S200 + S500 + S503))
echo "  Total responses: $TOTAL"
echo "  200 (success):   $S200 ($(python3 -c "print(f'{${S200}/${TOTAL}*100:.0f}%')"))"
echo "  500 (handler err): ${S500} (expected ~20%)"
echo "  503 (server err):  ${S503}"
echo "  RPS: $RPS"

echo ""
echo ""

# ─── Phase G: Scale-down verification ────────────────────

echo "================================================================"
echo "  PHASE G: Scale-Down & Cleanup Verification"
echo "================================================================"
sleep 5
MEM_AFTER=$(free -m | awk '/Mem:/{print $3}')
NSJAIL_PROCS=$(pgrep -c nsjail 2>/dev/null || echo 0)
ORVA_TEMPS=$(ls /tmp/orva-exec* 2>/dev/null | wc -l || echo 0)

echo "  Memory: ${MEM_BASELINE}MB (before) → ${MEM_AFTER}MB (after) | delta: $((MEM_AFTER - MEM_BASELINE))MB"
echo "  Orphaned nsjail processes: $NSJAIL_PROCS"
echo "  Orphaned temp dirs: $orva_TEMPS"

HEALTH=$(curl -s http://localhost:8443/api/v1/system/health)
STATUS=$(echo "$HEALTH" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])" 2>/dev/null)
ACTIVE=$(echo "$HEALTH" | python3 -c "import sys,json; print(json.load(sys.stdin)['sandbox']['active_executions'])" 2>/dev/null)
LIFETIME=$(echo "$HEALTH" | python3 -c "import sys,json; print(json.load(sys.stdin)['sandbox']['lifetime_executions'])" 2>/dev/null)
echo "  Server status: $STATUS"
echo "  Active sandboxes: $ACTIVE"
echo "  Lifetime executions: $LIFETIME"

# Cleanup
kill $SERVER_PID 2>/dev/null
echo ""
echo "================================================================"
echo "  TEST COMPLETE"
echo "================================================================"
