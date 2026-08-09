#!/usr/bin/env bash
# egress-test.sh — verify per-function network_mode toggle + egress policy
# enforcement.
#
# Asserts:
#  - Default function (network_mode=none) cannot resolve / connect outbound.
#  - PUT network_mode=egress unlocks outbound; warm pool drains and respawns.
#  - PUT back to none re-isolates.
#  - Invalid network_mode is rejected with 400 VALIDATION.
#  - Toggle latency overhead is bounded (sanity check on NSTUN startup).
#  - The compiled NSTUN policy is published (backend=nstun + a generation).
#  - A blocklist rule for the destination actually REFUSES the connection,
#    and deleting the rule restores reachability.

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

# Everything this script creates is torn down on any exit path. A leaked
# blocklist rule would keep blocking example.com for every later run, so it
# must not survive a mid-script failure.
fid=""; rule_id=""
cleanup() {
    if [ -n "$rule_id" ]; then
        curl -s -o /dev/null -H "X-Orva-API-Key: $KEY" \
            -X DELETE "$BASE/api/v1/firewall/rules/$rule_id" || true
    fi
    if [ -n "$fid" ]; then
        curl -s -o /dev/null -H "X-Orva-API-Key: $KEY" \
            -X DELETE "$BASE/api/v1/functions/$fid" || true
    fi
}
trap cleanup EXIT

# Retire the warm pool so the next invoke spawns under the current policy
# generation. nsjail loads NSTUN rules once per worker, and the policy-driven
# recycle is rate-limited to one per 60s — a PUT that touches the spawn config
# drains the pool now instead.
cold_start() {
    "${CURL[@]}" -X PUT "$BASE/api/v1/functions/$1" \
        -H "Content-Type: application/json" -d '{"memory_mb":128}' > /dev/null
    sleep 1
}

# The `.ok` field of the handler's own response: true = the outbound HTTPS GET
# completed, false = it was refused.
probe_ok() {
    "${CURL[@]}" -X POST "$BASE/fn/${1#fn_}/" -d '{}' | jq -r '.ok'
}
probe_err() {
    "${CURL[@]}" -X POST "$BASE/fn/${1#fn_}/" -d '{}' | jq -r '.err'
}

# 1. Create + deploy fn that does an outbound HTTPS GET and reports the
#    connection's success/failure. Use a tiny node fn — example.com is
#    stable and lets us tell "blocked" (DNS fail / ENETUNREACH) from
#    "succeeded" (HTML body) cleanly.
fn_name="egress-test-$$"
create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node\",\"memory_mb\":128,\"cpus\":1}")
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
resp=$("${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}')
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

resp2=$("${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}')
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
resp3=$("${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}')
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
"${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}' > /dev/null
t0=$(date +%s%N)
"${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}' > /dev/null
t1=$(date +%s%N)
warm_ms=$(( (t1 - t0) / 1000000 ))
# Generous bound — example.com round-trip dominates. We're checking
# the NSTUN stack isn't catastrophically broken (>5s would indicate a hang).
check "warm-pool egress latency < 5000 ms" \
    "$([ "$warm_ms" -lt 5000 ] && echo ok || echo fail)" "warm_ms=$warm_ms"

# 7. Egress policy enforcement. The function stays in egress mode from step 6,
#    so a blocklist rule covering its destination is the only variable: if the
#    invoke still succeeds, the compiled NSTUN policy is a no-op.
status=$("${CURL[@]}" "$BASE/api/v1/firewall/status")
backend=$(echo "$status" | jq -r '.backend')
gen_before=$(echo "$status" | jq -r '.policy_generation')
check "egress policy backend == nstun" \
    "$([ "$backend" = nstun ] && echo ok || echo fail)" "got=$backend"
check "a policy generation is published" \
    "$([ -n "$gen_before" ] && [ "$gen_before" != null ] && echo ok || echo fail)" \
    "gen=$gen_before"

# Idempotency: a leftover rule from an interrupted run would 409 the create.
stale_id=$("${CURL[@]}" "$BASE/api/v1/firewall/rules" \
    | jq -r '[.rules[]? | select(.kind=="custom" and .value=="example.com") | .id][0] // empty')
if [ -n "$stale_id" ]; then
    curl -s -o /dev/null -H "X-Orva-API-Key: $KEY" \
        -X DELETE "$BASE/api/v1/firewall/rules/$stale_id"
fi

# A hostname rule is resolved by the daemon and compiled into REJECT rules for
# every address it answers with — which is exactly what this function fetches.
rule=$("${CURL[@]}" -X POST "$BASE/api/v1/firewall/rules" \
    -H "Content-Type: application/json" \
    -d '{"rule_type":"hostname","value":"example.com","label":"egress-test"}')
rule_id=$(echo "$rule" | jq -r '.id')
check "blocklist rule created" \
    "$([ -n "$rule_id" ] && [ "$rule_id" != null ] && echo ok || echo fail)" \
    "resp=$(echo "$rule" | head -c 160)"

gen_after=$("${CURL[@]}" "$BASE/api/v1/firewall/status" | jq -r '.policy_generation')
check "new rule publishes a new policy generation" \
    "$([ -n "$gen_after" ] && [ "$gen_after" != "$gen_before" ] && echo ok || echo fail)" \
    "before=$gen_before after=$gen_after"

cold_start "$fid"
ok_blocked=$(probe_ok "$fid")
check "blocklist rule refuses the outbound connection" \
    "$([ "$ok_blocked" = false ] && echo ok || echo fail)" "ok=$ok_blocked"
err_blocked=$(probe_err "$fid")
# NSTUN REJECT synthesizes a refusal rather than blackholing the packet, so a
# blocked function fails immediately instead of hanging until its timeout.
check "refusal surfaces as ECONNREFUSED" \
    "$([ "$err_blocked" = ECONNREFUSED ] && echo ok || echo fail)" "err=$err_blocked"

# 8. Deleting the rule restores reachability — proves the block came from the
#    policy and not from a broken sandbox.
curl -s -o /dev/null -H "X-Orva-API-Key: $KEY" \
    -X DELETE "$BASE/api/v1/firewall/rules/$rule_id"
rule_id=""
cold_start "$fid"
ok_restored=$(probe_ok "$fid")
check "deleting the rule restores reachability" \
    "$([ "$ok_restored" = true ] && echo ok || echo fail)" "ok=$ok_restored"

echo
echo "=== egress-test: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
