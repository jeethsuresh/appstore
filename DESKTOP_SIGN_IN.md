# Gravitas sign-in through desktop Private Store

Private Store is an optional Gravitas account broker on macOS, Windows and Linux.
Sign in to Store once, then approve each service that asks to use that account.
Broker accounts currently use `gravitas.cool`; other identity domains retain
Store-only sign-in and apps use their browser for those accounts.
Existing Store installations can choose **Settings → Enable app sign-in** to
replace their older Store-only session with an explicitly consented broker session.

Spacetime, River and Haven try Store before system-browser PKCE sign-in. Cancelling
Store approval cancels sign-in. Missing/old Store installations, locked credentials,
expired accounts, identity outages and missing service consent can use the existing
browser flow. Shared-calendar/group permissions and account registration still use
the browser; Store delegates only `profile.name`. Local/offline work is retained.

## Add another native app

Use `desktop/sdk/desktop_store_broker.dart` in native Dart, or
`desktop/sdk/desktop-store-broker.cjs` in the Electron **main process**. The helpers
have no package dependencies. Generate a fresh random 32-byte base64url verifier
and state, and SHA-256/base64url the verifier into `challenge`.

```js
const { randomBytes, createHash } = require('node:crypto');
const { authorizeDesktopStore } = require('./desktop-store-broker.cjs');
const verifier = randomBytes(32).toString('base64url');
const result = await authorizeDesktopStore({
  appOrigin: 'https://your-service.example',
  challenge: createHash('sha256').update(verifier).digest('base64url'),
  state: randomBytes(32).toString('base64url'),
});
// null: use ordinary browser sign-in. StoreSignInCancelled: stop sign-in.
// Send result.code, verifier, result.sub and result.session_id to YOUR backend.
```

No per-app Store allowlist, notification integration or package identifier is
required. An app's existing HTTPS Gravitas service origin identifies the target;
its existing callback must be `/auth/gravitas/callback`. IAM requires the account's
existing consent for that target. First-time consent uses the service's normal
browser flow; Store cannot create or expand grants. Catalog mobile/desktop
membership remains independent of sign-in.

The backend exchanges the code using IAM's authorization-code grant and exact
`redirect_uri = appOrigin + /auth/gravitas/callback`, verifies the issuer, profile,
audience, subject, session family and live status proof, then returns its ordinary
app session. Follow Spacetime's `/api/auth/gravitas/broker`, River's
`/api/native/broker`, or Haven's `/api/auth/native/broker`. Check account/generation
again before persisting asynchronous results; explicit logout must win. Do not
send a verifier to Store. Do not accept Store's returned identity as authentication
without the backend's IAM exchange and live proof check.

For Electron apps with their own local backend, this helper is a sign-in building
block; it does not automatically add hosted synchronization or change local data
ownership. Never put Store discovery or IAM credentials in a preload/web API.

## Local protocol, version 1

Discovery is scoped to the current OS user:

- macOS: `~/Library/Application Support/Gravitas/Private Store/broker-v1.json`
- Windows: `%APPDATA%/Gravitas/Private Store/broker-v1.json`
- Linux: `$XDG_DATA_HOME/Gravitas/Private Store/broker-v1.json`, falling back to
  `~/.local/share/Gravitas/Private Store/broker-v1.json`.

Store atomically writes a user-private descriptor containing `protocolVersion:1`,
an ephemeral `http://127.0.0.1:<port>/v1/authorize` endpoint, random `nonce` and
`launch:{file,args}`. The descriptor is discovery metadata, never an IAM or Store
device token. A closed Store can be started using that last installed executable,
without a shell. Helpers reread the new endpoint/nonce after launch. Updates write
the new versioned executable on next Store launch; missing/moved executables fall
back to browser sign-in.

The only endpoint accepts POST JSON with the descriptor nonce as bearer:
`{protocolVersion:1, appOrigin, codeChallenge, state, username?}`. Origin headers,
non-loopback Host values, arbitrary callbacks/scopes and oversized bodies are
rejected. No CORS is enabled. One foreground request is handled at a time. Service
and account approval appears in a native Store dialog; cancellation, disconnection
or Store account changes invalidate the pending request.

Success is `{protocolVersion:1,status:"authorized",issuer,appOrigin,redirectUri,
codeChallenge,state,code,sub,session_id,expires_in}`. Helpers validate all bindings.
Only a one-use, child-PKCE-bound code with at most 60 seconds lifetime leaves Store.
Other statuses include `cancelled`, `login_required`, `consent_required`,
`unavailable`, `invalid_request` and `unauthorized_client`.

This channel trusts the current OS user. Unlike Android AccountManager, it does
not attest a caller's package/signature. The approval identifies the HTTPS service,
not a verified binary; approve only requests you initiated. A website cannot read
the nonce or call this API. Same-user processes can read discovery, so every
request requires approval and IAM still enforces service consent and PKCE.

## Credential and revocation rules

Store explicitly requests `profile.name native.broker` using its existing native
PKCE start/exchange and exact `/auth/android/callback` binding, which is shared by
both native Store clients. The existing browser enrollment endpoint never returns
broker credentials. Store's access, rotating refresh and status-proof credentials
remain in the main process and its OS-encrypted session file. No secure OS vault
means session-only in-memory credentials, never plaintext persistence.

Refresh is serialized; the rotated pair is persisted before any further IAM call.
Every authorization checks the live family and IAM's bound code response. Refresh
never extends the fixed family expiry. Central logout/revocation invalidates
Store delegation and resulting app sessions under the apps' normal live checks.
Store-only logout clears local Store credentials and revokes its device enrollment.

## Verification

`cd desktop && npm test` covers transport rejection, target/state binding, missing
consent, revocation, refresh serialization, sign-out/cancellation races and cold
Store launch. `npm run test:e2e` exercises the real Electron login and separate
process broker requests alongside install/update/self-update. Native package smoke
checks verify broker discovery on each matching runner. Consumer Flutter tests
exercise the dependency-free helper and app-session exchange/cancellation flows;
Haven's Go tests exercise its exact IAM callback, subject/family and one-use checks.
