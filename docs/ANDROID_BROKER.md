# Gravitas Android broker protocol v1

Private Store owns the Android account type `dev.jeeth.privatestore.gravitas` in package `dev.jeeth.privatestore`. Clients must verify the authenticator owner package and its installed signing certificate before interacting with accounts. The permanent store certificate SHA-256 is `e70b5b54a0d174f4ad8867889f6481a2b193dc9c130630f321563e12f3293793`.

## Spacetime request

The first supported client is exactly `com.jeethsuresh.spacetime`, certificate `f5714ad3d2b3d82b283efd61b2d99f5eaad5ec9efe67b644a62efc79fec94582`. The authenticator validates Android's system-supplied caller UID/package, rejects shared/unrecognized callers, and verifies the installed release certificate. The service policy pins:

- app origin: `https://spacetime.gravitas.cool`
- redirect URI: `https://spacetime.gravitas.cool/auth/gravitas/callback`
- delegated scope: `profile.name`

Generate a fresh 32-byte base64url state and S256 challenge from a fresh private verifier. Both state and challenge have 43 base64url characters. Call `AccountManager.getAuthToken` with token type `gravitas_code:spacetime:v1:<challenge>:<state>` and options `protocolVersion` (integer 1), `codeChallenge` and `state` matching that type. Never send the verifier to Private Store.

The actual token type binds both nonce and challenge because Android may ignore options and custom expiry advice when caching. The authenticator uses `customTokens=true`, expiry zero, and never calls `setAuthToken`. Immediately invalidate the received code through `AccountManager.invalidateAuthToken`; any new attempt uses fresh proofs. IAM independently enforces single-use codes and active family membership.

## Successful result

The returned bundle has:

- standard `AccountManager.KEY_AUTHTOKEN`: one-use IAM code
- standard account name/type keys for the selected store-owned account
- `protocolVersion`: integer 1
- `issuer`: `https://gravitas.cool`
- `appOrigin`: the exact Spacetime origin above
- `redirectUri`: the exact callback above
- `state`, `codeChallenge`: exact request values
- `sessionId`: original IAM login family UUID
- `subject`: stable IAM subject

Validate every binding before sending `{code, code_verifier, session_id, sub}` to Spacetime's pinned `POST /api/auth/gravitas/broker`. The backend exchanges with IAM, checks the resulting profile/status proof against subject/family/origin, and returns its existing `{token,user}`. Account names alone never authenticate an application. No store refresh/access/status credential is returned to the child.

## Visibility, cancellation and errors

Only the verified installed release client is granted account visibility on Android 8+. Reconcile visibility on store sign-in/resume, after a store-managed install, during periodic update work, and through the narrow prepare handshake. Before looking up accounts, the client verifies the authenticator and the exported provider's owner package/certificate and calls `ContentResolver.call(content://dev.jeeth.privatestore.gravitas, "prepare", null, null)`. The call-only provider checks the actual Binder caller UID against the same exact package/certificate policy, reconciles visibility, and returns only integer `protocolVersion: 1` and boolean `available`. It rejects other methods, query/CRUD/type requests and URI grants. This also handles installing Spacetime after the account was created. Accounts remain hidden from other packages. If the store/authenticator is absent or untrusted, or there are zero visible accounts, use the existing explicit browser login; do not invoke `addAccount` unexpectedly. The store itself requires Android 8.0; older clients retain browser fallback.

Store updates retain the package ID, permanent signing key, AndroidKeyStore aliases, preference names and account type. The `MY_PACKAGE_REPLACED` receiver clears completed self-install markers, restores the unique periodic update schedule, and reconciles existing account visibility without clearing either vault. The same recovery runs when the store opens, covering a stopped app or a missed completion broadcast. Install markers are committed before handing the APK to Android, because replacing the store can terminate the old process immediately.

Errors use standard `KEY_ERROR_CODE` and `KEY_ERROR_MESSAGE`, plus a stable `brokerError`:

- `invalid_request`: bad arguments or unsupported protocol; `ERROR_CODE_BAD_ARGUMENTS`
- `unauthorized_client` or `consent_required`: unsupported caller/service or missing target consent; `ERROR_CODE_UNSUPPORTED_OPERATION`
- `login_required`: absent, expired or centrally revoked broker state; `ERROR_CODE_BAD_AUTHENTICATION`
- `unavailable`: network/5xx/malformed upstream reply; `ERROR_CODE_NETWORK_ERROR`
- `cancelled`: `ERROR_CODE_CANCELED`

Cancellation stays cancelled. Missing consent or sign-in permits an explicit browser fallback. Transient errors preserve credentials/local app data and display an unavailable state. The broker never launches browser consent or interactive login from background work.

## IAM and lifetime

New native store login explicitly requests `profile.name native.broker` through the existing browser code/PKCE flow and exact store callback `https://apps.gravitas.cool/auth/android/callback`. Web and legacy store-only login do not obtain this privilege. The store backend returns its own origin-scoped IAM bundle only to an explicit native broker exchange request; Android keeps it encrypted with AndroidKeyStore.

Discovery pins `native_broker_authorization_endpoint` to the IAM issuer. `POST /oauth/broker/authorize` takes the store access bearer and `{app_origin,redirect_uri,code_challenge,code_challenge_method:"S256",scope,state}`. Spacetime requests `profile.name`; River uses the fixed permission set below. The response contains `{code,state,issuer,app_origin,redirect_uri,session_id,sub,expires_in:60}`. Existing target consent and an active original login family are required; recursive delegation and arbitrary scope expansion are forbidden.

Refresh is serialized, on demand and opportunistically during existing store update work. Rotation preserves the original absolute family expiry (maximum 30 days), never extends it. A revoked refresh requires reauthentication; outages retain encrypted state. Protected store browser/device requests validate the pinned live `session_check_endpoint` without positive caching. Central Gravitas logout revokes the same family and therefore store login, broker refresh, child codes and native child sessions. Existing store-local logout removes local broker/account state and revokes its device credential; it does not silently sign out an unrelated browser session.

## River request

Private Store 0.6.4 also accepts exactly `cool.gravitas.river`, with release
certificate `6559956b5cf6b2f796824995b31da7fa2c176ba652e8fcb40741686d850bb6b0`.
Its token type is `gravitas_code:river:v1:<challenge>:<state>`, using the same
version, nonce, verifier, result and caller checks as Spacetime. The fixed target
is `https://chat.gravitas.cool/auth/gravitas/callback`; the app origin is
`https://chat.gravitas.cool`. River exchanges the code and private verifier at
its `POST /api/native/broker`, checking subject, family, identity and every scope
before saving its ordinary encrypted session.

The exact requested scope set is `profile.name matrix.full chats.list chats.read
chats.write groups.create groups.manage groups.join spaces.channels contacts.read
contacts.manage calls.read calls.join posts.publish`. IAM requires prior consent
covering every scope with full room access; the broker cannot create or expand
that grant. A different account, missing consent, an absent/older Store, or an
unavailable broker uses River's browser flow. Matrix device approval remains
separate for a new or unlinked device. The selected caller determines the fixed
service mapping; no request may substitute another client's token type or supply
arbitrary URLs, scopes, packages or certificates.
