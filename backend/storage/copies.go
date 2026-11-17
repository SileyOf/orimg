package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"zhuapic/models"
	"zhuapic/utils"
)

var errNoSource = errors.New("没有可读取的完整副本")

// RegisterUpload records the copy written during upload and queues automatic
// backups to every other active backup storage.
func (s *Service) RegisterUpload(ctx context.Context, image *models.Image, st *Storage, checksum string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO image_copies (image_id, storage_id, object_key, state, checksum_sha256, size, verified_at)
		VALUES (?,?,?,'ready',?,?,NOW())`, image.ID, st.ID, image.StorageKey, checksum, image.Size); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO image_copies (image_id, storage_id, state, size)
		SELECT ?, id, 'pending', ? FROM storages WHERE auto_backup=1 AND status='active' AND id<>?`, image.ID, image.Size, st.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE images SET checksum_sha256=? WHERE id=?`, checksum, image.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.Wake()
	return nil
}

// ensureBackupCopies queues a copy of every live image on backup storages.
// storageID 0 means every active backup storage.
func (s *Service) ensureBackupCopies(ctx context.Context, storageID uint) error {
	query := `INSERT IGNORE INTO image_copies (image_id, storage_id, state, size)
		SELECT i.id, st.id, 'pending', i.size FROM images i JOIN storages st ON st.auto_backup=1 AND st.status='active'
		WHERE i.deleted_at IS NULL`
	args := []any{}
	if storageID != 0 {
		query += ` AND st.id=?`
		args = append(args, storageID)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

type copyRef struct {
	storage  Storage
	key      string
	checksum string
}

// readyCopies lists readable copies of an image, best source first.
func (s *Service) readyCopies(ctx context.Context, imageID uint, exclude uint) ([]copyRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.storage_id, c.object_key, c.checksum_sha256 FROM image_copies c JOIN storages st ON st.id=c.storage_id
		WHERE c.image_id=? AND c.state='ready' AND c.storage_id<>? AND c.object_key<>''
		ORDER BY st.provider<>'local', st.read_priority, st.id`, imageID, exclude)
	if err != nil {
		return nil, err
	}
	type raw struct {
		id            uint
		key, checksum string
	}
	var found []raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.id, &r.key, &r.checksum); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, r)
	}
	rows.Close()
	out := make([]copyRef, 0, len(found))
	for _, r := range found {
		st, err := s.Get(ctx, r.id)
		if err != nil {
			continue
		}
		out = append(out, copyRef{storage: *st, key: r.key, checksum: r.checksum})
	}
	return out, nil
}

func (s *Service) markCopy(ctx context.Context, imageID, storageID uint, state string, cause error) {
	_, _ = s.db.ExecContext(ctx, `UPDATE image_copies SET state=?, last_error=?, attempts=0, next_attempt_at=NULL WHERE image_id=? AND storage_id=? AND state='ready'`,
		state, truncateError(cause), imageID, storageID)
	s.Wake()
}

// ServePath returns a local file holding the image, reading from the best
// available copy. Local copies are served in place; remote copies go through
// the read cache. Offline storages are skipped.
func (s *Service) ServePath(ctx context.Context, image *models.Image) (string, string, error) {
	var expected string
	_ = s.db.QueryRowContext(ctx, `SELECT checksum_sha256 FROM images WHERE id=?`, image.ID).Scan(&expected)
	copies, err := s.readyCopies(ctx, image.ID, 0)
	if err != nil {
		return "", "", err
	}
	runtime := s.RuntimeOptions()
	var remote []copyRef
	for _, c := range copies {
		if c.storage.Mode() != "local" {
			remote = append(remote, c)
			continue
		}
		path := s.manager.LocalPath(c.storage.Options(runtime), c.key)
		info, err := os.Stat(path)
		if err == nil && (image.Size <= 0 || info.Size() == image.Size) {
			return path, "local", nil
		}
		if errors.Is(err, os.ErrNotExist) {
			s.markCopy(ctx, image.ID, c.storage.ID, "missing", fmt.Errorf("%w: %s", ErrObjectNotFound, path))
		}
	}
	cachePath := s.manager.CachePath(runtime, image.PublicName)
	ttl := utils.SettingCacheTTL(s.db, s.cfg.CacheTTL)
	fresh := func() bool {
		info, err := os.Stat(cachePath)
		return err == nil && time.Since(info.ModTime()) < ttl && (image.Size <= 0 || info.Size() == image.Size)
	}
	if fresh() {
		return cachePath, "cache", nil
	}
	if len(remote) == 0 {
		return "", "", errNoSource
	}
	unlock := fillLocks.Lock(cachePath)
	defer unlock()
	if fresh() {
		return cachePath, "cache", nil
	}
	var lastErr error = errNoSource
	for _, c := range remote {
		if !s.health.Available(c.storage.ID, c.storage.OfflineUntil) {
			continue
		}
		err := s.fillCache(ctx, c, runtime, cachePath, image.Size, firstNonEmpty(expected, c.checksum))
		if err == nil {
			s.health.OK(c.storage.ID)
			return cachePath, "remote", nil
		}
		lastErr = err
		switch {
		case errors.Is(err, ErrObjectNotFound):
			s.markCopy(ctx, image.ID, c.storage.ID, "missing", err)
		case errors.Is(err, errChecksum):
			s.markCopy(ctx, image.ID, c.storage.ID, "failed", err)
		default:
			s.health.Fail(c.storage.ID, err)
		}
	}
	// Every remote copy is unreachable: an expired cache entry is still the
	// right bytes, so serve it rather than fail.
	if info, err := os.Stat(cachePath); err == nil && (image.Size <= 0 || info.Size() == image.Size) {
		return cachePath, "stale-cache", nil
	}
	return "", "", lastErr
}

