#!/bin/sh
# Orva universal installer — POSIX sh (works under dash, busybox ash, bash).
#
# One install script for three outcomes:
#   * docker      — run the server as a Docker Compose stack (recommended)
#   * bare-metal  — install the daemon natively (systemd / OpenRC) + nsjail
#   * cli         — install only the `orva` CLI client (talk to a remote server)
#
# It is smart about the host: it detects Docker, an existing Orva install (and
# offers an upgrade), the distro, the architecture, and the init system. With a
# terminal attached it asks; piped (curl | sh) or with --yes it runs unattended.
#
# Quick start:
#   curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --docker --yes
#   curl -fsSL .../install.sh | sh -s -- --bare-metal --version vYYYY.MM.DD
#   curl -fsSL .../install.sh | sh -s -- --cli-only
#
# Everything is overridable by flag OR env var. Precedence: flag > env > prompt
# > default. Run with --help for the full reference.
#
# Idempotent: re-running upgrades the binary/image and preserves the data dir.

set -eu

# ── Defaults (each overridable by env, then by flag) ─────────────────────────
REPO="${ORVA_REPO:-Harsh-2002/Orva}"
VERSION="${ORVA_VERSION:-}"                       # empty → resolve "latest"
PREFIX="${ORVA_PREFIX:-/opt/orva}"
DATA_DIR="${ORVA_DATA_DIR:-/var/lib/orva}"
CLI_INSTALL_PATH="${ORVA_CLI_PATH:-/usr/local/bin/orva}"
COMPOSE_DIR="${ORVA_COMPOSE_DIR:-/opt/orva}"
PORT="${ORVA_PORT:-8443}"
DOCKER_RUNTIME_OVERRIDE="${ORVA_DOCKER_RUNTIME:-}"   # force the container runtime
SERVICE_USER="orva"
DRYRUN="${ORVA_INSTALL_DRYRUN:-0}"
NO_PKG="${ORVA_NO_PKG:-0}"

# Self-download URL used when the script is piped (curl | sh) and we need a
# real file on disk to sudo-re-exec and to read /dev/tty for prompts. An
# explicit ORVA_SELF_URL wins; otherwise it is derived from REPO *after*
# flag parsing (see resolve_self_url) so `--repo` is honoured.
SELF_URL="${ORVA_SELF_URL:-}"

resolve_self_url() {
    [ -n "$SELF_URL" ] && return
    SELF_URL="https://github.com/${REPO}/releases/latest/download/install.sh"
}

# Runtime state, filled in by flags / detection.
MODE=""                 # "", docker, bare-metal, cli
ASSUME_YES=0            # --yes / -y
WANT_START=""           # "", 1 (force start), 0 (skip)
WANT_CLI_SHORTCUT=1     # install `orva` onto PATH after a bare-metal install
DO_UNINSTALL=0
PURGE=0
INTERACTIVE=0           # decided in decide_interactive()
RUNTIME_FOR_COMPOSE=""  # decided in assess_runtime(); empty = docker default
DOCKER_DEFAULT_RUNTIME=""
DOCKER_RUNTIMES=""
DOCKER_RUNTIME_PATHS=""
SERVICE_DISABLE_USERNS="${ORVA_DISABLE_USERNS:-}"

# ── Logging ──────────────────────────────────────────────────────────────────
log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxxx\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

containerized_host() {
    [ -f /.dockerenv ] ||
        [ -f /run/.containerenv ] ||
        [ -f /run/systemd/container ] ||
        grep -qaE '(lxc|container)' /proc/1/cgroup 2>/dev/null
}

# Pacman 7 downloads as the unprivileged `alpm` user by default. That is the
# right security posture on a real Arch host, but its downloader sandbox cannot
# create temporary database/package files in some Docker/systemd-container
# filesystems (including the installer E2E image). Use a temporary config that
# keeps every repository/signature setting while disabling only DownloadUser,
# and only when we have positively detected a container. Never weaken pacman's
# sandbox on a normal bare-metal host.
pacman_run() {
    if containerized_host && [ -r /etc/pacman.conf ] &&
       grep -q '^[[:space:]]*DownloadUser[[:space:]]*=' /etc/pacman.conf; then
        (
            pr_conf=$(mktemp) || exit 1
            trap 'rm -f "$pr_conf"' EXIT INT TERM
            sed '/^[[:space:]]*DownloadUser[[:space:]]*=/d' /etc/pacman.conf > "$pr_conf"
            pacman --config "$pr_conf" "$@"
        )
        return $?
    fi
    pacman "$@"
}

usage() {
    cat <<EOF
Orva installer — install the server (Docker or bare-metal) or just the CLI.

USAGE:
  install.sh [MODE] [OPTIONS]
  curl -fsSL ${SELF_URL} | sh
  curl -fsSL ${SELF_URL} | sh -s -- [MODE] [OPTIONS]

MODE (pick one; if omitted, you are asked, or it is auto-detected):
  --docker            run the server as a Docker Compose stack
  --bare-metal        install the daemon natively (systemd/OpenRC + nsjail)
  --cli-only          install only the 'orva' CLI client
  --mode <m>          same as above; m = docker | bare-metal | cli

OPTIONS:
  --version <tag>     release to install (default: latest)            [ORVA_VERSION]
  --prefix <dir>      bare-metal install prefix (default: /opt/orva)  [ORVA_PREFIX]
  --data-dir <dir>    data directory (default: /var/lib/orva)         [ORVA_DATA_DIR]
  --cli-path <path>   CLI destination (default: /usr/local/bin/orva)  [ORVA_CLI_PATH]
  --compose-dir <d>   Docker compose dir (default: /opt/orva)         [ORVA_COMPOSE_DIR]
  --port <n>          listen port / Docker host port (default: 8443)  [ORVA_PORT]
  --runtime <name>    Docker container runtime (auto-detected if unset; [ORVA_DOCKER_RUNTIME]
                      Kata qemu/clh supported, gVisor/runsc refused)
  --repo <owner/name> GitHub repo (default: ${REPO})                  [ORVA_REPO]
  -y, --yes           non-interactive; accept all defaults
  --start / --no-start  enable+start the service after a bare-metal install
  --no-cli            do not add the 'orva' CLI shortcut to PATH (bare-metal)
  --no-pkg            skip system package installation                [ORVA_NO_PKG]
  --dry-run           detect + print plan, change nothing             [ORVA_INSTALL_DRYRUN]
  --uninstall [--purge]  remove Orva (--purge also deletes data + user)
  -h, --help          show this help

Examples:
  sh install.sh                                  # interactive, smart defaults
  sh install.sh --docker --port 9000 --yes
  sh install.sh --bare-metal --version vYYYY.MM.DD --start   # omit to take the current release
  sh install.sh --cli-only --cli-path ~/.local/bin/orva
EOF
}

