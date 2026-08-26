#!/usr/bin/env bash
# command-tree.sh — golden snapshot of the command tree exposed by the
# slim CLI and the server binary. Both binaries must expose the SAME set
# of client-side subcommands (single source of truth via cli/commands).
#
# Usage:
#   bash test/cli/command-tree.sh
#
# Builds both binaries fresh, walks every subcommand via `--help`, captures
# leaf command paths, and diffs them. Server binary additionally has
# serve/setup — those are filtered out before the diff.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

OUT="$LOGS_DIR/command-tree"
mkdir -p "$OUT"

log "building slim CLI"
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.Version=tree-test' \
    -o "$OUT/orva-cli" "$REPO_ROOT/cli/cmd/orva" || die "slim CLI build failed"

# The server embeds the dashboard with //go:embed all:ui_dist, and that is a
# compile error when the directory is absent. The built UI is not committed, so
# on a fresh checkout it is absent. This test compares Cobra command trees and
# has no interest in the dashboard whatsoever, so it stands up a one-file stub
# rather than making the job install Node and run a Vite build to satisfy an
# embed directive. A real ui_dist, if one is already there, is left alone.
UI_DIST="$REPO_ROOT/backend/internal/server/ui_dist"
UI_STUB=0
if [ ! -e "$UI_DIST/index.html" ]; then
    mkdir -p "$UI_DIST"
    printf '<!doctype html><title>ui not built</title>\n' > "$UI_DIST/index.html"
    UI_STUB=1
fi
cleanup_ui_stub() { [ "$UI_STUB" = 1 ] && rm -rf "$UI_DIST"; return 0; }
trap cleanup_ui_stub EXIT

log "building server binary"
go build -ldflags='-X main.Version=tree-test' \
    -o "$OUT/orva-server" "$REPO_ROOT/backend/cmd/orva" || die "server build failed"

# enumerate_commands <binary> > out
# Walks the Cobra tree by parsing `<bin> --help` recursively. Orva groups
# top-level commands under domain headings (rather than Cobra's default
# "Available Commands:" heading), while nested commands use the default.
# Treat every non-indented heading that ends in ':' as a possible command
# section, then accept only indented command/description rows. Usage/examples
# and flag sections explicitly turn collection off.
enumerate_commands() {
    local bin="$1"
    local prefix="$2"
    # shellcheck disable=SC2034   # used by recursive call args
    local depth="${3:-0}"
    [[ "$depth" -gt 4 ]] && return 0  # safety

    "$bin" $prefix --help 2>/dev/null \
        | awk '
            /^(Usage|Examples|Flags|Global Flags):/ { in_cmds = 0; next }
            /^[^[:space:]].*:$/                    { in_cmds = 1; next }
            in_cmds && /^  [a-zA-Z0-9][^[:space:]]*[[:space:]][[:space:]]/ {
                # First non-whitespace token = command name.
                gsub(/^[[:space:]]+/, "")
                split($0, a, /[[:space:]]+/)
                print a[1]
            }
        ' | while read -r sub; do
            [[ -z "$sub" ]] && continue
            [[ "$sub" == "help" ]] && continue
            local full
            if [[ -z "$prefix" ]]; then
                full="$sub"
            else
                full="$prefix $sub"
            fi
            echo "$full"
            enumerate_commands "$bin" "$full" "$((depth + 1))"
        done
}

enumerate_commands "$OUT/orva-cli"    "" 0 | sort -u > "$OUT/slim.txt"
enumerate_commands "$OUT/orva-server" "" 0 | sort -u > "$OUT/server.txt"

# Fail closed: an empty/partial parser result must never let two equally empty
# snapshots impersonate a passing command-surface comparison.
slim_count=$(wc -l < "$OUT/slim.txt")
server_count=$(wc -l < "$OUT/server.txt")
[[ "$slim_count" -ge 50 ]] || die "enumerated only $slim_count slim CLI commands; help parser is incomplete"
# The server binary adds exactly two top-level commands the slim CLI lacks:
# serve and setup. (`init` was removed -- it wrote an orva.yaml nothing read
# and pointed at a --config flag that does not exist.)
[[ "$server_count" -ge $((slim_count + 2)) ]] || \
    die "enumerated only $server_count server commands for $slim_count client commands"
for required in activity deploy functions invoke login system upgrade; do
    grep -qx "$required" "$OUT/slim.txt" || die "required top-level command missing from snapshot: $required"
done

# Server has additional serve/setup top-level commands; strip them
# before diffing so we compare the client-side surface only.
grep -vE '^(serve|setup)( |$)' "$OUT/server.txt" > "$OUT/server-client-only.txt"

log "slim CLI commands: $slim_count"
log "server CLI commands (server-only filtered): $(wc -l < "$OUT/server-client-only.txt")"

if diff -u "$OUT/slim.txt" "$OUT/server-client-only.txt" > "$OUT/diff.txt"; then
    ok "command trees match — single source of truth verified"
    echo
    echo "=== command-tree: PASS ==="
    exit 0
else
    fail "command trees diverge — see $OUT/diff.txt"
    head -40 "$OUT/diff.txt" >&2
    echo
    echo "=== command-tree: FAIL ==="
    exit 1
fi
