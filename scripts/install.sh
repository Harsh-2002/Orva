#!/bin/sh
# Orva bare-metal installer. POSIX sh — works on Ubuntu, Debian, Alpine,
# RHEL/Rocky/AlmaLinux, Fedora, Arch, openSUSE.
#
# Usage:
#   curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sh
#   ORVA_VERSION=v2026.04.28 sh install.sh        # pin version
#   ORVA_INSTALL_DRYRUN=1 sh install.sh           # parse + detect only
#   ORVA_NO_PKG=1 sh install.sh                   # skip system pkg install
#
# Idempotent: re-running overwrites the binary, preserves the data dir.

set -eu

PREFIX="${ORVA_PREFIX:-/opt/orva}"
DATA_DIR="${ORVA_DATA_DIR:-/var/lib/orva}"
SERVICE_USER="orva"
REPO="${ORVA_REPO:-Harsh-2002/Orva}"
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

    # Required packages — install must succeed for orva to run at all.
    pkgs="ca-certificates curl tar zstd"
    # Optional — needed only by the egress firewall feature. If install
    # fails or the host can't load nf_tables we degrade gracefully and
    # the operator still gets a working orvad with the firewall feature
    # disabled (UI surfaces a banner; manage-mode rules still persist).
    optional_pkgs="nftables"

    log "installing prerequisites: $pkgs"
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) skipping package install"
        return
    fi
    case "$DISTRO_ID" in
        ubuntu|debian)
            DEBIAN_FRONTEND=noninteractive apt-get update -qq
            DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $pkgs
            DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $optional_pkgs \
                || warn "optional package install failed: $optional_pkgs (egress firewall will be disabled)"
            ;;
        alpine)
            apk add --no-cache $pkgs
            apk add --no-cache $optional_pkgs \
                || warn "optional package install failed: $optional_pkgs (egress firewall will be disabled)"
            ;;
        fedora|rhel|centos|rocky|almalinux|amzn)
            # RHEL 9 / Rocky 9 ship `curl-minimal` which conflicts with the
            # full `curl` package. --allowerasing lets dnf swap them.
            if have dnf; then
                dnf install -y --allowerasing --setopt=install_weak_deps=False $pkgs
                dnf install -y --allowerasing --setopt=install_weak_deps=False $optional_pkgs \
                    || warn "optional package install failed: $optional_pkgs (egress firewall will be disabled)"
            else
                yum install -y --allowerasing $pkgs
                yum install -y --allowerasing $optional_pkgs \
                    || warn "optional package install failed: $optional_pkgs (egress firewall will be disabled)"
            fi
            ;;
        arch|manjaro|endeavouros)
            pacman -Syu --noconfirm --needed $pkgs
            pacman -S --noconfirm --needed $optional_pkgs \
                || warn "optional package install failed: $optional_pkgs (egress firewall will be disabled)"
            ;;
        opensuse-leap|opensuse-tumbleweed|sles)
            zypper --non-interactive install $pkgs
            zypper --non-interactive install $optional_pkgs \
                || warn "optional package install failed: $optional_pkgs (egress firewall will be disabled)"
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

# Result of check_nftables(), consumed by print_followup() to render an
# "egress firewall: enabled / disabled" line in the next-steps block.
NFTABLES_STATUS="unknown"
NFTABLES_HINT=""

check_nftables() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) skipping nftables probe"
        NFTABLES_STATUS="dryrun"
        return
    fi

    if ! have nft; then
        NFTABLES_STATUS="disabled"
        NFTABLES_HINT="install the 'nftables' package — egress filtering is off without it"
        warn "nft binary not found — egress firewall will be disabled"
        warn "  install nftables manually if you want per-function egress filtering"
        return
    fi

    # Probe: can we actually list tables? Fails when nf_tables module
    # isn't loaded (rare modern distros) or we lack CAP_NET_ADMIN.
    if ! nft list tables >/dev/null 2>&1; then
        NFTABLES_STATUS="disabled"
        NFTABLES_HINT="nft installed but kernel cannot apply rules — try 'modprobe nf_tables' or run as root"
        warn "nft list tables failed — kernel module missing or insufficient privileges"
        warn "  try: sudo modprobe nf_tables"
        warn "  egress firewall will be disabled until this is resolved"
        return
    fi

    # Already-present orva table from a previous install: orvad rebuilds
    # it on boot from the SQLite source of truth, so this is informational.
    if nft list table inet orva_firewall >/dev/null 2>&1; then
        log "found existing 'inet orva_firewall' table — orvad will rebuild on next start"
    fi

    # Detect operator-managed tables. We never touch them; we only own
    # 'inet orva_firewall'. This is purely a friendliness signal.
    other_count=$(nft list tables 2>/dev/null \
        | grep -v '^table inet orva_firewall$' \
        | grep -c '^table ' \
        || true)
    if [ "${other_count:-0}" -gt 0 ]; then
        log "existing nftables config detected ($other_count other tables) — orvad will only manage 'inet orva_firewall' and leave others alone"
        log "  inspect with:  nft list tables"
    fi

    NFTABLES_STATUS="enabled"
    log "nftables OK — egress firewall will be enforced"
}

