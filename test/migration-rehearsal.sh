#!/usr/bin/env bash
# migration-rehearsal.sh — upgrade a legacy data dir with the REAL binary.
#
# The UUIDv7 migration rewrites every functions.id, and each function's code
# lives at <dataDir>/functions/<id>/. If the ids move and the directories do
# not, every function fails to spawn and the build GC then removes the trees
# as orphans — minutes after a boot that reported success. That failure mode
# is invisible to unit tests, which is why this exists.
#
# What it does:
#   1. Boots orvad once against a scratch dir, so the schema under test is the
#      one the shipped binary produces.
#   2. Rewinds that database to a pre-migration state and seeds legacy ids,
#      a channel binding, soft references, and function code on disk.
#   3. Boots orvad again. The migration runs on this boot.
#   4. Verifies every id moved, every directory followed, and every byte of
#      handler source survived.
#
# Needs neither nsjail nor Docker: nothing is invoked, only migrated.
#
#   bash test/migration-rehearsal.sh
#   ORVA_REHEARSAL_KEEP=1 bash test/migration-rehearsal.sh   # keep the dir

set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${ORVA_REHEARSAL_PORT:-18499}"
DATA="${ORVA_REHEARSAL_DATA:-$(mktemp -d /tmp/orva-rehearsal-XXXXXX)}"
BIN="build/orva"
LOG="$DATA/orvad.log"

blue()  { printf '\033[1;36m==>\033[0m %s\n' "$1"; }
green() { printf '\033[1;32m✓\033[0m %s\n' "$1"; }
red()   { printf '\033[1;31m✗\033[0m %s\n' "$1"; }

cleanup() {
    if [ -n "${SERVER_PID:-}" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    if [ "${ORVA_REHEARSAL_KEEP:-0}" = "1" ]; then
        echo "kept: $DATA"
    else
        rm -rf "$DATA"
    fi
}
trap cleanup EXIT

# boot_orvad starts the server and waits for it to answer, or dies with the
# log. $1 is a label for the message.
boot_orvad() {
    local label="$1"
    blue "booting orvad ($label)"
    ORVA_DATA_DIR="$DATA" ORVA_PORT="$PORT" "$BIN" serve >>"$LOG" 2>&1 &
    SERVER_PID=$!

    local waited=0
    until curl -sf "http://127.0.0.1:$PORT/api/v1/system/health" >/dev/null 2>&1; do
        if ! kill -0 "$SERVER_PID" 2>/dev/null; then
            red "orvad exited during boot ($label)"
            echo "--- last 40 log lines ---"
            tail -40 "$LOG"
            exit 1
        fi
        sleep 0.25
        waited=$((waited + 1))
        if [ "$waited" -gt 120 ]; then
            red "orvad did not become healthy within 30s ($label)"
            tail -40 "$LOG"
            exit 1
        fi
    done
    green "orvad healthy ($label)"
}

stop_orvad() {
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
    green "orvad stopped"
}

blue "building server binary"
make build >/dev/null

# ── 1. Lay down the schema with the real binary ──────────────────────
boot_orvad "schema"
stop_orvad

# ── 2. Rewind to a pre-migration state ───────────────────────────────
blue "seeding a pre-UUIDv7 fixture"
go run ./test/migration-rehearsal -data "$DATA"

# Record what is on disk BEFORE, so the comparison is against observed
# reality rather than against what the seeder believes it wrote.
before_tree=$(find "$DATA/functions" -type f | sort | sed "s|$DATA/||")
before_count=$(printf '%s\n' "$before_tree" | grep -c . || true)
blue "on disk before migration: $before_count file(s) under functions/"

# ── 3. Boot again; the migration runs here ───────────────────────────
boot_orvad "migration"

# Give the build GC a chance to run against the migrated dir. Its interval
# floors at 30s, so this is the window in which a broken migration would
# delete the operator's source.
GC_WAIT="${ORVA_REHEARSAL_GC_WAIT:-35}"
blue "waiting ${GC_WAIT}s for a GC tick (this is when a broken migration eats the code)"
sleep "$GC_WAIT"

stop_orvad

# ── 4. Verify ────────────────────────────────────────────────────────
blue "verifying"
if ! go run ./test/migration-rehearsal -data "$DATA" -verify; then
    red "verification FAILED"
    # Only the migration lines. A scratch data dir has no runtime rootfs, so
    # the log is dominated by "pool worker spawn failed" warnings that have
    # nothing to do with this and would bury the actual failure above.
    echo "--- migration/reconcile lines from the orvad log ---"
    grep -E "uuidv7|reconcil|migration" "$LOG" | tail -20 || true
    echo "--- (full log: $LOG; re-run with ORVA_REHEARSAL_KEEP=1 to inspect) ---"
    exit 1
fi

after_count=$(find "$DATA/functions" -type f | wc -l)
if [ "$after_count" -ne "$before_count" ]; then
    red "file count under functions/ changed: $before_count -> $after_count"
    find "$DATA/functions" -type f | sed "s|$DATA/||" | sort
    exit 1
fi
green "file count under functions/ unchanged ($after_count)"

# The migration must not re-run once its marker is set.
#
# Counting total runs across the whole log would be wrong: boot 1 legitimately
# migrates a brand-new database, and boot 2 migrates because the seeder
# deliberately removed the marker. What matters is that a boot finding the
# marker already set does nothing. So snapshot the count, boot again, and
# assert it did not move.
starts_before=$(grep -c "uuidv7 migration: starting" "$LOG" || true)
boot_orvad "idempotence"
stop_orvad
starts_after=$(grep -c "uuidv7 migration: starting" "$LOG" || true)
if [ "$starts_after" -ne "$starts_before" ]; then
    red "migration re-ran on a boot whose marker was already set ($starts_before -> $starts_after)"
    exit 1
fi
green "migration did not re-run once its marker was set"

green "REHEARSAL PASSED"
