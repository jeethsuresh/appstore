# Appstore on Gravitas

The backend runs at `https://apps.gravitas.cool` in namespace `appstore` on
`do-tor1-gravitas-k8s-services`. Enter that root URL in the Android client.
The API requires device enrollment; the public health endpoint is `/healthz`.

The October 21, 2026 deployment uses source commit
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
Image publication does not automatically deploy to Kubernetes. No cluster
administrator credential is stored in GitHub Actions.

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
The image contains the Daybreak and Spacetime package/certificate pins.
Both current repositories are public, so `GITHUB_TOKEN` is empty. A read-only
repository token can be added to the runtime Secret if private apps are added.

## Access and operation

Load `ADMIN_TOKEN` from the private environment into a trusted terminal and set
`STORE_URL=https://apps.gravitas.cool`. Use `scripts/storectl.py` to issue
single-use invitations, list/revoke devices or request an immediate sync.
Never give the admin token to a phone. An invitation lasts at most seven days;
enrolled device sessions last one year and can be revoked immediately.

Release polling runs at startup and every 15 minutes. App repository webhooks
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