check_kernel_features() {
    log "checking kernel features"
    missing=""

    # Unprivileged user namespaces — required for nsjail to construct
    # isolated namespaces without giving orvad CAP_SYS_ADMIN on the host.
    if [ -r /proc/sys/kernel/unprivileged_userns_clone ]; then
        if [ "$(cat /proc/sys/kernel/unprivileged_userns_clone)" != "1" ]; then
            warn "unprivileged user namespaces are DISABLED"
            warn "  enable now:    echo 1 > /proc/sys/kernel/unprivileged_userns_clone"
            warn "  persist:       echo 'kernel.unprivileged_userns_clone = 1' > /etc/sysctl.d/99-orva.conf"
            missing="${missing}userns "
        fi
    fi

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
        VERSION=$(curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 \
            "https://api.github.com/repos/${REPO}/releases/latest" \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
        [ -n "$VERSION" ] || die "could not resolve latest tag (set ORVA_VERSION explicitly)"
    fi
    log "version: $VERSION"
}

download_and_install_binaries() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) skipping binary download"
        return
    fi
    base="https://github.com/${REPO}/releases/download/${VERSION}"
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT INT TERM

    log "downloading orva + nsjail (linux-${ARCH})"
    curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 -o "$tmp/orva"   "$base/orva-linux-${ARCH}"
    curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 -o "$tmp/nsjail" "$base/nsjail-linux-${ARCH}"
    curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 -o "$tmp/checksums.txt" "$base/checksums.txt"

    log "verifying SHA-256"
    ( cd "$tmp" && \
      grep " orva-linux-${ARCH}\$"   checksums.txt | sed "s/orva-linux-${ARCH}/orva/"     | sha256sum -c - && \
      grep " nsjail-linux-${ARCH}\$" checksums.txt | sed "s/nsjail-linux-${ARCH}/nsjail/" | sha256sum -c - \
    ) || die "checksum verification failed"

    log "installing binaries to $PREFIX/bin"
    install -d -m 0755 "$PREFIX/bin" "$PREFIX/share/orva/scripts" "$PREFIX/share/orva/runtimes"
    install -m 0755 "$tmp/orva"   "$PREFIX/bin/orva"
    install -m 0755 "$tmp/nsjail" "$PREFIX/bin/nsjail"
}

download_rootfs() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) skipping rootfs download"
        return
    fi
    base="https://github.com/${REPO}/releases/download/${VERSION}"
    install -d -m 0755 "$DATA_DIR/rootfs"

    for rt in node22 node24 python313 python314; do
        target="$DATA_DIR/rootfs/$rt"
        if [ -f "$target/.orva-rootfs-version" ] && \
           [ "$(cat "$target/.orva-rootfs-version" 2>/dev/null)" = "$VERSION" ]; then
            log "rootfs/$rt already at $VERSION (skipping)"
            continue
        fi
        log "downloading rootfs/$rt for $ARCH"
        tar_name="rootfs-${rt}-${ARCH}.tar.zst"
        if ! curl -fsSL -o "$tmp/$tar_name" "$base/$tar_name"; then
            warn "rootfs $rt for $ARCH not present in this release — runtime $rt will be unavailable"
            continue
        fi
        rm -rf "$target"
        install -d -m 0755 "$target"
        zstd -dc "$tmp/$tar_name" | tar -C "$target" -xf -
        printf '%s\n' "$VERSION" > "$target/.orva-rootfs-version"
    done
}

install_runtime_assets() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) skipping runtime asset install"
        return
    fi
    # The systemd unit + uninstall script ship in the release tarball
    # alongside the binary. They're tiny — embed inline so install.sh
    # has no extra files to download.
    cat > "$PREFIX/share/orva/scripts/orva.service" <<'EOF'
[Unit]
Description=Orva self-hosted serverless platform
Documentation=https://github.com/Harsh-2002/Orva
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=orva
Group=orva
ExecStart=/opt/orva/bin/orva serve
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s
KillSignal=SIGTERM
AmbientCapabilities=CAP_SYS_ADMIN
CapabilityBoundingSet=CAP_SYS_ADMIN
NoNewPrivileges=false
ReadWritePaths=/var/lib/orva
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
LimitNOFILE=65536
LimitNPROC=8192
Environment=ORVA_DATA_DIR=/var/lib/orva

[Install]
WantedBy=multi-user.target
EOF

    cat > "$PREFIX/share/orva/scripts/orva.openrc" <<'EOF'
#!/sbin/openrc-run
description="Orva self-hosted serverless platform"
command="/opt/orva/bin/orva"
command_args="serve"
command_user="orva:orva"
command_background="yes"
pidfile="/run/orva.pid"
output_log="/var/log/orva.log"
error_log="/var/log/orva.log"

depend() {
    need net
    after firewall
}

start_pre() {
    checkpath -d -m 0755 -o orva:orva /var/lib/orva
    checkpath -d -m 0755 -o orva:orva /var/lib/orva/functions
    checkpath -d -m 0755 -o orva:orva /var/lib/orva/rootfs
    checkpath -f -m 0644 -o orva:orva /var/log/orva.log
}
EOF
    chmod 0755 "$PREFIX/share/orva/scripts/orva.openrc"

    cat > "$PREFIX/share/orva/scripts/uninstall.sh" <<'EOF'
