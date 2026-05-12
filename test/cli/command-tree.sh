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
# serve/setup/init — those are filtered out before the diff.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

OUT="$LOGS_DIR/command-tree"
mkdir -p "$OUT"

log "building slim CLI"
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.Version=tree-test' \
    -o "$OUT/orva-cli" "$REPO_ROOT/cli/cmd/orva" || die "slim CLI build failed"

log "building server binary"
go build -ldflags='-X main.Version=tree-test' \
    -o "$OUT/orva-server" "$REPO_ROOT/backend/cmd/orva" || die "server build failed"

# enumerate_commands <binary> > out
# Walks the cobra tree by parsing `<bin> --help` recursively. Cobra's
# default help template lists subcommands as "  <name>   <desc>" lines
# under an "Available Commands:" header.
enumerate_commands() {
    local bin="$1"
    local prefix="$2"
    # shellcheck disable=SC2034   # used by recursive call args
    local depth="${3:-0}"
    [[ "$depth" -gt 4 ]] && return 0  # safety

    "$bin" $prefix --help 2>/dev/null \
        | awk '
            /^Available Commands:/ { in_cmds = 1; next }
            /^Flags:/              { in_cmds = 0 }
            /^Global Flags:/       { in_cmds = 0 }
            in_cmds && /^[[:space:]]+[a-zA-Z]/ {
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

# Server has additional serve/setup/init top-level commands; strip them
# before diffing so we compare the client-side surface only.
grep -vE '^(serve|setup|init)( |$)' "$OUT/server.txt" > "$OUT/server-client-only.txt"

log "slim CLI commands: $(wc -l < "$OUT/slim.txt")"
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
