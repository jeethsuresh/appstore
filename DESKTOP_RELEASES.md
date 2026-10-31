# Private Store for desktop

The Electron client in `desktop/` uses the same Go service, Gravitas account,
GitHub release polling/webhooks, app pages and verified downloads as Android.
macOS, Windows and Linux builds support x64 and ARM64. Desktop notifications
are not needed. The first release includes Private Store, RSS Reader and Email
on the operating systems/processors for which they have published binaries.
Apps without published desktop builds are not advertised as installable.

## One catalog, independent platforms

Existing entries with `packageName`, `assetName` and `certificateSha256` stay
Android-only. Add `"desktop": true` to enable desktop ingestion. Keep the Android
fields for an app available on both; omit them for a desktop-only app:

```json
{"id":"example","name":"Example","repository":"owner/example","desktop":true,
 "description":"An independent desktop app."}
```

No separate mobile/desktop app lists or platform arrays are required. Only
verified release artifacts determine supported desktop operating systems and
processors. `/v1/apps` without platform parameters remains the Android catalog;
desktop requests require `platform=macos|windows|linux&arch=x64|arm64`.
The public `/v1/desktop/apps` endpoint exposes the same desktop descriptions
and release metadata. Artifact downloads still require a device credential;
the website's desktop bootstrap downloads require a checked Gravitas session.

Existing Electron releases can be ingested without upstream changes: assets
named `*-mac-arm64.dmg`, `*-win-x64.exe` and `*-linux-x86_64.AppImage` (also
`x64`, `arm64`, `aarch64`, `amd64`) supply their GitHub SHA-256 digest. The
catalog name selects the default app entry; a unique top-level app entry is
detected when an existing native package uses a different executable name.
Windows NSIS payloads are extracted without running their installer or changing
an existing installation's uninstall registry. Legacy versions use
`major*1000000+minor*1000+patch`, with each component below 1000. Missing digests,
ambiguous targets and unsupported packages fail ingestion. Legacy release bodies
provide notes; absent bodies get a short version notice.

For other packaging or explicit version codes, publish `desktop-release.json`
alongside the binaries. It is the preferred contract:

```json
{
  "schemaVersion":1,"version":"1.2.3","versionCode":1002003,
  "changes":["A concise description of what changed."],
  "artifacts":[
    {"platform":"macos","arch":"arm64","file":"Example-mac-arm64.zip",
     "entry":"Example.app","sha256":"<64 lowercase hex characters>","size":12345},
    {"platform":"windows","arch":"x64","file":"Example-win-x64.zip",
     "entry":"Example.exe","sha256":"<64 lowercase hex characters>","size":12345},
    {"platform":"linux","arch":"x64","file":"Example-linux-x64.AppImage",
     "entry":"Example-linux-x64.AppImage","sha256":"<64 lowercase hex characters>","size":12345}
  ]
}
```

ZIP entries must be at the archive root. macOS `.dmg` and Electron NSIS `.exe`
artifacts are also accepted. Omit targets you do not build. The stable tag must
be `v1.2.3` or `desktop-v1.2.3`. Once ingested, a version code and its manifest
are immutable. When migrating a legacy app to a manifest, continue increasing
its existing inferred version code. Android codes and desktop codes remain
independent. Release selection looks for matching assets, so a desktop tag
never displaces the latest Android update.

The backend fetches every binary from the configured GitHub repository's asset
API, verifies size and SHA-256, and exposes the release only when every artifact
has verified. The client verifies the downloaded bytes again before extracting.
GitHub repository access and HTTPS are the desktop release trust root; checksums
are not an independent publisher signature. ZIP extraction rejects path
traversal, escaping symlinks, writes through symlinks and oversized expansions.

## Installation and self-update

Download a compatible build at `https://apps.gravitas.cool/#desktop-store` after
signing in. Extract ZIPs before launching on macOS/Windows. On Linux, make the
AppImage executable; a working AppImage/FUSE runtime and Electron-compatible
user namespaces are required. These builds do not have an Apple Developer ID,
notarization or Microsoft signing identity; the OS may require first-launch
approval. macOS application bundles receive the packager's local ad-hoc signing.

The store signs in through an isolated, sandboxed Gravitas browser window. Its
device credential is held in the main process and encrypted with the OS secret
service when available. On desktops without secure storage, credentials remain
in memory for that run. Central logout/revocation is checked by the same backend
device middleware as Android. The local app UI has no Node access, no remote
scripts and no access to the credential.

Apps install under the store's per-user data directory, in `apps/<id>/<code>/`.
Linux gets a desktop entry; Windows gets a Start menu shortcut; macOS gets an
app link in `~/Applications`. Previous version folders are retained, and existing
copies installed outside the store are not adopted or removed. There is no
automatic pruning or uninstall UI yet. Apps remain responsible for their data
directory and compatibility across versions.

Checks run at launch and every six hours while Private Store is open. Automatic
updates are enabled by default and can be disabled in Settings. Only installed
apps and the store are updated. An already running app continues using its old
binary until reopened from the store or shortcut. Private Store stages its own
new version and shows **Restart to update**; Electron relaunches directly into
the verified new executable, retaining the same store data and settings. The
catalog is cached so installed apps can still be opened during an outage.

## Merge, deploy and publish

Increase `version` and `versionCode` in `desktop/package.json`, and update
`desktop/changelog.json` in the same PR. The desktop release has its own
`desktop-vX.Y.Z` tag. Android continues using `vX.Y.Z` and the permanent Android
signing identity. Desktop publication explicitly leaves GitHub's latest Android
release selection unchanged.

`.github/workflows/ci.yml` runs backend and Android validation, desktop unit and
Electron flow tests, and all six native packages on PRs. On main, deployment
depends on all of those jobs. Desktop publishing also depends on those checks,
collects all six packages, validates the source changelog, uploads a draft with
the manifest, changelog and checksums, verifies GitHub's uploaded sizes/digests,
then publishes the stable release. There is no opt-in release flag. Reusing an
already published version skips publication; an interrupted draft fails with an
explicit error for inspection. Repository release polling ingests the published
artifacts automatically.

Development and validation use Node 24 and the exact lockfile:

```sh
cd desktop
npm ci
npm test
xvfb-run -a npm run test:e2e  # Linux
npm start
npm run package -- --linux --x64
cd ..
python3 scripts/test-desktop-release.py
python3 scripts/desktop-release.py validate
```

`tools/e2e/desktop.Dockerfile` supplies the Linux UI test dependencies. Desktop
flow tests exercise discovery, Gravitas enrollment handoff, app pages,
installation, updates, self-update staging/relaunch handoff and settings. Native
package builds run for both architectures on each operating system. Runtime
tests on actual macOS/Windows machines remain useful before expanding native
installer compatibility beyond the documented Electron formats.
