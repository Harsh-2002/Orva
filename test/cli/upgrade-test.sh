#!/usr/bin/env bash
# upgrade-test.sh — install an older orva CLI release, run `orva upgrade`,
# then assert the binary's --version output bumped to the current latest.
#
# Usage:
#   bash test/cli/upgrade-test.sh [old_version] [distro]
#
# old_version defaults to the second-newest release tag fetched from
# GitHub at runtime (so the test stays meaningful without manual pins).
# distro defaults to ubuntu24 (only Linux/macOS via docker — Windows
# self-upgrade is exercised by the cli-e2e CI workflow's windows-2022
# job, not here).

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

REPO="${ORVA_REPO:-Harsh-2002/Orva}"
OLD_VERSION="${1:-}"
DISTRO="${2:-ubuntu24}"

CONTAINER="orva-cli-upgrade-${DISTRO}"

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Resolve old version from GitHub if not pinned: take the 2nd-latest
# tag. If only one release exists (typical for a fresh release where
# the old tag was deleted per the single-active-release policy), there
# IS no previous version — skip the round-trip gracefully.
if [[ -z "$OLD_VERSION" ]]; then
    log "resolving previous release tag from GitHub"
    OLD_VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=2" \
        | grep -E '"tag_name":' | sed -n 's/.*"\([^"]*\)".*/\1/p' \
        | sed -n '2p' || true)
    if [[ -z "$OLD_VERSION" ]]; then
        warn "no previous release to upgrade FROM (only one active release exists)."
        warn "this is expected under Orva's single-active-release policy."
        warn "skipping the round-trip — exits 0 by design."
        echo
        echo "=== upgrade-test [$DISTRO]: skipped (no previous release) ==="
        exit 0
    fi
fi
log "old version under test: $OLD_VERSION"

LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
log "latest version: $LATEST"

if [[ "$OLD_VERSION" == "$LATEST" ]]; then
    warn "old and latest are the same tag ($OLD_VERSION); upgrade is a no-op."
    warn "Pin OLD_VERSION explicitly: bash $0 v2026.04.28"
    exit 0
fi

docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$CONTAINER" ubuntu:24.04 sleep 900 >/dev/null

docker exec "$CONTAINER" sh -c 'apt-get update -qq && apt-get install -y -q curl ca-certificates >/dev/null'

docker cp "$REPO_ROOT/scripts/install-cli.sh" "$CONTAINER:/tmp/install-cli.sh"

PASS=0; FAIL=0

log "installing $OLD_VERSION"
UPG_ENV=(-e "ORVA_VERSION=$OLD_VERSION")
[[ -n "${GITHUB_TOKEN:-}" ]] && UPG_ENV+=(-e "GITHUB_TOKEN=$GITHUB_TOKEN")
[[ -n "${GH_TOKEN:-}" ]] && UPG_ENV+=(-e "GH_TOKEN=$GH_TOKEN")
if ! docker exec "${UPG_ENV[@]}" "$CONTAINER" sh /tmp/install-cli.sh >"$LOGS_DIR/upgrade-install-old.log" 2>&1; then
    fail "old-version install failed — see $LOGS_DIR/upgrade-install-old.log"
    tail -20 "$LOGS_DIR/upgrade-install-old.log" >&2
    exit 1
fi

before=$(docker exec "$CONTAINER" /usr/local/bin/orva --version 2>&1 || echo "")
if [[ "$before" == *"$OLD_VERSION"* ]] || [[ "$before" == *"${OLD_VERSION#v}"* ]]; then
    ok "pre-upgrade version: $before"; PASS=$((PASS+1))
else
    fail "pre-upgrade --version unexpected: $before (wanted $OLD_VERSION)"; FAIL=$((FAIL+1))
fi

log "running orva upgrade"
upgrade_out=$(docker exec "$CONTAINER" /usr/local/bin/orva upgrade 2>&1)
echo "$upgrade_out" > "$LOGS_DIR/upgrade-output.log"

if [[ "$upgrade_out" == *"Upgrading"* ]] || [[ "$upgrade_out" == *"already the latest"* ]] || [[ "$upgrade_out" == *"Upgraded to"* ]]; then
    ok "orva upgrade ran (output captured in $LOGS_DIR/upgrade-output.log)"
    PASS=$((PASS+1))
else
    fail "orva upgrade produced unexpected output:"
    echo "$upgrade_out" >&2
    FAIL=$((FAIL+1))
fi

after=$(docker exec "$CONTAINER" /usr/local/bin/orva --version 2>&1 || echo "")
if [[ "$after" == *"$LATEST"* ]] || [[ "$after" == *"${LATEST#v}"* ]]; then
    ok "post-upgrade version matches latest: $after"; PASS=$((PASS+1))
else
    fail "post-upgrade --version unexpected: $after (wanted $LATEST)"; FAIL=$((FAIL+1))
fi

# `orva upgrade --check` on a now-latest binary should report up-to-date.
check_out=$(docker exec "$CONTAINER" /usr/local/bin/orva upgrade --check 2>&1 || echo "")
if [[ "$check_out" == *"up to date"* ]] || [[ "$check_out" == *"already the latest"* ]]; then
    ok "post-upgrade --check reports up-to-date"; PASS=$((PASS+1))
else
    warn "--check post-upgrade did not say 'up to date': $check_out"
fi

echo
echo "=== upgrade-test [$DISTRO]: $PASS passed, $FAIL failed ==="
[[ "$FAIL" -eq 0 ]]
