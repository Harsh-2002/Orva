#!/usr/bin/env bash
set -euo pipefail

# Native hosted-VM gate for the real installed systemd service. Unlike the
# privileged container matrix, sandbox failures are never advisory here.
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
version_args=()
if [[ -n "${RELEASE_TAG:-}" ]]; then
    version_args=(--version "$RELEASE_TAG")
fi

sh "$repo_root/scripts/install.sh" --bare-metal --yes --start "${version_args[@]}"
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

orva=(/opt/orva/bin/orva --endpoint "$endpoint" --api-key "$api_key")
"${orva[@]}" deploy "$workdir/node" --name ci-native-node --runtime node --follow
if ! node_body=$("${orva[@]}" invoke ci-native-node --body '{}' 2>&1); then
    printf 'Node invocation failed:\n%s\n' "$node_body" >&2
    exit 1
fi
if ! jq -e '. == {runtime:"node", ok:true}' <<<"$node_body" >/dev/null; then
    printf 'unexpected Node response:\n%s\n' "$node_body" >&2
    exit 1
fi

"${orva[@]}" deploy "$workdir/python" --name ci-native-python --runtime python --follow
if ! python_body=$("${orva[@]}" invoke ci-native-python --body '{}' 2>&1); then
    printf 'Python invocation failed:\n%s\n' "$python_body" >&2
    exit 1
fi
if ! jq -e '. == {runtime:"python", ok:true}' <<<"$python_body" >/dev/null; then
    printf 'unexpected Python response:\n%s\n' "$python_body" >&2
    exit 1
fi

echo "native installed service: Node and Python invocations passed"
