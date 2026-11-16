package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"zhuapic/config"
	"zhuapic/models"
)

type Manager struct {
	cfg        *config.Config
	cacheMu    sync.Mutex
	cacheStats CacheInventory
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg: cfg,
	}
}

type Options struct {
	UploadDir string
	CacheDir  string
	ThumbDir  string
	S3Prefix  string
	S3        config.S3Config
}

func (m *Manager) DefaultOptions() Options {
	return Options{
		UploadDir: m.cfg.UploadDir,
		CacheDir:  m.cfg.CacheDir,
		ThumbDir:  filepath.Join(filepath.Dir(m.cfg.UploadDir), "thumbs"),
		S3Prefix:  "images",
		S3:        m.cfg.S3,
	}
}

func (m *Manager) S3Configured(opts Options) bool {
	return NewS3Client(opts.S3).Configured()
}

func (m *Manager) Save(ctx context.Context, opts Options, mode string, image *models.Image, header *multipart.FileHeader) error {
	src, err := header.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	body, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	return m.SaveBytes(ctx, opts, mode, image, body)
}

func (m *Manager) SaveBytes(ctx context.Context, opts Options, mode string, image *models.Image, body []byte) error {
	reader := bytes.NewReader(body)
	return m.SaveReader(ctx, opts, mode, image, reader, int64(len(body)), ChecksumSHA256(body))
}

