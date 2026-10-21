# Validation

Validation completed on October 21, 2026. The deployable server was tested locally; no public deployment was created.

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
