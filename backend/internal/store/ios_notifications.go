package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"private-store/internal/gravitas"
	"regexp"
	"strings"
	"sync"
	"time"
)

var notificationAppID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func (s *Server) initAPNS() error {
	if s.cfg.APNSConfig != "" {
		var config map[string]struct {
			Origin       string                     `json:"origin"`
			Environments map[string]apnsCredentials `json:"environments"`
		}
		decoder := json.NewDecoder(strings.NewReader(s.cfg.APNSConfig))
		decoder.DisallowUnknownFields()
		if !json.Valid([]byte(s.cfg.APNSConfig)) || decoder.Decode(&config) != nil || config == nil {
			return errors.New("invalid NOTIFICATION_APNS_CONFIG")
		}
		if len(config) > 0 && (s.cfg.Federation == nil || len(s.cfg.ProtocolRelayKey) < 32) {
			return errors.New("APNs requires Gravitas protocol delivery")
		}
		s.apnsApps = make(map[string]apnsApp, len(config))
		topics := map[string]bool{}
		for id, app := range config {
			key := s.cfg.NotificationKeys[id]
			if !notificationAppID.MatchString(id) || !validNotificationOrigin(app.Origin) ||
				len(key) < 32 || key == s.cfg.ProtocolRelayKey || len(app.Environments) == 0 {
				return fmt.Errorf("invalid APNs application configuration for %q", id)
			}
			for other, otherKey := range s.cfg.NotificationKeys {
				if other != id && otherKey == key {
					return errors.New("APNs applications require distinct backend keys")
				}
			}
			for _, catalogApp := range s.cfg.Apps {
				if catalogApp.ID == id && catalogApp.Notifications != nil && catalogApp.Notifications.Origin != app.Origin {
					return fmt.Errorf("APNs and Android notification origins differ for %s", id)
				}
			}
			targets := make(map[string]*apnsClient, len(app.Environments))
			for environment, credentials := range app.Environments {
				topic := environment + "/" + credentials.Topic
				if (environment != "production" && environment != "sandbox") || topics[topic] {
					return fmt.Errorf("invalid APNs environment or duplicate topic for %s", id)
				}
				client, err := newAPNSClient(environment, credentials)
				if err != nil {
					return fmt.Errorf("APNs configuration for %s: %w", id, err)
				}
				topics[topic] = true
				targets[environment] = client
			}
			s.apnsApps[id] = apnsApp{origin: app.Origin, targets: targets}
		}
	}
	_, err := s.db.Exec(`
 CREATE TABLE IF NOT EXISTS ios_notification_installations(
   app TEXT NOT NULL, installation TEXT NOT NULL, provider TEXT NOT NULL UNIQUE,
   generation INTEGER NOT NULL, issuer TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '',
   environment TEXT NOT NULL DEFAULT '', token TEXT NOT NULL DEFAULT '', proof TEXT NOT NULL DEFAULT '',
   expires INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(app,installation)
 );
 CREATE UNIQUE INDEX IF NOT EXISTS ios_notification_token ON ios_notification_installations(app,environment,token) WHERE token<>'';
 CREATE INDEX IF NOT EXISTS ios_notification_subject ON ios_notification_installations(app,subject,expires) WHERE token<>'';
 CREATE TABLE IF NOT EXISTS ios_notifications(
   seq INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL REFERENCES ios_notification_installations(provider) ON DELETE CASCADE,
   event TEXT NOT NULL, payload TEXT NOT NULL, created INTEGER NOT NULL, next INTEGER NOT NULL,
   attempts INTEGER NOT NULL DEFAULT 0, acked INTEGER NOT NULL DEFAULT 0, UNIQUE(provider,event)
 );
 CREATE INDEX IF NOT EXISTS ios_notifications_ready ON ios_notifications(next,seq) WHERE acked=0;
 CREATE INDEX IF NOT EXISTS ios_notifications_pending ON ios_notifications(provider) WHERE acked=0;`)
	if err != nil {
		return err
	}
	s.apnsWake = make(chan struct{}, 1)
	s.mux.HandleFunc("PUT /v1/notification-installations/{app}/{installation}", s.iosInstallation)
	s.mux.HandleFunc("DELETE /v1/notification-installations/{app}/{installation}", s.iosInstallation)
	return nil
}

