#!/usr/bin/env bash
# egress-test.sh — verify per-function network_mode toggle.
#
# Asserts:
#  - Default function (network_mode=none) cannot resolve / connect outbound.
#  - PUT network_mode=egress unlocks outbound; warm pool drains and respawns.
#  - PUT back to none re-isolates.
#  - Invalid network_mode is rejected with 400 VALIDATION.
#  - Toggle latency overhead is bounded (sanity check on pasta startup).

set -euo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY}"

CURL=(curl -sf -H "X-Orva-API-Key: $KEY")

PASS=0; FAIL=0
check() {
    local label="$1" cond="$2"
    if [ "$cond" = "ok" ]; then echo "ok	$label"; PASS=$((PASS+1))
    else echo "fail	$label	${3-}"; FAIL=$((FAIL+1)); fi
}

# 1. Create + deploy fn that does an outbound HTTPS GET and reports the
#    connection's success/failure. Use a tiny node fn — example.com is
#    stable and lets us tell "blocked" (DNS fail / ENETUNREACH) from
#    "succeeded" (HTML body) cleanly.
fn_name="egress-test-$$"
create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node24\",\"memory_mb\":128,\"cpus\":1}")
fid=$(echo "$create" | jq -r '.id')
default_net=$(echo "$create" | jq -r '.network_mode')
check "default network_mode == none" \
    "$([ "$default_net" = none ] && echo ok || echo fail)" "got=$default_net"

# Adapter handler: try fetch(); return {ok, err, body, mode}.
read -r -d '' code <<'EOF' || true
exports.handler = async () => {
  let ok = false, err = null, body = null;
  try {
    const r = await fetch('https://example.com/', { signal: AbortSignal.timeout(3000) });
    body = (await r.text()).slice(0, 80);
    ok = r.ok;
  } catch (e) { err = String(e && (e.cause?.code || e.code || e.message)); }
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ok, err, body, mode: process.env.ORVA_NETWORK_MODE || null }),
  };
};
EOF

"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c "$code" '{code:$c, filename:"handler.js"}')" > /dev/null

# Wait for active.
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done
check "deploy active" "$([ "$s" = active ] && echo ok || echo fail)" "status=$s"

# 2. Invoke with network_mode=none — expect failure (no DNS, no TCP).
resp=$("${CURL[@]}" -X POST "$BASE/api/v1/invoke/$fid/" -d '{}')
ok_off=$(echo "$resp" | jq -r '.ok')
err_off=$(echo "$resp" | jq -r '.err')
check "default-mode invoke is blocked" \
    "$([ "$ok_off" = false ] && echo ok || echo fail)" \
    "ok=$ok_off err=$err_off"

# 3. Flip to egress.
upd=$("${CURL[@]}" -X PUT "$BASE/api/v1/functions/$fid" \
    -H "Content-Type: application/json" \
    -d '{"network_mode":"egress"}')
new_net=$(echo "$upd" | jq -r '.network_mode')
check "PUT network_mode=egress persists" \
    "$([ "$new_net" = egress ] && echo ok || echo fail)" "got=$new_net"

# Give RefreshForDeploy time to drain the warm pool. Worst case it's
# instant; budget 3s for the kill-on-release path.
sleep 3

resp2=$("${CURL[@]}" -X POST "$BASE/api/v1/invoke/$fid/" -d '{}')
ok_on=$(echo "$resp2" | jq -r '.ok')
body_on=$(echo "$resp2" | jq -r '.body')
check "egress-mode invoke reaches example.com" \
    "$([ "$ok_on" = true ] && echo ok || echo fail)" \
    "ok=$ok_on body=${body_on:-empty}"

# 4. Flip back to none — verify it re-isolates.
"${CURL[@]}" -X PUT "$BASE/api/v1/functions/$fid" \
    -H "Content-Type: application/json" \
    -d '{"network_mode":"none"}' > /dev/null
sleep 3
resp3=$("${CURL[@]}" -X POST "$BASE/api/v1/invoke/$fid/" -d '{}')
ok_off2=$(echo "$resp3" | jq -r '.ok')
check "PUT back to none re-isolates" \
    "$([ "$ok_off2" = false ] && echo ok || echo fail)" "ok=$ok_off2"

# 5. Validation: invalid network_mode → 400.
code_bad=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Orva-API-Key: $KEY" \
    -X PUT "$BASE/api/v1/functions/$fid" \
    -H "Content-Type: application/json" \
    -d '{"network_mode":"wat"}')
check "invalid network_mode rejected" \
    "$([ "$code_bad" = 400 ] && echo ok || echo fail)" "got=$code_bad"

# 6. Latency probe: warm-pool egress invoke should be reasonably fast.
"${CURL[@]}" -X PUT "$BASE/api/v1/functions/$fid" \
    -H "Content-Type: application/json" \
    -d '{"network_mode":"egress"}' > /dev/null
sleep 2
# Prime the pool.
"${CURL[@]}" -X POST "$BASE/api/v1/invoke/$fid/" -d '{}' > /dev/null
t0=$(date +%s%N)
"${CURL[@]}" -X POST "$BASE/api/v1/invoke/$fid/" -d '{}' > /dev/null
t1=$(date +%s%N)
warm_ms=$(( (t1 - t0) / 1000000 ))
# Generous bound — example.com round-trip dominates. We're checking
# pasta isn't catastrophically broken (>5s would indicate a hang).
check "warm-pool egress latency < 5000 ms" \
    "$([ "$warm_ms" -lt 5000 ] && echo ok || echo fail)" "warm_ms=$warm_ms"

# Cleanup.
"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" > /dev/null

echo
echo "=== egress-test: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
