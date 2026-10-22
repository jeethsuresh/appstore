# Private Store release workflow

**Build signed store release** (`store-release.yml`) builds and verifies a signed Android APK from `main`. The `android-release` environment contains this app's original keystore and credentials and allows only the `main` branch. The permanent signing certificate is unchanged. Existing local release processes remain available.

## Verify a signed build

Run the workflow from GitHub Actions with **create_draft** left unchecked, or:

```sh
gh workflow run store-release.yml --repo jeethsuresh/weather --ref main -f create_draft=false
```

This mode works with the currently published version and leaves existing releases/tags untouched. Download the signed APK and `SHA256SUMS` from the run's **daybreak-signed-android-VERSION-RUN_ID** artifact (retained 14 days). Verification artifacts are build evidence, not replacement assets for an existing release.

## Create a release

Increase both the version name and build number in `pubspec.yaml`, commit and merge to `main`, then run with **create_draft** checked:

```sh
gh workflow run store-release.yml --repo jeethsuresh/weather --ref main -f create_draft=true
```

The workflow refuses an existing release or tag, analyzes/tests with the pinned Flutter SDK, builds with the original key, and checks the APK's package, version name/code and established signing certificate. It saves a signed artifact and creates a draft release with the APK and checksums. Review and publish the draft to trigger Private Store ingestion through the existing signed webhook or polling. Published releases are never overwritten.

## Signing credentials

The environment has `RELEASE_KEYSTORE_BASE64`, `RELEASE_STORE_PASSWORD`, `RELEASE_KEY_PASSWORD` and `RELEASE_KEY_ALIAS`. Values were provisioned from the original protected local signing configuration; no key rotation was performed. GitHub encrypts the secrets, and the workflow loads them only after analysis/tests. The temporary keystore and signing properties are removed immediately after the build, including on failure, before artifact upload. Actions and the Flutter checkout are pinned to exact commits. Signing material is excluded from artifacts and source.

Keep a secure private backup of the original keystore and credentials. Future updates require that same key. Do not replace it with a generated or debug key.

Package: `dev.jeeth.local_weather`. Certificate SHA-256: `18cb4dbf5d8b3ed41336acd2c1f6dc12bd7aba2d8f5a6dcaf9339e42b6af683a`.
