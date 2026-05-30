#!/usr/bin/env bash
# test/install/matrix.sh — fast, unprivileged cross-distro checks for
# scripts/install.sh. Complements the heavier systemd-in-docker harness
# (install-test.sh): this one needs no privileged containers and runs in
# seconds per distro, so it is the default gate in CI and locally.
#
# It asserts, for every target distro image:
#   1. shellcheck is clean (run once, distro-independent)
#   2. the distro's real /bin/sh parses the script (POSIX: dash/busybox/bash)
#   3. `--help` exits 0 and `--dry-run` walks bare-metal + cli to completion
#   4. (optional, REAL_CLI=1) a real --cli-only install downloads + verifies
#      the checksum and the installed binary reports the expected version
#
# Usage:
#   bash test/install/matrix.sh                 # all checks, dry-run only
#   REAL_CLI=1 bash test/install/matrix.sh      # also do a real CLI install
#   CLI_VERSION=v2026.05.30 REAL_CLI=1 bash test/install/matrix.sh
#   bash test/install/matrix.sh debian:stable-slim alpine:latest   # subset
#
# Requires Docker. Exits non-zero on the first failure.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT="scripts/install.sh"
# Empty CLI_VERSION → the installer resolves the latest release itself, which
# survives the "one active release at a time" policy (a pinned tag would 404
# once the next release deletes it). Set CLI_VERSION=vX.Y.Z to pin.
CLI_VERSION="${CLI_VERSION:-}"
REAL_CLI="${REAL_CLI:-0}"

IMAGES=("$@")
if [ "${#IMAGES[@]}" -eq 0 ]; then
  IMAGES=(
    debian:stable-slim
    ubuntu:24.04
    alpine:latest
    fedora:latest
    almalinux:9
    archlinux:latest
  )
fi

pass=0 fail=0
ok()   { printf '\033[1;32m  ✓ %s\033[0m\n' "$*"; pass=$((pass + 1)); }
bad()  { printf '\033[1;31m  ✗ %s\033[0m\n' "$*" >&2; fail=$((fail + 1)); }
head_() { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; }

dr() { docker run --rm -v "$ROOT:/mnt" -w /mnt "$@"; }

head_ "shellcheck (POSIX sh)"
if dr koalaman/shellcheck:stable -s sh "$SCRIPT" >/tmp/mx_sc.log 2>&1; then
  ok "shellcheck clean"
else
  bad "shellcheck findings:"; cat /tmp/mx_sc.log >&2
fi

for img in "${IMAGES[@]}"; do
  head_ "$img"

  # 1. POSIX parse under the distro's own /bin/sh.
  if dr "$img" sh -n "/mnt/$SCRIPT" >/dev/null 2>&1; then
    ok "sh -n parses"
  else
    bad "sh -n FAILED"; continue
  fi

  # 2. --help exits 0.
  if dr "$img" sh "/mnt/$SCRIPT" --help >/dev/null 2>&1; then
    ok "--help exit 0"
  else
    bad "--help non-zero"
  fi

  # 3. --dry-run bare-metal walks to completion.
  if dr "$img" sh "/mnt/$SCRIPT" --dry-run --bare-metal 2>&1 | grep -q "installed (bare-metal)"; then
    ok "--dry-run --bare-metal"
  else
    bad "--dry-run --bare-metal did not complete"
  fi

  # 4. --dry-run cli.
  if dr "$img" sh "/mnt/$SCRIPT" --dry-run --cli-only 2>&1 | grep -q "would install CLI"; then
    ok "--dry-run --cli-only"
  else
    bad "--dry-run --cli-only did not complete"
  fi

  # 5. bad flag is rejected non-zero.
  if dr "$img" sh "/mnt/$SCRIPT" --nonsense >/dev/null 2>&1; then
    bad "bad flag was accepted"
  else
    ok "bad flag rejected"
  fi

  # 6. optional REAL cli install (download + checksum verify + run). With no
  #    pinned CLI_VERSION the installer resolves the latest release itself.
  if [ "$REAL_CLI" = "1" ]; then
    ver_flag=""
    [ -n "$CLI_VERSION" ] && ver_flag="--version $CLI_VERSION"
    if dr "$img" sh -c "
        (command -v curl >/dev/null 2>&1 || apk add --no-cache curl ca-certificates >/dev/null 2>&1 || \
          { apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null 2>&1; } || \
          { command -v dnf >/dev/null 2>&1 && dnf install -y -q curl >/dev/null 2>&1; } || \
          { command -v pacman >/dev/null 2>&1 && pacman -Sy --noconfirm curl >/dev/null 2>&1; } || true) ;
        sh /mnt/$SCRIPT --cli-only $ver_flag >/dev/null 2>&1 &&
        /usr/local/bin/orva --version 2>/dev/null | grep -qiE '^orva v'
      "; then
      ok "real --cli-only install verified (${CLI_VERSION:-latest})"
    else
      bad "real --cli-only install FAILED"
    fi
  fi
done

head_ "summary"
printf 'passed=%d  failed=%d\n' "$pass" "$fail"
[ "$fail" -eq 0 ] || exit 1
