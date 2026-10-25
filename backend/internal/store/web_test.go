package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"private-store/internal/gravitas"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestWebDownloadRequiresGravitasSession(t *testing.T) {
	s := testServer(t)
	c, err := gravitas.New("https://gravitas.cool", "https://apps.gravitas.cool", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Federation = c
	s.cfg.ClientAPK = filepath.Join(t.TempDir(), "store.apk")
	if err := os.WriteFile(s.cfg.ClientAPK, []byte("signed-apk-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, token := range []string{"", s.cfg.AdminToken} {
			if w := request(s, method, "/download/appstore.apk", token, ""); w.Code != 401 || strings.Contains(w.Body.String(), "signed-apk-fixture") {
				t.Fatal("anonymous download", w.Code)
			}
		}
	}
	w := request(s, "GET", "/", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `aria-label="Sign in"`) || !strings.Contains(w.Body.String(), `name="username"`) || strings.Contains(w.Body.String(), `href="/download/appstore.apk"`) || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("unsafe login shell")
	}
	session := httptest.NewRecorder()
	if err := c.SetSession(session, webTestIdentity(c, "test-subject", "<script>alert(1)</script>")); err != nil {
		t.Fatal(err)
	}
	cookie := session.Result().Cookies()[0]
	do := func(method, path, origin, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://apps.gravitas.cool"+path, nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Gravitas-CSRF", csrf)
		if path == "/download/appstore.apk" {
			r.Header.Set("Range", "bytes=0-5")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w = do("GET", "/", "", "")
	if !strings.Contains(w.Body.String(), `href="/download/appstore.apk"`) || strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("authenticated home or escaping")
	}
	w = do("GET", "/download/appstore.apk", "", "")
	if w.Code != 206 || w.Body.String() != "signed" || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("download", w.Code, w.Body.String())
	}
	if w = do("POST", "/auth/gravitas/logout", "https://evil.example", "1"); w.Code != 403 {
		t.Fatal("cross-origin logout")
	}
	if w = do("POST", "/auth/gravitas/logout", "", ""); w.Code != 403 {
		t.Fatal("logout without CSRF header")
	}
	if w = do("POST", "/auth/gravitas/logout", c.AppURL, "1"); w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed")
	}
	cookie.Value = "tampered"
	if w = do("GET", "/download/appstore.apk", "", ""); w.Code != 401 {
		t.Fatal("tampered session downloaded")
	}
	if w = request(s, "GET", "/auth/gravitas/callback?code=bad&state=bad", "", ""); w.Code != 303 || w.Header().Get("Location") != "/login?error=signin" {
		t.Fatal("invalid callback accepted")
	}
	if w = request(s, "GET", "/unknown", "", ""); w.Code != 404 {
		t.Fatal("unknown route")
	}
}

func TestBootstrapPinAndUnconfiguredWebFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.apk")
	data := []byte("verified bytes")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	if err := verifyClientAPK(path, hex.EncodeToString(h[:]), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if err := verifyClientAPK(path, clientSHA256, clientSize); err == nil {
		t.Fatal("wrong bootstrap accepted")
	}
	s := testServer(t)
	for _, path := range []string{"/download/appstore.apk", "/auth/gravitas/start", "/auth/gravitas/register", "/auth/gravitas/callback"} {
		if w := request(s, http.MethodGet, path, "", ""); w.Code != 503 {
			t.Fatal("unconfigured login failed open", path, w.Code)
		}
	}
}

