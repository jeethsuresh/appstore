package store

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVerifiedMetadataReusesDownloadsAcrossPollsAndRestarts(t *testing.T) {
	s := testServer(t)
	a := s.cfg.Apps[0]
	data := []byte(`{"changes":["Verified release metadata"]}`)
	var downloads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		_, _ = w.Write(data)
	}))
	defer upstream.Close()
	s.api = upstream.URL
	for range 3 {
		actual, err := s.verifiedMetadata(context.Background(), a, 1, int64(len(data)), "sha256:"+hash(string(data)), maxDesktopManifest)
		if err != nil || string(actual) != string(data) {
			t.Fatal("verified metadata unavailable", err)
		}
	}
	if downloads.Load() != 1 {
		t.Fatal("unchanged polling downloaded the same asset repeatedly")
	}
	config := s.cfg
	s.Close()
	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.api = upstream.URL
	if _, err := restarted.verifiedMetadata(context.Background(), a, 1, int64(len(data)), "sha256:"+hash(string(data)), maxDesktopManifest); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatal("a pod restart discarded the verified metadata cache")
	}
	entries, err := os.ReadDir(filepath.Join(config.DataDir, "metadata"))
	if err != nil || len(entries) != 1 {
		t.Fatal("missing private metadata cache", err)
	}
	cache := filepath.Join(config.DataDir, "metadata", entries[0].Name())
	if err := os.WriteFile(cache, []byte(strings.Repeat("x", len(data))), 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := restarted.verifiedMetadata(context.Background(), a, 1, int64(len(data)), "sha256:"+hash(string(data)), maxDesktopManifest)
	if err != nil || string(actual) != string(data) || downloads.Load() != 2 {
		t.Fatal("corrupted cache was not repaired", err)
	}
}

func TestMetadataCacheRefreshesNewAssetsAndRejectsBadDownloads(t *testing.T) {
	s := testServer(t)
	a := s.cfg.Apps[0]
	var downloads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		fmt.Fprint(w, "new metadata")
	}))
	defer upstream.Close()
	s.api = upstream.URL
	for _, id := range []int64{1, 2} {
		if _, err := s.verifiedMetadata(context.Background(), a, id, 12, "sha256:"+hash("new metadata"), maxDesktopManifest); err != nil {
			t.Fatal(err)
		}
	}
	if downloads.Load() != 2 {
		t.Fatal("a new GitHub asset identity did not refresh metadata")
	}
	if _, err := s.verifiedMetadata(context.Background(), a, 3, 12, "sha256:"+hash("bad metadata"), maxDesktopManifest); err == nil {
		t.Fatal("bad metadata checksum accepted")
	}
	if _, err := s.verifiedMetadata(context.Background(), a, 4, 13, "", maxDesktopManifest); err == nil {
		t.Fatal("truncated metadata accepted")
	}
	if _, err := s.verifiedMetadata(context.Background(), a, 5, maxDesktopManifest+1, "", maxDesktopManifest); err == nil {
		t.Fatal("oversized metadata accepted")
	}
	entries, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "metadata"))
	if err != nil || len(entries) != 2 {
		t.Fatal("invalid metadata was cached", err)
	}
}
