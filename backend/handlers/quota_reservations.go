package handlers

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"zhuapic/models"
	"zhuapic/utils"
)

// Reserve against committed images and in-flight uploads under the same user row lock.
// The upload deadline is shorter than the lease; crashed uploads release capacity within an hour.
func (a *App) reserveUploadQuota(ctx context.Context, user *models.User, policy models.UploadPolicy, size int64) (func(), error) {
	if user == nil {
		return func() {}, nil
	}
	id, err := utils.RandomHex(16)
	if err != nil {
		return nil, err
	}
	tx, err := a.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT active FROM users WHERE id=? FOR UPDATE`, user.ID).Scan(&active); err != nil {
		return nil, err
	}
	if !active {
		return nil, errors.New("账号已停用")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM upload_reservations WHERE user_id=? AND expires_at<=NOW()`, user.ID); err != nil {
		return nil, err
	}
	if policy.TotalQuotaBytes > 0 {
		var used, reserved int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM images WHERE user_id=? AND deleted_at IS NULL`, user.ID).Scan(&used); err != nil {
			return nil, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM upload_reservations WHERE user_id=? AND expires_at>NOW()`, user.ID).Scan(&reserved); err != nil {
			return nil, err
		}
		if size > policy.TotalQuotaBytes-used-reserved {
			return nil, errors.New("存储空间不足（含正在上传的图片）")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO upload_reservations(id,user_id,size,expires_at) VALUES(?,?,?,DATE_ADD(NOW(),INTERVAL 1 HOUR))`, id, user.ID, size); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.DB.ExecContext(cleanup, `DELETE FROM upload_reservations WHERE id=?`, id)
	}, nil
}
