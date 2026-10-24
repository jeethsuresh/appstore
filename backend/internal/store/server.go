package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"private-store/internal/gravitas"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Server struct {
	notifications      *notificationHub
	notificationClient *http.Client
	cfg                Config
	db                 *sql.DB
	mux                *http.ServeMux
	client             *http.Client
	api                string
	inspect            func(context.Context, string) (APKInfo, error)
	syncGate           chan struct{}
	nativeGate         chan struct{}
	queue              chan string
}

func New(cfg Config) (*Server, error) {
	if len(cfg.AdminToken) < 32 || len(cfg.WebhookSecret) < 32 {
		return nil, errors.New("ADMIN_TOKEN and WEBHOOK_SECRET must each contain at least 32 characters")
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "apks"), 0700); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(filepath.Join(cfg.DataDir, "store.db"))
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", "file:"+abs+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS invites(hash TEXT PRIMARY KEY, label TEXT NOT NULL, expires INTEGER NOT NULL, uses INTEGER NOT NULL CHECK(uses>=0));
 CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT UNIQUE NOT NULL, expires INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, created TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS device_identities(device_id TEXT PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE, issuer TEXT NOT NULL, subject TEXT NOT NULL, username TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS device_sessions(device_id TEXT PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE, proof TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS releases(id INTEGER PRIMARY KEY, app_id TEXT NOT NULL, tag TEXT NOT NULL, version_code INTEGER NOT NULL, version_name TEXT NOT NULL, sha256 TEXT NOT NULL, size INTEGER NOT NULL, min_sdk INTEGER NOT NULL, notes TEXT NOT NULL, created TEXT NOT NULL, UNIQUE(app_id,version_code));`)
	if err != nil {
		db.Close()
		return nil, err
	}
	for name, definition := range map[string]string{"asset_id": "INTEGER NOT NULL DEFAULT 0", "config_hash": "TEXT NOT NULL DEFAULT ''"} {
		var present int
		if err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('releases') WHERE name=?", name).Scan(&present); err != nil {
			db.Close()
			return nil, err
		}
		if present == 0 {
			if _, err = db.Exec("ALTER TABLE releases ADD COLUMN " + name + " " + definition); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	if err = os.Chmod(abs, 0600); err != nil {
		db.Close()
		return nil, err
	}
	s := &Server{cfg: cfg, db: db, mux: http.NewServeMux(), client: &http.Client{Timeout: 5 * time.Minute}, api: "https://api.github.com", queue: make(chan string, 32), syncGate: make(chan struct{}, 1), nativeGate: make(chan struct{}, 4)}
	if err := s.initNotifications(); err != nil {
		db.Close()
		return nil, err
	}
	s.inspect = s.inspectAPK
	if err := s.registerWeb(); err != nil {
		db.Close()
		return nil, err
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.PingContext(r.Context()); err != nil {
			fail(w, 503, "database unavailable")
			return
		}
		reply(w, 200, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("POST /v1/enroll", s.enroll)
	s.mux.HandleFunc("POST /v1/auth/gravitas/start", s.nativeStart)
	s.mux.HandleFunc("POST /v1/auth/gravitas/exchange", s.nativeExchange)
	s.mux.HandleFunc("GET /auth/android/callback", s.nativeReturn)
	s.mux.HandleFunc("GET /.well-known/assetlinks.json", s.assetLinks)
	s.mux.Handle("GET /v1/apps", s.device(http.HandlerFunc(s.catalog)))
	s.mux.Handle("GET /v1/releases/{id}/apk", s.device(http.HandlerFunc(s.download)))
	s.mux.Handle("DELETE /v1/device", s.device(http.HandlerFunc(s.logout)))
	s.mux.Handle("POST /admin/invites", s.admin(http.HandlerFunc(s.invite)))
	s.mux.Handle("GET /admin/devices", s.admin(http.HandlerFunc(s.devices)))
	s.mux.Handle("DELETE /admin/devices/{id}", s.admin(http.HandlerFunc(s.revoke)))
	s.mux.Handle("POST /admin/sync/{app}", s.admin(http.HandlerFunc(s.syncApp)))
	s.mux.HandleFunc("POST /webhooks/github", s.webhook)
	return s, nil
}
func (s *Server) Close() error { return s.db.Close() }
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "invalid JSON request")
		return false
	}
	return true
}
func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hmac.Equal([]byte(hash(bearer(r))), []byte(hash(s.cfg.AdminToken))) {
			fail(w, 401, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type deviceKey struct{}

func (s *Server) device(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id, linked, proof string
		err := s.db.QueryRowContext(r.Context(), "SELECT d.id,COALESCE(i.device_id,''),COALESCE(p.proof,'') FROM devices d LEFT JOIN device_identities i ON i.device_id=d.id LEFT JOIN device_sessions p ON p.device_id=d.id WHERE d.token_hash=? AND d.revoked=0 AND d.expires>?", hash(bearer(r)), time.Now().Unix()).Scan(&id, &linked, &proof)
		if err != nil {
			if err != sql.ErrNoRows {
				slog.Error("device authentication", "error", err)
				fail(w, 503, "authentication temporarily unavailable")
				return
			}
			fail(w, 401, "device authentication required")
			return
		}
		// Local device logout remains available during IAM outages. Invite-only
		// enrollments have no central account and keep their existing behavior.
		if linked != "" && !(r.Method == http.MethodDelete && r.URL.Path == "/v1/device") {
			if s.cfg.Federation == nil {
				fail(w, 503, "Gravitas is temporarily unavailable")
				return
			}
			identity, err := s.cfg.Federation.OpenDevice(proof)
			checked := err == nil
			if err == nil {
				err = s.cfg.Federation.CheckIdentity(r.Context(), identity)
			}
			if err != nil {
				if errors.Is(err, gravitas.ErrUnavailable) {
					fail(w, 503, "Gravitas is temporarily unavailable")
					return
				}
				if checked && errors.Is(err, gravitas.ErrLoginRequired) {
					if _, dbErr := s.db.ExecContext(r.Context(), "UPDATE devices SET revoked=1 WHERE id=?", id); dbErr != nil {
						fail(w, 503, "authentication temporarily unavailable")
						return
					}
				}
				fail(w, 401, "Sign in with Gravitas again")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, id)))
	})
}
func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label string `json:"label"`
		Uses  int    `json:"uses"`
		Hours int    `json:"expiresInHours"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Uses < 1 || in.Uses > 100 || in.Hours < 1 || in.Hours > 168 || len(in.Label) > 120 {
		fail(w, 400, "uses must be 1–100, expiry 1–168 hours, label at most 120 characters")
		return
	}
	t := token()
	expires := time.Now().Add(time.Duration(in.Hours) * time.Hour).Unix()
	if _, err := s.db.ExecContext(r.Context(), "INSERT INTO invites VALUES(?,?,?,?)", hash(t), in.Label, expires, in.Uses); err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 201, map[string]any{"invite": t, "expiresAt": expires})
}
func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Invite string `json:"invite"`
		Name   string `json:"deviceName"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Invite) != 64 || in.Name == "" || len(in.Name) > 120 {
		fail(w, 400, "valid invitation and device name required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), "UPDATE invites SET uses=uses-1 WHERE hash=? AND uses>0 AND expires>?", hash(in.Invite), time.Now().Unix())
	if err != nil {
		s.internal(w, err)
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		fail(w, 401, "invitation is invalid, expired, or exhausted")
		return
	}
	id, t := token(), token()
	expires := time.Now().Add(365 * 24 * time.Hour).Unix()
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO devices(id,name,token_hash,expires,created) VALUES(?,?,?,?,?)", id, in.Name, hash(t), expires, time.Now().UTC().Format(time.RFC3339)); err != nil {
		s.internal(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 201, map[string]any{"deviceId": id, "token": t, "expiresAt": expires})
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	fail(w, 500, "internal server error")
}
func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), "SELECT d.id,d.name,d.expires,d.revoked,d.created,COALESCE(i.username,'') FROM devices d LEFT JOIN device_identities i ON i.device_id=d.id ORDER BY d.created DESC")
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, created, account string
		var expires int64
		var revoked bool
		if err = rows.Scan(&id, &name, &expires, &revoked, &created, &account); err != nil {
			s.internal(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "expiresAt": expires, "revoked": revoked, "createdAt": created, "account": account})
	}
	if rows.Err() != nil {
		s.internal(w, rows.Err())
		return
	}
	reply(w, 200, out)
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) { s.revokeID(w, r, r.PathValue("id")) }
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.revokeID(w, r, r.Context().Value(deviceKey{}).(string))
}
func (s *Server) revokeID(w http.ResponseWriter, r *http.Request, id string) {
	res, err := s.db.ExecContext(r.Context(), "UPDATE devices SET revoked=1 WHERE id=?", id)
	if err != nil {
		s.internal(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "unknown device")
		return
	}
	s.notifications.wake(id)
	s.wakeNotifications()
	w.WriteHeader(204)
}
func (s *Server) latest(ctx context.Context, id string) (*Release, error) {
	var v Release
	err := s.db.QueryRowContext(ctx, "SELECT id,tag,version_code,version_name,sha256,size,min_sdk,notes,created,asset_id,config_hash FROM releases WHERE app_id=? ORDER BY version_code DESC LIMIT 1", id).Scan(&v.ID, &v.Tag, &v.VersionCode, &v.VersionName, &v.SHA256, &v.Size, &v.MinSDK, &v.Notes, &v.Created, &v.AssetID, &v.ConfigHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &v, err
}
func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	apps := append([]App{}, s.cfg.Apps...)
	for i := range apps {
		v, err := s.latest(r.Context(), apps[i].ID)
		if err != nil {
			s.internal(w, err)
			return
		}
		if v != nil && v.ConfigHash != appTrust(apps[i]) {
			v = nil
		}
		apps[i].Release = v
		if s.notificationApp(apps[i].ID) == nil {
			apps[i].Notifications = nil
		}
	}
	reply(w, 200, map[string]any{"apps": apps})
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	var digest, appID, configHash string
	err := s.db.QueryRowContext(r.Context(), "SELECT sha256,app_id,config_hash FROM releases WHERE id=?", r.PathValue("id")).Scan(&digest, &appID, &configHash)
	if err == sql.ErrNoRows {
		fail(w, 404, "release not found")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	allowed := false
	for _, a := range s.cfg.Apps {
		if a.ID == appID && configHash == appTrust(a) {
			allowed = true
		}
	}
	if !allowed || !hexDigest.MatchString(digest) {
		fail(w, 404, "release not found")
		return
	}
	f, err := os.Open(filepath.Join(s.cfg.DataDir, "apks", digest+".apk"))
	if err != nil {
		s.internal(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.internal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.apk"`, digest))
	http.ServeContent(w, r, digest+".apk", st.ModTime(), f)
}
func (s *Server) syncApp(w http.ResponseWriter, r *http.Request) {
	var a *App
	for i := range s.cfg.Apps {
		if s.cfg.Apps[i].ID == r.PathValue("app") {
			a = &s.cfg.Apps[i]
			break
		}
	}
	if a == nil {
		fail(w, 404, "unknown app")
		return
	}
	if err := s.Sync(r.Context(), *a, r.URL.Query().Get("tag")); err != nil {
		slog.Error("sync failed", "app", a.ID, "error", err)
		fail(w, 502, err.Error())
		return
	}
	v, err := s.latest(r.Context(), a.ID)
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 200, v)
}