# ── Self-bootstrap: get onto disk + become root ──────────────────────────────
# When piped (curl | sh) the script has no path on disk, so we can neither
# sudo-re-exec nor read /dev/tty reliably. Re-download a copy and re-exec it.
# Then, if not root, re-exec under sudo/doas. ORVA_BOOTSTRAPPED guards loops.
bootstrap() {
    if [ "${ORVA_BOOTSTRAPPED:-0}" != "1" ]; then
        bs_self="$0"
        bs_need_dl=0
        case "$bs_self" in
            sh|-sh|ash|dash|bash|-bash|-|"") bs_need_dl=1 ;;
            *) [ -f "$bs_self" ] || bs_need_dl=1 ;;
        esac
        if [ "$bs_need_dl" = "1" ]; then
            have curl || die "curl is required to bootstrap a piped install"
            bs_tmp=$(mktemp)
            # Clean the temp file if the download dies; on the successful
            # `exec` path the new process inherits no trap and reuses bs_tmp,
            # so we only need cleanup for the failure case.
            trap 'rm -f "$bs_tmp"' EXIT INT TERM
            log "piped execution detected — fetching installer to $bs_tmp"
            curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
                "$SELF_URL" -o "$bs_tmp" || die "failed to download installer from $SELF_URL"
            trap - EXIT INT TERM
            export ORVA_BOOTSTRAPPED=1
            exec sh "$bs_tmp" "$@"
        fi
        export ORVA_BOOTSTRAPPED=1
    fi

    # Become root for everything except --dry-run and --cli-only-to-a-user-path.
    if [ "$DRYRUN" != "1" ] && [ "$(id -u)" -ne 0 ] && root_required; then
        if have sudo; then
            warn "re-executing under sudo"
            exec sudo -E sh "$0" "$@"
        elif have doas; then
            warn "re-executing under doas"
            exec doas sh "$0" "$@"
        else
            die "must run as root (install sudo or doas, or re-run as root)"
        fi
    fi
}

# root_required: a CLI-only install into a user-writable path needs no root.
# Everything else (docker socket, /opt, system service) does.
root_required() {
    if [ "$MODE" = "cli" ]; then
        rr_dir=$(dirname "$CLI_INSTALL_PATH")
        [ -w "$rr_dir" ] && return 1   # writable → no root needed
        [ -d "$rr_dir" ] || return 0
        return 0
    fi
    return 0
}

# ── Flag parsing (flag > env > prompt > default) ─────────────────────────────
parse_args() {
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --docker)      MODE="docker" ;;
            --bare-metal|--baremetal|--bare) MODE="bare-metal" ;;
            --cli-only|--cli) MODE="cli" ;;
            --mode)        shift; MODE="${1:-}" ;;
            --mode=*)      MODE="${1#*=}" ;;
            --version)     shift; VERSION="${1:-}" ;;
            --version=*)   VERSION="${1#*=}" ;;
            --prefix)      shift; PREFIX="${1:-}" ;;
            --prefix=*)    PREFIX="${1#*=}" ;;
            --data-dir)    shift; DATA_DIR="${1:-}" ;;
            --data-dir=*)  DATA_DIR="${1#*=}" ;;
            --cli-path)    shift; CLI_INSTALL_PATH="${1:-}" ;;
            --cli-path=*)  CLI_INSTALL_PATH="${1#*=}" ;;
            --compose-dir) shift; COMPOSE_DIR="${1:-}" ;;
            --compose-dir=*) COMPOSE_DIR="${1#*=}" ;;
            --port)        shift; PORT="${1:-}" ;;
            --port=*)      PORT="${1#*=}" ;;
            --runtime)     shift; DOCKER_RUNTIME_OVERRIDE="${1:-}" ;;
            --runtime=*)   DOCKER_RUNTIME_OVERRIDE="${1#*=}" ;;
            --repo)        shift; REPO="${1:-}" ;;
            --repo=*)      REPO="${1#*=}" ;;
            -y|--yes|--non-interactive) ASSUME_YES=1 ;;
            --start)       WANT_START=1 ;;
            --no-start)    WANT_START=0 ;;
            --no-cli)      WANT_CLI_SHORTCUT=0 ;;
            --no-pkg)      NO_PKG=1 ;;
            --dry-run|--dryrun) DRYRUN=1 ;;
            --uninstall)   DO_UNINSTALL=1 ;;
            --purge)       PURGE=1 ;;
            -h|--help)     usage; exit 0 ;;
            *) die "unknown option: $1 (run with --help)" ;;
        esac
        shift
    done

    case "$MODE" in ""|docker|bare-metal|cli) ;; *) die "invalid --mode: $MODE" ;; esac
}

# ── Interactivity ────────────────────────────────────────────────────────────
# Interactive when a terminal is reachable AND the operator did not ask for
# unattended (--yes) AND there is something left to ask (no explicit mode).
decide_interactive() {
    INTERACTIVE=0
    [ "$ASSUME_YES" = "1" ] && return
    [ "$DRYRUN" = "1" ] && return
    # A /dev/tty device node merely EXISTING with rw permission bits is NOT a
    # reliable signal — under CI / `docker exec` (no -t) it is present and
    # readable yet has no controlling terminal, so opening it for a prompt
    # fails with ENXIO and, under `set -e`, aborts the whole installer.
    # Probe for a REAL terminal instead: stdin is a TTY (covers
    # `sh install.sh` from a shell), or /dev/tty behaves as one (covers
    # `curl | sh` from an interactive shell, where stdin is the pipe but a
    # controlling terminal is still attached). Both correctly read false in CI.
    if [ -t 0 ]; then
        INTERACTIVE=1
    elif [ -e /dev/tty ] && tty -s </dev/tty 2>/dev/null; then
        INTERACTIVE=1
    fi
}

# ask_yn QUESTION DEFAULT(y|n) → returns 0 for yes, 1 for no.
# Non-interactive: returns the default without prompting.
ask_yn() {
    ay_q="$1"; ay_def="$2"
    if [ "$INTERACTIVE" != "1" ]; then
        [ "$ay_def" = "y" ]
        return
    fi
    if [ "$ay_def" = "y" ]; then ay_hint="[Y/n]"; else ay_hint="[y/N]"; fi
    printf '\033[1;35m??\033[0m %s %s ' "$ay_q" "$ay_hint" > /dev/tty
    read -r ay_ans < /dev/tty || ay_ans=""
    [ -z "$ay_ans" ] && ay_ans="$ay_def"
    case "$ay_ans" in [Yy]*) return 0 ;; *) return 1 ;; esac
}

# ask_choice PROMPT DEFAULT OPT1 OPT2 ... → echoes the chosen option.
ask_choice() {
    ac_prompt="$1"; ac_def="$2"; shift 2
    if [ "$INTERACTIVE" != "1" ]; then printf '%s\n' "$ac_def"; return; fi
    {
        printf '\033[1;35m??\033[0m %s\n' "$ac_prompt"
        ac_i=1
        for ac_o in "$@"; do
            if [ "$ac_o" = "$ac_def" ]; then
                printf '   %d) %s  (default)\n' "$ac_i" "$ac_o"
            else
                printf '   %d) %s\n' "$ac_i" "$ac_o"
            fi
            ac_i=$((ac_i + 1))
        done
        printf '   choice [%s]: ' "$ac_def"
    } > /dev/tty
    read -r ac_sel < /dev/tty || ac_sel=""
    [ -z "$ac_sel" ] && { printf '%s\n' "$ac_def"; return; }
    ac_i=1
    for ac_o in "$@"; do
        if [ "$ac_sel" = "$ac_i" ] || [ "$ac_sel" = "$ac_o" ]; then
            printf '%s\n' "$ac_o"; return
        fi
        ac_i=$((ac_i + 1))
    done
    printf '%s\n' "$ac_def"
}

# ── Detection ────────────────────────────────────────────────────────────────
# read_osr FIELD — echo one /etc/os-release field WITHOUT leaking the file's
# other assignments into our scope. os-release defines VERSION=, NAME=, ID=,
# etc.; sourcing it directly would clobber this script's own $VERSION. We
# source inside a subshell and emit only the requested field.
read_osr() {
    [ -r /etc/os-release ] || return 0
    # shellcheck source=/dev/null
    ( . /etc/os-release 2>/dev/null
      case "$1" in
          ID)          printf '%s' "${ID:-}" ;;
          ID_LIKE)     printf '%s' "${ID_LIKE:-}" ;;
          PRETTY_NAME) printf '%s' "${PRETTY_NAME:-}" ;;
      esac )
}

