#!/usr/bin/env bash
# Shared helpers for the install-harness scripts in test/install/.
# Sourced by run-distro.sh, smoke-flow.sh, gvisor-flow.sh, etc.
# Bash-only (uses arrays, [[ ]] guards) — not POSIX sh.

set -uo pipefail

# Resolved once per source.
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC2034  # consumed by consumer scripts after source
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
LOGS_DIR="$HERE/logs"
DISTROS_TSV="$HERE/distros.tsv"

mkdir -p "$LOGS_DIR"

c_cyan='\033[1;36m'; c_green='\033[1;32m'; c_yellow='\033[1;33m'
c_red='\033[1;31m'; c_dim='\033[0;90m'; c_reset='\033[0m'

log()  { printf "${c_cyan}==>${c_reset} %s\n" "$*"; }
ok()   { printf "${c_green}✓${c_reset} %s\n" "$*"; }
warn() { printf "${c_yellow}!!!${c_reset} %s\n" "$*" >&2; }
fail() { printf "${c_red}✗${c_reset} %s\n" "$*" >&2; }
die()  { fail "$*"; exit 1; }
dim()  { printf "${c_dim}%s${c_reset}\n" "$*"; }

# Lookup distro row from distros.tsv. Sets DISTRO_IMAGE + DISTRO_INIT +
# DISTRO_INIT_CMD. init_cmd defaults to /sbin/init when omitted.
lookup_distro() {
    local distro="$1"
    local row image init init_cmd
    row=$(grep -E "^${distro}\b" "$DISTROS_TSV" | head -n1) \
        || die "distro '$distro' not found in $DISTROS_TSV"
    image=$(awk -F '\t' '{print $2}' <<< "$row")
    init=$(awk -F '\t' '{print $3}' <<< "$row")
    init_cmd=$(awk -F '\t' '{print $4}' <<< "$row")
    [[ -n "$image" && -n "$init" ]] || die "malformed row for '$distro': $row"
    [[ -z "$init_cmd" || "$init_cmd" == "-" ]] && init_cmd="/sbin/init"
    DISTRO_IMAGE="$image"
    DISTRO_INIT="$init"
    DISTRO_INIT_CMD="$init_cmd"
}

# Spin up a privileged container running PID 1 init for the given distro.
# Stdin output: container ID. Stderr: status messages.
start_distro_container() {
    local distro="$1" port="$2"
    lookup_distro "$distro"
    local name="orva-test-${distro}"

    docker rm -f "$name" >/dev/null 2>&1 || true

    log "starting $distro container ($DISTRO_IMAGE, init=$DISTRO_INIT, port=$port)"

    local args=(
        --privileged
        --name "$name"
        --hostname "$distro"
        -p "${port}:8443"
        --tmpfs /run --tmpfs /run/lock
        -v /sys/fs/cgroup:/sys/fs/cgroup:rw
        --cgroupns=host
    )

    if [[ "$DISTRO_INIT" == "systemd" ]]; then
        # systemd-enabled images: init binary path varies. jrei/* and rocky
        # images ship /sbin/init; lopsided/archlinux ships systemd at
        # /lib/systemd/systemd only. Per-distro DISTRO_INIT_CMD picks the
        # right one.
        docker run -d "${args[@]}" "$DISTRO_IMAGE" "$DISTRO_INIT_CMD" >/dev/null \
            || die "container start failed for $distro"
    else
        # Alpine: start with `tail -f /dev/null` so the container stays alive,
        # then bring up OpenRC manually inside it (Alpine's `openrc default`).
        docker run -d "${args[@]}" "$DISTRO_IMAGE" sh -c 'tail -f /dev/null' >/dev/null \
            || die "container start failed for $distro"
        docker exec "$name" sh -c 'apk add --no-cache openrc >/dev/null 2>&1 && rc-status >/dev/null 2>&1; openrc default >/dev/null 2>&1 || true'
    fi

    printf '%s\n' "$name"
}

# Wait for systemd / OpenRC to reach a usable state.
wait_for_init() {
    local container="$1" timeout="${2:-60}"
    log "waiting for init in $container (timeout ${timeout}s)"

    local init
    init=$(docker exec "$container" sh -c '[ -d /run/openrc ] && echo openrc || echo systemd' 2>/dev/null || echo systemd)

    local i=0
    while [[ $i -lt $timeout ]]; do
        if [[ "$init" == "systemd" ]]; then
            local state
            state=$(docker exec "$container" systemctl is-system-running --wait 2>/dev/null || true)
            case "$state" in
                running|degraded|starting) ok "init ready: $state"; return 0 ;;
            esac
        else
            # OpenRC: rc-status exits 0 once at least one runlevel is up.
            if docker exec "$container" rc-status --runlevel default >/dev/null 2>&1; then
                ok "init ready (openrc)"; return 0
            fi
        fi
        sleep 1
        i=$((i+1))
    done
    fail "init did not become ready within ${timeout}s"
    docker exec "$container" sh -c 'systemctl --no-pager --failed 2>/dev/null; rc-status 2>/dev/null' || true
    return 1
}

# Wait for the Orva HTTP health endpoint to return 200.
wait_for_health() {
    local base_url="$1" timeout="${2:-90}"
    log "waiting for $base_url/api/v1/system/health (timeout ${timeout}s)"
    local i=0
    while [[ $i -lt $timeout ]]; do
        local code
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 \
            "$base_url/api/v1/system/health" 2>/dev/null || echo 000)
        if [[ "$code" == "200" ]]; then
            ok "health endpoint responding"; return 0
        fi
        sleep 1
        i=$((i+1))
    done
    fail "health endpoint did not respond within ${timeout}s"
    return 1
}

# Convenience wrapper for docker exec with a shared env.
exec_in() {
    local container="$1"; shift
    docker exec -e DEBIAN_FRONTEND=noninteractive "$container" "$@"
}

# Read the bootstrap admin key from inside the container. Polls because the
# key is written async during the daemon's first boot.
read_admin_key() {
    local container="$1" timeout="${2:-30}"
    local i=0
    while [[ $i -lt $timeout ]]; do
        local key
        key=$(docker exec "$container" cat /var/lib/orva/.admin-key 2>/dev/null || true)
        if [[ -n "$key" ]]; then
            printf '%s' "$key"
            return 0
        fi
        sleep 1
        i=$((i+1))
    done
    return 1
}

# Cleanup helper — tears down container + volume.
cleanup_container() {
    local container="$1"
    log "tearing down $container"
    docker rm -f "$container" >/dev/null 2>&1 || true
}
