Private Store 0.3.0 adds verified self-updates and Gravitas sign-in for supported Android apps.

- Private Store appears in its own app catalog. Use **Update Private Store**, or let the six-hour update checks find a newer signed release. Android may ask for installation approval.
- Store updates preserve encrypted sign-in, the Gravitas account, update preferences and the background update schedule. The package and permanent signing certificate are unchanged.
- Explicit Gravitas sign-in enables the Android account broker for the release-signed Spacetime client. Each request uses a fresh, short-lived, single-use code bound to the client's PKCE proof and state. Store refresh credentials remain encrypted inside Private Store.
- Central Gravitas logout revokes linked store web/device sessions and connected native app sessions. Temporary IAM failures preserve local credentials and report unavailability.
- Spacetime installed after store sign-in can immediately discover the account through a restricted, certificate-checked preparation call. Browser sign-in remains available.
- Existing invitation enrollments and independent stores remain supported. Existing Gravitas enrollments can enable native app sign-in by signing in again.

Download **appstore-android.apk** and verify it with **SHA256SUMS**. This APK updates existing permanent-key store installations in place. The backend and gated bootstrap download are at **https://apps.gravitas.cool**. Private credentials and signing keys are excluded from the repository and release.
