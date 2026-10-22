# Signed release integrations

Both app workflows are merged and their `android-release` environments contain the original signing keys and credentials, with access restricted to the `main` branch:

- Daybreak: [jeethsuresh/weather PR #1](https://github.com/jeethsuresh/weather/pull/1), package `dev.jeeth.local_weather`, ARM64 APK `daybreak-android-arm64.apk`.
- Spacetime: [jeethsuresh/spacetime PR #1](https://github.com/jeethsuresh/spacetime/pull/1), package `com.jeethsuresh.spacetime`, universal APK `spacetime-vVERSION-android.apk`.

Each existing keystore's certificate and private-key password were validated locally before provisioning. The permanent app signing certificates are unchanged. Existing source work, published releases, and local release processes were retained. Mirrored workflows, instructions and portable patches are in `integrations/`.

## Verify a signed build

Run **Build signed store release** on `main`, with `create_draft` unchecked. This analyzes/tests, builds and verifies a signed APK, then saves the APK and `SHA256SUMS` as a 14-day run artifact. It works with the current published version and leaves all releases and tags untouched.

```sh
gh workflow run store-release.yml --repo jeethsuresh/weather --ref main -f create_draft=false
gh workflow run store-release.yml --repo jeethsuresh/spacetime --ref main -f create_draft=false
```

Verification artifacts are build evidence, not replacements for already-published APKs.

The first signed runs passed, and their downloaded APKs independently passed signature, permanent-certificate, package/version and SHA-256 verification:

- [Daybreak signed GitHub build](https://github.com/jeethsuresh/weather/actions/runs/39856942780) — 1.4.2 / code 7.
- [Spacetime signed GitHub build](https://github.com/jeethsuresh/spacetime/actions/runs/39857018980) — 0.2.0 / code 2.

## Create an app release

Increase both the version name and build number in the app's `pubspec.yaml` (`frontend/pubspec.yaml` for Spacetime), commit and merge to `main`, then run with `create_draft=true`. The workflow refuses existing releases/tags, builds with the original key, verifies the package/version/certificate, saves the verified artifact and creates a draft release with checksums. Review and publish the draft to trigger the existing store webhook or periodic ingestion. Published versions are never overwritten.

## Signing maintenance

Each environment has four encrypted secrets:

- `RELEASE_KEYSTORE_BASE64`
- `RELEASE_STORE_PASSWORD`
- `RELEASE_KEY_PASSWORD`
- `RELEASE_KEY_ALIAS` (`spacetime-release` for Spacetime)

Use the existing app key for all future updates. Keep the original local keystores and credentials backed up securely. The release workflow loads signing material only after analysis/tests and removes temporary files immediately after the build, including on failure, before artifact upload. Actions and the Flutter SDK are pinned to exact commits. No signing material appears in Git, patches, artifacts or release assets.

The secrets were provisioned on October 22, 2026. Environment branch restrictions prevent other branches and tags from receiving them. Do not broaden that allowlist merely to run a branch build.

## Store client workflow

The separate store client's optional signed-build workflow uses these `android-release` secrets:

- `STORE_KEYSTORE_BASE64`
- `STORE_STORE_PASSWORD`
- `STORE_KEY_ALIAS`
- `STORE_KEY_PASSWORD`

This is independent of the two app environments above. The store client continues to use the protected local key and `scripts/release.py --publish`; those local builds verify `android/release-certificate.sha256`. No store-client key upload was needed to configure Daybreak and Spacetime.
