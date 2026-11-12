# App notifications

Private Store distributes application payloads to installed, certificate-pinned Android apps and supports direct Apple Push Notification service (APNs) delivery to registered iOS apps. Spacetime is the first Android consumer. It displays invitations and updated events through a native handler, without starting Flutter, syncing calendars, fetching data, or running its UI.

## Gravitas protocol delivery

Hosted delivery uses the versioned [appstore PCR](../protocols/README.md). Private Store publishes it on sign-in and reconciles existing active devices on the server. Spacetime discovers the recipient's `appstore` version/capabilities, then sends the same native payload to Gravitas. Gravitas privately multicasts to registered providers; appstore checks the sender's app namespace, account and opaque device handle before inserting into this existing durable queue. The sender receives a constant acceptance receipt without store identities or fan-out counts. A missing subscription or discovery outage keeps Spacetime's outbox queued.

The hosted appstore Secret adds `protocol-relay-key`; IAM's `PROTOCOL_SERVICES` keeps the corresponding provider credential and Spacetime's existing sender key with `protocols.read`/`appstore.push` permissions. Spacetime sets `NOTIFICATION_PROTOCOL=appstore/1.0.0`. New native provider registrations carry this protocol marker and no store callback. The native listener, certificate/account checks, Android permission and Doze behavior below apply unchanged.

## iOS sign-in and delivery

Enable the Push Notifications capability in the iOS target. Request alert permission and register for remote notifications. Send the hex-encoded APNs token and the platform to the app's own authenticated backend when both login and APNs registration have finished. Repeat registration when Apple supplies a new token. The app backend derives the Gravitas session proof from the verified server-side login and selects the APNs environment from its build configuration. Development builds use `sandbox`; TestFlight and App Store builds use `production`.

The app backend calls `PUT /v1/notification-installations/{app}/{installation}` at AppStore using its server-only bearer from `NOTIFICATION_APP_KEYS`. The installation identifier is a random 32-byte value encoded as 64 lowercase hex characters, stable across sign-ins for that installation. The backend maintains an increasing positive generation per installation. Reuse the same generation and fields when retrying a request.

Spacetime and River expose an authenticated `/api/notifications/ios` endpoint for their own iOS clients. Spacetime keeps the returned device handle in its existing calendar notification outbox flow. River uses its existing Matrix `push.broadcast` flow. Their iOS bundle topics are `com.jeethsuresh.spacetime` and `cool.gravitas.river`.

```json
{
  "platform": "ios",
  "environment": "sandbox",
  "token": "APNS_DEVICE_TOKEN_AS_HEX",
  "session_proof": "GRAVITAS_STATUS_CREDENTIAL",
  "generation": 1
}
```

AppStore checks that the live proof belongs to the configured service origin. Gravitas derives the account and registers the existing `appstore/1.0.0` capability for that app's grant. This lets the service discover push support for someone who has signed in only on iOS. The registration response is `200 {"device":"OPAQUE_64_HEX_DEVICE_HANDLE"}`. Store that handle for the existing `push.send` request. `push.broadcast` includes registered iOS and Android installations of the sending app.

On sign-out or when the user turns off notifications, send `DELETE` to the same registration URL with a newer `{"generation":2}` and the same server credential. AppStore returns 204 after removing the token and queued payloads. AppStore returns 409 for stale generations or reuse of a generation with different fields. Retry transient failures, and ensure the app's logout revokes its Gravitas login session or reliably retries removal. AppStore checks the live session before submitting each queued notification to Apple.

The APNs payload contains a generic alert, an opaque notification `id`, the Gravitas `subject`, and the original `payload` when it fits. The client verifies the subject against its active account before using the payload and can deduplicate with the ID. When the full envelope would exceed 4,096 bytes, it contains `refresh: true` in place of the original payload. The client can refresh from its authenticated service when the user opens the notification. Keep sensitive content in the service and use opaque identifiers in payloads. AppStore retries temporary failures and clears invalid APNs tokens. APNs acceptance completes the server delivery attempt.

### AppStore configuration

Deploy the Gravitas IAM delegated AppStore registry endpoint before enabling iOS registrations. Set `NOTIFICATION_APNS_CONFIG` to a JSON object keyed by the app ID. Each app has a canonical HTTPS backend origin and one or both APNs environments. Use the Apple key authorized for each environment and topic.

