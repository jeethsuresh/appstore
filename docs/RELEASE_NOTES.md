Private Store 0.3.1 fixes installed-version detection for Still Launcher.

- Still Launcher is now visible to the store on Android 11 and later. Once its current release is installed, the store shows **Up to date** and stops offering to install it again.
- **Check for updates** refreshes installed versions even when the server catalog has not changed.
- Release verification checks the compiled APK can see every configured catalog app, preventing another catalog addition from silently losing installed-version detection.

Install **appstore-android.apk** over your existing Private Store. The permanent package and signing certificate are unchanged; your login, apps and update settings remain in place. Existing stores can find this release through their own update checks. Android may request installation approval.

The Gravitas-gated download is at **https://apps.gravitas.cool**. Signing keys and private test credentials are excluded from the release.
