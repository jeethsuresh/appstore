package store

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

const samplePublishPath = "/v1/desktop/publish/sample-game"

var sampleGame = App{ID: "sample-game", Name: "Sample Game", Repo: "example/sample", Desktop: true, DesktopRequiresSignIn: true, DesktopReleaseTagPrefix: "sample-", MacOSNotarizedFrom: 4000}

func TestDesktopPublisherSettingsAreScopedToPrivateListings(t *testing.T) {
	second := sampleGame
	second.ID, second.Name = "another-game", "Another Game"
	public := sampleGame
	public.ID, public.DesktopRequiresSignIn = "public-game", false
	apps := []App{sampleGame, second, public}
	settings := map[string]string{"SAMPLE_GAME_PUBLISH_TOKEN_SHA256": hash("first"), "PUBLIC_GAME_PUBLISH_TOKEN_SHA256": hash("ignored")}
	read := func(k string) string { return settings[k] }
	publishers, err := DesktopPublishers(apps, read)
	if err != nil || len(publishers) != 1 || publishers[sampleGame.ID] != hash("first") {
		t.Fatalf("unexpected desktop publishers: count=%d err=%v", len(publishers), err)
	}
	for _, invalid := range []string{"invalid", hash("first")} {
		settings["ANOTHER_GAME_PUBLISH_TOKEN_SHA256"] = invalid
		if _, err := DesktopPublishers(apps, read); err == nil {
			t.Fatal("accepted a malformed or shared publisher verifier")
		}
	}
}

