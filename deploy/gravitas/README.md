# Appstore on Gravitas

The backend runs at `https://apps.gravitas.cool` in namespace `appstore` on
`do-tor1-gravitas-k8s-services`. The Android client connects here by default.
Gravitas sign-in creates an account-bound device credential; the public health endpoint is `/healthz`.

The initial October 21, 2026 backend deployment used source commit
`4e1c30186829f8ffc1df90ad501734caabc69583` and image
`ghcr.io/jeethsuresh/appstore@sha256:3eb40fac0f2cf2bff46e08cd4f64860dc105bb3f0b8f905173804c12d2700ba8`.
Live verification covered both APK downloads, enrollment/revocation, signed
GitHub webhook delivery, pod replacement with retained credentials/catalog, and
cross-namespace network isolation. See [validation](../../docs/VALIDATION.md).

The existing Traefik ingress, wildcard DNS and automatically renewed wildcard
certificate provide HTTPS. This deployment does not create another load balancer,
copy certificate keys, or change DNS and other applications.

The `Verify store` workflow tests the backend, validates Compose and builds the
production image. Main-branch builds publish an AMD64 image to
`ghcr.io/jeethsuresh/appstore:<commit>`. Deployments use its immutable digest.
Successful main-branch builds deploy their immutable image digest to Kubernetes after both CI jobs pass. See [automatic deployment](AUTOMATIC_DEPLOYMENT.md) for scoped credentials, the enable switch and release verification. No cluster administrator credential is stored in GitHub Actions.

## First deployment

Use the cluster's matching Kubernetes client (currently 1.37) and administrator
context. Inspect `bootstrap.json` and `resources.json` before applying them.
Create deployment credentials outside the repository and preserve them privately:

```sh
kubectl --context do-tor1-gravitas-k8s-services apply -f deploy/gravitas/bootstrap.json
python3 scripts/init-env.py --domain apps.gravitas.cool --output /secure/path/appstore.env
kubectl --context do-tor1-gravitas-k8s-services -n appstore create secret generic appstore-runtime \
  --from-env-file=/secure/path/appstore.env
deploy/gravitas/deploy.sh ghcr.io/jeethsuresh/appstore@sha256:IMAGE_DIGEST
```

Set `KUBECTL=/path/to/kubectl` when selecting a different client binary.
`deploy.sh` validates the intended cluster endpoint, waits for readiness, checks
public HTTPS health, and requires anonymous catalog access to return 401.
The image contains Daybreak, Spacetime, Private Store and Still Launcher package/certificate pins.
All configured repositories are public, so `GITHUB_TOKEN` is empty. A read-only
repository token can be added to the runtime Secret if private apps are added.

## Android Gravitas sign-in

The native client uses the same pinned issuer and app origin as the website,
with a distinct fixed `/auth/android/callback` redirect and S256 PKCE. Explicit
native broker enrollment requests `profile.name native.broker`; legacy store-only
flows request `profile.name`. The server exchanges the one-use code with IAM and
creates or links a revocable device credential bounded by the IAM family expiry.
Only the explicit broker exchange returns the store-origin IAM bundle to the
native store, which encrypts it in AndroidKeyStore. The server persists only an
encrypted session-status proof and checks IAM on each protected request. Existing
Gravitas enrollments without a status proof must sign in again; invitation-only
devices remain independent. See [Android broker](../../docs/ANDROID_BROKER.md).

`/.well-known/assetlinks.json` publishes the permanent store signing certificate
for Android's verified HTTPS links. Preserve that certificate on every client
update. The app persists pending sign-in proofs encrypted in AndroidKeyStore;
cancellation clears them. No IAM registration or additional client secret is needed.

## Access and operation

Load `ADMIN_TOKEN` from the private environment into a trusted terminal and set
`STORE_URL=https://apps.gravitas.cool`. Use `scripts/storectl.py` to issue
single-use invitations, list/revoke devices or request an immediate sync.
Never give the admin token to a phone. An invitation lasts at most seven days;
invitation-only device sessions last one year and can be revoked immediately.
Gravitas-linked devices also require a live IAM family proof; central logout
revokes access immediately. IAM outages fail closed with 503 and retain credentials.

