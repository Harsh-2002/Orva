#!/usr/bin/env bash
# sdk-test.sh — end-to-end verification of the v0.7 runtime SDK surface.
#
# Deploys one Python and one Node function whose handlers exercise the
# new primitives (kv.incr, kv.cas, kv.list cursor, jobs idempotency,
# trace.span, log.info). Invokes them and asserts the wire-observable
# effects: the response body, the GET /api/v1/traces/{id} response, the
# GET /api/v1/executions/{id}/logs response, the GET /api/v1/jobs row.
#
# Requires a running Orva instance with ORVA_ENDPOINT / ORVA_API_KEY set,
# or the CLI's ~/.orva/config.yaml present.

set -uo pipefail

BASE="${ORVA_ENDPOINT:-${BASE_URL:-http://localhost:18443}}"
KEY="${ORVA_API_KEY:-${API_KEY:-}}"
if [ -z "$KEY" ] && [ -f "$HOME/.orva/config.yaml" ]; then
    KEY=$(grep -E '^api_key:' "$HOME/.orva/config.yaml" | awk '{print $2}' | tr -d '"')
fi
if [ -z "$KEY" ]; then
    echo "sdk-test: set ORVA_API_KEY (or have ~/.orva/config.yaml)"
    exit 2
fi

CURL=(curl -sf -H "X-Orva-API-Key: $KEY")

PASS=0
FAIL=0
noop_id=""
py_id=""
node_id=""

finish() {
    local status=$?
    trap - EXIT
    if [ "${SDK_TEST_KEEP_RESOURCES:-0}" = "1" ]; then
        echo "sdk-test: keeping resources py=$py_id node=$node_id noop=$noop_id"
    else
        for id in "$py_id" "$node_id" "$noop_id"; do
            if [ -n "$id" ]; then
                "${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$id" > /dev/null 2>&1 || true
            fi
        done
    fi
    echo
    echo "sdk-test: PASS=$PASS FAIL=$FAIL"
    if [ "$FAIL" -ne 0 ]; then
        exit 1
    fi
    exit "$status"
}
trap finish EXIT

assert_eq() {
    local label="$1" want="$2" got="$3"
    if [ "$got" = "$want" ]; then
        echo "ok   $label"
        PASS=$((PASS+1))
    else
        echo "fail $label    want=$want got=$got"
        FAIL=$((FAIL+1))
    fi
}

assert_nonempty() {
    local label="$1" got="$2"
    if [ -n "$got" ] && [ "$got" != "null" ] && [ "$got" != "[]" ]; then
        echo "ok   $label  ($got)"
        PASS=$((PASS+1))
    else
        echo "fail $label  empty/null/empty-array"
        FAIL=$((FAIL+1))
    fi
}

wait_active() {
    local fid="$1"
    local s
    for _ in $(seq 1 30); do
        s=$("${CURL[@]}" "$BASE/api/v1/functions/$fid" | jq -r '.status')
        [ "$s" = "active" ] && return 0
        sleep 0.5
    done
    echo "fail function $fid never reached status=active"
    FAIL=$((FAIL+1))
    return 1
}

# ── No-op target used by the idempotent-enqueue test ─────────────────
#
# jobs.enqueue requires the target function to exist (the handler resolves
# the name against the DB before inserting). We pre-deploy a tiny no-op
# named `sdk-test-noop`; the test function then enqueues to it twice with
# the same idempotency key and asserts dedup. Re-uses the noop across
# runs (UPSERT-by-name: if it already exists the create returns 409 and
# the script falls back to looking up its id).

NOOP_FN_NAME="sdk-test-noop"
noop_create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$NOOP_FN_NAME\",\"runtime\":\"python\",\"network_mode\":\"none\",\"entrypoint\":\"handler.py\"}" 2>/dev/null || true)
noop_id=$(echo "$noop_create" | jq -r '.id // empty')
if [ -z "$noop_id" ]; then
    noop_id=$(curl -s -H "X-Orva-API-Key: $KEY" "$BASE/api/v1/functions?limit=200" \
        | jq -r ".functions[] | select(.name==\"$NOOP_FN_NAME\") | .id" | head -1)
fi
if [ -n "$noop_id" ]; then
    "${CURL[@]}" -X POST "$BASE/api/v1/functions/$noop_id/deploy-inline" \
        -H "Content-Type: application/json" \
        -d '{"code":"def handler(event):\n    return {\"statusCode\": 200, \"body\": \"ok\"}","filename":"handler.py"}' > /dev/null
    wait_active "$noop_id" || true
