#!/usr/bin/env bash
# install-cli-test.sh — exercise scripts/install-cli.sh inside a privileged
# Docker container so we catch download/checksum/install regressions
# before they ship.
#
# Usage:
#   bash test/cli/install-cli-test.sh [distro]
#   default distro: ubuntu24
#
# Uses the existing systemd-in-docker harness helpers from
# test/install/lib/common.sh; falls back to a plain ubuntu:24.04 if that
# directory isn't checked out.
#
# This script tests the LOCAL working copy of install-cli.sh against the
# latest published release on GitHub. To pin a version, set ORVA_VERSION.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

DISTRO="${1:-ubuntu24}"
KEEP="${ORVA_KEEP_CONTAINER:-0}"
TEST_VERSION="${ORVA_VERSION:-}"
INSTALLER_PATH="${ORVA_CLI_INSTALLER_PATH:-$REPO_ROOT/scripts/install-cli.sh}"
[[ -s "$INSTALLER_PATH" ]] || die "CLI installer missing or empty: $INSTALLER_PATH"

# Pick a base image. If test/install/distros.tsv exists, use it; else
# default to ubuntu:24.04 (covers 95% of the value).
INSTALL_TSV="$REPO_ROOT/test/install/distros.tsv"
IMAGE="${ORVA_CLI_TEST_IMAGE:-}"
if [[ -z "$IMAGE" && -f "$INSTALL_TSV" ]]; then
    row=$(grep -E "^${DISTRO}\b" "$INSTALL_TSV" | head -n1)
    if [[ -n "$row" ]]; then
        IMAGE=$(awk -F '\t' '{print $2}' <<< "$row")
    fi
fi
IMAGE="${IMAGE:-ubuntu:24.04}"

CONTAINER="orva-cli-test-${DISTRO}"
log "test container: $CONTAINER ($IMAGE)"

cleanup() {
    [[ "$KEEP" == "1" ]] && return 0
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker rm -f "$CONTAINER" >/dev/null 2>&1 || true

# A plain container suffices — install-cli.sh doesn't need systemd. We
# do need bash + curl + ca-certificates installed.
docker run -d --name "$CONTAINER" "$IMAGE" sleep 600 >/dev/null \
    || die "container start failed"

log "installing test prerequisites inside container"
case "$IMAGE" in
    *alpine*)
        docker exec "$CONTAINER" sh -c 'apk add --no-cache curl ca-certificates bash >/dev/null'
        ;;
    *)
        docker exec "$CONTAINER" sh -c 'apt-get update -qq && apt-get install -y -q curl ca-certificates >/dev/null'
        ;;
esac

# PR/main validation passes the local working copy. Released-artifact E2E sets
# ORVA_CLI_INSTALLER_PATH to the downloaded, checksum-verified release asset.
log "copying CLI installer into $CONTAINER: $INSTALLER_PATH"
docker cp "$INSTALLER_PATH" "$CONTAINER:/tmp/install-cli.sh"

INSTALL_LOG="$LOGS_DIR/cli-install-${DISTRO}.log"
log "running install-cli.sh inside $CONTAINER (log → $INSTALL_LOG)"

ENV_FLAGS=()
[[ -n "$TEST_VERSION" ]] && ENV_FLAGS+=(-e "ORVA_VERSION=$TEST_VERSION")
# Forward a GITHUB_TOKEN if the harness is running in CI so install-cli.sh's
# api.github.com call doesn't hit the 60/hr unauthenticated rate limit.
[[ -n "${GITHUB_TOKEN:-}" ]] && ENV_FLAGS+=(-e "GITHUB_TOKEN=$GITHUB_TOKEN")
[[ -n "${GH_TOKEN:-}" ]] && ENV_FLAGS+=(-e "GH_TOKEN=$GH_TOKEN")

PASS=0; FAIL=0

if docker exec "${ENV_FLAGS[@]}" "$CONTAINER" sh /tmp/install-cli.sh >"$INSTALL_LOG" 2>&1; then
    ok "install-cli.sh exited 0"; PASS=$((PASS+1))
else
    fail "install-cli.sh exited non-zero — see $INSTALL_LOG"
    tail -30 "$INSTALL_LOG" >&2
    FAIL=$((FAIL+1))
fi

# Confirm /usr/local/bin/orva exists and is executable.
if docker exec "$CONTAINER" test -x /usr/local/bin/orva; then
    ok "/usr/local/bin/orva is executable"; PASS=$((PASS+1))
else
    fail "/usr/local/bin/orva missing or not executable"; FAIL=$((FAIL+1))
fi

# Size sanity: keep this contract aligned with build-matrix.sh. Dependency and
# Go toolchain updates can move the stripped binary slightly; 28 MiB still
# catches accidentally shipping the much larger server binary.
size=$(docker exec "$CONTAINER" stat -c '%s' /usr/local/bin/orva 2>/dev/null || echo 0)
if [[ "$size" -gt 0 ]] && [[ "$size" -le "$CLI_SIZE_LIMIT_BYTES" ]]; then
    ok "binary size: $((size / 1024 / 1024)) MB (≤ $CLI_SIZE_LIMIT_MB MB)"; PASS=$((PASS+1))
else
    fail "binary size out of expected range: $((size / 1024 / 1024)) MB (limit: $CLI_SIZE_LIMIT_MB MB)"; FAIL=$((FAIL+1))
fi

# --version should print a release tag (vYYYY.MM.DD).
version_out=$(docker exec "$CONTAINER" /usr/local/bin/orva --version 2>&1 || echo "")
if [[ "$version_out" =~ orva\ v?[0-9]+ ]]; then
    ok "orva --version: $version_out"; PASS=$((PASS+1))
else
    fail "orva --version did not return expected format: $version_out"; FAIL=$((FAIL+1))
fi

# The slim CLI must NOT have `serve` or `setup` (server-only).
# Capture into a variable first — `set -o pipefail` (set above) would
# make the entire pipeline inherit `orva …`'s non-zero exit code (it
# returns 1 when the subcommand is unknown), which would mask the
# grep's success and incorrectly trigger the else branch.
for forbidden in serve setup; do
    out=$(docker exec "$CONTAINER" /usr/local/bin/orva "$forbidden" --help 2>&1 || true)
    if echo "$out" | grep -q 'unknown command'; then
        ok "slim CLI correctly lacks 'orva $forbidden'"; PASS=$((PASS+1))
    else
        fail "slim CLI unexpectedly has 'orva $forbidden' subcommand"; FAIL=$((FAIL+1))
    fi
done

# Core subcommands must be present.
for required in functions deploy invoke logs kv secrets keys upgrade completion; do
    if docker exec "$CONTAINER" /usr/local/bin/orva "$required" --help >/dev/null 2>&1; then
        ok "slim CLI has 'orva $required'"; PASS=$((PASS+1))
    else
        fail "slim CLI missing 'orva $required'"; FAIL=$((FAIL+1))
    fi
done

# Completion script generation works for all 4 shells.
for shell in bash zsh fish powershell; do
    if docker exec "$CONTAINER" /usr/local/bin/orva completion "$shell" >/dev/null 2>&1; then
        ok "completion $shell generates"; PASS=$((PASS+1))
    else
        fail "completion $shell failed"; FAIL=$((FAIL+1))
    fi
done

echo
echo "=== install-cli-test [$DISTRO]: $PASS passed, $FAIL failed ==="
[[ "$FAIL" -eq 0 ]]
