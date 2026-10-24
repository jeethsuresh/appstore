# Signed Android releases

Private Store uses the same release process as the other Gravitas Android apps. Signing runs on GitHub-hosted runners; a local machine never needs the private key.

Increase both the stable version and Android version code in `android/app/build.gradle.kts`, then merge or push to `main`. **Signed Android release** automatically tests, builds, verifies and publishes the new version. An already published version is skipped. Existing tags, drafts and published assets are never overwritten. A lower version or non-increasing version code fails before signing.

The `android-release` GitHub environment is restricted to the `main` branch. It contains four encrypted secrets: `RELEASE_KEYSTORE_BASE64`, `RELEASE_STORE_PASSWORD`, `RELEASE_KEY_ALIAS`, and `RELEASE_KEY_PASSWORD`. Preserve the original key and certificate when restoring these secrets. They are loaded only after tests pass and removed before artifacts are uploaded; they are never committed or included in releases. Keep the protected original key backup separately.

The workflow can also run manually from `main`. Leave **publish_release** enabled to publish an unused version, or disable it to build and verify an artifact without changing releases. If publishing fails after uploading a draft, inspect that draft instead of overwriting it.

Every release includes the signed APK, `SHA256SUMS`, and `release-manifest.json` with the source commit, package, version/code, certificate and APK digest. GitHub's uploaded asset digests and sizes must match before the draft is published. `android-release.json` pins the app identity; the workflow pins its build tools and GitHub actions.

Package: `dev.jeeth.privatestore`. Permanent certificate SHA-256: `e70b5b54a0d174f4ad8867889f6481a2b193dc9c130630f321563e12f3293793`.

## Required changelog

Each new version must include an updated tracked `changelog.json` in the same PR as the explicit version/code bump. The JSON object has exactly four fields: `schemaVersion` (integer `1`), `version` (the stable source version), `versionCode` (the source Android version code), and `changes` (1–50 nonempty plain-text strings, each at most 2000 UTF-8 bytes). The complete file is limited to 32000 UTF-8 bytes. Keep entries concise and useful to people updating the app.

Every `main` push checks the source version and changelog. A new version automatically builds and publishes; an already published version is skipped. App-code changes intended for Android users require an explicit version/code bump before merge. CI validates the changelog before signing, includes it in `SHA256SUMS` and `release-manifest.json`, verifies the uploaded asset digest, and uses its entries as GitHub release notes. Private Store validates this JSON against the verified APK version and presents its entries as release notes on Android and the web. Legacy releases without the JSON asset retain their GitHub release text.
