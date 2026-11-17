package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"zhuapic/utils"
)

// Operation is a long-running admin action with visible progress.
type Operation struct {
	ID            uint       `json:"id"`
	Kind          string     `json:"kind"` // drain | verify | db_backup | remove
	StorageID     uint       `json:"storage_id"`
	StorageName   string     `json:"storage_name"`
	DestinationID uint       `json:"destination_id"`
	DeleteObjects bool       `json:"delete_objects"`
	Status        string     `json:"status"` // queued | running | done | failed | cancelled
	Total         int64      `json:"total"`
	Done          int64      `json:"done"`
	Failed        int64      `json:"failed"`
	Message       string     `json:"message"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

func (s *Service) ListOperations(ctx context.Context, limit int) ([]Operation, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.kind, COALESCE(o.storage_id,0), COALESCE(st.name, o.subject), COALESCE(o.destination_id,0), o.delete_objects,
		o.status, o.total, o.done, o.failed, o.message, o.created_at, o.started_at, o.finished_at
		FROM operations o LEFT JOIN storages st ON st.id=o.storage_id ORDER BY o.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		var op Operation
		var started, finished sql.NullTime
		if err := rows.Scan(&op.ID, &op.Kind, &op.StorageID, &op.StorageName, &op.DestinationID, &op.DeleteObjects, &op.Status,
			&op.Total, &op.Done, &op.Failed, &op.Message, &op.CreatedAt, &started, &finished); err != nil {
			return nil, err
		}
		if started.Valid {
			op.StartedAt = &started.Time
		}
		if finished.Valid {
			op.FinishedAt = &finished.Time
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (s *Service) insertOperation(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, kind string, storageID, destination uint, deleteObjects bool, status string, total int64, message string, user uint) (uint, error) {
	// subject keeps the storage name readable after the storage is removed.
	res, err := exec.ExecContext(ctx, `INSERT INTO operations (kind, storage_id, subject, destination_id, delete_objects, status, total, message, created_by, started_at, finished_at)
		VALUES (?, NULLIF(?,0), COALESCE((SELECT name FROM storages WHERE id=?), ''), NULLIF(?,0), ?, ?, ?, ?, NULLIF(?,0), IF(? IN ('running','done'), NOW(), NULL), IF(?='done', NOW(), NULL))`,
		kind, storageID, storageID, destination, deleteObjects, status, total, message, user, status, status)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}

// RemovalPlan describes what removing a storage would affect.
type RemovalPlan struct {
	StorageID  uint   `json:"storage_id"`
	Copies     int64  `json:"copies"`
	OnlyHere   int64  `json:"only_here"` // live images with no ready copy elsewhere
	InTrash    int64  `json:"in_trash"`  // recycled images with no ready copy elsewhere
	IsDefault  bool   `json:"is_default"`
	IsBackup   bool   `json:"is_db_backup"`
	Health     string `json:"health"`
	Reachable  bool   `json:"reachable"`
	GroupUsage int64  `json:"group_usage"`
}

const onlyHereSQL = `FROM image_copies c JOIN images i ON i.id=c.image_id WHERE c.storage_id=? AND NOT EXISTS (
	SELECT 1 FROM image_copies o JOIN storages os ON os.id=o.storage_id
	WHERE o.image_id=c.image_id AND o.storage_id<>c.storage_id AND o.state='ready' AND os.status<>'draining')`

func (s *Service) RemovalPlan(ctx context.Context, id uint) (*RemovalPlan, error) {
	st, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	plan := &RemovalPlan{StorageID: id, IsDefault: st.IsDefault, Health: st.Health, Reachable: s.health.Available(id, st.OfflineUntil)}
	plan.IsBackup = uint(utils.SettingInt64(s.db, "db_backup_storage_id", 0, 0, 1<<31)) == id
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_copies WHERE storage_id=?`, id).Scan(&plan.Copies); err != nil {
		return nil, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(i.deleted_at IS NULL),0), COALESCE(SUM(i.deleted_at IS NOT NULL),0) `+onlyHereSQL+` AND c.state='ready'`, id).Scan(&plan.OnlyHere, &plan.InTrash); err != nil {
		return nil, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM group_storages WHERE storage_id=?) + (SELECT COUNT(*) FROM user_groups WHERE default_storage_id=?)`, id, id).Scan(&plan.GroupUsage); err != nil {
		return nil, err
	}
	return plan, nil
}

