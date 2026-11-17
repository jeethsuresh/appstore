package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopDiscoverySelectsNewestVersionRegardlessOfReleaseOrder(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, tagPrefix string
		paginated               bool
	}{
		{name: "bazaar", tagPrefix: "desktop-v"},
		{name: "shared-repository-channel", prefix: "tool-", tagPrefix: "desktop-v"},
		{name: "legacy-tag", tagPrefix: "v"},
		{name: "later-page", tagPrefix: "desktop-v", paginated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(t)
			a := App{ID: "desktop", Name: "Desktop", Repo: "owner/desktop", Desktop: true, DesktopReleaseTagPrefix: tc.prefix}
			s.cfg.Apps = append(s.cfg.Apps, a)
			old, next := desktopFixture(), desktopFixture()
			old.Version, old.VersionCode = "1.0.9", 10
			next.Version, next.VersionCode = "1.0.10", 11
			oldData, _ := json.Marshal(old)
			nextData, _ := json.Marshal(next)
			oldTag, nextTag := tc.prefix+tc.tagPrefix+old.Version, tc.prefix+tc.tagPrefix+next.Version
			release := func(tag string, id int, size int) map[string]any {
				return map[string]any{"tag_name": tag, "body": "Notes for " + tag, "assets": []map[string]any{
					{"id": id, "name": "desktop-release.json", "size": size},
					{"id": 5, "name": "Store-linux-x64.AppImage", "size": 8},
				}}
			}
			older, newer := release(oldTag, 2, len(oldData)), release(nextTag, 3, len(nextData))
			draft := release(tc.prefix+tc.tagPrefix+"9.0.0", 6, len(nextData))
			draft["draft"] = true
			prerelease := release(tc.prefix+tc.tagPrefix+"8.0.0", 7, len(nextData))
			prerelease["prerelease"] = true
			missing := map[string]any{"tag_name": tc.prefix + tc.tagPrefix + "7.0.0"}
			foreign := release("another-channel-desktop-v6.0.0", 8, len(nextData))
			pageOne := []map[string]any{older, newer, draft, prerelease, missing, foreign}
			if tc.paginated {
				pageOne = []map[string]any{older}
				for len(pageOne) < 50 {
					pageOne = append(pageOne, draft)
				}
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/desktop/releases/tags/" + oldTag:
					json.NewEncoder(w).Encode(older)
				case "/repos/owner/desktop/releases":
					if r.URL.Query().Get("page") == "1" {
						json.NewEncoder(w).Encode(pageOne)
					} else if tc.paginated && r.URL.Query().Get("page") == "2" {
						json.NewEncoder(w).Encode([]map[string]any{newer, draft, prerelease, missing, foreign})
					} else {
						t.Errorf("unexpected release page: %s", r.URL)
						http.NotFound(w, r)
					}
				case "/repos/owner/desktop/releases/assets/2":
					w.Write(oldData)
				case "/repos/owner/desktop/releases/assets/3":
					w.Write(nextData)
				case "/repos/owner/desktop/releases/assets/5":
					fmt.Fprint(w, "appimage")
				default:
					t.Errorf("unexpected asset or release lookup: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			s.api = upstream.URL
			// Explicit tag selection remains exact; the next poll must upgrade
			// this installation even when GitHub lists the older release first.
			if err := s.Sync(context.Background(), a, oldTag); err != nil {
				t.Fatal(err)
			}
			if got, err := s.latestDesktop(context.Background(), a); err != nil || got == nil || got.Tag != oldTag {
				t.Fatalf("explicit tag sync: release=%+v err=%v", got, err)
			}
			for poll := 0; poll < 2; poll++ {
				if err := s.Sync(context.Background(), a, ""); err != nil {
					t.Fatal(err)
				}
				got, err := s.latestDesktop(context.Background(), a)
				if err != nil || got == nil || got.Tag != nextTag || got.VersionCode != 11 || got.Notes != "Notes for "+nextTag {
					t.Fatalf("poll selected wrong release or notes: release=%+v err=%v", got, err)
				}
			}
			w := request(s, "GET", "/v1/desktop/apps?platform=linux&arch=x64", "", "")
			var catalog struct{ Apps []App }
			if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil || w.Code != 200 || len(catalog.Apps) != 1 || catalog.Apps[0].DesktopRelease.Version != "1.0.10" {
				t.Fatalf("catalog did not advertise the update: %d %s (%v)", w.Code, w.Body.String(), err)
			}
		})
	}
}

func TestDesktopReleaseNumericVersionPreference(t *testing.T) {
	for _, pair := range [][2]string{
		{"desktop-v0.1.10", "desktop-v0.1.9"},
		{"desktop-v0.10.0", "desktop-v0.9.99"},
		{"desktop-v10.0.0", "desktop-v9.99.99"},
		{"desktop-v999999999999999999999999.0.0", "desktop-v99999999999999999999999.99.99"},
		{"v1.0.10", "desktop-v1.0.9"},
	} {
		for _, prefix := range []string{"", "tool-"} {
			a := App{DesktopReleaseTagPrefix: prefix}
			newer, older := githubRelease{Tag: prefix + pair[0]}, githubRelease{Tag: prefix + pair[1]}
			if !desktopReleaseNewer(a, newer, older) || desktopReleaseNewer(a, older, newer) || desktopReleaseNewer(a, newer, newer) {
				t.Fatalf("incorrect numeric ordering for %s and %s", newer.Tag, older.Tag)
			}
		}
	}
}
