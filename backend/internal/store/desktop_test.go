package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"private-store/internal/gravitas"
	"strings"
	"testing"
	"time"
)

func desktopFixture() DesktopRelease {
	sum := sha256.Sum256([]byte("appimage"))
	return DesktopRelease{SchemaVersion: 1, Version: "1.0.0", VersionCode: 1, Changes: []string{"Desktop release."}, Artifacts: []DesktopArtifact{{Platform: "linux", Arch: "x64", File: "Store-linux-x64.AppImage", Entry: "Store-linux-x64.AppImage", SHA256: hex.EncodeToString(sum[:]), Size: 8}}}
}
func TestDesktopManifestRejectsUnsafeAndAmbiguousTargets(t *testing.T) {
	base := desktopFixture()
	data, _ := json.Marshal(base)
	if _, err := parseDesktopManifest(data, "desktop-v1.0.0"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DesktopRelease){
		"path":           func(r *DesktopRelease) { r.Artifacts[0].File = "../bad.AppImage" },
		"entry":          func(r *DesktopRelease) { r.Artifacts[0].Entry = "../../bad" },
		"digest":         func(r *DesktopRelease) { r.Artifacts[0].SHA256 = "invalid" },
		"target":         func(r *DesktopRelease) { r.Artifacts[0].Arch = "any" },
		"duplicate":      func(r *DesktopRelease) { r.Artifacts = append(r.Artifacts, r.Artifacts[0]) },
		"notes":          func(r *DesktopRelease) { r.Changes = []string{"\x00"} },
		"unsafe-version": func(r *DesktopRelease) { r.VersionCode = 9007199254740992 },
	} {
		t.Run(name, func(t *testing.T) {
			next := desktopFixture()
			mutate(&next)
			data, _ := json.Marshal(next)
			if _, err := parseDesktopManifest(data, "desktop-v1.0.0"); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	if _, err := parseDesktopManifest(data, "v2.0.0"); err == nil {
		t.Fatal("mismatched tag accepted")
	}
}
func TestDesktopIngestionIsolationImmutabilityAndRepair(t *testing.T) {
	s := testServer(t)
	a := App{ID: "desktop", Name: "Desktop", Repo: "owner/desktop", Desktop: true}
	s.cfg.Apps = append(s.cfg.Apps, a)
	manifest := desktopFixture()
	data, _ := json.Marshal(manifest)
	badDownload := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/desktop/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v9.0.0","assets":[{"id":1,"name":"mobile.apk","size":8}]}`)
		case "/repos/owner/desktop/releases":
			fmt.Fprintf(w, `[{"tag_name":"desktop-v1.0.0","assets":[{"id":2,"name":"desktop-release.json","size":%d},{"id":3,"name":"Store-linux-x64.AppImage","size":8}]}]`, len(data))
		case "/repos/owner/desktop/releases/assets/2":
			w.Write(data)
		case "/repos/owner/desktop/releases/assets/3":
			if badDownload {
				fmt.Fprint(w, "badimage")
			} else {
				fmt.Fprint(w, "appimage")
			}
		default:
			t.Error("unexpected upstream path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	s.api = upstream.URL
	badDownload = true
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if rel, _ := s.latestDesktop(context.Background(), a); rel != nil {
		t.Fatal("partial release exposed")
	}
	badDownload = false
	if err := s.Sync(context.Background(), a, ""); err != nil {
		t.Fatal(err)
	}
	tok, id := enrollDevice(t, s)
	if w := request(s, "GET", "/v1/apps", tok, ""); strings.Contains(w.Body.String(), `"id":"desktop"`) {
		t.Fatal("desktop leaked into Android catalog")
	}
	if w := request(s, "GET", "/v1/apps?platform=linux&arch=x64", tok, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"desktop"`) || strings.Contains(w.Body.String(), `"id":"daybreak"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(s, "GET", "/v1/desktop/apps?platform=macos&arch=arm64", "", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"id":"desktop"`) {
		t.Fatal("incompatible target exposed")
	}
	path := "/v1/desktop/releases/desktop/1/linux/x64"
	if request(s, "GET", path, "", "").Code != 401 {
		t.Fatal("anonymous artifact download")
	}
	if w := request(s, "GET", path, tok, ""); w.Code != 200 || w.Body.String() != "appimage" {
		t.Fatal("verified download failed", w.Code)
	}
	cache := filepath.Join(s.cfg.DataDir, "desktop", manifest.Artifacts[0].SHA256)
	os.Remove(cache)
	if err := s.Sync(context.Background(), a, ""); err != nil {
		t.Fatal("cache repair failed", err)
	}
	manifest.Changes = []string{"Edited release"}
	data, _ = json.Marshal(manifest)
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("mutable release accepted")
	}
	request(s, "DELETE", "/admin/devices/"+id, s.cfg.AdminToken, "")
	if request(s, "GET", path, tok, "").Code != 401 {
		t.Fatal("revoked device downloaded desktop artifact")
	}
}
func TestDesktopOnlyAndUniversalConfiguration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "apps.json")
	for _, value := range []string{`[{"id":"desktop","name":"Desktop","repository":"owner/desktop","desktop":true}]`, `[{"id":"both","name":"Both","repository":"owner/both","desktop":true,"packageName":"dev.test.app","assetName":"app.apk"}]`} {
		os.WriteFile(p, []byte(value), 0600)
		if _, err := LoadApps(p); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(p, []byte(`[{"id":"empty","name":"Empty","repository":"owner/empty"}]`), 0600)
	if _, err := LoadApps(p); err == nil {
		t.Fatal("app with no platforms accepted")
	}
}
func TestLegacyDesktopAssetsNeedOnlyOneCatalogFlag(t *testing.T) {
	var rel githubRelease
	json.Unmarshal([]byte(`{"tag_name":"v0.1.4","body":"A desktop update.\nAnother change.","assets":[{"id":1,"name":"Reader-0.1.4-linux-x86_64.AppImage","size":8,"digest":"sha256:`+strings.Repeat("a", 64)+`"},{"id":2,"name":"Reader-0.1.4-mac-arm64.dmg","size":8,"digest":"sha256:`+strings.Repeat("b", 64)+`"},{"id":3,"name":"Reader-0.1.4-win-x64.exe","size":8,"digest":"sha256:`+strings.Repeat("c", 64)+`"}]}`), &rel)
	data, err := legacyDesktopManifest(App{Name: "RSS Reader"}, rel)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := parseDesktopManifest(data, rel.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != 3 || manifest.VersionCode != 1004 || manifest.Artifacts[1].Entry != "RSS Reader.app" {
		t.Fatal("legacy platform inference failed")
	}
	rel.Assets[0].Digest = ""
	if _, err := legacyDesktopManifest(App{Name: "RSS Reader"}, rel); err == nil {
		t.Fatal("unverified legacy artifact accepted")
	}
}
func TestDesktopEnrollmentUsesValidatedWebSessionAndRevocation(t *testing.T) {
	s := nativeFixture(t)
	if request(s, "POST", "/v1/auth/desktop/enroll", "", `{"deviceName":"Desktop"}`).Code != 403 {
		t.Fatal("missing CSRF marker accepted")
	}
	identity := gravitas.Identity{Issuer: "https://gravitas.cool", Subject: "stable-user", Username: "native-user", UserID: "@native-user:gravitas.cool", SessionID: "family-1", CheckToken: strings.Repeat("p", 43), SessionExpires: time.Now().Add(24 * time.Hour).Unix()}
	cookie := httptest.NewRecorder()
	if err := s.cfg.Federation.SetSession(cookie, identity); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://apps.gravitas.cool/v1/auth/desktop/enroll", strings.NewReader(`{"deviceName":"Desktop"}`))
	r.Header.Set("X-Gravitas-CSRF", "1")
	r.Header.Set("Origin", "https://apps.gravitas.cool")
	r.AddCookie(cookie.Result().Cookies()[0])
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	tok := field(t, w, "token")
	if request(s, "GET", "/v1/apps?platform=linux&arch=x64", tok, "").Code != 200 {
		t.Fatal("desktop device not enrolled")
	}
	request(s, "DELETE", "/v1/device", tok, "")
	if request(s, "GET", "/v1/apps?platform=linux&arch=x64", tok, "").Code != 401 {
		t.Fatal("revocation failed")
	}
}

func TestDesktopNativeLinuxZIP(t *testing.T) {
	rel := desktopFixture()
	rel.Artifacts[0].File = "Spacetime-linux-x64.zip"
	rel.Artifacts[0].Entry = "spacetime"
	data, _ := json.Marshal(rel)
	parsed, err := parseDesktopManifest(data, "desktop-v1.0.0")
	if err != nil || parsed.Artifacts[0].Entry != "spacetime" {
		t.Fatalf("native Linux bundle rejected: %v", err)
	}
	rel.Artifacts[0].Entry = "../spacetime"
	data, _ = json.Marshal(rel)
	if _, err := parseDesktopManifest(data, "desktop-v1.0.0"); err == nil {
		t.Fatal("unsafe native launch path accepted")
	}
}

func TestUniversalDiscoverySharesRequestsAndRefreshesNextPoll(t *testing.T) {
	requests := 0
	latest := "v1.0.0"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/repos/owner/app/releases" {
			t.Errorf("universal lookup wasted a latest request: %s", r.URL.Path)
		}
		fmt.Fprintf(w, `[{"tag_name":"desktop-%s","assets":[{"name":"desktop-release.json"}]},{"tag_name":"%s","assets":[{"name":"app.apk"}]}]`, latest, latest)
	}))
	defer api.Close()
	s := &Server{api: api.URL, client: api.Client()}
	app := App{Repo: "owner/app", Package: "example.universal", Desktop: true}
	desktop := func(r githubRelease) bool { return strings.HasPrefix(r.Tag, "desktop-") }
	mobile := func(r githubRelease) bool { return !strings.HasPrefix(r.Tag, "desktop-") }
	ctx := withReleaseDiscovery(context.Background())
	for _, matcher := range []func(githubRelease) bool{desktop, mobile} {
		if _, err := s.findRelease(ctx, app, "", matcher); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("wanted one request for both platforms, got %d", requests)
	}
	latest = "v1.1.0"
	rel, err := s.findRelease(withReleaseDiscovery(context.Background()), app, "", mobile)
	if err != nil || rel.Tag != latest || requests != 2 {
		t.Fatal("next poll did not discover fresh releases", err, rel.Tag, requests)
	}
}
