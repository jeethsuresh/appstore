package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseCachePrunesOnStartup(t *testing.T) {
	s := testServer(t)
	app := s.cfg.Apps[0]
	app.Desktop = true
	other := App{ID: "other", Repo: "owner/other", Desktop: true}
	cfg := s.cfg
	cfg.Apps = []App{app, other}

	oldAPK, newAPK := hash("old-android"), hash("new-android")
	for i, digest := range []string{oldAPK, newAPK} {
		if _, err := s.db.Exec(`INSERT INTO releases(app_id,tag,version_code,version_name,sha256,size,min_sdk,notes,created,config_hash)
			VALUES(?,'v',?,'1.0',?,1,24,'','',?)`, app.ID, i+1, digest, appTrust(app)); err != nil {
			t.Fatal(err)
		}
	}
	oldDesktop, shared, linux, windows := hash("old-desktop"), hash("shared"), hash("linux"), hash("windows")
	insertDesktop := func(app App, version int64, digests ...string) {
		t.Helper()
		release := desktopFixture()
		release.VersionCode = version
		release.Artifacts = nil
		for _, digest := range digests {
			release.Artifacts = append(release.Artifacts, DesktopArtifact{SHA256: digest})
		}
		manifest, err := json.Marshal(release)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("INSERT INTO desktop_releases VALUES(?,?,?,?)", app.ID, version, manifest, desktopTrust(app)); err != nil {
			t.Fatal(err)
		}
	}
	insertDesktop(app, 1, oldDesktop, shared)
	insertDesktop(app, 2, linux, windows)
	insertDesktop(other, 1, shared)
	previousRepo := app
	previousRepo.Repo = "owner/previous"
	previousTrust := hash("previous trust")
	insertDesktop(previousRepo, 3, previousTrust)

	keep := []string{"apks/" + newAPK + ".apk", "desktop/" + shared, "desktop/" + linux, "desktop/" + windows, "desktop/notes.txt"}
	prune := []string{"apks/" + oldAPK + ".apk", "apks/ingest-123.apk", "desktop/" + oldDesktop, "desktop/" + previousTrust, "desktop/" + hash("orphan"), "desktop/ingest-456"}
	for _, files := range [][]string{keep, prune} {
		for _, name := range files {
			if err := os.WriteFile(filepath.Join(cfg.DataDir, name), []byte("cache"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, name)); err != nil {
			t.Errorf("missing retained file %s: %v", name, err)
		}
	}
	for _, name := range prune {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected %s to be removed: %v", name, err)
		}
	}
	var count int
	if err := restarted.db.QueryRow("SELECT COUNT(*) FROM releases").Scan(&count); err != nil || count != 2 {
		t.Fatalf("release history changed: %d Android releases, %v", count, err)
	}
}