Release polling runs at startup and every 15 minutes. Configured app repository webhooks, including the store itself,
use `https://apps.gravitas.cool/webhooks/github`, JSON, release events, and
the runtime `WEBHOOK_SECRET`. The server accepts only its configured repositories
and re-fetches GitHub's authoritative release metadata. Polling recovers missed
events. Preserve runtime credentials across rollouts; replacing them deliberately
requires updating webhook secrets and restarting the pod.

One replica with Recreate owns the SQLite database and APK cache on a retained
20Gi block-storage claim. Rollouts briefly interrupt the service. The container
runs as UID 10001 with a read-only root, no capabilities or Kubernetes API token,
and health probes. CPU and memory requests/limits match at 100m and 256Mi.
The JVM and Go memory budgets leave room for APK signature verification within
the pod's limit. Temporary storage is bounded. Network policy allows inbound
connections only from Traefik, DNS to CoreDNS, and outbound public HTTPS for
GitHub/API assets. The Service has no public node port or load balancer.

To roll back, pass an earlier verified image digest to `deploy.sh`. Database
migrations are forward-only; confirm an older binary supports the current schema.
Persistent retention is not a backup. Stop the app before snapshotting/copying
the database and APKs together, or use SQLite's online backup API with a consistent
APK snapshot. No automatic offsite backup is configured.

## Browser downloads with Gravitas

The root page now provides a responsive download and installation UI. Sign-in
and registration use the same pinned-issuer PKCE flow as News and Office.
The server exchanges codes directly with `https://gravitas.cool`; passwords and
IAM access tokens are never exposed to the page. Host-only Secure/HttpOnly
cookies hold encrypted eight-hour app sessions. Local logout clears the app
session and leaves the central Gravitas login active. Protected downloads and
linked-device requests check IAM session status without a positive authorization
cache. The visible portal checks status every 15 seconds and on focus.

`GRAVITAS_REQUIRED=true`, `GRAVITAS_ISSUER=https://gravitas.cool`,
`GRAVITAS_APP_URL=https://apps.gravitas.cool` and a persistent 32-byte base64url
`GRAVITAS_SESSION_KEY` in `appstore-runtime` enable the UI. Missing configuration
never enables anonymous downloads. The existing enrolled-device API, invitations
and administrative credentials continue to work independently.

`GET /download/appstore.apk` requires a valid app session, including for HEAD
and Range requests, and serves the APK directly with private/no-store headers.
The image fetches the signed v0.4.0 client at build time, verifies its pinned
SHA-256, and verifies it again before starting an IAM-enabled server. Future
client releases require updating the URL/hash in Dockerfile and the hash/size
and version in `backend/internal/store/web.go`; the displayed version/size use those values. `CLIENT_APK` can select the same verified
file for native development. The public upstream GitHub release remains public;
this gate protects downloads served by apps.gravitas.cool.

Network policy additionally allows HTTPS through the existing shared Traefik
path to IAM. No IAM registration, new public service or new ingress is needed.

## App collection pages

`/apps/daybreak`, `/apps/spacetime`, `/apps/appstore` and `/apps/launcher` describe the configured apps, including
features, device requirements, local storage and network behavior. The home page
links to them through the collection cards and the store illustration. Descriptions
are public; the store APK remains behind the existing Gravitas session gate.
Signing in from a detail page returns to that page, with its download and setup
controls enabled. No new identity permissions are requested.

The editorial descriptions live in `backend/internal/store/web_catalog.go` and
are grounded in each app's source documentation. A page is published only while
its app is present in the store configuration. Release versions, sizes and Android
minimums come from the verified SQLite catalog; metadata verified under an old
package or certificate is hidden. Release notes are escaped as plain text. New
pages share the existing CSP, no-store policy, host-only session and logout flow.

## Private shared deployment tooling

The deployment job checks out `jeethsuresh/gravitas-deploy` at a full commit SHA,
using this repository’s read-only tooling key from the main-only `gravitas`
environment. `.gravitas/deploy.json` selects this app’s existing release script;
its migrations, manifests and public checks remain in this repository. The shared
action checks namespace permissions, exact image digest, readiness and HTTPS,
and removes its temporary kubeconfig on failure or success. Build/test jobs are
unchanged. Review a new tooling pin before updating it. New-app onboarding is
documented in the private tooling repository.
