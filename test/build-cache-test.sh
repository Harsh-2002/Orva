#!/usr/bin/env bash
# build-cache-test.sh — the per-function dependency cache, from the outside.
#
# Orva keeps npm/pip downloads in <dataDir>/build-cache/<fnID> so a redeploy
# does not refetch every dependency. This suite covers what an API client can
# observe: a function with real dependencies builds, rebuilds, survives having
# its cache purged mid-life, and the purge endpoint refuses to be pointed at
# anything that is not a function.
#
# The reuse itself (npm's content store untouched on the second build) is
# asserted in the Go tests and by inspecting the data dir directly — it is not
# observable over HTTP, so this suite does not pretend to check it.

set -uo pipefail

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY}"
CURL=(curl -s -H "X-Orva-API-Key: $KEY")

PASS=0; FAIL=0
check() {
    local label="$1" cond="$2"
    if [ "$cond" = "ok" ]; then echo "ok	$label"; PASS=$((PASS+1))
    else echo "fail	$label	${3:-}"; FAIL=$((FAIL+1)); fi
}

fn_name="bcache-$$"
echo "# 0: setup function $fn_name"
"${CURL[@]}" -X POST "$BASE/api/v1/functions" -H "Content-Type: application/json" \
    -d "{\"name\":\"$fn_name\",\"runtime\":\"node\",\"memory_mb\":256,\"cpus\":1,\"network_mode\":\"egress\"}" >/dev/null
fid=$("${CURL[@]}" "$BASE/api/v1/functions?limit=1000" | jq -r --arg n "$fn_name" '.functions[] | select(.name==$n) | .id')
if [ -z "$fid" ] || [ "$fid" = "null" ]; then echo "fail	create fn"; exit 1; fi
cleanup() { "${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$fid" >/dev/null 2>&1 || true; }
trap cleanup EXIT

PKG='{"name":"bcache","version":"1.0.0","dependencies":{"ms":"2.1.3","lodash":"4.17.21"}}'

# deploy <marker> → echoes the terminal deployment status
deploy() {
    local code dep body status
    code="// $1
const ms = require('ms');
module.exports = async () => ({ ms: ms('2 days') });
"
    body=$(jq -n --arg c "$code" --arg d "$PKG" '{code:$c, filename:"handler.js", dependencies:$d}')
    dep=$("${CURL[@]}" -X POST "$BASE/api/v1/functions/$fid/deploy-inline" \
        -H "Content-Type: application/json" -d "$body" | jq -r '.deployment_id // .id // empty')
    if [ -z "$dep" ]; then echo "no-deployment"; return; fi
    for _ in $(seq 1 240); do
        status=$("${CURL[@]}" "$BASE/api/v1/deployments/$dep" | jq -r '.status // empty')
        case "$status" in succeeded|failed) break ;; esac
        sleep 1
    done
    echo "$status"
}

echo "# 1: cold build populates the cache"
s=$(deploy cold)
check "first deploy with dependencies succeeds" "$([ "$s" = succeeded ] && echo ok || echo fail)" "status=$s"

echo "# 2: rebuild against the warm cache"
s=$(deploy warm)
check "redeploy with the same dependencies succeeds" "$([ "$s" = succeeded ] && echo ok || echo fail)" "status=$s"

echo "# 3: explicit purge"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Orva-API-Key: $KEY" \
    -X DELETE "$BASE/api/v1/functions/$fid/build-cache")
check "DELETE build-cache returns 200" "$([ "$code" = 200 ] && echo ok || echo fail)" "http=$code"

# By name as well as by id — the whole function API accepts either.
code=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Orva-API-Key: $KEY" \
    -X DELETE "$BASE/api/v1/functions/$fn_name/build-cache")
check "DELETE build-cache resolves a function name" "$([ "$code" = 200 ] && echo ok || echo fail)" "http=$code"

# Purging a cache that is already gone is a no-op, not an error.
code=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Orva-API-Key: $KEY" \
    -X DELETE "$BASE/api/v1/functions/$fid/build-cache")
check "purging an empty cache is idempotent" "$([ "$code" = 200 ] && echo ok || echo fail)" "http=$code"

echo "# 4: the endpoint only ever names a real function"
for bad in "no-such-function" "..%2F.." "build-cache"; do
    code=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Orva-API-Key: $KEY" \
        -X DELETE "$BASE/api/v1/functions/$bad/build-cache")
    check "purge '$bad' is refused" "$([ "$code" = 404 ] || [ "$code" = 400 ] && echo ok || echo fail)" "http=$code"
done

echo "# 5: a purged function still deploys (the cache is rebuilt, not required)"
s=$(deploy after-purge)
check "deploy after purge succeeds" "$([ "$s" = succeeded ] && echo ok || echo fail)" "status=$s"

echo "# 6: invocation still works after all of that"
out=$("${CURL[@]}" -X POST "$BASE/fn/$fid" -H "Content-Type: application/json" -d '{}')
check "invoke returns the dependency's output" \
    "$(echo "$out" | grep -q '172800000' && echo ok || echo fail)" "body=$out"

echo
echo "build-cache: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
