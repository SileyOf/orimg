package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"zhuapic/models"
	"zhuapic/utils"
)

type TargetConfig struct {
	Directory string `json:"directory"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style"`
}

type Target struct {
	ID             uint         `json:"id"`
	Name           string       `json:"name"`
	Provider       string       `json:"provider"`
	LegacyMode     string       `json:"legacy_mode"`
	Config         TargetConfig `json:"config"`
	Enabled        bool         `json:"enabled"`
	AutoBackup     bool         `json:"auto_backup"`
	UploadAccess   string       `json:"upload_access"`
	LocationLocked bool         `json:"location_locked"`
	Configured     bool         `json:"configured"`
	HasSecret      bool         `json:"has_secret"`
	Ready          int64        `json:"ready"`
	Pending        int64        `json:"pending"`
	Failed         int64        `json:"failed"`
	Bytes          int64        `json:"bytes"`
}

func (t Target) Mode() string {
	if t.Provider == "local" {
		return "local"
	}
	return "s3"
}

func ValidateTarget(t *Target) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len([]rune(t.Name)) > 80 {
		return errors.New("存储名称需要 1–80 个字符")
	}
	switch t.Provider {
	case "local", "s3", "r2", "minio", "b2":
	default:
		return errors.New("不支持的存储服务")
	}
	if t.LegacyMode != "" {
		return nil
	}
	if t.Mode() == "local" {
		if !filepath.IsAbs(t.Config.Directory) || filepath.Clean(t.Config.Directory) == "/" {
			return errors.New("请选择专用的本地绝对路径，不能使用根目录")
		}
		t.Config.Directory = filepath.Clean(t.Config.Directory)
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(t.Config.Endpoint))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Endpoint 必须是有效的 HTTP(S) 服务地址，不得包含凭据、查询或片段")
	}
	t.Config.Endpoint = strings.TrimRight(u.String(), "/")
	if strings.TrimSpace(t.Config.Bucket) == "" || strings.ContainsAny(t.Config.Bucket, "/\\?#") {
		return errors.New("请填写有效的 Bucket")
	}
	if t.Config.Region == "" || t.Config.AccessKey == "" || t.Config.SecretKey == "" {
		return errors.New("Region、Access Key 和 Secret Key 不能为空")
	}
	if strings.Contains(t.Config.Prefix, "..") || strings.HasPrefix(t.Config.Prefix, "/") {
		return errors.New("对象前缀必须是相对路径，不能含 ..")
	}
	t.Config.Prefix = cleanObjectPrefix(t.Config.Prefix)
	return nil
}

