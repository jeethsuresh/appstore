package store

import (
	"encoding/json"
	"fmt"
	"os"
	"private-store/internal/gravitas"
	"regexp"
	"strings"
)

type App struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Package        string           `json:"packageName"`
	Repo           string           `json:"repository"`
	Asset          string           `json:"assetName"`
	Certificate    string           `json:"certificateSha256"`
	Description    string           `json:"description"`
	Desktop        bool             `json:"desktop,omitempty"`
	DesktopRelease *DesktopRelease  `json:"desktopRelease,omitempty"`
	Release        *Release         `json:"release,omitempty"`
	Notifications  *NotificationApp `json:"notifications,omitempty"`
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
}

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
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`).MatchString(a.ID) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_][A-Za-z0-9_.-]*$`).MatchString(a.Repo) || (a.Package == "" && !a.Desktop) || (a.Package != "" && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`).MatchString(a.Package)) || a.Name == "" || strings.ContainsAny(a.Asset, "/\\") || (a.Package != "" && !strings.HasSuffix(a.Asset, ".apk")) || (a.Package == "" && (a.Asset != "" || a.Certificate != "" || a.Notifications != nil)) || (a.Certificate != "" && !hexDigest.MatchString(a.Certificate)) || seen[a.ID] || (a.Package != "" && packages[a.Package]) {
			return nil, fmt.Errorf("invalid or duplicate app configuration: %s", a.ID)
		}
		seen[a.ID] = true
		packages[a.Package] = true
	}
	return apps, nil
}
