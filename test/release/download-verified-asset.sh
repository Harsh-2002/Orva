#!/bin/sh
# Download one GitHub Release asset and prove it matches the release's
# checksums.txt before exposing it to an installer E2E job.
set -eu

[ "$#" -eq 2 ] || {
    echo "usage: $0 <asset-name> <destination>" >&2
    exit 2
}

asset=$1
destination=$2
repo=${ORVA_REPO:-${GITHUB_REPOSITORY:-Harsh-2002/Orva}}
tag=${ORVA_VERSION:-}

command -v curl >/dev/null 2>&1 || {
    echo "curl is required to download release assets" >&2
    exit 1
}

if [ -z "$tag" ]; then
    token=${GITHUB_TOKEN:-${GH_TOKEN:-}}
    if [ -n "$token" ]; then
        release_json=$(curl -fsSL -H "Authorization: Bearer $token" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$repo/releases/latest")
    else
        release_json=$(curl -fsSL -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$repo/releases/latest")
    fi
    tag=$(printf '%s' "$release_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
fi
[ -n "$tag" ] || { echo "could not resolve release tag" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
base="https://github.com/$repo/releases/download/$tag"
curl -fsSL --retry 3 --retry-delay 2 "$base/$asset" -o "$tmp/$asset"
curl -fsSL --retry 3 --retry-delay 2 "$base/checksums.txt" -o "$tmp/checksums.txt"

[ -s "$tmp/$asset" ] || { echo "release asset missing or empty: $asset" >&2; exit 1; }
[ -s "$tmp/checksums.txt" ] || { echo "release checksums.txt missing or empty" >&2; exit 1; }

want=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1; found=1; exit } END { if (!found) exit 1 }' \
    "$tmp/checksums.txt") || {
    echo "checksum entry missing for $asset" >&2
    exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
    got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
[ "$want" = "$got" ] || {
    echo "checksum mismatch for $asset: want=$want got=$got" >&2
    exit 1
}

mkdir -p "$(dirname "$destination")"
cp "$tmp/$asset" "$destination"
chmod 0755 "$destination" 2>/dev/null || true
echo "verified release asset $asset from $tag" >&2
printf '%s\n' "$tag"
