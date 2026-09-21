#!/usr/bin/env bash
# Build the pinned standalone nsjail release asset as a fully static binary.
# Run this inside Debian bookworm with the build dependencies installed.
set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 OUTPUT" >&2
    exit 64
fi

output="$1"
ref="${NSJAIL_REF:?NSJAIL_REF must name the pinned upstream commit}"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git clone --filter=blob:none https://github.com/google/nsjail.git "$work/nsjail"
git -C "$work/nsjail" checkout "$ref"

# Environment assignment is intentional: nsjail appends protobuf/libnl while
# its recursive kafel build remains free to clear LDFLAGS for relocatable code.
LDFLAGS=-static make -C "$work/nsjail" -j"$(nproc)"
strip "$work/nsjail/nsjail"

description=$(file "$work/nsjail/nsjail")
printf '%s\n' "$description"
case "$description" in
    *'statically linked'*) ;;
    *) echo "nsjail release asset is not statically linked" >&2; exit 1 ;;
esac
if readelf -l "$work/nsjail/nsjail" | grep -q 'Requesting program interpreter'; then
    echo "nsjail release asset unexpectedly has a dynamic interpreter" >&2
    exit 1
fi

install -D -m 0755 "$work/nsjail/nsjail" "$output"
