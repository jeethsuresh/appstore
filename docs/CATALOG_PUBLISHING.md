# Publish an app catalog from a backend

A backend publishes its complete catalog to Bazaar over HTTPS. The Store
keeps the last accepted catalog in SQLite and uses it for public app pages,
authenticated app catalogs and release ingestion. Existing GitHub release
workflows continue to supply the binaries and changelogs.

## Give the backend access

Set the Store's runtime `CATALOG_PUBLISHERS` JSON. On Gravitas this is a key in
`appstore-runtime`. Use a unique random bearer of at least 32 characters per
publisher, just as for notification integrations. Provision it separately from
notification credentials and share it only with the named backend.

```json
{
  "spacetime": {
    "key": "<server-only bearer>",
    "repositories": ["jeethsuresh/spacetime"],
    "adopt": ["spacetime"]
  }
}
```

`repositories` lists the exact GitHub repositories the backend can publish
from. Private repositories still need read access through the Store's GitHub
credential. The optional `adopt` list grants control of those existing
`config/apps.json` entries. An existing listing stays available until its first
valid publication, which must preserve its release repository, Android package,
APK filename pattern, release tag prefix and signing certificate. Its existing
notification configuration stays in the Store's trusted config. New apps just
need an unused app ID and an approved repository. Removing a publisher from this
runtime configuration and restarting the Store disables its publications and
restores any bundled listings it had adopted.

## Publish and update

Send `PUT /v1/catalog/{publisher}` with `Authorization: Bearer <key>` and
`Content-Type: application/json`. Send this envelope with every publication:

```json
{
  "schemaVersion": 1,
  "revision": 1,
  "apps": [
    {
      "id": "example",
      "name": "Example",
      "description": "Organize your notes and find them offline.",
      "repository": "owner/example",
      "logoPng": "<base64-encoded PNG>",
      "website": "https://example.com",
      "desktop": true
    }
  ]
}
```

- Send up to 32 entries. Each entry needs an app-specific PNG of up to 64 KiB
  and 512 × 512 pixels. The Store serves it from its own origin.
- For Android set `packageName`, `assetName` (which may use `{tag}`) and
  `certificateSha256`. Set `releaseTagPrefix` when multiple Android apps
  publish separately from one repository. Store clients still use Android's
  package visibility rules for detecting apps installed outside the Store.
- For desktop set `desktop: true`. Each desktop app needs its own release
  repository. Use `macOSNotarizedFrom` for the first desktop
  version code the publisher verified is signed and notarized for macOS.
- For iOS set `ios` to `testflight` or `app-store` and use `iosUrl` for the
  public Apple listing. Invitation-only TestFlight listings can omit the URL.
- Increase `revision` whenever the catalog changes, including when reverting
  copy or withdrawing a listing. Omit an app to withdraw it or send an empty
  array to withdraw the entire catalog. Withdrawn IDs and Android package
  identities remain reserved to their publisher.

A successful request returns 204. Repeating the same revision and contents is
safe. A stale revision, conflicting app ID/package, signing identity change, or
lower macOS cutoff returns 409. The Store applies a publication atomically and
queues its current release sources for ingestion. Stable GitHub releases and
the existing verification checks determine which builds become installable.
GitHub webhooks and the Store's regular polling pick up later releases.

Publish on backend startup and retry if the Store is temporarily unavailable.
To verify an onboarding, check the public `/apps/<id>` page and its logo, then
check the expected verified release through the Store client.

## Account visibility in the Android Store

A hosted app's backend is the source of its catalog. Store-owned listings are
for Bazaar itself and standalone apps without a service backend.
Configure each hosted publisher's canonical HTTPS service `origin` in
`CATALOG_PUBLISHERS`, alongside its key, repositories and adopted app IDs.

After a verified Gravitas service sign-in, the backend sends
`PUT /v1/catalog/{publisher}/accounts` with its publisher bearer and JSON
containing `issuer`, a live Gravitas `sessionProof` for its configured origin,
and an `apps` array of IDs from its published catalog. The Store reads the
stable subject from Gravitas and records membership against that issuer,
subject, publisher and app ID. It does not store the session proof. Existing
service accounts register the next time they sign in.

With `PERSONAL_ANDROID_CATALOG=true`, the Android catalog and APK download
endpoints check membership, administrator global visibility, or a match between
the user's verified identity domain and the app's configured host or its parent
domain. A publisher can set `hideFromUnregisteredUsersOnDomain: true` to require
membership even for users on its domain. The global setting overrides that
flag. A Gravitas administrator can change the setting on the Store's web page
for the app. Standalone listings can use `manualGlobal` in the Store config.