func (c *Coordinator) Target(ctx context.Context, id uint) (*Target, error) {
	var t Target
	var raw string
	err := c.db.QueryRowContext(ctx, "SELECT id, name, provider, COALESCE(legacy_mode,''), config_json, enabled, auto_backup, upload_access FROM storage_targets WHERE id = ?", id).
		Scan(&t.ID, &t.Name, &t.Provider, &t.LegacyMode, &raw, &t.Enabled, &t.AutoBackup, &t.UploadAccess)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &t.Config); err != nil {
		return nil, err
	}
	if t.LegacyMode != "" {
		opts := c.options()
		t.Config = TargetConfig{Directory: opts.UploadDir, Endpoint: opts.S3.Endpoint, Region: opts.S3.Region, Bucket: opts.S3.Bucket, Prefix: opts.S3Prefix, AccessKey: opts.S3.AccessKey, SecretKey: opts.S3.SecretKey, PathStyle: opts.S3.ForcePathStyle}
	} else if t.Config.SecretKey != "" {
		t.Config.SecretKey, err = utils.DecryptSecret(t.Config.SecretKey, c.cfg.JWTSecret)
		if err != nil {
			return nil, errors.New("存储密钥无法解密，请检查 JWT_SECRET 是否与原部署一致")
		}
	}
	t.HasSecret = t.Config.SecretKey != ""
	t.Configured = t.Mode() == "local" || c.manager.S3Configured(c.TargetOptions(&t))
	t.LocationLocked = true
	if t.LegacyMode != "" {
		var count int
		if err := c.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM image_replicas WHERE storage_mode=?)+(SELECT COUNT(*) FROM storage_target_replicas WHERE target_id=?)`, t.LegacyMode, t.ID).Scan(&count); err != nil {
			return nil, err
		}
		t.LocationLocked = count > 0
	}
	return &t, nil
}

func (c *Coordinator) TargetOptions(t *Target) Options {
	opts := c.options()
	opts.UploadDir = t.Config.Directory
	opts.S3.Endpoint = t.Config.Endpoint
	opts.S3.Region = t.Config.Region
	opts.S3.Bucket = t.Config.Bucket
	opts.S3.AccessKey = t.Config.AccessKey
	opts.S3.SecretKey = t.Config.SecretKey
	opts.S3.ForcePathStyle = t.Config.PathStyle
	opts.S3Prefix = t.Config.Prefix
	return opts
}

func (c *Coordinator) CheckTargetCapacity(t *Target) error {
	return c.checkDisk(c.TargetOptions(t))
}

func (c *Coordinator) CheckLegacyLocationChanges(ctx context.Context, payload map[string]string) error {
	opts := c.options()
	current := map[string]string{"local_upload_dir": opts.UploadDir, "s3_endpoint": opts.S3.Endpoint, "s3_bucket": opts.S3.Bucket, "s3_key_prefix": opts.S3Prefix, "s3_force_path_style": strconv.FormatBool(opts.S3.ForcePathStyle)}
	for key, old := range current {
		value, ok := payload[key]
		if !ok || strings.TrimSpace(value) == old {
			continue
		}
		mode := "s3"
		if key == "local_upload_dir" {
			mode = "local"
		}
		var count int
		if err := c.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM image_replicas WHERE storage_mode=?)+(SELECT COUNT(*) FROM storage_target_replicas r JOIN storage_targets t ON t.id=r.target_id WHERE t.legacy_mode=?)`, mode, mode).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return errors.New("此 Local/S3 位置已有图片引用，不能修改路径或桶；请新增存储并补齐副本")
		}
	}
	return nil
}

