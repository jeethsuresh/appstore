package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The only outbound destinations are operator-configured app origins. Phone
// requests cannot supply URLs, identities, callback credentials or packages.
type NotificationApp struct {
	Origin    string `json:"origin"`
	Authority string `json:"authority"`
}
type notificationHub struct {
	mu        sync.Mutex
	listeners map[string]chan struct{}
	work      chan struct{}
}

func (h *notificationHub) wake(device string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c := h.listeners[device]; c != nil {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}
func (s *Server) initNotifications() error {
	client, err := newNotificationHTTP(s.cfg.NotificationGateway)
	if err != nil {
		return fmt.Errorf("invalid NOTIFICATION_GATEWAY_ADDR: %w", err)
	}
	s.notificationClient = client
	for _, a := range s.cfg.Apps {
		if a.Notifications != nil {
			if !validNotificationOrigin(a.Notifications.Origin) || a.Notifications.Authority != a.Package+".notifications" {
				return fmt.Errorf("invalid notification configuration for %s", a.ID)
			}
		}
	}
	if s.cfg.NotificationURL != "" && !validNotificationOrigin(s.cfg.NotificationURL) {
		return errors.New("NOTIFICATION_PUBLIC_URL must be an HTTPS origin")
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS notification_devices(device TEXT PRIMARY KEY REFERENCES devices(id), generation INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS notification_providers(id TEXT PRIMARY KEY, device TEXT NOT NULL REFERENCES devices(id), app TEXT NOT NULL, issuer TEXT NOT NULL, subject TEXT NOT NULL, enabled INTEGER NOT NULL, synced INTEGER NOT NULL DEFAULT 0);
 CREATE UNIQUE INDEX IF NOT EXISTS notification_active_provider ON notification_providers(device,app) WHERE enabled=1;
 CREATE TABLE IF NOT EXISTS notifications(seq INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, device TEXT NOT NULL REFERENCES devices(id), app TEXT NOT NULL, event TEXT NOT NULL, payload TEXT NOT NULL, created INTEGER NOT NULL, acked INTEGER NOT NULL DEFAULT 0, UNIQUE(provider,event));
 CREATE INDEX IF NOT EXISTS notification_pending ON notifications(device,acked,seq);`)
	if err != nil {
		return err
	}
	s.notifications = &notificationHub{listeners: map[string]chan struct{}{}, work: make(chan struct{}, 1)}
	s.mux.Handle("PUT /v1/notification-apps", s.device(http.HandlerFunc(s.notificationApps)))
	s.mux.Handle("GET /v1/notifications/stream", s.device(http.HandlerFunc(s.notificationStream)))
	s.mux.Handle("DELETE /v1/notifications/{id}", s.device(http.HandlerFunc(s.notificationAck)))
	s.mux.HandleFunc("POST /v1/notifications/{app}", s.notificationReceive)
	return nil
}
func validNotificationOrigin(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == ""
}
func (s *Server) notificationApp(id string) *App {
	for i := range s.cfg.Apps {
		a := &s.cfg.Apps[i]
		if a.ID == id && a.Notifications != nil && len(s.cfg.NotificationKeys[id]) >= 32 && s.cfg.NotificationURL != "" {
			return a
		}
	}
	return nil
}
func (s *Server) wakeNotifications() {
	select {
	case s.notifications.work <- struct{}{}:
	default:
	}
}
func (s *Server) notificationApps(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Apps       []string `json:"apps"`
		Generation int64    `json:"generation"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Generation < 0 || len(in.Apps) > len(s.cfg.Apps) {
		fail(w, 400, "too many apps")
		return
	}
	wanted := map[string]bool{}
	for _, id := range in.Apps {
		if s.notificationApp(id) == nil {
			fail(w, 400, "notifications unavailable for app")
			return
		}
		wanted[id] = true
	}
	device := r.Context().Value(deviceKey{}).(string)
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		s.internal(w, e)
		return
	}
	defer tx.Rollback()
	var generation int64
	e = tx.QueryRow("SELECT generation FROM notification_devices WHERE device=?", device).Scan(&generation)
	if e != nil && e != sql.ErrNoRows {
		s.internal(w, e)
		return
	}
	if in.Generation < generation {
		w.WriteHeader(204)
		return
	}
	if _, e = tx.Exec(`INSERT INTO notification_devices(device,generation) VALUES(?,?) ON CONFLICT(device) DO UPDATE SET generation=excluded.generation`, device, in.Generation); e != nil {
		s.internal(w, e)
		return
	}
	var issuer, subject string
	e = tx.QueryRow("SELECT issuer,subject FROM device_identities WHERE device_id=?", device).Scan(&issuer, &subject)
	if e != nil && len(wanted) > 0 {
		fail(w, 409, "Gravitas account required for app notifications")
		return
	}
	for _, a := range s.cfg.Apps {
		if a.Notifications == nil {
			continue
		}
		// An identity change or reinstallation gets a new immutable provider ID.
		// Delayed callbacks can never become notifications for another account.
		_, e = tx.Exec(`UPDATE notification_providers SET enabled=0,synced=0 WHERE device=? AND app=? AND enabled=1 AND (?=0 OR issuer<>? OR subject<>?)`, device, a.ID, wanted[a.ID], issuer, subject)
		if e != nil {
			s.internal(w, e)
			return
		}
		if wanted[a.ID] {
			_, e = tx.Exec(`INSERT INTO notification_providers(id,device,app,issuer,subject,enabled) SELECT ?,?,?,?,?,1 WHERE NOT EXISTS(SELECT 1 FROM notification_providers WHERE device=? AND app=? AND enabled=1)`, token(), device, a.ID, issuer, subject, device, a.ID)
			if e != nil {
				s.internal(w, e)
				return
			}
		}
	}
	if _, e = tx.Exec(`DELETE FROM notifications WHERE device=? AND provider IN (SELECT id FROM notification_providers WHERE enabled=0)`, device); e != nil {
		s.internal(w, e)
		return
	}

	if e = tx.Commit(); e != nil {
		s.internal(w, e)
		return
	}
	s.wakeNotifications()
	s.notifications.wake(device)
	w.WriteHeader(204)
}

// Server-side retry work never wakes a phone. Registrations survive restarts and
// are reconciled after device revocation/expiry, removal and account changes.
func (s *Server) RunNotifications(ctx context.Context) {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		s.syncNotificationProviders(ctx)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.notifications.work:
		}
	}
}
func (s *Server) syncNotificationProviders(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `UPDATE notification_providers SET enabled=0,synced=0 WHERE enabled=1 AND (device IN (SELECT id FROM devices WHERE revoked=1 OR expires<=?) OR NOT EXISTS (SELECT 1 FROM device_identities i WHERE i.device_id=notification_providers.device AND i.issuer=notification_providers.issuer AND i.subject=notification_providers.subject))`, time.Now().Unix())
	_, _ = s.db.ExecContext(ctx, "DELETE FROM notifications WHERE created<?", time.Now().Add(-7*24*time.Hour).Unix())
	rows, e := s.db.QueryContext(ctx, "SELECT id,app,issuer,subject,enabled FROM notification_providers WHERE synced=0 LIMIT 256")
	if e != nil {
		return
	}
	type provider struct {
		ID, App, Issuer, Subject string
		Enabled                  bool
	}
	var pending []provider
	for rows.Next() {
		var p provider
		if rows.Scan(&p.ID, &p.App, &p.Issuer, &p.Subject, &p.Enabled) == nil {
			pending = append(pending, p)
		}
	}
	rows.Close()
	for _, p := range pending {
		a := s.notificationApp(p.App)
		if a == nil {
			continue
		}
		body, _ := json.Marshal(map[string]any{"issuer": p.Issuer, "subject": p.Subject, "callback": s.cfg.NotificationURL + "/v1/notifications/" + a.ID, "enabled": p.Enabled})
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, _ := http.NewRequestWithContext(reqCtx, "PUT", a.Notifications.Origin+"/api/notifications/providers/"+p.ID, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+s.cfg.NotificationKeys[p.App])
		req.Header.Set("Content-Type", "application/json")
		response, err := s.notificationHTTP().Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if response.StatusCode == 204 {
				if p.Enabled {
					_, _ = s.db.ExecContext(ctx, "UPDATE notification_providers SET synced=1 WHERE id=? AND enabled=1", p.ID)
				} else {
					_, _ = s.db.ExecContext(ctx, "DELETE FROM notification_providers WHERE id=? AND enabled=0", p.ID)
				}
			}
		}
		cancel()
	}
}

