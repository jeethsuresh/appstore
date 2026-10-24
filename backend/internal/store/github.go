package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxAPK int64 = 300 << 20

func appTrust(a App) string { return hash(a.Package + "\x00" + a.Certificate) }

type githubStatusError int

func (e githubStatusError) Error() string { return fmt.Sprintf("GitHub returned HTTP %d", int(e)) }
func transient(err error) bool {
	var status githubStatusError
	var network net.Error
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.As(err, &network) || (errors.As(err, &status) && (status == 429 || status >= 500))
}

type githubRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Body       string `json:"body"`
	Assets     []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func (s *Server) github(ctx context.Context, path, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.api+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "private-android-store")
	if s.cfg.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.GitHubToken)
	}
	// net/http strips Authorization on cross-host redirects, including GitHub's signed asset CDN.
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request failed: %w", err)
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, githubStatusError(res.StatusCode)
	}
	return res, nil
}
func (s *Server) Sync(ctx context.Context, a App, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	select {
	case s.syncGate <- struct{}{}:
		defer func() { <-s.syncGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = s.syncOnce(ctx, a, tag)
		if err == nil || !transient(err) {
			return err
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return err
}
func (s *Server) syncOnce(ctx context.Context, a App, tag string) error {
	if !hexDigest.MatchString(a.Certificate) {
		return fmt.Errorf("%s: configure the existing release certificate fingerprint before ingestion", a.ID)
	}
	path := "/repos/" + a.Repo + "/releases/latest"
	if tag != "" {
		path = "/repos/" + a.Repo + "/releases/tags/" + url.PathEscape(tag)
	}
	res, err := s.github(ctx, path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	var rel githubRelease
	err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&rel)
	res.Body.Close()
	if err != nil {
		return err
	}
	if rel.Draft || rel.Prerelease {
		return fmt.Errorf("only published stable releases are supported")
	}
	var assetID, size int64
	var digest string
	for _, asset := range rel.Assets {
		if asset.Name == strings.ReplaceAll(a.Asset, "{tag}", rel.Tag) {
			if assetID != 0 {
				return fmt.Errorf("duplicate APK assets")
			}
			assetID = asset.ID
			size = asset.Size
			digest = asset.Digest
		}
	}
	if assetID == 0 || size <= 0 || size > maxAPK {
		return fmt.Errorf("missing or invalid asset %s", a.Asset)
	}
	latest, err := s.latest(ctx, a.ID)
	if err != nil {
		return err
	}
	if latest != nil && latest.ConfigHash == appTrust(a) && latest.AssetID == assetID && latest.Tag == rel.Tag && latest.Size == size && (digest == "" || digest == "sha256:"+latest.SHA256) {
		if st, statErr := os.Stat(filepath.Join(s.cfg.DataDir, "apks", latest.SHA256+".apk")); statErr == nil && st.Mode().IsRegular() && st.Size() == size {
			// Existing verified APKs may have been ingested before JSON notes support.
			notes, notesErr := s.releaseNotes(ctx, a, rel, APKInfo{VersionName: latest.VersionName, VersionCode: latest.VersionCode})
			if notesErr != nil {
				return fmt.Errorf("release changelog verification failed: %w", notesErr)
			}
			if notes != latest.Notes {
				_, notesErr = s.db.ExecContext(ctx, "UPDATE releases SET notes=? WHERE id=?", notes, latest.ID)
			}
			return notesErr
		}
	}
	res, err = s.github(ctx, fmt.Sprintf("/repos/%s/releases/assets/%d", a.Repo, assetID), "application/octet-stream")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	f, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "apks"), "ingest-*.apk")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxAPK+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size || n > maxAPK {
		return fmt.Errorf("asset size mismatch")
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if digest != "" && digest != "sha256:"+actual {
		return fmt.Errorf("GitHub asset digest mismatch")
	}
	info, err := s.inspect(ctx, f.Name())
	if err != nil {
		return fmt.Errorf("APK verification failed: %w", err)
	}
	if info.Package != a.Package || info.Certificate != a.Certificate {
		return fmt.Errorf("APK package or signing certificate does not match %s", a.ID)
	}
	if info.VersionCode <= 0 || info.VersionName == "" || info.MinSDK <= 0 {
		return fmt.Errorf("invalid APK version metadata")
	}
	notes, err := s.releaseNotes(ctx, a, rel, info)
	if err != nil {
		return fmt.Errorf("release changelog verification failed: %w", err)
	}
	if latest != nil && info.VersionCode <= latest.VersionCode {
		if info.VersionCode != latest.VersionCode || actual != latest.SHA256 {
			return fmt.Errorf("version code %d is not newer than %d; releases are immutable", info.VersionCode, latest.VersionCode)
		}
		// Heal a missing cached file after a restore or interrupted write before returning.
		if err = os.Rename(f.Name(), filepath.Join(s.cfg.DataDir, "apks", actual+".apk")); err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, "UPDATE releases SET asset_id=?,tag=?,config_hash=?,notes=? WHERE id=?", assetID, rel.Tag, appTrust(a), notes, latest.ID)
		return err
	}
	dest := filepath.Join(s.cfg.DataDir, "apks", actual+".apk")
	if err = os.Rename(f.Name(), dest); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO releases(app_id,tag,version_code,version_name,sha256,size,min_sdk,notes,created,asset_id,config_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, a.ID, rel.Tag, info.VersionCode, info.VersionName, actual, n, info.MinSDK, notes, time.Now().UTC().Format(time.RFC3339), assetID, appTrust(a))
	return err
}
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		fail(w, 413, "webhook too large")
		return
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.WebhookSecret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Hub-Signature-256"))) {
		fail(w, 401, "invalid webhook signature")
		return
	}
	if r.Header.Get("X-GitHub-Event") == "ping" {
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Header.Get("X-GitHub-Event") != "release" {
		w.WriteHeader(204)
		return
	}
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &payload) != nil {
		fail(w, 400, "invalid webhook JSON")
		return
	}
	if payload.Action != "published" && payload.Action != "released" && payload.Action != "edited" {
		w.WriteHeader(204)
		return
	}
	for _, a := range s.cfg.Apps {
		if strings.EqualFold(a.Repo, payload.Repository.FullName) {
			select {
			case s.queue <- a.ID:
				reply(w, 202, map[string]string{"status": "queued"})
			default:
				fail(w, 503, "sync queue full; retry delivery")
			}
			return
		}
	}
	fail(w, 404, "repository is not configured")
}

// Polling provides recovery after restarts, missed webhooks, and transient failures.
func (s *Server) RunSync(ctx context.Context, interval time.Duration) {
	syncOne := func(a App) {
		syncCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
		defer cancel()
		if err := s.Sync(syncCtx, a, ""); err != nil {
			slog.Warn("release ingestion", "app", a.ID, "error", err)
		}
	}
	var ticks <-chan time.Time
	if interval > 0 {
		for _, a := range s.cfg.Apps {
			if ctx.Err() != nil {
				return
			}
			syncOne(a)
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		ticks = tick.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			for _, a := range s.cfg.Apps {
				if a.ID == id {
					syncOne(a)
				}
			}
		case <-ticks:
			for _, a := range s.cfg.Apps {
				if ctx.Err() != nil {
					return
				}
				syncOne(a)
			}
		}
	}
}
