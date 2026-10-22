package store

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Server) nativeStart(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas sign-in is not configured")
		return
	}
	var in struct {
		State     string `json:"state"`
		Challenge string `json:"codeChallenge"`
	}
	if !decode(w, r, &in) {
		return
	}
	target, err := s.cfg.Federation.NativeStart(in.State, in.Challenge)
	if err != nil {
		fail(w, 400, "Unable to start Gravitas sign-in")
		return
	}
	reply(w, 200, map[string]string{"authorizationUrl": target, "redirectUri": s.cfg.Federation.NativeRedirect()})
}
func (s *Server) nativeExchange(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas sign-in is not configured")
		return
	}
	var in struct {
		Code     string `json:"code"`
		Verifier string `json:"codeVerifier"`
		Name     string `json:"deviceName"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 {
		fail(w, 400, "A device name is required")
		return
	}
	select {
	case s.nativeGate <- struct{}{}:
		defer func() { <-s.nativeGate }()
	default:
		fail(w, 429, "Too many sign-ins. Try again shortly.")
		return
	}
	identity, err := s.cfg.Federation.NativeExchange(in.Code, in.Verifier)
	if err != nil {
		fail(w, 401, "Sign-in expired or was not completed. Start again.")
		return
	}
	// The IAM code is one-use and tied to this exact native callback and PKCE verifier.
	// Link a proven existing enrollment in place; otherwise issue a new device credential.
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(r.Context(), "SELECT id FROM devices WHERE token_hash=? AND revoked=0 AND expires>?", hash(bearer(r)), time.Now().Unix()).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		s.internal(w, err)
		return
	}
	credential := token()
	expires := time.Now().Add(365 * 24 * time.Hour).Unix()
	if id == "" {
		id = token()
		_, err = tx.ExecContext(r.Context(), "INSERT INTO devices(id,name,token_hash,expires,created) VALUES(?,?,?,?,?)", id, in.Name, hash(credential), expires, time.Now().UTC().Format(time.RFC3339))
	} else {
		_, err = tx.ExecContext(r.Context(), "UPDATE devices SET name=?,token_hash=?,expires=? WHERE id=?", in.Name, hash(credential), expires, id)
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), "INSERT INTO device_identities(device_id,issuer,subject,username) VALUES(?,?,?,?) ON CONFLICT(device_id) DO UPDATE SET issuer=excluded.issuer,subject=excluded.subject,username=excluded.username", id, identity.Issuer, identity.Subject, identity.Username)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 201, map[string]any{"deviceId": id, "token": credential, "expiresAt": expires, "account": identity.UserID})
}
func (s *Server) assetLinks(w http.ResponseWriter, r *http.Request) {
	// This is the public certificate of the permanently signed Android client.
	reply(w, 200, []any{map[string]any{"relation": []string{"delegate_permission/common.handle_all_urls"}, "target": map[string]any{"namespace": "android_app", "package_name": "dev.jeeth.privatestore", "sha256_cert_fingerprints": []string{"E7:0B:5B:54:A0:D1:74:F4:AD:88:67:88:9F:64:81:A2:B1:93:DC:9C:13:06:30:F3:21:56:3E:12:F3:29:37:93"}}}})
}

var nativeReturnPage = template.Must(template.New("native-return").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Return to Private Store</title><link rel="stylesheet" href="/assets/style.css"></head><body><main class="page"><section class="intro"><p class="eyebrow">PRIVATE STORE · GRAVITAS</p><h1>Back to<br>your apps.</h1><p class="lede">Finish signing in inside Private Store. Your password stays with Gravitas.</p>{{if .}}<a class="button" href="{{.}}">Open Private Store <span aria-hidden="true">↗</span></a><p class="button-note">If the app doesn’t open, enable its “Open supported links” setting in Android, then start sign-in again.</p>{{else}}<p class="notice">This sign-in link is incomplete. Open Private Store and start sign-in again.</p>{{end}}</section></main></body></html>`))

func (s *Server) nativeReturn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	// A fallback for browsers that do not automatically open verified HTTPS App Links.
	// It contains an authorization code protected by PKCE, never a device/IAM bearer token.
	var target template.URL
	q := r.URL.Query()
	if len(q["state"]) == 1 && len(q.Get("state")) == 43 && (len(q["code"]) == 1 || len(q["error"]) == 1) && len(q.Get("code")) <= 2048 && s.cfg.Federation != nil {
		origin, _ := url.Parse(s.cfg.Federation.AppURL)
		values := url.Values{"state": {q.Get("state")}}
		if q.Get("code") != "" {
			values.Set("code", q.Get("code"))
		} else {
			values.Set("error", "access_denied")
		}
		target = template.URL("intent://" + origin.Host + "/auth/android/callback?" + values.Encode() + "#Intent;scheme=https;package=dev.jeeth.privatestore;end")
	}
	_ = nativeReturnPage.Execute(w, target)
}
