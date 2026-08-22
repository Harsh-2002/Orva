#!/usr/bin/env bash
# End-to-end verification against a real Orva container on a throwaway Docker
# network.
#
# Why this exists: nsjail needs CAP_SYS_ADMIN, unconfined apparmor/seccomp and a
# delegated cgroupfs. A developer box often cannot give it those, and when it
# cannot, every invocation fails with WORKER_CRASHED for reasons that have
# nothing to do with the code under test. The container has exactly the
# privileges docker-compose.yml grants in production, so a sandbox failure here
# is a real failure.
#
# The network is created fresh and removed on exit, and nothing is published on
# a routable address, so this cannot reach or disturb an existing deployment.
set -euo pipefail

NETWORK=${ORVA_E2E_NETWORK:-orva-e2e-net}
NAME=${ORVA_E2E_NAME:-orva-e2e}
VOLUME=${ORVA_E2E_VOLUME:-orva-e2e-data}
PORT=${ORVA_E2E_PORT:-18600}
IMAGE=${ORVA_E2E_IMAGE:-orva:e2e-local}
KEEP=${ORVA_E2E_KEEP:-0}

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

# Prefer a daemon this user can reach directly; fall back to passwordless sudo.
# The wrapper is deliberately NOT named `docker`: a function shadowing the
# command it wraps is what shellcheck flags as SC2032/SC2033, and it made the
# first version of this script fail outright with `sudo command docker`, since
# `command` is a shell builtin rather than an executable.
if command -v docker >/dev/null && docker info >/dev/null 2>&1; then
  DOCKER=(docker)
elif sudo -n docker info >/dev/null 2>&1; then
  DOCKER=(sudo -n docker)
else
  echo "cannot reach a Docker daemon (tried direct, then passwordless sudo)" >&2
  exit 1
fi
dk() { "${DOCKER[@]}" "$@"; }

cleanup() {
  [ "$KEEP" = "1" ] && { echo "keeping $NAME (ORVA_E2E_KEEP=1)"; return; }
  dk rm -f "$NAME" >/dev/null 2>&1 || true
  dk volume rm "$VOLUME" >/dev/null 2>&1 || true
  dk network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> building $IMAGE"
# --network=host because this daemon may have no default `bridge` network, and
# buildkit refuses a user-defined one. It affects the build only.
dk build --network=host -t "$IMAGE" \
  --build-arg VERSION="v$(date -u +%Y.%m.%d)-e2e" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" . >/tmp/orva-e2e-build.log 2>&1 ||
  { tail -30 /tmp/orva-e2e-build.log; exit 1; }

# Order matters: a network cannot be removed while a container is still attached
# to it, and a leftover container from an ORVA_E2E_KEEP=1 run is exactly that.
# Removing the container first makes the network removal succeed instead of
# silently failing and leaving `network create` to error out.
echo "==> clearing any previous run"
dk rm -f "$NAME" >/dev/null 2>&1 || true
dk volume rm "$VOLUME" >/dev/null 2>&1 || true
dk network rm "$NETWORK" >/dev/null 2>&1 || true

echo "==> creating network $NETWORK"
dk network create "$NETWORK" >/dev/null

echo "==> starting $NAME on 127.0.0.1:$PORT"
# ORVA_REQUIRE_SANDBOX=1 makes a sandbox skip a hard failure, so a green run
# here cannot be a run that quietly never entered nsjail.
# cgroupns=host: nsjail sets memory.max / cpu.max per function and needs a
# writable, properly-delegated cgroupfs. pid=host: the cgroup.procs writes
# reference PIDs in the host's namespace, so without it every sandbox spawn
# fails with "No such file or directory" while enrolling the PID. Both mirror
# `cgroup: host` / `pid: host` in docker-compose.yml -- production runs this way.
dk run -d --name "$NAME" --network "$NETWORK" \
  --cgroupns=host --pid=host \
  -p "127.0.0.1:$PORT:8443" \
  -v "$VOLUME:/var/lib/orva" \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  --device /dev/net/tun:/dev/net/tun \
  -e ORVA_REQUIRE_SANDBOX=1 \
  "$IMAGE" >/dev/null

BASE="http://127.0.0.1:$PORT"
echo -n "==> waiting for health"
for _ in $(seq 1 60); do
  if curl -fsS -m 2 "$BASE/api/v1/system/health" >/dev/null 2>&1; then break; fi
  echo -n .; sleep 1
done
echo
curl -fsS -m 5 "$BASE/api/v1/system/health" >/dev/null ||  { dk logs "$NAME" | tail -30; exit 1; }

KEY=$(dk exec "$NAME" cat /var/lib/orva/.admin-key)
echo "==> admin key resolved, sandbox check"
if dk exec "$NAME" /usr/local/bin/nsjail -Mo --chroot / -- /bin/true >/dev/null 2>&1; then
  echo "    nsjail can create namespaces"
else
  echo "    nsjail CANNOT create namespaces -- the container lacks privileges."
  echo "    Check --cgroupns=host and --pid=host; see test/container/CLAUDE.md."
  exit 1
fi

status=0
echo
echo "==> API + sandbox suite"
python3 test/container/rollback_e2e.py "$BASE" "$KEY" "$NAME" || status=1

# Populate the instance first. Every list view renders differently once it has
# rows -- a KV key button below the touch floor went unmeasured through several
# full runs because no run had ever put a key in the store, so the control never
# existed to be measured. Empty lists hide controls.
if [ "${ORVA_E2E_BROWSER:-1}" = "1" ] && [ -d test/browser ]; then
  echo
  echo "==> seeding data so lists are not empty"
  python3 test/container/seed.py "$BASE" "$KEY" || true
fi

if [ "${ORVA_E2E_BROWSER:-1}" = "1" ] && [ -d test/browser ]; then
  echo
  echo "==> browser suite"
  # The container is thrown away at the end of this script, so the flows that
  # delete data have somewhere safe to run. They are the ones that cover the
  # confirm-dialog keyboard behaviour, which is where a data-loss bug shipped.
  browser_args=(--url "$BASE" --api-key "$KEY")
  [ "${ORVA_E2E_DESTRUCTIVE:-1}" = "1" ] && browser_args+=(--destructive)
  ( cd test/browser && node run.mjs "${browser_args[@]}" ) || status=1
fi

exit $status
