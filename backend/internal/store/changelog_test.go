package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChangelogNotesValidation(t *testing.T) {
	info := APKInfo{VersionName: "1.2.3", VersionCode: 9}
	valid := `{"schemaVersion":1,"version":"1.2.3","versionCode":9,"changes":["Calendar updates","Sign in with 🔥"]}`
	notes, err := changelogNotes([]byte(valid), info)
	if err != nil || notes != "• Calendar updates\n• Sign in with 🔥" {
		t.Fatalf("valid changelog: %q %v", notes, err)
	}
	for _, bad := range []string{
		`{`, `[]`, valid + `{}`, strings.Repeat("x", maxChangelog+1),
		strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1),
		strings.Replace(valid, `"version":"1.2.3"`, `"version":"1.2.2"`, 1),
		strings.Replace(valid, `"versionCode":9`, `"versionCode":8`, 1),
		strings.Replace(valid, `"versionCode":9`, `"versionCode":true`, 1),
		strings.Replace(valid, `"schemaVersion":1`, `"extra":1,"schemaVersion":1`, 1),
		`{"schemaVersion":1,"version":"1.2.3","versionCode":9,"changes":[]}`,
		`{"schemaVersion":1,"version":"1.2.3","versionCode":9,"changes":[" "]}`,
		`{"schemaVersion":1,"version":"1.2.3","versionCode":9,"changes":["a\nb"]}`,
		strings.Replace(valid, "Calendar updates", strings.Repeat("x", 2001), 1),
		strings.Replace(valid, "Calendar updates", "invalid\xff", 1),
	} {
		if _, err := changelogNotes([]byte(bad), info); err == nil {
			t.Errorf("accepted invalid changelog: %.100q", bad)
		}
	}
}

func TestChangelogIngestionAndExistingClientNotes(t *testing.T) {
	for _, scenario := range []string{"valid", "legacy", "wrong-version", "bad-digest", "bad-size", "duplicate", "malformed", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			s := testServer(t)
			a := s.cfg.Apps[0]
			apk := []byte("verified APK fixture")
			changes := []byte(`{"schemaVersion":1,"version":"1.4.2","versionCode":7,"changes":["Swipe between months","Choose visible calendars"]}`)
			if scenario == "wrong-version" {
				changes = []byte(strings.Replace(string(changes), "1.4.2", "1.4.1", 1))
			}
			if scenario == "malformed" {
				changes = []byte(`{"changes":`)
			}
			digest, size := "sha256:"+hash(string(changes)), len(changes)
			if scenario == "bad-digest" {
				digest = "sha256:" + strings.Repeat("0", 64)
			}
			if scenario == "bad-size" {
				size++
			}
			var apkDownloads atomic.Int64
			gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/" + a.Repo + "/releases/latest":
					assets := []map[string]any{{"id": 1, "name": a.Asset, "size": len(apk), "digest": "sha256:" + hash(string(apk))}}
					asset := map[string]any{"id": 2, "name": "changelog.json", "size": size, "digest": digest}
					if scenario != "legacy" {
						assets = append(assets, asset)
					}
					if scenario == "duplicate" {
						assets = append(assets, asset)
					}
					json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.4.2", "body": "Legacy release notes", "assets": assets})
				case "/repos/" + a.Repo + "/releases/assets/1":
					apkDownloads.Add(1)
					w.Write(apk)
				case "/repos/" + a.Repo + "/releases/assets/2":
					if scenario == "unavailable" {
						http.NotFound(w, r)
						return
					}
					if r.Header.Get("Accept") != "application/octet-stream" {
						t.Error("changelog must use the asset API")
					}
					w.Write(changes)
				default:
					http.NotFound(w, r)
				}
			}))
			defer gh.Close()
			s.api = gh.URL
			s.inspect = func(context.Context, string) (APKInfo, error) {
				return APKInfo{Package: a.Package, Certificate: a.Certificate, VersionCode: 7, VersionName: "1.4.2", MinSDK: 24}, nil
			}
			err := s.Sync(context.Background(), a, "")
			latest, queryErr := s.latest(context.Background(), a.ID)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			if scenario != "valid" && scenario != "legacy" {
				if err == nil || latest != nil {
					t.Fatalf("unverified changelog exposed a release: %v %+v", err, latest)
				}
				return
			}
			if err != nil || latest == nil {
				t.Fatalf("ingestion failed: %v", err)
			}
			want := "• Swipe between months\n• Choose visible calendars"
			if scenario == "legacy" {
				want = "Legacy release notes"
			}
			if latest.Notes != want {
				t.Fatalf("notes: got %q want %q", latest.Notes, want)
			}
			if scenario == "valid" {
				// Simulate a release cached by the old backend before JSON support.
				if _, err := s.db.Exec("UPDATE releases SET notes=? WHERE id=?", "Old release body", latest.ID); err != nil {
					t.Fatal(err)
				}
				if err := s.Sync(context.Background(), a, ""); err != nil {
					t.Fatal("cached JSON notes refresh:", err)
				}
				latest, _ = s.latest(context.Background(), a.ID)
				if latest == nil || latest.Notes != want || apkDownloads.Load() != 1 {
					t.Fatalf("cached notes were not refreshed without redownloading the APK: %+v, downloads %d", latest, apkDownloads.Load())
				}
			}
			tok, _ := enrollDevice(t, s)
			catalog := request(s, "GET", "/v1/apps", tok, "")
			encoded, _ := json.Marshal(want)
			if catalog.Code != 200 || !strings.Contains(catalog.Body.String(), fmt.Sprintf(`"notes":%s`, encoded)) {
				t.Fatalf("existing Android catalog API omitted the changelog: %s", catalog.Body.String())
			}
		})
	}
}