detect_distro() {
    DISTRO_ID=$(read_osr ID);          DISTRO_ID="${DISTRO_ID:-unknown}"
    DISTRO_LIKE=$(read_osr ID_LIKE)
    DISTRO_PRETTY=$(read_osr PRETTY_NAME); DISTRO_PRETTY="${DISTRO_PRETTY:-$DISTRO_ID}"
    log "host: $DISTRO_PRETTY"
}

detect_arch() {
    da_raw=$(uname -m)
    case "$da_raw" in
        x86_64|amd64)  ARCH=amd64 ;;
        aarch64|arm64) ARCH=arm64 ;;
        *) die "unsupported architecture: $da_raw (released builds: amd64, arm64)" ;;
    esac
    log "arch: $ARCH"
}

# Sets DOCKER_OK=1 and COMPOSE_CMD when a usable Docker + Compose v2/v1 exist.
detect_docker() {
    DOCKER_OK=0; COMPOSE_CMD=""
    have docker || return 0
    if ! docker info >/dev/null 2>&1; then
        warn "docker is installed but the daemon is not reachable (need sudo, or 'systemctl start docker')"
        return 0
    fi
    if docker compose version >/dev/null 2>&1; then
        COMPOSE_CMD="docker compose"; DOCKER_OK=1
    elif have docker-compose && docker-compose version >/dev/null 2>&1; then
        COMPOSE_CMD="docker-compose"; DOCKER_OK=1
    else
        warn "docker found but neither 'docker compose' nor 'docker-compose' works — Docker mode unavailable"
    fi
    if [ "$DOCKER_OK" = "1" ]; then
        DOCKER_DEFAULT_RUNTIME=$(docker info --format '{{.DefaultRuntime}}' 2>/dev/null || true)
        DOCKER_RUNTIMES=$(docker info --format '{{range $k, $v := .Runtimes}}{{$k}} {{end}}' 2>/dev/null || true)
        # name=path pairs so we can identify a runtime by its actual binary,
        # not just its (operator-chosen) label — a gVisor runtime registered
        # under a custom name must still be caught.
        DOCKER_RUNTIME_PATHS=$(docker info --format '{{range $k, $v := .Runtimes}}{{$k}}={{$v.Path}} {{end}}' 2>/dev/null || true)
    fi
}

# ── Docker runtime assessment ────────────────────────────────────────────────
# Orva runs functions in nsjail, which needs CLONE_NEW* namespace flags. That
# means the container runtime matters:
#   runc            — works (default)
#   kata / kata-*   — works (Kata puts a real kernel under the container:
#                     hypervisor isolation; qemu and cloud-hypervisor both OK)
#   runsc (gVisor)  — does NOT work: gVisor's user-space kernel rejects the
#                     namespace clone nsjail performs (verified incompatible)
# runtime_path NAME — echo the binary path Docker has registered for a runtime
# label (empty if unknown). Lets us classify a runtime by what it actually
# runs, not just its name.
runtime_path() {
    rp_name="$1"
    for rp_pair in $DOCKER_RUNTIME_PATHS; do
        case "$rp_pair" in
            "$rp_name"=*) printf '%s' "${rp_pair#*=}"; return ;;
        esac
    done
}

# is_gvisor NAME — true if the label OR its resolved binary points at gVisor.
# Catches a runsc runtime registered under an arbitrary name (e.g. "secure").
is_gvisor() {
    case "$1" in runsc|*gvisor*|*runsc*) return 0 ;; esac
    case "$(basename "$(runtime_path "$1")" 2>/dev/null)" in
        runsc|*gvisor*) return 0 ;;
    esac
    return 1
}

# is_kata NAME — true if the label OR its resolved binary is a Kata runtime.
is_kata() {
    case "$1" in kata|kata-*|*kata*) return 0 ;; esac
    case "$(basename "$(runtime_path "$1")" 2>/dev/null)" in
        *kata*) return 0 ;;
    esac
    return 1
}

runtime_available() { case " $DOCKER_RUNTIMES " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

# Sets RUNTIME_FOR_COMPOSE (empty = use Docker's default).
assess_runtime() {
    RUNTIME_FOR_COMPOSE=""
    ar_def="${DOCKER_DEFAULT_RUNTIME:-runc}"
    log "docker runtime: default=$ar_def${DOCKER_RUNTIMES:+, available: ${DOCKER_RUNTIMES}}"

    # 1. Explicit operator choice wins (but we still refuse gVisor).
    if [ -n "$DOCKER_RUNTIME_OVERRIDE" ]; then
        is_gvisor "$DOCKER_RUNTIME_OVERRIDE" &&
            die "--runtime $DOCKER_RUNTIME_OVERRIDE is gVisor; Orva cannot run under gVisor. Use runc or a Kata runtime."
        runtime_available "$DOCKER_RUNTIME_OVERRIDE" ||
            warn "runtime '$DOCKER_RUNTIME_OVERRIDE' is not in docker's runtime list — docker may reject it"
        RUNTIME_FOR_COMPOSE="$DOCKER_RUNTIME_OVERRIDE"
        is_kata "$DOCKER_RUNTIME_OVERRIDE" && log "  pinning Kata runtime '$DOCKER_RUNTIME_OVERRIDE' (hypervisor isolation)"
        return
    fi

    # 2. Default is gVisor → Orva won't run. Fall back to runc/Kata if present.
    if is_gvisor "$ar_def"; then
        warn "Docker's default runtime is gVisor ($ar_def) — Orva CANNOT run under it."
        warn "  nsjail needs CLONE_NEW* namespace flags gVisor rejects (verified incompatible)."
        if runtime_available runc; then
            RUNTIME_FOR_COMPOSE="runc"
            warn "  → pinning 'runtime: runc' for the Orva container so it works."
        else
            for ar_k in kata-clh kata-qemu kata; do
                runtime_available "$ar_k" && { RUNTIME_FOR_COMPOSE="$ar_k"; break; }
            done
            if [ -n "$RUNTIME_FOR_COMPOSE" ]; then
                warn "  → no runc; pinning Kata runtime '$RUNTIME_FOR_COMPOSE' instead."
            else
                warn "  → no runc or Kata runtime to fall back to — the stack will likely fail."
                if [ "$INTERACTIVE" = "1" ]; then
                    ask_yn "Continue anyway?" "n" || die "aborted: no Orva-compatible Docker runtime"
                fi
            fi
        fi
        return
    fi

    # 3. Default is Kata → supported; use it as-is (hypervisor isolation).
    if is_kata "$ar_def"; then
        log "  Kata default runtime ($ar_def) — supported (hypervisor isolation)."
        return
    fi

    # 4. runc / other → fine, use the default.
    log "  using Docker default runtime ($ar_def)."
}

# Sets EXISTING_KIND (none|bare|docker) and EXISTING_VERSION.
detect_existing() {
    EXISTING_KIND="none"; EXISTING_VERSION=""
    if [ -x "$PREFIX/bin/orva" ]; then
        EXISTING_KIND="bare"
        EXISTING_VERSION=$("$PREFIX/bin/orva" --version 2>/dev/null | head -n1 || echo "")
    fi
    if have docker && docker ps -a --format '{{.Names}}' 2>/dev/null | grep -q '^orva$'; then
        [ "$EXISTING_KIND" = "none" ] && EXISTING_KIND="docker"
        EXISTING_VERSION=$(docker inspect --format '{{.Config.Image}}' orva 2>/dev/null | sed 's/.*://' || echo "")
    fi
    if [ "$EXISTING_KIND" != "none" ]; then
        log "existing install: $EXISTING_KIND ${EXISTING_VERSION:+($EXISTING_VERSION)}"
    fi
}

# ── Mode selection ───────────────────────────────────────────────────────────
choose_mode() {
    [ -n "$MODE" ] && { log "mode: $MODE"; return; }

    # An existing install pins the mode for upgrades unless overridden.
    if [ "$EXISTING_KIND" = "docker" ] && [ "$DOCKER_OK" = "1" ]; then
        MODE="docker"; log "mode: docker (matching existing install)"; return
    fi
    if [ "$EXISTING_KIND" = "bare" ]; then
        MODE="bare-metal"; log "mode: bare-metal (matching existing install)"; return
    fi

    if [ "$INTERACTIVE" = "1" ]; then
        if [ "$DOCKER_OK" = "1" ]; then
            cm_pick=$(ask_choice "Docker is available. How do you want to run Orva?" \
                "Docker Compose" "Docker Compose" "Bare-metal (native service)" "CLI only")
        else
            cm_pick=$(ask_choice "How do you want to install Orva?" \
                "Bare-metal (native service)" "Bare-metal (native service)" "CLI only")
        fi
        case "$cm_pick" in
            "Docker Compose")            MODE="docker" ;;
            "Bare-metal (native service)") MODE="bare-metal" ;;
            "CLI only")                  MODE="cli" ;;
        esac
    else
        # Unattended default: prefer Docker when usable, else bare-metal.
        if [ "$DOCKER_OK" = "1" ]; then MODE="docker"; else MODE="bare-metal"; fi
    fi
    log "mode: $MODE"
}

