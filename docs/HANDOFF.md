# Handoff

The codebase contains a working Go/SQLite distribution server and native Android client, with Daybreak and Spacetime integrated from their actual repositories and original release certificates.

The full HTTPS enrollment/download/install/background-upgrade/revocation path passed on Android 16. The production container and the actual Compose/Caddy stack passed live APK ingestion and private-download checks. See [validation](VALIDATION.md).

The store's permanently signed APK uses `android/release-certificate.sha256`. Its private key and credentials remain only in the owner's ignored local `.signing/` directory. Back up that directory securely. They are excluded from Git, container builds, source archives and release assets.

The optional Daybreak and Spacetime signed-release workflows are saved locally and proposed as draft PRs in their repositories. Existing app source, release keys and release processes were retained. Review/merge those PRs and configure their protected environment secrets if GitHub should build signed releases. Current published app releases already work with ingestion.

Deployment files are ready, but no server or domain was provisioned, as requested. At deployment time generate `.env`, set the hostname and optional GitHub read token, start Compose, and create the first device invitation. Webhooks are optional; polling recovers missed releases.