func (s *Server) iosInstallation(w http.ResponseWriter, r *http.Request) {
	app, installation := r.PathValue("app"), r.PathValue("installation")
	key := s.cfg.NotificationKeys[app]
	if len(key) < 32 || !hmac.Equal([]byte(hash(bearer(r))), []byte(hash(key))) {
		fail(w, 401, "unauthorized")
		return
	}
	if !protocolDevice.MatchString(installation) {
		fail(w, 400, "invalid installation ID")
		return
	}
	var in struct {
		Generation  int64  `json:"generation"`
		Platform    string `json:"platform"`
		Environment string `json:"environment"`
		Token       string `json:"token"`
		Proof       string `json:"session_proof"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Generation <= 0 {
		fail(w, 400, "invalid registration generation")
		return
	}
	var identity gravitas.Identity
	var proof string
	if r.Method == http.MethodPut {
		target := s.apnsApps[app]
		if in.Platform != "ios" || !validAPNSToken(in.Token) {
			fail(w, 400, "invalid iOS registration")
			return
		}
		if target.targets[in.Environment] == nil {
			fail(w, 409, "APNs is unavailable for this app and environment")
			return
		}
		in.Token = strings.ToLower(in.Token)
		var err error
		identity, proof, err = s.cfg.Federation.RegisterNotificationSession(r.Context(), target.origin, in.Proof, s.cfg.ProtocolRelayKey)
		if err != nil {
			if errors.Is(err, gravitas.ErrLoginRequired) {
				fail(w, 403, "active app sign-in required")
			} else {
				fail(w, 503, "notification registration temporarily unavailable")
			}
			return
		}
	} else if in.Platform != "" || in.Environment != "" || in.Token != "" || in.Proof != "" {
		fail(w, 400, "invalid removal request")
		return
	}
	provider, err := s.saveIOSInstallation(r.Context(), app, installation, in.Generation, identity, in.Environment, in.Token, proof)
	if errors.Is(err, errIOSGeneration) {
		fail(w, 409, "registration generation is stale or already in use")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(204)
		return
	}
	s.wakeAPNS()
	reply(w, 200, map[string]string{"device": provider})
}

var errIOSGeneration = errors.New("conflicting installation generation")

const disableIOSInstallation = "UPDATE ios_notification_installations SET issuer='', subject='', session='', environment='', token='', proof='', expires=0"

func (s *Server) saveIOSInstallation(ctx context.Context, app, installation string, generation int64, identity gravitas.Identity, environment, deviceToken, proof string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var provider, issuer, subject, session, previousToken, previousEnvironment string
	var previousGeneration int64
	err = tx.QueryRowContext(ctx, `SELECT provider,issuer,subject,session,environment,token,generation
 FROM ios_notification_installations WHERE app=? AND installation=?`, app, installation).
		Scan(&provider, &issuer, &subject, &session, &previousEnvironment, &previousToken, &previousGeneration)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if generation < previousGeneration {
		return "", errIOSGeneration
	}
	if generation == previousGeneration {
		if issuer == identity.Issuer && subject == identity.Subject && session == identity.SessionID && previousToken == deviceToken && previousEnvironment == environment {
			return provider, nil
		}
		return "", errIOSGeneration
	}
	if deviceToken != "" {
		// A reinstalled app may get the same APNs token. Retire its former
		// binding and queued data atomically before recording the new one.
		args := []any{app, environment, deviceToken, installation}
		match := " WHERE app=? AND environment=? AND token=? AND installation<>?"
		if _, err := tx.ExecContext(ctx, "DELETE FROM ios_notifications WHERE provider IN (SELECT provider FROM ios_notification_installations"+match+")", args...); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, disableIOSInstallation+match, args...); err != nil {
			return "", err
		}
	}
	if provider == "" || issuer != identity.Issuer || subject != identity.Subject {
		if _, err := tx.ExecContext(ctx, "DELETE FROM ios_notifications WHERE provider=?", provider); err != nil {
			return "", err
		}
		provider = token()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ios_notification_installations(app,installation,provider,generation,issuer,subject,session,environment,token,proof,expires)
 VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(app,installation) DO UPDATE SET provider=excluded.provider,generation=excluded.generation,
 issuer=excluded.issuer,subject=excluded.subject,session=excluded.session,environment=excluded.environment,
 token=excluded.token,proof=excluded.proof,expires=excluded.expires`,
		app, installation, provider, generation, identity.Issuer, identity.Subject, identity.SessionID, environment, deviceToken, proof, identity.SessionExpires)
	if err != nil {
		return "", err
	}
	return provider, tx.Commit()
}

var errIOSQueueFull = errors.New("iOS notification queue full")

func enqueueIOSNotifications(ctx context.Context, tx *sql.Tx, app, issuer, subject, device, event string, payload json.RawMessage, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT provider FROM ios_notification_installations
 WHERE app=? AND issuer=? AND subject=? AND expires>? AND token<>'' AND (?='' OR provider=?)`, app, issuer, subject, now, device, device)
	if err != nil {
		return err
	}
	var providers []string
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			rows.Close()
			return err
		}
		providers = append(providers, provider)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(providers) == 0 {
		return nil
	}
	body := apnsPayload(event, subject, payload)
	for _, provider := range providers {
		var duplicate bool
		var pending int
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ios_notifications WHERE provider=? AND event=?),
 (SELECT COUNT(*) FROM ios_notifications WHERE provider=? AND acked=0)`, provider, event, provider).Scan(&duplicate, &pending)
		if err != nil {
			return err
		}
		if duplicate {
			continue
		}
		if pending >= 256 {
			return errIOSQueueFull
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO ios_notifications(provider,event,payload,created,next) VALUES(?,?,?,?,?)", provider, event, string(body), now, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) wakeAPNS() {
	select {
	case s.apnsWake <- struct{}{}:
	default:
	}
}

