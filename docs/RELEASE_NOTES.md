# Private Store 0.4.1

Private Store now recognises installed copies of Atlas, so its button correctly shows Installed or Update. Small APK downloads display in bytes or kB instead of rounding to 0.0 MB on Android and the web.

Catalog package visibility is checked before and after the Android build, so adding an app without its required Android visibility declaration fails CI.

Install **appstore-android.apk** over the existing Private Store. The package and permanent signing certificate are unchanged. GitHub now builds, verifies and publishes new signed versions automatically.