# ── Downloader bootstrap ─────────────────────────────────────────────────────
# Version resolution and asset downloads need curl (or wget). Minimal distro
# images (the E2E installer containers, and plenty of real bare-metal hosts) ship
# with neither, and the full prereq install happens later — so ensure a fetcher
# exists FIRST, installing curl via the detected package manager if necessary.
ensure_downloader() {
    if have curl; then return; fi
    [ "$DRYRUN" = "1" ] && return
    log "curl not found — installing it"
    case "$DISTRO_ID" in
        ubuntu|debian)
            DEBIAN_FRONTEND=noninteractive apt-get update -qq
            DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl ;;
        alpine) apk add --no-cache ca-certificates curl ;;
        fedora|rhel|centos|rocky|almalinux|amzn) { have dnf && dnf install -y curl; } || yum install -y curl ;;
        arch|manjaro|endeavouros) pacman_run -Sy --noconfirm --needed curl ;;
        opensuse-leap|opensuse-tumbleweed|sles) zypper --non-interactive install curl ;;
        *)
            case "$DISTRO_LIKE" in
                *debian*|*ubuntu*) DEBIAN_FRONTEND=noninteractive apt-get update -qq
                    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl ;;
                *rhel*|*fedora*) { have dnf && dnf install -y curl; } || yum install -y curl ;;
                *arch*) pacman_run -Sy --noconfirm --needed curl ;;
                *suse*) zypper --non-interactive install curl ;;
                *) die "curl is required to download the release — install it and re-run" ;;
            esac ;;
    esac
    have curl || die "failed to install curl"
}

# ── Version resolution ───────────────────────────────────────────────────────
#
# Two independent sources, because the API one is rate-limited and that limit
# is shared by IP. Unauthenticated api.github.com allows 60 requests/hour, so
# a NAT'd office, a CI runner, or a second install on the same box gets a 403
# and the install dies with "could not resolve latest release tag" -- which
# reads like Orva has no releases, not like a rate limit. curl --retry does
# not help: 403 is not a transient status, so it is never retried.
#
# The fallback is the plain /releases/latest redirect, which is ordinary web
# traffic rather than API traffic and is not subject to that quota. It
# resolves to .../releases/tag/<tag>, so the tag is the last path segment.
resolve_version() {
    if [ -n "$VERSION" ]; then log "version: $VERSION"; return; fi
    if [ "$DRYRUN" = "1" ]; then VERSION="latest"; log "version: latest (dryrun)"; return; fi
    log "resolving latest release from GitHub"

    # An authenticated call gets 5000/hour instead of 60. Honour a token if
    # the environment already has one (CI usually does); never require it.
    rv_auth=""
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        rv_auth="Authorization: Bearer ${GITHUB_TOKEN}"
    fi

    if [ -n "$rv_auth" ]; then
        VERSION=$(curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
            -H "$rv_auth" "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
    else
        VERSION=$(curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
            "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
    fi

    if [ -z "$VERSION" ]; then
        warn "GitHub API did not answer (rate limit?) — falling back to the release redirect"
        rv_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' --retry 3 --retry-delay 2 \
            --connect-timeout 15 "https://github.com/${REPO}/releases/latest" 2>/dev/null)
        case "$rv_url" in
            */releases/tag/*) VERSION="${rv_url##*/}" ;;
            *)                VERSION="" ;;
        esac
    fi

    # Guard against a redirect that lands somewhere unexpected: every Orva tag
    # is vYYYY.MM.DD, and feeding a bogus value downstream would build asset
    # URLs that 404 much later with a far less obvious message.
    case "$VERSION" in
        v[0-9][0-9][0-9][0-9].[0-9][0-9].[0-9][0-9]) ;;
        "") die "could not resolve latest release tag (pass --version)" ;;
        *)  die "resolved an unexpected release tag '$VERSION' (pass --version)" ;;
    esac
    log "version: $VERSION"
}

# ── Package installation (bare-metal only) ───────────────────────────────────
install_prereqs() {
    if [ "$NO_PKG" = "1" ]; then log "skipping package install (--no-pkg)"; return; fi
    case "$DISTRO_ID" in
        ubuntu)        ip_nsjail="libprotobuf32t64 libnl-route-3-200 libnl-3-200 libcap2-bin" ;;
        debian)        ip_nsjail="libprotobuf32 libnl-route-3-200 libnl-3-200 libcap2-bin" ;;
        alpine)        ip_nsjail="protobuf libnl3 gcompat libcap" ;;
        fedora|rhel|centos|rocky|almalinux|amzn) ip_nsjail="protobuf libnl3 libcap" ;;
        arch|manjaro|endeavouros)                ip_nsjail="protobuf libnl libcap" ;;
        opensuse-leap|opensuse-tumbleweed|sles)  ip_nsjail="libprotobuf-lite libnl3-200 libcap-progs" ;;
        *)             ip_nsjail="" ;;
    esac
    ip_pkgs="ca-certificates curl tar zstd $ip_nsjail"
    log "installing prerequisites: $ip_pkgs"
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would install: $ip_pkgs"; return; }

    case "$DISTRO_ID" in
        ubuntu|debian)
            DEBIAN_FRONTEND=noninteractive apt-get update -qq
            # shellcheck disable=SC2086
            DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $ip_pkgs ;;
        alpine)
            # shellcheck disable=SC2086
            apk add --no-cache $ip_pkgs ;;
        fedora|rhel|centos|rocky|almalinux|amzn)
            ip_dnf="yum"; have dnf && ip_dnf="dnf"
            # shellcheck disable=SC2086
            "$ip_dnf" install -y --allowerasing --setopt=install_weak_deps=False $ip_pkgs ;;
        arch|manjaro|endeavouros)
            # shellcheck disable=SC2086
            pacman_run -Syu --noconfirm --needed $ip_pkgs ;;
        opensuse-leap|opensuse-tumbleweed|sles)
            # shellcheck disable=SC2086
            zypper --non-interactive install $ip_pkgs ;;
        *)
            case "$DISTRO_LIKE" in
                *debian*|*ubuntu*) DEBIAN_FRONTEND=noninteractive apt-get update -qq
                    # shellcheck disable=SC2086
                    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $ip_pkgs ;;
                *rhel*|*fedora*)
                    # shellcheck disable=SC2086
                    { have dnf && dnf install -y $ip_pkgs; } || yum install -y $ip_pkgs ;;
                *arch*)
                    # shellcheck disable=SC2086
                    pacman_run -Syu --noconfirm --needed $ip_pkgs ;;
                *suse*)
                    # shellcheck disable=SC2086
                    zypper --non-interactive install $ip_pkgs ;;
                *) warn "unknown distro '$DISTRO_ID' — install manually: $ip_pkgs" ;;
            esac ;;
    esac
}

