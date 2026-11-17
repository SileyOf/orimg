package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zhuapic/config"
	"zhuapic/models"
)

func TestReplicaKeyUsesOriginalDate(t *testing.T) {
	cfg := &config.Config{UploadDir: t.TempDir(), CacheDir: t.TempDir()}
	manager := NewManager(cfg)
	opts := manager.DefaultOptions()
	opts.S3Prefix = "private/images"
	created := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	if got := manager.ReplicaKey(opts, "local", created, "../photo.png"); got != "20260709/photo.png" {
		t.Fatalf("unexpected local key: %q", got)
	}
	if got := manager.ReplicaKey(opts, "s3", created, "../photo.png"); got != "private/images/20260709/photo.png" {
		t.Fatalf("unexpected s3 key: %q", got)
	}
}

func TestLocalReplicaWriteIsReadableAndChecksummed(t *testing.T) {
	cfg := &config.Config{UploadDir: t.TempDir(), CacheDir: t.TempDir()}
	manager := NewManager(cfg)
	body := []byte("zhuapic-replica")
	key := filepath.Join("20260709", "sample.bin")
	if err := manager.Put(context.Background(), manager.DefaultOptions(), "local", key, "application/octet-stream", int64(len(body)), strings.NewReader(string(body)), ChecksumSHA256(body)); err != nil {
		t.Fatal(err)
	}
	reader, err := manager.Open(context.Background(), manager.DefaultOptions(), "local", key)
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Open(context.Background(), manager.DefaultOptions(), "local", "missing.bin"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing local object should be ErrObjectNotFound, got %v", err)
	}
	if err := manager.Put(context.Background(), manager.DefaultOptions(), "local", "short.bin", "", 99, strings.NewReader("short"), ""); err == nil {
		t.Fatal("short body accepted")
	}
	if string(read) != string(body) {
		t.Fatalf("unexpected replica content: %q", read)
	}
	if ChecksumSHA256(read) != ChecksumSHA256(body) {
		t.Fatal("checksum mismatch")
	}
	if _, err := os.Stat(filepath.Join(cfg.UploadDir, key)); err != nil {
		t.Fatal(err)
	}
}

func TestSaveReaderStreamsToLocalStorage(t *testing.T) {
	cfg := &config.Config{UploadDir: t.TempDir(), CacheDir: t.TempDir()}
	manager := NewManager(cfg)
	body := strings.Repeat("streamed-image-data", 4096)
	reader := strings.NewReader(body)
	checksum, err := ChecksumSHA256Reader(reader)
	if err != nil {
		t.Fatal(err)
	}
	image := &models.Image{PublicName: "stream.bin", MimeType: "application/octet-stream"}
	if err := manager.SaveReader(context.Background(), manager.DefaultOptions(), "local", image, reader, int64(len(body)), checksum); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(manager.LocalPath(manager.DefaultOptions(), image.StorageKey))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != body {
		t.Fatal("streamed local content mismatch")
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	if retryDelay(1) != 30*time.Second {
		t.Fatalf("unexpected first retry: %s", retryDelay(1))
	}
	if retryDelay(99) != 64*time.Minute {
		t.Fatalf("unexpected bounded retry: %s", retryDelay(99))
	}
}