#!/bin/sh
set -eu
PREFIX="${ORVA_PREFIX:-/opt/orva}"
DATA_DIR="${ORVA_DATA_DIR:-/var/lib/orva}"
SERVICE_USER="orva"
PURGE=0
for a in "$@"; do
    case "$a" in --purge) PURGE=1 ;; esac
done
have() { command -v "$1" >/dev/null 2>&1; }

if [ "$(id -u)" -ne 0 ]; then
    if have sudo; then exec sudo sh "$0" "$@"
    else echo "must run as root" >&2; exit 1; fi
fi

if have systemctl && [ -f /etc/systemd/system/orva.service ]; then
    systemctl stop orva 2>/dev/null || true
    systemctl disable orva 2>/dev/null || true
    rm -f /etc/systemd/system/orva.service
    systemctl daemon-reload 2>/dev/null || true
fi
if [ -f /etc/init.d/orva ]; then
    /etc/init.d/orva stop 2>/dev/null || true
    have rc-update && rc-update del orva default 2>/dev/null || true
    rm -f /etc/init.d/orva
fi

rm -rf "$PREFIX"

if [ "$PURGE" = "1" ]; then
    rm -rf "$DATA_DIR"
    if id -u "$SERVICE_USER" >/dev/null 2>&1; then
        userdel "$SERVICE_USER" 2>/dev/null || deluser "$SERVICE_USER" 2>/dev/null || true
    fi
    echo "uninstalled (data + user purged)"
else
    echo "uninstalled (preserved $DATA_DIR; re-run with --purge to remove)"
fi
EOF
    chmod 0755 "$PREFIX/share/orva/scripts/uninstall.sh"
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
            # On Linux distros that have `useradd`, the matching group is
            # auto-created by default (unless USERGROUPS_ENAB=no in
            # /etc/login.defs).
            useradd --system --no-create-home --shell /sbin/nologin "$SERVICE_USER" 2>/dev/null || \
              useradd -r -s /bin/false "$SERVICE_USER"
        elif have adduser; then
            # Alpine / BusyBox: `adduser -S` doesn't create a same-named
            # group — it puts the user in `nogroup`. Create the group
            # first so `install -g orva` works downstream.
            have addgroup && addgroup -S "$SERVICE_USER" 2>/dev/null || true
            adduser -S -D -H -s /sbin/nologin -G "$SERVICE_USER" "$SERVICE_USER"
        else
            warn "no useradd/adduser found — create user '$SERVICE_USER' manually"
        fi
    fi

    # Determine the real primary group (handles distros where `useradd`
    # doesn't create a same-named group).
    primary_group=$(id -gn "$SERVICE_USER" 2>/dev/null || echo "$SERVICE_USER")
    install -d -o "$SERVICE_USER" -g "$primary_group" \
        "$DATA_DIR" "$DATA_DIR/functions" "$DATA_DIR/rootfs"
}

install_service() {
    if [ "$DRYRUN" = "1" ]; then
        log "(dryrun) would install service unit"
        return
    fi
    if [ -d /run/systemd/system ] || (have systemctl && [ -d /etc/systemd/system ]); then
        log "installing systemd unit"
        install -m 0644 "$PREFIX/share/orva/scripts/orva.service" /etc/systemd/system/orva.service
        if [ -d /run/systemd/system ]; then
            systemctl daemon-reload
            log "  enable + start with: systemctl enable --now orva"
        else
            log "  installed unit file (this container has no systemd PID 1; enable on the real host)"
        fi
    elif [ -d /etc/init.d ] && have rc-update; then
        log "installing OpenRC unit"
        install -m 0755 "$PREFIX/share/orva/scripts/orva.openrc" /etc/init.d/orva
        log "  enable + start with: rc-update add orva default && service orva start"
    else
        warn "no service manager detected — start orva manually:"
        warn "  ORVA_DATA_DIR=$DATA_DIR $PREFIX/bin/orva serve"
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
  Egress firewall: $NFTABLES_STATUS${NFTABLES_HINT:+ ($NFTABLES_HINT)}

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

EOF
    if [ "$NFTABLES_STATUS" = "enabled" ]; then
        cat <<EOF
  Egress firewall is active. orvad manages 'inet orva_firewall' only
  and leaves any other nftables tables alone. Manage rules from the UI
  at /web/firewall, or curl /api/v1/firewall/rules. Inspect live state:
    sudo nft list table inet orva_firewall

EOF
    elif [ "$NFTABLES_STATUS" = "disabled" ]; then
        cat <<EOF
  Egress firewall is DISABLED on this host.
    $NFTABLES_HINT
  Per-function 'network_mode: egress' still works (sandbox isolation
  only). Re-run the installer once nftables is fixed to apply rules.

EOF
    fi
    cat <<EOF
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
    check_nftables
    resolve_version
    download_and_install_binaries
    install_runtime_assets
    create_user
    download_rootfs
    install_service
    print_followup
}

main "$@"
