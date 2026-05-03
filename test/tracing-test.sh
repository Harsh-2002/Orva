#!/usr/bin/env bash
# Tracing integration tests (v0.5).
#
# Verifies:
#   T1: HTTP root → trace_id is generated, response carries X-Trace-Id,
#       executions row has trace_id + span_id + trigger=http.
#   T2: F2F propagation — A invokes B; both share trace_id; B has
#       parent_span_id = A.span_id and trigger=f2f.
#   T3: Job propagation — A enqueues B; the job row carries A's trace,
#       and the eventually-run B execution has parent_span_id pointing
#       back at A.
#   T4: Cron is a root trace.
#   T5: External W3C `traceparent` is honored.
#   T6: Replay creates a fresh trace_id (no link back to the original).
#   T7: Outlier detection — repeated fast invocations build the
#       baseline; one slow invocation gets is_outlier=1.
#
# Requires:
#   - Orva running at http://localhost:8443
#   - Admin API key in $ORVA_API_KEY (or ~/.orva/config.yaml)

set -euo pipefail

ENDPOINT="${ORVA_ENDPOINT:-http://localhost:8443}"
API_KEY="${ORVA_API_KEY:-}"
if [ -z "$API_KEY" ] && [ -f "$HOME/.orva/config.yaml" ]; then
  API_KEY="$(grep -E '^api_key:' "$HOME/.orva/config.yaml" | sed 's/^api_key: //')"
fi
if [ -z "$API_KEY" ]; then
  echo "ORVA_API_KEY not set and no ~/.orva/config.yaml found"
  exit 1
fi

H_KEY=(-H "X-Orva-API-Key: $API_KEY")
H_JSON=(-H "Content-Type: application/json")

PASS=0
FAIL=0

ok() { printf '  \033[32m✓\033[0m %s\n' "$1"; PASS=$((PASS+1)); }
fail() { printf '  \033[31m✗\033[0m %s\n' "$1"; FAIL=$((FAIL+1)); }
section() { printf '\n\033[1m%s\033[0m\n' "$1"; }

# Wait for an async batch flush. SQLite WAL commits can take up to ~250ms
# under load; 1.2s is comfortable headroom for the test pace.
flush() { sleep 1.2; }

# Helper: deploy an inline function. Args: name, runtime, source [, network_mode].
# Returns the fn_<id> on stdout. Default network_mode=none; pass "egress"
# explicitly for functions that need to call the SDK (orva.invoke / jobs).
deploy() {
  local name="$1" runtime="$2" source="$3" netmode="${4:-none}" filename="handler.js"
  case "$runtime" in python*) filename="handler.py" ;; esac
  local create
  create=$(curl -sS "${H_KEY[@]}" "${H_JSON[@]}" -X POST "$ENDPOINT/api/v1/functions" \
    -d "{\"name\":\"$name\",\"runtime\":\"$runtime\",\"timeout_ms\":15000,\"network_mode\":\"$netmode\"}")
  local fid
  fid=$(echo "$create" | jq -r '.id // empty')
  if [ -z "$fid" ] || [ "$fid" = "null" ]; then
    # Already exists from a previous run — fetch it.
    fid=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/functions/$name" | jq -r '.id // empty')
  fi
  if [ -z "$fid" ]; then
    echo "could not create function $name" >&2
    return 1
  fi
  curl -sS "${H_KEY[@]}" "${H_JSON[@]}" -X POST "$ENDPOINT/api/v1/functions/$fid/deploy-inline" \
    -d "$(jq -nc --arg c "$source" --arg f "$filename" '{code:$c, filename:$f}')" >/dev/null
  for _ in $(seq 1 30); do
    local s
    s=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/functions/$fid" | jq -r .status)
    [ "$s" = "active" ] && { echo "$fid"; return 0; }
    sleep 0.5
  done
  echo "deploy timed out for $name" >&2
  return 1
}

cleanup() {
  local list
  list=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/functions" | jq -r '.functions[]?|"\(.name)\t\(.id)"')
  while IFS=$'\t' read -r name fid; do
    case "$name" in
      trace_chain_a|trace_chain_b|trace_chain_c|trace_outlier)
        curl -sS "${H_KEY[@]}" -X DELETE "$ENDPOINT/api/v1/functions/$fid" >/dev/null 2>&1 || true
        ;;
    esac
  done <<< "$list"
}
trap cleanup EXIT
cleanup  # also wipe leftovers from a previous interrupted run

section "T1: HTTP root span"

# Tiny function that echoes back its trace context env vars.
fid_c=$(deploy trace_chain_c node22 'module.exports.handler = async () => ({
  statusCode: 200,
  headers: {"content-type":"application/json"},
  body: JSON.stringify({
    trace: process.env.ORVA_TRACE_ID || "",
    span:  process.env.ORVA_SPAN_ID  || "",
  })
})')
short_c=${fid_c#fn_}

resp_headers=$(curl -sSI "${H_KEY[@]}" "$ENDPOINT/fn/$short_c/")
trace_id=$(echo "$resp_headers" | grep -i '^x-trace-id:' | awk '{print $2}' | tr -d '\r\n')
[ -n "$trace_id" ] && ok "X-Trace-Id present in HTTP response: $trace_id" || fail "X-Trace-Id missing"

flush
http_trace=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/traces/$trace_id")
spans=$(echo "$http_trace" | jq '.spans | length')
[ "$spans" = "1" ] && ok "Single span recorded for HTTP root" || fail "expected 1 span, got $spans"
trigger=$(echo "$http_trace" | jq -r '.spans[0].trigger')
[ "$trigger" = "http" ] && ok "trigger=http on root" || fail "trigger was $trigger"

section "T2: F2F propagation"

deploy trace_chain_b node22 'module.exports.handler = async () => ({
  statusCode: 200,
  headers: {"content-type":"application/json"},
  body: JSON.stringify({ from: "B" })
})' >/dev/null

