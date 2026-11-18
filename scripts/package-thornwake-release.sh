#!/usr/bin/env bash
set -euo pipefail
umask 077

version="${1:?Supply a semver}"
archive="${2:?Supply the staged universal ZIP}"
output="${3:?Supply the output directory}"
game="${4:-thornwake}"
IFS=$'\t' read -r name stem bundle library_name pck_name < <(node "$(dirname "$0")/native-game.cjs" "$game")
[[ -n "$bundle" && -n "$library_name" && -n "$pck_name" ]]
[[ "$version" =~ ^(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})$ ]]
: "${MAC_CERTIFICATE_P12_BASE64:?Missing Developer ID certificate}"
: "${MAC_CERTIFICATE_PASSWORD:?Missing certificate password}"
: "${MAC_NOTARY_KEY_BASE64:?Missing notarization key}"
: "${MAC_NOTARY_KEY_ID:?Missing notarization key ID}"
: "${MAC_NOTARY_ISSUER_ID:?Missing notarization issuer ID}"

temp="$(mktemp -d)"
trap 'rm -rf "$temp"' EXIT
python3 "$(dirname "$0")/validate-native-archive.py" "$archive" "$name" "$pck_name"
ditto -x -k "$archive" "$temp"
app="$temp/$name.app"
plist="$app/Contents/Info.plist"
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$plist")" == "$bundle" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$plist")" == "$version" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$plist")" == "$name" ]]
lipo "$app/Contents/MacOS/$name" -verify_arch arm64 x86_64
if [[ "$library_name" != "-" ]]; then
  lipo "$app/Contents/Frameworks/$library_name" -verify_arch arm64 x86_64
fi

APP_BUILDER_TMP_DIR="$temp" node "$(dirname "$0")/sign-thornwake.cjs" "$app" "$game"
unset MAC_CERTIFICATE_P12_BASE64 MAC_CERTIFICATE_PASSWORD
printf '%s' "$MAC_NOTARY_KEY_BASE64" | base64 -D > "$temp/key.p8"
unset MAC_NOTARY_KEY_BASE64
codesign --verify --deep --strict "$app"
mkdir -p "$output"
filename="$stem-$version-mac-universal.zip"
ditto -c -k --keepParent "$app" "$output/$filename"
xcrun notarytool submit "$output/$filename" --key "$temp/key.p8" --key-id "$MAC_NOTARY_KEY_ID" \
  --issuer "$MAC_NOTARY_ISSUER_ID" --wait --timeout 20m
xcrun stapler staple "$app"
xcrun stapler validate "$app"
codesign --verify --deep --strict "$app"
spctl --assess --type execute --verbose=2 "$app"
rm "$output/$filename"
ditto -c -k --sequesterRsrc --keepParent "$app" "$output/$filename"
python3 - "$output" "$filename" "$version" "$name" "$game" "$(dirname "$0")/native-game.cjs" "$(dirname "$0")/../desktop/thornwake-changelog.json" <<'PY'
from pathlib import Path
import hashlib, json, subprocess, sys
out, file, version = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
binary = out / file
major, minor, patch = map(int, version.split("."))
with binary.open("rb") as stream:
    digest = hashlib.file_digest(stream, "sha256").hexdigest()
changes = subprocess.check_output([
    "node", "-e", "process.stdout.write(JSON.stringify(require(process.argv[1])(process.argv[2]).changes))",
    str(Path(sys.argv[6]).resolve()), sys.argv[5],
], text=True)
changes = json.loads(changes)
if sys.argv[5] == "thornwake":
    notes = Path(sys.argv[7]).read_bytes()
    if len(notes) > 32000:
        raise ValueError("Thornwake changelog exceeds the size limit")
    changelog = json.loads(notes)
    version_code = major * 1_000_000 + minor * 1000 + patch
    if (changelog.get("schemaVersion"), changelog.get("version"), changelog.get("versionCode")) != (1, version, version_code):
        raise ValueError("Thornwake changelog must match the release version and versionCode")
    changes = changelog.get("changes")
    if not isinstance(changes, list) or not 1 <= len(changes) <= 50 or any(
        not isinstance(c, str) or not c.strip() or len(c.encode()) > 2000 or any(ord(x) < 32 for x in c) for c in changes
    ):
        raise ValueError("Thornwake changes must be nonempty plain-text strings")
manifest = {
    "schemaVersion": 1,
    "version": version,
    "versionCode": major * 1_000_000 + minor * 1000 + patch,
    "changes": changes,
    "artifacts": [{"platform": "macos", "arch": arch, "file": file, "entry": sys.argv[4] + ".app",
                   "sha256": digest, "size": binary.stat().st_size} for arch in ("arm64", "x64")],
}
(out / "desktop-release.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY
