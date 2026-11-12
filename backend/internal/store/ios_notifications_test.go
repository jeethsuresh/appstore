package store

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func iosFixture(t *testing.T) (*Server, string) {
	t.Helper()
	s, credential, device := notificationFixture(t)
	android := subscribeNotification(t, s, credential, device)
	s.cfg.ProtocolRelayKey = strings.Repeat("r", 48)
	s.cfg.Federation.ProtocolRegistry = json.RawMessage(`{"protocols":[{"protocol":"appstore","version":"1.0.0"}]}`)
	base := s.cfg.Federation.HTTP.Transport
	s.cfg.Federation.HTTP.Transport = nativeTransport(func(r *http.Request) (*http.Response, error) {
		proof := bearer(r)
		switch r.URL.Path {
		case "/protocols/v1/registry/appstore":
			if proof != s.cfg.ProtocolRelayKey || r.Method != http.MethodPut {
				t.Error("delegated registry was not authenticated")
			}
			var body struct {
				Proof string                    `json:"session_proof"`
				Spec  struct{ Protocol string } `json:"specification"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Spec.Protocol != "appstore" {
				t.Error("invalid registration body")
			}
			proof = body.Proof
		case "/oauth/session":
		default:
			return base.RoundTrip(r)
		}
		origin, subject := "https://spacetime.gravitas.cool", "stable-user"
		switch proof {
		case strings.Repeat("i", 43):
		case strings.Repeat("j", 43):
			subject = "other-user"
		case strings.Repeat("x", 43):
			origin = "https://other.example"
		default:
			return base.RoundTrip(r)
		}
		body := fmt.Sprintf(`{"active":true,"iss":"https://gravitas.cool","app_origin":%q,"sub":%q,"session_id":%q,"exp":%d}`,
			origin, subject, proof[:1], time.Now().Truncate(time.Hour).Add(24*time.Hour).Unix())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	s.apnsApps = map[string]apnsApp{
		"spacetime": {origin: "https://spacetime.gravitas.cool", targets: map[string]*apnsClient{"sandbox": testAPNSClient(t)}},
	}
	return s, android
}

func testAPNSClient(t *testing.T) *apnsClient {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newAPNSClient("sandbox", apnsCredentials{
		Topic: "cool.gravitas.example", KeyID: "ABCDEF0123", TeamID: "FEDCBA3210",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func registerIOS(t *testing.T, s *Server, installation, deviceToken, proof, key string, generation, status int) string {
	t.Helper()
	w := request(s, "PUT", "/v1/notification-installations/spacetime/"+installation, key,
		fmt.Sprintf(`{"platform":"ios","environment":"sandbox","token":%q,"session_proof":%q,"generation":%d}`, deviceToken, proof, generation))
	if w.Code != status {
		t.Fatalf("registration returned %d, want %d: %s", w.Code, status, w.Body)
	}
	if status == 200 {
		return field(t, w, "device")
	}
	return ""
}

func iosPush(t *testing.T, s *Server, device, subject, id string, payload any) int {
	t.Helper()
	capability := "push.broadcast"
	input := map[string]any{"app": "spacetime", "payload": payload}
	if device != "" {
		capability = "push.send"
		input["device"] = device
	}
	body, err := json.Marshal(map[string]any{
		"id": id, "protocol": "appstore", "version": "1.0.0", "capability": capability,
		"issuer": "https://gravitas.cool", "subject": subject, "sender": "https://spacetime.gravitas.cool", "input": input,
	})
	if err != nil {
		t.Fatal(err)
	}
	return request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, string(body)).Code
}

func TestIOSRegistrationAndSharedPushRouting(t *testing.T) {
	s, android := iosFixture(t)
	key, installation, deviceToken, proof := s.cfg.NotificationKeys["spacetime"], token(), strings.Repeat("a", 64), strings.Repeat("i", 43)
	registerIOS(t, s, installation, deviceToken, proof, s.cfg.ProtocolRelayKey, 1, 401)
	registerIOS(t, s, installation, deviceToken, strings.Repeat("x", 43), key, 1, 403)
	provider := registerIOS(t, s, installation, deviceToken, proof, key, 1, 200)
	if retry := registerIOS(t, s, installation, deviceToken, proof, key, 1, 200); retry != provider {
		t.Fatal("registration replay changed opaque handle")
	}
	if updated := registerIOS(t, s, installation, strings.Repeat("b", 72), proof, key, 2, 200); updated != provider {
		t.Fatal("token refresh changed opaque handle")
	}
	registerIOS(t, s, installation, deviceToken, proof, key, 1, 409)

	payload := map[string]string{"title": "Dinner"}
	for _, target := range []struct{ device, subject, event string }{
		{provider, "other-user", "wrong-account"},
		{provider, "stable-user", "ios-only"},
		{provider, "stable-user", "ios-only"},
		{android, "stable-user", "android-only"},
		{"", "stable-user", "mixed"},
	} {
		if code := iosPush(t, s, target.device, target.subject, target.event, payload); code != 204 {
			t.Fatal("push status", code)
		}
	}
	for _, table := range []string{"ios_notifications", "notifications"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 2 {
			t.Fatalf("%s has %d deliveries: %v", table, count, err)
		}
	}
	if _, err := s.db.Exec(`WITH RECURSIVE numbers(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM numbers WHERE i<254)
 INSERT INTO ios_notifications(provider,event,payload,created,next)
 SELECT ?, 'full-' || i, '{}', 0, 0 FROM numbers`, provider); err != nil {
		t.Fatal(err)
	}
	if code := iosPush(t, s, "", "stable-user", "full", payload); code != 503 {
		t.Fatal("full iOS queue should backpressure a mixed broadcast", code)
	}
	var androidCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&androidCount); err != nil || androidCount != 2 {
		t.Fatal("partial broadcast committed", androidCount, err)
	}
	switched := registerIOS(t, s, installation, deviceToken, strings.Repeat("j", 43), key, 3, 200)
	if switched == provider {
		t.Fatal("account change reused the old handle")
	}
	w := request(s, "DELETE", "/v1/notification-installations/spacetime/"+installation, key, `{"generation":4}`)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	registerIOS(t, s, installation, deviceToken, strings.Repeat("j", 43), key, 3, 409)
	var pending int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ios_notifications").Scan(&pending); err != nil || pending != 0 {
		t.Fatal("old account still has deliveries", pending, err)
	}
}

func TestIOSDeliveryAPNSWireRetryAndInvalidToken(t *testing.T) {
	s, _ := iosFixture(t)
	client := s.apnsApps["spacetime"].targets["sandbox"]
	deviceToken := strings.Repeat("a", 72)
	provider := registerIOS(t, s, token(), deviceToken, strings.Repeat("i", 43), s.cfg.NotificationKeys["spacetime"], 1, 200)
	var calls atomic.Int32
	apple := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.Method != "POST" || r.URL.Path != "/3/device/"+deviceToken ||
			r.Header.Get("apns-topic") != client.topic || r.Header.Get("apns-push-type") != "alert" ||
			len(r.Header.Get("apns-collapse-id")) != 64 {
			t.Error("invalid APNs wire request")
		}
		verifyAPNSJWT(t, client, strings.TrimPrefix(r.Header.Get("Authorization"), "bearer "))
		var body struct {
			Subject string
			Payload map[string]string
			APS     map[string]any
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Subject != "stable-user" || body.Payload["title"] != "Dinner" || body.APS["alert"] == nil {
			t.Error("invalid APNs payload")
		}
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"reason":"ServiceUnavailable"}`)
		case 2:
			w.WriteHeader(200)
		default:
			w.WriteHeader(410)
			_, _ = io.WriteString(w, `{"reason":"Unregistered"}`)
		}
	}))
	apple.EnableHTTP2 = true
	apple.StartTLS()
	t.Cleanup(apple.Close)
	transport := client.http.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	transport.TLSClientConfig.RootCAs.AddCert(apple.Certificate())
	client.endpoint = apple.URL

	push := func(id string) {
		t.Helper()
		if code := iosPush(t, s, provider, "stable-user", id, map[string]string{"title": "Dinner"}); code != 204 {
			t.Fatal(code)
		}
	}
	drain := func() {
		t.Helper()
		if _, err := s.deliverIOSNotifications(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	push("retry")
	drain()
	drain()
	if calls.Load() != 1 {
		t.Fatal("retry did not back off")
	}
	if _, err := s.db.Exec("UPDATE ios_notifications SET next=0"); err != nil {
		t.Fatal(err)
	}
	drain()
	push("retry")
	drain()
	if calls.Load() != 2 {
		t.Fatal("successful delivery was replayed")
	}
	push("unregistered")
	drain()
	push("after-unregistered")
	drain()
	if calls.Load() != 3 {
		t.Fatal("APNs-invalidated token was used again")
	}
}

func verifyAPNSJWT(t *testing.T, client *apnsClient, jwt string) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Error("missing APNs provider JWT")
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err != nil || len(signature) != 64 || !ecdsa.Verify(&client.key.PublicKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Error("invalid ES256 APNs signature")
	}
}

func TestAPNSOversizePayloadPromptsRefresh(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"title": strings.Repeat("中", 1500)})
	body := apnsPayload(strings.Repeat("a", 64), "stable-user", raw)
	var fields map[string]json.RawMessage
	if len(body) > apnsMaxPayload || json.Unmarshal(body, &fields) != nil ||
		string(fields["refresh"]) != "true" || fields["payload"] != nil || fields["aps"] == nil {
		t.Fatal("invalid oversized-push fallback")
	}
}