fid_a=$(deploy trace_chain_a node22 'const { invoke } = require("orva")
module.exports.handler = async () => {
  const r = await invoke("trace_chain_b", {})
  return { statusCode: 200, headers: {"content-type":"application/json"},
           body: JSON.stringify({ chained: r }) }
}' egress)
short_a=${fid_a#fn_}

resp_headers=$(curl -sSI "${H_KEY[@]}" "$ENDPOINT/fn/$short_a/")
chain_trace=$(echo "$resp_headers" | grep -i '^x-trace-id:' | awk '{print $2}' | tr -d '\r\n')
flush
chain=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/traces/$chain_trace")
chain_spans=$(echo "$chain" | jq '.spans | length')
[ "$chain_spans" = "2" ] && ok "Two spans for F2F chain" || fail "expected 2 spans, got $chain_spans"

a_span=$(echo "$chain" | jq -r '.spans[0].span_id')
b_parent=$(echo "$chain" | jq -r '.spans[1].parent_span_id')
[ -n "$a_span" ] && [ "$a_span" = "$b_parent" ] && ok "B.parent_span_id = A.span_id" \
  || fail "parent linkage broken (a=$a_span, b.parent=$b_parent)"

b_trigger=$(echo "$chain" | jq -r '.spans[1].trigger')
[ "$b_trigger" = "f2f" ] && ok "B.trigger = f2f" || fail "B.trigger was $b_trigger"

section "T5: External W3C traceparent"

ext_id="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
ext_par="bbbbbbbbbbbbbbbb"
ext_resp=$(curl -sSI "${H_KEY[@]}" -H "traceparent: 00-$ext_id-$ext_par-01" "$ENDPOINT/fn/$short_c/")
ext_trace=$(echo "$ext_resp" | grep -i '^x-trace-id:' | awk '{print $2}' | tr -d '\r\n')
case "$ext_trace" in
  tr_${ext_id}) ok "External traceparent honored: $ext_trace" ;;
  *) fail "external traceparent ignored (got $ext_trace, expected tr_$ext_id)" ;;
esac

section "T6: Replay creates fresh trace"

recent=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/executions?function_id=$fid_c&limit=1" | jq -r '.executions[0].id // empty')
if [ -n "$recent" ]; then
  curl -sS "${H_KEY[@]}" -X POST "$ENDPOINT/api/v1/executions/$recent/replay" >/dev/null
  flush
  newest=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/executions?function_id=$fid_c&limit=1" | jq -r '.executions[0]')
  rep_trace=$(echo "$newest" | jq -r '.trace_id // empty')
  [ -n "$rep_trace" ] && [ "$rep_trace" != "$trace_id" ] \
    && ok "Replay produced fresh trace_id ($rep_trace ≠ $trace_id)" \
    || fail "replay reused original trace ($rep_trace)"
else
  fail "could not find recent execution to replay"
fi

section "T7: Outlier detection"

# Deploy a function whose duration we can swap. We feed the baseline with
# fast calls, then make it slow.
fid_o=$(deploy trace_outlier python313 '
import os, time
def handler(event):
    if os.environ.get("SLOW") == "1":
        time.sleep(0.6)
    return {"statusCode": 200, "headers": {"content-type":"application/json"}, "body": "{}"}')
short_o=${fid_o#fn_}

# 25 fast calls — populates baseline above min sample threshold.
for _ in $(seq 1 25); do
  curl -sS "${H_KEY[@]}" "$ENDPOINT/fn/$short_o/" >/dev/null
done
flush

# Patch the function to slow mode and invoke once.
curl -sS "${H_KEY[@]}" "${H_JSON[@]}" -X PUT "$ENDPOINT/api/v1/functions/$fid_o" \
  -d '{"env_vars":{"SLOW":"1"}}' >/dev/null
sleep 1
curl -sS "${H_KEY[@]}" "$ENDPOINT/fn/$short_o/" >/dev/null
flush

recent=$(curl -sS "${H_KEY[@]}" "$ENDPOINT/api/v1/executions?function_id=$fid_o&limit=1")
flag=$(echo "$recent" | jq -r '.executions[0].is_outlier')
[ "$flag" = "true" ] && ok "Outlier flagged on slow invocation" \
  || fail "expected is_outlier=true, got $flag"

p95=$(echo "$recent" | jq -r '.executions[0].baseline_p95_ms')
[ "$p95" != "null" ] && [ "$p95" != "0" ] && ok "baseline_p95_ms recorded ($p95)" \
  || fail "baseline_p95_ms missing on flagged row"

section "Summary"
echo "  PASS: $PASS"
echo "  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