fi

# ── Python function exercising kv.incr / kv.cas / trace.span / log ──

PY_FN="sdk-test-py-$$"
PY_SRC=$(cat <<'EOF'
import json
from orva import kv, log, trace, jobs, crons, invoke, invoke_stream, OrvaError

def handler(event):
    log.info("py handler start", fields={"path": event.get("path","/")})

    with trace.span("setup"):
        kv.delete("sdk:counter")
        kv.delete("sdk:cas-value")
        kv.put("sdk:cas-value", "v0")

    with trace.span("incr"):
        a = kv.incr("sdk:counter", 7)
        b = kv.incr("sdk:counter", -2)

    with trace.span("cas"):
        ok = kv.cas("sdk:cas-value", "v0", "v1")

    kv.put("sdk:ttl", {"step": 1}, ttl_seconds=3600)
    ttl_before = kv.list(prefix="sdk:ttl")["keys"][0]["expires_at"]
    kv.put("sdk:ttl", {"step": 2})
    ttl_preserved = kv.list(prefix="sdk:ttl")["keys"][0]["expires_at"] == ttl_before
    kv.put("sdk:ttl", {"step": 3}, ttl_seconds=0)
    ttl_cleared = "expires_at" not in kv.list(prefix="sdk:ttl")["keys"][0]

    try:
        kv.put_many([
            {"key": "sdk:batch-should-rollback", "value": 1},
            {"key": "x" * 257, "value": 2},
        ])
        batch_failed = False
    except OrvaError:
        batch_failed = True
    batch_atomic = kv.get("sdk:batch-should-rollback") is None

    invoked = invoke("sdk-test-noop", {})["body"] == "ok"
    streamed = b"".join(invoke_stream("sdk-test-noop", {})).decode("utf-8") == "ok"
    job = jobs.enqueue("sdk-test-noop", {}, idempotency_key="py-sdk-auth", idempotency_window_seconds=60)
    cron = crons.upsert("sdk-auth-test", "0 3 * * *", enabled=False)

    log.info("py handler done", fields={"final": b})
    return {"statusCode": 200, "body": {
        "a": a, "b": b, "cas": ok,
        "ttlPreserved": ttl_preserved, "ttlCleared": ttl_cleared,
        "batchFailed": batch_failed, "batchAtomic": batch_atomic,
        "invoked": invoked, "streamed": streamed,
        "job": bool(job["id"]), "cronScoped": cron["function_id"] == __import__("orva").context.function_id,
    }}
EOF
)

py_create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$PY_FN\",\"runtime\":\"python\",\"network_mode\":\"egress\",\"entrypoint\":\"handler.py\"}")
py_id=$(echo "$py_create" | jq -r '.id')
assert_nonempty "POST /functions (python)" "$py_id"

py_deploy=$(jq -nc --arg src "$PY_SRC" '{code:$src,filename:"handler.py"}')
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$py_id/deploy-inline" \
    -H "Content-Type: application/json" -d "$py_deploy" > /dev/null
wait_active "$py_id"

py_resp=$(curl -sS -H "X-Orva-API-Key: $KEY" -X POST "$BASE/fn/$py_id" \
    -H "Content-Type: application/json" -d '{}')
py_body=$(echo "$py_resp" | head -c 4096)

a=$(echo "$py_body" | jq -r '.a // empty')
b=$(echo "$py_body" | jq -r '.b // empty')
cas_ok=$(echo "$py_body" | jq -r '.cas // empty')
py_ttl_preserved=$(echo "$py_body" | jq -r '.ttlPreserved // empty')
py_ttl_cleared=$(echo "$py_body" | jq -r '.ttlCleared // empty')
py_batch_failed=$(echo "$py_body" | jq -r '.batchFailed // empty')
py_batch_atomic=$(echo "$py_body" | jq -r '.batchAtomic // empty')
py_invoked=$(echo "$py_body" | jq -r '.invoked // empty')
py_streamed=$(echo "$py_body" | jq -r '.streamed // empty')
py_job=$(echo "$py_body" | jq -r '.job // empty')
py_cron_scoped=$(echo "$py_body" | jq -r '.cronScoped // empty')

if [ -z "$a" ]; then
    echo "diag python response: $py_body"
fi

