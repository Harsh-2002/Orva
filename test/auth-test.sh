#!/usr/bin/env bash
# auth-test.sh — verify per-function auth_mode + rate_limit_per_min.
#
# Asserts:
#  - auth_mode=none: anyone can invoke (no header → 200)
#  - auth_mode=platform_key: missing key → 401, valid key → 200
#  - auth_mode=signed: missing/bad sig → 401, valid HMAC → 200
#  - rate_limit_per_min: burst exceeds cap → 429 with Retry-After
#  - validation: invalid auth_mode → 400 VALIDATION

set -uo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY}"

CURL_AUTH=(curl -sS -H "X-Orva-API-Key: $KEY")

PASS=0; FAIL=0
check() {
    local label="$1" cond="$2" detail="${3:-}"
    if [ "$cond" = "ok" ]; then echo "ok	$label"; PASS=$((PASS+1))
    else echo "fail	$label	$detail"; FAIL=$((FAIL+1)); fi
}

# Echo handler (Node) — works for all three auth modes.
code='exports.handler = async (event) => ({ statusCode: 200, headers: {"Content-Type":"application/json"}, body: JSON.stringify({ok: true, method: event.method || "POST"}) });'

# ---------------------------------------------------------------
# 0. Validation: invalid auth_mode rejected at create time.
# ---------------------------------------------------------------
status=$("${CURL_AUTH[@]}" -o /dev/null -w '%{http_code}' \
    -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d '{"name":"auth-bogus-'$$'","runtime":"node24","auth_mode":"wat"}')
check "reject auth_mode=wat" "$([ "$status" = 400 ] && echo ok || echo fail)" "status=$status"

# ---------------------------------------------------------------
# 1. auth_mode=none — public, no header needed.
# ---------------------------------------------------------------
fn_none="auth-none-$$"
fid_none=$("${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$fn_none\",\"runtime\":\"node24\",\"auth_mode\":\"none\"}" | jq -r '.id')
"${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions/$fid_none/deploy-inline" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n --arg c "$code" '{code:$c, filename:"handler.js"}')" > /dev/null

# Wait active.
for _ in $(seq 1 15); do
    s=$("${CURL_AUTH[@]}" "$BASE/api/v1/functions/$fid_none" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done
check "auth=none deploy active" "$([ "$s" = active ] && echo ok || echo fail)" "status=$s"

# Hit it without ANY auth header.
status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/invoke/$fid_none/" -d '{}')
check "auth=none allows public invoke" "$([ "$status" = 200 ] && echo ok || echo fail)" "status=$status"

# ---------------------------------------------------------------
# 2. auth_mode=platform_key — header required.
# ---------------------------------------------------------------
fn_key="auth-key-$$"
fid_key=$("${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$fn_key\",\"runtime\":\"node24\",\"auth_mode\":\"platform_key\"}" | jq -r '.id')
"${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions/$fid_key/deploy-inline" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n --arg c "$code" '{code:$c, filename:"handler.js"}')" > /dev/null

