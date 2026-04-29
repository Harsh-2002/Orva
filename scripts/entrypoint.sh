#!/bin/sh
# Orva container entrypoint.
#
# The rootfs + adapters are baked into the image. When users mount a volume
# at /var/lib/orva to persist their DB + function code, that volume shadows
# the image's /var/lib/orva/rootfs. On first start the volume is empty and
# we copy the rootfs in. On upgrades we refresh the adapters in case the
# image has newer versions.

set -e

IMAGE_ROOTFS=/opt/orva/rootfs
VOLUME_ROOTFS=/var/lib/orva/rootfs

RUNTIMES="node22 node24 python313 python314"

for rt in $RUNTIMES; do
  # If the volume's rootfs for this runtime is empty, seed it from the image.
  if [ ! -d "$VOLUME_ROOTFS/$rt/usr" ]; then
    echo ">> seeding $VOLUME_ROOTFS/$rt from image"
    mkdir -p "$VOLUME_ROOTFS/$rt"
    cp -a "$IMAGE_ROOTFS/$rt/." "$VOLUME_ROOTFS/$rt/"
  fi
done

# Always refresh the adapters so image upgrades roll out even when the
# user has an existing volume.
for rt in node22 node24; do
  mkdir -p "$VOLUME_ROOTFS/$rt/opt/orva"
  cp "$IMAGE_ROOTFS/$rt/opt/orva/adapter.js" "$VOLUME_ROOTFS/$rt/opt/orva/adapter.js"
done
for rt in python313 python314; do
  mkdir -p "$VOLUME_ROOTFS/$rt/opt/orva"
  cp "$IMAGE_ROOTFS/$rt/opt/orva/adapter.py" "$VOLUME_ROOTFS/$rt/opt/orva/adapter.py"
done

mkdir -p /var/lib/orva/functions

# /dev/net/tun is required by nsjail's --user_net (nstun backend) which
# powers the per-function egress toggle. Containers don't get it by
# default; mknod it here so functions with network_mode=egress can spin
# up their userspace TCP/UDP stack. Failure is non-fatal — only egress
# functions need it, and operators may have already provided the device
# via `docker run --device /dev/net/tun`.
if [ ! -e /dev/net/tun ]; then
  mkdir -p /dev/net 2>/dev/null || true
  if mknod /dev/net/tun c 10 200 2>/dev/null; then
    chmod 0666 /dev/net/tun
    echo ">> created /dev/net/tun for egress sandboxes"
  else
    echo ">> WARN: could not create /dev/net/tun — egress functions will fail to spawn"
    echo "         (re-run docker with --device /dev/net/tun if you need egress)"
  fi
fi

touch /var/lib/orva/.setup-complete

exec "$@"
