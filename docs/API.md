# HTTP API

All deployment traffic uses HTTPS. JSON errors use `{"error":"message"}`. Tokens belong in `Authorization: Bearer TOKEN`, never query parameters.

## Enrollment

`POST /v1/enroll` accepts `{"invite":"64-character invitation","deviceName":"My phone"}`. A valid invitation is consumed atomically and returns `deviceId`, `token`, and Unix `expiresAt`. Invalid, expired or exhausted invitations return 401. Store the token securely on that device.

## Device endpoints

- `GET /v1/apps`: configured apps and their latest verified release, if available. A release includes `id`, `tag`, `versionCode`, `versionName`, `sha256`, `size`, `minSdk`, `notes` and `createdAt`. Apps include their package and signing-certificate identity. Assets verified under a different configuration are hidden until reverified.
- `GET /v1/releases/ID/apk`: authenticated APK bytes. Supports HTTP ranges. The Android client verifies the expected size/hash, package, version and certificate before handing the APK to Android's installer.
- `DELETE /v1/device`: immediately revokes the calling device, returning 204.

Missing, expired, or revoked device credentials return 401; temporary authentication database failures return 503. Every enrolled device sees the same initial catalog.

## Administration

Use `ADMIN_TOKEN` with:

- `POST /admin/invites`: `{"label":"My phone","uses":1,"expiresInHours":24}`. Uses may be 1–100; expiry 1–168 hours. Returns the invitation once and its Unix expiry. Only a hash is retained.
- `GET /admin/devices`: IDs, names, expiry, revocation state and creation timestamps; no device tokens.
- `DELETE /admin/devices/ID`: revokes that device.
- `POST /admin/sync/APP`: synchronously ingests the latest stable GitHub release, returning its verified metadata. Optional `?tag=TAG` selects a published stable tag. Increasing version codes remain mandatory. Transient upstream failures retry; verification failures do not.

Unknown apps/releases/devices return 404. Invalid input returns 400. Upstream ingestion failures return 502 to the administrator. The CLI wraps these operations.

## Webhook and health

`POST /webhooks/github` accepts signed GitHub `release` events for configured repositories. Validate with `X-Hub-Signature-256` and `WEBHOOK_SECRET`. Accepted events return 202 and queue a fresh GitHub API lookup; unsigned events return 401. A full queue returns 503 so GitHub can retry. Polling also recovers missed events.

`GET /healthz` is an unauthenticated liveness/readiness check with a SQLite ping. The container runs `store healthcheck` against it. No admin or device data is exposed.