var errChecksum = errors.New("副本内容校验失败")

func (s *Service) fillCache(ctx context.Context, c copyRef, runtime Options, cachePath string, size int64, expected string) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout(size))
	defer cancel()
	reader, err := s.manager.Open(ctx, c.storage.Options(runtime), c.storage.Mode(), c.key)
	if err != nil {
		return err
	}
	defer reader.Close()
	staged, hash, err := stageReplica(reader, size)
	if err != nil {
		return err
	}
	defer func() { staged.Close(); os.Remove(staged.Name()) }()
	if expected != "" && hash != expected {
		return errChecksum
	}
	return writeAtomicSized(cachePath, staged, size)
}

// opTimeout bounds one transfer: a minute plus time for ~200 KB/s.
func opTimeout(size int64) time.Duration {
	if size < 0 {
		size = 0
	}
	return time.Minute + time.Duration(size/(200<<10))*time.Second
}

// stageReplica copies exactly size bytes to a temporary file and hashes them.
func stageReplica(reader io.Reader, size int64) (*os.File, string, error) {
	f, err := os.CreateTemp("", "origin-copy-*")
	if err != nil {
		return nil, "", err
	}
	cleanup := func() { f.Close(); os.Remove(f.Name()) }
	limit := size + 1
	if size <= 0 {
		limit = 1 << 40
	}
	n, err := io.Copy(f, io.LimitReader(reader, limit))
	if err != nil {
		cleanup()
		return nil, "", err
	}
	if size > 0 && n != size {
		cleanup()
		return nil, "", fmt.Errorf("%w：大小不符，期望 %d，实际 %d", errChecksum, size, n)
	}
	checksum, err := ChecksumSHA256Reader(f)
	if err != nil {
		cleanup()
		return nil, "", err
	}
	return f, checksum, nil
}

// openSource finds a verified copy of an image to copy from, excluding one
// storage. The local read cache is the last resort.
func (s *Service) openSource(ctx context.Context, image *models.Image, expected string, exclude uint) (*os.File, string, error) {
	copies, err := s.readyCopies(ctx, image.ID, exclude)
	if err != nil {
		return nil, "", err
	}
	runtime := s.RuntimeOptions()
	for _, c := range copies {
		if !s.health.Available(c.storage.ID, c.storage.OfflineUntil) {
			continue
		}
		reader, err := s.manager.Open(ctx, c.storage.Options(runtime), c.storage.Mode(), c.key)
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				s.markCopy(ctx, image.ID, c.storage.ID, "missing", err)
			} else {
				s.health.Fail(c.storage.ID, err)
			}
			continue
		}
		staged, hash, err := stageReplica(reader, image.Size)
		reader.Close()
		if errors.Is(err, errChecksum) {
			s.markCopy(ctx, image.ID, c.storage.ID, "failed", err)
			continue
		}
		if err != nil {
			s.health.Fail(c.storage.ID, err)
			continue
		}
		want := firstNonEmpty(expected, c.checksum)
		if want != "" && hash != want {
			staged.Close()
			os.Remove(staged.Name())
			s.markCopy(ctx, image.ID, c.storage.ID, "failed", errChecksum)
			continue
		}
		s.health.OK(c.storage.ID)
		return staged, hash, nil
	}
	if cached, err := os.Open(s.manager.CachePath(runtime, image.PublicName)); err == nil {
		staged, hash, err := stageReplica(cached, image.Size)
		cached.Close()
		if err == nil && (expected == "" || hash == expected) {
			return staged, hash, nil
		}
		if err == nil {
			staged.Close()
			os.Remove(staged.Name())
		}
	}
	return nil, "", errNoSource
}

// PurgeImage permanently removes a recycled image. Remote objects are handed
// to the deletion queue, so an unreachable storage never blocks the purge.
func (s *Service) PurgeImage(ctx context.Context, imageID uint) error {
	var image models.Image
	err := s.db.QueryRowContext(ctx, `SELECT id, public_name, thumb_small, thumb_medium, thumb_large FROM images
		WHERE id=? AND deleted_at IS NOT NULL AND delete_after <= NOW()`, imageID).
		Scan(&image.ID, &image.PublicName, &image.ThumbSmall, &image.ThumbMedium, &image.ThumbLarge)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO storage_gc (storage_id, object_key)
		SELECT c.storage_id, c.object_key FROM image_copies c JOIN storages st ON st.id=c.storage_id
		WHERE c.image_id=? AND c.object_key<>''`, image.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_copies WHERE image_id=?`, image.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM images WHERE id=?`, image.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	runtime := s.RuntimeOptions()
	for _, key := range []string{image.ThumbSmall, image.ThumbMedium, image.ThumbLarge} {
		if key != "" && !filepath.IsAbs(key) {
			_ = os.Remove(filepath.Join(runtime.ThumbDir, filepath.Clean(key)))
		}
	}
	_ = os.Remove(s.manager.CachePath(runtime, image.PublicName))
	s.Wake()
	return nil
}

// DiscardUpload removes an object written during a failed upload.
func (s *Service) DiscardUpload(ctx context.Context, st *Storage, key string) {
	if key != "" {
		_ = s.manager.DeleteObject(ctx, s.Options(st), st.Mode(), key)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