// detach removes every reference to a storage from groups and settings.
func (s *Service) detach(ctx context.Context, tx *sql.Tx, id uint) error {
	for _, q := range []string{
		`DELETE FROM group_storages WHERE storage_id=?`,
		`UPDATE user_groups SET default_storage_id=NULL WHERE default_storage_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "UPDATE site_settings SET value='0' WHERE `key`='db_backup_storage_id' AND value=?", strconv.FormatUint(uint64(id), 10))
	return err
}

// StartDrain moves every image that only lives on the storage to
// destination, then removes the storage. With deleteObjects its files are
// deleted afterwards; otherwise they stay untouched on the storage.
func (s *Service) StartDrain(ctx context.Context, id, destination uint, deleteObjects bool, user uint) (uint, error) {
	unlock, err := s.LockConfig(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	st, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if st.Status == StatusDraining {
		return 0, errors.New("这个存储已经在移除中")
	}
	if st.IsDefault {
		return 0, errors.New("这是默认上传位置，请先把默认位置切换到其他存储")
	}
	plan, err := s.RemovalPlan(ctx, id)
	if err != nil {
		return 0, err
	}
	if plan.OnlyHere+plan.InTrash > 0 {
		if destination == 0 || destination == id {
			return 0, errors.New("请选择接收图片的存储")
		}
		dest, err := s.Get(ctx, destination)
		if err != nil {
			return 0, err
		}
		if dest.Status != StatusActive || !dest.Configured {
			return 0, errors.New("接收图片的存储必须已启用且配置完整")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE storages SET status='draining', accept_uploads=0, auto_backup=0 WHERE id=?`, id); err != nil {
		return 0, err
	}
	if err = s.detach(ctx, tx, id); err != nil {
		return 0, err
	}
	// Pending copies to a storage that is leaving are pointless.
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_copies WHERE storage_id=? AND state<>'ready' AND object_key=''`, id); err != nil {
		return 0, err
	}
	total := plan.OnlyHere + plan.InTrash
	op, err := s.insertOperation(ctx, tx, "drain", id, destination, deleteObjects, "running", total, "", user)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	utils.InvalidateSettings(s.db)
	_ = s.progressOperations(ctx)
	s.Wake()
	return op, nil
}

// ForceRemove drops an unreachable storage immediately. Live images whose
// only copy was on it move to the recycle bin; the returned count says how
// many. Files on the storage itself are left alone.
func (s *Service) ForceRemove(ctx context.Context, id uint, user uint) (int64, error) {
	unlock, err := s.LockConfig(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	st, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if st.IsDefault {
		return 0, errors.New("这是默认上传位置，请先把默认位置切换到其他存储")
	}
	days := utils.SettingInt64(s.db, "recycle_bin_days", 7, 1, 30)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE images SET deleted_at=NOW(), delete_after=DATE_ADD(NOW(), INTERVAL ? DAY)
		WHERE deleted_at IS NULL AND id IN (SELECT image_id FROM (SELECT c.image_id `+onlyHereSQL+`) lost)`, days, id)
	if err != nil {
		return 0, err
	}
	lost, _ := res.RowsAffected()
	for _, q := range []string{`DELETE FROM image_copies WHERE storage_id=?`, `DELETE FROM storage_gc WHERE storage_id=?`,
		`UPDATE operations SET status='cancelled', finished_at=NOW(), message='存储已被强制移除' WHERE storage_id=? AND status IN ('queued','running')`} {
		if _, err = tx.ExecContext(ctx, q, id); err != nil {
			return 0, err
		}
	}
	if err = s.detach(ctx, tx, id); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM storages WHERE id=?`, id); err != nil {
		return 0, err
	}
	message := fmt.Sprintf("已强制移除“%s”", st.Name)
	if lost > 0 {
		message += fmt.Sprintf("，%d 张仅存于此的图片已移入回收站", lost)
	}
	if _, err = s.insertOperation(ctx, tx, "remove", id, 0, false, "done", 0, message, user); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	utils.InvalidateSettings(s.db)
	s.health.Reset(id)
	return lost, nil
}

// StartVerify re-reads every ready copy on a storage and checks SHA-256.
// Damaged or missing copies are repaired from other copies automatically.
func (s *Service) StartVerify(ctx context.Context, id uint, user uint) (uint, error) {
	st, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if st.Status != StatusActive {
		return 0, errors.New("只能校验已启用的存储")
	}
	var existing uint
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM operations WHERE kind='verify' AND storage_id=? AND status='running' LIMIT 1`, id).Scan(&existing); err == nil {
		return existing, nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE image_copies SET state='verifying', attempts=0, next_attempt_at=NULL WHERE storage_id=? AND state='ready'`, id)
	if err != nil {
		return 0, err
	}
	total, _ := res.RowsAffected()
	status := "running"
	if total == 0 {
		status = "done"
	}
	op, err := s.insertOperation(ctx, s.db, "verify", id, 0, false, status, total, "", user)
	s.Wake()
	return op, err
}

// CancelOperation stops a queued or running operation.
func (s *Service) CancelOperation(ctx context.Context, id uint) error {
	var kind, status string
	var storageID uint
	if err := s.db.QueryRowContext(ctx, `SELECT kind, status, COALESCE(storage_id,0) FROM operations WHERE id=?`, id).Scan(&kind, &status, &storageID); err != nil {
		return errors.New("任务不存在")
	}
	if status != "queued" && status != "running" {
		return errors.New("任务已经结束")
	}
	if kind == "db_backup" && status == "running" {
		return errors.New("数据库备份正在执行，不能中途取消")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE operations SET status='cancelled', finished_at=NOW() WHERE id=?`, id); err != nil {
		return err
	}
	switch kind {
	case "drain":
		// The storage stays, paused, so nothing is lost; resume it to use it again.
		if _, err = tx.ExecContext(ctx, `UPDATE storages SET status='paused' WHERE id=? AND status='draining'`, storageID); err != nil {
			return err
		}
	case "verify":
		if _, err = tx.ExecContext(ctx, `UPDATE image_copies SET state='ready' WHERE storage_id=? AND state='verifying'`, storageID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// progressOperations advances drains and verifications.
func (s *Service) progressOperations(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, COALESCE(storage_id,0), COALESCE(destination_id,0), delete_objects, total, started_at
		FROM operations WHERE status='running' AND kind IN ('drain','verify')`)
	if err != nil {
		return err
	}
	type op struct {
		id, storage, dest uint
		kind              string
		deleteObjects     bool
		total             int64
		started           sql.NullTime
	}
	var ops []op
	for rows.Next() {
		var o op
		if err := rows.Scan(&o.id, &o.kind, &o.storage, &o.dest, &o.deleteObjects, &o.total, &o.started); err != nil {
			rows.Close()
			return err
		}
		ops = append(ops, o)
	}
	rows.Close()
	for _, o := range ops {
		var err error
		if o.kind == "verify" {
			err = s.progressVerify(ctx, o.id, o.storage, o.total, o.started)
		} else {
			err = s.progressDrain(ctx, o.id, o.storage, o.dest, o.deleteObjects, o.total)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) progressVerify(ctx context.Context, opID, storageID uint, total int64, started sql.NullTime) error {
	var remaining, failed int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='verifying'),0), COALESCE(SUM(state IN ('failed','missing') AND updated_at >= ?),0)
		FROM image_copies WHERE storage_id=?`, started.Time, storageID).Scan(&remaining, &failed); err != nil {
		return err
	}
	done := total - remaining
	if done < 0 {
		done = 0
	}
	if remaining > 0 {
		_, err := s.db.ExecContext(ctx, `UPDATE operations SET done=?, failed=? WHERE id=?`, done, failed, opID)
		return err
	}
	message := "全部副本校验通过"
	if failed > 0 {
		message = fmt.Sprintf("%d 个副本缺失或损坏，正在从其他副本自动修复", failed)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status='done', done=?, failed=?, message=?, finished_at=NOW() WHERE id=?`, total, failed, message, opID)
	return err
}

func (s *Service) progressDrain(ctx context.Context, opID, storageID, dest uint, deleteObjects bool, total int64) error {
	if dest != 0 {
		// Ask the destination for a copy of everything that only lives here.
		if _, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO image_copies (image_id, storage_id, state, size)
			SELECT c.image_id, ?, 'pending', i.size `+onlyHereSQL+` AND c.state='ready'`, dest, storageID); err != nil {
			return err
		}
		// Copies that already gave up get another round while draining.
		if _, err := s.db.ExecContext(ctx, `UPDATE image_copies SET attempts=0, next_attempt_at=NULL WHERE storage_id=? AND state IN ('failed','pending') AND attempts >= ?`, dest, maxCopyAttempts); err != nil {
			return err
		}
	}
	// Only readable copies can still be saved; broken ones have no source.
	var remaining, failed int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) `+onlyHereSQL+` AND c.state='ready'`, storageID).Scan(&remaining); err != nil {
		return err
	}
	if dest != 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_copies d JOIN image_copies c ON c.image_id=d.image_id AND c.storage_id=?
			WHERE d.storage_id=? AND d.state='failed'`, storageID, dest).Scan(&failed); err != nil {
			return err
		}
	}
	if remaining > 0 {
		done := total - remaining
		if done < 0 {
			done = 0
		}
		message := fmt.Sprintf("还有 %d 张图片等待复制", remaining)
		if failed > 0 {
			message += fmt.Sprintf("，其中 %d 张复制失败，可在副本异常中查看原因", failed)
		}
		_, err := s.db.ExecContext(ctx, `UPDATE operations SET done=?, failed=?, message=? WHERE id=?`, done, failed, message, opID)
		return err
	}
	// Everything is safe elsewhere: release the storage.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if deleteObjects {
		if _, err = tx.ExecContext(ctx, `INSERT INTO storage_gc (storage_id, object_key) SELECT storage_id, object_key FROM image_copies WHERE storage_id=? AND object_key<>''`, storageID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_copies WHERE storage_id=?`, storageID); err != nil {
		return err
	}
	var queued int64
	if deleteObjects {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_gc WHERE storage_id=?`, storageID).Scan(&queued); err != nil {
			return err
		}
	}
	if queued > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE operations SET done=total, message=? WHERE id=?`, fmt.Sprintf("图片已全部迁出，正在删除存储上的 %d 个文件", queued), opID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM storage_gc WHERE storage_id=?`, storageID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM storages WHERE id=?`, storageID); err != nil {
		return err
	}
	message := "图片已全部迁出，存储已移除"
	if !deleteObjects {
		message += "（存储上的文件保留未动）"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE operations SET status='done', done=total, message=?, finished_at=NOW() WHERE id=?`, message, opID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.health.Reset(storageID)
	return nil
}
