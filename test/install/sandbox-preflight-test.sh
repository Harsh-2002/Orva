#!/usr/bin/env bash
# Unit tests for install.sh's sandbox selection and repair paths. These do not
# need a privileged host or nsjail: side effects are replaced with deterministic
# fakes so CI covers failure recovery independently of runner kernel policy.
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
  local scenario="$1" override="${2-}" out rc capfile caps
  capfile=$(mktemp)
  set +e
  if [[ $# -eq 2 ]]; then
    out=$(ORVA_INSTALL_LIB=1 ORVA_DISABLE_USERNS="$override" SCENARIO="$scenario" CAPFILE="$capfile" \
      sh -c '
        . "$1"
        CAPS=""
        flush_caps() { printf "%s" "$CAPS" > "$CAPFILE"; }
        trap flush_caps EXIT
        clear_nsjail_caps() { CAPS="${CAPS}clear "; }
        apply_nsjail_caps() { CAPS="${CAPS}apply "; }
        probe_sandbox_mode() {
          CAPS="${CAPS}probe$1 "
          case "$SCENARIO:$1" in
            pass,*:0|*,pass:1) return 0 ;;
            *) return 1 ;;
          esac
        }
        select_sandbox_mode
        printf "mode=%s explicit=%s\n" "$SERVICE_DISABLE_USERNS" "$SERVICE_DISABLE_USERNS_EXPLICIT"
      ' sh "$INSTALLER" 2>&1)
  else
    out=$(ORVA_INSTALL_LIB=1 SCENARIO="$scenario" CAPFILE="$capfile" \
      sh -c '
        . "$1"
        CAPS=""
        flush_caps() { printf "%s" "$CAPS" > "$CAPFILE"; }
        trap flush_caps EXIT
        clear_nsjail_caps() { CAPS="${CAPS}clear "; }
        apply_nsjail_caps() { CAPS="${CAPS}apply "; }
        probe_sandbox_mode() {
          CAPS="${CAPS}probe$1 "
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
  caps=$(cat "$capfile")
  rm -f "$capfile"
  printf '%s\ncaps=%s\n' "$rc:$out" "$caps"
}

out=$(selector 'pass,pass')
if [[ "$out" == *'0:'* && "$out" == *'mode=0 explicit=0'* && "$out" == *'caps=clear probe0 '* ]]; then
  ok 'automatic selector keeps verified user namespaces and leaves nsjail capability-free'
else
  bad "automatic normal success: $out"
fi

out=$(selector 'fail,pass')
if [[ "$out" == *'0:'* && "$out" == *'mode=1 explicit=0'* && "$out" == *'file-capability fallback'* && "$out" == *'caps=clear probe0 apply probe1 '* ]]; then
  ok 'automatic selector probes without capabilities, then applies them only for the verified fallback'
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
if [[ "$out" == 1:* && "$out" == *'requested ORVA_DISABLE_USERNS=1 cannot run nsjail'* && "$out" == *'caps=clear apply probe1 '* ]]; then
  ok 'explicit fallback mode applies capabilities before probing and fails closed when not viable'
else
  bad "explicit fallback failure: $out"
fi

out=$(selector 'fail,fail')
if [[ "$out" == 1:* && "$out" == *'refusing to install a server that cannot invoke functions'* && "$out" == *'caps=clear probe0 apply probe1 '* ]]; then
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

# A failed first install can leave the same-version binary behind but no unit.
# Non-interactive retry must run the whole repair path rather than falsely
# succeeding with "already installed". This is the exact CI-matrix regression.
out=$(ORVA_INSTALL_LIB=1 sh -c '
  . "$1"
  EXISTING_KIND=bare
  EXISTING_VERSION=v2099.01.01
  VERSION=v2099.01.01
  INTERACTIVE=0
  DRYRUN=0
  calls=""
  record() { calls="${calls}$1 "; }
  install_prereqs() { record prereqs; }
  check_kernel_features() { record kernel; }
  download_and_install_binaries() { record binaries; }
  create_user() { record user; }
  check_egress_device() { record egress; }
  download_rootfs() { record rootfs; }
  install_adapters() { record adapters; }
  select_sandbox_mode() { SERVICE_DISABLE_USERNS=1; record sandbox; }
  write_service_files() { record files; }
  install_unit() { record unit; }
  restart_if_running() { record restart; }
  start_service() { record start; }
  install_cli_shortcut() { record cli; }
  print_followup_bare() { record followup; }
  run_bare_metal
  printf "calls=%s\n" "$calls"
' sh "$INSTALLER" 2>&1)
if [[ "$out" == *'running non-interactive repair'* && "$out" == *'calls=prereqs kernel binaries user egress rootfs adapters sandbox files unit restart cli followup '* ]]; then
  ok 'same-version non-interactive retry repairs a partial installation'
else
  bad "non-interactive repair: $out"
fi

out=$(ORVA_INSTALL_LIB=1 sh -c '
  . "$1"
  EXISTING_KIND=bare
  EXISTING_VERSION=v2099.01.01
  VERSION=v2099.01.01
  INTERACTIVE=1
  ask_yn() { return 1; }
  install_prereqs() { echo unexpected-side-effect; return 1; }
  run_bare_metal
' sh "$INSTALLER" 2>&1)
if [[ "$out" == *'nothing to do (already at v2099.01.01)'* && "$out" != *'unexpected-side-effect'* ]]; then
  ok 'interactive same-version install still honors repair refusal'
else
  bad "interactive repair refusal: $out"
fi

# `orva upgrade` is a separate process from the service and cannot see the
# unit's Environment=ORVA_DATA_DIR, so install.sh records the chosen path.
recdir=$(mktemp -d); record="$recdir/install.conf"
ORVA_INSTALL_LIB=1 ORVA_INSTALL_RECORD="$record" sh -c '
  . "$1"
  DATA_DIR=/srv/orva
  DRYRUN=0
  write_install_record
' sh "$INSTALLER" >/dev/null 2>&1
if [ -f "$record" ] && grep -qx 'DATA_DIR=/srv/orva' "$record"; then
  ok 'installer records the data directory for orva upgrade'
else
  bad "install record missing or wrong: $(cat "$record" 2>/dev/null || echo missing)"
fi
rm -rf "$recdir"

recdir=$(mktemp -d); record="$recdir/install.conf"
ORVA_INSTALL_LIB=1 ORVA_INSTALL_RECORD="$record" sh -c '
  . "$1"
  DATA_DIR=/srv/orva
  DRYRUN=1
  write_install_record
' sh "$INSTALLER" >/dev/null 2>&1
if [ ! -e "$record" ]; then
  ok 'dry-run does not write the install record'
else
  bad 'dry-run wrote the install record'
fi
rm -rf "$recdir"

# The uninstaller removes the record and tidies the directory that held it,
# including when ORVA_INSTALL_RECORD pointed somewhere other than /etc/orva.
recdir=$(mktemp -d)
mkdir -p "$recdir/prefix/share/orva/scripts" "$recdir/custom"
printf 'DATA_DIR=/srv/orva\n' > "$recdir/custom/install.conf"
ORVA_INSTALL_LIB=1 ORVA_INSTALL_RECORD="$recdir/custom/install.conf" sh -c '
  . "$1"
  PREFIX="$2"; DATA_DIR=/srv/orva; SERVICE_USER=orva; CLI_INSTALL_PATH=/usr/local/bin/orva
  write_uninstall_script
' sh "$INSTALLER" "$recdir/prefix" >/dev/null 2>&1
uninstaller="$recdir/prefix/share/orva/scripts/uninstall.sh"
if grep -qxF "rm -f \"$recdir/custom/install.conf\"" "$uninstaller" &&
   grep -qxF "rmdir \"$recdir/custom\" 2>/dev/null || true" "$uninstaller" &&
   ! grep -q 'rmdir /etc/orva' "$uninstaller"; then
  ok 'uninstaller tidies the directory of a custom install record path'
else
  bad "uninstaller record cleanup wrong: $(grep -n 'install.conf\|rmdir' "$uninstaller" 2>/dev/null)"
fi
rm -rf "$recdir"

printf 'passed=%d failed=%d\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
