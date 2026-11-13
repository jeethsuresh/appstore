package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestWorkAndNewsVerifiedReleaseIngestion(t *testing.T) {
	apps, err := LoadApps("../../../config/apps.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if a.ID != "work" && a.ID != "news" {
			continue
		}
		for _, tag := range []string{"webview-v0.1.0", "v0.1.0", "webview-v0.2.0"} {
			t.Run(a.ID+"/"+tag, func(t *testing.T) {
				s := testServer(t)
				s.cfg.Apps = []App{a}
				apk := []byte("verified wrapper APK fixture")
				changes := []byte(`{"schemaVersion":1,"version":"0.1.0","versionCode":1,"changes":["Install Work and News as separate Android apps from Private Store."]}`)
				assets := []map[string]any{
					{"id": 1, "name": a.Asset, "size": len(apk), "digest": "sha256:" + hash(string(apk))},
					{"id": 2, "name": "changelog.json", "size": len(changes), "digest": "sha256:" + hash(string(changes))},
				}
				gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/repos/" + a.Repo + "/releases/latest":
						// Latest remains Private Store; discover the wrappers from the release list.
						json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.6.12", "assets": []any{}})
					case "/repos/" + a.Repo + "/releases":
						json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": tag, "assets": assets}})
					case "/repos/" + a.Repo + "/releases/assets/1":
						w.Write(apk)
					case "/repos/" + a.Repo + "/releases/assets/2":
						w.Write(changes)
					default:
						http.NotFound(w, r)
					}
				}))
				defer gh.Close()
				s.api = gh.URL
				s.inspect = func(context.Context, string) (APKInfo, error) {
					return APKInfo{Package: a.Package, Certificate: a.Certificate, VersionName: "0.1.0", VersionCode: 1, MinSDK: 26}, nil
				}
				err := s.Sync(context.Background(), a, "")
				latest, queryErr := s.latest(context.Background(), a.ID)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				if tag != "webview-v0.1.0" {
					if err == nil || latest != nil {
						t.Fatalf("mismatched release tag exposed a release: %v %+v", err, latest)
					}
					return
				}
				if err != nil || latest == nil || latest.VersionName != "0.1.0" || latest.VersionCode != 1 || latest.Tag != tag {
					t.Fatalf("wrapper ingestion failed: %v %+v", err, latest)
				}
				if err := s.Sync(context.Background(), a, ""); err != nil {
					t.Fatal("cached wrapper notes refresh:", err)
				}
				page := request(s, "GET", "/apps/"+a.ID, "", "")
				if page.Code != 200 || !strings.Contains(page.Body.String(), "v0.1.0") || !strings.Contains(page.Body.String(), "Install Work and News as separate Android apps from Private Store.") {
					t.Fatalf("wrapper version and release notes missing from Store page: %s", page.Body.String())
				}
			})
		}
	}
}

func TestAndroidReleaseTagPrefixConfiguration(t *testing.T) {
	for _, prefix := range []string{"", "v", "webview-v", "../evil", "webview/v", "webview v"} {
		t.Run(prefix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "apps.json")
			data, err := json.Marshal([]App{{ID: "work", Name: "Work", Description: "Your hosted workspace.", LogoURL: "/assets/work.svg", Package: "cool.gravitas.work", Repo: "jeethsuresh/appstore", Asset: "work-android.apk", ReleaseTagPrefix: prefix}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = LoadApps(path)
			valid := prefix == "" || prefix == "v" || prefix == "webview-v"
			if (err == nil) != valid {
				t.Fatalf("prefix %q: %v", prefix, err)
			}
		})
	}
}
