Private Store 0.1.0 provides personal Android app distribution with Daybreak and Spacetime as the initial catalog.

- Go/SQLite backend with local APK storage, GitHub release polling/webhooks, invitation enrollment and revocable device credentials.
- APK signature/package/certificate verification, authenticated downloads, version/digest protection and cached ingestion.
- Native Kotlin/Jetpack Compose client with encrypted enrollment, download progress, installation prompts and periodic updates.
- Docker/Caddy deployment, health checks, administration CLI, CI and optional signed-release workflows.

Download **appstore-android.apk** and verify it using **SHA256SUMS**. It is signed with the permanent store release key. Deploy the included backend separately, then enter its HTTPS address and a device invitation in the app.

Validation includes real GitHub APK ingestion, Android UI enrollment, initial installation, a background-worker upgrade from Daybreak 1.4.1 to 1.4.2, revocation, and the actual non-root/read-only container and Compose/Caddy HTTPS stack.

No hosted server is included. Android may require installation confirmation or a Play Protect scan, and controls background scheduling. Existing development-signed store APKs must be removed before installing this release. Private signing keys and credentials are not included in this release.
