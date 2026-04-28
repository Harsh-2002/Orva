#!/usr/bin/env bash
# ceiling.sh — Sustained-load ramp to find the real throughput ceiling.
#
# Runs 120s at each concurrency step after a 60s warmup. Writes CSV to
# stdout so results from different builds can be compared (paste + diff).
#
# Usage:
#   bash test/ceiling.sh <api-key> <fn-id> [base-url]
#
# Output format (CSV):
#   label,c,n_requests,rps,p50_ms,p95_ms,p99_ms,err_pct,mem_mb,peak_busy,peak_idle
#
# "label" is a free-form tag (default "run"); useful for distinguishing
# baseline vs after runs when concatenating.
#
# The rows end with a "max," row summarising the peak rps and the concurrency
# level that achieved it. "knee," row marks the point where p99 > p50 * 3.

set -euo pipefail

KEY="${1:?api key required}"
FN="${2:?function id required}"
BASE="${3:-http://localhost:18443}"
LABEL="${CEILING_LABEL:-run}"

if ! command -v hey >/dev/null 2>&1; then
  echo "hey is required (github.com/rakyll/hey)" >&2
  exit 2
fi

URL="$BASE/api/v1/invoke/$FN/"

# Warmup — makes sure the pool is spawned + WAL sized.
echo "# warmup 60s at c=5 ..." >&2
hey -z 60s -c 5 -m POST -H "X-Orva-API-Key: $KEY" -d '{}' "$URL" \
    >/dev/null 2>&1 || true
echo "# warmup done" >&2

# CSV header
echo "label,c,n_requests,rps,p50_ms,p95_ms,p99_ms,err_pct,mem_mb,peak_busy,peak_idle"

# Parse a hey summary into CSV fields. hey writes p50/p95/p99 as seconds.
# Expected input: full hey stdout text.
parse_hey() {
  awk -v c="$1" -v label="$LABEL" -v mem="$2" -v pb="$3" -v pi="$4" '
    /Total:/        { total_s = $2 }
    /Requests\/sec/ { rps     = $2 }
    /^Number of errors:/ { errs = $4 }
    /50%/           { p50 = $2 }
    /95%/           { p95 = $2 }
    /99%/           { p99 = $2 }
    /Total data:/   { bytes = $3 }
    /Requests in total:/ { n = $NF }
    /200\t/         { ok = $1 }   # "[200] \t N responses"
    /^Status code distribution:/ { in_status = 1 ; next }
    in_status && /\[/ {
      code=$2; gsub(/[^0-9]/,"",code)
      cnt=$3;  gsub(/[^0-9]/,"",cnt)
      if (code=="200") ok += cnt; else bad += cnt
    }
    /^Error distribution:/ { in_status = 0 }
    END {
      tot = ok + bad + 0
      err_pct = tot ? (bad * 100.0 / tot) : 0
      printf "%s,%d,%d,%.2f,%.2f,%.2f,%.2f,%.2f,%d,%d,%d\n",
             label, c, tot, rps+0,
             (p50+0)*1000, (p95+0)*1000, (p99+0)*1000,
             err_pct, mem+0, pb+0, pi+0
    }
  '
}

# Poll /system/metrics once to snapshot mem + peak pool counters for a fn.
snapshot() {
  curl -sf -H "X-Orva-API-Key: $KEY" "$BASE/api/v1/system/metrics" | \
    awk -v fn="$FN" '
      /orva_host_mem_free_mb/ { mem = $2 }
      $1 ~ /orva_pool_busy\{function_id="/ && $0 ~ fn { busy = $2 }
      $1 ~ /orva_pool_idle\{function_id="/ && $0 ~ fn { idle = $2 }
      END { printf "%d %d %d\n", mem+0, busy+0, idle+0 }
    '
}

# Container RSS via docker stats (best effort; only if container is local).
container_mem_mb() {
  local name
  name=$(docker ps --filter "publish=${BASE##*:}" --format '{{.Names}}' 2>/dev/null | head -1)
  if [ -z "$name" ]; then echo 0; return; fi
  docker stats --no-stream --format '{{.MemUsage}}' "$name" 2>/dev/null | \
    awk '{ v=$1; sub(/[A-Za-z]+$/,"",v); u=$1; sub(/^[0-9.]+/,"",u);
           if (u=="GiB") v*=1024; print int(v) }'
}

peak_rps=0; peak_c=0; knee_c=""
for C in 1 5 10 25 50 100 200 500 1000 2000 5000; do
  # Guard: don't run c=5000 on a tiny host; hey needs fds.
  if [ "$C" -gt 1000 ]; then
    ulimit -n 65536 2>/dev/null || true
  fi

  echo "# c=$C (120s)" >&2
  tmp=$(mktemp)
  hey -z 120s -c "$C" -m POST -H "X-Orva-API-Key: $KEY" -d '{}' "$URL" \
      > "$tmp" 2>&1 || true

  mem=$(container_mem_mb)
  read -r _ pb pi <<<"$(snapshot)"

  csv_line=$(parse_hey "$C" "$mem" "$pb" "$pi" < "$tmp")
  echo "$csv_line"
  rm -f "$tmp"

  # Track peak & knee.
  rps=$(echo "$csv_line" | cut -d, -f4)
  p50=$(echo "$csv_line" | cut -d, -f5)
  p99=$(echo "$csv_line" | cut -d, -f7)
  rps_int=${rps%.*}
  if [ "$rps_int" -gt "$peak_rps" ]; then peak_rps=$rps_int; peak_c=$C; fi
  # Knee: first c where p99 > 3 * p50 (tail blowing out)
  if [ -z "$knee_c" ]; then
    p50_int=${p50%.*}; p99_int=${p99%.*}
    if [ "$p50_int" -gt 0 ] && [ "$p99_int" -gt $((p50_int * 3)) ]; then
      knee_c=$C
    fi
  fi
done

echo "max,${peak_c},0,${peak_rps},0,0,0,0,0,0,0"
echo "knee,${knee_c:-0},0,0,0,0,0,0,0,0,0"
