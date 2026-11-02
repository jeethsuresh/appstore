package store

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Started by the Matrix homeserver's integration test. All identities and
// credentials belong to disposable fixtures; none enter the release binary.
func TestRiverMatrixNotificationInterop(t *testing.T) {
	ready := os.Getenv("MATRIX_PUSH_INTEROP_READY")
	if ready == "" {
		t.Skip("run from the Matrix appstore protocol integration test")
	}
	s, credential, _ := notificationFixture(t)
	s.cfg.ProtocolRelayKey = strings.Repeat("s", 48)
	s.cfg.NotificationKeys["river"] = strings.Repeat("n", 48)
	s.cfg.Apps = append(s.cfg.Apps, App{ID: "river", Package: "cool.gravitas.river", Notifications: &NotificationApp{Origin: "https://chat.gravitas.cool", Authority: "cool.gravitas.river.notifications"}})
	if w := request(s, "PUT", "/v1/notification-apps", credential, `{"apps":["river"],"generation":1}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	server := httptest.NewServer(s)
	defer server.Close()
	data, _ := json.Marshal(map[string]string{"url": server.URL})
	if err := os.WriteFile(ready, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/notifications/stream", nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal("no Matrix push arrived", err)
		}
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var message struct {
			ID      int64
			App     string
			Payload map[string]any
		}
		if json.Unmarshal(line, &message) != nil || message.App != "river" || message.Payload["kind"] != "matrix.event" || message.Payload["subject"] != os.Getenv("NOTIFICATION_INTEROP_SUBJECT") || message.Payload["event_id"] != os.Getenv("MATRIX_PUSH_INTEROP_EVENT") || message.Payload["room_id"] != os.Getenv("MATRIX_PUSH_INTEROP_ROOM") {
			t.Fatalf("wrong native payload: %s", line)
		}
		if len(message.Payload) != 6 {
			t.Fatal("unexpected private data in native alert")
		}
		if w := request(s, "DELETE", "/v1/notifications/"+jsonNumber(message.ID), credential, ""); w.Code != 204 {
			t.Fatal(w.Code)
		}
		var remaining int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE acked=0").Scan(&remaining); err != nil || remaining != 0 {
			t.Fatal("acknowledgement did not clear the queue", remaining, err)
		}
		return
	}
}
