#!/usr/bin/env bash
# rollback-test.sh — exercises the Round-G mini-git rollback feature end-to-end.
#
# Flow:
#   1. Deploy code A → record code_hash_A + deployment_id_A → invoke → assert "A"
#   2. Deploy code B (different content) → invoke → assert "B"
#   3. Rollback to deployment_id_A → invoke → assert "A"
#   4. Confirm a new deployment row exists with source='rollback',
#      parent_deployment_id=dep_A
#   5. Rollback forward to deployment_id_B → invoke → assert "B"

set -euo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY}"
CURL=(curl -s -H "X-Orva-API-Key: $KEY")

PASS=0; FAIL=0
check() {
    local label="$1" cond="$2"
    if [ "$cond" = "ok" ]; then echo "ok	$label"; PASS=$((PASS+1))
    else echo "fail	$label	${3:-}"; FAIL=$((FAIL+1)); fi
}

fn_name="rb-$$"
echo "# 0: setup function $fn_name"
"${CURL[@]}" -X POST "$BASE/api/v1/functions" -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node22\",\"memory_mb\":128,\"cpus\":1}" >/dev/null
fid=$("${CURL[@]}" "$BASE/api/v1/functions" | jq -r --arg n "$fn_name" '.functions[] | select(.name==$n) | .id')
[ -z "$fid" ] && { echo "fail	create fn"; exit 1; }

# Helper: deploy inline code, wait until active, return deployment_id.
deploy_inline() {
    local code="$1"
    local resp
    resp=$("${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
        -H "Content-Type: application/json" \
        -d "$(jq -n --arg c "$code" '{code:$c, filename:"handler.js"}')")
    local depid
    depid=$(echo "$resp" | jq -r '.deployment_id')
    # Wait for terminal state.
    for _ in $(seq 1 30); do
        local s
        s=$("${CURL[@]}" "$BASE/api/v1/deployments/$depid" | jq -r '.status')
        [ "$s" = "succeeded" ] && break
        [ "$s" = "failed" ] && { echo "deploy failed: $resp" >&2; exit 1; }
        sleep 1
    done
    echo "$depid"
}

invoke_body() {
    "${CURL[@]}" -X POST "$BASE/api/v1/invoke/$fid" -d '{}' --max-time 5
}

# 1. Deploy code A
echo "# 1: deploy code A"
codeA='module.exports = async () => ({ which: "A", v: 1 });'
depA=$(deploy_inline "$codeA")
sleep 1
respA=$(invoke_body)
[ "$(echo "$respA" | jq -r '.which')" = "A" ] && check "invoke A returns A" ok || check "invoke A returns A" fail "got: $respA"
hashA=$("${CURL[@]}" "$BASE/api/v1/deployments/$depA" | jq -r '.code_hash')
[ -n "$hashA" ] && [ "$hashA" != "null" ] && check "deployment A records code_hash" ok || check "deployment A records code_hash" fail "got: $hashA"

# 2. Deploy code B
echo "# 2: deploy code B"
codeB='module.exports = async () => ({ which: "B", v: 2 });'
depB=$(deploy_inline "$codeB")
sleep 1
respB=$(invoke_body)
[ "$(echo "$respB" | jq -r '.which')" = "B" ] && check "invoke B returns B" ok || check "invoke B returns B" fail "got: $respB"

# 3. Rollback to A
echo "# 3: rollback to A (deployment_id=$depA)"
rollResp=$("${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/rollback" \
    -H "Content-Type: application/json" -d "{\"deployment_id\":\"$depA\"}")
echo "$rollResp" | jq -e '.status == "succeeded" and .source == "rollback"' >/dev/null \
    && check "rollback response shape" ok \
    || check "rollback response shape" fail "got: $rollResp"
parent=$(echo "$rollResp" | jq -r '.parent_deployment_id')
[ "$parent" = "$depA" ] && check "rollback parent_deployment_id matches" ok || check "rollback parent_deployment_id" fail "got: $parent"

# Drain the old worker before invoke so we don't hit a zombie.
sleep 1
respAfterRoll=$(invoke_body)
[ "$(echo "$respAfterRoll" | jq -r '.which')" = "A" ] && check "invoke after rollback returns A" ok || check "invoke after rollback" fail "got: $respAfterRoll"

# 4. Deployments table now has at least 3 rows for this fn.
deps=$("${CURL[@]}" "$BASE/api/v1/functions/$fid/deployments?limit=20")
n=$(echo "$deps" | jq -r '.deployments | length')
[ "$n" -ge 3 ] && check "deployments table has $n rows (>=3)" ok || check "deployments count" fail "got: $n"

# 5. Roll forward to B
echo "# 5: rollback forward to B (deployment_id=$depB)"
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/rollback" \
    -H "Content-Type: application/json" -d "{\"deployment_id\":\"$depB\"}" >/dev/null
sleep 1
respFwd=$(invoke_body)
[ "$(echo "$respFwd" | jq -r '.which')" = "B" ] && check "roll-forward returns B" ok || check "roll-forward" fail "got: $respFwd"

# 6. Rollback to a no-op (already-active version) should be rejected.
# Use the most-recent succeeded rollback row whose code_hash == fn.code_hash —
# i.e. ask the server "what's the active deployment". depB still has the
# right code_hash but isn't the active *row* (the roll-forward in step 5
# created a synthetic 'rollback' row that supersedes it). Either way the
# handler compares hashes, not row IDs, so depB should still trigger the
# "already active" rejection.
echo "# 6: rollback to active (no-op) → 400 VALIDATION"
noop=$(curl -s -i -H "X-Orva-API-Key: $KEY" -H "Content-Type: application/json" \
    -X POST "$BASE/api/v1/functions/$fid/rollback" -d "{\"deployment_id\":\"$depB\"}")
status=$(printf '%s' "$noop" | head -n1 | awk '{print $2}' | tr -d '\r')
body=$(printf '%s' "$noop" | awk 'BEGIN{p=0} /^\r?$/ {p=1; next} p {print}')
code=$(printf '%s' "$body" | jq -r '.error.code // empty' 2>/dev/null || echo "")
if [ "$status" = "400" ] && [ "$code" = "VALIDATION" ]; then
    check "rollback to active rejected (400 VALIDATION)" ok
else
    check "rollback to active rejected" fail "status=$status code=$code body=${body:0:200}"
fi

# Cleanup.
"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" >/dev/null

echo
echo "rollback-test	pass=$PASS	fail=$FAIL"
exit $FAIL