# ── Kernel / sandbox-egress preflight (bare-metal) ───────────────────────────
# The egress policy is enforced per sandbox by nsjail's NSTUN backend, which
# needs a TAP device from /dev/net/tun. On bare metal nothing creates or relaxes
# that node for us (the Docker entrypoint does), and the unit runs as
# $SERVICE_USER — so existence alone does not prove the daemon can open it.
# Warn, never block: functions with network_mode "none" are unaffected.
TUN_STATUS="unknown"; TUN_HINT=""
check_egress_device() {
    [ "$DRYRUN" = "1" ] && { TUN_STATUS="dryrun"; return; }
    [ -c /dev/net/tun ] || { have modprobe && modprobe tun 2>/dev/null; } || true
    if [ ! -c /dev/net/tun ]; then
        TUN_STATUS="unavailable"
        TUN_HINT="load the module: modprobe tun (persist it in /etc/modules-load.d/tun.conf)"
        warn "/dev/net/tun missing — functions with network_mode 'egress' cannot spawn"
        return
    fi
    ced_mode=$(stat -c '%A' /dev/net/tun 2>/dev/null || true)
    if [ -z "$ced_mode" ]; then
        TUN_STATUS="present (permissions unverified)"
        log "/dev/net/tun present — could not read its permission bits"
        return
    fi
    ced_owner=$(stat -c '%U' /dev/net/tun 2>/dev/null || true)
    ced_group=$(stat -c '%G' /dev/net/tun 2>/dev/null || true)
    # Symbolic mode is 'c' + owner(2-4) + group(5-7) + other(8-10); nsjail opens
    # the node read-write, so anything short of rw for $SERVICE_USER is a fail.
    ced_ok=0
    case "$ced_mode" in *rw?) ced_ok=1 ;; esac
    if [ "$ced_ok" = "0" ] && [ "$ced_owner" = "$SERVICE_USER" ]; then
        case "$ced_mode" in ?rw*) ced_ok=1 ;; esac
    fi
    if [ "$ced_ok" = "0" ] && [ -n "$ced_group" ] &&
       id -nG "$SERVICE_USER" 2>/dev/null | grep -qw -- "$ced_group"; then
        case "$ced_mode" in ????rw*) ced_ok=1 ;; esac
    fi
    if [ "$ced_ok" = "1" ]; then
        TUN_STATUS="ready"
        log "sandbox egress OK — $SERVICE_USER can open /dev/net/tun ($ced_mode)"
        return
    fi
    TUN_STATUS="blocked ($ced_mode ${ced_owner:-?}:${ced_group:-?})"
    TUN_HINT="grant $SERVICE_USER read-write access, e.g. a udev rule setting MODE=\"0660\" GROUP=\"$SERVICE_USER\""
    warn "$SERVICE_USER cannot open /dev/net/tun — egress functions will fail to spawn"
}

check_kernel_features() {
    ckf_missing=""
    if [ -r /proc/sys/kernel/unprivileged_userns_clone ] &&
       [ "$(cat /proc/sys/kernel/unprivileged_userns_clone)" != "1" ]; then
        warn "unprivileged user namespaces disabled — enable: sysctl -w kernel.unprivileged_userns_clone=1"
        ckf_missing="userns "
    fi
    if [ -r /proc/sys/kernel/apparmor_restrict_unprivileged_userns ] &&
       [ "$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns)" = "1" ]; then
        warn "AppArmor restricts unprivileged user namespaces — using nsjail's setcap fallback"
        ckf_missing="userns ${ckf_missing#userns }"
        [ -n "$SERVICE_DISABLE_USERNS" ] || SERVICE_DISABLE_USERNS=1
    fi
    if containerized_host; then
        warn "containerized host detected — using nsjail's setcap fallback"
        [ -n "$SERVICE_DISABLE_USERNS" ] || SERVICE_DISABLE_USERNS=1
    fi
    [ -n "$SERVICE_DISABLE_USERNS" ] || SERVICE_DISABLE_USERNS=0
    if ! grep -q cgroup2 /proc/mounts 2>/dev/null; then
        warn "cgroup v2 not detected — per-function resource limits will be best-effort"
        ckf_missing="${ckf_missing}cgroupv2 "
    fi
    if [ -n "$ckf_missing" ]; then
        warn "kernel features missing: ${ckf_missing}(isolation may be partial)"
    else
        log "kernel features: OK"
    fi
}

# ── Download helpers (all assets checksum-verified) ──────────────────────────
fetch() {
    # fetch URL DEST
    curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 -o "$2" "$1"
}

# verify FILE ASSET_NAME — checks FILE's sha256 against $tmp/checksums.txt.
verify() {
    v_file="$1"; v_asset="$2"
    v_want=$(grep " ${v_asset}\$" "$tmp/checksums.txt" 2>/dev/null | awk '{print $1}' | head -n1)
    [ -n "$v_want" ] || die "release checksums do not contain $v_asset"
    v_got=$(sha256sum "$v_file" | awk '{print $1}')
    [ "$v_want" = "$v_got" ] || die "checksum mismatch for $v_asset (want $v_want, got $v_got)"
}

# ── Bare-metal install ───────────────────────────────────────────────────────
download_and_install_binaries() {
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would download orva + nsjail ($ARCH)"; return; }
    base="https://github.com/${REPO}/releases/download/${VERSION}"
    log "downloading orva + nsjail (linux-${ARCH})"
    fetch "$base/orva-linux-${ARCH}"   "$tmp/orva"   || die "failed to download orva-linux-${ARCH}"
    fetch "$base/nsjail-linux-${ARCH}" "$tmp/nsjail" || die "failed to download nsjail-linux-${ARCH}"
    fetch "$base/checksums.txt"        "$tmp/checksums.txt" || die "failed to download checksums.txt"
    log "verifying checksums"
    verify "$tmp/orva"   "orva-linux-${ARCH}"
    verify "$tmp/nsjail" "nsjail-linux-${ARCH}"
    log "installing binaries to $PREFIX/bin"
    install -d -m 0755 "$PREFIX/bin" "$PREFIX/share/orva/scripts"
    install -m 0755 "$tmp/orva"   "$PREFIX/bin/orva"
    install -m 0755 "$tmp/nsjail" "$PREFIX/bin/nsjail"
    # Daemon defaults to nsjail at /usr/local/bin/nsjail (matches Docker image).
    install -d -m 0755 /usr/local/bin
    install -m 0755 "$tmp/nsjail" /usr/local/bin/nsjail
    nsjail_caps="cap_sys_admin,cap_setuid,cap_setgid,cap_net_admin,cap_net_bind_service=eip"
    have setcap || die "setcap is required to install nsjail capabilities"
    setcap "$nsjail_caps" "$PREFIX/bin/nsjail" || die "failed to apply capabilities to $PREFIX/bin/nsjail"
    setcap "$nsjail_caps" /usr/local/bin/nsjail || die "failed to apply capabilities to /usr/local/bin/nsjail"
    cap_state=$(getcap /usr/local/bin/nsjail 2>/dev/null || true)
    for cap_name in cap_sys_admin cap_setuid cap_setgid cap_net_admin cap_net_bind_service; do
        case "$cap_state" in
            *"$cap_name"*) ;;
            *) die "nsjail capability verification failed: missing $cap_name" ;;
        esac
    done
    case "$cap_state" in
        *=eip*) ;;
        *) die "nsjail capability verification failed: expected effective, inheritable, and permitted flags" ;;
    esac
}

