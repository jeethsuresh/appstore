# Handoff

The codebase contains a working Go/SQLite distribution server and native Android client, with Daybreak and Spacetime integrated from their actual repositories and original release certificates.

The full HTTPS enrollment/download/install/background-upgrade/revocation path passed on Android 16. The production container and the actual Compose/Caddy stack passed live APK ingestion and private-download checks. See [validation](VALIDATION.md).

The store's permanently signed APK uses `android/release-certificate.sha256`. Its private key and credentials remain only in the owner's ignored local `.signing/` directory. Back up that directory securely. They are excluded from Git, container builds, source archives and release assets.

The optional Daybreak and Spacetime signed-release workflows are saved locally and proposed as draft PRs in their repositories. Existing app source, release keys and release processes were retained. Review/merge those PRs and configure their protected environment secrets if GitHub should build signed releases. Current published app releases already work with ingestion.

The backend is deployed at **https://apps.gravitas.cool** on the existing Gravitas Kubernetes cluster. It uses its own `appstore` namespace and retained 20Gi volume, with the existing shared Traefik ingress and wildcard TLS. Both app releases were ingested and downloaded over authenticated HTTPS; credentials/catalog survived pod replacement, revocation passed, and cross-namespace Service access was denied. Existing RSS, Spacetime and Office endpoints still respond normally.

Signed release webhooks are configured in both app repositories, and GitHub's ping deliveries returned 200. Polling remains enabled for recovery. The administrator/webhook environment remains outside this repository with mode 0600. Phones sign in with Gravitas; manual invitations remain available for independent stores. See [Kubernetes operations](../deploy/gravitas/README.md) for updates, persistence and backup instructions. CI publishes backend images; cluster rollout remains an explicit operation.


The web portal now links to dedicated Daybreak and Spacetime pages with features,
compatibility, current verified release information and installation guidance.
Visitors can browse the descriptions before login; Gravitas sign-in unlocks the
store APK download and returns them to the app page they were viewing.

The Android client signs in through the system browser using PKCE and verified
HTTPS App Links. Pending sign-in proofs and account-bound device credentials are
encrypted with AndroidKeyStore. Existing invitation enrollments survive the
update and can connect a Gravitas account in place. Sign-out revokes the device
credential; central Gravitas sign-in remains active. The app requests only the
profile name, never the password or central access/refresh tokens.
