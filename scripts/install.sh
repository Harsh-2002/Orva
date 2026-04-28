#!/bin/sh
# Orva bare-metal installer. POSIX sh — works on Ubuntu, Debian, Alpine,
# RHEL/Rocky/AlmaLinux, Fedora, Arch, openSUSE.
#
# Usage:
#   curl -fsSL https://github.com/Harsh-2002/orva/releases/latest/download/install.sh | sh
#   ORVA_VERSION=v2026.04.28 sh install.sh        # pin version
#   ORVA_INSTALL_DRYRUN=1 sh install.sh           # parse + detect only
#   ORVA_NO_PKG=1 sh install.sh                   # skip system pkg install
#
# Idempotent: re-running overwrites the binary, preserves the data dir.

set -eu

PREFIX="${ORVA_PREFIX:-/opt/orva}"
DATA_DIR="${ORVA_DATA_DIR:-/var/lib/orva}"
SERVICE_USER="orva"
REPO="${ORVA_REPO:-Harsh-2002/orva}"
DRYRUN="${ORVA_INSTALL_DRYRUN:-0}"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxxx\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

require_root() {
    if [ "$(id -u)" -ne 0 ] && [ "$DRYRUN" != "1" ]; then
        if have sudo; then
            warn "re-execing under sudo"
            exec sudo -E sh "$0" "$@"
        elif have doas; then
            warn "re-execing under doas"
            exec doas sh "$0" "$@"
        else
            die "must run as root (no sudo/doas available)"
        fi
    fi
}

detect_distro() {
    [ -r /etc/os-release ] || die "/etc/os-release missing; cannot detect distro"
    # shellcheck disable=SC1091
    . /etc/os-release
    DISTRO_ID="${ID:-unknown}"
    DISTRO_LIKE="${ID_LIKE:-}"
    DISTRO_PRETTY="${PRETTY_NAME:-$DISTRO_ID}"
    log "detected: $DISTRO_PRETTY"
}

detect_arch() {
    raw=$(uname -m)
    case "$raw" in
        x86_64|amd64)  ARCH=amd64 ;;
        aarch64|arm64) ARCH=arm64 ;;
        *) die "unsupported architecture: $raw (released: amd64, arm64)" ;;
    esac
    log "architecture: $ARCH"
}

install_prereqs() {
    if [ "${ORVA_NO_PKG:-}" = "1" ]; then
        log "skipping package install (ORVA_NO_PKG=1)"
        return
    fi
    pkgs="ca-certificates curl tar"
    log "installing prerequisites: $pkgs"
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) skipping package install"
        return
    fi
    case "$DISTRO_ID" in
        ubuntu|debian)
            DEBIAN_FRONTEND=noninteractive apt-get update -qq
            DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $pkgs
            ;;
        alpine)
            apk add --no-cache $pkgs
            ;;
        fedora|rhel|centos|rocky|almalinux|amzn)
            if have dnf; then
                dnf install -y --setopt=install_weak_deps=False $pkgs
            else
                yum install -y $pkgs
            fi
            ;;
        arch|manjaro|endeavouros)
            pacman -Syu --noconfirm --needed $pkgs
            ;;
        opensuse-leap|opensuse-tumbleweed|sles)
            zypper --non-interactive install $pkgs
            ;;
        *)
            case "$DISTRO_LIKE" in
                *debian*|*ubuntu*) DEBIAN_FRONTEND=noninteractive apt-get update -qq && \
                    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $pkgs ;;
                *rhel*|*fedora*)   dnf install -y $pkgs ;;
                *arch*)            pacman -Syu --noconfirm --needed $pkgs ;;
                *suse*)            zypper --non-interactive install $pkgs ;;
                *) warn "unknown distro $DISTRO_ID — install $pkgs manually" ;;
            esac
            ;;
    esac
}

check_kernel_features() {
    log "checking kernel features"
    missing=""

    # Unprivileged user namespaces — required for nsjail to construct
    # isolated namespaces without giving the orvad process CAP_SYS_ADMIN
    # on the host. Most distros enable this; RHEL <9 disables it.
    if [ -r /proc/sys/kernel/unprivileged_userns_clone ]; then
        if [ "$(cat /proc/sys/kernel/unprivileged_userns_clone)" != "1" ]; then
            warn "unprivileged user namespaces are DISABLED"
            warn "  enable now:    echo 1 > /proc/sys/kernel/unprivileged_userns_clone"
            warn "  persist:       echo 'kernel.unprivileged_userns_clone = 1' > /etc/sysctl.d/99-orva.conf"
            missing="${missing}userns "
        fi
    fi

    # cgroup v2 — needed for memory.max + cpu.max enforcement.
    if ! grep -q "cgroup2" /proc/mounts 2>/dev/null; then
        warn "cgroup v2 not detected — per-function resource limits will be best-effort"
        warn "  RHEL/Rocky 8: grubby --update-kernel=ALL --args=systemd.unified_cgroup_hierarchy=1 + reboot"
        missing="${missing}cgroupv2 "
    fi

    if [ -n "$missing" ]; then
        warn "kernel features missing: $missing — orva will install but isolation may be partial"
    else
        log "kernel features: OK"
    fi
}