download_rootfs() {
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would download runtime rootfs tarballs"; return; }
    base="https://github.com/${REPO}/releases/download/${VERSION}"
    install -d -m 0755 "$DATA_DIR/rootfs"
    for dr_rt in node python; do
        dr_target="$DATA_DIR/rootfs/$dr_rt"
        if [ -f "$dr_target/.orva-rootfs-version" ] &&
           [ "$(cat "$dr_target/.orva-rootfs-version" 2>/dev/null)" = "$VERSION" ]; then
            log "rootfs/$dr_rt already at $VERSION (skipping)"; continue
        fi
        dr_tar="rootfs-${dr_rt}-${ARCH}.tar.zst"
        log "downloading rootfs/$dr_rt"
        if ! fetch "$base/$dr_tar" "$tmp/$dr_tar"; then
            warn "rootfs $dr_rt ($ARCH) not in this release — runtime unavailable"; continue
        fi
        verify "$tmp/$dr_tar" "$dr_tar"
        rm -rf "$dr_target"; install -d -m 0755 "$dr_target"
        zstd -dc "$tmp/$dr_tar" | tar -C "$dr_target" -xf -
        printf '%s\n' "$VERSION" > "$dr_target/.orva-rootfs-version"
    done
}

create_user() {
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would create user $SERVICE_USER + $DATA_DIR"; return; }
    if id -u "$SERVICE_USER" >/dev/null 2>&1; then
        log "user $SERVICE_USER exists"
    else
        log "creating system user $SERVICE_USER"
        if have useradd; then
            useradd --system --no-create-home --shell /sbin/nologin "$SERVICE_USER" 2>/dev/null ||
                useradd -r -s /bin/false "$SERVICE_USER"
        elif have adduser; then
            { have addgroup && addgroup -S "$SERVICE_USER" 2>/dev/null; } || true
            adduser -S -D -H -s /sbin/nologin -G "$SERVICE_USER" "$SERVICE_USER"
        else
            warn "no useradd/adduser — create user '$SERVICE_USER' manually"
        fi
    fi
    cu_group=$(id -gn "$SERVICE_USER" 2>/dev/null || echo "$SERVICE_USER")
    install -d -o "$SERVICE_USER" -g "$cu_group" "$DATA_DIR" "$DATA_DIR/functions" "$DATA_DIR/rootfs"
}

write_service_files() {
    [ "$DRYRUN" = "1" ] && return
    install -d -m 0755 "$PREFIX/share/orva/scripts"
    cat > "$PREFIX/share/orva/scripts/orva.service" <<EOF
[Unit]
Description=Orva self-hosted serverless platform
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
ExecStart=${PREFIX}/bin/orva serve
Restart=on-failure
# The restore endpoint exits 70 on purpose to force a restart under
# Restart=on-failure. Listed explicitly so the intent survives a future
# change to the Restart= policy.
RestartForceExitStatus=70
RestartSec=5s
TimeoutStopSec=30s
KillSignal=SIGTERM
# The daemon itself holds no capabilities: the egress policy is enforced inside
# each sandbox by nsjail, not by host firewall rules. The bounding set must stay
# a superset of nsjail's file capabilities (=eip) — Linux rejects execve(nsjail)
# with EPERM if any of them cannot be raised here, before nsjail logs a word.
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_NET_ADMIN CAP_SETUID CAP_SETGID CAP_NET_BIND_SERVICE
NoNewPrivileges=false
Delegate=yes
ReadWritePaths=${DATA_DIR}
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
# ProtectKernelTunables is intentionally OMITTED: it overmounts /proc/sys,
# which prevents nsjail from mounting a fresh procfs inside its user
# namespace (functions would crash with 'Failed to mount mandatory point
# /proc'). nsjail already isolates each function heavily (userns + seccomp
# + chroot), so the marginal loss is acceptable.
ProtectKernelModules=true
LimitNOFILE=65536
LimitNPROC=8192
Environment=ORVA_DATA_DIR=${DATA_DIR}
Environment=ORVA_DISABLE_USERNS=${SERVICE_DISABLE_USERNS}
Environment=ORVA_PORT=${PORT}

[Install]
WantedBy=multi-user.target
EOF

    cat > "$PREFIX/share/orva/scripts/orva.openrc" <<EOF
#!/sbin/openrc-run
description="Orva self-hosted serverless platform"
command="${PREFIX}/bin/orva"
command_args="serve"
command_user="${SERVICE_USER}:${SERVICE_USER}"
command_background="yes"
pidfile="/run/orva.pid"
output_log="/var/log/orva.log"
error_log="/var/log/orva.log"

export ORVA_DATA_DIR="${DATA_DIR}"
export ORVA_DISABLE_USERNS="${SERVICE_DISABLE_USERNS}"
export ORVA_PORT="${PORT}"

depend() {
    need net
    after firewall
}

start_pre() {
    checkpath -d -m 0755 -o ${SERVICE_USER}:${SERVICE_USER} ${DATA_DIR}
    checkpath -d -m 0755 -o ${SERVICE_USER}:${SERVICE_USER} ${DATA_DIR}/functions
    checkpath -d -m 0755 -o ${SERVICE_USER}:${SERVICE_USER} ${DATA_DIR}/rootfs
    checkpath -f -m 0644 -o ${SERVICE_USER}:${SERVICE_USER} /var/log/orva.log
}
EOF
    chmod 0755 "$PREFIX/share/orva/scripts/orva.openrc"
    write_uninstall_script
}

install_adapters() {
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would install language adapters"; return; }
    log "installing language adapters into rootfs"
    ORVA_DATA_DIR="$DATA_DIR" "$PREFIX/bin/orva" setup --skip-nsjail --data-dir "$DATA_DIR" \
        || warn "orva setup failed — adapters may be missing (invocations could crash)"
    if id -u "$SERVICE_USER" >/dev/null 2>&1; then
        ia_group=$(id -gn "$SERVICE_USER" 2>/dev/null || echo "$SERVICE_USER")
        chown -R "$SERVICE_USER:$ia_group" "$DATA_DIR/rootfs" 2>/dev/null || true
    fi
}

# install_unit copies the (re)generated unit file into the init system and
# sets SVC_KIND. Always run on fresh install AND upgrade so unit-file changes
# actually reach existing hosts. SVC_KIND ∈ none|systemd|openrc.
SVC_KIND="none"
install_unit() {
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would install service unit"; SVC_KIND="dryrun"; return; }
    if [ -d /run/systemd/system ] || { have systemctl && [ -d /etc/systemd/system ]; }; then
        SVC_KIND="systemd"
        log "installing systemd unit"
        install -m 0644 "$PREFIX/share/orva/scripts/orva.service" /etc/systemd/system/orva.service
        if [ -d /run/systemd/system ]; then
            systemctl daemon-reload
        fi
    elif [ -d /etc/init.d ] && have rc-update; then
        SVC_KIND="openrc"
        log "installing OpenRC unit"
        install -m 0755 "$PREFIX/share/orva/scripts/orva.openrc" /etc/init.d/orva
    else
        SVC_KIND="none"
        warn "no service manager detected — run manually: ORVA_DATA_DIR=$DATA_DIR $PREFIX/bin/orva serve"
    fi
}

