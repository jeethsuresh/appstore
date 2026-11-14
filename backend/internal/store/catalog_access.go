package store

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"private-store/internal/gravitas"
)

type catalogIdentityKey struct{}

func (s *Server) initCatalogAccess() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS catalog_memberships (
		issuer TEXT NOT NULL, subject TEXT NOT NULL, publisher TEXT NOT NULL, app TEXT NOT NULL,
		PRIMARY KEY(issuer, subject, publisher, app)
	);
	CREATE TABLE IF NOT EXISTS catalog_global_apps (app TEXT PRIMARY KEY);`)
	if err != nil {
		return err
	}
	s.mux.HandleFunc("PUT /v1/catalog/{publisher}/accounts", s.registerCatalogAccount)
	s.mux.HandleFunc("POST /admin/catalog/{app}/visibility", s.globalizeCatalogApp)
	return nil
}

func (s *Server) registerCatalogAccount(w http.ResponseWriter, r *http.Request) {
	publisher := r.PathValue("publisher")
	config, ok := s.cfg.CatalogPublishers[publisher]
	if !ok || config.Origin == "" || !hmac.Equal([]byte(hash(bearer(r))), []byte(hash(config.Key))) {
		fail(w, 401, "unauthorized")
		return
	}
	var body struct {
		Issuer string   `json:"issuer"`
		Proof  string   `json:"sessionProof"`
		Apps   []string `json:"apps"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Apps) == 0 || len(body.Apps) > maxCatalogApps {
		fail(w, 400, "supply the registered apps")
		return
	}
	for _, id := range body.Apps {
		if !appIDPattern.MatchString(id) {
			fail(w, 400, "invalid app ID")
			return
		}
	}
	if s.cfg.Federation == nil {
		fail(w, 503, "Gravitas is unavailable")
		return
	}
	subject, err := s.cfg.Federation.CatalogAccount(r.Context(), body.Issuer, config.Origin, body.Proof)
	if err != nil {
		if errors.Is(err, gravitas.ErrLoginRequired) {
			fail(w, 401, "a verified service account is required")
		} else {
			fail(w, 503, "Gravitas is unavailable")
		}
		return
	}
	c := &s.published
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, id := range body.Apps {
		if c.identities[id].Publisher != publisher ||
			!slices.ContainsFunc(c.apps, func(a App) bool { return a.ID == id && a.catalogPublisher == publisher }) {
			fail(w, 403, "app is unavailable from this publisher")
			return
		}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	for _, id := range body.Apps {
		_, err := tx.ExecContext(r.Context(), "INSERT OR IGNORE INTO catalog_memberships VALUES (?,?,?,?)", body.Issuer, subject, publisher, id)
		if err != nil {
			s.internal(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) visibleAndroidApps(ctx context.Context) ([]App, error) {
	apps := s.catalogApps()
	if !s.cfg.PersonalCatalog {
		return apps, nil
	}
	identity, _ := ctx.Value(catalogIdentityKey{}).(gravitas.Identity)
	if identity.Subject == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT app, '' FROM catalog_global_apps
		UNION ALL SELECT app, publisher FROM catalog_memberships WHERE issuer=? AND subject=?`, identity.Issuer, identity.Subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	visible := map[string]map[string]bool{}
	for rows.Next() {
		var app, publisher string
		if err := rows.Scan(&app, &publisher); err != nil {
			return nil, err
		}
		if visible[app] == nil {
			visible[app] = map[string]bool{}
		}
		visible[app][publisher] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	domain := strings.ToLower(identity.Domain)
	out := make([]App, 0, len(apps))
	for _, app := range apps {
		host := app.catalogHost
		if app.ID == "appstore" || (app.catalogPublisher == "" && app.ManualGlobal) ||
			visible[app.ID][""] || (app.catalogPublisher != "" && visible[app.ID][app.catalogPublisher]) ||
			(!app.HideFromUnregisteredUsersOnDomain && domain != "" &&
				(host == domain || strings.HasSuffix(host, "."+domain))) {
			out = append(out, app)
		}
	}
	return out, nil
}

func (s *Server) catalogAdmin(r *http.Request) (bool, error) {
	if s.cfg.Federation == nil {
		return false, nil
	}
	identity, err := s.cfg.Federation.CookieIdentity(r)
	if err != nil {
		return false, nil
	}
	return s.cfg.Federation.CheckStoreAdmin(r.Context(), identity)
}

func (s *Server) globalizeCatalogApp(w http.ResponseWriter, r *http.Request) {
	federation := s.cfg.Federation
	if federation == nil || !federation.ValidMutation(r) {
		fail(w, 403, "Gravitas administrator required")
		return
	}
	admin, err := s.catalogAdmin(r)
	if errors.Is(err, gravitas.ErrUnavailable) {
		fail(w, 503, "Gravitas is unavailable")
		return
	}
	if err != nil || !admin {
		fail(w, 403, "Gravitas administrator required")
		return
	}
	id := r.PathValue("app")
	if !slices.ContainsFunc(s.catalogApps(), func(a App) bool { return a.ID == id }) {
		fail(w, 404, "unknown app")
		return
	}
	var body struct {
		Global *bool `json:"global"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Global == nil {
		fail(w, 400, "supply global")
		return
	}
	if *body.Global {
		_, err = s.db.ExecContext(r.Context(), "INSERT OR IGNORE INTO catalog_global_apps VALUES (?)", id)
	} else {
		_, err = s.db.ExecContext(r.Context(), "DELETE FROM catalog_global_apps WHERE app=?", id)
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(204)
}

func catalogOriginHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