func (s *Server) RunAPNS(ctx context.Context) {
	if len(s.apnsApps) == 0 {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		more, err := s.deliverIOSNotifications(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("APNs queue", "error", err)
		}
		if more && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.apnsWake:
		}
	}
}

type iosDelivery struct {
	seq, generation, created, expires int64
	attempts                          int
	app, provider, subject            string
	environment, token, proof         string
	event, payload                    string
}

func (s *Server) pruneIOSNotifications(ctx context.Context, now time.Time) error {
	// Keep receipt metadata for the same window as the existing Android queue.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ios_notifications WHERE created<=? OR provider IN
 (SELECT provider FROM ios_notification_installations WHERE expires<=?)`, now.Add(-7*24*time.Hour).Unix(), now.Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, disableIOSInstallation+" WHERE token<>'' AND expires<=?", now.Unix())
	return err
}

func (s *Server) deliverIOSNotifications(ctx context.Context) (bool, error) {
	now := time.Now()
	const batchSize = 16
	rows, err := s.db.QueryContext(ctx, `SELECT q.seq,q.event,q.payload,q.created,q.attempts,
 p.app,p.provider,p.generation,p.subject,p.environment,p.token,p.proof,p.expires
 FROM ios_notifications q JOIN ios_notification_installations p ON p.provider=q.provider
 WHERE q.acked=0 AND q.next<=? AND q.created>? AND p.token<>'' AND p.expires>? ORDER BY q.next,q.seq LIMIT ?`,
		now.Unix(), now.Add(-7*24*time.Hour).Unix(), now.Unix(), batchSize)
	if err != nil {
		return false, err
	}
	var batch []iosDelivery
	for rows.Next() {
		var d iosDelivery
		if err := rows.Scan(&d.seq, &d.event, &d.payload, &d.created, &d.attempts,
			&d.app, &d.provider, &d.generation, &d.subject, &d.environment, &d.token, &d.proof, &d.expires); err != nil {
			rows.Close()
			return false, err
		}
		batch = append(batch, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	var workers sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, d := range batch {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		workers.Go(func() {
			defer func() { <-slots }()
			if err := s.deliverIOSNotification(ctx, d); err != nil && ctx.Err() == nil {
				slog.Error("APNs queue update", "error", err)
			}
		})
	}
	workers.Wait()
	return len(batch) == batchSize, nil
}

func (s *Server) deliverIOSNotification(ctx context.Context, d iosDelivery) error {
	app := s.apnsApps[d.app]
	client := app.targets[d.environment]
	result := apnsRetry
	if client != nil {
		err := s.cfg.Federation.CheckNotificationSession(ctx, app.origin, d.subject, d.proof)
		if errors.Is(err, gravitas.ErrLoginRequired) {
			result = apnsInvalidToken
		} else if err == nil {
			var current bool
			err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ios_notification_installations
 WHERE provider=? AND generation=? AND token<>'' AND expires>?)`, d.provider, d.generation, time.Now().Unix()).Scan(&current)
			if err != nil || !current {
				return err
			}
			expires := min(d.expires, time.Unix(d.created, 0).Add(7*24*time.Hour).Unix(), time.Now().Add(time.Hour).Unix())
			result = client.send(ctx, d.token, d.event, []byte(d.payload), time.Unix(expires, 0))
		}
	}
	switch result {
	case apnsAccepted, apnsRejected:
		_, err := s.db.ExecContext(ctx, "UPDATE ios_notifications SET acked=1,payload='{}' WHERE seq=?", d.seq)
		return err
	case apnsInvalidToken:
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `DELETE FROM ios_notifications WHERE provider IN
 (SELECT provider FROM ios_notification_installations WHERE provider=? AND generation=?)`, d.provider, d.generation)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, disableIOSInstallation+" WHERE provider=? AND generation=?", d.provider, d.generation); err != nil {
			return err
		}
		return tx.Commit()
	default:
		delay := min(30*time.Minute, 15*time.Second<<min(d.attempts, 7))
		_, err := s.db.ExecContext(ctx, "UPDATE ios_notifications SET next=?,attempts=attempts+1 WHERE seq=?", time.Now().Add(delay).Unix(), d.seq)
		return err
	}
}
