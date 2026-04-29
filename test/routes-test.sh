#!/usr/bin/env bash
# routes-test.sh — verify custom-route exact + prefix matching, method
# restriction, and isolation from direct invokes.

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

# 1. Deploy a Node fn that echoes path + method.
fn_name="routes-test-$$"
create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node24\",\"memory_mb\":128,\"cpus\":1}")
fid=$(echo "$create" | jq -r '.id')

code='exports.handler = async (event) => ({ statusCode: 200, headers: {"Content-Type":"application/json"}, body: JSON.stringify({path: event.path, method: event.method}) });'
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c "$code" '{code:$c, filename:"handler.js"}')" > /dev/null

# Wait active.
for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done

# 2. Register an exact route + a non-reserved prefix route. (Routes
# starting with /api/, /fn/, /mcp/, /web/, /_orva/ are correctly rejected by
# the server with 400 — that's a deliberate isolation guard.)
"${CURL[@]}" -X POST "$BASE/api/v1/routes" \
    -H "Content-Type: application/json" \
    -d "{\"path\":\"/webhooks/stripe\",\"function_id\":\"$fid\",\"methods\":\"*\"}" > /dev/null
"${CURL[@]}" -X POST "$BASE/api/v1/routes" \
    -H "Content-Type: application/json" \
    -d "{\"path\":\"/customer/*\",\"function_id\":\"$fid\",\"methods\":\"*\"}" > /dev/null

# Verify the reserved-prefix guard fires (defence-in-depth check).
reserved_code=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Orva-API-Key: $KEY" \
    -X POST "$BASE/api/v1/routes" -H "Content-Type: application/json" \
    -d "{\"path\":\"/api/v1/customer/*\",\"function_id\":\"$fid\",\"methods\":\"*\"}")
check "reserved-prefix route registration rejected" \
    "$([ "$reserved_code" = 400 ] && echo ok || echo fail)" "got=$reserved_code"

sleep 1

# 3. Exact route hits.
exact=$(curl -sf -X POST -H "X-Orva-API-Key: $KEY" "$BASE/webhooks/stripe" -d '{}')
got_path=$(echo "$exact" | jq -r '.path')
check "exact route /webhooks/stripe → path=/" \
    "$([ "$got_path" = "/" ] && echo ok || echo fail)" "got=$got_path"

# 4. Prefix route hits — strip prefix, function sees the suffix.
prefix=$(curl -sf -X POST -H "X-Orva-API-Key: $KEY" "$BASE/customer/orders/123" -d '{}')
got_pp=$(echo "$prefix" | jq -r '.path')
check "prefix route /customer/* → path=/orders/123" \
    "$([ "$got_pp" = "/orders/123" ] && echo ok || echo fail)" "got=$got_pp"

# 5. Direct invoke still works while custom routes exist.
direct=$(curl -sf -X POST -H "X-Orva-API-Key: $KEY" "$BASE/fn/${fid#fn_}/" -d '{}')
got_direct=$(echo "$direct" | jq -r '.path')
check "direct invoke coexists" \
    "$([ "$got_direct" = "/" ] && echo ok || echo fail)" "got=$got_direct"

# 6. Method restriction — register a POST/PUT-only route and verify.
"${CURL[@]}" -X POST "$BASE/api/v1/routes" \
    -H "Content-Type: application/json" \
    -d "{\"path\":\"/restricted/post-only\",\"function_id\":\"$fid\",\"methods\":\"POST,PUT\"}" > /dev/null
sleep 1
get_status=$(curl -s -o /dev/null -w '%{http_code}' -X GET -H "X-Orva-API-Key: $KEY" "$BASE/restricted/post-only")
post_status=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "X-Orva-API-Key: $KEY" "$BASE/restricted/post-only" -d '{}')
check "GET on POST-only route → 405" \
    "$([ "$get_status" = 405 ] && echo ok || echo fail)" "got=$get_status"
check "POST on POST-only route → 200" \
    "$([ "$post_status" = 200 ] && echo ok || echo fail)" "got=$post_status"

# 7. Concurrent load on exact route. Capture full output to a file to
# sidestep SIGPIPE from awk closing the pipe early (with `set -o pipefail`
# this would otherwise abort the script).
if command -v hey >/dev/null 2>&1; then
    tmp=$(mktemp)
    hey -z 10s -c 25 -m POST -H "X-Orva-API-Key: $KEY" -d '{}' "$BASE/webhooks/stripe" > "$tmp" 2>&1 || true
    rps=$(awk '/Requests\/sec/ {print $2; exit}' "$tmp")
    rm -f "$tmp"
    rps_int=${rps%.*}
    check "exact route under load (c=25, 10s)" \
        "$([ "${rps_int:-0}" -gt 30 ] && echo ok || echo fail)" "rps=$rps"
fi

# Cleanup. DELETE expects ?path=… query param.
for p in "/webhooks/stripe" "/customer/*" "/restricted/post-only"; do
    enc=$(printf '%s' "$p" | jq -sRr @uri)
    "${CURL[@]}" -X DELETE "$BASE/api/v1/routes?path=$enc" > /dev/null 2>&1 || true
done
"${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" > /dev/null

echo
echo "routes-test	pass=$PASS	fail=$FAIL"
exit $FAIL