func publisherFixture(t *testing.T) (*Server, string, DesktopRelease, []byte) {
	t.Helper()
	s := nativeFixture(t)
	s.cfg.Apps = append(s.cfg.Apps, sampleGame)
	key := strings.Repeat("e", 64)
	s.cfg.DesktopPublisherHashes = map[string]string{sampleGame.ID: hash(key)}

	var buf bytes.Buffer
	zipper := zip.NewWriter(&buf)
	for file, content := range map[string]string{"Info.plist": "<plist/>", "MacOS/Sample Game": "universal game", "Resources/Sample Game.pck": "GDPCexample", "_CodeSignature/CodeResources": "<plist/>"} {
		w, err := zipper.Create("Sample Game.app/Contents/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	arm := DesktopArtifact{Platform: "macos", Arch: "arm64", File: "SampleGame-0.4.0-mac-universal.zip", Entry: "Sample Game.app", SHA256: hash(buf.String()), Size: int64(buf.Len())}
	intel := arm
	intel.Arch = "x64"
	rel := DesktopRelease{SchemaVersion: 1, Version: "0.4.0", VersionCode: 4000, Changes: []string{"New arenas."}, Artifacts: []DesktopArtifact{arm, intel}}
	return s, key, rel, buf.Bytes()
}

func uploadFixture(t *testing.T, rel DesktopRelease, archive []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	manifest, err := form.CreateFormFile("manifest", "desktop-release.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(manifest).Encode(rel); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("archive", rel.Artifacts[0].File)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return form.FormDataContentType(), buf.Bytes()
}

func publishFixture(s *Server, key, contentType string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", samplePublishPath, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", contentType)
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	return out
}

func TestDesktopPublisherVerifiesPrivateArchiveAndRetries(t *testing.T) {
	s, key, rel, archive := publisherFixture(t)
	mime, body := uploadFixture(t, rel, archive)
	for _, token := range []string{"", strings.Repeat("a", 64), s.cfg.AdminToken} {
		if res := publishFixture(s, token, mime, body); res.Code != 401 {
			t.Fatalf("untrusted publisher received %d", res.Code)
		}
	}
	s.cfg.DesktopPublisherHashes = nil
	if res := publishFixture(s, key, mime, body); res.Code != 401 {
		t.Fatalf("publisher was active without a verifier: %d", res.Code)
	}
	s.cfg.DesktopPublisherHashes = map[string]string{sampleGame.ID: hash(key)}
	other := httptest.NewRequest("POST", "/v1/desktop/publish/another-game", bytes.NewReader(body))
	other.Header.Set("Authorization", "Bearer "+key)
	denied := httptest.NewRecorder()
	s.ServeHTTP(denied, other)
	if denied.Code != 401 {
		t.Fatalf("publisher authorized for a different app: %d", denied.Code)
	}
	if res := publishFixture(s, key, "application/json", []byte("{}")); res.Code != 400 {
		t.Fatalf("unexpected upload type returned %d", res.Code)
	}
	var wait sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			codes[i] = publishFixture(s, key, mime, body).Code
		}()
	}
	wait.Wait()
	sort.Ints(codes)
	if codes[0] != 200 || codes[1] != 201 {
		t.Fatalf("concurrent upload results: %v", codes)
	}
	artifact := rel.Artifacts[0]
	if err := os.Remove(filepath.Join(s.cfg.DataDir, "desktop", artifact.SHA256)); err != nil {
		t.Fatal(err)
	}
	response := publishFixture(s, key, mime, body)
	var receipt struct {
		AppID, Version, SHA256 string
		VersionCode, Size      int64
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.AppID != "sample-game" ||
		receipt.VersionCode != 4000 || receipt.Version != rel.Version || receipt.SHA256 != artifact.SHA256 || receipt.Size != artifact.Size {
		t.Fatalf("replay receipt: %d %s", response.Code, response.Body.String())
	}
	rel.Changes = []string{"Replaced notes."}
	conflictType, conflictBody := uploadFixture(t, rel, archive)
	if res := publishFixture(s, key, conflictType, conflictBody); res.Code != 409 {
		t.Fatalf("modified release reused the same version: %d", res.Code)
	}
	const path = "/v1/desktop/releases/sample-game/4000/macos/arm64"
	if res := request(s, "GET", path, key, ""); res.Code != 401 {
		t.Fatalf("publisher credential could download as device: %d", res.Code)
	}
	if res := request(s, "GET", path, "", ""); res.Code != 401 {
		t.Fatalf("anonymous download returned %d", res.Code)
	}
	if res := request(s, "GET", "/v1/desktop/apps?platform=macos&arch=arm64", "", ""); res.Code != 200 || strings.Contains(res.Body.String(), "sample-game") {
		t.Fatalf("public browse returned private game: %s", res.Body.String())
	}
	login := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Bazaar Mac"}`)
	token := field(t, login, "token")
	for _, arch := range []string{"arm64", "x64"} {
		if res := request(s, "GET", strings.Replace(path, "arm64", arch, 1), token, ""); res.Code != 200 || !bytes.Equal(res.Body.Bytes(), archive) {
			t.Fatalf("signed-in %s download failed: %d", arch, res.Code)
		}
	}
	rel.Version, rel.VersionCode = "0.4.1", 4001
	for i := range rel.Artifacts {
		rel.Artifacts[i].File = "SampleGame-0.4.1-mac-universal.zip"
	}
	newType, newBody := uploadFixture(t, rel, archive)
	if res := publishFixture(s, key, newType, newBody); res.Code != 201 {
		t.Fatalf("next version failed: %d %s", res.Code, res.Body.String())
	}
	if res := publishFixture(s, key, mime, body); res.Code != 409 {
		t.Fatalf("version rollback returned %d", res.Code)
	}
}

func TestDesktopPublisherRejectsMismatchedArtifacts(t *testing.T) {
	s, key, base, archive := publisherFixture(t)
	for name, change := range map[string]func(*DesktopRelease, *[]byte){
		"truncated":     func(_ *DesktopRelease, b *[]byte) { *b = (*b)[:len(*b)-1] },
		"changed bytes": func(_ *DesktopRelease, b *[]byte) { (*b)[0]++ },
		"invalid ZIP with matching digest": func(r *DesktopRelease, b *[]byte) {
			*b = []byte("not a valid ZIP")
			for i := range r.Artifacts {
				r.Artifacts[i].SHA256, r.Artifacts[i].Size = hash(string(*b)), int64(len(*b))
			}
		},
		"other app":               func(r *DesktopRelease, _ *[]byte) { r.Artifacts[0].Entry = "Other.app" },
		"missing arch":            func(r *DesktopRelease, _ *[]byte) { r.Artifacts = r.Artifacts[:1] },
		"different target digest": func(r *DesktopRelease, _ *[]byte) { r.Artifacts[1].SHA256 = strings.Repeat("a", 64) },
		"wrong code":              func(r *DesktopRelease, _ *[]byte) { r.VersionCode++ },
		"oversized":               func(r *DesktopRelease, _ *[]byte) { r.Artifacts[0].Size = maxPublisherArchive + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			next := base
			next.Artifacts = append([]DesktopArtifact(nil), base.Artifacts...)
			data := bytes.Clone(archive)
			change(&next, &data)
			mime, body := uploadFixture(t, next, data)
			if res := publishFixture(s, key, mime, body); res.Code != 400 {
				t.Fatalf("invalid upload returned %d: %s", res.Code, res.Body.String())
			}
		})
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM desktop_releases").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid release persisted: %d, %v", count, err)
	}
	if files, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "desktop")); err != nil || len(files) != 0 {
		t.Fatalf("staged files left behind: %v, %v", files, err)
	}
	// A direct publisher is the release source even when GitHub is unavailable.
	s.client.Transport = nativeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("attempted GitHub polling for private push-only app")
		return nil, nil
	})
	if err := s.Sync(t.Context(), sampleGame, ""); err != nil {
		t.Fatal(err)
	}
}
