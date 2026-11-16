package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zhuapic/config"
	"zhuapic/models"
)

func TestEnsureS3CacheSingleFetch(t *testing.T) {
	var fetches atomic.Int32
	body := []byte("cached-object-body")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	dir := t.TempDir()
	manager := NewManager(&config.Config{UploadDir: filepath.Join(dir, "uploads"), CacheDir: filepath.Join(dir, "cache")})
	opts := manager.DefaultOptions()
	opts.S3 = config.S3Config{Endpoint: server.URL, Region: "auto", Bucket: "b", AccessKey: "a", SecretKey: "s", ForcePathStyle: true}
	image := &models.Image{PublicName: "abc.png", StorageKey: "images/abc.png", Size: int64(len(body))}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, _, err := manager.EnsureS3Cache(context.Background(), opts, image, time.Hour)
			if err == nil {
				var got []byte
				if got, err = os.ReadFile(path); err == nil && string(got) != string(body) {
					err = os.ErrInvalid
				}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("expected a single remote fetch, got %d", n)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "cache", ".zhuapic-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestKeyedMutexReleasesEntries(t *testing.T) {
	var locks keyedMutex
	unlock := locks.Lock("a")
	unlock()
	if len(locks.locks) != 0 {
		t.Fatal("lock entry leaked")
	}
}
