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
trap 'rm -rf "$temp"' EXIT
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

APP_BUILDER_TMP_DIR="$temp" node "$(dirname "$0")/sign-thornwake.cjs" "$app"
unset MAC_CERTIFICATE_P12_BASE64 MAC_CERTIFICATE_PASSWORD
printf '%s' "$MAC_NOTARY_KEY_BASE64" | base64 -D > "$temp/key.p8"
unset MAC_NOTARY_KEY_BASE64
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
python3 - "$output" "$filename" "$version" "$(dirname "$0")/../desktop/thornwake-changelog.json" <<'PY'
from pathlib import Path
import hashlib, json, sys
out, file, version = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
binary = out / file
major, minor, patch = map(int, version.split("."))
with binary.open("rb") as stream:
    digest = hashlib.file_digest(stream, "sha256").hexdigest()
notes = Path(sys.argv[4]).read_bytes()
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
    **changelog,
    "artifacts": [{"platform": "macos", "arch": arch, "file": file, "entry": "Thornwake.app",
                   "sha256": digest, "size": binary.stat().st_size} for arch in ("arm64", "x64")],
}
(out / "desktop-release.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY
