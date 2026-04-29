#!/usr/bin/env bash
# errors-test.sh — provoke each error code in the Round F catalog and
# assert: HTTP status, error.code, Retry-After header presence, and
# (where applicable) that error.details / error.hint are populated.
#
# Skips codes that need exotic infrastructure (TOO_MANY_REQUESTS at
# host-wide saturation, MEMORY_EXHAUSTED) — they're covered by atscale.

set -euo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY}"
CURL=(curl -s -H "X-Orva-API-Key: $KEY")

PASS=0; FAIL=0
check() {
    local label="$1" cond="$2"
    if [ "$cond" = "ok" ]; then echo "ok	$label"; PASS=$((PASS+1))
    else echo "fail	$label	$3"; FAIL=$((FAIL+1)); fi
}

# Extract HTTP status from a curl -i response. Handles trailing \r.
http_status() { printf '%s' "$1" | head -n1 | awk '{print $2}' | tr -d '\r'; }

# Extract a header value (case-insensitive) from a curl -i response.
http_header() {
    local name="$1" resp="$2"
    printf '%s' "$resp" | awk -v n="$(echo "$name" | tr 'A-Z' 'a-z')" '
        BEGIN{IGNORECASE=1}
        /^\r?$/ {exit}
        {
            line=$0; sub(/\r$/,"",line)
            if (tolower(line) ~ "^"n":") { sub(/^[^:]+:[ \t]*/,"",line); print line; exit }
        }
    '
}

# Extract the body from a curl -i response (everything after the empty line).
http_body() {
    printf '%s' "$1" | awk 'BEGIN{p=0} /^\r?$/ {p=1; next} p {print}'
}

http_assert() {
    local label="$1" want_status="$2" want_code="$3" want_retry_after="${4:-}" resp="$5"

    local got_status got_code got_retry
    got_status=$(http_status "$resp")
    got_code=$(http_body "$resp" | jq -r '.error.code // empty' 2>/dev/null || echo "")
    got_retry=$(http_header "Retry-After" "$resp")

    local ok="ok" reason=""
    if [ "$got_status" != "$want_status" ]; then ok="fail"; reason="status got=$got_status want=$want_status"; fi
    if [ -z "$reason" ] && [ "$got_code" != "$want_code" ]; then ok="fail"; reason="code got=$got_code want=$want_code"; fi
    if [ -z "$reason" ] && [ -n "$want_retry_after" ] && [ -z "$got_retry" ]; then ok="fail"; reason="missing Retry-After"; fi
    check "$label" "$ok" "$reason"
}

# 1. PAYLOAD_TOO_LARGE — POST > 6 MB body via stdin (argv would overflow).
echo "# 1: PAYLOAD_TOO_LARGE"
big_file=$(mktemp)
python3 -c "import sys; sys.stdout.write('x'*8000000)" > "$big_file"
resp=$(curl -s -i -H "X-Orva-API-Key: $KEY" -H "Content-Type: application/json" \
    -X POST "$BASE/api/v1/functions" --data-binary @"$big_file" 2>&1)
rm -f "$big_file"
http_assert "PAYLOAD_TOO_LARGE 413" "413" "PAYLOAD_TOO_LARGE" "" "$resp"

# 2. WORKER_CRASHED — deploy a fn whose handler exits the worker process.
echo "# 2: WORKER_CRASHED"
fn_name="errs-crash-$$"
"${CURL[@]}" -X POST "$BASE/api/v1/functions" -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node24\",\"memory_mb\":128,\"cpus\":1}" >/dev/null
fid=$("${CURL[@]}" "$BASE/api/v1/functions" | jq -r --arg n "$fn_name" '.functions[] | select(.name==$n) | .id')

crash_code='exports.handler = async () => { process.exit(1); };'
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c "$crash_code" '{code:$c, filename:"handler.js"}')" >/dev/null

for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = active ] && break
    sleep 1
done

resp=$(curl -s -i -X POST -H "X-Orva-API-Key: $KEY" "$BASE/fn/${fid#fn_}/" -d '{}')
http_assert "WORKER_CRASHED 502" "502" "WORKER_CRASHED" "" "$resp"

"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" >/dev/null

# 3. TIMEOUT — handler that sleeps past timeout_ms.
echo "# 3: TIMEOUT"
fn_name="errs-timeout-$$"
"${CURL[@]}" -X POST "$BASE/api/v1/functions" -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node24\",\"memory_mb\":128,\"cpus\":1,\"timeout_ms\":1000}" >/dev/null
fid=$("${CURL[@]}" "$BASE/api/v1/functions" | jq -r --arg n "$fn_name" '.functions[] | select(.name==$n) | .id')

slow='exports.handler = async () => { await new Promise(r => setTimeout(r, 5000)); return { statusCode: 200, body: "" }; };'
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c "$slow" '{code:$c, filename:"handler.js"}')" >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = active ] && break
    sleep 1
done

resp=$(curl -s -i -X POST --max-time 30 -H "X-Orva-API-Key: $KEY" "$BASE/fn/${fid#fn_}/" -d '{}')
http_assert "TIMEOUT 504" "504" "TIMEOUT" "" "$resp"

"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" >/dev/null

# 4. NOT_FOUND — invoke nonexistent fn id.
echo "# 4: NOT_FOUND"
resp=$(curl -s -i -X POST -H "X-Orva-API-Key: $KEY" "$BASE/fn/definitely_not_real/" -d '{}')
http_assert "NOT_FOUND 404" "404" "NOT_FOUND" "" "$resp"

# 5. METHOD_NOT_ALLOWED — register a POST-only route, hit with GET.
echo "# 5: METHOD_NOT_ALLOWED via custom route"
fn_name="errs-method-$$"
"${CURL[@]}" -X POST "$BASE/api/v1/functions" -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node24\",\"memory_mb\":128,\"cpus\":1}" >/dev/null
fid=$("${CURL[@]}" "$BASE/api/v1/functions" | jq -r --arg n "$fn_name" '.functions[] | select(.name==$n) | .id')

ok_code='exports.handler = async () => ({ statusCode: 200, body: "{}" });'
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c "$ok_code" '{code:$c, filename:"handler.js"}')" >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = active ] && break
    sleep 1
done
"${CURL[@]}" -X POST "$BASE/api/v1/routes" -H "Content-Type: application/json" \
    -d "{\"path\":\"/errs/method-only\",\"function_id\":\"$fid\",\"methods\":\"POST\"}" >/dev/null
sleep 1
resp=$(curl -s -i -X GET -H "X-Orva-API-Key: $KEY" "$BASE/errs/method-only")
http_assert "METHOD_NOT_ALLOWED 405" "405" "METHOD_NOT_ALLOWED" "" "$resp"

"${CURL[@]}" -X DELETE "$BASE/api/v1/routes?path=$(printf '%s' "/errs/method-only" | jq -sRr @uri)" >/dev/null
"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" >/dev/null

# 6. POOL_AT_CAPACITY — set max_warm=1, hammer with c=10 and short ctx.
echo "# 6: POOL_AT_CAPACITY"
fn_name="errs-pool-$$"
"${CURL[@]}" -X POST "$BASE/api/v1/functions" -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node24\",\"memory_mb\":128,\"cpus\":1,\"timeout_ms\":2000}" >/dev/null
fid=$("${CURL[@]}" "$BASE/api/v1/functions" | jq -r --arg n "$fn_name" '.functions[] | select(.name==$n) | .id')

slow_ok='exports.handler = async () => { await new Promise(r => setTimeout(r, 1500)); return { statusCode: 200, body: "{}" }; };'
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c "$slow_ok" '{code:$c, filename:"handler.js"}')" >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = active ] && break
    sleep 1
done

# Pin pool max_warm=1 via the public API. PoolRefresh tears down the running
# pool so the next acquire picks up the new bounds without a server restart.
pool_resp=$("${CURL[@]}" -X PUT "$BASE/api/v1/pool/config" \
    -H "Content-Type: application/json" \
    -d "{\"function_id\":\"$fid\",\"min_warm\":1,\"max_warm\":1,\"idle_ttl_seconds\":600,\"target_concurrency\":1}")
if echo "$pool_resp" | jq -e '.max_warm == 1' >/dev/null 2>&1; then
    sleep 1  # let the pool refresh settle

    # Fire 5 invokes simultaneously. With max_warm=1 and a 1.5s handler,
    # at least one should hit POOL_AT_CAPACITY (waits past TimeoutMS).
    capacity_hit=0
    pids=()
    out_dir=$(mktemp -d)
    for i in 1 2 3 4 5; do
        ( curl -s -i --max-time 5 -X POST -H "X-Orva-API-Key: $KEY" "$BASE/fn/${fid#fn_}/" -d '{}' > "$out_dir/$i.txt" ) &
        pids+=($!)
    done
    wait "${pids[@]}" 2>/dev/null
    for f in "$out_dir"/*.txt; do
        if grep -q "POOL_AT_CAPACITY" "$f" 2>/dev/null; then capacity_hit=$((capacity_hit+1)); fi
    done
    rm -rf "$out_dir"
    check "POOL_AT_CAPACITY at least once under contention" \
        "$([ "$capacity_hit" -ge 1 ] && echo ok || echo fail)" "hits=$capacity_hit"
else
    echo "# (skipping POOL_AT_CAPACITY: pool/config PUT returned: $pool_resp)"
fi

"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" >/dev/null

# 7. NOT_ACTIVE — invoke a fn whose status is "inactive" (set via PUT). Was
# unreachable before the function-update handler honored the status field.
echo "# 7: NOT_ACTIVE"
fn_name="errs-inactive-$$"
"${CURL[@]}" -X POST "$BASE/api/v1/functions" -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node22\"}" >/dev/null
fid=$("${CURL[@]}" "$BASE/api/v1/functions" | jq -r --arg n "$fn_name" '.functions[] | select(.name==$n) | .id')
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c 'module.exports = async () => ({ok:true});' '{code:$c, filename:"handler.js"}')" >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = active ] && break
    sleep 1
done
"${CURL[@]}" -X PUT "$BASE/api/v1/functions/$fid" -H "Content-Type: application/json" \
    -d '{"status":"inactive"}' >/dev/null
resp=$(curl -s -i -X POST -H "X-Orva-API-Key: $KEY" "$BASE/fn/${fid#fn_}/" -d '{}')
http_assert "NOT_ACTIVE 409" "409" "NOT_ACTIVE" "" "$resp"

# Verify whitelist: bogus status value is rejected with 400 VALIDATION.
bogus=$(curl -s -i -X PUT -H "X-Orva-API-Key: $KEY" -H "Content-Type: application/json" \
    "$BASE/api/v1/functions/$fid" -d '{"status":"building"}')
http_assert "PUT status=building rejected (whitelist)" "400" "VALIDATION" "" "$bogus"

"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" >/dev/null

echo
echo "errors-test	pass=$PASS	fail=$FAIL"
exit $FAIL