func (m *Manager) SaveReader(ctx context.Context, opts Options, mode string, image *models.Image, reader io.ReadSeeker, size int64, checksum string) error {
	opts = m.normalizeOptions(opts)
	if checksum == "" {
		var err error
		checksum, err = ChecksumSHA256Reader(reader)
		if err != nil {
			return err
		}
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch mode {
	case "s3":
		return m.saveS3Reader(ctx, opts, image, reader, size, checksum)
	case "local", "mirror":
		return m.saveLocalReader(opts, image, reader)
	default:
		return errors.New("unsupported storage mode")
	}
}

func (m *Manager) ReadBytes(ctx context.Context, opts Options, mode, key string) ([]byte, error) {
	opts = m.normalizeOptions(opts)
	switch mode {
	case "local":
		return os.ReadFile(m.LocalPath(opts, key))
	case "s3":
		return NewS3Client(opts.S3).GetObject(ctx, key)
	default:
		return nil, errors.New("unsupported storage mode")
	}
}

func (m *Manager) WriteReplica(ctx context.Context, opts Options, mode, key, contentType string, body []byte) (string, error) {
	opts = m.normalizeOptions(opts)
	switch mode {
	case "local":
		if key == "" {
			return "", errors.New("local storage key is required")
		}
		path := m.LocalPath(opts, key)
		if err := writeAtomic(path, body); err != nil {
			return "", err
		}
		return key, nil
	case "s3":
		if key == "" {
			return "", errors.New("s3 storage key is required")
		}
		if err := NewS3Client(opts.S3).PutObject(ctx, key, contentType, body); err != nil {
			return "", err
		}
		return key, nil
	default:
		return "", errors.New("unsupported storage mode")
	}
}

func (m *Manager) DeleteReplica(ctx context.Context, opts Options, mode, key string) error {
	opts = m.normalizeOptions(opts)
	switch mode {
	case "local":
		err := os.Remove(m.LocalPath(opts, key))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	case "s3":
		return NewS3Client(opts.S3).DeleteObject(ctx, key)
	default:
		return errors.New("unsupported storage mode")
	}
}

func (m *Manager) ReplicaKey(opts Options, mode string, createdAt time.Time, publicName string) string {
	opts = m.normalizeOptions(opts)
	day := createdAt.Format("20060102")
	if createdAt.IsZero() {
		day = time.Now().Format("20060102")
	}
	if mode == "s3" {
		return joinObjectKey(opts.S3Prefix, day, filepath.Base(publicName))
	}
	return filepath.Join(day, filepath.Base(publicName))
}

func ChecksumSHA256(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func ChecksumSHA256Reader(reader io.ReadSeeker) (string, error) {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func DiskUsagePercent(path string) (float64, error) {
	probe := filepath.Clean(path)
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return 0, os.ErrNotExist
		}
		probe = parent
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(probe, &stat); err != nil {
		return 0, err
	}
	if stat.Blocks == 0 {
		return 0, nil
	}
	used := stat.Blocks - stat.Bavail
	return float64(used) / float64(stat.Blocks) * 100, nil
}

func (m *Manager) Delete(ctx context.Context, opts Options, image *models.Image) error {
	opts = m.normalizeOptions(opts)
	if image.StorageMode == "s3" {
		_ = os.Remove(m.CachePath(opts, image.PublicName))
		return NewS3Client(opts.S3).DeleteObject(ctx, image.StorageKey)
	}
	return os.Remove(m.LocalPath(opts, image.StorageKey))
}

func (m *Manager) LocalPath(opts Options, storageKey string) string {
	opts = m.normalizeOptions(opts)
	if filepath.IsAbs(storageKey) {
		return filepath.Clean(storageKey)
	}
	storageKey = strings.TrimLeft(filepath.Clean(storageKey), string(filepath.Separator))
	return filepath.Join(opts.UploadDir, storageKey)
}

func (m *Manager) CachePath(opts Options, publicName string) string {
	opts = m.normalizeOptions(opts)
	return filepath.Join(opts.CacheDir, filepath.Base(publicName))
}

func (m *Manager) EnsureS3Cache(ctx context.Context, opts Options, image *models.Image, ttl time.Duration) (string, bool, error) {
	opts = m.normalizeOptions(opts)
	cachePath := m.CachePath(opts, image.PublicName)
	if stat, err := os.Stat(cachePath); err == nil {
		if time.Since(stat.ModTime()) < ttl {
			return cachePath, true, nil
		}
		_ = os.Remove(cachePath)
	}

	body, err := NewS3Client(opts.S3).GetObject(ctx, image.StorageKey)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(opts.CacheDir, 0755); err != nil {
		return "", false, err
	}
	tmp := cachePath + ".tmp"
	if err := os.WriteFile(tmp, body, 0644); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	return cachePath, false, nil
}

func (m *Manager) saveLocal(opts Options, image *models.Image, body []byte) error {
	return m.saveLocalReader(opts, image, bytes.NewReader(body))
}

func (m *Manager) saveLocalReader(opts Options, image *models.Image, reader io.Reader) error {
	day := time.Now().Format("20060102")
	key := filepath.Join(day, image.PublicName)
	dir := filepath.Join(opts.UploadDir, day)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	destPath := filepath.Join(dir, image.PublicName)
	if err := writeAtomicReader(destPath, reader); err != nil {
		return err
	}
	image.StorageKey = key
	return nil
}

func writeAtomic(path string, body []byte) error {
	return writeAtomicReader(path, bytes.NewReader(body))
}

func writeAtomicReader(path string, reader io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".zhuapic-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, reader); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (m *Manager) saveS3(ctx context.Context, opts Options, image *models.Image, body []byte) error {
	return m.saveS3Reader(ctx, opts, image, bytes.NewReader(body), int64(len(body)), ChecksumSHA256(body))
}

func (m *Manager) saveS3Reader(ctx context.Context, opts Options, image *models.Image, reader io.Reader, size int64, checksum string) error {
	s3 := NewS3Client(opts.S3)
	if !s3.Configured() {
		return errors.New("s3 is selected but not configured")
	}
	key := joinObjectKey(opts.S3Prefix, time.Now().Format("20060102"), image.PublicName)
	if err := s3.PutObjectReader(ctx, key, image.MimeType, size, reader, checksum); err != nil {
		return err
	}
	image.StorageKey = key
	return nil
}

func (m *Manager) normalizeOptions(opts Options) Options {
	if opts.UploadDir == "" {
		opts.UploadDir = m.cfg.UploadDir
	}
	if opts.CacheDir == "" {
		opts.CacheDir = m.cfg.CacheDir
	}
	if opts.ThumbDir == "" {
		opts.ThumbDir = filepath.Join(filepath.Dir(opts.UploadDir), "thumbs")
	}
	opts.S3Prefix = cleanObjectPrefix(opts.S3Prefix)
	if opts.S3Prefix == "" {
		opts.S3Prefix = "images"
	}
	if opts.S3.Region == "" {
		opts.S3.Region = m.cfg.S3.Region
	}
	return opts
}

func joinObjectKey(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.ReplaceAll(part, "\\", "/"), "/")
		if part != "" && part != "." {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, "/")
}

func cleanObjectPrefix(prefix string) string {
	prefix = strings.TrimSpace(strings.ReplaceAll(prefix, "\\", "/"))
	prefix = strings.Trim(prefix, "/")
	if prefix == "." {
		return ""
	}
	parts := []string{}
	for _, part := range strings.Split(prefix, "/") {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/")
}

func StartCacheJanitor(ctx context.Context, cacheDirFn func() string, ttlFn func() time.Duration, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	clean := func() {
		ttl := ttlFn()
		cacheDir := cacheDirFn()
		cleanCache(ctx, cacheDir, ttl, time.Now())
	}

	clean()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}
