#!/usr/bin/env bash
set -euo pipefail

arch="${1:?Supply arm64 or x64}"
case "$arch" in
  arm64|x64) ;;
  *) echo "Unsupported macOS architecture: $arch" >&2; exit 1 ;;
esac

: "${CSC_LINK:?Set the Developer ID signing identity}"
: "${CSC_KEY_PASSWORD:?Set the signing identity password}"
: "${MAC_NOTARY_KEY_BASE64:?Set the notarization API private key}"
: "${APPLE_API_KEY_ID:?Set the notarization API key ID}"
: "${APPLE_API_ISSUER:?Set the notarization API issuer ID}"
umask 077
APPLE_API_KEY="$(mktemp)"
export APPLE_API_KEY
trap 'rm -f "$APPLE_API_KEY"' EXIT
printf '%s' "$MAC_NOTARY_KEY_BASE64" | base64 -D > "$APPLE_API_KEY"
unset MAC_NOTARY_KEY_BASE64

cd "$(dirname "$0")/../desktop"
version="$(node -p 'require("./package.json").version')"
app_dir=mac
if [ "$arch" = arm64 ]; then app_dir=mac-arm64; fi
app="dist/$app_dir/Private Store.app"
npm run package -- --mac "--$arch" --dir -c.mac.identity='(WV3J3848JR)' -c.mac.notarize=true
ditto -c -k --sequesterRsrc --keepParent "$app" "dist/PrivateStore-$version-mac-$arch.zip"
