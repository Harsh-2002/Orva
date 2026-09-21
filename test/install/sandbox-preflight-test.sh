#!/usr/bin/env bash
# Unit tests for install.sh's sandbox mode selector. These do not need a
# privileged host or nsjail: probe_sandbox_mode is replaced with a deterministic
# fake, which lets CI cover the exact "normal mode fails, fallback works"
# regression independently of the runner's kernel policy.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
INSTALLER="$ROOT/scripts/install.sh"

pass=0
fail=0
ok() { printf 'ok - %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf 'not ok - %s\n' "$1" >&2; fail=$((fail + 1)); }

# selector SCENARIO [ORVA_DISABLE_USERNS]
# Scenario syntax is '<normal result>,<fallback result>', each pass|fail.
selector() {
  local scenario="$1" override="${2-}" out rc
  set +e
  if [[ $# -eq 2 ]]; then
    out=$(ORVA_INSTALL_LIB=1 ORVA_DISABLE_USERNS="$override" SCENARIO="$scenario" \
      sh -c '
        . "$1"
        probe_sandbox_mode() {
          case "$SCENARIO:$1" in
            pass,*:0|*,pass:1) return 0 ;;
            *) return 1 ;;
          esac
        }
        select_sandbox_mode
        printf "mode=%s explicit=%s\n" "$SERVICE_DISABLE_USERNS" "$SERVICE_DISABLE_USERNS_EXPLICIT"
      ' sh "$INSTALLER" 2>&1)
  else
    out=$(ORVA_INSTALL_LIB=1 SCENARIO="$scenario" \
      sh -c '
        . "$1"
        probe_sandbox_mode() {
          case "$SCENARIO:$1" in
            pass,*:0|*,pass:1) return 0 ;;
            *) return 1 ;;
          esac
        }
        select_sandbox_mode
        printf "mode=%s explicit=%s\n" "$SERVICE_DISABLE_USERNS" "$SERVICE_DISABLE_USERNS_EXPLICIT"
      ' sh "$INSTALLER" 2>&1)
  fi
  rc=$?
  set -e
  printf '%s\n' "$rc:$out"
}

out=$(selector 'pass,pass')
if [[ "$out" == *'0:'* && "$out" == *'mode=0 explicit=0'* ]]; then
  ok 'automatic selector keeps verified user namespaces'
else
  bad "automatic normal success: $out"
fi

out=$(selector 'fail,pass')
if [[ "$out" == *'0:'* && "$out" == *'mode=1 explicit=0'* && "$out" == *'file-capability fallback'* ]]; then
  ok 'automatic selector repairs blocked user namespaces with verified fallback'
else
  bad "automatic fallback: $out"
fi

out=$(selector 'fail,pass' 0)
if [[ "$out" == 1:* && "$out" == *'requested ORVA_DISABLE_USERNS=0 cannot run nsjail'* ]]; then
  ok 'explicit normal mode fails closed instead of silently changing policy'
else
  bad "explicit normal failure: $out"
fi

out=$(selector 'pass,fail' 1)
if [[ "$out" == 1:* && "$out" == *'requested ORVA_DISABLE_USERNS=1 cannot run nsjail'* ]]; then
  ok 'explicit fallback mode fails closed when it is not viable'
else
  bad "explicit fallback failure: $out"
fi

out=$(selector 'fail,fail')
if [[ "$out" == 1:* && "$out" == *'refusing to install a server that cannot invoke functions'* ]]; then
  ok 'neither viable mode aborts the install before service startup'
else
  bad "both modes failure: $out"
fi

out=$(selector 'pass,pass' nonsense)
if [[ "$out" == 1:* && "$out" == *'ORVA_DISABLE_USERNS must be exactly 0 or 1'* ]]; then
  ok 'invalid explicit mode is rejected'
else
  bad "invalid explicit mode: $out"
fi

printf 'passed=%d failed=%d\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
