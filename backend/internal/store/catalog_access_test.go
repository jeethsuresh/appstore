package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"private-store/internal/gravitas"
)

func TestPersonalAndroidCatalogDomainMembershipAndGlobalOverride(t *testing.T) {
	hidden := App{ID: "hidden", catalogPublisher: "calendar", catalogHost: "cal.gravitas.cool", HideFromUnregisteredUsersOnDomain: true}
	s, err := New(Config{
		DataDir: t.TempDir(), AdminToken: strings.Repeat("a", 64), WebhookSecret: strings.Repeat("b", 64), PersonalCatalog: true,
		Apps: []App{
			{ID: "appstore"}, {ID: "launcher", ManualGlobal: true}, hidden,
			{ID: "local", catalogHost: "cal.gravitas.cool"},
			{ID: "unrelated", catalogHost: "notgravitas.cool"},
			{ID: "global", catalogHost: "remote.example"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO catalog_memberships VALUES ('https://gravitas.cool','alice','calendar','hidden');
		INSERT INTO catalog_memberships VALUES ('https://gravitas.cool','bob','other','hidden');
		INSERT INTO catalog_global_apps VALUES ('global');`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ issuer, domain, subject, want string }{
		{"https://gravitas.cool", "gravitas.cool", "alice", "appstore launcher hidden local global"},
		{"https://gravitas.cool", "gravitas.cool", "bob", "appstore launcher local global"},
		{"https://other.example", "other.example", "alice", "appstore launcher global"},
		{"https://remote.example", "remote.example", "carol", "appstore launcher global"},
		{"", "", "", ""},
	} {
		identity := gravitas.Identity{Issuer: tc.issuer, Subject: tc.subject, Domain: tc.domain}
		apps, err := s.visibleAndroidApps(context.WithValue(context.Background(), catalogIdentityKey{}, identity))
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, app := range apps {
			ids = append(ids, app.ID)
		}
		if got := strings.Join(ids, " "); got != tc.want {
			t.Errorf("%+v: got %q", tc, got)
		}
	}
}

func TestPublisherRegistrationUsesIssuerSubjectAndOriginFromProof(t *testing.T) {
	const origin = "https://cal.gravitas.cool"
	const issuer = "https://iam.example"
	var wrongOrigin atomic.Bool
	iam := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint := "https://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/iam-configuration":
			parsed, _ := url.Parse(endpoint)
			json.NewEncoder(w).Encode(map[string]string{
				"issuer": endpoint, "identity_domain": parsed.Hostname(),
				"browser_initiation_endpoint": endpoint + "/start", "token_endpoint": endpoint + "/token",
				"userinfo_endpoint": endpoint + "/me", "session_check_endpoint": endpoint + "/status",
			})
		case "/status":
			proofOrigin := origin
			if wrongOrigin.Load() {
				proofOrigin = issuer
			}
			json.NewEncoder(w).Encode(map[string]any{
				"active": true, "iss": endpoint, "sub": "alice", "app_origin": proofOrigin,
				"session_id": "session", "exp": time.Now().Add(time.Hour).Unix(),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer iam.Close()
	endpoint := iam.URL
	client, err := gravitas.New(endpoint, "https://apps.gravitas.cool", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = iam.Client()
	key := strings.Repeat("k", 64)
	s, err := New(Config{DataDir: t.TempDir(), AdminToken: strings.Repeat("a", 64), WebhookSecret: strings.Repeat("b", 64),
		CatalogPublishers: map[string]CatalogPublisher{"calendar": {Key: key, Origin: origin, Repositories: []string{"example/calendar"}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Production startup verifies a bootstrap APK. This route test attaches
	// the live test IAM client after initializing an independent Store.
	s.cfg.Federation = client
	entry := catalogEntry{App: App{ID: "calendar", Name: "Calendar", Description: "Keep dates.", Repo: "example/calendar", Desktop: true}, LogoPNG: catalogTestLogo(t)}
	putCatalog(t, s, "calendar", key, catalogPublication{SchemaVersion: 1, Revision: 1, Apps: []catalogEntry{entry}}, 204)
	body, _ := json.Marshal(map[string]any{"issuer": endpoint, "sessionProof": strings.Repeat("x", 43), "apps": []string{"calendar"}})
	w := request(s, "PUT", "/v1/catalog/calendar/accounts", key, string(body))
	if w.Code != 204 {
		t.Fatalf("registration returned %d: %s", w.Code, w.Body.String())
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM catalog_memberships WHERE issuer=? AND subject='alice' AND publisher='calendar' AND app='calendar'", endpoint).Scan(&count); err != nil || count != 1 {
		t.Fatalf("registration did not record the IAM subject: %d %v", count, err)
	}
	wrongOrigin.Store(true)
	w = request(s, "PUT", "/v1/catalog/calendar/accounts", key, string(body))
	if w.Code != 401 {
		t.Fatalf("wrong service origin returned %d", w.Code)
	}
}
