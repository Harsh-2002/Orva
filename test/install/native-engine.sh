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
api_key=$(< /var/lib/orva/.admin-key)
workdir=$(mktemp -d /tmp/orva-native-engine.XXXXXX)
cleanup() {
    /opt/orva/bin/orva functions delete ci-native-node --yes --endpoint "$endpoint" --api-key "$api_key" >/dev/null 2>&1 || true
    /opt/orva/bin/orva functions delete ci-native-python --yes --endpoint "$endpoint" --api-key "$api_key" >/dev/null 2>&1 || true
    rm -rf -- "$workdir"
}
trap cleanup EXIT

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
node_body=$("${orva[@]}" invoke ci-native-node --body '{}' 2>/dev/null)
jq -e '. == {runtime:"node", ok:true}' <<<"$node_body" >/dev/null

"${orva[@]}" deploy "$workdir/python" --name ci-native-python --runtime python --follow
python_body=$("${orva[@]}" invoke ci-native-python --body '{}' 2>/dev/null)
jq -e '. == {runtime:"python", ok:true}' <<<"$python_body" >/dev/null

echo "native installed service: Node and Python invocations passed"
