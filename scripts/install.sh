#!/bin/sh
# install.sh — POSIX-compliant installer for self-hosted Orva.
#
#   curl -fsSL https://orva.dev/install.sh | sh
#
# Works on Debian/Ubuntu, Fedora/RHEL/CentOS/Alma/Rocky, Alpine, Arch/Manjaro,
# openSUSE/SLES. Installs:
#   - the orva binary at /usr/local/bin/orva (or ~/.local/bin/orva)
#   - nsjail at /usr/local/bin/nsjail (downloaded prebuilt; fallback: build)
#   - four language rootfs trees at ${ORVA_DATA_DIR:-~/.orva}/rootfs/*
#   - the data dir layout and .setup-complete marker
#
# Env vars:
#   ORVA_VERSION    pin a specific release (default: latest)
#   ORVA_PREFIX     binary install dir (default: /usr/local/bin or ~/.local/bin)
#   ORVA_DATA_DIR   data dir (default: ~/.orva)
#   ORVA_NO_PKG     skip system-package install (assume deps already present)
#   ORVA_NO_SETCAP  skip setcap (use this when running inside a container
#                   that already grants caps via --cap-add)

set -eu

# ── helpers ─────────────────────────────────────────────────────────────

msg()  { printf '==> %s\n' "$*"; }
warn() { printf 'warn: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

fetch() {
    # fetch URL DEST — curl-first, wget fallback.
    _u="$1"; _d="$2"
    if have curl; then
        curl -fsSL "$_u" -o "$_d"
    elif have wget; then
        wget -qO "$_d" "$_u"
    else
        die "need curl or wget to download $_u"
    fi
}

# ── sanity ──────────────────────────────────────────────────────────────

os=$(uname -s | tr 'A-Z' 'a-z')
[ "$os" = "linux" ] || die "Orva currently supports Linux only. Use Docker on macOS/Windows: ghcr.io/harsh-2002/orva:latest"

arch=$(uname -m)
case "$arch" in
    x86_64|amd64)   arch=amd64 ;;
    aarch64|arm64)  arch=arm64 ;;
    *)              die "unsupported architecture: $arch (need x86_64 or aarch64)" ;;
esac

# Distro ID via /etc/os-release — standard on every supported distro.
distro=unknown
distro_like=""
if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    distro="${ID:-unknown}"
    distro_like="${ID_LIKE:-}"
fi
msg "host: $distro ($arch) on Linux"

# ── root / sudo ─────────────────────────────────────────────────────────

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
    if have sudo; then
        SUDO="sudo"
    elif have doas; then
        SUDO="doas"
    else
        die "this installer needs root (or sudo/doas) for package install + setcap"
    fi
fi

# ── system packages per distro ──────────────────────────────────────────

install_pkgs() {
    # Base runtime deps. The package names differ per distro but they all
    # provide: curl, tar, zstd (for rootfs), setcap (libcap2-bin), and
    # the shared libs nsjail links against (libprotobuf, libnl-route-3).
    case "${distro}:${distro_like}" in
        ubuntu:*|debian:*|*:*debian*)
            $SUDO apt-get update -y
            $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
                ca-certificates curl tar zstd libprotobuf-dev libnl-route-3-200 libcap2-bin \
                2>/dev/null || \
            $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
                ca-certificates curl tar zstd libprotobuf32 libnl-route-3-200 libcap2-bin
            ;;
        fedora:*|rhel:*|centos:*|almalinux:*|rocky:*|amzn:*|*fedora*|*rhel*)
            if have dnf; then
                $SUDO dnf install -y ca-certificates curl tar zstd protobuf libnl3 libcap
            else
                $SUDO yum install -y ca-certificates curl tar zstd protobuf libnl3 libcap
            fi
            ;;
        alpine:*)
            $SUDO apk add --no-cache ca-certificates curl tar zstd protobuf-dev libnl3 libcap
            ;;
        arch:*|manjaro:*|*arch*)
            $SUDO pacman -Sy --noconfirm --needed ca-certificates curl tar zstd protobuf libnl libcap
            ;;
        opensuse*:*|sles:*|*suse*)
            $SUDO zypper --non-interactive install ca-certificates curl tar zstd libprotobuf-lite libnl3-200 libcap2 libcap-progs
            ;;
        *)
            warn "unknown distro '$distro'; install manually:"
            warn "  ca-certificates curl tar zstd libprotobuf libnl3 libcap (and libcap setcap tool)"
            ;;
    esac
}

