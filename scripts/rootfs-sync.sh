#!/bin/sh
# Sourced by orva-entrypoint, and by test/install/rootfs-sync-test.sh.
#
#   sync_runtime_rootfs IMAGE_ROOTFS VOLUME_ROOTFS RUNTIME...
#
# A volume mounted at /var/lib/orva shadows the rootfs baked into the image, so
# the runtimes in it only change when something copies them over. Each rootfs
# carries a .orva-runtime stamp naming the interpreter it holds ("node v26.13.0").
# A volume that is empty is seeded from the image. One whose stamp differs from the
# image's, including a volume from before stamps existed, is replaced: otherwise an
# image upgrade would never move an existing volume off the old Node or Python, and
# interpreter security fixes would never reach it.
#
# The copy goes to a sibling directory first, so an interrupted refresh leaves the
# working rootfs in place. An image with no stamp changes nothing.
sync_runtime_rootfs() {
  srr_image=$1
  srr_volume=$2
  shift 2
  for srr_rt in "$@"; do
    rm -rf "${srr_volume:?}/${srr_rt:?}.new"
    srr_img_id=$(cat "$srr_image/$srr_rt/.orva-runtime" 2>/dev/null || true)
    srr_vol_id=$(cat "$srr_volume/$srr_rt/.orva-runtime" 2>/dev/null || true)

    if [ ! -d "$srr_volume/$srr_rt/usr" ]; then
      echo ">> seeding $srr_volume/$srr_rt from image (${srr_img_id:-unstamped})"
      mkdir -p "$srr_volume/$srr_rt"
      cp -a "$srr_image/$srr_rt/." "$srr_volume/$srr_rt/"
    elif [ -n "$srr_img_id" ] && [ "$srr_img_id" != "$srr_vol_id" ]; then
      echo ">> refreshing $srr_rt runtime: ${srr_vol_id:-unstamped} -> $srr_img_id"
      mkdir -p "$srr_volume/$srr_rt.new"
      cp -a "$srr_image/$srr_rt/." "$srr_volume/$srr_rt.new/"
      rm -rf "${srr_volume:?}/${srr_rt:?}"
      mv "$srr_volume/$srr_rt.new" "$srr_volume/$srr_rt"
    fi
  done
}
