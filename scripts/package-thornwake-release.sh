#!/usr/bin/env bash
set -euo pipefail
umask 077

version="${1:?Supply a semver}"
archive="${2:?Supply the staged universal ZIP}"
output="${3:?Supply the output directory}"
[[ "$version" =~ ^(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})$ ]]
: "${MAC_CERTIFICATE_P12_BASE64:?Missing Developer ID certificate}"
: "${MAC_CERTIFICATE_PASSWORD:?Missing certificate password}"
: "${MAC_NOTARY_KEY_BASE64:?Missing notarization key}"
: "${MAC_NOTARY_KEY_ID:?Missing notarization key ID}"
: "${MAC_NOTARY_ISSUER_ID:?Missing notarization issuer ID}"

temp="$(mktemp -d)"
keychain="$temp/signing.keychain-db"
cleanup() {
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  rm -rf "$temp"
}
trap cleanup EXIT
python3 - "$archive" <<'PY'
from pathlib import PurePosixPath
from zipfile import ZipFile
from stat import S_IFDIR, S_IFREG, S_IFMT
import sys
with ZipFile(sys.argv[1]) as z:
    paths = set()
    total = 0
    for entry in z.infolist():
        path = PurePosixPath(entry.filename)
        if (not path.parts or path.parts[0] != "Thornwake.app" or path.is_absolute() or ".." in path.parts
                or "\\" in entry.filename or S_IFMT(entry.external_attr >> 16) not in (0, S_IFDIR, S_IFREG)
                or path.as_posix().casefold() in paths):
            raise SystemExit("Unexpected or unsafe Thornwake ZIP entry")
        paths.add(path.as_posix().casefold())
        total += entry.file_size
        if total > 384 * 1024 * 1024:
            raise SystemExit("Thornwake ZIP exceeds the expanded size limit")
    if "thornwake.app/contents/macos/thornwake" not in paths:
        raise SystemExit("The ZIP is missing its executable")
PY
ditto -x -k "$archive" "$temp"
app="$temp/Thornwake.app"
plist="$app/Contents/Info.plist"
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$plist")" == "cool.gravitas.thornwake" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$plist")" == "$version" ]]
library="$app/Contents/Frameworks/libthornwake_core.dylib"
lipo "$app/Contents/MacOS/Thornwake" -verify_arch arm64 x86_64
lipo "$library" -verify_arch arm64 x86_64

printf '%s' "$MAC_CERTIFICATE_P12_BASE64" | base64 -D > "$temp/cert.p12"
printf '%s' "$MAC_NOTARY_KEY_BASE64" | base64 -D > "$temp/key.p8"
unset MAC_CERTIFICATE_P12_BASE64 MAC_NOTARY_KEY_BASE64
key_password="$(openssl rand -hex 24)"
security create-keychain -p "$key_password" "$keychain"
security unlock-keychain -p "$key_password" "$keychain"
security set-keychain-settings -t 3600 "$keychain"
security import "$temp/cert.p12" -k "$keychain" -P "$MAC_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$key_password" "$keychain" >/dev/null
unset MAC_CERTIFICATE_PASSWORD key_password
identity="$(security find-identity -v -p codesigning "$keychain" | awk '/Developer ID Application:.*\(WV3J3848JR\)/ {print $2}')"
[[ "$identity" =~ ^[0-9A-F]{40}$ ]]
codesign --keychain "$keychain" --force --timestamp --options runtime --sign "$identity" "$library"
codesign --keychain "$keychain" --force --timestamp --options runtime --sign "$identity" "$app"
codesign --verify --deep --strict "$app"
mkdir -p "$output"
filename="Thornwake-$version-mac-universal.zip"
ditto -c -k --keepParent "$app" "$output/$filename"
xcrun notarytool submit "$output/$filename" --key "$temp/key.p8" --key-id "$MAC_NOTARY_KEY_ID" \
  --issuer "$MAC_NOTARY_ISSUER_ID" --wait --timeout 20m
xcrun stapler staple "$app"
xcrun stapler validate "$app"
codesign --verify --deep --strict "$app"
spctl --assess --type execute --verbose=2 "$app"
rm "$output/$filename"
ditto -c -k --sequesterRsrc --keepParent "$app" "$output/$filename"
python3 - "$output" "$filename" "$version" <<'PY'
from pathlib import Path
import hashlib, json, sys
out, file, version = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
binary = out / file
major, minor, patch = map(int, version.split("."))
with binary.open("rb") as stream:
    digest = hashlib.file_digest(stream, "sha256").hexdigest()
manifest = {
    "schemaVersion": 1,
    "version": version,
    "versionCode": major * 1_000_000 + minor * 1000 + patch,
    "changes": ["Sign in with Gravitas and host or join native Thornwake multiplayer lobbies."],
    "artifacts": [{"platform": "macos", "arch": arch, "file": file, "entry": "Thornwake.app",
                   "sha256": digest, "size": binary.stat().st_size} for arch in ("arm64", "x64")],
}
(out / "desktop-release.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY
