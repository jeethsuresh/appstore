# Private Store

A personal Android app distribution platform with Gravitas account sign-in, optional invitation-based device enrollment, GitHub release ingestion, and a native Kotlin/Jetpack Compose client. Daybreak and Spacetime are the first two supported apps.

The signed [0.2.0 release](https://github.com/jeethsuresh/appstore/releases/tag/v0.2.0) updates 0.1.0 in place.

Visit [apps.gravitas.cool](https://apps.gravitas.cool) and sign in with Gravitas to download the signed **appstore-android.apk**. Open the app and sign in with Gravitas; no separate device invitation is needed. The app connects automatically to `https://apps.gravitas.cool`; see [Kubernetes deployment](deploy/gravitas/README.md). Docker/Caddy remains available for independent installations.

Browse the collection at [apps.gravitas.cool](https://apps.gravitas.cool), with dedicated [Daybreak](https://apps.gravitas.cool/apps/daybreak) and [Spacetime](https://apps.gravitas.cool/apps/spacetime) pages. App descriptions are public; downloading the store requires Gravitas sign-in. Each page explains features, Android compatibility, local data and optional connections, with current verified release details.

## Included

- Go backend, SQLite, and local APK storage.
- Browser-based Gravitas sign-in with PKCE, verified Android App Links, account-bound device credentials, and immediate revocation. Optional device invitations support independent stores.
- Authenticated catalog and APK downloads; GitHub credentials stay on the server.
- Release polling at startup/every 15 minutes and HMAC-validated GitHub webhooks. Unchanged assets are cached; transient GitHub failures retry, and missing cached files are repaired.
- APK signature verification, pinned release certificates and package IDs, digest/size checks, real APK metadata extraction, and immutable increasing version codes.
- Native Android enrollment, AndroidKeyStore credential encryption, release notes, download progress, installation prompts, and six-hour WorkManager update checks for installed apps.
- Kubernetes/Traefik and Docker/Caddy deployment with HTTPS, persistent storage, non-root/read-only backend, health checks, an administration CLI, CI, and signed-build workflows.

Background updates request installation without interaction when Android permits. Otherwise a notification opens Android's confirmation. Enable installation permission and notifications. Android controls scheduling and whether confirmation or a Play Protect scan is needed.

## Supported apps

- **Daybreak:** [jeethsuresh/weather](https://github.com/jeethsuresh/weather), `dev.jeeth.local_weather`, `daybreak-android-arm64.apk`. ARM64 phones; existing release certificate retained.
- **Spacetime:** [jeethsuresh/spacetime](https://github.com/jeethsuresh/spacetime), `com.jeethsuresh.spacetime`, `spacetime-{tag}-android.apk`. Existing release certificate retained; universal APK.
- **Private Store:** [jeethsuresh/appstore](https://github.com/jeethsuresh/appstore), `dev.jeeth.privatestore`, `appstore-android.apk`. The store updates itself through this catalog, using its permanent release certificate.

Their application source is Flutter; this store's client is native Kotlin. Repository visibility is independent of store authentication: the platform restricts its API/cached downloads and can ingest private repositories, but it does not make an upstream public GitHub release private.

## Deploy

For the Gravitas Kubernetes cluster, use the [deployment files and operational notes](deploy/gravitas/README.md). The following instructions cover an independent Docker/Caddy installation.

Requires Docker Compose on a Linux host, a hostname pointing to it, and inbound ports 80/443:

```sh
python3 scripts/init-env.py
# Edit .env: set STORE_DOMAIN and, for private repos, GITHUB_TOKEN.
docker compose up -d --build
docker compose logs -f store
```

Caddy obtains HTTPS certificates. The backend has no public host port. `.env` is created with mode 0600; never commit it. A fine-grained GitHub token only needs read access to repository contents. Public repositories also work anonymously, subject to GitHub rate limits.

The `store_data` volume holds the SQLite database and APKs. Back it up while the service is stopped, or use SQLite's online backup API plus an APK snapshot. Preserve Caddy's certificate volume. Restore with the same app configuration and signing pins. Old APKs are retained so clients with an earlier catalog can finish downloading; storage pruning is not included.

## Connect a phone

At apps.gravitas.cool, install the signed store and choose **Sign in with Gravitas**. Existing enrollments remain valid; **Connect Gravitas account** links them in place. Creating a Gravitas account follows the central login system's registration policy.

Independent stores can continue using device invitations. In a trusted terminal, export `ADMIN_TOKEN` from your private deployment environment without placing it in shell history:

```sh
export STORE_URL=https://store.example.com
python3 scripts/storectl.py sync daybreak
python3 scripts/storectl.py sync spacetime
python3 scripts/storectl.py sync appstore
python3 scripts/storectl.py invite --label 'My phone' --uses 1 --hours 24
python3 scripts/storectl.py devices
python3 scripts/storectl.py revoke DEVICE_ID
```

For an independent store, choose **Use an invitation or another store**, enter its HTTPS address/invitation/device name, and enroll. Enable app installation and notifications, then install the apps you want. Background checks update installed apps only. Certificates must match existing installations; a development-signed copy may need to be removed once after backing up any app data.

Admin tokens never belong on phones. Device sessions expire after one year; sign in again with Gravitas, or use a fresh invitation for an independent store. All authorized devices share the same catalog. Gravitas manages user accounts; the store requests only `profile.name`.

## GitHub webhooks

In each app repository, add a webhook with payload URL `https://YOUR_HOST/webhooks/github`, JSON content type, secret equal to `WEBHOOK_SECRET`, and **Releases** events. The server validates `X-Hub-Signature-256`, accepts configured repositories only, and fetches assets through GitHub's API. Event-supplied download URLs are never trusted.

Polling recovers missed deliveries, restarts, and assets uploaded after publication. Stable releases are supported; prereleases and historical release browsing are not. An administrator can explicitly ingest a stable tag using `storectl.py sync APP --tag TAG`; version codes still cannot decrease.

## Build the Android client

Requires JDK 17+, Android SDK 36, and network access for dependencies:

```sh
cd android
./gradlew assembleDebug testDebugUnitTest lintDebug
./gradlew connectedDebugAndroidTest  # disposable emulator recommended
```

Production releases use the permanent store certificate in `android/release-certificate.sha256`. The original protected key remains in the owner's ignored `.signing/` directory and must be backed up securely. No private signing material is stored in Git or release assets. Debug APKs have a different key; switching from debug to release requires reinstalling the store and enrolling again.

For a new independent fork, create its own key with `scripts/create-signing-key.py`; never replace the original project's key for an update. The helper writes an ignored protected signing configuration. Local signed builds use:

```sh
python3 scripts/build-android.py
python3 scripts/verify-android.py android/app/build/outputs/apk/release/app-release.apk
```

Alternatively supply `STORE_KEYSTORE`, `STORE_STORE_PASSWORD`, `STORE_KEY_ALIAS`, and `STORE_KEY_PASSWORD` directly to Gradle. Release builds reject missing signing configuration. The optional GitHub signed-client workflow uses the same key through `android-release` environment secrets; see [integration notes](docs/INTEGRATIONS.md).

## Backend development

Requires Go 1.25+, a C compiler for SQLite/CGO, Java, `apksigner`, and `aapt`/`aapt2`. The container supplies the APK tools.

```sh
cd backend
go test -race -cover ./...
go vet ./...
go build -o ../store ./cmd/store
cd ..
# Set ADMIN_TOKEN and WEBHOOK_SECRET (at least 32 characters each).
./store
```

Native defaults: `LISTEN_ADDR=127.0.0.1:8080`, `DATA_DIR=data`, `APPS_CONFIG=config/apps.json`, `POLL_INTERVAL=15m`. Set `AAPT`, `APKSIGNER`, and `JAVA_HOME` to SDK tools when needed. Optional `TLS_CERT_FILE`/`TLS_KEY_FILE` enable direct HTTPS; Caddy normally terminates TLS. `POLL_INTERVAL=0s` disables startup/periodic polling while retaining webhook and manual sync. Configuration changes require restarting the backend.

See [API](docs/API.md), [validation](docs/VALIDATION.md), [app integrations](docs/INTEGRATIONS.md), and [release notes](docs/RELEASE_NOTES.md).

To add another app, configure its repository, asset pattern, package and certificate pin, declare the package in Android's `<queries>`, and rebuild the client. This initial single-instance platform does not implement per-user catalogs, APK pruning, split-APK installation, signing-key rotation, or device-owner installation.
