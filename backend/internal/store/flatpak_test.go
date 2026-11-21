package store

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"private-store/internal/gravitas"
	"strings"
	"testing"
)

func flatpakFixture() DesktopRelease {
	release := desktopFixture()
	release.Artifacts = append(release.Artifacts, DesktopArtifact{Platform: "linux", Arch: "x64", Format: "flatpak", File: "Bazaar-linux-x64.flatpak", Entry: "cool.gravitas.privatestore", SHA256: hash("flatpak"), Size: 7})
	return release
}

func TestFlatpakManifestKeepsPortableVariantAndRejectsInvalidPackages(t *testing.T) {
	release := flatpakFixture()
	for _, platform := range []string{"macos", "windows"} {
		for _, arch := range []string{"x64", "arm64"} {
			a := release.Artifacts[0]
			a.Platform, a.Arch, a.File, a.Entry = platform, arch, "Archive.zip", "Bazaar.exe"
			if platform == "macos" {
				a.Entry = "Bazaar.app"
			}
			release.Artifacts = append(release.Artifacts, a)
		}
	}
	arm := release.Artifacts[0]
	arm.Arch = "arm64"
	release.Artifacts = append(release.Artifacts, arm)
	data, _ := json.Marshal(release)
	if _, err := parseDesktopManifest(data, "desktop-v1.0.0"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*DesktopArtifact){
		func(a *DesktopArtifact) { a.Format = "unknown" },
		func(a *DesktopArtifact) { a.Arch = "arm64" },
		func(a *DesktopArtifact) { a.Entry = "../outside" },
		func(a *DesktopArtifact) { a.File = "Bazaar.AppImage" },
		func(a *DesktopArtifact) { a.Format = "" },
	} {
		r := flatpakFixture()
		mutate(&r.Artifacts[1])
		data, _ := json.Marshal(r)
		if _, err := parseDesktopManifest(data, "desktop-v1.0.0"); err == nil {
			t.Fatal("invalid Flatpak accepted", r.Artifacts[1])
		}
	}
}

func TestFlatpakCatalogAndDownloadsAreOptInAndKeepManagedApps(t *testing.T) {
	s := testServer(t)
	for _, id := range []string{"appstore", "native-game"} {
		a := App{ID: id, Name: id, Repo: "owner/" + id, Desktop: true}
		s.cfg.Apps = append(s.cfg.Apps, a)
		release := desktopFixture()
		if id == "appstore" {
			release = flatpakFixture()
		}
		data, _ := json.Marshal(release)
		if _, err := s.db.Exec("INSERT INTO desktop_releases VALUES(?,?,?,?)", id, 1, data, desktopTrust(a)); err != nil {
			t.Fatal(err)
		}
		for _, artifact := range release.Artifacts {
			content := "appimage"
			if artifact.Format == "flatpak" {
				content = "flatpak"
			}
			if err := os.WriteFile(filepath.Join(s.cfg.DataDir, "desktop", artifact.SHA256), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	token, device := enrollDevice(t, s)
	for _, suffix := range []string{"", "&format=flatpak"} {
		response := request(s, "GET", "/v1/apps?platform=linux&arch=x64"+suffix, token, "")
		var catalog struct {
			Apps []App `json:"apps"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil || len(catalog.Apps) != 2 {
			t.Fatal(response.Body.String(), err)
		}
		for _, app := range catalog.Apps {
			if len(app.DesktopRelease.Artifacts) != 1 {
				t.Fatal("legacy clients received ambiguous packages")
			}
			want := ""
			if app.ID == "appstore" && suffix != "" {
				want = "flatpak"
			}
			if app.DesktopRelease.Artifacts[0].Format != want {
				t.Fatal("incorrect format", app.ID)
			}
		}
	}
	for _, suffix := range []string{"", "?format=flatpak"} {
		route := "/v1/desktop/releases/appstore/1/linux/x64" + suffix
		if request(s, "GET", route, "", "").Code != 401 {
			t.Fatal("anonymous download")
		}
		response := request(s, "GET", route, token, "")
		want := "appimage"
		if suffix != "" {
			want = "flatpak"
		}
		if response.Code != 200 || response.Body.String() != want {
			t.Fatal("wrong package", response.Code, response.Body.String())
		}
	}
	federation, err := gravitas.New("https://gravitas.cool", "https://apps.gravitas.cool", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Federation = federation
	session := httptest.NewRecorder()
	if err := federation.SetSession(session, webTestIdentity(federation, "flatpak-user", "Flatpak user")); err != nil {
		t.Fatal(err)
	}
	webRequest := func(route string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://apps.gravitas.cool"+route, nil)
		r.AddCookie(session.Result().Cookies()[0])
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	page := webRequest("/")
	if !strings.Contains(page.Body.String(), "format=flatpak") {
		t.Fatal("Flatpak bootstrap download missing")
	}
	if response := webRequest("/download/desktop/linux/x64?format=flatpak"); response.Code != 200 || response.Body.String() != "flatpak" {
		t.Fatal("incorrect bootstrap", response.Code)
	}
	if request(s, "GET", "/download/desktop/linux/x64?format=flatpak", "", "").Code != 401 {
		t.Fatal("anonymous bootstrap download")
	}
	request(s, "DELETE", "/admin/devices/"+device, s.cfg.AdminToken, "")
	if request(s, "GET", "/v1/desktop/releases/appstore/1/linux/x64?format=flatpak", token, "").Code != 401 {
		t.Fatal("revoked device downloaded Flatpak")
	}
}
