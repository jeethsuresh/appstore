# Work and News for Android

Work (`cool.gravitas.work`) loads https://work.gravitas.cool; News
(`cool.gravitas.news`) loads https://news.gravitas.cool. Both are built from
`webview-android`, with independent app storage, launcher icons and package IDs.
They use the hosted apps, including their existing Gravitas HTTPS sign-in flow.
They require internet access and Android 8.0 or later with Android System WebView.
This does not replace the Office and RSS desktop releases.

Android 14 and later use the system gesture's starting edge: left is Back,
right is Forward. Forward with no remaining history is a no-op. Cancelling a
gesture leaves navigation unchanged. Keyboard dismissal keeps Android's default
priority. Back at the root closes the activity. On Android 8–13, system Back keeps its usual behavior; use the toolbar for Forward.
Website history, including History API entries, is retained for Forward.
Completing a sign-in/account transition clears authentication navigation history.

The shell has no JavaScript/native bridge, file-system access, cleartext traffic,
third-party cookies or Android backups. It uses Android's file picker for uploads.
External HTTPS, email and phone links go to a suitable installed app. HTTPS
downloads open in the browser, which may need its own sign-in. Blob downloads
are not supported by this initial shell. No native background sync or push
notification service is included. Website storage and cookies persist between
launches; clearing Android app storage signs out that wrapper.

## Verify

Use JDK 17 and Android SDK 36 with build-tools 35.0.0:

```sh
python3 scripts/test-webview-release.py
python3 scripts/webview-release.py validate
cd webview-android
./gradlew --no-daemon assembleDebug testWorkDebugUnitTest testNewsDebugUnitTest lintWorkDebug lintNewsDebug
./gradlew --no-daemon connectedWorkDebugAndroidTest connectedNewsDebugAndroidTest
```

Instrumentation uses local WebView fixtures to exercise real page/history
navigation, Forward at the root, branching after Back, gesture cancellation,
and isolated storage. Release CI runs the fixtures before loading signing secrets.

## Publish and deliver

`webview-release.json`, `webview-android/app/build.gradle.kts` and
`webview-android/changelog.json` declare the stable version and increasing
Android version code. Update all three together for each shell release.

The main-only `android-release` environment signs both packages with the existing
permanent Private Store key. The pinned public certificate is checked before
building and in both finished APKs. Versioned `webview-vX.Y.Z` releases contain
`work-android.apk`, `news-android.apk`, `changelog.json`, `webview-release.json`
and `SHA256SUMS`. CI checks both APK identities and signatures, verifies uploaded
asset sizes and hashes in a draft, then publishes without changing GitHub's
latest release (which remains the Store APK). Existing versions are immutable.

Private Store discovers the matching assets through its existing release-list
fallback, verifies their certificate, package, version and checksums, and ingests
their shared machine-readable changelog. Both apps appear as Android entries,
with web pages at `/apps/work` and `/apps/news`. The existing `office` and `rss`
entries continue to deliver desktop packages from their original repositories.
The Store release includes Android package visibility for both wrappers.
