package store

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

var protocolDevice = regexp.MustCompile(`^[a-f0-9]{64}$`)

// This endpoint accepts only Gravitas-facilitated envelopes. The sender's app
// namespace is tied to its configured backend origin, and the target must match
// the provider's current issuer/subject/device. No queue counts leave the store.
func (s *Server) protocolInvoke(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.ProtocolRelayKey) < 32 || !hmac.Equal([]byte(hash(bearer(r))), []byte(hash(s.cfg.ProtocolRelayKey))) {
		fail(w, 401, "unauthorized")
		return
	}
	var in struct {
		ID         string `json:"id"`
		Protocol   string `json:"protocol"`
		Version    string `json:"version"`
		Capability string `json:"capability"`
		Issuer     string `json:"issuer"`
		Subject    string `json:"subject"`
		Sender     string `json:"sender"`
		Input      struct {
			App     string          `json:"app"`
			Device  string          `json:"device,omitempty"`
			Payload json.RawMessage `json:"payload"`
		} `json:"input"`
	}
	if !decode(w, r, &in) {
		return
	}
	var object map[string]any
	if in.Protocol != "appstore" || in.Version != "1.0.0" || len(in.ID) < 1 || len(in.ID) > 128 ||
		len(in.Subject) < 1 || len(in.Subject) > 256 ||
		(in.Capability != "push.send" && in.Capability != "push.broadcast") ||
		(in.Capability == "push.send" && !protocolDevice.MatchString(in.Input.Device)) ||
		(in.Capability == "push.broadcast" && in.Input.Device != "") ||
		len(in.Input.Payload) > 8192 || json.Unmarshal(in.Input.Payload, &object) != nil || object == nil {
		fail(w, 400, "invalid appstore protocol input")
		return
	}
	a := s.notificationApp(in.Input.App)
	if a == nil || a.Notifications.Origin != in.Sender || s.cfg.Federation == nil || in.Issuer != s.cfg.Federation.Issuer {
		fail(w, 403, "sender is not authorized for this app")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(r.Context(), `SELECT p.id,p.device FROM notification_providers p
 JOIN devices d ON d.id=p.device JOIN device_identities i ON i.device_id=d.id
 WHERE p.enabled=1 AND p.app=? AND p.issuer=? AND p.subject=? AND p.issuer=i.issuer AND p.subject=i.subject
 AND d.revoked=0 AND d.expires>? AND (?='' OR p.id=?)`, a.ID, in.Issuer, in.Subject, time.Now().Unix(), in.Input.Device, in.Input.Device)
	if err != nil {
		s.internal(w, err)
		return
	}
	type target struct{ provider, device string }
	var targets []target
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.provider, &t.device); err != nil {
			break
		}
		targets = append(targets, t)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		if err == nil {
			err = rowErr
		}
		s.internal(w, err)
		return
	}
	event := hash(in.Sender + "\x00" + in.ID)
	for _, target := range targets {
		var exists, count int
		if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM notifications WHERE provider=? AND event=?", target.provider, event).Scan(&exists); err != nil {
			s.internal(w, err)
			return
		}
		if exists > 0 {
			continue
		}
		if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM notifications WHERE device=? AND acked=0", target.device).Scan(&count); err != nil {
			s.internal(w, err)
			return
		}
		if count >= 256 {
			fail(w, 503, "queue temporarily unavailable")
			return
		}
		if _, err = tx.ExecContext(r.Context(), "INSERT INTO notifications(provider,device,app,event,payload,created) VALUES(?,?,?,?,?,?)", target.provider, target.device, a.ID, event, string(in.Input.Payload), time.Now().Unix()); err != nil {
			s.internal(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		s.internal(w, err)
		return
	}
	for _, target := range targets {
		s.notifications.wake(target.device)
	}
	w.WriteHeader(204)
}

func (s *Server) syncProtocolRegistry(ctx context.Context) {
	if s.cfg.Federation == nil || len(s.cfg.ProtocolRelayKey) < 32 {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.device_id,s.proof FROM device_sessions s JOIN devices d ON d.id=s.device_id LEFT JOIN protocol_registry_sync p ON p.device=d.id WHERE d.revoked=0 AND d.expires>? AND COALESCE(p.next,0)<? LIMIT 32`, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return
	}
	type item struct{ device, proof string }
	var proofs []item
	for rows.Next() {
		var i item
		if rows.Scan(&i.device, &i.proof) == nil {
			proofs = append(proofs, i)
		}
	}
	rows.Close()
	for _, i := range proofs {
		identity, err := s.cfg.Federation.OpenDevice(i.proof)
		if err != nil {
			continue
		}
		// IAM revalidates the session and user itself. Failed registration is
		// retried by the existing server worker; it never wakes the phone.
		if err = s.cfg.Federation.RegisterProtocols(ctx, identity, s.cfg.ProtocolRelayKey); err != nil {
			_, _ = s.db.ExecContext(ctx, "INSERT INTO protocol_registry_sync(device,next) VALUES(?,?) ON CONFLICT(device) DO UPDATE SET next=excluded.next", i.device, time.Now().Add(5*time.Minute).Unix())
			continue
		}
		_, _ = s.db.ExecContext(ctx, "INSERT INTO protocol_registry_sync(device,next) VALUES(?,?) ON CONFLICT(device) DO UPDATE SET next=excluded.next", i.device, time.Now().Add(6*time.Hour).Unix())
	}
}
