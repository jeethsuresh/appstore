package store

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"private-store/internal/gravitas"
	"strings"
	"sync"
	"testing"
	"time"
)

type nativeTransport func(*http.Request) (*http.Response, error)

func (f nativeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func nativeFixture(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	c, err := gravitas.New("https://gravitas.cool", "https://apps.gravitas.cool", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	expiry := time.Now().Add(24 * time.Hour).Unix()
	var mu sync.Mutex
	c.HTTP.Transport = nativeTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		status, body := 200, ""
		if r.URL.Host != "gravitas.cool" {
			t.Fatal("untrusted issuer request")
		}
		switch r.URL.Path {
		case "/.well-known/iam-configuration":
			body = `{"issuer":"https://gravitas.cool","identity_domain":"gravitas.cool","browser_initiation_endpoint":"https://gravitas.cool/oauth/start","token_endpoint":"https://gravitas.cool/oauth/token","userinfo_endpoint":"https://gravitas.cool/app/v1/me","session_check_endpoint":"https://gravitas.cool/oauth/session"}`
		case "/oauth/token":
			_ = r.ParseForm()
			code := r.Form.Get("code")
			if (code != "first" && code != "link" && code != "broker") || used[code] || r.Form.Get("code_verifier") != strings.Repeat("v", 43) || r.Form.Get("redirect_uri") != "https://apps.gravitas.cool/auth/android/callback" {
				status, body = 400, `{"error":"invalid_grant"}`
			} else {
				used[code] = true
				data := map[string]any{"access_token": "opaque-iam-access", "token_type": "Bearer", "session_id": "family-1", "session_check_token": strings.Repeat("p", 43), "session_check_expires_in": 86400, "scope": "profile.name"}
				if code == "broker" {
					data["access_token"] = strings.Repeat("a", 43)
					data["refresh_token"] = strings.Repeat("r", 43)
					data["scope"] = "profile.name native.broker"
					data["expires_in"] = 3600
				}
				encoded, _ := json.Marshal(data)
				body = string(encoded)
			}
		case "/app/v1/me":
			if r.Header.Get("Authorization") != "Bearer opaque-iam-access" && r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 43) {
				t.Fatal("missing IAM proof")
			}
			body = `{"sub":"stable-user","username":"native-user","domain":"gravitas.cool","user_id":"@native-user:gravitas.cool"}`
		case "/oauth/session":
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 43) {
				status = 401
			} else {
				encoded, _ := json.Marshal(map[string]any{"active": true, "iss": "https://gravitas.cool", "sub": "stable-user", "app_origin": "https://apps.gravitas.cool", "session_id": "family-1", "exp": expiry})
				body = string(encoded)
			}
		default:
			t.Fatal("unexpected IAM endpoint", r.URL.Path)
		}
		if subject := os.Getenv("NOTIFICATION_INTEROP_SUBJECT"); subject != "" {
			body = strings.ReplaceAll(body, "stable-user", subject)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	s.cfg.Federation = c
	return s
}
func TestNativeSignInBindsDeviceAndRotatesLegacyCredential(t *testing.T) {
	s := nativeFixture(t)
	w := request(s, "POST", "/v1/auth/gravitas/start", "", `{"state":"`+strings.Repeat("s", 43)+`","codeChallenge":"`+strings.Repeat("c", 43)+`"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	u, _ := url.Parse(field(t, w, "authorizationUrl"))
	q := u.Query()
	if u.Host != "gravitas.cool" || q.Get("scope") != "profile.name" || q.Get("codeChallengeMethod") != "S256" || q.Get("redirectURL") != "https://apps.gravitas.cool/auth/android/callback" || len(w.Result().Cookies()) != 0 {
		t.Fatal("invalid native authorization flow")
	}
	legacy, id := enrollDevice(t, s)
	if w = request(s, "POST", "/v1/auth/gravitas/exchange", legacy, `{"code":"first","codeVerifier":"bad","deviceName":"Phone"}`); w.Code != 401 {
		t.Fatal("bad proof accepted")
	}
	w = request(s, "POST", "/v1/auth/gravitas/exchange", legacy, `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Phone"}`)
	if w.Code != 201 || field(t, w, "deviceId") != id || field(t, w, "account") != "@native-user:gravitas.cool" {
		t.Fatal(w.Code, w.Body.String())
	}
	credential := field(t, w, "token")
	if request(s, "GET", "/v1/apps", credential, "").Code != 200 || request(s, "GET", "/v1/apps", legacy, "").Code != 401 {
		t.Fatal("credential not rotated")
	}
	var issuer, subject, username, digest string
	var count int
	if err := s.db.QueryRow("SELECT issuer,subject,username FROM device_identities WHERE device_id=?", id).Scan(&issuer, &subject, &username); err != nil {
		t.Fatal(err)
	}
	if issuer != "https://gravitas.cool" || subject != "stable-user" || username != "native-user" {
		t.Fatal("identity binding")
	}
	s.db.QueryRow("SELECT token_hash FROM devices WHERE id=?", id).Scan(&digest)
	if digest != hash(credential) || digest == credential {
		t.Fatal("device credential stored in clear")
	}
	s.db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&count)
	if count != 1 {
		t.Fatal("legacy enrollment duplicated")
	}
	if w = request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Replay"}`); w.Code != 401 {
		t.Fatal("replay accepted")
	}
	w = request(s, "GET", "/admin/devices", s.cfg.AdminToken, "")
	if !strings.Contains(w.Body.String(), "native-user") {
		t.Fatal("admin identity missing")
	}
	request(s, "DELETE", "/v1/device", credential, "")
	if request(s, "GET", "/v1/apps", credential, "").Code != 401 {
		t.Fatal("revoked Gravitas device accepted")
	}
	// A fresh authorized phone needs no separate manual invitation.
	w = request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"link","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"New phone"}`)
	if w.Code != 201 || field(t, w, "deviceId") == id {
		t.Fatal("fresh account enrollment failed", w.Code)
	}
}
func TestNativeFlowFailsClosedAndAppLinksUsePermanentCertificate(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/v1/auth/gravitas/start", "/v1/auth/gravitas/exchange"} {
		if request(s, "POST", path, "", `{}`).Code != 503 {
			t.Fatal("unconfigured native login allowed")
		}
	}
	s = nativeFixture(t)
	for _, body := range []string{`{}`, `{"state":"bad","codeChallenge":"bad"}`, `{"state":"` + strings.Repeat("s", 43) + `","codeChallenge":"` + strings.Repeat("c", 43) + `","redirectUri":"https://evil.example"}`} {
		if request(s, "POST", "/v1/auth/gravitas/start", "", body).Code != 400 {
			t.Fatal("bad start accepted")
		}
	}
	if request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":""}`).Code != 400 {
		t.Fatal("nameless enrollment")
	}
	w := request(s, "GET", "/.well-known/assetlinks.json", "", "")
	var links []struct {
		Target struct {
			Package      string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if json.Unmarshal(w.Body.Bytes(), &links) != nil || links[0].Target.Package != "dev.jeeth.privatestore" || len(links[0].Target.Fingerprints[0]) != 95 {
		t.Fatal("invalid public app association")
	}
	w = request(s, "GET", "/auth/android/callback?state="+strings.Repeat("s", 43)+"&code="+url.QueryEscape(`x"><script>alert(1)</script>;component=evil`), "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "package=dev.jeeth.privatestore;end") || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("unsafe browser return page", w.Body.String())
	}
	r := httptest.NewRequest("POST", "/v1/auth/gravitas/exchange", strings.NewReader(`{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"No identity injection","subject":"admin"}`))
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 400 {
		t.Fatal("caller supplied identity accepted")
	}
}
