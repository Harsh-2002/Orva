#!/usr/bin/env bash
set -euo pipefail

# Native hosted-VM gate for the real installed systemd service. Unlike the
# privileged container matrix, sandbox failures are never advisory here.
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
installer_path="${ORVA_INSTALLER_PATH:-$repo_root/scripts/install.sh}"
[[ -s "$installer_path" ]] || { echo "server installer missing or empty: $installer_path" >&2; exit 1; }
version_args=()
if [[ -n "${RELEASE_TAG:-}" ]]; then
    version_args=(--version "$RELEASE_TAG")
fi

sh "$installer_path" --bare-metal --yes --start "${version_args[@]}"
systemctl is-active --quiet orva

endpoint=http://127.0.0.1:8443
admin_key_file=/var/lib/orva/.admin-key

report_service_state() {
    systemctl show orva \
        -p ActiveState -p SubState -p ExecMainStatus -p Result --no-pager >&2 || true
    systemctl cat orva --no-pager >&2 || true
    getcap /usr/local/bin/nsjail >&2 || true
}

# `systemctl is-active` only proves the process has entered the running state;
# the HTTP listener and bootstrap key may still be starting. The container
# matrix waits for health, so give the native service the same bounded gate.
ready=0
for _ in $(seq 1 60); do
    if [[ -s "$admin_key_file" ]] &&
       curl -fsS "$endpoint/api/v1/system/health" >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 1
done
if [[ "$ready" != "1" ]]; then
    echo "orva did not become API-ready within 60 seconds" >&2
    report_service_state
    exit 1
fi

api_key=$(< "$admin_key_file")
workdir=$(mktemp -d /tmp/orva-native-engine.XXXXXX)
cleanup() {
    /opt/orva/bin/orva functions delete ci-native-node --yes --endpoint "$endpoint" --api-key "$api_key" >/dev/null 2>&1 || true
    /opt/orva/bin/orva functions delete ci-native-python --yes --endpoint "$endpoint" --api-key "$api_key" >/dev/null 2>&1 || true
    rm -rf -- "$workdir"
}
trap cleanup EXIT

diagnose_failure() {
    status=$?
    echo "native engine validation failed (exit $status)" >&2
    report_service_state
    exit "$status"
}
trap diagnose_failure ERR

mkdir -p "$workdir/node" "$workdir/python"
cat > "$workdir/node/handler.js" <<'EOF'
exports.handler = async () => ({
  statusCode: 200,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ runtime: "node", ok: true }),
});
EOF
cat > "$workdir/python/handler.py" <<'EOF'
import json

def handler(event):
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"runtime": "python", "ok": True}),
    }
EOF

orva=(/opt/orva/bin/orva --quiet --endpoint "$endpoint" --api-key "$api_key")
"${orva[@]}" deploy "$workdir/node" --name ci-native-node --runtime node --follow
node_error="$workdir/node-invoke.stderr"
if ! node_body=$("${orva[@]}" invoke ci-native-node --body '{}' 2>"$node_error"); then
    printf 'Node invocation failed:\n' >&2
    cat "$node_error" >&2
    exit 1
fi
if ! jq -e '. == {runtime:"node", ok:true}' <<<"$node_body" >/dev/null; then
    printf 'unexpected Node response:\n%s\n' "$node_body" >&2
    exit 1
fi

"${orva[@]}" deploy "$workdir/python" --name ci-native-python --runtime python --follow
python_error="$workdir/python-invoke.stderr"
if ! python_body=$("${orva[@]}" invoke ci-native-python --body '{}' 2>"$python_error"); then
    printf 'Python invocation failed:\n' >&2
    cat "$python_error" >&2
    exit 1
fi
if ! jq -e '. == {runtime:"python", ok:true}' <<<"$python_body" >/dev/null; then
    printf 'unexpected Python response:\n%s\n' "$python_body" >&2
    exit 1
fi

# Dependency installs — the post-release canary for the build jail.
#
# This script installs the PUBLISHED binary, so it cannot gate a merge; the
# e2e job's arch matrix does that against the branch. What this adds is
# coverage of the shipped artifact on real amd64 AND arm64 hardware, which is
# where a seccomp profile that Kafel cannot compile would surface as every
# dependency build failing on one architecture. --follow exits non-zero on a
# failed build, so a broken build jail fails this script rather than warning.
mkdir -p "$workdir/node-deps" "$workdir/python-deps"
cat > "$workdir/node-deps/package.json" <<'EOF'
{ "name": "ci-native-node-deps", "version": "1.0.0", "dependencies": { "semver": "7.6.3" } }
EOF
cat > "$workdir/node-deps/handler.js" <<'EOF'
const semver = require("semver");
exports.handler = async () => ({
  statusCode: 200,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ runtime: "node", valid: semver.valid("1.2.3") }),
});
EOF
# urllib3 is py3-none-any, so this asserts the build jail rather than
# manylinux wheel-platform resolution.
printf 'urllib3==2.2.3\n' > "$workdir/python-deps/requirements.txt"
cat > "$workdir/python-deps/handler.py" <<'EOF'
import json
import urllib3

def handler(event):
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"runtime": "python", "v": urllib3.__version__}),
    }
EOF

"${orva[@]}" deploy "$workdir/node-deps" --name ci-native-node-deps --runtime node --follow
if ! deps_body=$("${orva[@]}" invoke ci-native-node-deps --body '{}' 2>"$workdir/node-deps.stderr"); then
    printf 'Node dependency invocation failed:\n' >&2
    cat "$workdir/node-deps.stderr" >&2
    exit 1
fi
if ! jq -e '. == {runtime:"node", valid:"1.2.3"}' <<<"$deps_body" >/dev/null; then
    printf 'unexpected Node dependency response:\n%s\n' "$deps_body" >&2
    exit 1
fi

"${orva[@]}" deploy "$workdir/python-deps" --name ci-native-python-deps --runtime python --follow
if ! pydeps_body=$("${orva[@]}" invoke ci-native-python-deps --body '{}' 2>"$workdir/python-deps.stderr"); then
    printf 'Python dependency invocation failed:\n' >&2
    cat "$workdir/python-deps.stderr" >&2
    exit 1
fi
if ! jq -e '. == {runtime:"python", v:"2.2.3"}' <<<"$pydeps_body" >/dev/null; then
    printf 'unexpected Python dependency response:\n%s\n' "$pydeps_body" >&2
    exit 1
fi

echo "native installed service: Node and Python invocations passed (incl. jailed dependency installs)"
