#!/usr/bin/env bash
# heavy-deploy-test.sh — verify the async build queue handles a real-world
# Python deploy with pip dependencies, that POST returns 202 quickly,
# that SSE log streams emit content during the build, and that the failure
# path is non-destructive (zero-downtime previous-version preservation).

set -euo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY}"

CURL=(curl -sf -H "X-Orva-API-Key: $KEY")

PASS=0; FAIL=0
check() {
    local label="$1" cond="$2"
    if [ "$cond" = "ok" ]; then echo "ok	$label"; PASS=$((PASS+1))
    else echo "fail	$label	$3"; FAIL=$((FAIL+1)); fi
}

fn_name="heavy-test-$$"
create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"python313\",\"memory_mb\":256,\"cpus\":1,\"timeout_ms\":30000}")
fid=$(echo "$create" | jq -r '.id')

# 1. First deploy: a trivial handler so we have a known-good baseline.
trivial='import json
def handler(event, context):
    return {"statusCode": 200, "headers": {"Content-Type":"application/json"}, "body": json.dumps({"v":"baseline"})}
'
body=$(jq -n --arg c "$trivial" '{code:$c, filename:"handler.py"}')
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" -d "$body" > /dev/null

# Wait baseline active.
for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done
check "baseline deploy active" "$([ "$s" = active ] && echo ok || echo fail)" "status=$s"

baseline_resp=$("${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}')
check "baseline invoke ok" \
    "$([ "$(echo "$baseline_resp" | jq -r '.v')" = "baseline" ] && echo ok || echo fail)" \
    "got=$baseline_resp"

# 2. Heavy deploy with real pip deps. Time the POST.
heavy='import json, requests
def handler(event, context):
    return {"statusCode": 200, "headers": {"Content-Type":"application/json"}, "body": json.dumps({"v":"heavy", "requests": requests.__version__})}
'
deps='requests==2.31.0
'
body=$(jq -n --arg c "$heavy" --arg d "$deps" '{code:$c, filename:"handler.py", dependencies:$d}')

t0=$(date +%s%N)
deploy=$("${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" -d "$body")
t1=$(date +%s%N)
post_ms=$(( (t1 - t0) / 1000000 ))
dep_id=$(echo "$deploy" | jq -r '.deployment_id')
check "POST /deploy-inline returned <500ms" \
    "$([ "$post_ms" -lt 500 ] && echo ok || echo fail)" "took=${post_ms}ms"
check "deployment_id returned" \
    "$([ -n "$dep_id" ] && [ "$dep_id" != null ] && echo ok || echo fail)" "got=$dep_id"

# 3. SSE log capture in background.
sse_log=/tmp/heavy-deploy-stream-$$.log
( timeout 90 curl -sN -H "X-Orva-API-Key: $KEY" "$BASE/api/v1/deployments/$dep_id/stream" > "$sse_log" 2>&1 || true ) &
SSE_PID=$!

# 4. Poll deployment until terminal.
deadline=$(( $(date +%s) + 120 ))
final_status=""
while [ "$(date +%s)" -lt $deadline ]; do
    final_status=$("${CURL[@]}" "$BASE/api/v1/deployments/$dep_id" | jq -r '.status')
    if [ "$final_status" = "succeeded" ] || [ "$final_status" = "failed" ]; then break; fi
    sleep 2
done
check "heavy deploy reached terminal status" \
    "$([ "$final_status" = succeeded ] || [ "$final_status" = failed ] && echo ok || echo fail)" \
    "status=$final_status"
check "heavy deploy succeeded" \
    "$([ "$final_status" = succeeded ] && echo ok || echo fail)" \
    "status=$final_status (check $sse_log for build errors)"

# Wait for SSE to flush.
wait $SSE_PID 2>/dev/null || true

# 5. SSE log content sanity.
log_size=$(wc -c < "$sse_log" 2>/dev/null || echo 0)
# With a warm pip cache the install output is small (~80 B). Just verify
# the stream produced *something* — the goal is to prove SSE works, not
# to police pip verbosity.
check "SSE log captured" \
    "$([ "$log_size" -gt 30 ] && echo ok || echo fail)" "size=${log_size}B"

# 6. Heavy invoke works.
if [ "$final_status" = "succeeded" ]; then
    sleep 2  # let the warm pool refresh
    heavy_resp=$("${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}')
    got_v=$(echo "$heavy_resp" | jq -r '.v')
    got_req_v=$(echo "$heavy_resp" | jq -r '.requests')
    check "heavy invoke returns v=heavy" \
        "$([ "$got_v" = "heavy" ] && echo ok || echo fail)" "got=$heavy_resp"
    check "requests module loaded inside sandbox" \
        "$([ "$got_req_v" = "2.31.0" ] && echo ok || echo fail)" "got=$got_req_v"
fi

# 7. Failure path: deploy with garbage requirements.txt.
garbage='import json
def handler(event, context):
    return {"statusCode": 200, "body": "{}"}
'
bad_deps='this-package-does-not-exist-12345==99.99.99
'
body=$(jq -n --arg c "$garbage" --arg d "$bad_deps" '{code:$c, filename:"handler.py", dependencies:$d}')
deploy=$("${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" -d "$body")
bad_dep_id=$(echo "$deploy" | jq -r '.deployment_id')

deadline=$(( $(date +%s) + 60 ))
bad_status=""
while [ "$(date +%s)" -lt $deadline ]; do
    bad_status=$("${CURL[@]}" "$BASE/api/v1/deployments/$bad_dep_id" | jq -r '.status')
    if [ "$bad_status" = "succeeded" ] || [ "$bad_status" = "failed" ]; then break; fi
    sleep 2
done
check "bad deploy → status=failed" \
    "$([ "$bad_status" = failed ] && echo ok || echo fail)" "status=$bad_status"

# 8. Function status preserved as previous active version.
fn_status=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
check "function stays active after failed redeploy" \
    "$([ "$fn_status" = active ] && echo ok || echo fail)" "status=$fn_status"

# 9. Previous code still serves traffic (zero-downtime).
preserved=$("${CURL[@]}" -X POST "$BASE/fn/${fid#fn_}/" -d '{}')
preserved_v=$(echo "$preserved" | jq -r '.v')
# Either heavy (if heavy deploy succeeded) or baseline — point is that
# something works, not a 500/503.
check "invoke after failed redeploy still works" \
    "$([ -n "$preserved_v" ] && [ "$preserved_v" != null ] && echo ok || echo fail)" \
    "got=$preserved"

# Save first 200 lines of the SSE log as evidence.
head -200 "$sse_log" > /home/dev/Orva/test/heavy-deploy-stream.log 2>/dev/null || true

# Cleanup.
rm -f "$sse_log"
"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" > /dev/null

echo
echo "heavy-deploy-test	pass=$PASS	fail=$FAIL"
exit $FAIL
