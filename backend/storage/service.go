package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"zhuapic/config"
	"zhuapic/utils"
)

// Service owns storage locations and image copies: where every image lives,
// how it is read back, and the background work that keeps copies complete.
type Service struct {
	db      *sql.DB
	cfg     *config.Config
	manager *Manager
	health  *healthTracker
	wakeMu  sync.Mutex
	wakeCh  chan struct{}
}

func NewService(db *sql.DB, cfg *config.Config, manager *Manager) *Service {
	return &Service{db: db, cfg: cfg, manager: manager, health: newHealthTracker(db), wakeCh: make(chan struct{})}
}

func (s *Service) Manager() *Manager { return s.manager }

// Wake asks every background worker to look for new work now. Closing the
// channel broadcasts to all waiting lanes at once.
func (s *Service) Wake() {
	s.wakeMu.Lock()
	close(s.wakeCh)
	s.wakeCh = make(chan struct{})
	s.wakeMu.Unlock()
}

func (s *Service) wakeChannel() <-chan struct{} {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	return s.wakeCh
}

// RuntimeOptions are the site's local working directories.
func (s *Service) RuntimeOptions() Options {
	opts := s.manager.DefaultOptions()
	if dir := strings.TrimSpace(utils.GetSetting(s.db, "s3_cache_dir", "")); dir != "" {
		opts.CacheDir = dir
	}
	if dir := strings.TrimSpace(utils.GetSetting(s.db, "thumb_dir", "")); dir != "" {
		opts.ThumbDir = dir
	}
	return opts
}

func (s *Service) Options(st *Storage) Options { return st.Options(s.RuntimeOptions()) }

func (s *Service) DefaultID() uint {
	id, _ := strconv.ParseUint(utils.GetSetting(s.db, "default_storage_id", "0"), 10, 64)
	return uint(id)
}

const storageColumns = `id, name, provider, config_json, status, accept_uploads, auto_backup, read_priority, health, health_error, health_checked_at, offline_until`

type rowScanner interface{ Scan(...any) error }

func (s *Service) scan(row rowScanner) (*Storage, error) {
	var st Storage
	var raw string
	var checked, offline sql.NullTime
	if err := row.Scan(&st.ID, &st.Name, &st.Provider, &raw, &st.Status, &st.AcceptUploads, &st.AutoBackup, &st.ReadPriority,
		&st.Health, &st.HealthError, &checked, &offline); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &st.Config); err != nil {
		return nil, fmt.Errorf("storage %d has invalid configuration", st.ID)
	}
	if st.Config.SecretKey != "" {
		secret, err := utils.DecryptSecret(st.Config.SecretKey, s.cfg.JWTSecret)
		if err != nil {
			return nil, errors.New("存储密钥无法解密，请检查 JWT_SECRET 是否与原部署一致")
		}
		st.Config.SecretKey = secret
	}
	if checked.Valid {
		st.HealthCheckedAt = &checked.Time
	}
	if offline.Valid && offline.Time.After(time.Now()) {
		st.OfflineUntil = &offline.Time
	}
	st.HasSecret = st.Config.SecretKey != ""
	st.Configured = s.manager.Configured(st.Mode(), st.Options(Options{}))
	return &st, nil
}

// Get loads one storage with decrypted credentials.
func (s *Service) Get(ctx context.Context, id uint) (*Storage, error) {
	st, err := s.scan(s.db.QueryRowContext(ctx, `SELECT `+storageColumns+` FROM storages WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("存储不存在")
	}
	if err != nil {
		return nil, err
	}
	st.IsDefault = st.ID == s.DefaultID()
	return st, nil
}

// All returns every storage with decrypted credentials, ordered for reads.
func (s *Service) All(ctx context.Context) ([]Storage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+storageColumns+` FROM storages ORDER BY provider<>'local', read_priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	defaultID := s.DefaultID()
	out := []Storage{}
	for rows.Next() {
		st, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		st.IsDefault = st.ID == defaultID
		out = append(out, *st)
	}
	return out, rows.Err()
}

// List returns storages with copy statistics and without secrets.
func (s *Service) List(ctx context.Context) ([]Storage, error) {
	storages, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	stats := map[uint][5]int64{}
	rows, err := s.db.QueryContext(ctx, `SELECT storage_id, COALESCE(SUM(state='ready'),0), COALESCE(SUM(state IN ('pending','verifying')),0),
		COALESCE(SUM(state='failed'),0), COALESCE(SUM(state='missing'),0), COALESCE(SUM(IF(state='ready',size,0)),0)
		FROM image_copies GROUP BY storage_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uint
		var v [5]int64
		if err := rows.Scan(&id, &v[0], &v[1], &v[2], &v[3], &v[4]); err != nil {
			rows.Close()
			return nil, err
		}
		stats[id] = v
	}
	rows.Close()
	for i := range storages {
		v := stats[storages[i].ID]
		storages[i].Ready, storages[i].Pending, storages[i].Failed, storages[i].Missing, storages[i].Bytes = v[0], v[1], v[2], v[3], v[4]
		storages[i].Locked = v[0]+v[1]+v[2]+v[3] > 0
		storages[i].Config.SecretKey = ""
	}
	return storages, nil
}

// LockConfig serialises storage and group configuration changes across
// processes.
func (s *Service) LockConfig(ctx context.Context) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var acquired int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK('orimg:storage-config', 10)`).Scan(&acquired); err != nil || acquired != 1 {
		conn.Close()
		if err == nil {
			err = errors.New("存储配置正在被修改，请稍后重试")
		}
		return nil, err
	}
	return func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, `SELECT RELEASE_LOCK('orimg:storage-config')`)
		conn.Close()
	}, nil
}

// Save creates or updates a storage. Locations that already hold copies are
// immutable, so no existing object can lose its address.
func (s *Service) Save(ctx context.Context, st *Storage) error {
	unlock, err := s.LockConfig(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var previous *Storage
	if st.ID != 0 {
		if previous, err = s.Get(ctx, st.ID); err != nil {
			return err
		}
		if previous.Status == StatusDraining {
			return errors.New("存储正在移除，不能修改")
		}
		st.Provider = previous.Provider
		if st.Config.SecretKey == "" {
			st.Config.SecretKey = previous.Config.SecretKey
		}
		if st.Status == "" {
			st.Status = previous.Status
		}
	}
	if st.Status != StatusActive && st.Status != StatusPaused {
		st.Status = StatusActive
	}
	if err := st.Validate(); err != nil {
		return err
	}
	if !s.manager.Configured(st.Mode(), st.Options(Options{})) {
		return errors.New("存储配置不完整")
	}
	if previous != nil {
		var copies int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_copies WHERE storage_id=?`, st.ID).Scan(&copies); err != nil {
			return err
		}
		if copies > 0 && previous.LocationKey() != st.LocationKey() {
			return errors.New("这个存储已经保存了图片，地址、目录、Bucket 和前缀不能修改；请新增存储并迁移")
		}
		if previous.IsDefault && (!st.AcceptUploads || st.Status != StatusActive) {
			return errors.New("这是默认上传位置，请先把默认位置切换到其他存储")
		}
	}
	others, err := s.All(ctx)
	if err != nil {
		return err
	}
	for _, other := range others {
		if other.ID != st.ID && other.LocationKey() == st.LocationKey() {
			return fmt.Errorf("与存储“%s”的位置相同，两个存储会互相覆盖文件", other.Name)
		}
	}
	cfg := st.Config
	if cfg.SecretKey != "" {
		if cfg.SecretKey, err = utils.EncryptSecret(cfg.SecretKey, s.cfg.JWTSecret); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(cfg)
	if st.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO storages (name, provider, config_json, status, accept_uploads, auto_backup, read_priority) VALUES (?,?,?,?,?,?,?)`,
			st.Name, st.Provider, string(raw), st.Status, st.AcceptUploads, st.AutoBackup, st.ReadPriority)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		st.ID = uint(id)
	} else if _, err := s.db.ExecContext(ctx, `UPDATE storages SET name=?, config_json=?, status=?, accept_uploads=?, auto_backup=?, read_priority=? WHERE id=?`,
		st.Name, string(raw), st.Status, st.AcceptUploads, st.AutoBackup, st.ReadPriority, st.ID); err != nil {
		return err
	}
	s.health.Reset(st.ID)
	if st.AutoBackup && st.Status == StatusActive {
		if err := s.ensureBackupCopies(ctx, st.ID); err != nil {
			return err
		}
		s.Wake()
	}
	return nil
}

// SetDefault chooses the site-wide upload storage.
func (s *Service) SetDefault(ctx context.Context, id uint) error {
	st, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if st.Status != StatusActive || !st.AcceptUploads || !st.Configured {
		return errors.New("默认上传位置必须是已启用、允许上传且配置完整的存储")
	}
	return utils.UpsertSetting(s.db, "default_storage_id", strconv.FormatUint(uint64(id), 10))
}

