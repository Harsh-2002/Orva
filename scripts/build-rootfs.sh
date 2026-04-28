#!/usr/bin/env bash
# build-rootfs.sh — extract a minimal language rootfs from an official image.
#
# Usage: build-rootfs.sh <output-dir> <runtime>
#   runtime: node22 | node24 | python313 | python314
#
# The resulting rootfs is a directory usable as an nsjail --chroot target.
# It contains /usr/local/bin/node (or python3), all required shared libs,
# /opt/orva/ (empty, will be populated by the caller with the adapter),
# /tmp (empty dir), and /code (empty dir — nsjail mounts user code here).

set -euo pipefail

out="${1:-}"
runtime="${2:-}"
if [[ -z "$out" || -z "$runtime" ]]; then
  echo "usage: $0 <output-dir> <node22|node24|python313|python314>" >&2
  exit 2
fi

case "$runtime" in
  node22)    image="node:22-slim" ;;
  node24)    image="node:24-slim" ;;
  python313) image="python:3.13-slim" ;;
  python314) image="python:3.14-slim" ;;
  *) echo "unsupported runtime: $runtime" >&2; exit 2 ;;
esac

if ! command -v docker >/dev/null 2>&1; then
  echo "docker required (used to extract the base image filesystem)" >&2
  exit 2
fi

rm -rf "$out"
mkdir -p "$out"

echo ">> pulling $image"
docker pull "$image" >/dev/null

tmp="orva-rootfs-$$-$(date +%s)"
docker create --name "$tmp" "$image" /bin/true >/dev/null
trap 'docker rm -f "$tmp" >/dev/null 2>&1 || true' EXIT

echo ">> exporting filesystem to $out"
docker export "$tmp" | tar -xf - -C "$out"

# nsjail expects the language binary at /usr/local/bin/<name>.
case "$runtime" in
  node22|node24)
    if [[ ! -x "$out/usr/local/bin/node" ]]; then
      ln -sf "$(readlink -f "$out/usr/local/bin/node" 2>/dev/null || echo /usr/local/bin/node)" "$out/usr/local/bin/node" || true
    fi
    ;;
  python313|python314)
    if [[ ! -x "$out/usr/local/bin/python3" ]]; then
      (cd "$out/usr/local/bin" && ln -sf python python3) || true
    fi
    ;;
esac

# Standard Orva rootfs structure.
mkdir -p "$out/opt/orva" "$out/code" "$out/tmp"
chmod 1777 "$out/tmp"

echo ">> $runtime rootfs ready at $out ($(du -sh "$out" | cut -f1))"
