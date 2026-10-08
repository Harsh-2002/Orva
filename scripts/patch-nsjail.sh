#!/usr/bin/env bash
# Apply the reviewed NSTUN correction to the exact supported upstream revision.
set -euo pipefail
[[ $# == 1 ]] || { echo "usage: $0 NSJAIL_SOURCE" >&2; exit 64; }
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
expected=5ebcc30bef4af60d6e28f012dd8bf7b99b8b0acf
[[ $(git -C "$1" rev-parse HEAD) == "$expected" ]] || {
    echo "NSTUN patch requires nsjail $expected; review the patch before changing the pin" >&2
    exit 1
}
git -C "$1" apply --check "$script_dir/nsjail/nstun-partial-write.patch"
git -C "$1" apply "$script_dir/nsjail/nstun-partial-write.patch"
