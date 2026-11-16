package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zhuapic/config"
	"zhuapic/models"
	"zhuapic/utils"
)

type Coordinator struct {
	db      *sql.DB
	cfg     *config.Config
	manager *Manager
}

func NewCoordinator(db *sql.DB, cfg *config.Config, manager *Manager) *Coordinator {
	return &Coordinator{db: db, cfg: cfg, manager: manager}
}

func (c *Coordinator) Start(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	c.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

func (c *Coordinator) runOnce(ctx context.Context) {
	if c.cfg.DisableBackgroundWork {
		return
	}
	_ = c.expireDueImages(ctx)
	_ = c.purgeExpiredImage(ctx)
	_ = c.ensureScheduledDatabaseBackup(ctx)
	_ = c.processDatabaseBackup(ctx)
	if !utils.SettingBool(c.db, "replica_worker_enabled", true) {
		return
	}
	_, _ = c.db.ExecContext(ctx, `UPDATE image_replicas SET status = 'failed', last_error = 'worker interrupted', next_retry_at = NOW(), lease_token = '', lease_expires_at = NULL WHERE status = 'syncing' AND (lease_expires_at IS NULL OR lease_expires_at < NOW())`)
	_, _ = c.db.ExecContext(ctx, `UPDATE storage_job_items SET status = 'failed', last_error = 'worker interrupted', next_retry_at = NOW(), lease_token = '', lease_expires_at = NULL WHERE status = 'running' AND lease_expires_at < NOW()`)
	_, _ = c.db.ExecContext(ctx, `UPDATE storage_job_items SET status = 'queued', last_error = 'worker interrupted', next_retry_at = NOW() WHERE status = 'running' AND updated_at < DATE_SUB(NOW(), INTERVAL 10 MINUTE)`)
	_ = c.processPendingReplica(ctx)
	_ = c.processJob(ctx)
	_ = c.processTargetReplica(ctx)
}

func (c *Coordinator) expireDueImages(ctx context.Context) error {
	days := utils.SettingInt64(c.db, "recycle_bin_days", 7, 1, 30)
	_, err := c.db.ExecContext(ctx, `UPDATE images SET deleted_at = NOW(),
		delete_after = DATE_ADD(NOW(), INTERVAL ? DAY)
		WHERE deleted_at IS NULL AND expires_at IS NOT NULL AND expires_at <= NOW() LIMIT 100`, days)
	return err
}

func (c *Coordinator) options() Options {
	settings := utils.GetSettings(c.db)
	opts := c.manager.DefaultOptions()
	opts.UploadDir = firstSetting(settings["local_upload_dir"], c.cfg.UploadDir)
	opts.CacheDir = firstSetting(settings["s3_cache_dir"], c.cfg.CacheDir)
	opts.ThumbDir = firstSetting(settings["thumb_dir"], "data/thumbs")
	opts.S3Prefix = firstSetting(settings["s3_key_prefix"], "images")
	opts.S3.Endpoint = strings.TrimRight(firstSetting(settings["s3_endpoint"], c.cfg.S3.Endpoint), "/")
	opts.S3.Region = firstSetting(settings["s3_region"], c.cfg.S3.Region)
	opts.S3.Bucket = firstSetting(settings["s3_bucket"], c.cfg.S3.Bucket)
	opts.S3.AccessKey = firstSetting(settings["s3_access_key"], c.cfg.S3.AccessKey)
	opts.S3.SecretKey = firstSetting(settings["s3_secret_key"], c.cfg.S3.SecretKey)
	if decrypted, err := utils.DecryptSecret(opts.S3.SecretKey, c.cfg.JWTSecret); err == nil {
		opts.S3.SecretKey = decrypted
	} else {
		opts.S3.SecretKey = ""
	}
	opts.S3.ForcePathStyle = boolSetting(settings["s3_force_path_style"], c.cfg.S3.ForcePathStyle)
	return opts
}

func (c *Coordinator) RegisterImage(ctx context.Context, image *models.Image, checksum string) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert := func(mode, key, status string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO image_replicas
			(image_id, storage_mode, storage_key, status, checksum_sha256, size, last_error)
			VALUES (?, ?, ?, ?, ?, ?, '')
			ON DUPLICATE KEY UPDATE storage_key = VALUES(storage_key), status = VALUES(status), checksum_sha256 = VALUES(checksum_sha256), size = VALUES(size), last_error = ''`,
			image.ID, mode, key, status, checksum, image.Size)
		return err
	}
	switch image.StorageMode {
	case "local":
		err = insert("local", image.StorageKey, "ready")
	case "s3":
		err = insert("s3", image.StorageKey, "ready")
	case "mirror":
		if err = insert("local", image.StorageKey, "ready"); err == nil {
			s3Key := c.manager.ReplicaKey(c.options(), "s3", image.CreatedAt, image.PublicName)
			err = insert("s3", s3Key, "pending")
		}
	default:
		err = errors.New("unsupported storage mode")
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE images SET checksum_sha256 = ? WHERE id = ?`, checksum, image.ID)
	if err != nil {
		return err
	}
	if err = c.enqueueAutomatic(ctx, tx, image.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Coordinator) EnsureMirrorLocal(ctx context.Context, image *models.Image) (string, bool, error) {
	opts := c.options()
	var localKey string
	err := c.db.QueryRowContext(ctx, `SELECT storage_key FROM image_replicas WHERE image_id = ? AND storage_mode = 'local' AND status = 'ready'`, image.ID).Scan(&localKey)
	if err == nil {
		path := c.manager.LocalPath(opts, localKey)
		if _, statErr := os.Stat(path); statErr == nil {
			return path, true, nil
		}
	}
	var s3Key string
	if err := c.db.QueryRowContext(ctx, `SELECT storage_key FROM image_replicas WHERE image_id = ? AND storage_mode = 's3' AND status = 'ready'`, image.ID).Scan(&s3Key); err != nil {
		return "", false, errors.New("no healthy replica available")
	}
	body, err := c.manager.ReadBytes(ctx, opts, "s3", s3Key)
	if err != nil {
		return "", false, err
	}
	if localKey == "" {
		localKey = c.manager.ReplicaKey(opts, "local", image.CreatedAt, image.PublicName)
	}
	if _, err := c.manager.WriteReplica(ctx, opts, "local", localKey, image.MimeType, body); err != nil {
		return "", false, err
	}
	checksum := ChecksumSHA256(body)
	_, err = c.db.ExecContext(ctx, `INSERT INTO image_replicas
		(image_id, storage_mode, storage_key, status, checksum_sha256, size, last_error, last_verified_at)
		VALUES (?, 'local', ?, 'ready', ?, ?, '', NOW())
		ON DUPLICATE KEY UPDATE storage_key = VALUES(storage_key), status = 'ready', checksum_sha256 = VALUES(checksum_sha256), size = VALUES(size), last_error = '', last_verified_at = NOW()`,
		image.ID, localKey, checksum, len(body))
	if err != nil {
		return "", false, err
	}
	return c.manager.LocalPath(opts, localKey), false, nil
}

func (c *Coordinator) ReadyReplicaKey(ctx context.Context, imageID uint, mode string) (string, error) {
	if !validReplicaMode(mode) {
		return "", errors.New("invalid replica mode")
	}
	var key string
	err := c.db.QueryRowContext(ctx, `SELECT storage_key FROM image_replicas WHERE image_id = ? AND storage_mode = ? AND status = 'ready'`, imageID, mode).Scan(&key)
	return key, err
}

func (c *Coordinator) CreateMigrationJob(ctx context.Context, source, target string, userID uint) (uint, error) {
	if !validReplicaMode(source) || !validReplicaMode(target) || source == target {
		return 0, errors.New("invalid migration direction")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO storage_jobs (type, source_mode, target_mode, status, created_by, last_error) VALUES ('migrate', ?, ?, 'queued', NULLIF(?, 0), '')`, source, target, userID)
	if err != nil {
		return 0, err
	}
	jobID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO storage_job_items (job_id, image_id, source_mode, target_mode, status, size, last_error)
		SELECT ?, images.id, ?, ?, 'queued', images.size, ''
		FROM images
		JOIN image_replicas source ON source.image_id = images.id AND source.storage_mode = ? AND source.status = 'ready'
		LEFT JOIN image_replicas target ON target.image_id = images.id AND target.storage_mode = ? AND target.status = 'ready'
		WHERE images.deleted_at IS NULL AND target.id IS NULL`, jobID, source, target, source, target)
	if err != nil {
		return 0, err
	}
	if err := updateJobTotals(ctx, tx, uint(jobID)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return uint(jobID), nil
}

func (c *Coordinator) CreateVerifyJob(ctx context.Context, mode string, userID uint) (uint, error) {
	if mode != "all" && !validReplicaMode(mode) {
		return 0, errors.New("invalid verify mode")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO storage_jobs (type, source_mode, target_mode, status, created_by, last_error) VALUES ('verify', ?, ?, 'queued', NULLIF(?, 0), '')`, mode, mode, userID)
	if err != nil {
		return 0, err
	}
	jobID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	where := ""
	args := []any{jobID}
	if mode != "all" {
		where = " AND replicas.storage_mode = ?"
		args = append(args, mode)
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO storage_job_items (job_id, image_id, source_mode, target_mode, status, size, last_error)
		SELECT ?, images.id, replicas.storage_mode, replicas.storage_mode, 'queued', images.size, ''
		FROM images JOIN image_replicas replicas ON replicas.image_id = images.id AND replicas.status = 'ready'
		WHERE images.deleted_at IS NULL`+where, args...)
	if err != nil {
		return 0, err
	}
	if err := updateJobTotals(ctx, tx, uint(jobID)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return uint(jobID), nil
}

func (c *Coordinator) CreateDatabaseBackupJob(ctx context.Context, userID uint) (uint, error) {
	unlock, err := c.LockTargetConfig(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	var existing uint
	if err := c.db.QueryRowContext(ctx, `SELECT id FROM storage_jobs WHERE type='db_backup' AND status IN ('queued','running') LIMIT 1`).Scan(&existing); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	target := "s3"
	if id := utils.SettingInt64(c.db, "db_backup_target_id", 0, 0, 2147483647); id > 0 {
		target = TargetSelection(uint(id))
	}
	if _, _, err := c.databaseBackupDestination(ctx, target); err != nil {
		return 0, err
	}
	res, err := c.db.ExecContext(ctx, `INSERT INTO storage_jobs
		(type, source_mode, target_mode, status, total_items, created_by, last_error)
		VALUES ('db_backup', 'mysql', ?, 'queued', 1, NULLIF(?, 0), '')`, target, userID)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return uint(id), err
}

func updateJobTotals(ctx context.Context, tx *sql.Tx, jobID uint) error {
	_, err := tx.ExecContext(ctx, `UPDATE storage_jobs SET
		total_items = (SELECT COUNT(*) FROM storage_job_items WHERE job_id = ?),
		total_bytes = COALESCE((SELECT SUM(size) FROM storage_job_items WHERE job_id = ?), 0)
		WHERE id = ?`, jobID, jobID, jobID)
	return err
}

func (c *Coordinator) ListJobs(ctx context.Context, limit int) ([]models.StorageJob, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, err := c.db.QueryContext(ctx, `SELECT id, type, source_mode, target_mode, status, total_items, processed_items, success_items, failed_items,
		total_bytes, processed_bytes, last_error, COALESCE(created_by, 0), started_at, completed_at, created_at, updated_at
		FROM storage_jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []models.StorageJob{}
	for rows.Next() {
		var job models.StorageJob
		if err := rows.Scan(&job.ID, &job.Type, &job.SourceMode, &job.TargetMode, &job.Status, &job.TotalItems, &job.ProcessedItems,
			&job.SuccessItems, &job.FailedItems, &job.TotalBytes, &job.ProcessedBytes, &job.LastError, &job.CreatedBy,
			&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (c *Coordinator) ControlJob(ctx context.Context, id uint, action string) error {
	switch action {
	case "pause":
		_, err := c.db.ExecContext(ctx, `UPDATE storage_jobs SET status = 'paused' WHERE id = ? AND type<>'db_backup' AND status IN ('queued', 'running')`, id)
		return err
	case "resume":
		_, err := c.db.ExecContext(ctx, `UPDATE storage_jobs SET status = 'running' WHERE id = ? AND type<>'db_backup' AND status = 'paused'`, id)
		return err
	case "retry":
		var jobType string
		if err := c.db.QueryRowContext(ctx, `SELECT type FROM storage_jobs WHERE id = ?`, id).Scan(&jobType); err != nil {
			return err
		}
		if jobType == "db_backup" {
			unlock, err := c.LockTargetConfig(ctx)
			if err != nil {
				return err
			}
			defer unlock()
			var active int
			if err = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_jobs WHERE type='db_backup' AND status IN ('queued','running')`).Scan(&active); err != nil {
				return err
			}
			if active > 0 {
				return errors.New("已有数据库备份任务，请等待完成")
			}
			_, err = c.db.ExecContext(ctx, `UPDATE storage_jobs SET status = 'queued', processed_items = 0, success_items = 0, failed_items = 0, processed_bytes = 0, completed_at = NULL, last_error = '' WHERE id = ? AND status='completed_with_errors'`, id)
			return err
		}
		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `UPDATE storage_job_items SET status = 'queued', attempts = 0, last_error = '', next_retry_at = NULL WHERE job_id = ? AND status = 'failed'`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE storage_jobs SET status = 'running', failed_items = 0, completed_at = NULL, last_error = '' WHERE id = ?`, id); err != nil {
			return err
		}
		return tx.Commit()
	default:
		return errors.New("invalid job action")
	}
}

func (c *Coordinator) ensureScheduledDatabaseBackup(ctx context.Context) error {
	if !utils.SettingBool(c.db, "db_backup_enabled", false) {
		return nil
	}
	now := time.Now()
	hour := int(utils.SettingInt64(c.db, "db_backup_hour", 4, 0, 23))
	if now.Hour() != hour {
		return nil
	}
	var count int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_jobs WHERE type = 'db_backup' AND DATE(created_at) = CURDATE()`).Scan(&count); err != nil || count > 0 {
		return err
	}
	_, err := c.CreateDatabaseBackupJob(ctx, 0)
	return err
}

func (c *Coordinator) processDatabaseBackup(ctx context.Context) error {
	_, _ = c.db.ExecContext(ctx, `UPDATE storage_jobs SET status='completed_with_errors',processed_items=1,failed_items=1,last_error='备份执行中断或超时，请重新备份',completed_at=NOW() WHERE type='db_backup' AND status='running' AND started_at<DATE_SUB(NOW(),INTERVAL 35 MINUTE)`)
	var jobID uint
	var target string
	err := c.db.QueryRowContext(ctx, `SELECT id,target_mode FROM storage_jobs WHERE type = 'db_backup' AND status = 'queued' ORDER BY created_at ASC LIMIT 1`).Scan(&jobID, &target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.db.ExecContext(ctx, `UPDATE storage_jobs SET status = 'running', started_at = NOW() WHERE id = ? AND status = 'queued'`, jobID)
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	key, size, runErr := c.runDatabaseBackup(runCtx, target, jobID)
	if runErr != nil {
		_, _ = c.db.ExecContext(ctx, `UPDATE storage_jobs SET status = 'completed_with_errors', processed_items = 1, failed_items = 1, last_error = ?, completed_at = NOW() WHERE id = ?`, truncateError(runErr), jobID)
		return runErr
	}
	_, err = c.db.ExecContext(ctx, `UPDATE storage_jobs SET status = 'completed', processed_items = 1, success_items = 1, total_bytes = ?, processed_bytes = ?, last_error = ?, completed_at = NOW() WHERE id = ?`, size, size, "stored: "+key, jobID)
	return err
}

func (c *Coordinator) databaseBackupDestination(ctx context.Context, target string) (Options, string, error) {
	opts, mode := c.options(), "s3"
	if target != "s3" {
		id, err := ParseTargetSelection(target)
		if err != nil {
			return opts, mode, err
		}
		t, err := c.Target(ctx, id)
		if err != nil {
			return opts, mode, err
		}
		if !t.Enabled {
			return opts, mode, errors.New("备份存储已停用")
		}
		opts, mode = c.TargetOptions(t), t.Mode()
	}
	if mode == "s3" && !c.manager.S3Configured(opts) {
		return opts, mode, errors.New("备份存储未配置完整")
	}
	return opts, mode, nil
}

func (c *Coordinator) runDatabaseBackup(ctx context.Context, target string, jobID uint) (string, int64, error) {
	opts, mode, err := c.databaseBackupDestination(ctx, target)
	if err != nil {
		return "", 0, err
	}
	dumpPath := utils.GetSetting(c.db, "mysqldump_path", "mysqldump")
	resolved, err := exec.LookPath(dumpPath)
	if err != nil {
		return "", 0, errors.New("mysqldump was not found")
	}
	tempDir := filepath.Join(opts.CacheDir, "database-backups")
	if err := os.MkdirAll(tempDir, 0750); err != nil {
		return "", 0, err
	}
	temp, err := os.CreateTemp(tempDir, "zhuapic-*.sql.gz")
	if err != nil {
		return "", 0, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	gzipWriter := gzip.NewWriter(temp)
	var stderr bytes.Buffer
	args := []string{
		"--no-tablespaces", "--set-gtid-purged=OFF",
		"--single-transaction", "--quick", "--skip-lock-tables", "--hex-blob", "--routines", "--events", "--triggers",
		"--default-character-set=utf8mb4", "--protocol=TCP", "-h", c.cfg.DBHost, "-P", c.cfg.DBPort, "-u", c.cfg.DBUser}
	if c.cfg.DBTLS == "verify" {
		args = append(args, "--ssl-mode=VERIFY_IDENTITY")
		if c.cfg.DBTLSCA != "" {
			ca, err := os.CreateTemp(tempDir, "mysql-ca-*.pem")
			if err != nil {
				gzipWriter.Close()
				temp.Close()
				return "", 0, err
			}
			defer os.Remove(ca.Name())
			_, writeErr := ca.WriteString(c.cfg.DBTLSCA)
			closeErr := ca.Close()
			if writeErr != nil || closeErr != nil {
				gzipWriter.Close()
				temp.Close()
				return "", 0, errors.Join(writeErr, closeErr)
			}
			args = append(args, "--ssl-ca="+ca.Name())
		}
	}
	args = append(args, c.cfg.DBName)
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+c.cfg.DBPassword)
	cmd.Stdout = gzipWriter
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := gzipWriter.Close()
	fileCloseErr := temp.Close()
	if runErr != nil {
		return "", 0, fmt.Errorf("mysqldump failed: %s", strings.TrimSpace(stderr.String()))
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	if fileCloseErr != nil {
		return "", 0, fileCloseErr
	}
	body, err := os.Open(tempName)
	if err != nil {
		return "", 0, err
	}
	defer body.Close()
	stat, err := body.Stat()
	if err != nil {
		return "", 0, err
	}
	prefix := cleanObjectPrefix(utils.GetSetting(c.db, "db_backup_s3_prefix", "backups/database"))
	if prefix == "" {
		prefix = "backups/database"
	}
	key := joinObjectKey(prefix, fmt.Sprintf("origin-%s-%d.sql.gz", time.Now().Format("20060102-150405"), jobID))
	if err = writeDatabaseArchive(ctx, opts, mode, key, body, stat.Size()); err != nil {
		return "", 0, err
	}
	return key, stat.Size(), nil
}

func (c *Coordinator) Stats(ctx context.Context) (map[string]any, error) {
	stats := map[string]any{}
	var images, bytes, trash int64
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM images WHERE deleted_at IS NULL`).Scan(&images, &bytes); err != nil {
		return nil, err
	}
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images WHERE deleted_at IS NOT NULL`).Scan(&trash); err != nil {
		return nil, err
	}
	stats["images"] = images
	stats["bytes"] = bytes
	stats["trash"] = trash
	for _, mode := range []string{"local", "s3"} {
		var count, replicaBytes int64
		_ = c.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(replicas.size), 0) FROM image_replicas replicas JOIN images ON images.id = replicas.image_id AND images.deleted_at IS NULL WHERE replicas.storage_mode = ? AND replicas.status = 'ready'`, mode).Scan(&count, &replicaBytes)
		stats[mode+"_ready"] = count
		stats[mode+"_bytes"] = replicaBytes
	}
	var pending, failed int64
	_ = c.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(replicas.status IN ('pending', 'syncing')), 0), COALESCE(SUM(replicas.status IN ('failed', 'corrupt')), 0) FROM image_replicas replicas JOIN images ON images.id = replicas.image_id AND images.deleted_at IS NULL`).Scan(&pending, &failed)
	stats["pending"] = pending
	stats["failed"] = failed
	opts := c.options()
	if usage, err := DiskUsagePercent(opts.UploadDir); err == nil {
		stats["disk_used_percent"] = usage
	}
	stats["disk_stop_percent"] = utils.SettingInt64(c.db, "disk_stop_percent", 85, 50, 99)
	return stats, nil
}

func (c *Coordinator) CheckLocalCapacity() error {
	return c.checkDisk(c.options())
}

func (c *Coordinator) PurgeImage(ctx context.Context, imageID uint) error {
	return c.purgeImageByID(ctx, imageID)
}

func (c *Coordinator) processPendingReplica(ctx context.Context) error {
	var replicaID, imageID uint
	var targetMode, targetKey, sourceMode, sourceKey, mimeType, publicName, expected string
	var createdAt time.Time
	leaseToken, err := utils.RandomHex(16)
	if err != nil {
		return err
	}
	res, err := c.db.ExecContext(ctx, `UPDATE image_replicas target
		JOIN images ON images.id = target.image_id AND images.deleted_at IS NULL AND images.storage_mode = 'mirror'
		SET target.status = 'syncing', target.lease_token = ?, target.lease_expires_at = DATE_ADD(NOW(), INTERVAL 10 MINUTE)
		WHERE (target.status IN ('pending', 'failed') OR (target.status = 'syncing' AND target.lease_expires_at < NOW()))
			AND (target.next_retry_at IS NULL OR target.next_retry_at <= NOW()) AND target.attempts < 8
		ORDER BY target.updated_at ASC LIMIT 1`, leaseToken)
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return nil
	}
	err = c.db.QueryRowContext(ctx, `SELECT target.id, images.id, target.storage_mode, target.storage_key,
		source.storage_mode, source.storage_key, images.mime_type, images.public_name, images.created_at,
		COALESCE(NULLIF(images.checksum_sha256, ''), source.checksum_sha256)
		FROM image_replicas target
		JOIN images ON images.id = target.image_id AND images.deleted_at IS NULL AND images.storage_mode = 'mirror'
		JOIN image_replicas source ON source.image_id = images.id AND source.storage_mode <> target.storage_mode AND source.status = 'ready'
		WHERE target.lease_token = ? LIMIT 1`, leaseToken).Scan(&replicaID, &imageID, &targetMode, &targetKey, &sourceMode, &sourceKey, &mimeType, &publicName, &createdAt, &expected)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	opts := c.options()
	if targetKey == "" {
		targetKey = c.manager.ReplicaKey(opts, targetMode, createdAt, publicName)
	}
	body, err := c.manager.ReadBytes(ctx, opts, sourceMode, sourceKey)
	if err == nil && expected != "" && ChecksumSHA256(body) != expected {
		err = errors.New("source checksum mismatch")
	}
	if err == nil && targetMode == "local" {
		err = c.checkDisk(opts)
	}
	if err == nil {
		_, err = c.manager.WriteReplica(ctx, opts, targetMode, targetKey, mimeType, body)
	}
	if err != nil {
		return c.failReplica(ctx, replicaID, err)
	}
	checksum := ChecksumSHA256(body)
	_, err = c.db.ExecContext(ctx, `UPDATE image_replicas SET storage_key = ?, status = 'ready', checksum_sha256 = ?, size = ?, attempts = 0, last_error = '', next_retry_at = NULL, last_verified_at = NOW(), lease_token = '', lease_expires_at = NULL WHERE id = ? AND lease_token = ?`, targetKey, checksum, len(body), replicaID, leaseToken)
	return err
}

func (c *Coordinator) processJob(ctx context.Context) error {
	_, _ = c.db.ExecContext(ctx, `UPDATE storage_jobs SET status = 'running', started_at = COALESCE(started_at, NOW()) WHERE status = 'queued' ORDER BY created_at ASC LIMIT 1`)
	var itemID, jobID, imageID uint
	var jobType, sourceMode, targetMode string
	leaseToken, err := utils.RandomHex(16)
	if err != nil {
		return err
	}
	res, err := c.db.ExecContext(ctx, `UPDATE storage_job_items items
		JOIN storage_jobs jobs ON jobs.id = items.job_id AND jobs.status = 'running'
		SET items.status = 'running', items.lease_token = ?, items.lease_expires_at = DATE_ADD(NOW(), INTERVAL 15 MINUTE)
		WHERE (items.status IN ('queued', 'failed') OR (items.status = 'running' AND items.lease_expires_at < NOW()))
			AND items.attempts < 3 AND (items.next_retry_at IS NULL OR items.next_retry_at <= NOW())
		ORDER BY items.updated_at ASC LIMIT 1`, leaseToken)
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return c.finishIdleJobs(ctx)
	}
	err = c.db.QueryRowContext(ctx, `SELECT items.id, items.job_id, items.image_id, jobs.type, items.source_mode, items.target_mode
		FROM storage_job_items items JOIN storage_jobs jobs ON jobs.id = items.job_id AND jobs.status = 'running'
		WHERE items.lease_token = ? LIMIT 1`, leaseToken).Scan(&itemID, &jobID, &imageID, &jobType, &sourceMode, &targetMode)
	if errors.Is(err, sql.ErrNoRows) {
		return c.finishIdleJobs(ctx)
	}
	if err != nil {
		return err
	}
	err = c.runJobItem(ctx, jobType, imageID, sourceMode, targetMode)
	if err != nil {
		attempts := 1
		_ = c.db.QueryRowContext(ctx, `SELECT attempts + 1 FROM storage_job_items WHERE id = ?`, itemID).Scan(&attempts)
		delay := retryDelay(attempts)
		_, _ = c.db.ExecContext(ctx, `UPDATE storage_job_items SET status = 'failed', attempts = attempts + 1, last_error = ?, next_retry_at = ?, lease_token = '', lease_expires_at = NULL WHERE id = ? AND lease_token = ?`, truncateError(err), time.Now().Add(delay), itemID, leaseToken)
	} else {
		_, _ = c.db.ExecContext(ctx, `UPDATE storage_job_items SET status = 'completed', attempts = attempts + 1, last_error = '', next_retry_at = NULL, lease_token = '', lease_expires_at = NULL WHERE id = ? AND lease_token = ?`, itemID, leaseToken)
	}
	return c.refreshJob(ctx, jobID)
}