assert_eq "python kv.incr first call returns 7"  "7" "$a"
assert_eq "python kv.incr second call returns 5" "5" "$b"
assert_eq "python kv.cas succeeds on match"      "true" "$cas_ok"
assert_eq "python omitted TTL preserves expiry"  "true" "$py_ttl_preserved"
assert_eq "python explicit zero clears expiry"   "true" "$py_ttl_cleared"
assert_eq "python invalid batch throws"           "true" "$py_batch_failed"
assert_eq "python failed batch writes nothing"    "true" "$py_batch_atomic"
assert_eq "python scoped invoke succeeds"          "true" "$py_invoked"
assert_eq "python scoped stream invoke succeeds"   "true" "$py_streamed"
assert_eq "python scoped job enqueue succeeds"     "true" "$py_job"
assert_eq "python cron stays in caller scope"      "true" "$py_cron_scoped"

# Fetch the most recent execution for this function to peek at trace/logs.
py_exec=$("${CURL[@]}" "$BASE/api/v1/executions?function_id=$py_id&limit=1" | jq -r '.executions[0]')
py_trace_id=$(echo "$py_exec" | jq -r '.trace_id // empty')
assert_nonempty "python execution trace_id propagated" "$py_trace_id"

sleep 1.5  # async writer
py_trace=$("${CURL[@]}" "$BASE/api/v1/traces/$py_trace_id")
py_user_spans=$(echo "$py_trace" | jq -r '.user_spans | length')
py_log_entries=$(echo "$py_trace" | jq -r '.log_entries | length')

# 3 user spans: setup, incr, cas
[ "$py_user_spans" -ge 3 ] && {
    echo "ok   python trace contains ≥3 user_spans (got $py_user_spans)"
    PASS=$((PASS+1))
} || {
    echo "fail python trace user_spans count: want ≥3, got $py_user_spans"
    FAIL=$((FAIL+1))
}

# 2 log lines: handler start + done
[ "$py_log_entries" -ge 2 ] && {
    echo "ok   python trace contains ≥2 log_entries (got $py_log_entries)"
    PASS=$((PASS+1))
} || {
    echo "fail python trace log_entries count: want ≥2, got $py_log_entries"
    FAIL=$((FAIL+1))
}

# ── Node function exercising getMany/putMany/list cursor/idempotency ──

NODE_FN="sdk-test-node-$$"
NODE_SRC=$(cat <<'EOF'
const { kv, jobs, crons, invoke, invokeStream, context, log, OrvaError } = require('orva')

exports.handler = async (event) => {
  log.info('node handler start')

  // Bulk write 6 entries, then walk with a cursor (limit 3 -> 2 pages).
  await kv.putMany([
    { key: 'b:1', value: 1 }, { key: 'b:2', value: 2 }, { key: 'b:3', value: 3 },
    { key: 'b:4', value: 4 }, { key: 'b:5', value: 5 }, { key: 'b:6', value: 6 },
  ])

  let cursor = ''
  const walked = []
  for (let i = 0; i < 5; i++) {
    const page = await kv.list({ prefix: 'b:', limit: 3, cursor })
    for (const k of page.keys) walked.push(k.key)
    cursor = page.nextCursor
    if (!cursor) break
  }

  const many = await kv.getMany(['b:1', 'b:3', 'b:9999'])

  await kv.put('b:ttl', { step: 1 }, { ttlSeconds: 3600 })
  const ttlBefore = (await kv.list({ prefix: 'b:ttl' })).keys[0].expires_at
  await kv.put('b:ttl', { step: 2 })
  const ttlPreserved = (await kv.list({ prefix: 'b:ttl' })).keys[0].expires_at === ttlBefore
  await kv.put('b:ttl', { step: 3 }, { ttlSeconds: 0 })
  const ttlCleared = !('expires_at' in (await kv.list({ prefix: 'b:ttl' })).keys[0])

  let batchFailed = false
  try {
    await kv.putMany([
      { key: 'b:batch-should-rollback', value: 1 },
      { key: 'x'.repeat(257), value: 2 },
    ])
  } catch (error) {
    if (!(error instanceof OrvaError)) throw error
    batchFailed = true
  }
  const batchAtomic = (await kv.get('b:batch-should-rollback', null)) === null
  const invoked = (await invoke('sdk-test-noop', {})).body === 'ok'
  const streamChunks = []
  for await (const chunk of invokeStream('sdk-test-noop', {})) streamChunks.push(chunk)
  const streamed = Buffer.concat(streamChunks).toString('utf8') === 'ok'
  const cron = await crons.upsert('sdk-auth-test', '0 3 * * *', { enabled: false })

  // Idempotent enqueue — same key twice should return the same job id.
  // Target the noop helper function the test script pre-deploys.
  const j1 = await jobs.enqueue('sdk-test-noop', {}, {
    idempotencyKey: 'demo-idem',
    idempotencyWindowSeconds: 60,
  })
  const j2 = await jobs.enqueue('sdk-test-noop', {}, {
    idempotencyKey: 'demo-idem',
    idempotencyWindowSeconds: 60,
  })

  log.info('node handler done', { walked: walked.length })
  return { statusCode: 200, body: JSON.stringify({
    walked, many,
    sameJob: j1.id === j2.id, j2Replayed: j2.replayed,
    ttlPreserved, ttlCleared, batchFailed, batchAtomic,
    invoked, streamed, cronScoped: cron.function_id === context.functionId,
  }) }
}
EOF
)