func TestAppPagesBrowsePubliclyButKeepDownloadsGated(t *testing.T) {
	s := testServer(t)
	s.cfg.Apps = append(s.cfg.Apps, App{ID: "spacetime", Name: "Spacetime", Package: "com.jeethsuresh.spacetime", Repo: "jeethsuresh/spacetime", Certificate: strings.Repeat("d", 64)})
	s.cfg.Apps = append(s.cfg.Apps, App{ID: "river", Name: "River", Package: "cool.gravitas.river", Repo: "jeethsuresh/river", Certificate: "6559956b5cf6b2f796824995b31da7fa2c176ba652e8fcb40741686d850bb6b0"})
	s.cfg.Apps = append(s.cfg.Apps, App{ID: "atlas", Name: "Atlas", Package: "cool.gravitas.atlas", Repo: "jeethsuresh/atlas", Certificate: "ee7ce4ba953a4f64cb1b857c6cf0f124a68dc6cd90840b1a46259c9b759e7f4a"})
	c, err := gravitas.New("https://gravitas.cool", "https://apps.gravitas.cool", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Federation = c
	_, err = s.db.Exec("INSERT INTO releases(app_id,tag,version_code,version_name,sha256,size,min_sdk,notes,created,asset_id,config_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?)", "daybreak", "v1.4.2", 7, "1.4.2", strings.Repeat("e", 64), 18385963, 24, "<script>alert(1)</script>", "2026-10-21T00:00:00Z", 1, appTrust(s.cfg.Apps[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ id, name, feature string }{{"daybreak", "Daybreak", "Choose your weather source"}, {"spacetime", "Spacetime", "Let plans repeat"}, {"river", "River", "Give each topic a home"}, {"atlas", "Atlas", "Make the map yours"}} {
		w := request(s, "GET", "/apps/"+check.id, "", "")
		page := w.Body.String()
		for _, content := range []string{check.name, check.feature, "What you can do.", "At home on your device.", `name="username"`, "Android 8.0+", "/apps/"} {
			if !strings.Contains(page, content) {
				t.Fatalf("%s missing %q: %s", check.id, content, page)
			}
		}
		if w.Code != 200 || strings.Contains(page, `href="/download/appstore.apk"`) || strings.Contains(page, "<script>alert(1)</script>") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unsafe public app page", w.Code, page)
		}
		returnsToApp := false
		for _, form := range regexp.MustCompile(`(?s)<form\b([^>]*)>(.*?)</form>`).FindAllStringSubmatch(page, -1) {
			action := regexp.MustCompile(`action="([^"]+)"`).FindStringSubmatch(form[1])
			next := regexp.MustCompile(`name="next" value="([^"]+)"`).FindStringSubmatch(form[2])
			if len(action) < 2 || len(next) < 2 {
				continue
			}
			target, err := url.Parse(html.UnescapeString(action[1]))
			if err == nil && target.Path == "/auth/gravitas/start" && html.UnescapeString(next[1]) == "/apps/"+check.id {
				returnsToApp = true
			}
		}
		if !returnsToApp {
			t.Fatal("sign-in must return to this app")
		}
	}
	w := request(s, "GET", "/apps/daybreak", "", "")
	for _, content := range []string{"v1.4.2", "18.4 MB", "Android 7.0+"} {
		if !strings.Contains(html.UnescapeString(w.Body.String()), content) {
			t.Fatal("release metadata missing", content)
		}
	}
	if !strings.Contains(w.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("release notes must be escaped")
	}
	// Atlas is a tiny WebView APK. Its verified byte count must not round to 0.0 MB.
	atlas := s.cfg.Apps[len(s.cfg.Apps)-1]
	_, err = s.db.Exec("INSERT INTO releases(app_id,tag,version_code,version_name,sha256,size,min_sdk,notes,created,asset_id,config_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?)", "atlas", "v1.0.1", 2, "1.0.1", strings.Repeat("a", 64), 11560, 26, "Atlas updates", "2026-10-25T00:00:00Z", 2, appTrust(atlas))
	if err != nil {
		t.Fatal(err)
	}
	w = request(s, "GET", "/apps/atlas", "", "")
	page := html.UnescapeString(w.Body.String())
	if w.Code != 200 || !strings.Contains(page, "v1.0.1") || !strings.Contains(page, "11.6 kB") || strings.Contains(page, "0.0 MB") {
		t.Fatal("tiny verified Atlas download size missing", w.Code, page)
	}
	// Old metadata verified under a different package/certificate must stay hidden.
	s.cfg.Apps[0].Certificate = strings.Repeat("f", 64)
	if w = request(s, "GET", "/apps/daybreak", "", ""); strings.Contains(w.Body.String(), "v1.4.2") {
		t.Fatal("release with stale trust exposed")
	}
	session := httptest.NewRecorder()
	if err := c.SetSession(session, webTestIdentity(c, "verified-subject", "reader")); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://apps.gravitas.cool/apps/spacetime", nil)
	r.AddCookie(session.Result().Cookies()[0])
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `href="/download/appstore.apk"`) || !strings.Contains(w.Body.String(), "Copy address") {
		t.Fatal("authenticated app setup unavailable", w.Code, w.Body.String())
	}
	for _, path := range []string{"/apps/not-configured", "/apps/", "/assets/app.html"} {
		if w = request(s, "GET", path, "", ""); w.Code != 404 {
			t.Fatal("unknown app/asset exposed", path, w.Code)
		}
	}
	s.cfg.Apps = s.cfg.Apps[:1]
	if w = request(s, "GET", "/apps/spacetime", "", ""); w.Code != 404 {
		t.Fatal("removed app remains published")
	}
	for _, path := range []string{"/assets/daybreak.svg", "/assets/spacetime.svg", "/assets/river.svg", "/assets/atlas.svg"} {
		if w = request(s, "GET", path, "", ""); w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" {
			t.Fatal("missing app icon", path)
		}
	}
	w = request(s, "GET", "/", "", "")
	if !strings.Contains(w.Body.String(), `id="apps"`) || !strings.Contains(w.Body.String(), `href="/apps/daybreak"`) {
		t.Fatal("app pages are not discoverable")
	}
}

func webTestIdentity(c *gravitas.Client, subject, username string) gravitas.Identity {
	identity := gravitas.Identity{Issuer: c.Issuer, Subject: subject, Username: username, SessionID: "web-family", CheckToken: strings.Repeat("p", 43), SessionExpires: time.Now().Add(time.Hour).Unix()}
	c.HTTP.Transport = nativeTransport(func(r *http.Request) (*http.Response, error) {
		var data any
		switch r.URL.Path {
		case "/.well-known/iam-configuration":
			data = map[string]any{"issuer": c.Issuer, "identity_domain": "gravitas.cool", "browser_initiation_endpoint": c.Issuer + "/oauth/start", "token_endpoint": c.Issuer + "/oauth/token", "userinfo_endpoint": c.Issuer + "/app/v1/me", "session_check_endpoint": c.Issuer + "/oauth/session"}
		case "/oauth/session":
			data = map[string]any{"active": true, "iss": c.Issuer, "sub": subject, "app_origin": c.AppURL, "session_id": identity.SessionID, "exp": identity.SessionExpires}
		default:
			return nil, fmt.Errorf("unexpected fixture endpoint")
		}
		raw, _ := json.Marshal(data)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return identity
}