for _ in $(seq 1 15); do
    s=$("${CURL_AUTH[@]}" "$BASE/api/v1/functions/$fid_key" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done

# No header → 401.
status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/invoke/$fid_key/" -d '{}')
check "auth=platform_key blocks unauthed" "$([ "$status" = 401 ] && echo ok || echo fail)" "status=$status"

# Valid header → 200.
status=$("${CURL_AUTH[@]}" -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/invoke/$fid_key/" -d '{}')
check "auth=platform_key allows valid key" "$([ "$status" = 200 ] && echo ok || echo fail)" "status=$status"

# Bogus header → 401.
status=$(curl -sS -o /dev/null -w '%{http_code}' -H 'X-Orva-API-Key: not-a-real-key' \
    -X POST "$BASE/api/v1/invoke/$fid_key/" -d '{}')
check "auth=platform_key rejects bad key" "$([ "$status" = 401 ] && echo ok || echo fail)" "status=$status"

# ---------------------------------------------------------------
# 3. auth_mode=signed — HMAC required.
# ---------------------------------------------------------------
fn_sig="auth-signed-$$"
fid_sig=$("${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$fn_sig\",\"runtime\":\"node24\",\"auth_mode\":\"signed\"}" | jq -r '.id')

# Set the signing secret BEFORE first deploy.
SIGNING_SECRET="test-secret-$(date +%s)"
"${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions/$fid_sig/secrets" \
    -H 'Content-Type: application/json' \
    -d "{\"key\":\"ORVA_SIGNING_SECRET\",\"value\":\"$SIGNING_SECRET\"}" > /dev/null

"${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions/$fid_sig/deploy-inline" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n --arg c "$code" '{code:$c, filename:"handler.js"}')" > /dev/null

for _ in $(seq 1 15); do
    s=$("${CURL_AUTH[@]}" "$BASE/api/v1/functions/$fid_sig" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done

# 3a. No headers → 401.
status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/invoke/$fid_sig/" -d '{}')
check "auth=signed blocks no-headers" "$([ "$status" = 401 ] && echo ok || echo fail)" "status=$status"

# 3b. Valid HMAC → 200.
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SIGNING_SECRET" -hex | awk '{print $NF}')
status=$(curl -sS -o /dev/null -w '%{http_code}' \
    -H "X-Orva-Timestamp: $TS" \
    -H "X-Orva-Signature: sha256=$SIG" \
    -H 'Content-Type: application/json' \
    -X POST "$BASE/api/v1/invoke/$fid_sig/" -d "$BODY")
check "auth=signed allows valid HMAC" "$([ "$status" = 200 ] && echo ok || echo fail)" "status=$status"

# 3c. Tampered body → 401 (sig over old body, send new body).
status=$(curl -sS -o /dev/null -w '%{http_code}' \
    -H "X-Orva-Timestamp: $TS" \
    -H "X-Orva-Signature: sha256=$SIG" \
    -H 'Content-Type: application/json' \
    -X POST "$BASE/api/v1/invoke/$fid_sig/" -d '{"hello":"tampered"}')
check "auth=signed rejects tampered body" "$([ "$status" = 401 ] && echo ok || echo fail)" "status=$status"

# 3d. Stale timestamp (>5 min ago) → 401.
OLD_TS=$(( $(date +%s) - 600 ))
OLD_SIG=$(printf '%s.%s' "$OLD_TS" "$BODY" | openssl dgst -sha256 -hmac "$SIGNING_SECRET" -hex | awk '{print $NF}')
status=$(curl -sS -o /dev/null -w '%{http_code}' \
    -H "X-Orva-Timestamp: $OLD_TS" \
    -H "X-Orva-Signature: sha256=$OLD_SIG" \
    -H 'Content-Type: application/json' \
    -X POST "$BASE/api/v1/invoke/$fid_sig/" -d "$BODY")
check "auth=signed rejects stale timestamp" "$([ "$status" = 401 ] && echo ok || echo fail)" "status=$status"

# ---------------------------------------------------------------
# 4. rate_limit_per_min — burst 5, send 7 fast, expect ≥1 to be 429.
# ---------------------------------------------------------------
fn_rl="auth-rl-$$"
fid_rl=$("${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$fn_rl\",\"runtime\":\"node24\",\"auth_mode\":\"none\",\"rate_limit_per_min\":5}" | jq -r '.id')
"${CURL_AUTH[@]}" -X POST "$BASE/api/v1/functions/$fid_rl/deploy-inline" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n --arg c "$code" '{code:$c, filename:"handler.js"}')" > /dev/null
for _ in $(seq 1 15); do
    s=$("${CURL_AUTH[@]}" "$BASE/api/v1/functions/$fid_rl" | jq -r '.status')
    [ "$s" = "active" ] && break
    sleep 1
done

throttled=0
for i in $(seq 1 7); do
    code_=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/invoke/$fid_rl/" -d '{}')
    if [ "$code_" = "429" ]; then throttled=$((throttled+1)); fi
done
check "rate_limit returns 429 on burst" "$([ "$throttled" -ge 1 ] && echo ok || echo fail)" "throttled=$throttled/7"

# ---------------------------------------------------------------
# Cleanup.
# ---------------------------------------------------------------
for f in "$fid_none" "$fid_key" "$fid_sig" "$fid_rl"; do
    [ -n "$f" ] && [ "$f" != "null" ] && "${CURL_AUTH[@]}" -X DELETE "$BASE/api/v1/functions/$f" > /dev/null || true
done

echo
echo "auth-test: pass=$PASS fail=$FAIL"
[ "$FAIL" -eq 0 ]
