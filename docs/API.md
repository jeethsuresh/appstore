# HTTP API

All deployment traffic uses HTTPS. JSON errors use `{"error":"message"}`. Tokens belong in `Authorization: Bearer TOKEN`, never query parameters.

## Gravitas sign-in

When federation is configured:

- `POST /v1/auth/gravitas/start`: `{"state":"43-character base64url state","codeChallenge":"43-character S256 challenge"}` returns `authorizationUrl` and `redirectUri`. The browser visits the pinned Gravitas issuer with only `profile.name` scope. The native redirect is exactly `https://apps.gravitas.cool/auth/android/callback`.
- `POST /v1/auth/gravitas/exchange`: `{"code":"one-use IAM code","codeVerifier":"43-character verifier","deviceName":"My phone"}` returns `deviceId`, `token`, Unix `expiresAt`, and `account`. The server validates the code, PKCE proof and issuer profile, then issues a one-year revocable device credential. It never returns IAM tokens. An optional valid existing device bearer credential links and rotates that enrollment in place; otherwise a new device is created. Invalid/replayed codes return 401, malformed input 400, unavailable federation 503, and saturated exchanges 429.
- `GET /.well-known/assetlinks.json`: public association for the permanent signed Android package. `GET /auth/android/callback` provides a browser fallback to the app; it never contains a device token.

The Android client checks the exact callback origin/path, unique constant-time matching state and a ten-minute expiry before exchanging. Its encrypted pending verifier survives activity/process recreation. Cancellation clears that proof. Every Gravitas-authorized device sees the same catalog; no manual device invitation is needed.

## Invitation enrollment

`POST /v1/enroll` accepts `{"invite":"64-character invitation","deviceName":"My phone"}`. A valid invitation is consumed atomically and returns `deviceId`, `token`, and Unix `expiresAt`. Invalid, expired or exhausted invitations return 401. Store the token securely on that device.

## Device endpoints

- `GET /v1/apps`: configured apps and their latest verified release, if available. A release includes `id`, `tag`, `versionCode`, `versionName`, `sha256`, `size`, `minSdk`, `notes` and `createdAt`. Apps include their package and signing-certificate identity. Assets verified under a different configuration are hidden until reverified.
- `GET /v1/releases/ID/apk`: authenticated APK bytes. Supports HTTP ranges. The Android client verifies the expected size/hash, package, version and certificate before handing the APK to Android's installer.
- `DELETE /v1/device`: immediately revokes the calling device, returning 204.

Missing, expired, or revoked device credentials return 401; temporary authentication database failures return 503. Every enrolled device sees the same initial catalog.

## Administration

Use `ADMIN_TOKEN` with:

- `POST /admin/invites`: `{"label":"My phone","uses":1,"expiresInHours":24}`. Uses may be 1–100; expiry 1–168 hours. Returns the invitation once and its Unix expiry. Only a hash is retained.
- `GET /admin/devices`: IDs, names, expiry, revocation state, creation timestamps and linked account identity when present; no device tokens.
- `DELETE /admin/devices/ID`: revokes that device.
- `POST /admin/sync/APP`: synchronously ingests the latest stable GitHub release, returning its verified metadata. Optional `?tag=TAG` selects a published stable tag. Increasing version codes remain mandatory. Transient upstream failures retry; verification failures do not.

Unknown apps/releases/devices return 404. Invalid input returns 400. Upstream ingestion failures return 502 to the administrator. The CLI wraps these operations.

## Webhook and health

`POST /webhooks/github` accepts signed GitHub `release` events for configured repositories. Validate with `X-Hub-Signature-256` and `WEBHOOK_SECRET`. Accepted events return 202 and queue a fresh GitHub API lookup; unsigned events return 401. A full queue returns 503 so GitHub can retry. Polling also recovers missed events.

For releases with a `changelog.json` asset, ingestion verifies its size, digest, schema, version name and version code against the verified APK. The existing release `notes` field contains its plain-text `changes` as a bulleted list, so existing Android and web clients display the new changelog without an API migration. Previously cached APKs also refresh their notes from verified JSON without downloading the APK again. Legacy releases without the JSON asset use their GitHub release body. Invalid JSON assets fail ingestion rather than silently falling back to unrelated notes. See [app integrations](INTEGRATIONS.md) for the JSON contract.

`GET /healthz` is an unauthenticated liveness/readiness check with a SQLite ping. The container runs `store healthcheck` against it. No admin or device data is exposed.

## Browser portal

`GET /`, `/apps/daybreak` and `/apps/spacetime` are public app descriptions. `/auth/gravitas/start` and `/auth/gravitas/register` initiate browser PKCE with same-origin return paths; `/auth/gravitas/callback` establishes an encrypted eight-hour host-only Secure/HttpOnly session. `POST /auth/gravitas/logout` clears that local session. `GET`/`HEAD /download/appstore.apk` requires it, including range requests, and returns private/no-store signed APK bytes whose size and SHA-256 are pinned at startup. Browser sessions do not authenticate device APIs.


Explicit native requests set `broker: true` on both `/v1/auth/gravitas/start` and `/v1/auth/gravitas/exchange`. Only that flow requests `profile.name native.broker` and returns the store-origin encrypted-at-rest native bundle; see [Android broker](ANDROID_BROKER.md) for fields and child-client protocol. Linked device credentials and browser sessions require a current IAM family proof on every protected request. IAM revocation returns 401; outages return 503 without deleting local authorization state. `GET /auth/gravitas/session` checks the web session for the visible portal. Invitation-only credentials remain independent.

The configured catalog includes `appstore` (`dev.jeeth.privatestore`) as well as Daybreak and Spacetime. Store release ingestion, authenticated APK download and signature/version verification use the same API as the other apps. `GET /apps/appstore` describes self-updates.
