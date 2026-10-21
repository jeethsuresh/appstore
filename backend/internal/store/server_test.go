package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{DataDir: t.TempDir(), AdminToken: strings.Repeat("a", 64), WebhookSecret: strings.Repeat("b", 64), Apps: []App{{ID: "daybreak", Name: "Daybreak", Package: "dev.jeeth.local_weather", Repo: "jeethsuresh/weather", Asset: "daybreak-android-arm64.apk", Certificate: strings.Repeat("c", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func request(s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func field(t *testing.T, w *httptest.ResponseRecorder, name string) string {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	str, _ := data[name].(string)
	if str == "" {
		t.Fatalf("missing %s in %s", name, w.Body.String())
	}
	return str
}
func enrollDevice(t *testing.T, s *Server) (string, string) {
	t.Helper()
	w := request(s, "POST", "/admin/invites", s.cfg.AdminToken, `{"label":"phone","uses":1,"expiresInHours":1}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	invite := field(t, w, "invite")
	w = request(s, "POST", "/v1/enroll", "", fmt.Sprintf(`{"invite":%q,"deviceName":"Pixel"}`, invite))
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	return field(t, w, "token"), field(t, w, "deviceId")
}
func TestDeviceLifecycle(t *testing.T) {
	s := testServer(t)
	if w := request(s, "GET", "/v1/apps", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/admin/invites", "bad", `{}`); w.Code != 401 {
		t.Fatal(w.Code)
	}
	tok, id := enrollDevice(t, s)
	if w := request(s, "GET", "/v1/apps", tok, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Daybreak") {
		t.Fatal(w.Code, w.Body.String())
	}
	var stored string
	s.db.QueryRow("SELECT token_hash FROM devices WHERE id=?", id).Scan(&stored)
	if stored == tok || stored != hash(tok) {
		t.Fatal("token stored in cleartext")
	}
	request(s, "DELETE", "/admin/devices/"+id, s.cfg.AdminToken, "")
	if w := request(s, "GET", "/v1/apps", tok, ""); w.Code != 401 {
		t.Fatal("revoked token accepted")
	}
}
func TestInviteIsAtomicAndExpires(t *testing.T) {
	s := testServer(t)
	invite := token()
	s.db.Exec("INSERT INTO invites VALUES(?,?,?,?)", hash(invite), "one", time.Now().Add(time.Hour).Unix(), 1)
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- request(s, "POST", "/v1/enroll", "", fmt.Sprintf(`{"invite":%q,"deviceName":"Phone"}`, invite)).Code
		}()
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == 201 {
			success++
		} else if code != 401 {
			t.Fatalf("unexpected %d", code)
		}
	}
	if success != 1 {
		t.Fatalf("%d enrollments for one invite", success)
	}
	expired := token()
	s.db.Exec("INSERT INTO invites VALUES(?,?,?,?)", hash(expired), "expired", time.Now().Add(-time.Minute).Unix(), 1)
	if w := request(s, "POST", "/v1/enroll", "", fmt.Sprintf(`{"invite":%q,"deviceName":"Phone"}`, expired)); w.Code != 401 {
		t.Fatal("expired invite accepted")
	}
}
func TestMalformedEnrollment(t *testing.T) {
	s := testServer(t)
	for _, body := range []string{`{}`, `{"invite":"a","deviceName":"Phone"}`, `{} {}`, strings.Repeat("x", 20000)} {
		if w := request(s, "POST", "/v1/enroll", "", body); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
func TestWebhook(t *testing.T) {
	s := testServer(t)
	body := []byte(`{"action":"published","repository":{"full_name":"jeethsuresh/weather"}}`)
	send := func(body []byte, sig string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(body))
		r.Header.Set("X-GitHub-Event", "release")
		r.Header.Set("X-Hub-Signature-256", sig)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.WebhookSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if send(body, "bad").Code != 401 || send(append(body, ' '), sig).Code != 401 {
		t.Fatal("unsigned webhook accepted")
	}
	if w := send(body, sig); w.Code != 202 {
		t.Fatal(w.Code)
	}
	if <-s.queue != "daybreak" {
		t.Fatal("wrong queue")
	}
}
func TestIngestionAndPrivateDownload(t *testing.T) {
	s := testServer(t)
	a := s.cfg.Apps[0]
	data := []byte("APK content fixture")
	digest := hash(string(data))
	version := int64(7)
	pkg, cert := a.Package, a.Certificate
	ghDigest := "sha256:" + digest
	assetName := a.Asset
	assetID := int64(1)
	var downloads atomic.Int64
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/jeethsuresh/weather/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.4.2", "assets": []map[string]any{{"id": assetID, "name": assetName, "size": len(data), "digest": ghDigest}}})
		case fmt.Sprintf("/repos/jeethsuresh/weather/releases/assets/%d", assetID):
			downloads.Add(1)
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Error("asset accept header")
			}
			w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	s.api = gh.URL
	s.inspect = func(context.Context, string) (APKInfo, error) {
		return APKInfo{Package: pkg, Certificate: cert, VersionCode: version, VersionName: "1.4.2", MinSDK: 24}, nil
	}
	if err := s.Sync(context.Background(), a, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background(), a, ""); err != nil {
		t.Fatal("idempotent sync:", err)
	}
	if downloads.Load() != 1 {
		t.Fatal("unchanged release was downloaded again")
	}
	latest, _ := s.latest(context.Background(), a.ID)
	if err := os.Remove(filepath.Join(s.cfg.DataDir, "apks", latest.SHA256+".apk")); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background(), a, ""); err != nil {
		t.Fatal("cache repair:", err)
	}
	if downloads.Load() != 2 {
		t.Fatal("missing APK cache was not repaired")
	}
	path := fmt.Sprintf("/v1/releases/%d/apk", latest.ID)
	if w := request(s, "GET", path, "", ""); w.Code != 401 {
		t.Fatal("public APK")
	}
	tok, _ := enrollDevice(t, s)
	if w := request(s, "GET", path, tok, ""); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Range", "bytes=0-2")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != string(data[:3]) {
		t.Fatal("range support", w.Code)
	}
	for _, bad := range []string{"/v1/releases/../../store.db/apk", "/v1/releases/nope/apk"} {
		if w := request(s, "GET", bad, tok, ""); w.Code == 200 {
			t.Fatal("path traversal")
		}
	}
	assetID++
	version = 6
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("downgrade accepted")
	}
	assetID++
	version = 8
	pkg = "wrong.package"
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("package mismatch accepted")
	}
	pkg = a.Package
	assetID++
	cert = strings.Repeat("e", 64)
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("certificate mismatch accepted")
	}
	cert = a.Certificate
	ghDigest = "sha256:" + strings.Repeat("0", 64)
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	ghDigest = "sha256:" + digest
	assetName = "wrong.apk"
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("wrong asset accepted")
	}
	a.Certificate = ""
	if err := s.Sync(context.Background(), a, ""); err == nil {
		t.Fatal("missing pin accepted")
	}
}
func TestConfigValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	os.WriteFile(path, []byte(`[{"id":"a","name":"A","packageName":"a.b","repository":"../evil","assetName":"a.apk"}]`), 0600)
	if _, err := LoadApps(path); err == nil {
		t.Fatal("invalid config accepted")
	}
}
func TestActualAPKVerification(t *testing.T) {
	apk := os.Getenv("TEST_APK")
	if apk == "" {
		t.Skip("set TEST_APK, AAPT, and APKSIGNER for real APK verification")
	}
	s := testServer(t)
	s.cfg.AAPT = os.Getenv("AAPT")
	s.cfg.APKSigner = os.Getenv("APKSIGNER")
	info, err := s.inspectAPK(context.Background(), apk)
	if err != nil {
		t.Fatal(err)
	}
	if info.Package != s.cfg.Apps[0].Package || info.Certificate != "18cb4dbf5d8b3ed41336acd2c1f6dc12bd7aba2d8f5a6dcaf9339e42b6af683a" || info.VersionCode < 7 {
		t.Fatalf("unexpected metadata: %+v", info)
	}
	b, err := os.ReadFile(apk)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xff
	bad := filepath.Join(t.TempDir(), "tampered.apk")
	os.WriteFile(bad, b, 0600)
	if _, err = s.inspectAPK(context.Background(), bad); err == nil {
		t.Fatal("tampered APK accepted")
	}
}

func TestLiveGitHubIngestion(t *testing.T) {
	if os.Getenv("LIVE_GITHUB") != "1" {
		t.Skip("set LIVE_GITHUB=1 to download and verify the two real public releases")
	}
	s := testServer(t)
	apps, err := LoadApps("../../../config/apps.json")
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Apps = apps
	s.cfg.AAPT = os.Getenv("AAPT")
	s.cfg.APKSigner = os.Getenv("APKSIGNER")
	for _, app := range apps {
		if err = s.Sync(context.Background(), app, ""); err != nil {
			t.Fatalf("%s: %v", app.ID, err)
		}
		v, err := s.latest(context.Background(), app.ID)
		if err != nil || v == nil {
			t.Fatalf("missing release: %v", err)
		}
		t.Logf("%s: %s, version code %d, %d bytes, sha256 %s", app.ID, v.VersionName, v.VersionCode, v.Size, v.SHA256)
	}
	tok, _ := enrollDevice(t, s)
	if w := request(s, "GET", "/v1/apps", tok, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestConfigChangeInvalidatesVerifiedCatalog(t *testing.T) {
	s := testServer(t)
	a := s.cfg.Apps[0]
	digest := hash("fixture")
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("INSERT INTO releases(app_id,tag,version_code,version_name,sha256,size,min_sdk,notes,created,asset_id,config_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?)", a.ID, "v1", 1, "1.0", digest, 7, 24, "", now, 1, appTrust(a))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.cfg.DataDir, "apks", digest+".apk"), []byte("fixture"), 0600)
	tok, _ := enrollDevice(t, s)
	if w := request(s, "GET", "/v1/apps", tok, ""); !strings.Contains(w.Body.String(), `"release":`) {
		t.Fatal("verified release missing")
	}
	s.cfg.Apps[0].Certificate = strings.Repeat("e", 64)
	if w := request(s, "GET", "/v1/apps", tok, ""); strings.Contains(w.Body.String(), `"release":`) {
		t.Fatal("catalog exposed a release verified for a different pin")
	}
	if w := request(s, "GET", "/v1/releases/1/apk", tok, ""); w.Code != 404 {
		t.Fatal("old trust configuration served an APK")
	}
}
func TestSyncCanBeCancelledWhileQueued(t *testing.T) {
	s := testServer(t)
	// Hold the gate as another ingestion would, then cancel a queued request.
	s.syncGate <- struct{}{}
	defer func() { <-s.syncGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Sync(ctx, s.cfg.Apps[0], ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued sync did not cancel: %v", err)
	}
}
func TestTransientGitHubFailureIsRetried(t *testing.T) {
	s := testServer(t)
	a := s.cfg.Apps[0]
	var requests atomic.Int64
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		http.NotFound(w, r)
	}))
	defer gh.Close()
	s.api = gh.URL
	err := s.Sync(context.Background(), a, "")
	if requests.Load() != 2 || err == nil {
		t.Fatalf("transient failure retry count %d, error %v", requests.Load(), err)
	}
}

func TestSchemaMigrationPreservesExistingData(t *testing.T) {
	s := testServer(t)
	cfg := s.cfg
	tok, id := enrollDevice(t, s)
	if _, err := s.db.Exec("ALTER TABLE releases DROP COLUMN asset_id"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE releases DROP COLUMN config_hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if w := request(reopened, "GET", "/v1/apps", tok, ""); w.Code != 200 {
		t.Fatal("migration lost enrolled device", w.Code)
	}
	var count int
	if err = reopened.db.QueryRow("SELECT COUNT(*) FROM devices WHERE id=?", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration did not preserve devices", err)
	}
	var columns int
	if err = reopened.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('releases') WHERE name IN ('asset_id','config_hash')").Scan(&columns); err != nil || columns != 2 {
		t.Fatal("migration did not add cache columns", err)
	}
}