```json
{
  "example": {
    "origin": "https://example.gravitas.cool",
    "environments": {
      "sandbox": {
        "topic": "cool.gravitas.example",
        "team_id": "TEAM_ID",
        "key_id": "KEY_ID",
        "private_key": "APPLE_P8_PEM_CONTENTS"
      }
    }
  }
}
```

Add the app's distinct backend key to `NOTIFICATION_APP_KEYS` and configure its origin with `protocols.read` and `appstore.push` in IAM's `PROTOCOL_SERVICES`. Apps that broadcast also need `appstore.push.broadcast`. For an app in the Android catalog, the configured origin must match its existing notification origin. For the hosted Store, supply the APNs JSON through the `appstore-runtime` Secret. Keep the Apple private key and service credentials in the runtime secrets. AppStore connects directly to Apple's HTTPS endpoints using HTTP/2 on port 443.

## Legacy/direct backend configuration (independent stores and migration)

Generate one random service key of at least 32 characters outside source control. Configure the **same value** as:

- Appstore: `NOTIFICATION_PUBLIC_URL=https://apps.gravitas.cool` and `NOTIFICATION_APP_KEYS=spacetime=YOUR_SERVICE_KEY`.
- Spacetime: `NOTIFICATION_STORE_URL=https://apps.gravitas.cool` and `NOTIFICATION_PROVIDER_KEY=YOUR_SERVICE_KEY`.

The Kubernetes manifests read optional Secrets: `appstore-notifications` / `app-keys` containing `spacetime=YOUR_SERVICE_KEY`, and `spacetime-notifications` / `provider-key` containing the same raw key. Populate them through your existing secret-management process, then restart both deployments. Docker Compose accepts the appstore variables from its environment. No real credentials are included in the repository.

The hosted manifests use `NOTIFICATION_GATEWAY_ADDR=traefik.traefik.svc.cluster.local:443` to route backend traffic through the existing private ingress. This changes only the TCP destination: the configured public HTTPS origin still determines Host, SNI and certificate verification. Redirects are disabled. Independent deployments can omit this setting.

Both apps must use the same Gravitas issuer. The Android clients must be signed with their existing release certificates. The store catalog declares Spacetime's HTTPS backend and native notification authority. Notification capability is advertised only when its backend URL and key are configured. Independent invitation-only enrollments keep app installation/update support; app notifications require a linked Gravitas account. The Spacetime integration currently targets the hosted Gravitas account/server.

Install the upgraded clients, sign in to the same Gravitas account, and enable **App notifications** in Private Store. In Spacetime, choose **Settings → Sync & invites → Enable invite & event notifications** to grant Android notification permission. Android also allows disabling the **Invites & event updates** channel independently of local reminders.

For screen-off delivery, tap **Allow screen-off notifications** in Private Store and approve Android's request to stop optimizing Private Store's battery use. Only the shared distributor needs this exemption; Spacetime does not need a separate connection or exemption. The store shows the current Android permission state and offers **Battery settings** to change it later. Declining the request keeps queued delivery during Doze. The system prompt is opened only by this explicit tap, never by the service, boot, or package-change receivers. This option may increase battery use. Android/OEM restrictions, force-stop, network outages, disabled notification channels, and Do Not Disturb can still prevent or delay visible alerts; this is not a guarantee of immediate delivery.

A new supporting app is registered after the store's explicit PackageInstaller success callback and when the store next opens. While the relay is running it also receives package changes through a dynamic receiver; a manifest receiver handles final uninstalls. This avoids relying on package-added manifest broadcasts restricted on modern Android. A signed Spacetime client also prepares the distributor through local verified IPC while visible. Android can reject a cold foreground-service start after an unattended install; opening Private Store or Spacetime starts it within Android's foreground-start rules. Force-stop suppresses delivery until the application is opened again.

## Delivery protocol