// SetStatus pauses or resumes a storage.
func (s *Service) SetStatus(ctx context.Context, id uint, status string) error {
	st, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if st.Status == StatusDraining {
		return errors.New("存储正在移除")
	}
	if status == StatusPaused && st.IsDefault {
		return errors.New("这是默认上传位置，请先切换默认位置再暂停")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE storages SET status=? WHERE id=?`, status, id); err != nil {
		return err
	}
	if status == StatusActive {
		s.health.Reset(id)
		_, _ = s.db.ExecContext(ctx, `UPDATE storages SET offline_until=NULL WHERE id=?`, id)
		s.Wake()
	}
	return nil
}

// Probe writes, reads back and deletes a small object.
func (s *Service) Probe(ctx context.Context, id uint) error {
	st, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	opts := s.Options(st)
	if st.Mode() == "local" {
		if err := s.checkDisk(opts); err != nil {
			return err
		}
	}
	token, err := utils.RandomHex(8)
	if err != nil {
		return err
	}
	body := []byte("Origin storage probe " + token)
	key := joinObjectKey(opts.S3Prefix, ".origin-probe", token+".txt")
	if st.Mode() == "local" {
		key = ".origin-probe/" + token + ".txt"
	}
	err = func() error {
		if err := s.manager.Put(ctx, opts, st.Mode(), key, "text/plain", int64(len(body)), bytes.NewReader(body), ChecksumSHA256(body)); err != nil {
			return fmt.Errorf("写入失败：%w", err)
		}
		reader, err := s.manager.Open(ctx, opts, st.Mode(), key)
		if err != nil {
			return fmt.Errorf("读取失败：%w", err)
		}
		got, err := io.ReadAll(io.LimitReader(reader, 1024))
		reader.Close()
		if err != nil || !bytes.Equal(got, body) {
			return errors.New("读取内容与写入不一致")
		}
		if err := s.manager.DeleteObject(ctx, opts, st.Mode(), key); err != nil {
			return fmt.Errorf("删除失败：%w", err)
		}
		return nil
	}()
	if err != nil {
		s.health.Fail(st.ID, err)
		return err
	}
	s.health.Reset(st.ID)
	s.health.OK(st.ID)
	_, _ = s.db.ExecContext(ctx, `UPDATE storages SET health='ok', health_error='', health_checked_at=NOW(), offline_until=NULL WHERE id=?`, st.ID)
	s.Wake()
	return nil
}

// RetryFailed requeues failed and missing copies on a storage.
func (s *Service) RetryFailed(ctx context.Context, id uint) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE image_copies SET state=IF(state='missing','missing','pending'), attempts=0, next_attempt_at=NULL, last_error=''
		WHERE storage_id=? AND state IN ('failed','missing')`, id)
	if err != nil {
		return 0, err
	}
	s.health.Reset(id)
	_, _ = s.db.ExecContext(ctx, `UPDATE storages SET offline_until=NULL WHERE id=?`, id)
	s.Wake()
	return res.RowsAffected()
}

// Failures lists copies that need attention.
func (s *Service) Failures(ctx context.Context, limit int) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.image_id, c.storage_id, i.public_name, st.name, c.state, c.attempts, c.last_error, c.updated_at
		FROM image_copies c JOIN images i ON i.id=c.image_id JOIN storages st ON st.id=c.storage_id
		WHERE c.state IN ('failed','missing') OR (c.state='pending' AND c.attempts>0)
		ORDER BY c.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var imageID, storageID uint
		var name, storage, state, message string
		var attempts int
		var updated time.Time
		if err := rows.Scan(&imageID, &storageID, &name, &storage, &state, &attempts, &message, &updated); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"image_id": imageID, "storage_id": storageID, "filename": name, "storage": storage,
			"state": state, "attempts": attempts, "error": message, "updated_at": updated.Format(time.RFC3339)})
	}
	return out, rows.Err()
}

// UploadCandidates are storages that may receive new uploads.
func (s *Service) UploadCandidates(ctx context.Context) ([]Storage, error) {
	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	out := []Storage{}
	for _, st := range all {
		if st.Status == StatusActive && st.AcceptUploads && st.Configured {
			out = append(out, st)
		}
	}
	return out, nil
}

// CheckCapacity applies the local disk safety limit to a local storage.
func (s *Service) CheckCapacity(st *Storage) error {
	if st.Mode() != "local" {
		return nil
	}
	return s.checkDisk(s.Options(st))
}

func (s *Service) checkDisk(opts Options) error {
	usage, err := DiskUsagePercent(opts.UploadDir)
	if err != nil {
		return err
	}
	limit := float64(utils.SettingInt64(s.db, "disk_stop_percent", 85, 50, 99))
	if usage >= limit {
		return fmt.Errorf("本地磁盘已用 %.1f%%，达到 %.0f%% 安全线", usage, limit)
	}
	return nil
}

// Stats summarises copies for the dashboard.
func (s *Service) Stats(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	var pending, failed, missing, operations int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(state IN ('pending','verifying')),0), COALESCE(SUM(state='failed'),0), COALESCE(SUM(state='missing'),0) FROM image_copies`).Scan(&pending, &failed, &missing); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations WHERE status IN ('queued','running')`).Scan(&operations); err != nil {
		return nil, err
	}
	var orphaned int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images i WHERE i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM image_copies c WHERE c.image_id=i.id AND c.state='ready')`).Scan(&orphaned); err != nil {
		return nil, err
	}
	out["pending"], out["failed"], out["missing"], out["operations"], out["without_copy"] = pending, failed, missing, operations, orphaned
	return out, nil
}
