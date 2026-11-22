package store

import (
	"archive/zip"
	"bytes"
	"crypto/hmac"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxPublisherArchive = 192 << 20

// Explicitly configured native-game publishers use one verifier per app ID.
// Catalog visibility remains independent from permission to publish a release.
func DesktopPublishers(apps []App, getenv func(string) string) (map[string]string, error) {
	result := make(map[string]string)
	seen := make(map[string]bool)
	for _, app := range apps {
		if !app.Desktop {
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

func nativeGameManifest(data []byte, app App) (*DesktopRelease, error) {
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
	if code != rel.VersionCode || !app.allowsDesktopArtifact("macos", code) || (len(rel.Artifacts) != 2 && len(rel.Artifacts) != 4) {
		return nil, errors.New("expected the notarized universal release for this version")
	}
	var mac []DesktopArtifact
	for _, artifact := range rel.Artifacts {
		if artifact.Format != "" || artifact.Size > maxPublisherArchive {
			return nil, errors.New("invalid native game artifact")
		}
		if artifact.Platform == "macos" {
			mac = append(mac, artifact)
			continue
		}
		suffix, entry := "linux-x64.zip", app.Name
		if artifact.Platform == "windows" {
			suffix, entry = "win-x64.zip", app.Name+".exe"
		}
		if artifact.Arch != "x64" || artifact.File != strings.ReplaceAll(app.Name, " ", "")+"-"+rel.Version+"-"+suffix || artifact.Entry != entry {
			return nil, errors.New("invalid native game artifact")
		}
	}
	if len(mac) != 2 {
		return nil, errors.New("both notarized macOS architectures are required")
	}
	first, second := mac[0], mac[1]
	if first.Arch == "x64" {
		first, second = second, first
	}
	if first.Platform != "macos" || first.Arch != "arm64" || second.Arch != "x64" ||
		first.File != strings.ReplaceAll(app.Name, " ", "")+"-"+rel.Version+"-mac-universal.zip" || first.Entry != app.Name+".app" ||
		first.Size > maxPublisherArchive {
		return nil, errors.New("invalid universal macOS artifact")
	}
	second.Arch = first.Arch
	if first != second {
		return nil, errors.New("both architectures must share the same universal archive")
	}
	if len(rel.Artifacts) == 4 {
		seen := map[string]bool{}
		for _, a := range rel.Artifacts {
			seen[a.Platform+"/"+a.Arch] = true
		}
		if !seen["linux/x64"] || !seen["windows/x64"] {
			return nil, errors.New("both Linux and Windows packages are required")
		}
	}
	rel.Tag = app.DesktopReleaseTagPrefix + rel.Tag
	return rel, nil
}

// The protected signing workflow verifies Developer ID, notarization and an
// installed match. Bazaar independently checks the transferred archive and its
// required bundle entries before making it discoverable.
func validGameArchive(file, name string) bool {
	z, err := zip.OpenReader(file)
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

func validNativeGameArchive(file string, app App, artifact DesktopArtifact) bool {
	if artifact.Platform == "macos" {
		return validGameArchive(file, app.Name)
	}
	z, err := zip.OpenReader(file)
	if err != nil {
		return false
	}
	defer z.Close()
	seen := map[string]bool{}
	var expanded uint64
	for _, entry := range z.File {
		name := entry.Name
		clean := strings.TrimSuffix(name, "/")
		folded := strings.ToLower(clean)
		if clean == "" || path.IsAbs(clean) || path.Clean(clean) != clean || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(name, "\\\x00:") || seen[folded] || (!entry.FileInfo().IsDir() && !entry.Mode().IsRegular()) {
			return false
		}
		seen[folded] = true
		if entry.UncompressedSize64 > (768<<20)-expanded {
			return false
		}
		expanded += entry.UncompressedSize64
	}
	read := func(name string, size int) []byte {
		f, e := z.Open(name)
		if e != nil {
			return nil
		}
		defer f.Close()
		buf := make([]byte, size)
		if _, e := io.ReadFull(f, buf); e != nil {
			return nil
		}
		return buf
	}
	if string(read(app.Name+".pck", 4)) != "GDPC" {
		return false
	}
	if artifact.Platform == "linux" {
		header := read(artifact.Entry, 20)
		return len(header) == 20 && string(header[:4]) == "\x7fELF" && header[4] == 2 && header[5] == 1 && binary.LittleEndian.Uint16(header[18:20]) == 62
	}
	header := read(artifact.Entry, 64)
	if len(header) != 64 || string(header[:2]) != "MZ" {
		return false
	}
	offset := binary.LittleEndian.Uint32(header[60:64])
	if offset > 1<<20 {
		return false
	}
	header = read(artifact.Entry, int(offset)+6)
	return len(header) == int(offset)+6 && string(header[offset:offset+4]) == "PE\x00\x00" && binary.LittleEndian.Uint16(header[offset+4:offset+6]) == 0x8664
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
		if candidate.ID == id && candidate.Desktop {
			app = candidate
			break
		}
	}
	if app.ID == "" {
		fail(w, 404, "publisher unavailable")
		return
	}
	const maxRequest = 3*maxPublisherArchive + maxDesktopManifest + (64 << 10)
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
	rel, err := nativeGameManifest(data, app)
	if err != nil {
		fail(w, 400, err.Error())
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
	// Stage and validate every unique ZIP before exposing any new release.
	type stagedArchive struct {
		artifact DesktopArtifact
		file     string
	}
	var staged []stagedArchive
	defer func() {
		for _, item := range staged {
			_ = os.Remove(item.file)
		}
	}()
	seen := map[string]bool{}
	for _, artifact := range rel.Artifacts {
		if seen[artifact.File] {
			continue
		}
		seen[artifact.File] = true
		part, err := parts.NextPart()
		if err != nil || part.FormName() != "archive" || part.FileName() != artifact.File {
			fail(w, 400, "expected the named game archives in manifest order")
			return
		}
		file, err := s.stageDesktopAsset(part, artifact)
		if err != nil {
			if errors.Is(err, errDesktopIntegrity) || errors.Is(err, io.ErrUnexpectedEOF) {
				fail(w, 400, "archive does not match manifest")
			} else {
				s.internal(w, err)
			}
			return
		}
		staged = append(staged, stagedArchive{artifact, file})
		if !validNativeGameArchive(file, app, artifact) {
			fail(w, 400, "expected a complete native game ZIP")
			return
		}
	}
	if _, err := parts.NextPart(); err != io.EOF {
		fail(w, 400, "unexpected release archive")
		return
	}
	for _, item := range staged {
		dest := filepath.Join(s.cfg.DataDir, "desktop", item.artifact.SHA256)
		if err := os.Rename(item.file, dest); err != nil {
			s.internal(w, err)
			return
		}
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
	artifact := rel.Artifacts[0]
	reply(w, status, map[string]any{"appId": app.ID, "version": rel.Version, "versionCode": rel.VersionCode, "sha256": artifact.SHA256, "size": artifact.Size, "artifacts": rel.Artifacts})
}
