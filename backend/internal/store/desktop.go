package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"private-store/internal/gravitas"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxDesktopAsset int64 = 2 << 30
const maxDesktopManifest = 64 << 10

// A single convention-based manifest advertises only the targets actually built.
// Desktop version codes are independent of Android's version codes.
type DesktopArtifact struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	File     string `json:"file"`
	Entry    string `json:"entry"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
type DesktopRelease struct {
	SchemaVersion int               `json:"schemaVersion"`
	Version       string            `json:"version"`
	VersionCode   int64             `json:"versionCode"`
	Changes       []string          `json:"changes"`
	Artifacts     []DesktopArtifact `json:"artifacts"`
	Tag           string            `json:"tag,omitempty"`
}

func desktopTrust(a App) string { return hash(a.Repo + "\x00desktop-release-v1") }
func validTarget(platform, arch string) bool {
	return (platform == "macos" || platform == "windows" || platform == "linux") && (arch == "x64" || arch == "arm64")
}

var desktopVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var desktopFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,180}$`)

func parseDesktopManifest(data []byte, tag string) (*DesktopRelease, error) {
	var rel DesktopRelease
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if len(data) > maxDesktopManifest || d.Decode(&rel) != nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid desktop release manifest")
	}
	if rel.SchemaVersion != 1 || !desktopVersion.MatchString(rel.Version) || rel.VersionCode < 1 || rel.VersionCode > 9007199254740991 || (tag != "v"+rel.Version && tag != "desktop-v"+rel.Version) || rel.Tag != "" {
		return nil, errors.New("desktop schema, version or tag mismatch")
	}
	// Reuse the exact plain-text changelog validation used for mobile releases.
	notes, _ := json.Marshal(releaseChangelog{SchemaVersion: 1, Version: rel.Version, VersionCode: rel.VersionCode, Changes: rel.Changes})
	if _, err := changelogNotes(notes, APKInfo{VersionName: rel.Version, VersionCode: rel.VersionCode}); err != nil {
		return nil, err
	}
	if len(rel.Artifacts) == 0 || len(rel.Artifacts) > 6 {
		return nil, errors.New("desktop release requires 1–6 targets")
	}
	seen := map[string]bool{}
	for _, a := range rel.Artifacts {
		key := a.Platform + "/" + a.Arch
		if !validTarget(a.Platform, a.Arch) || seen[key] || !desktopFilename.MatchString(a.File) || !hexDigest.MatchString(a.SHA256) || a.Size <= 0 || a.Size > maxDesktopAsset || !desktopFilename.MatchString(a.Entry) {
			return nil, errors.New("invalid or duplicate desktop artifact")
		}
		if (a.Platform == "macos" && (!(strings.HasSuffix(a.File, ".zip") || strings.HasSuffix(a.File, ".dmg")) || !strings.HasSuffix(a.Entry, ".app"))) || (a.Platform == "windows" && (!(strings.HasSuffix(a.File, ".zip") || strings.HasSuffix(a.File, ".exe")) || !strings.HasSuffix(a.Entry, ".exe"))) || (a.Platform == "linux" && (!strings.HasSuffix(a.File, ".AppImage") || a.Entry != a.File)) {
			return nil, errors.New("unsupported desktop package format")
		}
		seen[key] = true
	}
	rel.Tag = tag
	return &rel, nil
}

// /latest can point at the other platform's release. Search published releases
// by their assets so a desktop release can never displace an Android update.
func (s *Server) findRelease(ctx context.Context, a App, tag string, matches func(githubRelease) bool) (githubRelease, error) {
	path := "/repos/" + a.Repo + "/releases/latest"
	if tag != "" {
		path = "/repos/" + a.Repo + "/releases/tags/" + url.PathEscape(tag)
	}
	res, err := s.github(ctx, path, "application/vnd.github+json")
	if err != nil {
		return githubRelease{}, err
	}
	var rel githubRelease
	err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&rel)
	res.Body.Close()
	if err != nil {
		return rel, err
	}
	if !rel.Draft && !rel.Prerelease && matches(rel) {
		return rel, nil
	}
	if tag != "" {
		return rel, errors.New("stable release is missing this platform's assets")
	}
	for page := 1; page <= 4; page++ {
		res, err = s.github(ctx, fmt.Sprintf("/repos/%s/releases?per_page=50&page=%d", a.Repo, page), "application/vnd.github+json")
		if err != nil {
			return rel, err
		}
		var releases []githubRelease
		err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&releases)
		res.Body.Close()
		if err != nil {
			return rel, err
		}
		for _, candidate := range releases {
			if !candidate.Draft && !candidate.Prerelease && matches(candidate) {
				return candidate, nil
			}
		}
		if len(releases) < 50 {
			break
		}
	}
	return rel, errors.New("no published release has this platform's assets")
}

