Private Store 0.2.0 adds Gravitas account sign-in on Android and a web home for the app collection.

- Sign in in your browser, then return to Private Store through a verified Android App Link. No separate device invitation is required at apps.gravitas.cool.
- PKCE and encrypted pending state protect the sign-in and allow it to resume after the activity is recreated. Passwords and Gravitas access tokens stay out of the Android app.
- Existing enrollments keep working and can connect a Gravitas account in place. Sign-out immediately revokes device access.
- Browse Daybreak and Spacetime pages with features, compatibility and verified release information. Gravitas sign-in gates the website's store APK download.
- Existing signed app installation, release ingestion/webhooks and periodic updates remain available. Independent stores retain invitation enrollment.

Download **appstore-android.apk** and verify it with **SHA256SUMS**. It uses the same permanent signing certificate as 0.1.0 and updates that release in place. The live backend is at **https://apps.gravitas.cool**. The repository includes Kubernetes and Docker/Caddy deployment configuration; private credentials and signing keys are excluded.

Android controls background scheduling and may require installation confirmation or a Play Protect scan. Development-signed store copies use a different key and require reinstalling after preserving any needed data.