resolve_version() {
    if [ -n "${ORVA_VERSION:-}" ]; then
        VERSION="$ORVA_VERSION"
    elif [ "$DRYRUN" = "1" ]; then
        VERSION="v0.0.0-dryrun"
    else
        log "fetching latest release tag from GitHub"
        VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
        [ -n "$VERSION" ] || die "could not resolve latest tag (set ORVA_VERSION explicitly)"
    fi
    log "version: $VERSION"
}

download_and_verify() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) skipping tarball download + extract"
        return
    fi
    base="https://github.com/${REPO}/releases/download/${VERSION}"
    tar_name="orva-${VERSION}-linux-${ARCH}.tar.gz"
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT INT TERM

    log "downloading $tar_name"
    curl -fsSL -o "$tmp/$tar_name" "$base/$tar_name"
    curl -fsSL -o "$tmp/SHA256SUMS"  "$base/SHA256SUMS"

    log "verifying SHA-256"
    ( cd "$tmp" && grep " $tar_name\$" SHA256SUMS | sha256sum -c - ) \
        || die "checksum mismatch for $tar_name"

    log "extracting to $PREFIX"
    mkdir -p "$PREFIX"
    tar -C "$PREFIX" -xzf "$tmp/$tar_name" --strip-components=1
    chmod +x "$PREFIX/bin/"*
}

create_user() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) would create system user $SERVICE_USER + data dir $DATA_DIR"
        return
    fi
    if id -u "$SERVICE_USER" >/dev/null 2>&1; then
        log "user $SERVICE_USER already exists"
    else
        log "creating system user $SERVICE_USER"
        if have useradd; then
            useradd --system --no-create-home --shell /sbin/nologin "$SERVICE_USER" 2>/dev/null || \
              useradd -r -s /bin/false "$SERVICE_USER"
        elif have adduser; then
            # Alpine busybox adduser
            adduser -S -D -H -s /sbin/nologin "$SERVICE_USER"
        else
            warn "no useradd/adduser found — create user '$SERVICE_USER' manually"
        fi
    fi
    install -d -o "$SERVICE_USER" -g "$SERVICE_USER" \
        "$DATA_DIR" "$DATA_DIR/functions" "$DATA_DIR/rootfs"
}

install_service() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) would install service unit"
        return
    fi
    if [ -d /run/systemd/system ] || have systemctl; then
        log "installing systemd unit"
        install -m 0644 "$PREFIX/share/orva/scripts/orva.service" /etc/systemd/system/orva.service
        systemctl daemon-reload
        log "  enable + start with: systemctl enable --now orva"
    elif [ -d /etc/init.d ] && have rc-update; then
        log "installing OpenRC unit"
        install -m 0755 "$PREFIX/share/orva/scripts/orva.openrc" /etc/init.d/orva
        log "  enable + start with: rc-update add orva default && service orva start"
    else
        warn "no service manager detected — start orva manually:"
        warn "  $PREFIX/bin/orva serve --data-dir $DATA_DIR"
    fi
}

print_followup() {
    cat <<EOF

══════════════════════════════════════════════════════════════════════
  orva $VERSION installed to $PREFIX
══════════════════════════════════════════════════════════════════════

  Data directory:  $DATA_DIR
  Service user:    $SERVICE_USER
  Binary:          $PREFIX/bin/orva
  nsjail:          $PREFIX/bin/nsjail (static, no glibc/libstdc++ deps)

  Next:
    1. Start the service (if installed):
         systemctl enable --now orva           # systemd hosts
         rc-update add orva default && service orva start    # OpenRC (Alpine)
       or run in foreground:
         $PREFIX/bin/orva serve

    2. Read the bootstrap admin key (printed once at startup):
         journalctl -u orva | grep -A1 BOOTSTRAP    # systemd
         cat $DATA_DIR/.admin-key                   # always available after first boot

    3. Open http://<host>:8443 — onboard admin user, deploy first function.

  Front the service with TLS (caddy / nginx / traefik) before exposing
  it publicly: the UI's clipboard buttons require HTTPS or localhost.

  Uninstall:
    sh $PREFIX/share/orva/scripts/uninstall.sh

EOF
}

main() {
    require_root "$@"
    detect_distro
    detect_arch
    install_prereqs
    check_kernel_features
    resolve_version
    download_and_verify
    create_user
    install_service
    print_followup
}

main "$@"
