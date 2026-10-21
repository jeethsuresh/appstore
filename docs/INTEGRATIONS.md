# Signed release integrations

The initial apps were integrated using their actual repositories, manifests, existing signed release assets, and release certificates. Their existing application code and signing keys were preserved.

- Daybreak: `jeethsuresh/weather`, package `dev.jeeth.local_weather`. The optional workflow is proposed in [PR #1](https://github.com/jeethsuresh/weather/pull/1); its existing GitHub CI passed. The current local release script and published-APK verification workflow remain available.
- Spacetime: `jeethsuresh/spacetime`, package `com.jeethsuresh.spacetime`. The optional workflow is proposed in [PR #1](https://github.com/jeethsuresh/spacetime/pull/1). Its existing release signing configuration is retained.

Mirrored workflow files and portable patches are in `integrations/`. Both pull requests are drafts for review; they have not been merged. Current published releases can already be ingested without merging them.

## App workflow setup

For each app, configure a GitHub environment named `android-release` with:

- `RELEASE_KEYSTORE_BASE64`: base64 of that app's existing keystore
- `RELEASE_STORE_PASSWORD`
- `RELEASE_KEY_PASSWORD`
- `RELEASE_KEY_ALIAS` (Spacetime requires `spacetime-release`)

Use the existing app key. The workflow verifies the established signing fingerprint, package, version name and version code; an unrelated key fails. Adding the workflow does not upload any private key or password. Those secrets were not automatically provisioned.

Bump the version name and build number in `pubspec.yaml`, commit on `main`, and run **Build signed store release**. It analyzes/tests with the existing Flutter SDK, builds and verifies the APK, and creates a draft release with checksums. Review and publish the draft to trigger store ingestion. Existing release tags are never overwritten.

Daybreak retains `daybreak-android-arm64.apk`. Spacetime retains `spacetime-vVERSION-android.apk`. A reused version code cannot replace an existing verified release.

## Store client workflow

The store's optional signed-build workflow uses these `android-release` environment secrets:

- `STORE_KEYSTORE_BASE64`
- `STORE_STORE_PASSWORD`
- `STORE_KEY_ALIAS`
- `STORE_KEY_PASSWORD`

It verifies against `android/release-certificate.sha256` and uploads a signed APK artifact. Local builds keep the key on the original machine. `scripts/release.py --publish` publishes a tested local build and checksums after source/identity verification; it never uploads signing material.
