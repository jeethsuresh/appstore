package store

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"private-store/internal/gravitas"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type App struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	Package            string           `json:"packageName"`
	Repo               string           `json:"repository"`
	Asset              string           `json:"assetName"`
	ReleaseTagPrefix   string           `json:"releaseTagPrefix,omitempty"`
	Certificate        string           `json:"certificateSha256"`
	Description        string           `json:"description"`
	LogoURL            string           `json:"logoUrl"`
	Website            string           `json:"website,omitempty"`
	IOS                string           `json:"ios,omitempty"`
	IOSURL             string           `json:"iosUrl,omitempty"`
	Desktop            bool             `json:"desktop,omitempty"`
	MacOSNotarizedFrom int64            `json:"macOSNotarizedFrom,omitempty"`
	DesktopRelease     *DesktopRelease  `json:"desktopRelease,omitempty"`
	Release            *Release         `json:"release,omitempty"`
	Notifications      *NotificationApp `json:"notifications,omitempty"`
}
type Release struct {
	ConfigHash  string `json:"-"`
	AssetID     int64  `json:"-"`
	ID          int64  `json:"id"`
	Tag         string `json:"tag"`
	VersionCode int64  `json:"versionCode"`
	VersionName string `json:"versionName"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	MinSDK      int    `json:"minSdk"`
	Notes       string `json:"notes"`
	Created     string `json:"createdAt"`
}
type Config struct {
	ProtocolRelayKey                                                 string
	DataDir, AdminToken, WebhookSecret, GitHubToken, AAPT, APKSigner string
	Apps                                                             []App
	Federation                                                       *gravitas.Client
	ClientAPK                                                        string
	NotificationURL                                                  string
	NotificationGateway                                              string
	NotificationKeys                                                 map[string]string
	APNSConfig                                                       string
}

var logoPath = regexp.MustCompile(`^/assets/[a-z0-9-]+\.(svg|png)$`)

func validateBranding(app App) error {
	if strings.TrimSpace(app.Name) != app.Name || utf8.RuneCountInString(app.Name) > 80 ||
		!strings.ContainsFunc(app.Name, unicode.IsLetter) || strings.ContainsFunc(app.Name, unicode.IsControl) ||
		strings.Contains(app.Name, "://") || strings.EqualFold(app.Name, app.ID+".gravitas.cool") {
		return fmt.Errorf("%s: name must be a human-readable app name", app.ID)
	}
	if strings.TrimSpace(app.Description) == "" || strings.TrimSpace(app.Description) != app.Description ||
		utf8.RuneCountInString(app.Description) > 500 || strings.ContainsFunc(app.Description, unicode.IsControl) {
		return fmt.Errorf("%s: description must contain 1–500 characters of plain text", app.ID)
	}
	if !logoPath.MatchString(app.LogoURL) || (app.LogoURL == "/assets/icon.svg" && app.ID != "appstore") {
		return fmt.Errorf("%s: logoUrl must identify an app-specific bundled SVG or PNG", app.ID)
	}
	if icon, err := webFiles.ReadFile("web/" + strings.TrimPrefix(app.LogoURL, "/assets/")); err != nil || len(icon) == 0 {
		return fmt.Errorf("%s: logo asset does not exist: %s", app.ID, app.LogoURL)
	}
	if app.Website != "" {
		u, err := url.Parse(app.Website)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
			u.Fragment != "" || strings.ContainsAny(app.Website, "\\#") {
			return fmt.Errorf("%s: website must be an HTTPS URL without credentials or a fragment", app.ID)
		}
	}
	return nil
}

func validateIOS(app App) error {
	switch app.IOS {
	case "":
		if app.IOSURL == "" {
			return nil
		}
	case "testflight", "app-store":
		if app.IOSURL == "" && app.IOS == "testflight" {
			return nil
		}
		u, err := url.Parse(app.IOSURL)
		if err == nil && u.Scheme == "https" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
			((app.IOS == "testflight" && u.Host == "testflight.apple.com" && testFlightPath.MatchString(u.Path)) ||
				(app.IOS == "app-store" && u.Host == "apps.apple.com" && appStorePath.MatchString(u.Path))) {
			return nil
		}
	}
	return fmt.Errorf("%s: ios must specify testflight or app-store with a matching Apple listing URL when supplied; app-store requires a URL", app.ID)
}

var testFlightPath = regexp.MustCompile(`^/join/[A-Za-z0-9]+$`)
var appStorePath = regexp.MustCompile(`^/(?:[a-z]{2}/)?app/(?:[A-Za-z0-9-]+/)?id[0-9]+$`)

var hexDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func LoadApps(path string) ([]App, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var apps []App
	if err = json.Unmarshal(b, &apps); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	packages := map[string]bool{}
	for i := range apps {
		a := &apps[i]
		a.Certificate = strings.ToLower(strings.ReplaceAll(a.Certificate, ":", ""))
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`).MatchString(a.ID) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_][A-Za-z0-9_.-]*$`).MatchString(a.Repo) || (a.Package == "" && !a.Desktop && a.IOS == "") || (a.Package != "" && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`).MatchString(a.Package)) || (a.ReleaseTagPrefix != "" && (a.Package == "" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`).MatchString(a.ReleaseTagPrefix))) || strings.ContainsAny(a.Asset, "/\\") || (a.Package != "" && !strings.HasSuffix(a.Asset, ".apk")) || (a.Package == "" && (a.Asset != "" || a.Certificate != "" || a.Notifications != nil)) || (a.Certificate != "" && !hexDigest.MatchString(a.Certificate)) || seen[a.ID] || (a.Package != "" && packages[a.Package]) {
			return nil, fmt.Errorf("invalid or duplicate app configuration: %s", a.ID)
		}
		if a.MacOSNotarizedFrom < 0 || a.MacOSNotarizedFrom > 9007199254740991 || (!a.Desktop && a.MacOSNotarizedFrom != 0) {
			return nil, fmt.Errorf("invalid macOS notarization cutoff: %s", a.ID)
		}
		if err := validateBranding(*a); err != nil {
			return nil, err
		}
		if err := validateIOS(*a); err != nil {
			return nil, err
		}
		seen[a.ID] = true
		packages[a.Package] = true
	}
	return apps, nil
}
