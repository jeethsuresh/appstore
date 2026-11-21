# Bazaar for desktop

The Electron client in `desktop/` uses the same Go service, Gravitas account,
GitHub release polling/webhooks, app pages and verified downloads as Android.
macOS, Windows and Linux builds support x64 and ARM64. Desktop notifications
are not needed. The first release included Private Store, RSS Reader and Email
on the operating systems/processors for which they have published binaries.
Apps without published desktop builds are not advertised as installable.

Linux x86-64 also has a Flatpak target, including SteamOS. All six existing
portable packages, including both Linux AppImages, remain available.

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
Flatpak Bazaar adds `format=flatpak` to select its own Flatpak artifact. This
only changes the `appstore` listing; managed apps keep their native packages.
Omitting `format` retains the existing one-artifact-per-target API contract.
For Mac distribution, set `macOSNotarizedFrom` to the desktop version code of
the first release verified for Developer ID and Apple notarization. Wait until
that release is ingested before enabling the cutoff. Earlier Mac releases stay
in the cache but cannot be downloaded, and future releases must pass the
publishing repository's notarization checks.
The public `/v1/desktop/apps` endpoint exposes desktop descriptions and release
metadata for public listings. Artifact downloads require a device credential.
The website's desktop bootstrap downloads require a checked Gravitas session.

For an account-only desktop listing, set `desktopRequiresSignIn: true` and keep
the release assets in a private repository readable by the backend's GitHub
reader. A signed-in Bazaar device uses `/v1/apps?platform=macos&arch=arm64`
(or its own platform and architecture) to discover the app and its existing
device credential to download it. Public catalog pages and legacy invite-only
devices omit the listing.

### Direct publishing of private macOS games

A private desktop game can send a signed universal Godot archive directly from
its release pipeline. To enable this source, configure a dedicated verifier
in the Bazaar backend environment. Derive the setting name by uppercasing the
catalog ID, replacing hyphens with underscores, and appending
`_PUBLISH_TOKEN_SHA256`. For example, a listing called `sample-game` uses
`SAMPLE_GAME_PUBLISH_TOKEN_SHA256`. The backend polls GitHub for all listings
without a configured publisher.

The pipeline holds a fresh 32-byte random token encoded as lowercase hex. Set
the backend verifier to the lowercase hex SHA-256 of that ASCII token, with
no newline. Each private game requires its own verifier. The upload credential
only authorizes publishing to the configured listing.

Send a multipart POST to `/v1/desktop/publish/<app-id>` with a Bearer token.
The first part, `manifest`, carries `desktop-release.json`. The second,
`archive`, carries the ZIP named in the manifest, up to 192 MiB. The two macOS
architecture records must reference one ZIP with the same size and SHA-256.
The filename is the catalog name with spaces removed, followed by
`-<version>-mac-universal.zip`. The ZIP contains the catalog name followed by
`.app`, and the embedded executable and pack use that same product name.

A first submission returns HTTP 201. A verified identical replay returns 200
and can repair a missing cached archive. Both responses carry `appId`,
`version`, `versionCode`, `sha256` and `size`. A modified or older version
returns 409. The signing pipeline must verify the Developer ID signature,
notarization and installed game before submitting its archive.

Existing Electron releases can be ingested without upstream changes: assets
named `*-mac-arm64.dmg`, `*-win-x64.exe` and `*-linux-x86_64.AppImage` (also
`x64`, `arm64`, `aarch64`, `amd64`) supply their GitHub SHA-256 digest. The
catalog name selects the default app entry; a unique top-level app entry is
detected when an existing native package uses a different executable name.
Windows NSIS payloads are extracted without running their installer or changing
an existing installation's uninstall registry. Legacy versions use
`major*1000000+minor*1000+patch`, with each component below 1000. Missing digests,
ambiguous targets and unsupported packages fail ingestion. The matching GitHub
release's body supplies the changelog for both legacy and manifest-based desktop
releases. Empty bodies fall back to the manifest's `changes` or a short legacy
version notice. Webhooks and polling refresh edited release notes while retaining
the immutable version and verified artifacts.

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
An additional Linux x64 artifact may set `"format":"flatpak"`, name a `.flatpak`
bundle, and use the Flatpak application ID as `entry`. Bazaar uses the permanent
ID `cool.gravitas.privatestore` and the `stable` branch. Artifact download and
bootstrap URLs add `?format=flatpak`; the existing authentication, size/SHA-256
checks and all-or-nothing release ingestion apply to this artifact too.
Within each desktop channel, discovery selects the highest stable numeric
`major.minor.patch` version among the first 200 releases, regardless of GitHub's
list order. Explicit tag syncs still select only the requested tag.

