#!/usr/bin/env bash
# Unit tests for scripts/rootfs-sync.sh, the container entrypoint's runtime
# refresh. Pure shell on temp directories: no root, nsjail or Docker needed.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SYNC="$ROOT/scripts/rootfs-sync.sh"

pass=0
fail=0
ok() { printf 'ok - %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf 'not ok - %s\n' "$1" >&2; fail=$((fail + 1)); }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# run under sh, as the container does, and capture the log
sync() { sh -c '. "$1"; sync_runtime_rootfs "$2" "$3" node python' sh "$SYNC" "$1" "$2" 2>&1; }

# make_rootfs DIR RUNTIME STAMP MARKER   (STAMP empty = a rootfs from before stamps)
make_rootfs() {
  mkdir -p "$1/$2/usr/local/bin" "$1/$2/opt/orva"
  printf '%s\n' "$4" > "$1/$2/usr/local/bin/marker"
  if [ -n "$3" ]; then printf '%s\n' "$3" > "$1/$2/.orva-runtime"; fi
}

new_case() {
  case_dir="$work/$1"
  image="$case_dir/image"
  volume="$case_dir/volume"
  mkdir -p "$case_dir/functions/keepme"
  printf 'user code\n' > "$case_dir/functions/keepme/handler.js"
  make_rootfs "$image" node "node v26.13.0" "image-node"
  make_rootfs "$image" python "python 3.15.2" "image-python"
  mkdir -p "$volume"
}

new_case empty
out=$(sync "$image" "$volume")
if [ "$(cat "$volume/node/usr/local/bin/marker")" = image-node ] &&
   [ "$(cat "$volume/python/.orva-runtime")" = "python 3.15.2" ] &&
   [[ "$out" == *"seeding"* ]]; then
  ok 'an empty volume is seeded from the image, stamps included'
else
  bad "seed: $out"
fi

new_case current
make_rootfs "$volume" node "node v26.13.0" "volume-node"
make_rootfs "$volume" python "python 3.15.2" "volume-python"
out=$(sync "$image" "$volume")
if [ "$(cat "$volume/node/usr/local/bin/marker")" = volume-node ] &&
   [ "$(cat "$volume/python/usr/local/bin/marker")" = volume-python ] &&
   [[ "$out" != *"refreshing"* && "$out" != *"seeding"* ]]; then
  ok 'a volume that already matches the image is left alone'
else
  bad "no-op: $out"
fi

new_case old-release
make_rootfs "$volume" node "" "old-node-24"
make_rootfs "$volume" python "" "old-python-3.14"
out=$(sync "$image" "$volume")
if [ "$(cat "$volume/node/usr/local/bin/marker")" = image-node ] &&
   [ "$(cat "$volume/python/usr/local/bin/marker")" = image-python ] &&
   [ "$(cat "$volume/node/.orva-runtime")" = "node v26.13.0" ] &&
   [[ "$out" == *"refreshing node runtime: unstamped -> node v26.13.0"* ]] &&
   [ ! -e "$volume/node.new" ] && [ ! -e "$volume/python.new" ]; then
  ok 'a volume from before stamps existed is moved to the image runtimes'
else
  bad "old release volume: $out"
fi

new_case patch-bump
make_rootfs "$volume" node "node v26.12.1" "old-node-26.12"
make_rootfs "$volume" python "python 3.15.2" "volume-python"
out=$(sync "$image" "$volume")
if [ "$(cat "$volume/node/usr/local/bin/marker")" = image-node ] &&
   [ "$(cat "$volume/python/usr/local/bin/marker")" = volume-python ] &&
   [[ "$out" == *"node v26.12.1 -> node v26.13.0"* ]]; then
  ok 'a patch release of one interpreter refreshes only that runtime'
else
  bad "patch bump: $out"
fi

new_case unstamped-image
make_rootfs "$image" node "" "image-node-unstamped"
rm -f "$image/node/.orva-runtime"
make_rootfs "$volume" node "node v24.22.1" "volume-node"
make_rootfs "$volume" python "python 3.15.2" "volume-python"
out=$(sync "$image" "$volume")
if [ "$(cat "$volume/node/usr/local/bin/marker")" = volume-node ] && [[ "$out" != *"refreshing"* ]]; then
  ok 'an image with no stamp never replaces a volume'
else
  bad "unstamped image: $out"
fi

new_case interrupted
make_rootfs "$volume" python "python 3.15.2" "volume-python"
mkdir -p "$volume/node.new/usr"
printf 'half copied\n' > "$volume/node.new/usr/partial"
out=$(sync "$image" "$volume")
if [ "$(cat "$volume/node/usr/local/bin/marker")" = image-node ] &&
   [ ! -e "$volume/node.new" ] && [ ! -e "$volume/node/usr/partial" ]; then
  ok 'an interrupted refresh is discarded and the runtime is restored'
else
  bad "interrupted: $out"
fi

new_case user-data
make_rootfs "$volume" node "" "old-node"
make_rootfs "$volume" python "" "old-python"
sync "$image" "$volume" >/dev/null
if [ "$(cat "$case_dir/functions/keepme/handler.js")" = "user code" ]; then
  ok 'function code and other volume contents are never touched'
else
  bad 'a refresh changed data outside the rootfs'
fi

new_case unsafe-path
# Refused by the ${var:?} guard itself, not by a later failure that happens to stop it.
rc=0
out=$(sh -c '. "$1"; sync_runtime_rootfs "$2" "" node' sh "$SYNC" "$image" 2>&1) || rc=$?
if [ "$rc" -ne 0 ] && [[ "$out" == *srr_volume* ]]; then
  ok 'an empty volume path is refused before anything is deleted or copied'
else
  bad "empty volume path was not refused by the guard (rc=$rc): $out"
fi

printf 'passed=%d failed=%d\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
