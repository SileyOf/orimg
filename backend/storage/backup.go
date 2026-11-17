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
	"strings"
	"time"

	"zhuapic/utils"
)

// CreateDatabaseBackup queues a MySQL backup to the configured storage.
func (s *Service) CreateDatabaseBackup(ctx context.Context, user uint) (uint, error) {
	var existing uint
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM operations WHERE kind='db_backup' AND status IN ('queued','running') LIMIT 1`).Scan(&existing); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	target := uint(utils.SettingInt64(s.db, "db_backup_storage_id", 0, 0, 1<<31))
	if target == 0 {
		return 0, errors.New("请先选择数据库备份存储")
	}
	st, err := s.Get(ctx, target)
	if err != nil {
		return 0, err
	}
	if st.Status == StatusDraining || !st.Configured {
		return 0, errors.New("备份存储不可用")
	}
	return s.insertOperation(ctx, s.db, "db_backup", target, 0, false, "queued", 1, "", user)
}

func (s *Service) scheduleDatabaseBackup(ctx context.Context) error {
	if !utils.SettingBool(s.db, "db_backup_enabled", false) {
		return nil
	}
	if time.Now().Hour() != int(utils.SettingInt64(s.db, "db_backup_hour", 4, 0, 23)) {
		return nil
	}
	var today int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations WHERE kind='db_backup' AND DATE(created_at)=CURDATE()`).Scan(&today); err != nil || today > 0 {
		return err
	}
	_, err := s.CreateDatabaseBackup(ctx, 0)
	return err
}

func (s *Service) backupRunner(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = s.db.ExecContext(ctx, `UPDATE operations SET status='failed', message='备份执行中断或超时，请重新备份', finished_at=NOW()
			WHERE kind='db_backup' AND status='running' AND started_at < DATE_SUB(NOW(), INTERVAL 35 MINUTE)`)
		var id, target uint
		err := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(storage_id,0) FROM operations WHERE kind='db_backup' AND status='queued' ORDER BY id LIMIT 1`).Scan(&id, &target)
		if err == nil {
			res, err := s.db.ExecContext(ctx, `UPDATE operations SET status='running', started_at=NOW() WHERE id=? AND status='queued'`, id)
			if n, _ := res.RowsAffected(); err == nil && n == 1 {
				runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
				key, size, runErr := s.runDatabaseBackup(runCtx, target, id)
				cancel()
				if runErr != nil {
					_, _ = s.db.ExecContext(ctx, `UPDATE operations SET status='failed', failed=1, message=?, finished_at=NOW() WHERE id=?`, truncateError(runErr), id)
				} else {
					_, _ = s.db.ExecContext(ctx, `UPDATE operations SET status='done', done=1, message=?, finished_at=NOW() WHERE id=?`,
						fmt.Sprintf("已保存 %s（%.1f MB）", key, float64(size)/(1<<20)), id)
				}
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) runDatabaseBackup(ctx context.Context, target uint, opID uint) (string, int64, error) {
	st, err := s.Get(ctx, target)
	if err != nil {
		return "", 0, err
	}
	opts := s.Options(st)
	resolved, err := resolveMysqldump(utils.GetSetting(s.db, "mysqldump_path", "mysqldump"))
	if err != nil {
		return "", 0, errors.New("找不到 mysqldump，请安装 MySQL 客户端或在设置中填写路径")
	}
	tempDir := filepath.Join(opts.CacheDir, "database-backups")
	if err := os.MkdirAll(tempDir, 0750); err != nil {
		return "", 0, err
	}
	temp, err := os.CreateTemp(tempDir, "origin-*.sql.gz")
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
		"--default-character-set=utf8mb4", "--protocol=TCP", "-h", s.cfg.DBHost, "-P", s.cfg.DBPort, "-u", s.cfg.DBUser}
	if s.cfg.DBTLS == "verify" {
		args = append(args, "--ssl-mode=VERIFY_IDENTITY")
		if s.cfg.DBTLSCA != "" {
			ca, err := os.CreateTemp(tempDir, "mysql-ca-*.pem")
			if err != nil {
				gzipWriter.Close()
				temp.Close()
				return "", 0, err
			}
			defer os.Remove(ca.Name())
			_, writeErr := ca.WriteString(s.cfg.DBTLSCA)
			closeErr := ca.Close()
			if writeErr != nil || closeErr != nil {
				gzipWriter.Close()
				temp.Close()
				return "", 0, errors.Join(writeErr, closeErr)
			}
			args = append(args, "--ssl-ca="+ca.Name())
		}
	}
	args = append(args, s.cfg.DBName)
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+s.cfg.DBPassword)
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
	prefix := cleanObjectPrefix(utils.GetSetting(s.db, "db_backup_s3_prefix", "backups/database"))
	if prefix == "" {
		prefix = "backups/database"
	}
	key := joinObjectKey(prefix, fmt.Sprintf("origin-%s-%d.sql.gz", time.Now().Format("20060102-150405"), opID))
	if err = writeDatabaseArchive(ctx, opts, st.Mode(), key, body, stat.Size()); err != nil {
		s.health.Fail(st.ID, err)
		return "", 0, err
	}
	s.health.OK(st.ID)
	return key, stat.Size(), nil
}

// resolveMysqldump honours the configured path, but falls back to the
// mysqldump on PATH when it is missing. Sites moved from a host install into
// the container keep a host path (for example a panel's MySQL directory)
// that does not exist inside the image.
func resolveMysqldump(configured string) (string, error) {
	resolved, err := exec.LookPath(configured)
	if err == nil || configured == "mysqldump" {
		return resolved, err
	}
	return exec.LookPath("mysqldump")
}
