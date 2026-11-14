package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var releaseTempFile = regexp.MustCompile(`^ingest-[0-9]+(?:\.apk)?$`)

// Run before serving requests at startup, or while Sync holds syncGate.
func (s *Server) pruneReleaseCache() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.pruneReleaseFiles(ctx); err != nil {
		slog.Warn("pruning release cache", "error", err)
	}
}

func (s *Server) pruneReleaseFiles(ctx context.Context) error {
	apks := make(map[string]bool)
	desktop := make(map[string]bool)
	for _, app := range s.catalogApps() {
		if app.Package != "" {
			current, err := s.latest(ctx, app.ID)
			if err != nil {
				return fmt.Errorf("latest Android release for %s: %w", app.ID, err)
			}
			if current != nil && current.ConfigHash == appTrust(app) {
				apks[current.SHA256+".apk"] = true
			}
		}
		if app.Desktop {
			current, err := s.latestDesktop(ctx, app)
			if err != nil {
				return fmt.Errorf("latest desktop release for %s: %w", app.ID, err)
			}
			if current != nil {
				for _, artifact := range current.Artifacts {
					desktop[artifact.SHA256] = true
				}
			}
		}
	}

	// Resolve all current references before unlinking anything. An open download
	// can finish reading its file after the cache entry has been unlinked.
	apkCount, apkErr := pruneReleaseDir(ctx, filepath.Join(s.cfg.DataDir, "apks"), ".apk", apks)
	desktopCount, desktopErr := pruneReleaseDir(ctx, filepath.Join(s.cfg.DataDir, "desktop"), "", desktop)
	if apkCount+desktopCount > 0 {
		slog.Info("pruned release cache", "android_files", apkCount, "desktop_files", desktopCount)
	}
	return errors.Join(apkErr, desktopErr)
}

func pruneReleaseDir(ctx context.Context, dir, suffix string, keep map[string]bool) (int, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		name := file.Name()
		if !file.Type().IsRegular() || keep[name] {
			continue
		}
		digest, hasSuffix := strings.CutSuffix(name, suffix)
		if !(hasSuffix && hexDigest.MatchString(digest)) && !releaseTempFile.MatchString(name) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
			continue
		}
		removed++
	}
	return removed, nil
}
