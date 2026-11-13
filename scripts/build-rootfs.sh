#!/usr/bin/env bash
# build-rootfs.sh — extract a minimal language rootfs from an official image.
#
# Usage: build-rootfs.sh <output-dir> <runtime>
#   runtime: node | python   (latest-stable only: Node.js 26 / Python 3.15)
#
# The resulting rootfs is a directory usable as an nsjail --chroot target.
# It contains /usr/local/bin/node (or python3), all required shared libs,
# /opt/orva/ (empty, will be populated by the caller with the adapter),
# /tmp (empty dir), and /code (empty dir — nsjail mounts user code here).

set -euo pipefail

out="${1:-}"
runtime="${2:-}"
if [[ -z "$out" || -z "$runtime" ]]; then
  echo "usage: $0 <output-dir> <node|python>" >&2
  exit 2
fi

case "$runtime" in
  node)   image="node:26-slim" ;;
  python) image="python:3.15-slim" ;;
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
  node)
    if [[ ! -x "$out/usr/local/bin/node" ]]; then
      ln -sf "$(readlink -f "$out/usr/local/bin/node" 2>/dev/null || echo /usr/local/bin/node)" "$out/usr/local/bin/node" || true
    fi
    # The upstream binary ships full symbols (~17 MiB on amd64). JavaScript
    # stack traces come from V8's own tables, so removing them costs only
    # native frame names in a core dump.
    if command -v strip >/dev/null 2>&1; then
      nr_before=$(stat -c %s "$out/usr/local/bin/node" 2>/dev/null || echo 0)
      strip --strip-unneeded "$out/usr/local/bin/node" 2>/dev/null || true
      nr_after=$(stat -c %s "$out/usr/local/bin/node" 2>/dev/null || echo 0)
      echo ">> stripped node binary: $nr_before -> $nr_after bytes"
    else
      echo ">> strip not found; node binary keeps its symbols" >&2
    fi
    ;;
  python)
    if [[ ! -x "$out/usr/local/bin/python3" ]]; then
      (cd "$out/usr/local/bin" && ln -sf python python3) || true
    fi
    # The runtime rootfs is read-only; chroot keeps cached source paths valid.
    stdlib_rel=$(cd "$out" && ls -d usr/local/lib/python3.* 2>/dev/null | head -1)
    if [[ -z "$stdlib_rel" ]]; then
      echo "no stdlib directory found under $out/usr/local/lib" >&2
      exit 1
    fi
    echo ">> precompiling ${stdlib_rel}"
    # The container runs as root (chroot needs it), so it would otherwise leave
    # root-owned 0600 files that this script cannot package as a normal user.
    stdlib_abs="/${stdlib_rel}"
    docker run --rm \
      -e HOST_UID_GID="$(id -u):$(id -g)" \
      -v "$(cd "$out" && pwd)":/rootfs \
      --entrypoint /bin/sh "$image" -c "
        set -e
        chroot /rootfs /usr/local/bin/python3 -m compileall -q '$stdlib_abs'
        chown -R \"\$HOST_UID_GID\" '/rootfs$stdlib_abs'
        find '/rootfs$stdlib_abs' -name '*.pyc' -exec chmod 0644 {} +
      "
    pyc=$(find "$out/$stdlib_rel" -name '*.pyc' | wc -l)
    echo ">> stdlib bytecode: $pyc .pyc files"
    ;;
esac

# Keep interpreter identity separate from content changes for dependency caching.
case "$runtime" in
  node)   stamp="node $(docker run --rm --entrypoint node "$image" --version)" ;;
  python) stamp="python $(docker run --rm --entrypoint python3 "$image" --version | awk '{print $2}')" ;;
esac
printf '%s\n' "$stamp" > "$out/.orva-runtime"
cp "$(dirname "$0")/rootfs-revisions/$runtime" "$out/.orva-rootfs-revision"
echo ">> runtime stamp: $stamp"

# Standard Orva rootfs structure.
mkdir -p "$out/opt/orva" "$out/code" "$out/tmp"
chmod 1777 "$out/tmp"

echo ">> $runtime rootfs ready at $out ($(du -sh "$out" | cut -f1))"
