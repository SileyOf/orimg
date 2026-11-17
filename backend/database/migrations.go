package database

import (
	"database/sql"
	"fmt"

	"zhuapic/config"
)

type migration struct {
	version int
	name    string
	up      func(*sql.Tx) error
}

func runVersionedMigrations(db *sql.DB, cfg *config.Config) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INT NOT NULL PRIMARY KEY,
		name VARCHAR(190) NOT NULL,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return err
	}

	migrations := []migration{
		{version: 1, name: "upload policies and user groups", up: migrateUploadPolicies},
		{version: 2, name: "storage task leases", up: migrateStorageTaskLeases},
		{version: 3, name: "api tokens audit logs and image filters", up: migrateV030},
		{version: 4, name: "image retention expiry", up: migrateImageRetention},
		{version: 5, name: "named storage targets and durable multi-location replicas", up: migrateStorageTargets},
		{version: 6, name: "storage upload access", up: migrateStorageUploadAccess},
		{version: 7, name: "group storage permissions and user profiles", up: migrateUserProfiles},
		{version: 8, name: "transactional mail and revocable sessions", up: migrateMail},
		{version: 9, name: "email registration and editable mail templates", up: migrateRegistration},
		{version: 10, name: "unified storages, image copies, operations and group storage access", up: migrateUnifiedStorage(cfg)},
	}
	for _, item := range migrations {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, item.version).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := item.up(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", item.version, item.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, item.version, item.name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func migrateUserProfiles(tx *sql.Tx) error {
	columns := []struct{ table, name, definition string }{
		{"upload_policies", "allow_storage_selection", "TINYINT(1) NOT NULL DEFAULT 0"},
		{"users", "avatar_url", "VARCHAR(190) NOT NULL DEFAULT ''"},
		{"users", "quota_bytes", "BIGINT NULL"},
	}
	for _, col := range columns {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, col.table, col.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", col.table, col.name, col.definition)); err != nil {
				return err
			}
			if col.name == "allow_storage_selection" {
				if _, err := tx.Exec(`UPDATE upload_policies SET allow_storage_selection=1 WHERE storage_mode IN ('','default')`); err != nil {
					return err
				}
			}
		}
	}
	for _, statement := range []string{
		`UPDATE user_groups SET is_system=0`,
		"INSERT IGNORE INTO site_settings (`key`,value) SELECT 'guest_group_id',CAST(id AS CHAR) FROM user_groups WHERE slug='guest'",
		`CREATE TABLE IF NOT EXISTS user_avatars (name VARCHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, content MEDIUMBLOB NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY idx_avatar_user (user_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS upload_reservations (id CHAR(32) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, size BIGINT NOT NULL, expires_at DATETIME NOT NULL, INDEX idx_reservation_user (user_id, expires_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateStorageUploadAccess(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='storage_targets' AND COLUMN_NAME='upload_access'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := tx.Exec(`ALTER TABLE storage_targets ADD COLUMN upload_access VARCHAR(16) NOT NULL DEFAULT 'admin'`)
		return err
	}
	return nil
}

func migrateImageRetention(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'images' AND COLUMN_NAME = 'expires_at'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(`ALTER TABLE images ADD COLUMN expires_at DATETIME NULL AFTER is_animated`); err != nil {
			return err
		}
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'images' AND INDEX_NAME = 'idx_images_expiry'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := tx.Exec(`CREATE INDEX idx_images_expiry ON images (deleted_at, expires_at, id)`)
		return err
	}
	return nil
}

func migrateV030(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS api_tokens (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			user_id BIGINT UNSIGNED NOT NULL,
			name VARCHAR(80) NOT NULL,
			token_hash CHAR(64) NOT NULL,
			token_prefix VARCHAR(20) NOT NULL,
			last_used_at DATETIME NULL,
			expires_at DATETIME NULL,
			revoked_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE KEY idx_api_tokens_hash (token_hash),
			INDEX idx_api_tokens_user_active (user_id, revoked_at, created_at),
			INDEX idx_api_tokens_expiry (expires_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			actor_user_id BIGINT UNSIGNED NULL,
			actor_label VARCHAR(190) NOT NULL DEFAULT '',
			action VARCHAR(80) NOT NULL,
			target_type VARCHAR(50) NOT NULL DEFAULT '',
			target_id VARCHAR(100) NOT NULL DEFAULT '',
			ip VARCHAR(80) NOT NULL DEFAULT '',
			user_agent VARCHAR(255) NOT NULL DEFAULT '',
			details TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_audit_logs_created (created_at, id),
			INDEX idx_audit_logs_action_created (action, created_at, id),
			INDEX idx_audit_logs_actor_created (actor_user_id, created_at, id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	indexes := []struct {
		name, statement string
	}{
		{"idx_images_user_storage_cursor", `CREATE INDEX idx_images_user_storage_cursor ON images (user_id, deleted_at, storage_mode, created_at, id)`},
		{"idx_images_storage_cursor", `CREATE INDEX idx_images_storage_cursor ON images (deleted_at, storage_mode, created_at, id)`},
		{"idx_images_original_name", `CREATE INDEX idx_images_original_name ON images (original_name(100))`},
	}
	for _, index := range indexes {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'images' AND INDEX_NAME = ?`, index.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.Exec(index.statement); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateStorageTaskLeases(tx *sql.Tx) error {
	columns := []struct {
		table, name, definition string
	}{
		{"storage_job_items", "lease_token", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"storage_job_items", "lease_expires_at", "DATETIME NULL"},
		{"image_replicas", "lease_token", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"image_replicas", "lease_expires_at", "DATETIME NULL"},
	}
	for _, column := range columns {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`, column.table, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.name, column.definition)); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateUploadPolicies(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS upload_policies (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(80) NOT NULL,
			slug VARCHAR(50) NOT NULL,
			max_file_mb INT NOT NULL DEFAULT 50,
			max_batch_count INT NOT NULL DEFAULT 20,
			max_daily_uploads INT NOT NULL DEFAULT 0,
			max_daily_bytes BIGINT NOT NULL DEFAULT 0,
			total_quota_bytes BIGINT NOT NULL DEFAULT 0,
			storage_mode VARCHAR(20) NOT NULL DEFAULT 'default',
			retention_days INT NOT NULL DEFAULT 0,
			allowed_types TEXT NOT NULL,
			allow_svg TINYINT(1) NOT NULL DEFAULT 0,
			allow_api TINYINT(1) NOT NULL DEFAULT 0,
			active TINYINT(1) NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY idx_upload_policies_slug (slug)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS user_groups (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(80) NOT NULL,
			slug VARCHAR(50) NOT NULL,
			policy_id BIGINT UNSIGNED NOT NULL,
			is_system TINYINT(1) NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY idx_user_groups_slug (slug),
			INDEX idx_user_groups_policy (policy_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS upload_usage_daily (
			usage_date DATE NOT NULL,
			subject_type VARCHAR(20) NOT NULL,
			subject_key VARCHAR(190) NOT NULL,
			upload_count BIGINT NOT NULL DEFAULT 0,
			upload_bytes BIGINT NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (usage_date, subject_type, subject_key)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}

	var groupColumn int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'users' AND COLUMN_NAME = 'group_id'`).Scan(&groupColumn); err != nil {
		return err
	}
	if groupColumn == 0 {
		if _, err := tx.Exec(`ALTER TABLE users ADD COLUMN group_id BIGINT UNSIGNED NULL AFTER role, ADD INDEX idx_users_group (group_id)`); err != nil {
			return err
		}
	}

	const commonTypes = "jpg,jpeg,png,gif,webp,avif,bmp,tif,tiff,ico,svg,heic,heif,jxl,psd"
	policies := []struct {
		name, slug, storage  string
		fileMB, batch, daily int
		quotaGB              int64
		allowSVG, allowAPI   bool
	}{
		{"Guest", "guest", "default", 10, 5, 30, 0, false, false},
		{"Member", "member", "default", 50, 20, 0, 5, true, false},
		{"Trusted", "trusted", "mirror", 100, 50, 0, 50, true, true},
		{"Admin", "admin", "default", 200, 100, 0, 0, true, true},
	}
	for _, policy := range policies {
		if _, err := tx.Exec(`INSERT IGNORE INTO upload_policies
			(name, slug, max_file_mb, max_batch_count, max_daily_uploads, total_quota_bytes, storage_mode, allowed_types, allow_svg, allow_api)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, policy.name, policy.slug, policy.fileMB, policy.batch, policy.daily, policy.quotaGB*1024*1024*1024, policy.storage, commonTypes, policy.allowSVG, policy.allowAPI); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT IGNORE INTO user_groups (name, slug, policy_id, is_system)
			SELECT ?, ?, id, 1 FROM upload_policies WHERE slug = ?`, policy.name, policy.slug, policy.slug); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE users SET group_id = (SELECT id FROM user_groups WHERE slug = IF(users.role = 'admin', 'admin', 'member')) WHERE group_id IS NULL`); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE site_settings SET value = 'classic' WHERE ` + "`key`" + ` = 'upload_mode'`)
	return err
}