// The gateway changes only the TCP destination. The HTTPS URL still controls
// the Host header, SNI and certificate verification, and redirects stay disabled.
func newNotificationHTTP(gateway string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if gateway != "" {
		if _, _, err := net.SplitHostPort(gateway); err != nil {
			return nil, err
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, gateway)
		}
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func (s *Server) notificationHTTP() *http.Client {
	if s.notificationClient != nil {
		return s.notificationClient
	}
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (s *Server) notificationReceive(w http.ResponseWriter, r *http.Request) {
	a := s.notificationApp(r.PathValue("app"))
	if a == nil || !hmac.Equal([]byte(hash(bearer(r))), []byte(hash(s.cfg.NotificationKeys[a.ID]))) {
		fail(w, 401, "unauthorized")
		return
	}
	var in struct {
		Provider string          `json:"provider"`
		ID       string          `json:"id"`
		Payload  json.RawMessage `json:"payload"`
	}
	if !decode(w, r, &in) {
		return
	}
	var object map[string]json.RawMessage
	if len(in.ID) < 1 || len(in.ID) > 200 || len(in.Payload) > 8192 || json.Unmarshal(in.Payload, &object) != nil || object == nil {
		fail(w, 400, "invalid notification")
		return
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		s.internal(w, e)
		return
	}
	defer tx.Rollback()
	var device string
	e = tx.QueryRow(`SELECT p.device FROM notification_providers p JOIN devices d ON d.id=p.device JOIN device_identities i ON i.device_id=d.id WHERE p.id=? AND p.app=? AND p.enabled=1 AND d.revoked=0 AND d.expires>? AND p.issuer=i.issuer AND p.subject=i.subject`, in.Provider, a.ID, time.Now().Unix()).Scan(&device)
	if e != nil {
		if e == sql.ErrNoRows {
			w.WriteHeader(410)
		} else {
			s.internal(w, e)
		}
		return
	}
	var exists int
	if e = tx.QueryRow("SELECT COUNT(*) FROM notifications WHERE provider=? AND event=?", in.Provider, in.ID).Scan(&exists); e != nil {
		s.internal(w, e)
		return
	}
	if exists > 0 {
		w.WriteHeader(204)
		return
	}
	var queued int
	if e = tx.QueryRow("SELECT COUNT(*) FROM notifications WHERE device=? AND acked=0", device).Scan(&queued); e != nil {
		s.internal(w, e)
		return
	}
	if queued >= 256 {
		fail(w, 503, "device queue full")
		return
	}
	_, e = tx.Exec("INSERT INTO notifications(provider,device,app,event,payload,created) VALUES(?,?,?,?,?,?)", in.Provider, device, a.ID, in.ID, string(in.Payload), time.Now().Unix())
	if e != nil {
		s.internal(w, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.internal(w, e)
		return
	}
	s.notifications.wake(device)
	w.WriteHeader(204)
}
func (s *Server) notificationAck(w http.ResponseWriter, r *http.Request) {
	_, e := s.db.ExecContext(r.Context(), "UPDATE notifications SET acked=1,payload='{}' WHERE seq=? AND device=?", r.PathValue("id"), r.Context().Value(deviceKey{}).(string))
	if e != nil {
		s.internal(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) notificationStream(w http.ResponseWriter, r *http.Request) {
	device := r.Context().Value(deviceKey{}).(string)
	wake := make(chan struct{}, 1)
	s.notifications.mu.Lock()
	old := s.notifications.listeners[device]
	s.notifications.listeners[device] = wake
	if old != nil {
		close(old)
	}
	s.notifications.mu.Unlock()
	defer func() {
		s.notifications.mu.Lock()
		if s.notifications.listeners[device] == wake {
			delete(s.notifications.listeners, device)
		}
		s.notifications.mu.Unlock()
	}()
	control := http.NewResponseController(w)
	_ = control.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	if control.Flush() != nil {
		return
	}
	check := time.NewTicker(time.Minute)
	defer check.Stop()
	keepalive := time.NewTicker(15 * time.Minute)
	defer keepalive.Stop()
	var last int64
	for {
		// Revalidate on every message and on the server timer, including central
		// revocation. The timer sends no traffic to the phone.
		valid := false
		s.device(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { valid = true })).ServeHTTP(&discardResponse{header: http.Header{}}, r)
		if !valid {
			return
		}
		rows, e := s.db.QueryContext(r.Context(), `SELECT n.seq,n.app,n.payload FROM notifications n JOIN notification_providers p ON p.id=n.provider JOIN device_identities i ON i.device_id=p.device AND i.issuer=p.issuer AND i.subject=p.subject WHERE n.device=? AND n.acked=0 AND n.seq>? AND p.enabled=1 AND n.created>? ORDER BY n.seq LIMIT 256`, device, last, time.Now().Add(-7*24*time.Hour).Unix())
		if e != nil {
			return
		}
		type message struct {
			ID      int64           `json:"id"`
			App     string          `json:"app"`
			Payload json.RawMessage `json:"payload"`
		}
		var batch []message
		for rows.Next() {
			var m message
			var payload string
			if rows.Scan(&m.ID, &m.App, &payload) != nil {
				rows.Close()
				return
			}
			m.Payload = json.RawMessage(payload)
			batch = append(batch, m)
		}
		err := rows.Err()
		rows.Close()
		if err != nil {
			return
		}
		for _, m := range batch {
			_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if json.NewEncoder(w).Encode(m) != nil || control.Flush() != nil {
				return
			}
			last = m.ID
		}
		_ = control.SetWriteDeadline(time.Time{})
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-wake:
			if !ok {
				return
			}
		case <-check.C:
		case <-keepalive.C:
			_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, e = fmt.Fprintln(w); e != nil || control.Flush() != nil {
				return
			}
			_ = control.SetWriteDeadline(time.Time{})
		}
	}
}

type discardResponse struct{ header http.Header }

func (d *discardResponse) Header() http.Header         { return d.header }
func (d *discardResponse) WriteHeader(int)             {}
func (d *discardResponse) Write(b []byte) (int, error) { return len(b), nil }

// Keys remain server-side. Names are APP_ID=secret separated by commas.
func NotificationKeys(raw string) (map[string]string, error) {
	out := map[string]string{}
	if raw == "" {
		return out, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		id, key, ok := strings.Cut(entry, "=")
		if !ok || len(key) < 32 {
			return nil, errors.New("invalid NOTIFICATION_APP_KEYS")
		}
		out[id] = key
	}
	return out, nil
}
