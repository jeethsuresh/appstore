package store

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeArchive(t *testing.T, platform string, extra map[string]string) []byte {
	t.Helper()
	var executable []byte
	entry := sampleGame.Name
	if platform == "linux" {
		executable = make([]byte, 64)
		copy(executable, "\x7fELF\x02\x01")
		binary.LittleEndian.PutUint16(executable[18:20], 62)
	} else {
		entry += ".exe"
		executable = make([]byte, 128)
		copy(executable, "MZ")
		binary.LittleEndian.PutUint32(executable[60:64], 64)
		copy(executable[64:], "PE\x00\x00")
		binary.LittleEndian.PutUint16(executable[68:70], 0x8664)
	}
	files := map[string]string{entry: string(executable), sampleGame.Name + ".pck": "GDPCnative pack"}
	for k, v := range extra {
		files[k] = v
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, value := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func crossPlatformFixture(t *testing.T) (*Server, string, DesktopRelease, map[string][]byte) {
	t.Helper()
	s, key, release, mac := publisherFixture(t)
	archives := map[string][]byte{release.Artifacts[0].File: mac}
	for _, platform := range []string{"linux", "windows"} {
		file, entry := "SampleGame-0.4.0-linux-x64.zip", sampleGame.Name
		if platform == "windows" {
			file, entry = "SampleGame-0.4.0-win-x64.zip", sampleGame.Name+".exe"
		}
		body := nativeArchive(t, platform, nil)
		release.Artifacts = append(release.Artifacts, DesktopArtifact{Platform: platform, Arch: "x64", File: file, Entry: entry, SHA256: hash(string(body)), Size: int64(len(body))})
		archives[file] = body
	}
	return s, key, release, archives
}

func uploadCrossPlatform(t *testing.T, release DesktopRelease, archives map[string][]byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	manifest, _ := form.CreateFormFile("manifest", "desktop-release.json")
	if err := json.NewEncoder(manifest).Encode(release); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, artifact := range release.Artifacts {
		if seen[artifact.File] {
			continue
		}
		seen[artifact.File] = true
		body, exists := archives[artifact.File]
		if !exists {
			continue
		}
		part, err := form.CreateFormFile("archive", artifact.File)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return form.FormDataContentType(), buf.Bytes()
}

func TestDesktopPublisherPublishesAllPlatformsAndRepairsIdenticalReplay(t *testing.T) {
	s, key, release, archives := crossPlatformFixture(t)
	mime, body := uploadCrossPlatform(t, release, archives)
	if res := publishFixture(s, key, mime, body); res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	login := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Cross-platform Bazaar"}`)
	token := field(t, login, "token")
	for _, artifact := range release.Artifacts {
		res := request(s, "GET", "/v1/desktop/releases/sample-game/4000/"+artifact.Platform+"/"+artifact.Arch, token, "")
		if res.Code != 200 || !bytes.Equal(res.Body.Bytes(), archives[artifact.File]) {
			t.Fatal("wrong platform download", artifact.Platform, res.Code)
		}
		anonymous := request(s, "GET", "/v1/desktop/apps?platform="+artifact.Platform+"&arch="+artifact.Arch, "", "")
		if strings.Contains(anonymous.Body.String(), sampleGame.ID) {
			t.Fatal("private game leaked")
		}
	}
	if err := os.Remove(filepath.Join(s.cfg.DataDir, "desktop", release.Artifacts[3].SHA256)); err != nil {
		t.Fatal(err)
	}
	res := publishFixture(s, key, mime, body)
	var receipt struct{ Artifacts []DesktopArtifact }
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &receipt) != nil || len(receipt.Artifacts) != 4 {
		t.Fatal("replay failed", res.Code, res.Body.String())
	}
}

func TestDesktopPublisherLeavesPreviousReleaseOnMissingOrCorruptPlatform(t *testing.T) {
	s, key, release, archives := crossPlatformFixture(t)
	legacy := release
	legacy.Artifacts = legacy.Artifacts[:2]
	mime, body := uploadCrossPlatform(t, legacy, archives)
	if res := publishFixture(s, key, mime, body); res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	release.Version, release.VersionCode = "0.4.1", 4001
	for i := range release.Artifacts {
		release.Artifacts[i].File = strings.ReplaceAll(release.Artifacts[i].File, "0.4.0", "0.4.1")
	}
	updated := map[string][]byte{}
	for name, body := range archives {
		updated[strings.ReplaceAll(name, "0.4.0", "0.4.1")] = body
	}
	last := release.Artifacts[3].File
	for _, missing := range []bool{true, false} {
		if missing {
			delete(updated, last)
		} else {
			updated[last] = []byte("corrupt final package")
		}
		mime, body = uploadCrossPlatform(t, release, updated)
		if res := publishFixture(s, key, mime, body); res.Code != 400 {
			t.Fatal(res.Code, res.Body.String())
		}
		current, err := s.latestDesktop(t.Context(), sampleGame)
		if err != nil || current.VersionCode != 4000 {
			t.Fatal("failed transaction replaced old release", err)
		}
		files, _ := os.ReadDir(filepath.Join(s.cfg.DataDir, "desktop"))
		if len(files) != 1 {
			t.Fatal("failed transaction left staged files", len(files))
		}
	}
}

func TestNativeGamePublisherRejectsDisguisedOrEscapingPackages(t *testing.T) {
	for _, platform := range []string{"linux", "windows"} {
		a := DesktopArtifact{Platform: platform, Entry: sampleGame.Name}
		if platform == "windows" {
			a.Entry += ".exe"
		}
		for _, extra := range []map[string]string{{"../outside": "bad"}, {a.Entry: "not the named platform"}, {sampleGame.Name + ".pck": "not a pack"}} {
			file := filepath.Join(t.TempDir(), "game.zip")
			if err := os.WriteFile(file, nativeArchive(t, platform, extra), 0600); err != nil {
				t.Fatal(err)
			}
			if validNativeGameArchive(file, sampleGame, a) {
				t.Fatal("accepted unsafe native package", platform, extra)
			}
		}
	}
}

func TestConfiguredPublicGamePublisherIsIndependentOfGitHubPolling(t *testing.T) {
	s, key, release, archives := crossPlatformFixture(t)
	public := sampleGame
	public.DesktopRequiresSignIn = false
	s.cfg.Apps[len(s.cfg.Apps)-1] = public
	mime, body := uploadCrossPlatform(t, release, archives)
	if res := publishFixture(s, key, mime, body); res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	res := request(s, "GET", "/v1/desktop/apps?platform=linux&arch=x64", "", "")
	if !strings.Contains(res.Body.String(), sampleGame.ID) {
		t.Fatal("public visibility changed", res.Body.String())
	}
	s.client.Transport = nativeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("polled over an explicit publisher")
		return nil, nil
	})
	if err := s.Sync(t.Context(), public, ""); err != nil {
		t.Fatal(err)
	}
}