The backend fetches every binary from the configured GitHub repository's asset
API, verifies size and SHA-256, and exposes the release only when every artifact
has verified. Verified changelogs and desktop manifests are cached by GitHub asset identity, size and digest across polls and backend restarts, reducing anonymous API usage. New release discovery still runs on every poll. The client verifies the downloaded bytes again before extracting.
GitHub repository access and HTTPS are the desktop release trust root; checksums
are not an independent publisher signature. ZIP extraction rejects path
traversal, escaping symlinks, writes through symlinks and oversized expansions.

## Installation and self-update

Download a compatible build at `https://apps.gravitas.cool/#desktop-store` after
signing in. Extract ZIPs before launching on macOS/Windows. On Linux, make the
AppImage executable. Bazaar uses a static AppImage runtime with FUSE 3 support
starting in 0.1.12. On a system where FUSE mounting is unavailable, run the
AppImage with `--appimage-extract-and-run`. Published macOS builds use Apple
Developer ID and carry a stapled Apple notarization ticket. Windows builds may
require first-launch approval.

The macOS signer signs all thin/universal Mach-O files and nested bundles with
the selected identity and secure timestamps. Binary data resources, including
Chromium locale PAK files, are sealed by their enclosing app/framework signature.
Strict bundle verification rejects modified resources. The signer preserves the
keychain, entitlements and hardened runtime options; publication still requires
Developer ID verification, notarization and a native launch smoke test.

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

Installations need space for the verified download and the unpacked app at the
same time. Bazaar checks available space before downloading and unpacking ZIPs,
and leaves the existing app in place if an update runs out of space.

Checks run at launch and every six hours while Bazaar is open. Automatic
updates are enabled by default and can be disabled in Settings. Only installed
apps and the store are updated. An already running app continues using its old
binary until reopened from the store or shortcut. Bazaar stages its own
new version and shows **Restart to update**; Electron relaunches directly into
the verified new executable, retaining the same store data and settings. The
catalog is cached so installed apps can still be opened during an outage.
The 0.1.5 Mac bundles keep app resources unpacked so that 0.1.4 can install the
update. The 0.1.5 installer can extract packages that contain an Electron ASAR.

### Flatpak installation and updates

Download the x86-64 `.flatpak` bundle from the signed-in desktop downloads page
and open it with Discover. For a per-user command-line installation:

```sh
flatpak remote-add --user --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo
flatpak install --user ./PrivateStore-0.1.13-linux-x86_64.flatpak
flatpak run cool.gravitas.privatestore
```

The package uses Freedesktop 25.08 and the matching Electron BaseApp/Zypak.
Flatpak supplies GPU runtime extensions for Bazaar's own UI. Installed apps and
games run on the host through `flatpak-spawn --host`, using the host session's
environment and drivers. Sandbox library paths and XDG overrides are not
forwarded to those processes. Existing versioned installs, native app updates,
desktop shortcuts and broker sign-in continue to work.

Bazaar's own Flatpak updates use its existing authenticated catalog, schedule,
automatic-update setting and verified-download checks. They do not depend on
publishing Bazaar to Flathub. The verified bundle is staged in persistent app
storage, then installed transactionally into the same user/system installation
with `flatpak install --or-update --bundle`. The running version remains active
until **Restart to update**, which starts the new Flatpak deployment. Failed
transactions do not advance the installed version or pending restart. A system
installation can require the operating system's normal authorization to update;
per-user installation avoids administrator authorization. Named nondefault
system installations are currently unsupported.

