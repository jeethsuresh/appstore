# Signed release integrations

All five catalog apps use the same GitHub-hosted signing and automatic publishing process: [Daybreak](https://github.com/jeethsuresh/weather/blob/main/ANDROID_RELEASES.md), [Spacetime](https://github.com/jeethsuresh/spacetime/blob/main/ANDROID_RELEASES.md), [River](https://github.com/jeethsuresh/river/blob/main/ANDROID_RELEASES.md), [Still Launcher](https://github.com/jeethsuresh/launcher/blob/main/ANDROID_RELEASES.md), and [Private Store](../ANDROID_RELEASES.md).

Increase both the version name and Android version code, then merge or push to `main`. The **Signed Android release** workflow tests before loading the original signing key, verifies the permanent certificate, package, version and minimum Android version, and uploads the APK, checksums and release manifest. It verifies GitHub's uploaded asset digests before publishing automatically. The store receives new releases through its existing webhook or periodic ingestion.

Every repository uses a main-only `android-release` environment containing `RELEASE_KEYSTORE_BASE64`, `RELEASE_STORE_PASSWORD`, `RELEASE_KEY_ALIAS`, and `RELEASE_KEY_PASSWORD`. Private keys and passwords are encrypted secrets, never Git files or release assets. Preserve the original protected backup and existing certificate when maintaining them.

For a verification-only build of an already published version, run the workflow manually on `main` with `publish_release` disabled. It saves a verified signed artifact for 14 days. Existing releases, tags and assets are never overwritten.

Still Launcher retains its original preview signing identity (`app.still.launcher`, certificate SHA-256 `508184332d54eadbf5ad558c0b00c8350769cb386130c68eeec4aa8bf47c8b09`) so updates retain layout and settings. Published APKs use `StillLauncher-{tag}-android.apk`.