func (c *Coordinator) ListTargets(ctx context.Context) ([]Target, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT id FROM storage_targets ORDER BY id")
	if err != nil {
		return nil, err
	}
	ids := []uint{}
	for rows.Next() {
		var id uint
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	targets := []Target{}
	for _, id := range ids {
		t, err := c.Target(ctx, id)
		if err != nil {
			return nil, err
		}
		err = c.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(status='ready'),0), COALESCE(SUM(status IN ('pending','syncing','verifying')),0), COALESCE(SUM(status IN ('failed','verify_failed')),0), COALESCE(SUM(IF(status='ready',size,0)),0) FROM storage_target_replicas WHERE target_id=?`, id).Scan(&t.Ready, &t.Pending, &t.Failed, &t.Bytes)
		if err != nil {
			return nil, err
		}
		if t.LegacyMode != "" {
			var n, b int64
			if err = c.db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(size),0) FROM image_replicas WHERE storage_mode=? AND status='ready'", t.LegacyMode).Scan(&n, &b); err != nil {
				return nil, err
			}
			t.Ready += n
			t.Bytes += b
		}
		t.Config.SecretKey = ""
		targets = append(targets, *t)
	}
	return targets, nil
}

func (c *Coordinator) SaveTarget(ctx context.Context, t *Target) error {
	unlock, lockErr := c.LockTargetConfig(ctx)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	var previous *Target
	if t.ID != 0 {
		var err error
		previous, err = c.Target(ctx, t.ID)
		if err != nil {
			return err
		}
		t.LegacyMode = previous.LegacyMode
		if t.LegacyMode != "" {
			t.Provider = previous.Provider
		}
		if t.UploadAccess == "" {
			t.UploadAccess = previous.UploadAccess
		}
		if t.Config.SecretKey == "" {
			t.Config.SecretKey = previous.Config.SecretKey
		}
		// Locations are immutable, even before first upload, so an in-flight write cannot be orphaned.
		if previous.LocationLocked && (t.Provider != previous.Provider || t.Config.Directory != previous.Config.Directory || t.Config.Endpoint != previous.Config.Endpoint || t.Config.Bucket != previous.Config.Bucket || t.Config.Prefix != previous.Config.Prefix || t.Config.PathStyle != previous.Config.PathStyle) {
			return errors.New("存储位置不能直接修改；请新增存储并补齐副本")
		}
	} else {
		t.LegacyMode = ""
	}
	if t.UploadAccess == "" {
		t.UploadAccess = "admin"
	}
	if t.UploadAccess != "admin" && t.UploadAccess != "members" && t.UploadAccess != "public" {
		return errors.New("无效的上传可选范围")
	}
	if err := ValidateTarget(t); err != nil {
		return err
	}
	if t.AutoBackup && t.Mode() == "s3" && !c.manager.S3Configured(c.TargetOptions(t)) {
		return errors.New("自动备份需要完整的 S3 配置")
	}
	if !t.Enabled && (t.AutoBackup || c.DefaultTargetID() == t.ID && t.ID != 0) {
		return errors.New("请先取消默认存储和自动备份，再暂停此存储")
	}
	if !t.Enabled && t.ID != 0 {
		var refs int
		if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM upload_policies WHERE storage_mode=? OR (?<>'' AND storage_mode IN (?, 'mirror'))", TargetSelection(t.ID), t.LegacyMode, t.LegacyMode).Scan(&refs); err != nil {
			return err
		}
		if refs > 0 {
			return errors.New("用户组仍指定此存储，请先调整用户组策略")
		}
	}
	if t.LegacyMode != "" {
		return c.saveLegacyTarget(ctx, t, previous)
	}
	cfg := t.Config
	if cfg.SecretKey != "" {
		encrypted, err := utils.EncryptSecret(cfg.SecretKey, c.cfg.JWTSecret)
		if err != nil {
			return err
		}
		cfg.SecretKey = encrypted
	}
	raw, _ := json.Marshal(cfg)
	if t.ID == 0 {
		res, err := c.db.ExecContext(ctx, "INSERT INTO storage_targets (name,provider,config_json,enabled,auto_backup,upload_access) VALUES (?,?,?,?,?,?)", t.Name, t.Provider, string(raw), t.Enabled, t.AutoBackup, t.UploadAccess)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		t.ID = uint(id)
		return nil
	}
	_, err := c.db.ExecContext(ctx, "UPDATE storage_targets SET name=?,config_json=?,enabled=?,auto_backup=?,upload_access=? WHERE id=?", t.Name, string(raw), t.Enabled, t.AutoBackup, t.UploadAccess, t.ID)
	return err
}

func (c *Coordinator) DeleteTarget(ctx context.Context, id uint) error {
	unlock, lockErr := c.LockTargetConfig(ctx)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	t, err := c.Target(ctx, id)
	if err != nil {
		return err
	}
	if t.LegacyMode != "" {
		return errors.New("兼容存储保留旧图片引用，不能删除；可关闭自动备份")
	}
	if t.AutoBackup || c.DefaultTargetID() == id {
		return errors.New("请先取消自动备份或更换默认存储")
	}
	var refs int
	if uint(utils.SettingInt64(c.db, "db_backup_target_id", 0, 0, 2147483647)) == id {
		return errors.New("请先更换数据库备份存储")
	}
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_jobs WHERE type='db_backup' AND target_mode=? AND status IN ('queued','running')`, TargetSelection(id)).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return errors.New("此存储仍有数据库备份任务")
	}
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM upload_policies WHERE storage_mode=?", TargetSelection(id)).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return errors.New("用户组仍指定此存储，请先调整用户组策略")
	}
	res, err := c.db.ExecContext(ctx, `DELETE FROM storage_targets WHERE id=? AND NOT EXISTS (SELECT 1 FROM storage_target_replicas WHERE target_id=?)`, id, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("存储仍有图片或任务引用，不能删除。删除配置不会删除远端文件")
	}
	return nil
}

func (c *Coordinator) DefaultTargetID() uint {
	id, _ := strconv.ParseUint(utils.GetSetting(c.db, "default_storage_target_id", "0"), 10, 64)
	if id == 0 {
		mode := utils.GetSetting(c.db, "storage_mode", c.cfg.StorageMode)
		// Preserve existing Mirror writes until an administrator explicitly selects a new default.
		if mode == "local" || mode == "s3" {
			_ = c.db.QueryRow("SELECT id FROM storage_targets WHERE legacy_mode=?", mode).Scan(&id)
		}
	}
	return uint(id)
}

