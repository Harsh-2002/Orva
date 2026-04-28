#!/usr/bin/env bash
# run-all.sh — umbrella for the E.4 verification suite. Runs each test in
# sequence, captures pass/fail per script, prints a summary at the end.
# Does NOT exit on first failure — the operator wants the full picture.

set -uo pipefail   # not -e: we want to keep going

BASE="${BASE_URL:-http://localhost:18443}"
KEY="${API_KEY:?set API_KEY first}"
CONTAINER="${ORVA_CONTAINER:-}"

export BASE_URL="$BASE"
export API_KEY="$KEY"
export ORVA_CONTAINER="$CONTAINER"

DIR="$(cd "$(dirname "$0")" && pwd)"
RESULTS="$DIR/run-all-results.tsv"
> "$RESULTS"

run() {
    local script="$1" out
    echo
    echo "============================================================"
    echo "  $script"
    echo "============================================================"
    if out=$(bash "$DIR/$script" 2>&1); then
        echo "$out"
        line=$(echo "$out" | tail -1)
        echo "$script	pass	$line" >> "$RESULTS"
    else
        echo "$out"
        line=$(echo "$out" | tail -1)
        echo "$script	fail	$line" >> "$RESULTS"
    fi
}

run secrets-test.sh
run routes-test.sh
run heavy-deploy-test.sh
run onboarding-flow.sh
run errors-test.sh
run rollback-test.sh
run atscale.sh

echo
echo "============================================================"
echo "  SUMMARY"
echo "============================================================"
column -ts $'\t' "$RESULTS" || cat "$RESULTS"