if [ "${ORVA_NO_PKG:-}" = "1" ]; then
    msg "skipping system package install (ORVA_NO_PKG=1)"
else
    msg "installing system packages via $distro package manager"
    install_pkgs
fi

# ── unprivileged user namespaces ────────────────────────────────────────
# nsjail needs user namespaces to work without full root. Most modern
# kernels enable them; Debian stable gates them behind a sysctl.
if [ -r /proc/sys/kernel/unprivileged_userns_clone ]; then
    if [ "$(cat /proc/sys/kernel/unprivileged_userns_clone)" = "0" ]; then
        warn "unprivileged user namespaces are disabled"
        warn "enable with: echo 1 | sudo tee /proc/sys/kernel/unprivileged_userns_clone"
        warn "(persist: add 'kernel.unprivileged_userns_clone=1' to /etc/sysctl.d/orva.conf)"
    fi
fi

# ── install directories ────────────────────────────────────────────────

version="${ORVA_VERSION:-latest}"
data_dir="${ORVA_DATA_DIR:-$HOME/.orva}"
prefix="${ORVA_PREFIX:-}"

if [ -z "$prefix" ]; then
    if [ "$(id -u)" -eq 0 ] || [ -w /usr/local/bin ]; then
        prefix=/usr/local/bin
    else
        prefix="$HOME/.local/bin"
        mkdir -p "$prefix"
        case ":${PATH:-}:" in
            *":$prefix:"*) ;;
            *) warn "$prefix is not on \$PATH — add it to your shell rc" ;;
        esac
    fi
fi

mkdir -p "$data_dir/functions" "$data_dir/rootfs"

# Resolve release base URL.
if [ "$version" = "latest" ]; then
    base="https://github.com/Harsh-2002/Orva/releases/latest/download"
else
    base="https://github.com/Harsh-2002/Orva/releases/download/$version"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

# ── download orva binary ────────────────────────────────────────────────

msg "downloading orva binary"
fetch "$base/orva-linux-$arch" "$tmp/orva"
chmod +x "$tmp/orva"
$SUDO install -m 0755 "$tmp/orva" "$prefix/orva"
msg "installed $prefix/orva"

# ── download nsjail binary (prebuilt, static) ───────────────────────────

nsjail_dst=/usr/local/bin/nsjail
if [ -x "$nsjail_dst" ]; then
    msg "nsjail present at $nsjail_dst"
else
    msg "downloading nsjail"
    if fetch "$base/nsjail-linux-$arch" "$tmp/nsjail" 2>/dev/null; then
        chmod +x "$tmp/nsjail"
        $SUDO install -m 0755 "$tmp/nsjail" "$nsjail_dst"
        msg "installed $nsjail_dst"
    else
        warn "prebuilt nsjail not available for $arch; install manually from https://github.com/google/nsjail"
        warn "or use the Docker image (ghcr.io/harsh-2002/orva:latest) which bundles nsjail."
    fi
fi

# ── grant nsjail capabilities so it doesn't need root at runtime ────────

if [ "${ORVA_NO_SETCAP:-}" = "1" ]; then
    msg "skipping setcap (ORVA_NO_SETCAP=1)"
elif have setcap && [ -x "$nsjail_dst" ]; then
    msg "applying setcap on $nsjail_dst"
    $SUDO setcap 'cap_sys_admin,cap_setuid,cap_setgid=eip' "$nsjail_dst" || \
        warn "setcap failed (Docker without cap_setfcap); running with --cap-add SYS_ADMIN at runtime instead"
fi

# ── run orva setup to fetch rootfs tarballs + install adapters ──────────

msg "running orva setup (this fetches the language rootfs tarballs)"
"$prefix/orva" setup \
    --data-dir "$data_dir" \
    --skip-nsjail \
    --rootfs-url "$base"

# ── done ────────────────────────────────────────────────────────────────

cat <<EOF

────────────────────────────────────────────────────────────────
  Orva installed.

  Start the server:
    $prefix/orva serve --data-dir $data_dir

  On first run an admin API key is printed — copy it.
  Then open http://localhost:8443 in your browser.

  Uninstall:
    rm -rf $data_dir $prefix/orva $nsjail_dst

  Docs: https://github.com/Harsh-2002/Orva
────────────────────────────────────────────────────────────────
EOF