1. The phone verifies each installed package's single signing certificate and exported notification provider against the authenticated catalog. It sends the installed supporting app IDs to `PUT /v1/notification-apps`, with a monotonically increasing registration generation so delayed disable requests cannot override newer settings.
2. The store derives the issuer and stable subject from the authenticated device. Its server registers an immutable provider ID at `PUT /api/notifications/providers/{id}` on Spacetime, using the service key. The callback is pinned to the operator-configured store origin. Registration retries happen on the server.
3. Spacetime writes an outbox record in the same database transaction as an event mutation. A new invite generates `event.invited`; later versions generate `event.updated` for pending/accepted invitees and the organizer. Failed or rolled-back writes send nothing. JSON, CalDAV and recurrence/source writes use the same event persistence hook.
4. Spacetime sends `POST /v1/notifications/spacetime` with `{provider,id,payload}` and the service key. The store validates the app/provider/current device identity, stores it durably, suppresses duplicates and wakes the device's one existing stream.
5. `GET /v1/notifications/stream` returns newline-delimited `{id,app,payload}` messages. It uses the device bearer in an HTTP header; credentials never enter URLs. Pending messages replay after disconnect and once a minute on a live connection until acknowledged. `DELETE /v1/notifications/{id}` acknowledges only that device's delivery.
6. The listener verifies the installed signature again and uses explicit Binder `ContentProvider.call("notify")` to deliver locally. Spacetime verifies the store package, actual caller UID and permanent certificate. It checks a small cached account record bound to the encrypted-session fingerprint, posts the native Android notification, and suppresses duplicate/older event versions. Only then does the store acknowledge delivery. A receiver exception, missing receiver or declined handoff is never acknowledged. The listener continues with other messages while the server retains and retries the failed alert. Receiving apps suppress duplicate/older notifications, and may explicitly accept a permanently invalid or irrelevant payload without displaying it.

Uninstall, disabling distribution and sign-out remove subscriptions. Local sign-out stops the listener immediately; a best-effort unregister runs even when device revocation is unavailable. Device expiry/revocation and identity changes are also reconciled server-side. Reinstallation/account changes get new provider IDs, so stale callbacks cannot cross accounts. An offline unregister is retried at the next existing six-hour update check when distribution is disabled. The store rejects callbacks for revoked/expired devices immediately, and rechecks live IAM state while streaming. The queues retain payloads for at most seven days. The store holds at most 256 pending deliveries per device and returns 503 for backpressure; Spacetime retries durable callbacks with exponential backoff. Receipts retain only deduplication metadata after acknowledgement.

## Battery behavior

The listener is a native Android `specialUse` foreground service in **`:notifications`**, a separate process from the store UI. It owns one blocking stream and one executor. The UI, Compose screens, account broker, APK ingestion and Flutter engines do not stay running to receive notifications. Android requires a quiet ongoing notification for this direct background connection.

There is no polling, client heartbeat, scheduled sync, exact alarm, wake lock or periodic WorkManager task for delivery. Connectivity callbacks stop retries when offline. Without a user-granted battery exemption, Doze entry closes the stream and cancels reconnects; exiting Doze/network recovery reconnects and replays pending messages. With the exemption, both the initial connection and reconnect policy allow networking during Doze. Returning from battery settings rechecks the actual system exemption and restarts the connection when that state changes. Failed connections retry after five seconds, backing off to at most two minutes plus up to four seconds of jitter using ordinary non-wakeup timers. A validated network change, power-policy change, package reconciliation, or explicit foreground start resets that backoff and reconnects immediately. The server sends a small idle liveness frame once a minute, and Android abandons a silent stream after ninety seconds. Configure reverse proxies to allow unbuffered responses and idle timeouts longer than one minute. These server frames replace the previous fifteen-minute keepalive to prevent silent network failures from delaying alerts for twenty minutes; they do not wake the screen or hold a wake lock. Server-side IAM revalidation sends no IAM-check traffic to the phone. The relay stops when no installed supporting app remains. Package changes do bounded reconciliation work once.