func (c *Coordinator) runJobItem(ctx context.Context, jobType string, imageID uint, sourceMode, targetMode string) error {
	var image models.Image
	var expected string
	err := c.db.QueryRowContext(ctx, `SELECT id, public_name, mime_type, size, created_at, checksum_sha256 FROM images WHERE id = ? AND deleted_at IS NULL`, imageID).
		Scan(&image.ID, &image.PublicName, &image.MimeType, &image.Size, &image.CreatedAt, &expected)
	if err != nil {
		return err
	}
	var sourceKey, sourceChecksum string
	if err := c.db.QueryRowContext(ctx, `SELECT storage_key, checksum_sha256 FROM image_replicas WHERE image_id = ? AND storage_mode = ? AND status = 'ready'`, imageID, sourceMode).Scan(&sourceKey, &sourceChecksum); err != nil {
		return err
	}
	opts := c.options()
	body, err := c.manager.ReadBytes(ctx, opts, sourceMode, sourceKey)
	if err != nil {
		return err
	}
	checksum := ChecksumSHA256(body)
	knownChecksum := firstSetting(expected, sourceChecksum)
	if knownChecksum != "" && checksum != knownChecksum {
		_, _ = c.db.ExecContext(ctx, `UPDATE image_replicas SET status = 'corrupt', last_error = 'checksum mismatch' WHERE image_id = ? AND storage_mode = ?`, imageID, sourceMode)
		return errors.New("checksum mismatch")
	}
	if jobType == "verify" {
		_, err = c.db.ExecContext(ctx, `UPDATE image_replicas SET checksum_sha256 = ?, size = ?, last_verified_at = NOW(), last_error = '' WHERE image_id = ? AND storage_mode = ?`, checksum, len(body), imageID, sourceMode)
		if err == nil && expected == "" {
			_, err = c.db.ExecContext(ctx, `UPDATE images SET checksum_sha256 = ? WHERE id = ? AND checksum_sha256 = ''`, checksum, imageID)
		}
		return err
	}
	if jobType != "migrate" {
		return errors.New("unsupported storage job")
	}
	if targetMode == "local" {
		if err := c.checkDisk(opts); err != nil {
			return err
		}
	}
	targetKey := c.manager.ReplicaKey(opts, targetMode, image.CreatedAt, image.PublicName)
	if _, err := c.manager.WriteReplica(ctx, opts, targetMode, targetKey, image.MimeType, body); err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO image_replicas (image_id, storage_mode, storage_key, status, checksum_sha256, size, last_error, last_verified_at)
		VALUES (?, ?, ?, 'ready', ?, ?, '', NOW())
		ON DUPLICATE KEY UPDATE storage_key = VALUES(storage_key), status = 'ready', checksum_sha256 = VALUES(checksum_sha256), size = VALUES(size), attempts = 0, last_error = '', next_retry_at = NULL, last_verified_at = NOW()`, imageID, targetMode, targetKey, checksum, len(body)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE images SET checksum_sha256 = IF(checksum_sha256 = '', ?, checksum_sha256) WHERE id = ?`, checksum, imageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Coordinator) refreshJob(ctx context.Context, jobID uint) error {
	_, err := c.db.ExecContext(ctx, `UPDATE storage_jobs SET
		processed_items = (SELECT COUNT(*) FROM storage_job_items WHERE job_id = ? AND status IN ('completed', 'failed')),
		success_items = (SELECT COUNT(*) FROM storage_job_items WHERE job_id = ? AND status = 'completed'),
		failed_items = (SELECT COUNT(*) FROM storage_job_items WHERE job_id = ? AND status = 'failed' AND attempts >= 3),
		processed_bytes = COALESCE((SELECT SUM(size) FROM storage_job_items WHERE job_id = ? AND status = 'completed'), 0),
		last_error = COALESCE((SELECT last_error FROM storage_job_items WHERE job_id = ? AND status = 'failed' ORDER BY updated_at DESC LIMIT 1), '')
		WHERE id = ?`, jobID, jobID, jobID, jobID, jobID, jobID)
	if err != nil {
		return err
	}
	return c.finishIdleJobs(ctx)
}

