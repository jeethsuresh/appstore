package store

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestExistingDeviceProtocolRegistryReconciliation(t *testing.T) {
	s := nativeFixture(t)
	s.cfg.ProtocolRelayKey = strings.Repeat("r", 48)
	s.cfg.Federation.ProtocolRegistry = json.RawMessage(`{"schema_version":1,"protocols":[]}`)
	base := s.cfg.Federation.HTTP.Transport
	backfills := 0
	s.cfg.Federation.HTTP.Transport = nativeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/app/v1/protocols/registry" || r.URL.Path == "/protocols/v1/registry" {
			if r.URL.Path == "/protocols/v1/registry" {
				backfills++
				var body struct {
					Proof    string          `json:"session_proof"`
					Registry json.RawMessage `json:"registry"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Proof != strings.Repeat("p", 43) || bearer(r) != s.cfg.ProtocolRelayKey || !json.Valid(body.Registry) {
					t.Fatal("unbound reconciliation")
				}
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		response, err := base.RoundTrip(r)
		if err == nil && r.URL.Path == "/.well-known/iam-configuration" {
			var body map[string]any
			json.NewDecoder(response.Body).Decode(&body)
			response.Body.Close()
			body["protocol_registry_endpoint"] = "https://gravitas.cool/app/v1/protocols/registry"
			data, _ := json.Marshal(body)
			response.Body = io.NopCloser(strings.NewReader(string(data)))
		}
		return response, err
	})
	w := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Existing phone"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.syncProtocolRegistry(t.Context())
	s.syncProtocolRegistry(t.Context())
	if backfills != 1 {
		t.Fatal("successful reconciliation did not retain its retry schedule", backfills)
	}
}

func protocolEnvelope(t *testing.T, s *Server, provider, id string) string {
	t.Helper()
	var subject string
	if err := s.db.QueryRow("SELECT subject FROM notification_providers WHERE id=?", provider).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"id": id, "protocol": "appstore", "version": "1.0.0", "capability": "push.send", "issuer": "https://gravitas.cool", "subject": subject, "sender": "https://spacetime.gravitas.cool", "input": map[string]any{"app": "spacetime", "device": provider, "payload": map[string]any{"kind": "event.invited", "title": "Dinner"}}})
	return string(body)
}

func TestProtocolPushAuthenticationIdentityReplayAndRevocation(t *testing.T) {
	s, credential, device := notificationFixture(t)
	s.cfg.ProtocolRelayKey = strings.Repeat("r", 48)
	provider := subscribeNotification(t, s, credential, device)
	body := protocolEnvelope(t, s, provider, "notify-1")
	if w := request(s, "POST", "/api/protocols/invoke", s.cfg.NotificationKeys["spacetime"], body); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		if w := request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, body); w.Code != 204 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE device=?", device).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	for _, replacement := range []struct{ old, next string }{
		{"https://spacetime.gravitas.cool", "https://other.example"},
		{"https://gravitas.cool", "https://other-issuer.example"},
	} {
		if w := request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, strings.Replace(body, replacement.old, replacement.next, 1)); w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// A valid issuer/subject mismatch is silently unaddressed, and never routed
	// to a different account or exposed to the original protocol sender.
	wrong := strings.Replace(body, "stable-user", "another-user", 1)
	if w := request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, wrong); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, err := s.db.Exec("UPDATE devices SET revoked=1 WHERE id=?", device); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, strings.Replace(body, "notify-1", "notify-2", 1)); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestProtocolPushBroadcastAndBackpressureAreAtomic(t *testing.T) {
	s, credential, device := notificationFixture(t)
	s.cfg.ProtocolRelayKey = strings.Repeat("r", 48)
	provider := subscribeNotification(t, s, credential, device)
	secondCredential, secondDevice := enrollDevice(t, s)
	_, err := s.db.Exec("INSERT INTO device_identities(device_id,issuer,subject,username) VALUES(?,?,?,?)", secondDevice, "https://gravitas.cool", "stable-user", "native-user")
	if err != nil {
		t.Fatal(err)
	}
	// Copy the verified session envelope so both test phones use the same account.
	if _, err = s.db.Exec("INSERT INTO device_sessions(device_id,proof) SELECT ?,proof FROM device_sessions WHERE device_id=?", secondDevice, device); err != nil {
		t.Fatal(err)
	}
	secondProvider := subscribeNotification(t, s, secondCredential, secondDevice)
	var envelope map[string]any
	json.Unmarshal([]byte(protocolEnvelope(t, s, provider, "broadcast")), &envelope)
	envelope["capability"] = "push.broadcast"
	delete(envelope["input"].(map[string]any), "device")
	body, _ := json.Marshal(envelope)
	if w := request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, string(body)); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	for i := 0; i < 255; i++ {
		if _, err = s.db.Exec("INSERT INTO notifications(provider,device,app,event,payload,created) VALUES(?,?,? ,?,'{}',0)", secondProvider, secondDevice, "spacetime", token()); err != nil {
			t.Fatal(err)
		}
	}
	envelope["id"] = "full"
	body, _ = json.Marshal(envelope)
	if w := request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, string(body)); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE device=?", device).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial broadcast committed", count, err)
	}
}

func TestProtocolPushRejectsInvalidSchema(t *testing.T) {
	s, credential, device := notificationFixture(t)
	s.cfg.ProtocolRelayKey = strings.Repeat("r", 48)
	provider := subscribeNotification(t, s, credential, device)
	var envelope map[string]any
	json.Unmarshal([]byte(protocolEnvelope(t, s, provider, "test")), &envelope)
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["version"] = "2.0.0" },
		func(m map[string]any) { m["input"].(map[string]any)["device"] = "*" },
		func(m map[string]any) { m["input"].(map[string]any)["payload"] = nil },
		func(m map[string]any) { m["callback"] = "http://localhost" },
	} {
		original, _ := json.Marshal(envelope)
		var candidate map[string]any
		json.Unmarshal(original, &candidate)
		mutate(candidate)
		data, _ := json.Marshal(candidate)
		if w := request(s, "POST", "/api/protocols/invoke", s.cfg.ProtocolRelayKey, string(data)); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
