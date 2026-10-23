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

GitHub Actions workflows pass actionlint and embedded-helper checks. Both app integration PRs passed their existing CI and were merged. Original app signing keys and passwords were locally validated, then provisioned as encrypted GitHub environment secrets restricted to `main`. Verification-only signed builds exercise the configured secrets without modifying published releases; see [integrations](INTEGRATIONS.md).

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


## Signed app release workflows

On October 22, both configured main-only GitHub workflows passed full analysis/tests, original-key signing, APK verification and artifact upload: [Daybreak](https://github.com/jeethsuresh/weather/actions/runs/39856942780) and [Spacetime](https://github.com/jeethsuresh/spacetime/actions/runs/39857018980). The downloaded artifacts were independently verified with `apksigner`, `aapt2` and SHA-256: original certificates, correct packages, Daybreak 1.4.2/code 7 and Spacetime 0.2.0/code 2. Each artifact contained only its APK and `SHA256SUMS`. Both runs removed temporary signing material successfully and skipped draft creation. The actual release preflight also refused both existing published versions during read-only checks. No published app version was replaced.


## Store self-update and native broker (October 22, 2026)

Private Store is included in the production catalog with its permanent package and certificate. Its own signed GitHub release webhook returned 200; polling remains the recovery path. The production catalog identity and gated self-update description pass backend race/vet tests.

A disposable Android 16 emulator replaced the running `dev.jeeth.privatestore.testbed` from version code 30000 to 30001 through the store’s real certificate/version verification and Android PackageInstaller path. After a fresh launch, the test decrypted the original device and broker vaults, found the existing Android account, retained the automatic-update preference and last-check value, verified the completed marker was removed, and found exactly one unfinished periodic update job. The fixture held Android’s normal battery constraint to keep deliberately fake credentials offline. The harness cleans only its disposable test package and restores battery simulation. Run `tools/e2e/self-update.py --serial emulator-N`; Android/Play Protect confirmation may be required. This exercises process replacement, not an accelerated six-hour timing claim.

Seven isolated broker device tests cover encrypted storage, nonce-bound results and caller rejection, concurrent single refresh, a rotated pair retained across a status outage, explicit revocation clearing credentials, local logout racing an in-flight refresh, provider access denial, and a stale device denial that must preserve a newly accepted login. Debug unit tests, release lint, permanent-key signed build and APK identity/signature checks pass.

The IAM coordinator’s live synthetic HTTP integration passed explicit native enrollment, scope-upgrade rejection, first-consent requirements, PKCE/callback/subject/family/replay checks, fixed refresh-family expiry, and central logout across store cookies/devices, Spacetime native sessions, pending codes and refresh credentials. An independent family remained valid. Actual signed cross-package emulator verification is recorded separately after completion; test credentials are never included in this report.
