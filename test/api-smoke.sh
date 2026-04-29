#!/usr/bin/env bash
# api-smoke.sh — fast smoke of every public REST endpoint. Catches
# serialization regressions (e.g. schema changes, scanner drift) and
# lets us verify the egress feature didn't break neighboring routes.
#
# This is "does the endpoint exist and respond with the expected family
# of status codes" — not deep functional coverage. Pair with run-all.sh.

set -uo pipefail   # not -e

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY}"

CURL=(curl -sf -H "X-Orva-API-Key: $KEY")
HCURL=(curl -s -o /dev/null -w '%{http_code}' -H "X-Orva-API-Key: $KEY")

PASS=0; FAIL=0
expect_code() {
    local label="$1" want="$2" got="$3"
    if [ "$got" = "$want" ]; then
        echo "ok   $label    HTTP $got"
        PASS=$((PASS+1))
    else
        echo "fail $label    expected HTTP $want, got $got"
        FAIL=$((FAIL+1))
    fi
}

# --- system ---
code=$("${HCURL[@]}" "$BASE/api/v1/system/health")
expect_code "GET  /system/health" 200 "$code"

code=$("${HCURL[@]}" "$BASE/api/v1/system/metrics.json")
expect_code "GET  /system/metrics.json" 200 "$code"

# --- auth (already onboarded) ---
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/auth/status")
expect_code "GET  /auth/status" 200 "$code"

# --- functions: create with egress, query, update, delete ---
fn_name="smoke-$$"
create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node24\",\"network_mode\":\"egress\"}")
fid=$(echo "$create" | jq -r '.id')
mode=$(echo "$create" | jq -r '.network_mode')
[ "$mode" = "egress" ] && expect_code "POST /functions {network_mode:egress}" 201 201 \
    || expect_code "POST /functions {network_mode:egress}" 201 wrong

code=$("${HCURL[@]}" "$BASE/api/v1/functions")
expect_code "GET  /functions" 200 "$code"

code=$("${HCURL[@]}" "$BASE/api/v1/functions/$fid")
expect_code "GET  /functions/{id}" 200 "$code"

code=$("${HCURL[@]}" -X PUT -H "Content-Type: application/json" \
    -d '{"network_mode":"none"}' "$BASE/api/v1/functions/$fid")
expect_code "PUT  /functions/{id} {network_mode:none}" 200 "$code"

code=$("${HCURL[@]}" -X PUT -H "Content-Type: application/json" \
    -d '{"network_mode":"wat"}' "$BASE/api/v1/functions/$fid")
expect_code "PUT  /functions/{id} invalid network_mode" 400 "$code"

# --- inline deploy → wait active → invoke ---
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
    -H "Content-Type: application/json" \
    -d "$(jq -n '{code:"exports.handler = async () => ({statusCode:200, body:\"ok\"});", filename:"handler.js"}')" > /dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
    s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done

code=$("${HCURL[@]}" -X POST "$BASE/api/v1/invoke/$fid/" -d '{}')
expect_code "POST /invoke/{id}/" 200 "$code"

code=$("${HCURL[@]}" "$BASE/api/v1/functions/$fid/source")
expect_code "GET  /functions/{id}/source" 200 "$code"

code=$("${HCURL[@]}" "$BASE/api/v1/functions/$fid/deployments")
expect_code "GET  /functions/{id}/deployments" 200 "$code"

# --- secrets (upsert returns 200, not 201) ---
code=$("${HCURL[@]}" -X POST "$BASE/api/v1/functions/$fid/secrets" \
    -H "Content-Type: application/json" -d '{"key":"FOO","value":"bar"}')
expect_code "POST /functions/{id}/secrets" 200 "$code"

code=$("${HCURL[@]}" "$BASE/api/v1/functions/$fid/secrets")
expect_code "GET  /functions/{id}/secrets" 200 "$code"

code=$("${HCURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid/secrets/FOO")
expect_code "DELETE /functions/{id}/secrets/{key}" 200 "$code"

# --- API keys (mounted at /api/v1/keys) ---
code=$("${HCURL[@]}" "$BASE/api/v1/keys")
expect_code "GET  /keys" 200 "$code"

key_create=$("${CURL[@]}" -X POST "$BASE/api/v1/keys" \
    -H "Content-Type: application/json" -d '{"name":"smoke"}' || echo '{}')
kid=$(echo "$key_create" | jq -r '.id // empty')
if [ -n "$kid" ]; then
    PASS=$((PASS+1)); echo "ok   POST /keys             HTTP 201"
    code=$("${HCURL[@]}" -X DELETE "$BASE/api/v1/keys/$kid")
    expect_code "DELETE /keys/{id}" 200 "$code"
else
    FAIL=$((FAIL+1)); echo "fail POST /keys             create returned no id"
fi

# --- routes ---
code=$("${HCURL[@]}" "$BASE/api/v1/routes")
expect_code "GET  /routes" 200 "$code"

route_path="/smoke-$$/x"
code=$("${HCURL[@]}" -X POST "$BASE/api/v1/routes" \
    -H "Content-Type: application/json" \
    -d "{\"path\":\"$route_path\",\"function_id\":\"$fid\",\"methods\":\"*\"}")
expect_code "POST /routes" 201 "$code"

# --- cleanup ---
code=$("${HCURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid")
expect_code "DELETE /functions/{id}" 200 "$code"

echo
echo "=== api-smoke: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