Flatpak stores Bazaar's settings and credentials in its own per-user app data
directory; updates retain that directory. A first Flatpak install is independent
of an existing AppImage installation and does not migrate or delete its state.
Host shortcuts and broker discovery use the host's XDG data location. The
Flatpak explicitly grants host-command access for installing and launching
software, plus access to those two integration directories. It is not a security
boundary around the software Bazaar installs. The renderer retains its existing
Electron sandbox and credential isolation.

## Merge, deploy and publish

Increase `version` and `versionCode` in `desktop/package.json`, and update
`desktop/changelog.json` in the same PR. The desktop release has its own
`desktop-vX.Y.Z` tag. Android continues using `vX.Y.Z` and the permanent Android
signing identity. Desktop publication explicitly leaves GitHub's latest Android
release selection unchanged.

`.github/workflows/ci.yml` runs backend and Android validation, desktop unit and
Electron flow tests, and all six native packages on PRs. On main, deployment
depends on all of those jobs. Desktop publishing also depends on those checks,
collects all seven packages and uses the signed Mac packages from the main-only
`macos-release` environment. Each Mac job verifies the Developer ID signature
and stapled ticket after installing the ZIP, then smoke-tests the installed app.
Publication validates the source changelog, uploads a draft with
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
npm run package:flatpak  # Linux x86-64; requires flatpak-builder and the runtimes above
cd ..
python3 scripts/test-desktop-release.py
python3 scripts/desktop-release.py validate
```

Fedora build hosts should avoid Flatpak 1.18.2, whose `build-init --base`
regression is fixed in 1.18.3. This concerns the build tools, not the minimum
version required to install Bazaar. See [the upstream build issue](https://github.com/flatpak/flatpak/issues/6818).

`tools/e2e/desktop.Dockerfile` supplies the Linux UI test dependencies. Desktop
flow tests exercise discovery, Gravitas enrollment handoff, app pages,
installation, updates, self-update staging/relaunch handoff and settings. Native
package builds run for both architectures on each operating system. Linux
package tests check native FUSE launch and the extraction fallback on both CPUs.
Runtime tests on actual macOS/Windows machines remain useful before expanding
native installer compatibility beyond the documented Electron formats.

The additional `desktop-flatpak` CI job builds the x86-64 bundle and a disposable
newer version. `node test/flatpak-smoke.cjs` installs the first package in a
separate session, opens the packaged UI, installs and updates a native fixture,
launches it on the host, applies the verified newer Flatpak and relaunches it.
It verifies retained settings, broker discovery and isolation of library
environment variables. A pre-existing user installation is never replaced by
the test. Actual SteamOS hardware and GPU/game combinations require separate
compatibility checks.

Native Flutter Linux bundles can use ZIPs with an explicit root executable entry and adjacent `lib/` and `data/` directories. The same traversal, symlink, checksum and versioned-install checks apply to these ZIPs. River, Spacetime and Haven are universal catalog entries; Office, RSS Reader and Email are desktop-only.

Desktop Bazaar also supplies foreground Gravitas app sign-in. See
[DESKTOP_SIGN_IN.md](DESKTOP_SIGN_IN.md) for the protocol, account upgrade and
reusable Dart/Electron helpers.

## Thornwake

Thornwake is a separate, macOS-only desktop listing. Its shipped packages are
universal builds from Rail Yard. Bazaar discovers them in the Appstore
repository under `thornwake-desktop-vX.Y.Z`, using the same manifest and
download verification as its other desktop listings.

Update `desktop/thornwake-changelog.json` with the game version, its numeric
version code (`major * 1000000 + minor * 1000 + patch`), and user-facing notes.
The publisher validates and copies these notes into the release manifest.

Stage the headlessly tested universal app ZIP as `Thornwake-unsigned.zip` on
a draft with that tag. Dispatch **Sign and publish Thornwake** on main with
the version and SHA-256 of the staged ZIP. The workflow signs both
architectures in the existing macOS release environment, notarizes and
staples the app, tests the installed build headlessly, and publishes the
verified ZIP and desktop manifest. The draft input ZIP is removed before
publication.
