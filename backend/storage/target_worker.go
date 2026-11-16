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

func (c *Coordinator) openReplica(ctx context.Context, opts Options, mode, key string) (io.ReadCloser, error) {
	if mode == "local" {
		return os.Open(c.manager.LocalPath(opts, key))
	}
	return NewS3Client(opts.S3).OpenObject(ctx, key)
}

// Copies use disk-backed staging with an exact byte limit instead of buffering whole originals.
func stageReplica(reader io.Reader, size int64) (*os.File, string, error) {
	f, err := os.CreateTemp("", "orimg-replica-*")
	if err != nil {
		return nil, "", err
	}
	cleanup := func() { f.Close(); os.Remove(f.Name()) }
	n, err := io.Copy(f, io.LimitReader(reader, size+1))
	if err != nil {
		cleanup()
		return nil, "", err
	}
	if n != size {
		cleanup()
		return nil, "", fmt.Errorf("副本大小不符: expected %d, received %d", size, n)
	}
	checksum, err := ChecksumSHA256Reader(f)
	if err != nil {
		cleanup()
		return nil, "", err
	}
	return f, checksum, nil
}

func (c *Coordinator) imageSource(ctx context.Context, imageID uint, skipTarget uint) (io.ReadCloser, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT target_id,storage_key,is_primary FROM storage_target_replicas WHERE image_id=? AND target_id<>? AND (status IN ('ready','verifying') OR (is_primary=1 AND status='syncing')) ORDER BY is_primary DESC,target_id`, imageID, skipTarget)
	if err != nil {
		return nil, err
	}
	type ref struct {
		id      uint
		key     string
		primary bool
	}
	refs := []ref{}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.key, &r.primary); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		if !r.primary {
			continue
		}
		t, err := c.Target(ctx, r.id)
		if err != nil {
			continue
		}
		reader, err := c.openReplica(ctx, c.TargetOptions(t), t.Mode(), r.key)
		if err == nil {
			return reader, nil
		}
	}
	rows, err = c.db.QueryContext(ctx, "SELECT storage_mode,storage_key FROM image_replicas WHERE image_id=? AND status='ready' ORDER BY storage_mode", imageID)
	if err != nil {
		return nil, err
	}
	type oldRef struct{ mode, key string }
	old := []oldRef{}
	for rows.Next() {
		var r oldRef
		if err := rows.Scan(&r.mode, &r.key); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, r := range old {
		reader, err := c.openReplica(ctx, c.options(), r.mode, r.key)
		if err == nil {
			return reader, nil
		}
	}
	for _, r := range refs {
		if r.primary {
			continue
		}
		t, err := c.Target(ctx, r.id)
		if err != nil {
			continue
		}
		reader, err := c.openReplica(ctx, c.TargetOptions(t), t.Mode(), r.key)
		if err == nil {
			return reader, nil
		}
	}
	return nil, errors.New("没有可读取的完整副本；源存储可能不可达")
}

func (c *Coordinator) processTargetReplica(ctx context.Context) error {
	if c.cfg.DisableBackgroundWork {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	_, err := c.db.ExecContext(ctx, `UPDATE storage_target_replicas SET status=IF(lease_token LIKE 'verify:%','verify_failed','failed'),next_retry_at=NOW(),last_error='任务中断，可重试',lease_token='' WHERE status='syncing' AND lease_expires_at<NOW()`)
	if err != nil {
		return err
	}
	var image models.Image
	var targetID uint
	var status, expected, key string
	err = c.db.QueryRowContext(ctx, `SELECT r.image_id,r.target_id,r.status,r.storage_key,i.public_name,i.mime_type,i.size,i.checksum_sha256 FROM storage_target_replicas r JOIN storage_targets t ON t.id=r.target_id JOIN images i ON i.id=r.image_id
 WHERE t.enabled=1 AND i.deleted_at IS NULL AND r.attempts<8 AND r.status IN ('pending','failed','verifying','verify_failed') AND (r.next_retry_at IS NULL OR r.next_retry_at<=NOW()) ORDER BY r.created_at,r.image_id LIMIT 1`).Scan(&image.ID, &targetID, &status, &key, &image.PublicName, &image.MimeType, &image.Size, &expected)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	unlock, err := c.lockImage(ctx, image.ID)
	if err != nil {
		return err
	}
	defer unlock()
	token, err := utils.RandomHex(16)
	if err != nil {
		return err
	}
	verify := status == "verifying" || status == "verify_failed"
	if verify {
		token = "verify:" + token
	}
	result, err := c.db.ExecContext(ctx, `UPDATE storage_target_replicas r JOIN images i ON i.id=r.image_id SET r.status='syncing',r.lease_token=?,r.lease_expires_at=DATE_ADD(NOW(),INTERVAL 15 MINUTE),r.attempts=r.attempts+1 WHERE r.image_id=? AND r.target_id=? AND r.status=? AND i.deleted_at IS NULL`, token, image.ID, targetID, status)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	checksum, copyErr := c.copyTargetReplica(ctx, &image, targetID, key, verify, expected)
	if copyErr != nil {
		failedStatus := "failed"
		if verify {
			failedStatus = "verify_failed"
		}
		_, err = c.db.ExecContext(ctx, `UPDATE storage_target_replicas SET status=?,last_error=?,next_retry_at=DATE_ADD(NOW(),INTERVAL 5 MINUTE),lease_token='',lease_expires_at=NULL WHERE image_id=? AND target_id=? AND lease_token=?`, failedStatus, truncateError(copyErr), image.ID, targetID, token)
		return err
	}
	_, err = c.db.ExecContext(ctx, `UPDATE storage_target_replicas SET status='ready',checksum_sha256=?,size=?,last_error='',verified_at=NOW(),next_retry_at=NULL,lease_token='',lease_expires_at=NULL WHERE image_id=? AND target_id=? AND lease_token=?`, checksum, image.Size, image.ID, targetID, token)
	if err == nil {
		_, err = c.db.ExecContext(ctx, "UPDATE images SET checksum_sha256=? WHERE id=? AND checksum_sha256=''", checksum, image.ID)
	}
	return err
}

func (c *Coordinator) copyTargetReplica(ctx context.Context, image *models.Image, targetID uint, key string, verify bool, expected string) (string, error) {
	t, err := c.Target(ctx, targetID)
	if err != nil {
		return "", err
	}
	opts := c.TargetOptions(t)
	if key == "" {
		key = targetObjectKey(t, image)
	}
	// Persist intent before writing, so cleanup can find objects after a crash.
	if _, err = c.db.ExecContext(ctx, "UPDATE storage_target_replicas SET storage_key=? WHERE image_id=? AND target_id=?", key, image.ID, targetID); err != nil {
		return "", err
	}
	var checksum string
	if !verify {
		reader, err := c.imageSource(ctx, image.ID, targetID)
		if err != nil {
			return "", err
		}
		staged, hash, err := stageReplica(reader, image.Size)
		reader.Close()
		if err != nil {
			return "", err
		}
		defer func() { staged.Close(); os.Remove(staged.Name()) }()
		if expected != "" && hash != expected {
			return "", errors.New("源副本校验失败，已停止写入")
		}
		checksum = hash
		if t.Mode() == "local" {
			if err = c.checkDisk(opts); err != nil {
				return "", err
			}
			err = writeAtomicReader(c.manager.LocalPath(opts, key), staged)
		} else {
			err = NewS3Client(opts.S3).PutObjectReader(ctx, key, image.MimeType, image.Size, staged, hash)
		}
		if err != nil {
			return "", err
		}
	}
	reader, err := c.openReplica(ctx, opts, t.Mode(), key)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	verified, hash, err := stageReplica(reader, image.Size)
	if err != nil {
		return "", err
	}
	verified.Close()
	os.Remove(verified.Name())
	if (expected != "" && hash != expected) || (checksum != "" && hash != checksum) {
		return "", errors.New("目标副本 SHA-256 校验失败")
	}
	return hash, nil
}

// Purge and multi-target writes take the same per-image lock, including across processes.
func (c *Coordinator) lockImage(ctx context.Context, id uint) (func(), error) {
	return c.storageLock(ctx, fmt.Sprintf("orimg-image-%s-%d", c.cfg.DBName, id))
}

func (c *Coordinator) LockTargetConfig(ctx context.Context) (func(), error) {
	return c.storageLock(ctx, "orimg-storage-"+c.cfg.DBName)
}

func (c *Coordinator) storageLock(ctx context.Context, name string) (func(), error) {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var acquired int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&acquired); err != nil || acquired != 1 {
		conn.Close()
		return nil, errors.New("存储操作正在执行，请稍后重试")
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(releaseCtx, "SELECT RELEASE_LOCK(?)", name)
		conn.Close()
	}, nil
}

func (c *Coordinator) NamedImagePath(ctx context.Context, image *models.Image) (string, bool, error) {
	var count int
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM storage_target_replicas WHERE image_id=? AND (is_primary=1 OR status='ready')", image.ID).Scan(&count); err != nil {
		return "", false, err
	}
	if count == 0 {
		return "", false, nil
	}
	var primaryID uint
	var primaryKey string
	if err := c.db.QueryRowContext(ctx, "SELECT target_id,storage_key FROM storage_target_replicas WHERE image_id=? AND is_primary=1 AND status IN ('ready','verifying') LIMIT 1", image.ID).Scan(&primaryID, &primaryKey); err == nil {
		if target, err := c.Target(ctx, primaryID); err == nil && target.Mode() == "local" {
			localPath := c.manager.LocalPath(c.TargetOptions(target), primaryKey)
			if info, err := os.Stat(localPath); err == nil && info.Size() == image.Size {
				return localPath, true, nil
			}
		}
	}
	path := c.manager.CachePath(c.options(), image.PublicName)
	if info, err := os.Stat(path); err == nil && info.Size() == image.Size {
		return path, true, nil
	}
	unlock := fillLocks.Lock(path)
	defer unlock()
	if info, err := os.Stat(path); err == nil && info.Size() == image.Size {
		return path, true, nil
	}
	reader, err := c.imageSource(ctx, image.ID, 0)
	if err != nil {
		return "", true, err
	}
	defer reader.Close()
	staged, hash, err := stageReplica(reader, image.Size)
	if err != nil {
		return "", true, err
	}
	defer func() { staged.Close(); os.Remove(staged.Name()) }()
	var expected string
	if err = c.db.QueryRowContext(ctx, "SELECT checksum_sha256 FROM images WHERE id=?", image.ID).Scan(&expected); err != nil {
		return "", true, err
	}
	if expected != "" && hash != expected {
		return "", true, errors.New("图片校验失败")
	}
	if err = writeAtomicReader(path, staged); err != nil {
		return "", true, err
	}
	return path, true, nil
}

func (c *Coordinator) purgeTargetReplicas(ctx context.Context, imageID uint) error {
	rows, err := c.db.QueryContext(ctx, "SELECT target_id,storage_key FROM storage_target_replicas WHERE image_id=?", imageID)
	if err != nil {
		return err
	}
	type ref struct {
		id  uint
		key string
	}
	refs := []ref{}
	for rows.Next() {
		var r ref
		if err = rows.Scan(&r.id, &r.key); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r.key == "" {
			continue
		}
		t, err := c.Target(ctx, r.id)
		if err != nil {
			return err
		}
		if err = c.manager.DeleteReplica(ctx, c.TargetOptions(t), t.Mode(), r.key); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) ProbeTarget(ctx context.Context, id uint) error {
	t, err := c.Target(ctx, id)
	if err != nil {
		return err
	}
	token, err := utils.RandomHex(16)
	if err != nil {
		return err
	}
	key := filepath.ToSlash(filepath.Join("orimg-probes", token))
	opts := c.TargetOptions(t)
	data := []byte("Origin storage connection probe " + token)
	if _, err = c.manager.WriteReplica(ctx, opts, t.Mode(), key, "text/plain", data); err != nil {
		return err
	}
	reader, readErr := c.openReplica(ctx, opts, t.Mode(), key)
	if readErr == nil {
		var body []byte
		body, readErr = io.ReadAll(io.LimitReader(reader, 1024))
		reader.Close()
		if readErr == nil && ChecksumSHA256(body) != ChecksumSHA256(data) {
			readErr = errors.New("读回校验失败")
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deleteErr := c.manager.DeleteReplica(cleanupCtx, opts, t.Mode(), key)
	if deleteErr != nil {
		return fmt.Errorf("测试文件未能清理 (%s)，请检查删除权限", key)
	}
	return readErr
}