# start_service enables + starts the freshly-installed unit. WANT_START: 1
# force, 0 skip, "" ask (default yes). Only called on FRESH installs — upgrades
# use restart_if_running so a deliberately-stopped service stays stopped.
start_service() {
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would (maybe) start service"; return; }
    [ "$SVC_KIND" = "none" ] && return

    is_start="$WANT_START"
    if [ -z "$is_start" ]; then
        if ask_yn "Enable and start the orva service now?" "y"; then is_start=1; else is_start=0; fi
    fi
    [ "$is_start" != "1" ] && { log "service installed but not started"; return; }

    if [ "$SVC_KIND" = "systemd" ] && [ -d /run/systemd/system ]; then
        log "enabling + starting orva (systemd)"
        systemctl enable --now orva || warn "failed to start orva — check: journalctl -u orva"
    elif [ "$SVC_KIND" = "systemd" ]; then
        warn "no systemd PID 1 here (container?) — enable on the real host: systemctl enable --now orva"
    elif [ "$SVC_KIND" = "openrc" ]; then
        log "enabling + starting orva (OpenRC)"
        rc-update add orva default 2>/dev/null || true
        service orva start || rc-service orva start || warn "failed to start orva"
    fi
}

# After an upgrade, restart whatever is already running so the new binary lands.
restart_if_running() {
    [ "$DRYRUN" = "1" ] && return
    if have systemctl && systemctl is-active --quiet orva 2>/dev/null; then
        log "restarting orva (systemd) to apply the upgrade"
        systemctl restart orva || warn "restart failed — check: journalctl -u orva"
    elif [ -f /etc/init.d/orva ] && { rc-service orva status >/dev/null 2>&1 || service orva status >/dev/null 2>&1; }; then
        log "restarting orva (OpenRC) to apply the upgrade"
        { rc-service orva restart || service orva restart; } || warn "restart failed"
    fi
}

install_cli_shortcut() {
    [ "$WANT_CLI_SHORTCUT" != "1" ] && return
    [ "$DRYRUN" = "1" ] && { log "(dryrun) would link CLI → $CLI_INSTALL_PATH"; return; }
    install -d -m 0755 "$(dirname "$CLI_INSTALL_PATH")"
    # Symlink the already-installed server binary (it IS the CLI) — no second download.
    ln -sf "$PREFIX/bin/orva" "$CLI_INSTALL_PATH"
    log "CLI on PATH: $CLI_INSTALL_PATH -> $PREFIX/bin/orva"
}

run_bare_metal() {
    bm_upgrade=0
    [ "$EXISTING_KIND" = "bare" ] && bm_upgrade=1
    if [ "$bm_upgrade" = "1" ]; then
        if [ -n "$EXISTING_VERSION" ] && [ "$EXISTING_VERSION" = "$VERSION" ]; then
            ask_yn "Orva $VERSION is already installed. Reinstall/repair?" "n" || {
                log "nothing to do (already at $VERSION)"; return; }
        else
            log "upgrading bare-metal install ${EXISTING_VERSION:-?} → $VERSION"
        fi
    fi
    install_prereqs
    check_kernel_features
    download_and_install_binaries
    write_service_files
    create_user
    # After create_user: the /dev/net/tun probe reports whether the *service
    # user* can open it, which needs that user to exist.
    check_egress_device
    download_rootfs
    install_adapters
    # Always refresh the unit file so unit changes reach existing installs.
    install_unit
    if [ "$bm_upgrade" = "1" ]; then
        # Apply the new binary + refreshed unit to a running service, but don't
        # resurrect one the operator stopped, and don't re-prompt to start.
        restart_if_running
    else
        start_service
    fi
    install_cli_shortcut
    print_followup_bare
}

# ── Docker (Compose) install ─────────────────────────────────────────────────
write_compose() {
    wc_runtime=""
    [ -n "$RUNTIME_FOR_COMPOSE" ] && wc_runtime="    runtime: ${RUNTIME_FOR_COMPOSE}"
    install -d -m 0755 "$COMPOSE_DIR"
    # The Orva image is published with exactly ONE tag, :latest (multi-arch);
    # there is no per-version image tag, so pinning :${VERSION} here would 404
    # on `docker compose pull`. Versioning lives in the GitHub Release/binaries.
    cat > "$COMPOSE_DIR/docker-compose.yml" <<EOF
# Generated by the Orva installer. Re-run install.sh --docker to update.
services:
  orva:
    image: ghcr.io/harsh-2002/orva:latest
    container_name: orva
${wc_runtime}
    restart: unless-stopped
    ports:
      - "${PORT}:8443"
    # nsjail enrolls each sandbox PID in the host cgroup hierarchy; without
    # pid: host + cgroup: host every invocation fails to spawn on runc with
    # "Launching child process failed". On Kata these are unnecessary (the
    # guest kernel delegates cgroups) — comment them out if using runtime: kata-clh.
    pid: host
    cgroup: host
    # SYS_ADMIN: nsjail user namespaces / mounts / cgroup delegation.
    # No NET_ADMIN: each sandbox's TAP device is created inside nsjail's own
    # user namespace, so the kernel checks ns_capable() there instead.
    cap_add:
      - SYS_ADMIN
    # Lifted so nsjail can apply its own per-function seccomp + namespaces.
    security_opt:
      - seccomp=unconfined
      - apparmor=unconfined
      - systempaths=unconfined
    # Per-function network_mode: egress uses nsjail --user_net (open()s /dev/net/tun).
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - orva-data:/var/lib/orva
      - /sys/fs/cgroup:/sys/fs/cgroup:rw
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://localhost:8443/api/v1/system/health"]
      interval: 30s
      timeout: 5s
      start_period: 15s
      retries: 3
volumes:
  orva-data:
    name: orva-data
EOF
    log "wrote $COMPOSE_DIR/docker-compose.yml (image: ghcr.io/harsh-2002/orva:latest, port: $PORT)"
}

run_docker() {
    [ "$DOCKER_OK" = "1" ] || die "Docker mode requested but Docker/Compose is not usable here"
    assess_runtime
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) would write $COMPOSE_DIR/docker-compose.yml (runtime: ${RUNTIME_FOR_COMPOSE:-docker default}) and run: $COMPOSE_CMD up -d"
        return
    fi
    write_compose
    log "pulling image and starting stack"
    # shellcheck disable=SC2086
    ( cd "$COMPOSE_DIR" && $COMPOSE_CMD pull && $COMPOSE_CMD up -d ) \
        || die "docker compose failed"
    print_followup_docker
}

# ── CLI-only install ─────────────────────────────────────────────────────────
run_cli() {
    if [ "$DRYRUN" = "1" ]; then log "(dryrun) would install CLI → $CLI_INSTALL_PATH"; return; fi
    base="https://github.com/${REPO}/releases/download/${VERSION}"
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM
    log "downloading orva CLI (linux-${ARCH}) → $CLI_INSTALL_PATH"
    if ! fetch "$base/orva-cli-linux-${ARCH}" "$tmp/orva"; then
        warn "orva-cli-linux-${ARCH} missing — falling back to full orva-linux-${ARCH}"
        fetch "$base/orva-linux-${ARCH}" "$tmp/orva" || die "could not download CLI binary"
        rc_asset="orva-linux-${ARCH}"
    else
        rc_asset="orva-cli-linux-${ARCH}"
    fi
    # Fail closed, exactly like the bare-metal path: a missing or flaked
    # checksums.txt must abort, never install an unverified binary.
    fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "failed to download checksums.txt"
    verify "$tmp/orva" "$rc_asset"
    install -d -m 0755 "$(dirname "$CLI_INSTALL_PATH")"
    install -m 0755 "$tmp/orva" "$CLI_INSTALL_PATH"
    log "CLI installed: $("$CLI_INSTALL_PATH" --version 2>/dev/null || echo orva)"
    print_followup_cli
}

