package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpacetimeNotificationEndToEnd(t *testing.T) {
	binary := os.Getenv("SPACETIME_TEST_BINARY")
	if binary == "" {
		t.Skip("build spacetime's Go test binary and set SPACETIME_TEST_BINARY")
	}
	s, credential, device := notificationFixture(t)
	store := httptest.NewServer(s)
	defer store.Close()
	if ready := os.Getenv("PROTOCOL_INTEROP_READY"); ready != "" {
		s.cfg.ProtocolRelayKey = strings.Repeat("r", 48)
		data, _ := json.Marshal(map[string]string{"url": store.URL})
		if err := os.WriteFile(ready, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ready := filepath.Join(t.TempDir(), "ready.json")
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestNotificationInteropFixture$", "-test.timeout=40s")
	command.Env = append(os.Environ(), "NOTIFICATION_INTEROP_READY="+ready, "NOTIFICATION_INTEROP_STORE="+store.URL)
	var logs bytes.Buffer
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var upstream struct{ URL, Token string }
	for {
		data, err := os.ReadFile(ready)
		if err == nil && json.Unmarshal(data, &upstream) == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Spacetime fixture did not start")
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer func() {
		request, _ := http.NewRequest("POST", upstream.URL+"/fixture/stop", nil)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			response.Body.Close()
		}
		if err = command.Wait(); err != nil {
			t.Error(err, logs.String())
		}
	}()
	target, _ := url.Parse(upstream.URL)
	s.notificationClient = &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})}
	subscribeNotification(t, s, credential, device)
	s.syncNotificationProviders(ctx)
	streamRequest, _ := http.NewRequestWithContext(ctx, "GET", store.URL+"/v1/notifications/stream", nil)
	streamRequest.Header.Set("Authorization", "Bearer "+credential)
	stream, err := http.DefaultClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	writeEvent := func(method, path string, body any) map[string]any {
		t.Helper()
		data, _ := json.Marshal(body)
		request, _ := http.NewRequestWithContext(ctx, method, upstream.URL+path, bytes.NewReader(data))
		request.Header.Set("Authorization", "Bearer "+upstream.Token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 201 {
			b, _ := io.ReadAll(response.Body)
			t.Fatal(response.StatusCode, string(b))
		}
		var event map[string]any
		if json.NewDecoder(response.Body).Decode(&event) != nil {
			t.Fatal("invalid event response")
		}
		return event
	}
	receive := func(kind, title string) {
		t.Helper()
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				t.Fatal("notification was not relayed", err)
			}
			if strings.TrimSpace(string(line)) == "" {
				continue
			}
			var message struct {
				ID      int64
				App     string
				Payload struct{ Kind, Title, Username string }
			}
			if json.Unmarshal(line, &message) != nil || message.App != "spacetime" || message.Payload.Kind != kind || message.Payload.Title != title || message.Payload.Username != "sam" {
				t.Fatal("wrong relayed message", string(line))
			}
			if w := request(s, "DELETE", "/v1/notifications/"+jsonNumber(message.ID), credential, ""); w.Code != 204 {
				t.Fatal(w.Code)
			}
			return
		}
	}
	event := writeEvent("POST", "/api/events", map[string]any{"title": "E2E dinner", "kind": "event", "start": "2026-10-25T18:00:00Z", "end": "2026-10-25T19:00:00Z", "attendees": map[string]string{"sam": "pending"}})
	receive("event.invited", "E2E dinner")
	event["title"] = "E2E dinner moved"
	writeEvent("PUT", "/api/events/"+event["id"].(string), event)
	receive("event.updated", "E2E dinner moved")
}
func jsonNumber(n int64) string { data, _ := json.Marshal(n); return string(data) }
