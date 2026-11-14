package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func catalogTestLogo(t *testing.T) []byte {
	t.Helper()
	var logo bytes.Buffer
	if err := png.Encode(&logo, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return logo.Bytes()
}

func putCatalog(t *testing.T, s *Server, publisher, key string, publication catalogPublication, want int) {
	t.Helper()
	data, err := json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, "/v1/catalog/"+publisher, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("publish as %s got %d, want %d: %s", publisher, w.Code, want, w.Body.String())
	}
}

func TestPublishedCatalogAdoptionWithdrawalAndRestart(t *testing.T) {
	app := App{ID: "calendar", Name: "Bundled Calendar", Description: "Keep a calendar.",
		LogoURL: "/assets/daybreak.svg", Repo: "example/calendar", Asset: "calendar-{tag}.apk",
		Certificate: strings.Repeat("c", 64), Package: "com.example.calendar", Desktop: true,
		MacOSNotarizedFrom: 100, Notifications: &NotificationApp{
			Origin: "https://calendar.example.com", Authority: "com.example.calendar.notifications",
		}}
	key := strings.Repeat("k", 64)
	cfg := Config{DataDir: t.TempDir(), AdminToken: strings.Repeat("a", 64), WebhookSecret: strings.Repeat("b", 64),
		Apps: []App{app}, CatalogPublishers: map[string]CatalogPublisher{
			"calendar": {Key: key, Repositories: []string{app.Repo}, Adopt: []string{app.ID}},
		}}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pageHas := func(name string) {
		t.Helper()
		w := request(s, "GET", "/apps/calendar", "", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), name) {
			t.Fatalf("calendar page %d lacks %q", w.Code, name)
		}
	}
	pageHas("Bundled Calendar")
	tok, _ := enrollDevice(t, s)
	_, err = s.db.Exec(`INSERT INTO releases(app_id, tag, version_code, version_name, sha256, size, min_sdk, notes, created, config_hash)
		VALUES(?, 'v2.0.0', 20, '2.0.0', ?, 100, 24, 'Verified release', '', ?)`, app.ID, strings.Repeat("e", 64), appTrust(app))
	if err != nil {
		t.Fatal(err)
	}
	entry := catalogEntry{App: app, LogoPNG: catalogTestLogo(t)}
	entry.Name, entry.LogoURL, entry.Notifications = "Publisher Calendar", "", nil
	publication := catalogPublication{SchemaVersion: 1, Revision: 1, Apps: []catalogEntry{entry}}
	putCatalog(t, s, "calendar", cfg.AdminToken, publication, 401)
	publication.Apps[0].Certificate = strings.Repeat("d", 64)
	putCatalog(t, s, "calendar", key, publication, 409)
	publication.Apps[0] = entry
	putCatalog(t, s, "calendar", key, publication, 204)
	putCatalog(t, s, "calendar", key, publication, 204)
	pageHas("Publisher Calendar")
	if apps := s.catalogApps(); len(apps) != 1 || apps[0].Notifications != app.Notifications {
		t.Fatal("adoption lost the Store notification configuration")
	}
	if apps := s.catalogSyncApps(); len(apps) != 1 || apps[0].ID != "calendar" {
		t.Fatal("accepted catalog did not queue its app for release ingestion")
	}
	logo := request(s, "GET", "/assets/catalog-calendar.png", "", "")
	if logo.Code != 200 || logo.Header().Get("Content-Type") != "image/png" || !bytes.Equal(logo.Body.Bytes(), entry.LogoPNG) {
		t.Fatalf("published logo returned %d", logo.Code)
	}
	if w := request(s, "GET", "/v1/apps", tok, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"versionCode":20`) {
		t.Fatalf("adoption lost the verified release: %d %s", w.Code, w.Body.String())
	}
	publication.Apps[0].Name = "Revised Calendar"
	putCatalog(t, s, "calendar", key, publication, 409)
	publication.Revision = 2
	putCatalog(t, s, "calendar", key, publication, 204)
	publication.Revision = 3
	publication.Apps = []catalogEntry{}
	putCatalog(t, s, "calendar", key, publication, 204)
	if w := request(s, "GET", "/apps/calendar", "", ""); w.Code != 404 {
		t.Fatalf("withdrawn listing still visible: %d", w.Code)
	}
	if w := request(s, "GET", "/v1/releases/1/apk", tok, ""); w.Code != 404 {
		t.Fatalf("withdrawn download still accessible: %d", w.Code)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", "/apps/calendar", "", ""); w.Code != 404 {
		t.Fatalf("restart revived a withdrawn listing: %d", w.Code)
	}
	publication.Revision, publication.Apps = 2, []catalogEntry{entry}
	putCatalog(t, s, "calendar", key, publication, 409)
	publication.Revision = 4
	putCatalog(t, s, "calendar", key, publication, 204)
	pageHas("Publisher Calendar")

	delete(cfg.CatalogPublishers, "calendar")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pageHas("Bundled Calendar")
}

func TestPublishedCatalogScopesAndQueuesSharedRepository(t *testing.T) {
	key := strings.Repeat("k", 64)
	other := strings.Repeat("o", 64)
	s, err := New(Config{DataDir: t.TempDir(), AdminToken: strings.Repeat("a", 64), WebhookSecret: strings.Repeat("b", 64),
		CatalogPublishers: map[string]CatalogPublisher{
			"main":  {Key: key, Repositories: []string{"example/releases"}},
			"other": {Key: other, Repositories: []string{"example/other"}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	logo := catalogTestLogo(t)
	one := catalogEntry{App: App{ID: "one", Name: "App One", Description: "The first app.",
		Repo: "example/releases", Package: "com.example.one", Asset: "one.apk", Certificate: strings.Repeat("c", 64)}, LogoPNG: logo}
	two := one
	two.ID, two.Name, two.Package, two.Asset = "two", "App Two", "com.example.two", "two.apk"
	publication := catalogPublication{SchemaVersion: 1, Revision: 1, Apps: []catalogEntry{one, two}}
	putCatalog(t, s, "other", other, publication, 403)
	publication.Apps[1].Package = one.Package
	putCatalog(t, s, "main", key, publication, 409)
	publication.Apps[1] = two
	publication.Apps[0].Desktop, publication.Apps[1].Desktop = true, true
	putCatalog(t, s, "main", key, publication, 409)
	publication.Apps[0].Desktop, publication.Apps[1].Desktop = false, false
	publication.Apps[1].LogoPNG = []byte("invalid PNG")
	putCatalog(t, s, "main", key, publication, 400)
	if len(s.catalogApps()) != 0 {
		t.Fatal("invalid catalog was partially stored")
	}
	publication.Apps[1] = two
	putCatalog(t, s, "main", key, publication, 204)
	collision := one
	collision.Repo = "example/other"
	putCatalog(t, s, "other", other, catalogPublication{SchemaVersion: 1, Revision: 1, Apps: []catalogEntry{collision}}, 409)
	collision.ID = "three"
	putCatalog(t, s, "other", other, catalogPublication{SchemaVersion: 1, Revision: 1, Apps: []catalogEntry{collision}}, 409)
	payload := `{"action":"published","repository":{"full_name":"example/releases"}}`
	r := httptest.NewRequest("POST", "/webhooks/github", strings.NewReader(payload))
	r.Header.Set("X-GitHub-Event", "release")
	mac := hmac.New(sha256.New, []byte(s.cfg.WebhookSecret))
	mac.Write([]byte(payload))
	r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 202 || len(s.queue) != 2 || <-s.queue != "one" || <-s.queue != "two" {
		t.Fatalf("webhook did not queue both apps: %d", w.Code)
	}
}

func TestPublishedCatalogIngestsDesktopRelease(t *testing.T) {
	key := strings.Repeat("k", 64)
	s, err := New(Config{DataDir: t.TempDir(), AdminToken: strings.Repeat("a", 64), WebhookSecret: strings.Repeat("b", 64),
		CatalogPublishers: map[string]CatalogPublisher{"publisher": {Key: key, Repositories: []string{"example/desktop"}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entry := catalogEntry{App: App{ID: "desktop", Name: "Desktop App", Description: "A desktop app.", Repo: "example/desktop", Desktop: true},
		LogoPNG: catalogTestLogo(t)}
	putCatalog(t, s, "publisher", key, catalogPublication{SchemaVersion: 1, Revision: 1, Apps: []catalogEntry{entry}}, 204)
	manifest, err := json.Marshal(desktopFixture())
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/desktop/releases":
			fmt.Fprintf(w, `[{"tag_name":"desktop-v1.0.0","assets":[{"id":2,"name":"desktop-release.json","size":%d},{"id":3,"name":"Store-linux-x64.AppImage","size":8}]}]`, len(manifest))
		case "/repos/example/desktop/releases/assets/2":
			w.Write(manifest)
		case "/repos/example/desktop/releases/assets/3":
			fmt.Fprint(w, "appimage")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	s.api = upstream.URL
	if err := s.Sync(context.Background(), s.catalogApps()[0], ""); err != nil {
		t.Fatal(err)
	}
	tok, _ := enrollDevice(t, s)
	if w := request(s, "GET", "/v1/apps?platform=linux&arch=x64", tok, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Desktop App"`) {
		t.Fatalf("desktop catalog: %d %s", w.Code, w.Body.String())
	}
	if w := request(s, "GET", "/v1/desktop/releases/desktop/1/linux/x64", tok, ""); w.Code != 200 || w.Body.String() != "appimage" {
		t.Fatalf("desktop download: %d %s", w.Code, w.Body.String())
	}
}
