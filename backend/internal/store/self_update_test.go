package store

import (
	"os"
	"strings"
	"testing"
)

func TestProductionCatalogIncludesStoreWithPermanentReleaseIdentity(t *testing.T) {
	apps, err := LoadApps("../../../config/apps.json")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile("../../../android/release-certificate.sha256")
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if app.ID != "appstore" {
			continue
		}
		if app.Package != "dev.jeeth.privatestore" || app.Repo != "jeethsuresh/appstore" || app.Asset != "appstore-android.apk" || app.Certificate != strings.TrimSpace(string(cert)) {
			t.Fatal("Self-updates must retain the published store identity and permanent signing certificate")
		}
		s := testServer(t)
		s.cfg.Apps = append(s.cfg.Apps, app)
		page := request(s, "GET", "/apps/appstore", "", "")
		if page.Code != 200 || !strings.Contains(page.Body.String(), "Keep the store current") || strings.Contains(page.Body.String(), `href="/download/appstore.apk"`) {
			t.Fatal("Self-update page must describe updates and keep bootstrap downloads gated")
		}
		return
	}
	t.Fatal("The production catalog is missing Private Store")
}