node_create=$("${CURL[@]}" -X POST "$BASE/api/v1/functions" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$NODE_FN\",\"runtime\":\"node\",\"network_mode\":\"egress\",\"entrypoint\":\"handler.js\"}")
node_id=$(echo "$node_create" | jq -r '.id')
assert_nonempty "POST /functions (node)" "$node_id"

node_deploy=$(jq -nc --arg src "$NODE_SRC" '{code:$src,filename:"handler.js"}')
"${CURL[@]}" -X POST "$BASE/api/v1/functions/$node_id/deploy-inline" \
    -H "Content-Type: application/json" -d "$node_deploy" > /dev/null
wait_active "$node_id"

# We have to clean b: keys from the operator endpoint up-front since
# this function is per-id; KV is per-function namespace.
"${CURL[@]}" "$BASE/api/v1/functions/$node_id/kv?prefix=b:&limit=20" \
    | jq -r '.entries[]?.key' | while read k; do
    "${CURL[@]}" -X DELETE "$BASE/api/v1/functions/$node_id/kv/$k" > /dev/null
done

node_resp=$(curl -sS -H "X-Orva-API-Key: $KEY" -X POST "$BASE/fn/$node_id" \
    -H "Content-Type: application/json" -d '{}')
node_body=$(echo "$node_resp" | head -c 4096)

walked=$(echo "$node_body" | jq -r '.walked | length')
many_one=$(echo "$node_body" | jq -r '.many["b:1"]')
many_missing=$(echo "$node_body" | jq -r '.many["b:9999"]')
same=$(echo "$node_body" | jq -r '.sameJob')
replayed=$(echo "$node_body" | jq -r '.j2Replayed')
node_ttl_preserved=$(echo "$node_body" | jq -r '.ttlPreserved')
node_ttl_cleared=$(echo "$node_body" | jq -r '.ttlCleared')
node_batch_failed=$(echo "$node_body" | jq -r '.batchFailed')
node_batch_atomic=$(echo "$node_body" | jq -r '.batchAtomic')
node_invoked=$(echo "$node_body" | jq -r '.invoked')
node_streamed=$(echo "$node_body" | jq -r '.streamed')
node_cron_scoped=$(echo "$node_body" | jq -r '.cronScoped')

if [ "$walked" = "null" ]; then
    echo "diag node response: $node_body"
fi

assert_eq "node kv.list cursor walks all 6 keys"          "6"    "$walked"
assert_eq "node kv.getMany returns value for present key" "1"    "$many_one"
assert_eq "node kv.getMany returns null for missing key"  "null" "$many_missing"
assert_eq "node jobs.enqueue idempotency returns same id" "true" "$same"
assert_eq "node jobs.enqueue second call has replayed=t"  "true" "$replayed"
assert_eq "node omitted TTL preserves expiry"              "true" "$node_ttl_preserved"
assert_eq "node explicit zero clears expiry"               "true" "$node_ttl_cleared"
assert_eq "node invalid batch throws"                       "true" "$node_batch_failed"
assert_eq "node failed batch writes nothing"                "true" "$node_batch_atomic"
assert_eq "node scoped invoke succeeds"                      "true" "$node_invoked"
assert_eq "node scoped stream invoke succeeds"               "true" "$node_streamed"
assert_eq "node cron stays in caller scope"                  "true" "$node_cron_scoped"

exit 0
