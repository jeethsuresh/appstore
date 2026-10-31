package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// GitHub assets have immutable IDs: replacing an upload creates a new ID.
// Cache verified small manifests/changelogs across polls and pod restarts.
// Release discovery still runs on every poll, so new asset IDs/digests refresh
// immediately without a TTL or additional app configuration.
func (s *Server) verifiedMetadata(ctx context.Context, a App, id, size int64, digest string, limit int64) ([]byte, error) {
	if id <= 0 || size <= 0 || size > limit {
		return nil, fmt.Errorf("invalid release metadata asset")
	}
	valid := func(data []byte) bool {
		if int64(len(data)) != size {
			return false
		}
		sum := sha256.Sum256(data)
		return digest == "" || digest == "sha256:"+hex.EncodeToString(sum[:])
	}
	directory := filepath.Join(s.cfg.DataDir, "metadata")
	key := hash(fmt.Sprintf("%s\x00%d\x00%d\x00%s", a.Repo, id, size, digest))
	destination := filepath.Join(directory, key)
	if f, err := os.Open(destination); err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, limit+1))
		f.Close()
		if readErr == nil && valid(data) {
			return data, nil
		}
	}
	response, err := s.github(ctx, fmt.Sprintf("/repos/%s/releases/assets/%d", a.Repo, id), "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if !valid(data) {
		return nil, fmt.Errorf("release metadata checksum or size mismatch")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(directory, "metadata-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Rename(f.Name(), destination); err != nil {
		return nil, err
	}
	return data, nil
}
