#!/usr/bin/env bash
# Exercise the compiled dependency's real TCP implementation with controlled syscalls.
set -euo pipefail
[[ $# == 1 ]] || { echo "usage: $0 BUILT_NSJAIL_SOURCE" >&2; exit 64; }
source_dir=$(cd -- "$1" && pwd)
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
objcopy --redefine-sym main=nsjail_entrypoint "$source_dir/nsjail.o" "$work/nsjail.o"
mapfile -t objects < <(find "$source_dir" -name '*.o' \
    ! -path '*/kafel/*' ! -path "$source_dir/nsjail.o" ! -path "$source_dir/nstun/tcp.o")
read -r -a libs <<< "$(pkg-config --libs protobuf libnl-route-3.0)"
g++ -std=c++20 -O2 -Wall -Wextra -Werror -Wno-unused-parameter -Wno-unused-variable \
    -I"$source_dir" -I"$source_dir/kafel/include" \
    "$script_dir/nsjail/tcp-regression.cc" "$work/nsjail.o" "${objects[@]}" \
    "$source_dir/kafel/libkafel.a" -pthread "${libs[@]}" \
    -Wl,--wrap=send -Wl,--wrap=epoll_ctl -o "$work/tcp-regression"
timeout 30 "$work/tcp-regression"
