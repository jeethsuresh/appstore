package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const maxChangelog = 32000

type releaseChangelog struct {
	SchemaVersion int      `json:"schemaVersion"`
	Version       string   `json:"version"`
	VersionCode   int64    `json:"versionCode"`
	Changes       []string `json:"changes"`
}

func changelogNotes(data []byte, info APKInfo) (string, error) {
	if len(data) > maxChangelog || !utf8.Valid(data) {
		return "", fmt.Errorf("changelog must be at most %d UTF-8 bytes", maxChangelog)
	}
	var changes releaseChangelog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&changes); err != nil {
		return "", fmt.Errorf("invalid changelog JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("changelog must contain one JSON object")
	}
	if changes.SchemaVersion != 1 || changes.Version != info.VersionName || changes.VersionCode != info.VersionCode {
		return "", fmt.Errorf("changelog schema or version does not match the verified APK")
	}
	if len(changes.Changes) < 1 || len(changes.Changes) > 50 {
		return "", fmt.Errorf("changelog requires 1-50 changes")
	}
	for _, change := range changes.Changes {
		if strings.TrimSpace(change) == "" || len(change) > 2000 || strings.ContainsFunc(change, func(r rune) bool { return r < 32 }) {
			return "", fmt.Errorf("changelog entries must be nonempty plain text of at most 2000 UTF-8 bytes")
		}
	}
	return "• " + strings.Join(changes.Changes, "\n• "), nil
}

func (s *Server) releaseNotes(ctx context.Context, a App, rel githubRelease, info APKInfo) (string, error) {
	var assetID, size int64
	var digest string
	for _, asset := range rel.Assets {
		if asset.Name != "changelog.json" {
			continue
		}
		if assetID != 0 || asset.ID <= 0 || asset.Size <= 0 || asset.Size > maxChangelog {
			return "", fmt.Errorf("duplicate or invalid changelog asset")
		}
		assetID, size, digest = asset.ID, asset.Size, asset.Digest
	}
	if assetID == 0 {
		// Preserve notes for releases published before the JSON contract existed.
		notes := rel.Body
		if len(notes) > maxChangelog {
			notes = notes[:maxChangelog]
		}
		return notes, nil
	}
	if rel.Tag != "v"+info.VersionName {
		return "", fmt.Errorf("changelog release tag does not match the verified APK")
	}
	data, err := s.verifiedMetadata(ctx, a, assetID, size, digest, maxChangelog)
	if err != nil {
		return "", err
	}
	return changelogNotes(data, info)
}
