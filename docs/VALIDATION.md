# Validation

Validation completed October 21–22, 2026, including local deployment tests, the live Gravitas Kubernetes deployment, web login and native Gravitas sign-in.

## Backend

`go test -race -cover ./...` and `go vet ./...` pass. Tests cover enrollment under concurrency, invitation expiry/exhaustion, credential hashing, revocation, protected catalog/APK access, range responses, malformed requests, HMAC webhooks, version immutability, package/certificate/digest mismatches, cache reuse/repair, trust-configuration changes, transient retries, cancellation, and migrations that preserve enrolled devices.

With `TEST_APK`, `AAPT` and `APKSIGNER`, the real signed Daybreak APK is verified and a modified copy is rejected. `LIVE_GITHUB=1` downloads and verifies current releases in temporary storage.

Verified live assets:

- Daybreak 1.4.2 / version code 7 / SHA-256 `8861bdb1cb7aa6d51983f0d7fcf2cf728d0fca73f473bc53d77d4822175ccec8`.
- Spacetime 0.2.0 / version code 2 / SHA-256 `03992b48f7d3f9adf46272b4b3b7cc13fa1b6e137813312fb7daa957a8a87f74`.

A separate native process smoke test verified startup, HTTP administration/enrollment, immediate revocation, and graceful shutdown.

## Android

Debug/release builds, JVM version/URL/digest tests, lint, and emulator encryption/launch tests pass. Release identity is checked by `scripts/verify-android.py` against the permanent public certificate and checked-in package/version.

The full distribution test uses the actual enrollment form and Install button. It passes through HTTPS enrollment, AndroidKeyStore persistence, both real signed APK downloads, initial Daybreak 1.4.1/code 6 installation, ingestion of 1.4.2/code 7, a WorkManager upgrade with the activity closed, and device revocation. Android reports the isolated store package as installer of record. Android/Play Protect prompts were approved normally; device protection was not disabled.

To repeat on a disposable emulator:

```sh
export JAVA_HOME=/path/to/jdk
export ANDROID_HOME=/path/to/android-sdk
python3 tools/e2e/run.py --serial emulator-5554
```

The test refuses to remove an existing Daybreak installation unless `--reset-test-daybreak` is explicitly supplied. It uses `dev.jeeth.privatestore.testbed` and an ephemeral CA trusted only by the `e2e` variant. Production/debug trust settings are unchanged. WorkManager's real worker is exercised immediately; the test does not wait six hours or claim to prove every OEM's background timing/installation policy.

## Deployment

The production image built and ran as UID 10001 with a read-only filesystem and dropped capabilities. It ingested both real APKs using its packaged APK verification tools, served authenticated downloads with matching hashes, denied anonymous access, and revoked a device.

The actual Compose/Caddy configuration passed health checks, HTTPS certificate verification, both live ingestions, authenticated APK downloads and revocation on temporary loopback ports. Test containers/volumes were removed afterward.

Repeat with a built image and Docker:

```sh
docker build -t localhost/private-store:verification .
python3 tools/e2e/container-smoke.py --engine docker
python3 tools/e2e/compose-smoke.py --compose docker
```

Podman is supported by the helpers. For Compose on macOS with Podman, pass `--podman --compose /path/to/docker-compose`.

GitHub Actions workflows pass static validation. Daybreak's integration PR passed its existing CI. The app signing workflows require protected environment secrets before they can execute; no private keys were uploaded during this work.

## Live Kubernetes deployment

The backend is live at `https://apps.gravitas.cool`. [Verify store CI](https://github.com/jeethsuresh/appstore/actions/workflows/ci.yml) runs backend race tests/vet, Android builds/tests/lint, Compose validation and production image publication. Rollouts use the successful main-branch build's immutable, anonymously pullable AMD64 image digest. The deployment script checks the intended cluster, readiness, public HTTPS health and anonymous API denial.

The live checks passed trusted HTTPS, HTTP-to-HTTPS redirection, anonymous API/download denial, invalid/exhausted invitation rejection, single-use enrollment, current Daybreak/Spacetime ingestion, full authenticated APK hashes, range responses, webhook HMAC validation, and immediate revocation. GitHub delivered signed pings successfully for both configured release webhooks.

A real pod replacement preserved the device credential, catalog release identities and APK storage. A temporary unprivileged pod in another namespace resolved the store Service but could not connect; it was deleted afterward. Existing RSS and Spacetime health endpoints remained healthy, and Office retained its 401 access gate. The store is Ready with no container restarts, matching 100m/256Mi requests and limits, a read-only non-root container and retained storage. Runtime secrets and the owner's unused enrollment invitation remain outside Git with mode 0600.


## Gravitas portal and Android 0.2.0

Backend tests additionally cover pinned issuer/redirect/identity validation, native code replay and invalid proof rejection, account-bound device creation without an invitation, linking/rotating existing enrollment, immediate revocation, public Android domain association and escaped browser fallback. The public app-page tests cover trusted release metadata, removed/unknown app 404s, escaping and sign-in return paths. The complete Go race suite and vet pass.

Android JVM PKCE/state/URL tests, release lint and the permanently signed 0.2.0 build pass. A real emulator update from the signed 0.1.0 APK to 0.2.0 succeeds in place. Android's domain verifier reports `apps.gravitas.cool: verified` for the permanent package and certificate, without forcing verification state.

`tools/e2e/native-login.py` exercises the real app sign-in UI and deployed IAM/store endpoints with an existing test account. It checks cancellation, a fresh proof on restart, encrypted pending state after activity recreation, rejection of a mismatched callback state, account credential persistence, both real catalog apps, a signed Daybreak download and immediate revocation. The host harness handles the existing account password and browser HTTP session; only the public authorization URL and PKCE-protected callback cross the isolated test control channel. The verifier stays on Android. No account is created, and the test credential is revoked afterward. Browser page rendering/typing is not automated by this harness.

```sh
export JAVA_HOME=/path/to/jdk
export ANDROID_HOME=/path/to/android-sdk
python3 tools/e2e/native-login.py --serial emulator-5554 --iam-login-env /secure/path/existing-test-account.env
```

The private env file supplies `IAM_USERNAME` and `IAM_PASSWORD`. The harness requires a disposable emulator and uses only the isolated `.testbed` package and a temporary test-only CA. It neither changes production trust nor bundles credentials in an APK.

Live web verification visits both app pages, signs in through real Gravitas with profile-only scope, returns to the selected page, downloads the signed bootstrap APK with a matching SHA-256, checks range responses, logs out and confirms anonymous GET/HEAD/range requests are denied. Desktop and 320/390-pixel layouts were checked in the browser without horizontal overflow.
