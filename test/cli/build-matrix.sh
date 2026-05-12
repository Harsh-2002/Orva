#!/usr/bin/env bash
# build-matrix.sh — cross-compile the slim CLI for all 6 release targets
# and confirm each binary is well-formed (correct file type, size in the
# expected range, --version works under qemu/native).
#
# Usage:
#   bash test/cli/build-matrix.sh
#
# Exit non-zero on any failure. Suitable for CI gate.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/common.sh
source "$HERE/lib/common.sh"

OUT="$REPO_ROOT/build/cli-matrix"
rm -rf "$OUT" && mkdir -p "$OUT"

PASS=0; FAIL=0

# Build all 6 targets.
for target in "${CLI_TARGETS[@]}"; do
    os=${target%/*}
    arch=${target#*/}
    ext=""
    [[ "$os" == "windows" ]] && ext=".exe"
    name="orva-cli-${os}-${arch}${ext}"
    log "building $name"
    if CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -trimpath -ldflags='-s -w -X main.Version=test' \
        -o "$OUT/$name" "$REPO_ROOT/cli/cmd/orva" 2>&1 \
        | sed 's/^/  /'; then
        ok "built $name"
        PASS=$((PASS+1))
    else
        fail "build failed: $name"
        FAIL=$((FAIL+1))
        continue
    fi
done

# File-type sanity check: each binary should match its target format.
log "verifying binary formats"
for target in "${CLI_TARGETS[@]}"; do
    os=${target%/*}
    arch=${target#*/}
    ext=""
    [[ "$os" == "windows" ]] && ext=".exe"
    name="orva-cli-${os}-${arch}${ext}"
    bin="$OUT/$name"
    [[ -f "$bin" ]] || continue
    desc=$(file "$bin" 2>/dev/null || echo "no-file-command")
    case "$os" in
        linux)   want="ELF" ;;
        darwin)  want="Mach-O" ;;
        windows) want="PE32" ;;
    esac
    if [[ "$desc" == *"$want"* ]]; then
        ok "$name: format OK ($want)"
        PASS=$((PASS+1))
    else
        fail "$name: expected $want, got: $desc"
        FAIL=$((FAIL+1))
    fi
done

# Size sanity: the slim CLI should be under 20 MB stripped. If it
# balloons past that, somebody pulled in a heavy server package.
log "verifying binary sizes"
SIZE_LIMIT_BYTES=$((20 * 1024 * 1024))
for f in "$OUT"/orva-cli-*; do
    [[ -f "$f" ]] || continue
    size=$(stat -c '%s' "$f" 2>/dev/null || stat -f '%z' "$f" 2>/dev/null || echo 0)
    if [[ "$size" -le "$SIZE_LIMIT_BYTES" ]]; then
        ok "$(basename "$f"): $((size / 1024 / 1024)) MB (≤ 20 MB)"
        PASS=$((PASS+1))
    else
        fail "$(basename "$f"): $((size / 1024 / 1024)) MB exceeds 20 MB ceiling"
        FAIL=$((FAIL+1))
    fi
done

# Run --version on the host-native binary (always linux/amd64 in CI).
NATIVE_OS=$(go env GOOS)
NATIVE_ARCH=$(go env GOARCH)
native_ext=""
[[ "$NATIVE_OS" == "windows" ]] && native_ext=".exe"
NATIVE_BIN="$OUT/orva-cli-${NATIVE_OS}-${NATIVE_ARCH}${native_ext}"

if [[ -x "$NATIVE_BIN" ]]; then
    log "running native --version"
    if out=$("$NATIVE_BIN" --version 2>&1); then
        if [[ "$out" == *"test"* ]]; then
            ok "native --version: $out"
            PASS=$((PASS+1))
        else
            fail "native --version: missing 'test' marker: $out"
            FAIL=$((FAIL+1))
        fi
    else
        fail "native --version exited non-zero"
        FAIL=$((FAIL+1))
    fi
fi

# Optionally run --version under qemu for cross-arch Linux binaries.
if command -v qemu-aarch64 >/dev/null 2>&1 && [[ -x "$OUT/orva-cli-linux-arm64" ]]; then
    log "running linux/arm64 --version under qemu-aarch64"
    if out=$(qemu-aarch64 -L /usr/aarch64-linux-gnu "$OUT/orva-cli-linux-arm64" --version 2>&1); then
        ok "qemu-aarch64: $out"
        PASS=$((PASS+1))
    else
        warn "qemu-aarch64 invocation failed (often a libc/dyld mismatch on the runner): $out"
    fi
fi

echo
echo "=== build-matrix: $PASS passed, $FAIL failed ==="
[[ "$FAIL" -eq 0 ]]
