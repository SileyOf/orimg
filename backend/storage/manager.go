package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"zhuapic/config"
	"zhuapic/models"
)

// ErrObjectNotFound reports that a storage answered but the object is absent.
// It is not a health failure: the storage itself is reachable.
var ErrObjectNotFound = errors.New("object not found")

// Manager performs object I/O for one storage at a time. It knows nothing
// about which images live where; Service owns that bookkeeping.
type Manager struct {
	cfg        *config.Config
	cacheMu    sync.Mutex
	cacheStats CacheInventory
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// Options describe one storage location plus the site's local working
// directories (read cache and thumbnails).
type Options struct {
	UploadDir string // local storage directory
	CacheDir  string
	ThumbDir  string
	S3Prefix  string // object prefix for S3 and WebDAV keys
	S3        config.S3Config
	WebDAV    WebDAVConfig
}

func (m *Manager) DefaultOptions() Options {
	return Options{
		UploadDir: m.cfg.UploadDir,
		CacheDir:  m.cfg.CacheDir,
		ThumbDir:  filepath.Join(filepath.Dir(m.cfg.UploadDir), "thumbs"),
	}
}

func (m *Manager) S3Configured(opts Options) bool {
	return NewS3Client(opts.S3).Configured()
}

// Configured reports whether a storage mode has enough settings to be used.
func (m *Manager) Configured(mode string, opts Options) bool {
	switch mode {
	case "local":
		return strings.TrimSpace(opts.UploadDir) != ""
	case "s3":
		return m.S3Configured(opts)
	case "webdav":
		return NewWebDAVClient(opts.WebDAV).Configured()
	default:
		return false
	}
}

// SaveReader writes a new upload and sets image.StorageKey.
func (m *Manager) SaveReader(ctx context.Context, opts Options, mode string, image *models.Image, reader io.ReadSeeker, size int64, checksum string) error {
	if checksum == "" {
		var err error
		if checksum, err = ChecksumSHA256Reader(reader); err != nil {
			return err
		}
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return err
	}
	key := m.ReplicaKey(opts, mode, time.Now(), image.PublicName)
	if err := m.Put(ctx, opts, mode, key, image.MimeType, size, reader, checksum); err != nil {
		return err
	}
	image.StorageKey = key
	return nil
}

// Put stores exactly size bytes under key. checksum is the hex SHA-256 of the
// body; S3 signs with it.
func (m *Manager) Put(ctx context.Context, opts Options, mode, key, contentType string, size int64, body io.Reader, checksum string) error {
	if key == "" {
		return errors.New("storage key is required")
	}
	opts = m.normalizeOptions(opts)
	switch mode {
	case "local":
		return writeAtomicSized(m.LocalPath(opts, key), body, size)
	case "s3":
		if checksum == "" {
			return errors.New("s3 upload requires a checksum")
		}
		return NewS3Client(opts.S3).PutObjectReader(ctx, key, contentType, size, body, checksum)
	case "webdav":
		return NewWebDAVClient(opts.WebDAV).PutReader(ctx, key, contentType, size, body)
	default:
		return errors.New("unsupported storage mode")
	}
}

func (m *Manager) Open(ctx context.Context, opts Options, mode, key string) (io.ReadCloser, error) {
	opts = m.normalizeOptions(opts)
	switch mode {
	case "local":
		f, err := os.Open(m.LocalPath(opts, key))
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrObjectNotFound, key)
		}
		return f, err
	case "s3":
		return NewS3Client(opts.S3).OpenObject(ctx, key)
	case "webdav":
		return NewWebDAVClient(opts.WebDAV).Open(ctx, key)
	default:
		return nil, errors.New("unsupported storage mode")
	}
}

// DeleteObject removes an object; a missing object counts as deleted.
func (m *Manager) DeleteObject(ctx context.Context, opts Options, mode, key string) error {
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
	case "webdav":
		return NewWebDAVClient(opts.WebDAV).Delete(ctx, key)
	default:
		return errors.New("unsupported storage mode")
	}
}

// ReplicaKey is the object key for an image on a storage: the upload day plus
// the public name, below the storage's prefix for remote storages.
func (m *Manager) ReplicaKey(opts Options, mode string, createdAt time.Time, publicName string) string {
	opts = m.normalizeOptions(opts)
	day := createdAt.Format("20060102")
	if createdAt.IsZero() {
		day = time.Now().Format("20060102")
	}
	if mode == "local" {
		return filepath.ToSlash(filepath.Join(day, filepath.Base(publicName)))
	}
	return joinObjectKey(opts.S3Prefix, day, filepath.Base(publicName))
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
	if abs, err := filepath.Abs(probe); err == nil {
		probe = abs
	}
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

// LocalPath resolves a key inside a local storage directory. Keys can never
// escape the directory.
func (m *Manager) LocalPath(opts Options, storageKey string) string {
	opts = m.normalizeOptions(opts)
	if filepath.IsAbs(storageKey) {
		return filepath.Clean(storageKey) // keys written by very old versions
	}
	storageKey = strings.TrimLeft(filepath.Clean(filepath.FromSlash(storageKey)), string(filepath.Separator))
	if !filepath.IsLocal(storageKey) {
		storageKey = filepath.Base(storageKey)
	}
	return filepath.Join(opts.UploadDir, storageKey)
}

func (m *Manager) CachePath(opts Options, publicName string) string {
	opts = m.normalizeOptions(opts)
	return filepath.Join(opts.CacheDir, filepath.Base(publicName))
}

func writeAtomic(path string, body []byte) error {
	return writeAtomicReader(path, bytes.NewReader(body))
}

func writeAtomicReader(path string, reader io.Reader) error {
	return writeAtomicSized(path, reader, -1)
}

// writeAtomicSized writes through a unique temp file and renames it into place.
// When size is positive, a short or oversized body is rejected before rename.
func writeAtomicSized(path string, reader io.Reader, size int64) error {
	if size > 0 {
		reader = io.LimitReader(reader, size+1)
	}
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
	written, err := io.Copy(tmp, reader)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if size > 0 && written != size {
		_ = tmp.Close()
		return fmt.Errorf("unexpected object size: expected %d, received %d", size, written)
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

func (m *Manager) normalizeOptions(opts Options) Options {
	if opts.CacheDir == "" {
		opts.CacheDir = m.cfg.CacheDir
	}
	if opts.ThumbDir == "" {
		opts.ThumbDir = filepath.Join(filepath.Dir(m.cfg.UploadDir), "thumbs")
	}
	opts.S3Prefix = cleanObjectPrefix(opts.S3Prefix)
	if opts.S3.Region == "" {
		opts.S3.Region = "auto"
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
		cleanCache(ctx, cacheDirFn(), ttlFn(), time.Now())
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