func (s *Server) initDesktop() error {
	if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "desktop"), 0700); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS desktop_releases(app_id TEXT NOT NULL,version_code INTEGER NOT NULL,manifest BLOB NOT NULL,config_hash TEXT NOT NULL,PRIMARY KEY(app_id,version_code))`)
	if err != nil {
		return err
	}
	s.mux.HandleFunc("GET /v1/desktop/apps", s.desktopCatalog)
	s.mux.Handle("GET /v1/desktop/releases/{app}/{version}/{platform}/{arch}", s.device(http.HandlerFunc(s.desktopDownload)))
	s.mux.HandleFunc("GET /desktop/connect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		_, _ = io.WriteString(w, "<!doctype html><html lang=en><meta charset=utf-8><title>Private Store</title><p>Return to Private Store to finish connecting this desktop.</p></html>")
	})
	s.mux.HandleFunc("POST /v1/auth/desktop/enroll", s.desktopEnroll)
	s.mux.HandleFunc("GET /download/desktop/{platform}/{arch}", s.desktopBootstrap)
	return nil
}

func (s *Server) latestDesktop(ctx context.Context, a App) (*DesktopRelease, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT manifest FROM desktop_releases WHERE app_id=? AND config_hash=? ORDER BY version_code DESC LIMIT 1", a.ID, desktopTrust(a)).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rel DesktopRelease
	err = json.Unmarshal(data, &rel)
	return &rel, err
}
func (s *Server) syncDesktop(ctx context.Context, a App, tag string) error {
	rel, err := s.findRelease(ctx, a, tag, func(r githubRelease) bool {
		for _, v := range r.Assets {
			if v.Name == "desktop-release.json" || legacyDesktopTarget(v.Name) != "" {
				return true
			}
		}
		return false
	})
	if err != nil {
		return err
	}
	var manifest []byte
	hasManifest := false
	for _, asset := range rel.Assets {
		if asset.Name == "desktop-release.json" {
			hasManifest = true
		}
	}
	if hasManifest {
		manifest, err = s.desktopAsset(ctx, a, rel, "desktop-release.json", maxDesktopManifest)
	} else {
		manifest, err = legacyDesktopManifest(a, rel)
	}
	if err != nil {
		return err
	}
	next, err := parseDesktopManifest(manifest, rel.Tag)
	if err != nil {
		return err
	}
	current, err := s.latestDesktop(ctx, a)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(next)
	if current != nil && next.VersionCode <= current.VersionCode {
		previous, _ := json.Marshal(current)
		if next.VersionCode != current.VersionCode || !bytes.Equal(data, previous) {
			return errors.New("desktop releases are immutable and cannot decrease version code")
		}
	}
	for _, artifact := range next.Artifacts {
		dest := filepath.Join(s.cfg.DataDir, "desktop", artifact.SHA256)
		// The content-addressed cache is private to the backend; repair missing files.
		if st, e := os.Stat(dest); e == nil && st.Mode().IsRegular() && st.Size() == artifact.Size {
			continue
		}
		var assetID int64
		for _, asset := range rel.Assets {
			if asset.Name == artifact.File {
				if assetID != 0 || asset.Size != artifact.Size || (asset.Digest != "" && asset.Digest != "sha256:"+artifact.SHA256) {
					return errors.New("desktop asset metadata mismatch")
				}
				assetID = asset.ID
			}
		}
		if assetID <= 0 {
			return fmt.Errorf("missing desktop artifact %s", artifact.File)
		}
		res, e := s.github(ctx, fmt.Sprintf("/repos/%s/releases/assets/%d", a.Repo, assetID), "application/octet-stream")
		if e != nil {
			return e
		}
		f, e := os.CreateTemp(filepath.Join(s.cfg.DataDir, "desktop"), "ingest-*")
		if e != nil {
			res.Body.Close()
			return e
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, artifact.Size+1))
		res.Body.Close()
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil || n != artifact.Size || hex.EncodeToString(h.Sum(nil)) != artifact.SHA256 {
			os.Remove(f.Name())
			return errors.New("desktop download failed size/checksum verification")
		}
		if e = os.Rename(f.Name(), dest); e != nil {
			os.Remove(f.Name())
			return e
		}
	}
	// Publish the complete multi-platform release atomically, after every target verifies.
	_, err = s.db.ExecContext(ctx, "INSERT INTO desktop_releases(app_id,version_code,manifest,config_hash) VALUES(?,?,?,?) ON CONFLICT(app_id,version_code) DO NOTHING", a.ID, next.VersionCode, data, desktopTrust(a))
	return err
}
func (s *Server) desktopAsset(ctx context.Context, a App, rel githubRelease, name string, limit int64) ([]byte, error) {
	var id, size int64
	var digest string
	for _, asset := range rel.Assets {
		if asset.Name == name {
			if id != 0 || asset.ID <= 0 || asset.Size <= 0 || asset.Size > limit {
				return nil, errors.New("invalid desktop manifest asset")
			}
			id, size, digest = asset.ID, asset.Size, asset.Digest
		}
	}
	if id == 0 {
		return nil, errors.New("missing desktop manifest")
	}
	return s.verifiedMetadata(ctx, a, id, size, digest, limit)
}
func (s *Server) desktopCatalog(w http.ResponseWriter, r *http.Request) {
	platform, arch := r.URL.Query().Get("platform"), r.URL.Query().Get("arch")
	if !validTarget(platform, arch) {
		fail(w, 400, "platform (macos/windows/linux) and arch (x64/arm64) are required")
		return
	}
	apps := []App{}
	for _, a := range s.cfg.Apps {
		if a.Desktop {
			rel, err := s.latestDesktop(r.Context(), a)
			if err != nil {
				s.internal(w, err)
				return
			}
			if rel == nil {
				continue
			}
			selected := *rel
			selected.Artifacts = nil
			for _, artifact := range rel.Artifacts {
				if artifact.Platform == platform && artifact.Arch == arch {
					selected.Artifacts = append(selected.Artifacts, artifact)
				}
			}
			if len(selected.Artifacts) == 0 {
				continue
			}
			a.Release = nil
			a.Notifications = nil
			a.DesktopRelease = &selected
			apps = append(apps, a)
		}
	}
	reply(w, 200, map[string]any{"apps": apps})
}
func (s *Server) desktopDownload(w http.ResponseWriter, r *http.Request) {
	for _, a := range s.cfg.Apps {
		if a.ID == r.PathValue("app") && a.Desktop {
			var data []byte
			if s.db.QueryRowContext(r.Context(), "SELECT manifest FROM desktop_releases WHERE app_id=? AND version_code=? AND config_hash=?", a.ID, r.PathValue("version"), desktopTrust(a)).Scan(&data) != nil {
				break
			}
			var rel DesktopRelease
			if json.Unmarshal(data, &rel) != nil {
				break
			}
			for _, artifact := range rel.Artifacts {
				if artifact.Platform == r.PathValue("platform") && artifact.Arch == r.PathValue("arch") {
					s.serveDesktopArtifact(w, r, artifact)
					return
				}
			}
		}
	}
	fail(w, 404, "desktop release not found")
}
func (s *Server) serveDesktopArtifact(w http.ResponseWriter, r *http.Request, a DesktopArtifact) {
	f, err := os.Open(filepath.Join(s.cfg.DataDir, "desktop", a.SHA256))
	if err != nil {
		fail(w, 503, "download temporarily unavailable")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() != a.Size {
		fail(w, 503, "download temporarily unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, a.File))
	http.ServeContent(w, r, a.File, st.ModTime(), f)
}
func (s *Server) desktopBootstrap(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "sign-in is not configured")
		return
	}
	if _, err := s.cfg.Federation.Session(r); err != nil {
		s.webErrorAuth(w, err)
		return
	}
	for _, a := range s.cfg.Apps {
		if a.ID == "appstore" && a.Desktop {
			rel, err := s.latestDesktop(r.Context(), a)
			if err != nil {
				s.internal(w, err)
				return
			}
			if rel == nil {
				break
			}
			for _, artifact := range rel.Artifacts {
				if artifact.Platform == r.PathValue("platform") && artifact.Arch == r.PathValue("arch") {
					s.serveDesktopArtifact(w, r, artifact)
					return
				}
			}
		}
	}
	fail(w, 404, "no Private Store release for this desktop target")
}
func (s *Server) webErrorAuth(w http.ResponseWriter, err error) {
	if errors.Is(err, gravitas.ErrUnavailable) {
		fail(w, 503, "Gravitas is temporarily unavailable")
	} else {
		fail(w, 401, "Sign in with Gravitas again")
	}
}
func (s *Server) desktopEnroll(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Federation == nil {
		fail(w, 503, "sign-in is not configured")
		return
	}
	if !s.cfg.Federation.ValidMutation(r) {
		fail(w, 403, "invalid enrollment request")
		return
	}
	identity, err := s.cfg.Federation.Session(r)
	if err != nil {
		s.webErrorAuth(w, err)
		return
	}
	var in struct {
		Name string `json:"deviceName"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 {
		fail(w, 400, "a device name is required")
		return
	}
	proof, err := s.cfg.Federation.SealDevice(identity)
	if err != nil {
		s.internal(w, err)
		return
	}
	id, credential := token(), token()
	expires := min(time.Now().Add(365*24*time.Hour).Unix(), identity.SessionExpires)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO devices(id,name,token_hash,expires,created) VALUES(?,?,?,?,?)", id, in.Name, hash(credential), expires, time.Now().UTC().Format(time.RFC3339)); err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO device_identities(device_id,issuer,subject,username) VALUES(?,?,?,?)", id, identity.Issuer, identity.Subject, identity.Username)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO device_sessions(device_id,proof) VALUES(?,?)", id, proof)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 201, map[string]any{"deviceId": id, "token": credential, "expiresAt": expires, "account": identity.Username})
}