func (c *Coordinator) finishIdleJobs(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `UPDATE storage_jobs jobs SET status =
		CASE WHEN EXISTS (SELECT 1 FROM storage_job_items items WHERE items.job_id = jobs.id AND items.status = 'failed') THEN 'completed_with_errors' ELSE 'completed' END,
		completed_at = NOW()
		WHERE jobs.status = 'running'
		AND NOT EXISTS (SELECT 1 FROM storage_job_items items WHERE items.job_id = jobs.id AND (items.status IN ('queued', 'running') OR (items.status = 'failed' AND items.attempts < 3)))`)
	return err
}

func (c *Coordinator) failReplica(ctx context.Context, replicaID uint, cause error) error {
	var attempts int
	_ = c.db.QueryRowContext(ctx, `SELECT attempts + 1 FROM image_replicas WHERE id = ?`, replicaID).Scan(&attempts)
	_, err := c.db.ExecContext(ctx, `UPDATE image_replicas SET status = 'failed', attempts = attempts + 1, last_error = ?, next_retry_at = ?, lease_token = '', lease_expires_at = NULL WHERE id = ?`, truncateError(cause), time.Now().Add(retryDelay(attempts)), replicaID)
	return err
}

func (c *Coordinator) purgeExpiredImage(ctx context.Context) error {
	var imageID uint
	err := c.db.QueryRowContext(ctx, `SELECT id FROM images WHERE deleted_at IS NOT NULL AND delete_after <= NOW() ORDER BY delete_after ASC LIMIT 1`).Scan(&imageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.purgeImageByID(ctx, imageID)
}

func (c *Coordinator) purgeImageByID(ctx context.Context, imageID uint) error {
	unlock, lockErr := c.lockImage(ctx, imageID)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	var image models.Image
	err := c.db.QueryRowContext(ctx, `SELECT id, public_name, thumb_small, thumb_medium, thumb_large FROM images WHERE id = ? AND deleted_at IS NOT NULL AND delete_after <= NOW()`, imageID).
		Scan(&image.ID, &image.PublicName, &image.ThumbSmall, &image.ThumbMedium, &image.ThumbLarge)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.db.ExecContext(ctx, `UPDATE images SET delete_after = DATE_ADD(NOW(), INTERVAL 10 MINUTE) WHERE id = ? AND delete_after <= NOW()`, image.ID)
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return nil
	}
	rows, err := c.db.QueryContext(ctx, `SELECT storage_mode, storage_key FROM image_replicas WHERE image_id = ? AND status <> 'deleted'`, image.ID)
	if err != nil {
		return err
	}
	type replica struct{ mode, key string }
	replicas := []replica{}
	for rows.Next() {
		var item replica
		if err := rows.Scan(&item.mode, &item.key); err != nil {
			rows.Close()
			return err
		}
		replicas = append(replicas, item)
	}
	rows.Close()
	opts := c.options()
	if err := c.purgeTargetReplicas(ctx, image.ID); err != nil {
		return err
	}
	for _, item := range replicas {
		if err := c.manager.DeleteReplica(ctx, opts, item.mode, item.key); err != nil {
			_, _ = c.db.ExecContext(ctx, `UPDATE images SET delete_after = DATE_ADD(NOW(), INTERVAL 5 MINUTE) WHERE id = ?`, image.ID)
			return err
		}
	}
	for _, key := range []string{image.ThumbSmall, image.ThumbMedium, image.ThumbLarge} {
		if key != "" && !filepath.IsAbs(key) {
			_ = os.Remove(filepath.Join(opts.ThumbDir, filepath.Clean(key)))
		}
	}
	_ = os.Remove(c.manager.CachePath(opts, image.PublicName))
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM storage_target_replicas WHERE image_id = ?`, image.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_replicas WHERE image_id = ?`, image.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM images WHERE id = ?`, image.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Coordinator) checkDisk(opts Options) error {
	usage, err := DiskUsagePercent(opts.UploadDir)
	if err != nil {
		return err
	}
	limit := float64(utils.SettingInt64(c.db, "disk_stop_percent", 85, 50, 99))
	if usage >= limit {
		return fmt.Errorf("local disk usage %.1f%% reached %.0f%% safety limit", usage, limit)
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<uint(attempt-1)) * 30 * time.Second
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	return message
}

func validReplicaMode(mode string) bool {
	return mode == "local" || mode == "s3"
}

func firstSetting(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func boolSetting(raw string, fallback bool) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}
