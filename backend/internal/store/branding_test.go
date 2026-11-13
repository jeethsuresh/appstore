package store

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogRequiresBranding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	valid := App{ID: "work", Name: "Work", Description: "Open your shared workspace.", LogoURL: "/assets/work.svg", Repo: "owner/work", Desktop: true}
	for _, check := range []struct {
		label, want string
		edit        func(*App)
	}{
		{"valid", "", func(*App) {}},
		{"empty name", "human-readable", func(a *App) { a.Name = " " }},
		{"URL as name", "human-readable", func(a *App) { a.Name = "https://work.gravitas.cool" }},
		{"blank description", "description", func(a *App) { a.Description = " \n " }},
		{"missing logo", "logoUrl", func(a *App) { a.LogoURL = "" }},
		{"generic logo", "logoUrl", func(a *App) { a.LogoURL = "/assets/icon.svg" }},
		{"missing asset", "does not exist", func(a *App) { a.LogoURL = "/assets/missing.svg" }},
		{"foreign asset", "logoUrl", func(a *App) { a.LogoURL = "https://elsewhere.test/logo.svg" }},
		{"invalid website", "HTTPS URL", func(a *App) { a.Website = "http://work.gravitas.cool" }},
	} {
		t.Run(check.label, func(t *testing.T) {
			app := valid
			check.edit(&app)
			data, err := json.Marshal([]App{app})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = LoadApps(path)
			if check.want == "" && err != nil || check.want != "" && (err == nil || !strings.Contains(err.Error(), check.want)) {
				t.Fatalf("got %v, expected %q", err, check.want)
			}
		})
	}
}

func TestEveryCatalogAppHasBrandedPublicPage(t *testing.T) {
	apps, err := LoadApps("../../../config/apps.json")
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	s.cfg.Apps = apps
	home := request(s, http.MethodGet, "/", "", "")
	if home.Code != http.StatusOK {
		t.Fatalf("collection returned %d", home.Code)
	}
	logos := map[string]string{}
	for _, app := range apps {
		t.Run(app.ID, func(t *testing.T) {
			if previous := logos[app.LogoURL]; previous != "" {
				t.Fatalf("logo reused by %s", previous)
			}
			logos[app.LogoURL] = app.ID
			path := "/apps/" + app.ID
			page := request(s, http.MethodGet, path, "", "")
			if page.Code != http.StatusOK {
				t.Fatalf("page returned %d", page.Code)
			}
			displayURL := "apps.gravitas.cool" + path
			if app.Website != "" {
				displayURL = strings.TrimPrefix(app.Website, "https://")
			}
			for _, body := range []string{home.Body.String(), page.Body.String()} {
				decoded := html.UnescapeString(body)
				for _, expected := range []string{app.Name, app.Description, `src="` + app.LogoURL + `"`, displayURL} {
					if !strings.Contains(decoded, expected) {
						t.Errorf("page lacks %q", expected)
					}
				}
			}
			asset := request(s, http.MethodGet, app.LogoURL, "", "")
			if asset.Code != http.StatusOK || asset.Body.Len() == 0 || !strings.HasPrefix(asset.Header().Get("Content-Type"), "image/") {
				t.Errorf("logo not served as an image: %d %s", asset.Code, asset.Header().Get("Content-Type"))
			}
		})
	}
}

func TestIOSOnlyAppUsesAppleListing(t *testing.T) {
	app := App{ID: "sample", Name: "Sample App", Description: "A small app for iPhone.", LogoURL: "/assets/work.svg", Repo: "owner/sample", IOS: "app-store", IOSURL: "https://apps.apple.com/ca/app/sample-app/id123456789"}
	data, err := json.Marshal([]App{app})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "apps.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	apps, err := LoadApps(path)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	s.cfg.Apps = apps
	page := request(s, http.MethodGet, "/apps/sample", "", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), app.IOSURL) ||
		strings.Contains(page.Body.String(), "Install Sample App inside Private Store") {
		t.Fatalf("unexpected iOS-only listing: %d %s", page.Code, page.Body)
	}
	app.IOSURL = "https://apps.apple.com.evil.example/app/id123456789"
	if err := validateIOS(app); err == nil {
		t.Fatal("accepted an iOS listing outside Apple")
	}
}