func (c *Coordinator) SetDefaultTarget(ctx context.Context, id uint) error {
	unlock, lockErr := c.LockTargetConfig(ctx)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	if id != 0 {
		t, err := c.Target(ctx, id)
		if err != nil {
			return err
		}
		if !t.Enabled {
			return errors.New("目标存储已暂停")
		}
		if t.Mode() == "s3" && !c.manager.S3Configured(c.TargetOptions(t)) {
			return errors.New("请先完善 S3 配置")
		}
	}
	_, err := c.db.ExecContext(ctx, "INSERT INTO site_settings (`key`,value) VALUES ('default_storage_target_id',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", strconv.FormatUint(uint64(id), 10))
	utils.InvalidateSettings(c.db)
	return err
}

func (c *Coordinator) RegisterTargetImage(ctx context.Context, image *models.Image, t *Target, checksum string) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO storage_target_replicas (image_id,target_id,storage_key,is_primary,status,checksum_sha256,size,verified_at) VALUES (?,?,?,1,'ready',?,?,NOW())`, image.ID, t.ID, image.StorageKey, checksum, image.Size); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE images SET checksum_sha256=? WHERE id=?", checksum, image.ID); err != nil {
		return err
	}
	if err = c.enqueueAutomatic(ctx, tx, image.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Coordinator) enqueueAutomatic(ctx context.Context, tx *sql.Tx, imageID uint) error {
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO storage_target_replicas (image_id,target_id,size)
 SELECT i.id,t.id,i.size FROM images i CROSS JOIN storage_targets t
 WHERE i.id=? AND t.enabled=1 AND t.auto_backup=1
 AND NOT EXISTS (SELECT 1 FROM image_replicas r WHERE r.image_id=i.id AND r.storage_mode=t.legacy_mode AND r.status='ready')`, imageID)
	return err
}

func (c *Coordinator) QueueTarget(ctx context.Context, id uint, action string) (int64, error) {
	t, err := c.Target(ctx, id)
	if err != nil {
		return 0, err
	}
	if !t.Enabled {
		return 0, errors.New("目标存储已暂停")
	}
	if t.Mode() == "s3" && !c.manager.S3Configured(c.TargetOptions(t)) {
		return 0, errors.New("目标 S3 配置不完整")
	}
	var res sql.Result
	switch action {
	case "backfill":
		res, err = c.db.ExecContext(ctx, `INSERT IGNORE INTO storage_target_replicas (image_id,target_id,size)
   SELECT i.id,?,i.size FROM images i WHERE i.deleted_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM image_replicas r WHERE r.image_id=i.id AND r.storage_mode=? AND r.status='ready')`, id, t.LegacyMode)
	case "retry":
		res, err = c.db.ExecContext(ctx, "UPDATE storage_target_replicas SET status=IF(status='verify_failed','verifying','pending'),attempts=0,last_error='',next_retry_at=NULL WHERE target_id=? AND status IN ('failed','verify_failed')", id)
	case "verify":
		// Legacy copies remain under the legacy verifier; new records use this queue.
		res, err = c.db.ExecContext(ctx, "UPDATE storage_target_replicas SET status='verifying',attempts=0,last_error='',next_retry_at=NULL WHERE target_id=? AND status='ready'", id)
	default:
		return 0, errors.New("无效的存储操作")
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (c *Coordinator) TargetFailures(ctx context.Context) ([]map[string]any, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT r.image_id,r.target_id,i.original_name,t.name,r.status,r.attempts,r.last_error FROM storage_target_replicas r JOIN storage_targets t ON t.id=r.target_id JOIN images i ON i.id=r.image_id WHERE r.status<>'ready' ORDER BY r.updated_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var imageID, targetID uint
		var name, target, status, message string
		var attempts int
		if err := rows.Scan(&imageID, &targetID, &name, &target, &status, &attempts, &message); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"image_id": imageID, "target_id": targetID, "filename": name, "target": target, "status": status, "attempts": attempts, "error": message})
	}
	return out, rows.Err()
}

func targetObjectKey(t *Target, image *models.Image) string {
	// Separate namespace prevents a backup from overwriting a legacy primary object.
	prefix := "orimg-replicas/" + strconv.FormatUint(uint64(t.ID), 10)
	if t.Mode() == "s3" && t.Config.Prefix != "" {
		prefix = t.Config.Prefix + "/" + prefix
	}
	return fmt.Sprintf("%s/%s", prefix, filepath.Base(image.PublicName))
}