Android permits exempt apps to use the network during Doze; see [Android's Doze and App Standby documentation](https://developer.android.com/training/monitoring-device-state/doze-standby#support_for_other_use_cases). The exemption does not hold the CPU awake, wake the display, bypass notification permissions, or guarantee delivery latency. No battery-consumption measurement is implied by these architectural controls. Real-device idle drain must be compared against a baseline before releasing.

## Validation

Backend unit suites: `go test -race ./...` and `go vet ./...` in both repositories. They cover registration retry/removal, service authentication, pinned callbacks, stable identity mapping, atomic event/outbox writes, queue limits, cross-device acknowledgement rejection, replay, duplicate suppression, account changes and revoked devices.

Run both real backends together with sibling clones:

```sh
docker build -f appstore/integrations/notifications.Dockerfile .
```

The integration test creates an event with an invite, registers the real provider, runs Spacetime's real outbox worker, verifies the appstore's live stream, acknowledges the invite, updates the event and verifies the next notification. Test-only IAM identity fixtures and loopback transports never enter a release binary.

Android builds: `./gradlew assembleDebug testDebugUnitTest lintDebug` in the store; `flutter analyze`, `flutter test`, and `flutter build apk --debug` in Spacetime. On a disposable emulator run Spacetime's `StoreNotificationDeliveryTest` to verify native OS notifications, replay/version suppression, wrong-account/sign-out rejection and unsigned IPC rejection without launching Flutter.

CI runs `NotificationRelayPowerTest` on an isolated Android 35 emulator/testbed package. It turns the screen off, forces Doze, grants and revokes the battery exemption, and verifies that the production connection policy follows the actual OS state and rejects offline retries. The e2e variant requires a disposable CA certificate at `android/app/build/generated/e2e-res/raw/integration_ca.pem`; CI generates one before building, as the existing `tools/e2e/` runners do. After preparing that resource, run `./gradlew --no-daemon -PstoreTestBuildType=e2e -Pandroid.testInstrumentationRunnerArguments.class=dev.jeeth.privatestore.NotificationRelayPowerTest connectedE2eAndroidTest` in `android/` on a disposable emulator. It restores idle, battery and exemption state in `finally`. This tests the power gate, not physical-device delivery latency or battery drain.

Before release, install both permanently signed APKs on a disposable device and test invite/update delivery with both activities closed; duplicate/reordered delivery; airplane mode/reconnect; app replacement/removal; both sign-outs; and notification permission/channel denial. Force Doze using `adb shell dumpsys deviceidle force-idle`: without an exemption, verify the relay socket/retry work stops and the queued alert arrives after `adb shell dumpsys deviceidle unforce`; with the exemption, verify a new invitation reaches Spacetime while the screen remains off. Revoke the exemption and check that delayed delivery resumes. Inspect processes to confirm only the store's `:notifications` process is retained. Compare `adb shell dumpsys batterystats` and idle CPU/network activity over a long unplugged baseline and notification-enabled run. Physical-device power measurements and signed-device IPC verification are separate from emulator/unit checks.

## Checks completed in this workspace (2026-10-24)

- Both Go backends: complete race-test suites and `go vet` passed.
- Real two-backend HTTP integration: provider registration, invitation delivery, acknowledgement and event-update delivery passed.
- Appstore: debug APK build, native unit tests and Android lint passed.
- Spacetime: its pinned Flutter 3.32.0-0.2.pre SDK and unchanged dependency lockfile passed analysis and all 143 Flutter tests. Its debug APK build, native unit tests, device-test Kotlin compilation and Android lint passed.
- Merged manifests confirm the store listener uses `:notifications` and Spacetime uses `android.app.Application`; the native provider does not create a Flutter engine.
- No Android device/emulator was connected. Device tests were compiled, not executed. Signed APK interoperability, Doze behavior and physical battery drain still need the device validation above. No production deployment or signed release was performed.

## River messages

The catalog also supports `cool.gravitas.river.notifications` at `https://chat.gravitas.cool`, pinned to River's existing permanent signing certificate. Add `river=THE_DISTINCT_RIVER_KEY` to `NOTIFICATION_APP_KEYS`; River uses the matching `NOTIFICATION_PROVIDER_KEY`. IAM configures that sender origin with `protocols.read`, `appstore.push` and `appstore.push.broadcast`, and enables `MATRIX_APPSTORE_PUSH_ORIGIN=https://chat.gravitas.cool`.

River uses `push.broadcast`; Private Store remains the sole owner of installed-device subscriptions. The River reconciliation endpoint acknowledges the authenticated protocol marker without duplicating that device registry. Matrix push rules decide which events notify. The native payload contains account, conversation and event identifiers plus a replay sequence; it contains no message content or credentials. In River enable **Settings → Message notifications** and approve Android's notification permission. Both apps must use the same Gravitas account. Existing Store installations pick up this catalog capability when next opened; no new Store APK is required.

The disposable `TestRiverMatrixNotificationInterop` fixture is launched by IAM's integration test to exercise encrypted event → protocol relay → Store stream → acknowledgement. It is absent from production binaries.

The required `NotificationRelayRecoveryTest` uses a disposable HTTPS server and a certificate-pinned fixture provider on an Android emulator. It verifies retries after a receiver exception, continued handoff to the next alert, automatic recovery after a dropped stream, explicit foreground recovery from a silent socket, and the real ninety-second read deadline followed by queued replay. The fixture server certificate and PKCS12 key are generated under the ignored e2e build resources and never enter a release APK. Backend tests verify same-stream replay until acknowledgement and periodic idle liveness.
