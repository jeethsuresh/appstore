package store

import (
	"archive/zip"
	"bytes"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxPublisherArchive = 192 << 20

// Only bundled private desktop apps whose operators configured an individual
// verifier use direct publishing. Each verifier is confined to its app ID.
func DesktopPublishers(apps []App, getenv func(string) string) (map[string]string, error) {
	result := make(map[string]string)
	seen := make(map[string]bool)
	for _, app := range apps {
		if !app.Desktop || !app.DesktopRequiresSignIn {
			continue
		}
		setting := strings.ToUpper(strings.ReplaceAll(app.ID, "-", "_")) + "_PUBLISH_TOKEN_SHA256"
		verifier := getenv(setting)
		if verifier == "" {
			continue
		}
		if !hexDigest.MatchString(verifier) || seen[verifier] {
			return nil, errors.New("each desktop publisher needs its own SHA-256 verifier")
		}
		result[app.ID] = verifier
		seen[verifier] = true
	}
	return result, nil
}

func universalMacManifest(data []byte, app App) (*DesktopRelease, error) {
	var header struct{ Version string }
	if json.Unmarshal(data, &header) != nil {
		return nil, errors.New("invalid desktop manifest")
	}
	rel, err := parseDesktopManifest(data, "desktop-v"+header.Version)
	if err != nil {
		return nil, err
	}
	var code int64
	for _, component := range strings.Split(rel.Version, ".") {
		value, err := strconv.ParseInt(component, 10, 64)
		if err != nil || value > 999 {
			return nil, errors.New("invalid native game version")
		}
		code = code*1000 + value
	}
	if code != rel.VersionCode || !app.allowsDesktopArtifact("macos", code) || len(rel.Artifacts) != 2 {
		return nil, errors.New("expected the notarized universal release for this version")
	}
	first, second := rel.Artifacts[0], rel.Artifacts[1]
	if first.Arch == "x64" {
		first, second = second, first
	}
	if first.Platform != "macos" || first.Arch != "arm64" || second.Arch != "x64" ||
		first.File != strings.ReplaceAll(app.Name, " ", "")+"-"+rel.Version+"-mac-universal.zip" || first.Entry != app.Name+".app" ||
		first.Size > maxPublisherArchive {
		return nil, errors.New("invalid universal macOS artifact")
	}
	rel.Artifacts[0], rel.Artifacts[1] = first, second
	second.Arch = first.Arch
	if first != second {
		return nil, errors.New("both architectures must share the same universal archive")
	}
	rel.Tag = app.DesktopReleaseTagPrefix + rel.Tag
	return rel, nil
}

// The protected signing workflow verifies Developer ID, notarization and an
// installed match. Bazaar independently checks the transferred archive and its
// required bundle entries before making it discoverable.
func validGameArchive(path, name string) bool {
	z, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer z.Close()
	for _, file := range []string{"Info.plist", "MacOS/" + name, "Resources/" + name + ".pck", "_CodeSignature/CodeResources"} {
		f, err := z.Open(name + ".app/Contents/" + file)
		if err != nil {
			return false
		}
		var prefix [4]byte
		_, err = io.ReadFull(f, prefix[:])
		f.Close()
		if err != nil || (file == "Resources/"+name+".pck" && string(prefix[:]) != "GDPC") {
			return false
		}
	}
	return true
}

func (s *Server) publishDesktop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("app")
	key := bearer(r)
	if !hexDigest.MatchString(key) || !hmac.Equal([]byte(hash(key)), []byte(s.cfg.DesktopPublisherHashes[id])) {
		fail(w, 401, "publisher authentication required")
		return
	}
	var app App
	for _, candidate := range s.catalogApps() {
		if candidate.ID == id && candidate.Desktop && candidate.DesktopRequiresSignIn {
			app = candidate
			break
		}
	}
	if app.ID == "" {
		fail(w, 404, "publisher unavailable")
		return
	}
	const maxRequest = maxPublisherArchive + maxDesktopManifest + (64 << 10)
	if r.ContentLength > maxRequest {
		fail(w, 413, "release exceeds upload limit")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequest)
	// Normal API requests use a short read timeout. This authenticated stream
	// may transfer the entire universal archive over a slower connection.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Minute))
	parts, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "expected a multipart release")
		return
	}
	manifest, err := parts.NextPart()
	if err != nil || manifest.FormName() != "manifest" {
		fail(w, 400, "send the desktop manifest first")
		return
	}
	data, err := io.ReadAll(io.LimitReader(manifest, maxDesktopManifest+1))
	if err != nil || len(data) > maxDesktopManifest {
		fail(w, 400, "invalid manifest size")
		return
	}
	rel, err := universalMacManifest(data, app)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	artifact := rel.Artifacts[0]
	part, err := parts.NextPart()
	if err != nil || part.FormName() != "archive" || part.FileName() != artifact.File {
		fail(w, 400, "expected the named universal archive")
		return
	}
	// Serialize with polling and cache pruning, including same-version retries.
	select {
	case s.syncGate <- struct{}{}:
		defer func() { <-s.syncGate }()
	case <-r.Context().Done():
		return
	}
	defer s.pruneReleaseCache()
	current, err := s.latestDesktop(r.Context(), app)
	if err != nil {
		s.internal(w, err)
		return
	}
	encoded, _ := json.Marshal(rel)
	replayed := current != nil && current.VersionCode == rel.VersionCode
	if current != nil && current.VersionCode >= rel.VersionCode {
		current.Notes = ""
		previous, _ := json.Marshal(current)
		if !bytes.Equal(encoded, previous) {
			fail(w, 409, "desktop releases are immutable and cannot decrease version")
			return
		}
	}
	staged, err := s.stageDesktopAsset(part, artifact)
	if err != nil {
		if errors.Is(err, errDesktopIntegrity) || errors.Is(err, io.ErrUnexpectedEOF) {
			fail(w, 400, "archive does not match manifest")
		} else {
			s.internal(w, err)
		}
		return
	}
	defer os.Remove(staged)
	if _, err := parts.NextPart(); err != io.EOF || !validGameArchive(staged, app.Name) {
		fail(w, 400, "expected one complete signed game ZIP")
		return
	}
	dest := filepath.Join(s.cfg.DataDir, "desktop", artifact.SHA256)
	if err := os.Rename(staged, dest); err != nil {
		s.internal(w, err)
		return
	}
	status := http.StatusOK
	if !replayed {
		_, err = s.db.ExecContext(r.Context(), "INSERT INTO desktop_releases(app_id,version_code,manifest,config_hash) VALUES(?,?,?,?)",
			app.ID, rel.VersionCode, encoded, desktopTrust(app))
		if err != nil {
			s.internal(w, err)
			return
		}
		status = http.StatusCreated
	}
	reply(w, status, map[string]any{"appId": app.ID, "version": rel.Version, "versionCode": rel.VersionCode, "sha256": artifact.SHA256, "size": artifact.Size})
}
