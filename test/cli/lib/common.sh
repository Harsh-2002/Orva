#!/usr/bin/env bash
# Shared helpers for the CLI-specific test scripts in test/cli/.
# Sourced by build-matrix.sh, install-cli-test.sh, upgrade-test.sh, etc.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC2034  # consumed by consumer scripts after source
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
LOGS_DIR="$HERE/logs"

mkdir -p "$LOGS_DIR"

c_cyan='\033[1;36m'; c_green='\033[1;32m'; c_yellow='\033[1;33m'
c_red='\033[1;31m'; c_dim='\033[0;90m'; c_reset='\033[0m'

log()  { printf "${c_cyan}==>${c_reset} %s\n" "$*"; }
ok()   { printf "${c_green}✓${c_reset} %s\n" "$*"; }
warn() { printf "${c_yellow}!!!${c_reset} %s\n" "$*" >&2; }
fail() { printf "${c_red}✗${c_reset} %s\n" "$*" >&2; }
die()  { fail "$*"; exit 1; }
dim()  { printf "${c_dim}%s${c_reset}\n" "$*"; }

# Six release targets the CLI ships on.
# shellcheck disable=SC2034  # consumed by consumer scripts after source
CLI_TARGETS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
    "windows/arm64"
)