# ── Uninstall ────────────────────────────────────────────────────────────────
write_uninstall_script() {
    cat > "$PREFIX/share/orva/scripts/uninstall.sh" <<EOF
#!/bin/sh
set -eu
PREFIX="\${ORVA_PREFIX:-${PREFIX}}"
DATA_DIR="\${ORVA_DATA_DIR:-${DATA_DIR}}"
SERVICE_USER="${SERVICE_USER}"
CLI_PATH="${CLI_INSTALL_PATH}"
PURGE=0
for a in "\$@"; do case "\$a" in --purge) PURGE=1 ;; esac; done
have() { command -v "\$1" >/dev/null 2>&1; }
[ "\$(id -u)" -eq 0 ] || { have sudo && exec sudo sh "\$0" "\$@"; echo "run as root" >&2; exit 1; }
if have systemctl && [ -f /etc/systemd/system/orva.service ]; then
    systemctl disable --now orva 2>/dev/null || true
    rm -f /etc/systemd/system/orva.service; systemctl daemon-reload 2>/dev/null || true
fi
if [ -f /etc/init.d/orva ]; then
    openrc_pid=""
    [ -s /run/orva.pid ] && openrc_pid=\$(cat /run/orva.pid 2>/dev/null || true)
    rc-service orva stop 2>/dev/null || service orva stop 2>/dev/null || true
    # OpenRC can return from stop before the daemon's pid is gone.
    # A rapid uninstall/reinstall would then reuse its stale pidfile and mark
    # the new service started without launching it. Bound the wait, terminate
    # a lingering old process, and clear OpenRC's cached service state.
    if [ -n "\$openrc_pid" ]; then
        openrc_wait=0
        while kill -0 "\$openrc_pid" 2>/dev/null && [ "\$openrc_wait" -lt 30 ]; do
            sleep 1
            openrc_wait=\$((openrc_wait + 1))
        done
        if kill -0 "\$openrc_pid" 2>/dev/null; then
            echo "orva process \$openrc_pid did not stop; terminating it" >&2
            kill -TERM "\$openrc_pid" 2>/dev/null || true
            sleep 2
            kill -KILL "\$openrc_pid" 2>/dev/null || true
        fi
    fi
    rc-service orva zap 2>/dev/null || true
    rm -f /run/orva.pid
    have rc-update && rc-update del orva default 2>/dev/null || true
    rm -f /etc/init.d/orva
fi
rm -rf "\$PREFIX"; rm -f /usr/local/bin/nsjail
# Remove the CLI at \$CLI_PATH whether it is our symlink (possibly dangling
# after \$PREFIX removal) or a regular-file copy left by an older install-cli
# run. -e is false for a dangling symlink, so test -L too.
if [ -e "\$CLI_PATH" ] || [ -L "\$CLI_PATH" ]; then rm -f "\$CLI_PATH"; fi
if [ "\$PURGE" = "1" ]; then
    rm -rf "\$DATA_DIR"
    id -u "\$SERVICE_USER" >/dev/null 2>&1 && { userdel "\$SERVICE_USER" 2>/dev/null || deluser "\$SERVICE_USER" 2>/dev/null || true; }
    echo "uninstalled (data + user purged)"
else
    echo "uninstalled (kept \$DATA_DIR; re-run with --purge to remove)"
fi
EOF
    chmod 0755 "$PREFIX/share/orva/scripts/uninstall.sh"
}

run_uninstall() {
    log "uninstalling Orva"
    un_done=0
    if have docker && docker ps -a --format '{{.Names}}' 2>/dev/null | grep -q '^orva$'; then
        log "removing Docker stack"
        if [ -f "$COMPOSE_DIR/docker-compose.yml" ] && [ -n "${COMPOSE_CMD:-}" ]; then
            if [ "$PURGE" = "1" ]; then
                # shellcheck disable=SC2086
                ( cd "$COMPOSE_DIR" && $COMPOSE_CMD down --volumes ) || true
            else
                # shellcheck disable=SC2086
                ( cd "$COMPOSE_DIR" && $COMPOSE_CMD down ) || true
            fi
        else
            docker rm -f orva 2>/dev/null || true
            if [ "$PURGE" = "1" ]; then
                docker volume rm orva-data 2>/dev/null || true
            fi
        fi
        un_done=1
    fi
    if [ -f "$PREFIX/share/orva/scripts/uninstall.sh" ]; then
        if [ "$PURGE" = "1" ]; then
            sh "$PREFIX/share/orva/scripts/uninstall.sh" --purge
        else
            sh "$PREFIX/share/orva/scripts/uninstall.sh"
        fi; un_done=1
    fi
    [ "$un_done" = "1" ] || warn "no Orva install found"
}

# ── Follow-up messages ───────────────────────────────────────────────────────
print_followup_bare() {
    cat <<EOF

══════════════════════════════════════════════════════════════════════
  Orva $VERSION installed (bare-metal) → $PREFIX
══════════════════════════════════════════════════════════════════════
  Data:            $DATA_DIR
  Binary / CLI:    $PREFIX/bin/orva   (also on PATH: $CLI_INSTALL_PATH)
  nsjail:          $PREFIX/bin/nsjail
  Sandbox egress:  $TUN_STATUS${TUN_HINT:+ ($TUN_HINT)}

  Start / status:
    systemctl enable --now orva            # systemd
    rc-update add orva default && service orva start   # OpenRC
  Admin key (first boot): cat $DATA_DIR/.admin-key
  Then open http://<host>:$PORT  (front with TLS before exposing publicly).
  Uninstall: sh $PREFIX/share/orva/scripts/uninstall.sh
EOF
}

print_followup_docker() {
    cat <<EOF

══════════════════════════════════════════════════════════════════════
  Orva $VERSION running (Docker Compose) — $COMPOSE_DIR/docker-compose.yml
══════════════════════════════════════════════════════════════════════
  Open:    http://localhost:$PORT
  Logs:    (cd $COMPOSE_DIR && $COMPOSE_CMD logs -f)
  Stop:    (cd $COMPOSE_DIR && $COMPOSE_CMD down)
  Update:  re-run this installer, or:
           (cd $COMPOSE_DIR && $COMPOSE_CMD pull && $COMPOSE_CMD up -d)
  Admin key (first boot): $COMPOSE_CMD logs orva | grep -A1 BOOTSTRAP
EOF
}

print_followup_cli() {
    cat <<EOF

══════════════════════════════════════════════════════════════════════
  Orva CLI $VERSION → $CLI_INSTALL_PATH
══════════════════════════════════════════════════════════════════════
  orva login --endpoint https://orva.example.com --api-key <key>
  orva functions list
  Config: ~/.orva/config.yaml (mode 0600)
EOF
}

# ── Main ─────────────────────────────────────────────────────────────────────
main() {
    parse_args "$@"
    resolve_self_url   # derive SELF_URL from REPO now that --repo is parsed
    bootstrap "$@"

    if [ "$DO_UNINSTALL" = "1" ]; then
        detect_docker
        run_uninstall
        exit 0
    fi

    detect_distro
    detect_arch
    detect_docker
    decide_interactive
    detect_existing
    choose_mode
    ensure_downloader
    resolve_version

    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM

    case "$MODE" in
        docker)     run_docker ;;
        bare-metal) run_bare_metal ;;
        cli)        run_cli ;;
        *)          die "no install mode selected" ;;
    esac
}

main "$@"
