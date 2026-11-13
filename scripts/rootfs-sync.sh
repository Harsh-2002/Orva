#!/bin/sh
# Sourced by orva-entrypoint, and by test/install/rootfs-sync-test.sh.
#
#   sync_runtime_rootfs IMAGE_ROOTFS VOLUME_ROOTFS RUNTIME...
#
# Content can change without an interpreter bump (for example, stdlib bytecode).
# Stage refreshes beside the working tree so a failed copy leaves it intact.
sync_runtime_rootfs() {
  srr_image=$1
  srr_volume=$2
  shift 2
  for srr_rt in "$@"; do
    rm -rf "${srr_volume:?}/${srr_rt:?}.new"
    srr_img_id=$(cat "$srr_image/$srr_rt/.orva-runtime" 2>/dev/null || true)
    srr_vol_id=$(cat "$srr_volume/$srr_rt/.orva-runtime" 2>/dev/null || true)
    srr_img_rev=$(cat "$srr_image/$srr_rt/.orva-rootfs-revision" 2>/dev/null || true)
    srr_vol_rev=$(cat "$srr_volume/$srr_rt/.orva-rootfs-revision" 2>/dev/null || true)

    if [ ! -d "$srr_volume/$srr_rt/usr" ]; then
      echo ">> seeding $srr_volume/$srr_rt from image (${srr_img_id:-unstamped})"
      mkdir -p "$srr_volume/$srr_rt" || return 1
      cp -a "$srr_image/$srr_rt/." "$srr_volume/$srr_rt/" || return 1
    elif [ -n "$srr_img_id" ] && { [ "$srr_img_id" != "$srr_vol_id" ] ||
      { [ -n "$srr_img_rev" ] && [ "$srr_img_rev" != "$srr_vol_rev" ]; }; }; then
      echo ">> refreshing $srr_rt runtime: ${srr_vol_id:-unstamped} -> $srr_img_id (rootfs ${srr_vol_rev:-unrevisioned} -> ${srr_img_rev:-unrevisioned})"
      mkdir -p "$srr_volume/$srr_rt.new" || return 1
      cp -a "$srr_image/$srr_rt/." "$srr_volume/$srr_rt.new/" || return 1
      rm -rf "${srr_volume:?}/${srr_rt:?}" || return 1
      mv "$srr_volume/$srr_rt.new" "$srr_volume/$srr_rt" || return 1
    fi
  done
}
