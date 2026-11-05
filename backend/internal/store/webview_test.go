package store

import (
	"strings"
	"testing"
)

func TestWorkAndNewsCatalogPages(t *testing.T) {
	apps, err := LoadApps("../../../config/apps.json")
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	s.cfg.Apps = apps
	for _, id := range []string{"work", "news"} {
		w := request(s, "GET", "/apps/"+id, "", "")
		if w.Code != 200 {
			t.Fatalf("%s page: %d", id, w.Code)
		}
		for _, text := range []string{"https://" + id + ".gravitas.cool", "/assets/" + id + ".svg", "Android 8.0+", "right"} {
			if !strings.Contains(w.Body.String(), text) {
				t.Fatalf("%s missing %q", id, text)
			}
		}
		icon := request(s, "GET", "/assets/"+id+".svg", "", "")
		if icon.Code != 200 || !strings.Contains(icon.Body.String(), "<svg") {
			t.Fatalf("%s icon unavailable", id)
		}
	}
}
