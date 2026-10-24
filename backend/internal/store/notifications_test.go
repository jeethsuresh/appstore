package store

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func notificationFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := nativeFixture(t)
	s.cfg.NotificationURL = "https://apps.gravitas.cool"
	s.cfg.NotificationKeys = map[string]string{"spacetime": strings.Repeat("n", 48)}
	s.cfg.Apps = append(s.cfg.Apps, App{ID: "spacetime", Package: "com.jeethsuresh.spacetime", Notifications: &NotificationApp{Origin: "https://spacetime.gravitas.cool", Authority: "com.jeethsuresh.spacetime.notifications"}})
	w := request(s, "POST", "/v1/auth/gravitas/exchange", "", `{"code":"first","codeVerifier":"`+strings.Repeat("v", 43)+`","deviceName":"Test"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	return s, field(t, w, "token"), field(t, w, "deviceId")
}
func subscribeNotification(t *testing.T, s *Server, credential, device string) string {
	t.Helper()
	w := request(s, "PUT", "/v1/notification-apps", credential, `{"apps":["spacetime"]}`)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	var id string
	if err := s.db.QueryRow("SELECT id FROM notification_providers WHERE device=? AND enabled=1", device).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func deliverNotification(s *Server, provider, id string) *httptest.ResponseRecorder {
	return request(s, "POST", "/v1/notifications/spacetime", s.cfg.NotificationKeys["spacetime"], fmt.Sprintf(`{"provider":%q,"id":%q,"payload":{"kind":"event.invited","event_id":"%s","version":1,"username":"native-user","title":"Dinner"}}`, provider, id, strings.Repeat("f", 48)))
}
func TestNotificationRegistrationIdentityRetryAndRemoval(t *testing.T) {
	s, credential, device := notificationFixture(t)
	provider := subscribeNotification(t, s, credential, device)
	var calls int
	var enabled bool
	s.notificationClient = &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://spacetime.gravitas.cool/api/notifications/providers/"+provider || bearer(r) != s.cfg.NotificationKeys["spacetime"] {
			t.Fatal("untrusted provider registration", r.URL)
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["issuer"] != "https://gravitas.cool" || body["subject"] != "stable-user" || body["callback"] != "https://apps.gravitas.cool/v1/notifications/spacetime" {
			t.Fatal("wrong identity", body)
		}
		enabled = body["enabled"].(bool)
		code := 204
		if calls == 1 {
			code = 503
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	s.syncNotificationProviders(t.Context())
	s.syncNotificationProviders(t.Context())
	s.syncNotificationProviders(t.Context())
	if calls != 2 || !enabled {
		t.Fatal("failed registration not retried or successful one repeated", calls)
	}
	if w := deliverNotification(s, provider, "invite"); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(s, "PUT", "/v1/notification-apps", credential, `{"apps":[]}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := deliverNotification(s, provider, "late"); w.Code != 410 {
		t.Fatal("removed app accepted callback", w.Code)
	}
	s.syncNotificationProviders(t.Context())
	if calls != 3 || enabled {
		t.Fatal("upstream provider not removed")
	}
	next := subscribeNotification(t, s, credential, device)
	if next == provider {
		t.Fatal("reinstallation reused stale provider")
	}
}
func TestNotificationQueueAuthenticationIsolationAndReplay(t *testing.T) {
	s, credential, device := notificationFixture(t)
	provider := subscribeNotification(t, s, credential, device)
	for _, key := range []string{"", s.cfg.AdminToken, strings.Repeat("x", 48)} {
		if w := request(s, "POST", "/v1/notifications/spacetime", key, `{}`); w.Code != 401 {
			t.Fatal("forged callback", w.Code)
		}
	}
	if w := request(s, "PUT", "/v1/notification-apps", credential, `{"apps":["daybreak"]}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for range 2 {
		if w := deliverNotification(s, provider, "invite"); w.Code != 204 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate queued", count, err)
	}
	server := httptest.NewServer(s)
	defer server.Close()
	read := func() int64 {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/notifications/stream", nil)
		r.Header.Set("Authorization", "Bearer "+credential)
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		line, err := bufio.NewReader(resp.Body).ReadBytes('\n')
		if err != nil {
			t.Fatal("no streamed delivery", err)
		}
		var m struct {
			ID      int64
			App     string
			Payload map[string]any
		}
		if json.Unmarshal(line, &m) != nil || m.App != "spacetime" || m.Payload["title"] != "Dinner" {
			t.Fatal("wrong envelope", string(line))
		}
		return m.ID
	}
	seq := read()
	if read() != seq {
		t.Fatal("unacknowledged delivery was lost")
	}
	other, _ := enrollDevice(t, s)
	if w := request(s, "DELETE", fmt.Sprintf("/v1/notifications/%d", seq), other, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	var acked int
	_ = s.db.QueryRow("SELECT acked FROM notifications WHERE seq=?", seq).Scan(&acked)
	if acked != 0 {
		t.Fatal("another device acknowledged message")
	}
	if w := request(s, "DELETE", fmt.Sprintf("/v1/notifications/%d", seq), credential, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	_ = s.db.QueryRow("SELECT acked FROM notifications WHERE seq=?", seq).Scan(&acked)
	if acked != 1 {
		t.Fatal("ack not persisted")
	}
	if w := deliverNotification(s, provider, "invite"); w.Code != 204 {
		t.Fatal(w.Code)
	}
	_ = s.db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count)
	if count != 1 {
		t.Fatal("acknowledged delivery replayed")
	}
	if w := request(s, "DELETE", "/v1/device", credential, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := deliverNotification(s, provider, "after-logout"); w.Code != 410 {
		t.Fatal("revoked account accepted callback", w.Code)
	}
}
func TestNotificationAccountChangeAndBoundedQueue(t *testing.T) {
	s, credential, device := notificationFixture(t)
	provider := subscribeNotification(t, s, credential, device)
	_, err := s.db.Exec("UPDATE device_identities SET subject='other' WHERE device_id=?", device)
	if err != nil {
		t.Fatal(err)
	}
	if w := deliverNotification(s, provider, "old-account"); w.Code != 410 {
		t.Fatal("cross-account delivery", w.Code)
	}
	next := subscribeNotification(t, s, credential, device)
	if next == provider {
		t.Fatal("identity change kept provider")
	}
	for i := range 256 {
		if w := deliverNotification(s, next, fmt.Sprint(i)); w.Code != 204 {
			t.Fatal(i, w.Code)
		}
	}
	if w := deliverNotification(s, next, "overflow"); w.Code != 503 {
		t.Fatal("queue is unbounded", w.Code)
	}
	if w := request(s, "POST", "/v1/notifications/spacetime", s.cfg.NotificationKeys["spacetime"], fmt.Sprintf(`{"provider":%q,"id":"bad","payload":null}`, next)); w.Code != 400 {
		t.Fatal("non-object payload accepted", w.Code)
	}
}

func TestNotificationStaleUnregisterCannotUndoNewSubscription(t *testing.T) {
	s, credential, device := notificationFixture(t)
	for _, body := range []string{`{"apps":[],"generation":1}`, `{"apps":["spacetime"],"generation":2}`, `{"apps":[],"generation":1}`} {
		if w := request(s, "PUT", "/v1/notification-apps", credential, body); w.Code != 204 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var active int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM notification_providers WHERE device=? AND enabled=1", device).Scan(&active); err != nil || active != 1 {
		t.Fatal("stale disable overrode newer enable", active, err)
	}
}
