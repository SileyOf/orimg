package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zhuapic/config"
)

func TestKeyedMutexReleasesEntries(t *testing.T) {
	var locks keyedMutex
	unlock := locks.Lock("a")
	unlock()
	if len(locks.locks) != 0 {
		t.Fatal("lock entry leaked")
	}
}

func TestFillCacheVerifiesChecksumAndWritesAtomically(t *testing.T) {
	body := []byte("cached-object-body")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	dir := t.TempDir()
	cfg := &config.Config{UploadDir: filepath.Join(dir, "uploads"), CacheDir: filepath.Join(dir, "cache")}
	s := &Service{cfg: cfg, manager: NewManager(cfg)}
	ref := copyRef{key: "images/abc.png", storage: Storage{ID: 1, Provider: "s3", Config: StorageConfig{Endpoint: server.URL, Region: "auto", Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true}}}
	runtime := s.manager.DefaultOptions()
	cachePath := filepath.Join(cfg.CacheDir, "abc.png")
	if err := s.fillCache(context.Background(), ref, runtime, cachePath, int64(len(body)), "0000"); !errors.Is(err, errChecksum) {
		t.Fatalf("checksum mismatch not detected: %v", err)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatal("unverified content reached the cache")
	}
	if err := s.fillCache(context.Background(), ref, runtime, cachePath, int64(len(body)), ChecksumSHA256(body)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cachePath)
	if err != nil || string(got) != string(body) {
		t.Fatalf("cache content: %q %v", got, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(cfg.CacheDir, ".zhuapic-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestHealthTrackerOpensAfterRepeatedFailures(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	writes := 0
	h := &healthTracker{exec: func(string, ...any) { writes++ }, failures: map[uint]int{}, openTill: map[uint]time.Time{}, now: func() time.Time { return now }}
	h.Fail(7, fmt.Errorf("%w: gone", ErrObjectNotFound))
	h.Fail(7, context.Canceled)
	for i := 0; i < failureThreshold-1; i++ {
		h.Fail(7, errors.New("connection refused"))
	}
	if !h.Available(7, nil) {
		t.Fatal("breaker opened before the threshold (missing objects must not count)")
	}
	h.Fail(7, errors.New("connection refused"))
	if h.Available(7, nil) {
		t.Fatal("breaker did not open")
	}
	first := h.RetryAt(7)
	if first.Sub(now) != time.Minute {
		t.Fatalf("first back-off %s", first.Sub(now))
	}
	h.Fail(7, errors.New("connection refused"))
	if h.RetryAt(7).Sub(now) != 2*time.Minute {
		t.Fatalf("back-off did not grow: %s", h.RetryAt(7).Sub(now))
	}
	for i := 0; i < 20; i++ {
		h.Fail(7, errors.New("connection refused"))
	}
	if h.RetryAt(7).Sub(now) != 30*time.Minute {
		t.Fatalf("back-off not capped: %s", h.RetryAt(7).Sub(now))
	}
	now = now.Add(31 * time.Minute)
	if !h.Available(7, nil) {
		t.Fatal("breaker did not half-open after the window")
	}
	h.OK(7)
	if h.failures[7] != 0 || !h.RetryAt(7).IsZero() {
		t.Fatal("success did not reset the breaker")
	}
	persisted := now.Add(time.Minute)
	if h.Available(8, &persisted) {
		t.Fatal("offline state persisted by another process was ignored")
	}
	if writes == 0 {
		t.Fatal("health state was never persisted")
	}
}
