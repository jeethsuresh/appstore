package store

import (
	"strings"
	"testing"
)

func TestHavenCatalogPinsItsReleaseIdentityAndDescribesForegroundBackup(t *testing.T) {
	apps, err := LoadApps("../../../config/apps.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if app.ID != "haven" {
			continue
		}
		if app.Package != "cool.gravitas.haven" || app.Repo != "jeethsuresh/haven" || app.Asset != "Haven-{tag}-android.apk" || app.Certificate != "3c01375ebd40c543f9d6d1a6c1aeb18c689188eee35341a3b1ba3b89a397c085" {
			t.Fatal("Haven must retain its permanent package, signing certificate, and release asset pattern")
		}
		s := testServer(t)
		s.cfg.Apps = append(s.cfg.Apps, app)
		page := request(s, "GET", "/apps/haven", "", "")
		if page.Code != 200 || !strings.Contains(page.Body.String(), "while Haven is open") || !strings.Contains(page.Body.String(), "https://haven.gravitas.cool") || !strings.Contains(page.Body.String(), "browser localStorage") || !strings.Contains(page.Body.String(), "device reports") || strings.Contains(page.Body.String(), "status and small thumbnails") {
			t.Fatal("Haven's Store page must describe the real first-release behavior")
		}
		return
	}
	t.Fatal("Production catalog is missing Haven")
}
