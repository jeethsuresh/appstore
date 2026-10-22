package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"private-store/internal/gravitas"
	"strings"
	"testing"
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
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Sign in with Gravitas") || strings.Contains(w.Body.String(), `href="/download/appstore.apk"`) || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("unsafe login shell")
	}
	session := httptest.NewRecorder()
	if err := c.SetSession(session, gravitas.Identity{Issuer: c.Issuer, Subject: "test-subject", Username: "<script>alert(1)</script>"}); err != nil {
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