// Existing Electron releases need only desktop:true. Native installer formats
// use the established Electron productName (the catalog name) as their entry.
func legacyDesktopTarget(file string) string {
	name := strings.ToLower(file)
	arch := ""
	if strings.Contains(name, "arm64") || strings.Contains(name, "aarch64") {
		arch = "arm64"
	} else if strings.Contains(name, "x64") || strings.Contains(name, "x86_64") || strings.Contains(name, "amd64") {
		arch = "x64"
	}
	if arch == "" {
		return ""
	}
	if strings.HasSuffix(file, ".AppImage") {
		return "linux/" + arch
	}
	if strings.Contains(name, "-mac-") && strings.HasSuffix(name, ".dmg") {
		return "macos/" + arch
	}
	if strings.Contains(name, "-win-") && strings.HasSuffix(name, ".exe") {
		return "windows/" + arch
	}
	return ""
}
func legacyDesktopManifest(a App, rel githubRelease) ([]byte, error) {
	version := strings.TrimPrefix(rel.Tag, "v")
	if !desktopVersion.MatchString(version) {
		return nil, errors.New("legacy desktop release needs a stable vX.Y.Z tag")
	}
	parts := strings.Split(version, ".")
	nums := []int64{}
	for _, part := range parts {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil || n >= 1000 {
			return nil, errors.New("legacy version component must be below 1000")
		}
		nums = append(nums, n)
	}
	result := DesktopRelease{SchemaVersion: 1, Version: version, VersionCode: nums[0]*1000000 + nums[1]*1000 + nums[2], Changes: []string{"Published desktop release " + version + ". See the app repository for full release history."}}
	if result.VersionCode == 0 {
		return nil, errors.New("legacy version must be newer than 0.0.0")
	}
	if notes := strings.TrimSpace(rel.Body); notes != "" && len(notes) <= 2000 {
		result.Changes = nil
		for _, line := range strings.FieldsFunc(notes, func(r rune) bool { return r < ' ' }) {
			if strings.TrimSpace(line) != "" {
				result.Changes = append(result.Changes, strings.TrimSpace(line))
			}
		}
		if len(result.Changes) > 50 {
			result.Changes = result.Changes[:50]
		}
	}
	for _, asset := range rel.Assets {
		target := legacyDesktopTarget(asset.Name)
		if target == "" {
			continue
		}
		digest := strings.TrimPrefix(asset.Digest, "sha256:")
		if !hexDigest.MatchString(digest) {
			return nil, errors.New("legacy desktop asset requires GitHub's SHA-256 digest or a desktop-release.json manifest")
		}
		parts := strings.Split(target, "/")
		entry := asset.Name
		if parts[0] == "macos" {
			entry = a.Name + ".app"
		}
		if parts[0] == "windows" {
			entry = a.Name + ".exe"
		}
		result.Artifacts = append(result.Artifacts, DesktopArtifact{Platform: parts[0], Arch: parts[1], File: asset.Name, Entry: entry, SHA256: digest, Size: asset.Size})
	}
	return json.Marshal(result)
}
