package store

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCentralDeviceRevocationOutageAndNoPositiveCache(t *testing.T) {
	s := nativeFixture(t)
	w := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Test"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	credential := field(t, w, "token")
	base := s.cfg.Federation.HTTP.Transport
	var status, calls atomic.Int32
	status.Store(200)
	s.cfg.Federation.HTTP.Transport = nativeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/session" {
			calls.Add(1)
			if code := status.Load(); code != 200 {
				return &http.Response{StatusCode: int(code), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			}
		}
		return base.RoundTrip(r)
	})
	for range 2 {
		if w = request(s, "GET", "/v1/apps", credential, ""); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("positive auth decision was cached")
	}
	status.Store(503)
	if w = request(s, "GET", "/v1/apps", credential, ""); w.Code != 503 {
		t.Fatal("outage did not fail closed", w.Code)
	}
	status.Store(200)
	if w = request(s, "GET", "/v1/apps", credential, ""); w.Code != 200 {
		t.Fatal("outage discarded device state", w.Code)
	}
	status.Store(401)
	if w = request(s, "GET", "/v1/apps", credential, ""); w.Code != 401 {
		t.Fatal("central logout not enforced", w.Code)
	}
	status.Store(200)
	if w = request(s, "GET", "/v1/apps", credential, ""); w.Code != 401 {
		t.Fatal("revoked credential resurrected", w.Code)
	}
}
func TestLocalDeviceLogoutWorksDuringIAMOutage(t *testing.T) {
	s := nativeFixture(t)
	w := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Test"}`)
	credential := field(t, w, "token")
	s.cfg.Federation.HTTP.Transport = nativeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("local logout must not call IAM")
		return nil, nil
	})
	if w = request(s, "DELETE", "/v1/device", credential, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
}
func TestOldGravitasEnrollmentMustReauthenticateWithoutBreakingInvites(t *testing.T) {
	s := nativeFixture(t)
	invitation := field(t, request(s, "POST", "/admin/invites", s.cfg.AdminToken, `{"label":"old","uses":2,"expiresInHours":1}`), "invite")
	enrolled := request(s, "POST", "/v1/enroll", "", `{"invite":"`+invitation+`","deviceName":"old"}`)
	credential, id := field(t, enrolled, "token"), field(t, enrolled, "deviceId")
	if _, err := s.db.Exec("INSERT INTO device_identities(device_id,issuer,subject,username) VALUES(?,?,?,?)", id, "https://gravitas.cool", "old-subject", "old"); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", "/v1/apps", credential, ""); w.Code != 401 {
		t.Fatal("old proofless Gravitas enrollment accepted")
	}
	linked := request(s, "POST", "/v1/auth/gravitas/exchange", credential, `{"code":"link","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"linked"}`)
	if linked.Code != 201 || field(t, linked, "deviceId") != id {
		t.Fatal("old device could not reauthenticate in place", linked.Code)
	}
	onlyInvite := request(s, "POST", "/v1/enroll", "", `{"invite":"`+invitation+`","deviceName":"independent"}`)
	s.cfg.Federation.HTTP.Transport = nativeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invite-only device must not call IAM")
		return nil, nil
	})
	if w := request(s, "GET", "/v1/apps", field(t, onlyInvite, "token"), ""); w.Code != 200 {
		t.Fatal("invite enrollment changed", w.Code)
	}
}
func TestBrokerBundleOnlyReturnedForExplicitNativeExchange(t *testing.T) {
	s := nativeFixture(t)
	start := request(s, "POST", "/v1/auth/gravitas/start", "", `{"state":"`+strings.Repeat("s", 43)+`","codeChallenge":"`+strings.Repeat("c", 43)+`","broker":true}`)
	if start.Code != 200 || !strings.Contains(field(t, start, "authorizationUrl"), "native.broker") {
		t.Fatal("broker privilege not explicitly requested")
	}
	old := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"old"}`)
	if old.Code != 201 || strings.Contains(old.Body.String(), "broker") || strings.Contains(old.Body.String(), "opaque-iam-access") {
		t.Fatal("legacy exchange leaked IAM credentials")
	}
	modern := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"broker","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"broker","broker":true}`)
	if modern.Code != 201 {
		t.Fatal(modern.Code, modern.Body.String())
	}
	var out struct {
		DeviceID string `json:"deviceId"`
		Broker   struct {
			Refresh string `json:"refreshToken"`
			Family  string `json:"sessionId"`
			Subject string `json:"subject"`
		} `json:"broker"`
	}
	if json.Unmarshal(modern.Body.Bytes(), &out) != nil || out.Broker.Refresh != strings.Repeat("r", 43) || out.Broker.Family != "family-1" || out.Broker.Subject != "stable-user" {
		t.Fatal("invalid private native broker bundle")
	}
	var stored string
	if err := s.db.QueryRow("SELECT proof FROM device_sessions WHERE device_id=?", out.DeviceID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, strings.Repeat("p", 43)) || strings.Contains(stored, strings.Repeat("r", 43)) {
		t.Fatal("plaintext central credential persisted")
	}
}
func TestWebStatusFailurePreservesCookieAndRevocationClearsIt(t *testing.T) {
	s := nativeFixture(t)
	c := s.cfg.Federation
	identity := webTestIdentity(c, "web-subject", "reader")
	saved := httptest.NewRecorder()
	if err := c.SetSession(saved, identity); err != nil {
		t.Fatal(err)
	}
	cookie := saved.Result().Cookies()[0]
	base := c.HTTP.Transport
	var status atomic.Int32
	status.Store(503)
	c.HTTP.Transport = nativeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/session" {
			return &http.Response{StatusCode: int(status.Load()), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return base.RoundTrip(r)
	})
	check := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", c.AppURL+path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/download/appstore.apk", "/", "/apps/daybreak", "/auth/gravitas/session"} {
		w := check(path)
		if w.Code != 503 || len(w.Result().Cookies()) != 0 {
			t.Fatal("IAM outage discarded browser session", path, w.Code)
		}
	}
	status.Store(401)
	w := check("/download/appstore.apk")
	if w.Code != 401 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("central logout did not clear browser session", w.Code)
	}
	// Clearing the app cookie remains local and does not require a live IAM check.
	r := httptest.NewRequest("POST", c.AppURL+"/auth/gravitas/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-Gravitas-CSRF", "1")
	r.Header.Set("Origin", c.AppURL)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("local logout unavailable")
	}
}
