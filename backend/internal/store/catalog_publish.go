package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"mime"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
)

const maxCatalogApps = 32
const maxCatalogLogo = 64 << 10

// CatalogPublisher is an operator-approved backend and its release sources.
type CatalogPublisher struct {
	Origin       string   `json:"origin,omitempty"`
	Key          string   `json:"key"`
	Repositories []string `json:"repositories"`
	Adopt        []string `json:"adopt,omitempty"`
}

type catalogEntry struct {
	App
	LogoPNG []byte `json:"logoPng"`
}

type catalogPublication struct {
	SchemaVersion int            `json:"schemaVersion"`
	Revision      int64          `json:"revision"`
	Apps          []catalogEntry `json:"apps"`
}

type catalogIdentity struct {
	Publisher, Repo, Package, Asset, Prefix, Certificate string
	MacCutoff                                            int64
}

type publishedCatalog struct {
	mu           sync.RWMutex
	publications map[string]catalogPublication
	identities   map[string]catalogIdentity
	apps         []App
	logos        map[string][]byte
	pending      map[string]bool
	wake         chan struct{}
}

func ParseCatalogPublishers(raw string) (map[string]CatalogPublisher, error) {
	if raw == "" {
		return nil, nil
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	var publishers map[string]CatalogPublisher
	if d.Decode(&publishers) != nil || d.Decode(&struct{}{}) != io.EOF || publishers == nil {
		return nil, errors.New("invalid CATALOG_PUBLISHERS")
	}
	return publishers, nil
}

func (s *Server) initCatalog() error {
	adoptions := map[string]string{}
	keys := map[string]bool{}
	for id, p := range s.cfg.CatalogPublishers {
		if !appIDPattern.MatchString(id) || len(p.Key) < 32 || keys[p.Key] || len(p.Repositories) == 0 {
			return fmt.Errorf("invalid catalog publisher %q", id)
		}
		keys[p.Key] = true
		if p.Origin != "" && !validNotificationOrigin(p.Origin) {
			return fmt.Errorf("invalid HTTPS origin for catalog publisher %s", id)
		}
		for _, repo := range p.Repositories {
			if !repositoryPattern.MatchString(repo) {
				return fmt.Errorf("invalid repository for catalog publisher %s", id)
			}
		}
		for _, appID := range p.Adopt {
			if !appIDPattern.MatchString(appID) || appID == "appstore" || adoptions[appID] != "" {
				return fmt.Errorf("invalid catalog adoption %s for %s", appID, id)
			}
			adoptions[appID] = id
			for _, a := range s.cfg.Apps {
				if a.ID == appID && !p.allows(a.Repo) {
					return fmt.Errorf("catalog publisher %s cannot adopt repository for %s", id, appID)
				}
			}
		}
	}

	c := &s.published
	c.publications = make(map[string]catalogPublication)
	c.identities = make(map[string]catalogIdentity)
	c.wake = make(chan struct{}, 1)
	c.pending = make(map[string]bool)
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS catalog_publications (
		publisher TEXT PRIMARY KEY, publication BLOB NOT NULL
	);
	CREATE TABLE IF NOT EXISTS catalog_identities (
		app TEXT PRIMARY KEY, identity BLOB NOT NULL
	);`)
	if err != nil {
		return err
	}
	if err := readCatalogRows(s, "SELECT publisher,publication FROM catalog_publications", c.publications); err != nil {
		return err
	}
	if err := readCatalogRows(s, "SELECT app,identity FROM catalog_identities", c.identities); err != nil {
		return err
	}
	s.rebuildCatalog()
	s.mux.HandleFunc("PUT /v1/catalog/{publisher}", s.publishCatalog)
	s.mux.HandleFunc("GET /assets/{asset}", s.publishedLogo)
	return nil
}

func readCatalogRows[T any](s *Server, query string, dst map[string]T) error {
	rows, err := s.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw []byte
		var value T
		if err := rows.Scan(&key, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		dst[key] = value
	}
	return rows.Err()
}

func (p CatalogPublisher) allows(repo string) bool {
	return slices.ContainsFunc(p.Repositories, func(approved string) bool { return strings.EqualFold(approved, repo) })
}

func (s *Server) catalogApps() []App {
	if len(s.cfg.CatalogPublishers) == 0 {
		return s.cfg.Apps
	}
	s.published.mu.RLock()
	defer s.published.mu.RUnlock()
	return s.published.apps
}

// Each rebuild replaces the whole snapshot. Readers can keep using their previous slice.
func (s *Server) rebuildCatalog() {
	c := &s.published
	active := map[string]catalogEntry{}
	hideStatic := map[string]bool{}
	bundled := map[string]App{}
	packages := map[string]string{}
	for _, app := range s.cfg.Apps {
		bundled[app.ID] = app
		packages[app.Package] = app.ID
	}
	for publisher, p := range s.cfg.CatalogPublishers {
		publication, ok := c.publications[publisher]
		if !ok {
			continue
		}
		// A publisher takes over an existing listing only after advertising it.
		for _, id := range p.Adopt {
			if a, ok := bundled[id]; ok && c.identities[id].Publisher == publisher && p.allows(a.Repo) {
				hideStatic[id] = true
			}
		}
		for _, entry := range publication.Apps {
			a := entry.App
			if !p.allows(a.Repo) || (a.Package != "" && packages[a.Package] != "" && packages[a.Package] != a.ID) {
				continue
			}
			if original, ok := bundled[a.ID]; ok {
				if !hideStatic[a.ID] || !identityFor(publisher, original).matches(publisher, a) {
					delete(hideStatic, a.ID)
					continue
				}
				a.Notifications = original.Notifications
			}
			a.catalogPublisher, a.catalogHost = publisher, catalogOriginHost(p.Origin)
			a.LogoURL = "/assets/catalog-" + a.ID + ".png"
			entry.App = a
			active[a.ID] = entry
		}
	}
	apps := make([]App, 0, len(bundled)+len(active))
	logos := make(map[string][]byte, len(active))
	for _, a := range s.cfg.Apps {
		if entry, ok := active[a.ID]; ok {
			apps = append(apps, entry.App)
			logos[entry.LogoURL] = entry.LogoPNG
			delete(active, a.ID)
		} else if !hideStatic[a.ID] {
			apps = append(apps, a)
		}
	}
	ids := make([]string, 0, len(active))
	for id := range active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := active[id]
		apps = append(apps, entry.App)
		logos[entry.LogoURL] = entry.LogoPNG
	}
	c.apps, c.logos = apps, logos
}

func (s *Server) catalogSyncApps() []App {
	c := &s.published
	c.mu.Lock()
	defer c.mu.Unlock()
	var apps []App
	for _, a := range c.apps {
		if c.pending[a.ID] && (a.Package != "" || a.Desktop) {
			apps = append(apps, a)
		}
	}
	clear(c.pending)
	return apps
}

func identityFor(publisher string, a App) catalogIdentity {
	return catalogIdentity{publisher, a.Repo, a.Package, a.Asset, a.ReleaseTagPrefix, a.Certificate, a.MacOSNotarizedFrom}
}

func (id catalogIdentity) matches(publisher string, a App) bool {
	return id.Publisher == publisher && id.Repo == a.Repo && id.Package == a.Package &&
		id.Asset == a.Asset && id.Prefix == a.ReleaseTagPrefix && id.Certificate == a.Certificate &&
		a.MacOSNotarizedFrom >= id.MacCutoff
}

func (s *Server) publishedLogo(w http.ResponseWriter, r *http.Request) {
	s.published.mu.RLock()
	logo := s.published.logos[r.URL.Path]
	s.published.mu.RUnlock()
	if logo == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(logo)
}

func (s *Server) publishCatalog(w http.ResponseWriter, r *http.Request) {
	publisher := r.PathValue("publisher")
	p, ok := s.cfg.CatalogPublishers[publisher]
	if !ok || !hmac.Equal([]byte(hash(bearer(r))), []byte(hash(p.Key))) {
		fail(w, 401, "unauthorized")
		return
	}
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != "application/json" {
		fail(w, 415, "catalog requires JSON")
		return
	}
	var publication catalogPublication
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&publication) != nil || d.Decode(&struct{}{}) != io.EOF ||
		publication.SchemaVersion != 1 || publication.Revision < 1 ||
		publication.Apps == nil || len(publication.Apps) > maxCatalogApps {
		fail(w, 400, "invalid catalog publication")
		return
	}
	ids := map[string]bool{}
	for i := range publication.Apps {
		entry := &publication.Apps[i]
		if err := validatePublishedEntry(entry); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if ids[entry.ID] {
			fail(w, 400, "duplicate app ID")
			return
		}
		ids[entry.ID] = true
		if !p.allows(entry.Repo) || entry.ID == "appstore" {
			fail(w, 403, "publisher is not authorized for this app repository or ID")
			return
		}
	}
	sort.Slice(publication.Apps, func(i, j int) bool { return publication.Apps[i].ID < publication.Apps[j].ID })

	status, err := s.replaceCatalog(r.Context(), publisher, p, publication)
	if status == 500 {
		s.internal(w, err)
		return
	}
	if err != nil {
		fail(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validatePublishedEntry(entry *catalogEntry) error {
	if entry.ManualGlobal || entry.LogoURL != "" || entry.Release != nil || entry.DesktopRelease != nil || entry.Notifications != nil {
		return errors.New("catalog entries cannot set Store-managed fields")
	}
	if err := validateApp(&entry.App); err != nil {
		return err
	}
	if err := validateListing(entry.App); err != nil {
		return err
	}
	if entry.Package != "" && entry.Certificate == "" {
		return errors.New("catalog APKs require a release certificate fingerprint")
	}
	if len(entry.LogoPNG) == 0 || len(entry.LogoPNG) > maxCatalogLogo {
		return errors.New("catalog logo must be a PNG no larger than 64 KiB")
	}
	config, err := png.DecodeConfig(bytes.NewReader(entry.LogoPNG))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 512 || config.Height > 512 {
		return errors.New("catalog logo must be a PNG up to 512 × 512 pixels")
	}
	if _, err := png.Decode(bytes.NewReader(entry.LogoPNG)); err != nil {
		return errors.New("invalid catalog PNG")
	}
	return nil
}

func (s *Server) replaceCatalog(ctx context.Context, publisher string, p CatalogPublisher, publication catalogPublication) (int, error) {
	data, err := json.Marshal(publication)
	if err != nil {
		return 500, err
	}
	c := &s.published
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous, ok := c.publications[publisher]; ok && publication.Revision <= previous.Revision {
		stored, err := json.Marshal(previous)
		if err != nil {
			return 500, err
		}
		if bytes.Equal(data, stored) {
			return 204, nil
		}
		return 409, errors.New("catalog revision must increase when its contents change")
	}
	if err := s.checkCatalogIdentities(publisher, p, publication.Apps); err != nil {
		return 409, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 500, err
	}
	defer tx.Rollback()
	for _, a := range publication.Apps {
		id, err := json.Marshal(identityFor(publisher, a.App))
		if err != nil {
			return 500, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO catalog_identities(app,identity) VALUES(?,?)", a.ID, id); err != nil {
			return 500, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO catalog_publications(publisher,publication) VALUES(?,?)", publisher, data); err != nil {
		return 500, err
	}
	if err := tx.Commit(); err != nil {
		return 500, err
	}
	c.publications[publisher] = publication
	for _, a := range publication.Apps {
		c.identities[a.ID] = identityFor(publisher, a.App)
		c.pending[a.ID] = true
	}
	s.rebuildCatalog()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return 204, nil
}

func (s *Server) checkCatalogIdentities(publisher string, p CatalogPublisher, entries []catalogEntry) error {
	packages := map[string]string{}
	desktopRepos := map[string]string{}
	for _, a := range s.published.apps {
		if a.Desktop && s.published.identities[a.ID].Publisher != publisher {
			desktopRepos[strings.ToLower(a.Repo)] = a.ID
		}
	}
	for id, identity := range s.published.identities {
		packages[identity.Package] = id
	}
	for _, a := range s.cfg.Apps {
		packages[a.Package] = a.ID
		if a.Desktop {
			desktopRepos[strings.ToLower(a.Repo)] = a.ID
		}
	}
	for _, entry := range entries {
		a := entry.App
		for _, original := range s.cfg.Apps {
			if a.ID == original.ID && (!slices.Contains(p.Adopt, a.ID) || !identityFor(publisher, original).matches(publisher, a)) {
				return fmt.Errorf("app %s conflicts with a bundled listing or its release identity", a.ID)
			}
		}
		if existing, ok := s.published.identities[a.ID]; ok && !existing.matches(publisher, a) {
			return fmt.Errorf("app %s conflicts with a reserved release identity", a.ID)
		}
		if a.Package != "" {
			if other := packages[a.Package]; other != "" && other != a.ID {
				return fmt.Errorf("Android package for %s is reserved by %s", a.ID, other)
			}
			packages[a.Package] = a.ID
		}
		if a.Desktop {
			repo := strings.ToLower(a.Repo)
			if other := desktopRepos[repo]; other != "" && other != a.ID {
				return fmt.Errorf("desktop release repository for %s is used by %s", a.ID, other)
			}
			desktopRepos[repo] = a.ID
		}
	}
	return nil
}
