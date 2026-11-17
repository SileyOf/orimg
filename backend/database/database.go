package database

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"zhuapic/config"
	"zhuapic/utils"
)

func migrate(db *sql.DB, cfg *config.Config) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(80) NOT NULL,
			email VARCHAR(190) NOT NULL,
			email_verified TINYINT(1) NOT NULL DEFAULT 0,
			password VARCHAR(255) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'user',
			active TINYINT(1) NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY idx_users_email_unique (email),
			INDEX idx_users_username (username),
			INDEX idx_users_role (role),
			INDEX idx_users_active (active)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS images (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			public_name VARCHAR(120) NOT NULL UNIQUE,
			original_name VARCHAR(255) NOT NULL,
			mime_type VARCHAR(120) NOT NULL,
			ext VARCHAR(20) NOT NULL,
			size BIGINT NOT NULL DEFAULT 0,
			width INT NOT NULL DEFAULT 0,
			height INT NOT NULL DEFAULT 0,
			storage_mode VARCHAR(20) NOT NULL,
			storage_key VARCHAR(500) NOT NULL,
			user_id BIGINT UNSIGNED NULL,
			upload_ip VARCHAR(80) NOT NULL DEFAULT '',
			thumb_small VARCHAR(500) NOT NULL DEFAULT '',
			thumb_medium VARCHAR(500) NOT NULL DEFAULT '',
			thumb_large VARCHAR(500) NOT NULL DEFAULT '',
			is_animated TINYINT(1) NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_images_created_at (created_at),
			INDEX idx_images_storage_mode (storage_mode),
			INDEX idx_images_user_created (user_id, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS site_settings (
			` + "`key`" + ` VARCHAR(80) NOT NULL PRIMARY KEY,
			value TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS image_replicas (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			image_id BIGINT UNSIGNED NOT NULL,
			storage_mode VARCHAR(20) NOT NULL,
			storage_key VARCHAR(500) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'ready',
			checksum_sha256 CHAR(64) NOT NULL DEFAULT '',
			size BIGINT NOT NULL DEFAULT 0,
			attempts INT NOT NULL DEFAULT 0,
			last_error VARCHAR(1000) NOT NULL DEFAULT '',
			last_verified_at DATETIME NULL,
			next_retry_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY idx_replicas_image_mode (image_id, storage_mode),
			INDEX idx_replicas_status_retry (status, next_retry_at),
			INDEX idx_replicas_mode_status (storage_mode, status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS storage_jobs (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			type VARCHAR(30) NOT NULL,
			source_mode VARCHAR(20) NOT NULL DEFAULT '',
			target_mode VARCHAR(20) NOT NULL DEFAULT '',
			status VARCHAR(20) NOT NULL DEFAULT 'queued',
			total_items BIGINT NOT NULL DEFAULT 0,
			processed_items BIGINT NOT NULL DEFAULT 0,
			success_items BIGINT NOT NULL DEFAULT 0,
			failed_items BIGINT NOT NULL DEFAULT 0,
			total_bytes BIGINT NOT NULL DEFAULT 0,
			processed_bytes BIGINT NOT NULL DEFAULT 0,
			last_error VARCHAR(1000) NOT NULL DEFAULT '',
			created_by BIGINT UNSIGNED NULL,
			started_at DATETIME NULL,
			completed_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_storage_jobs_status (status, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS storage_job_items (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			job_id BIGINT UNSIGNED NOT NULL,
			image_id BIGINT UNSIGNED NOT NULL,
			source_mode VARCHAR(20) NOT NULL DEFAULT '',
			target_mode VARCHAR(20) NOT NULL DEFAULT '',
			status VARCHAR(20) NOT NULL DEFAULT 'queued',
			size BIGINT NOT NULL DEFAULT 0,
			attempts INT NOT NULL DEFAULT 0,
			last_error VARCHAR(1000) NOT NULL DEFAULT '',
			next_retry_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY idx_job_items_unique (job_id, image_id, target_mode),
			INDEX idx_job_items_work (status, next_retry_at, job_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	unified, err := unifiedSchemaApplied(db)
	if err != nil {
		return err
	}
	for _, stmt := range statements {
		// Legacy replica and job tables are only needed until schema 10
		// converts them; never recreate them afterwards.
		if unified && (strings.Contains(stmt, "image_replicas") || strings.Contains(stmt, "storage_job")) {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	columns := []struct {
		table string
		name  string
		def   string
	}{
		{"users", "email", "VARCHAR(190) NOT NULL DEFAULT ''"},
		{"users", "email_verified", "TINYINT(1) NOT NULL DEFAULT 0"},
		{"users", "role", "VARCHAR(20) NOT NULL DEFAULT 'user'"},
		{"users", "active", "TINYINT(1) NOT NULL DEFAULT 1"},
		{"images", "user_id", "BIGINT UNSIGNED NULL"},
		{"images", "thumb_small", "VARCHAR(500) NOT NULL DEFAULT ''"},
		{"images", "thumb_medium", "VARCHAR(500) NOT NULL DEFAULT ''"},
		{"images", "thumb_large", "VARCHAR(500) NOT NULL DEFAULT ''"},
		{"images", "is_animated", "TINYINT(1) NOT NULL DEFAULT 0"},
		{"images", "checksum_sha256", "CHAR(64) NOT NULL DEFAULT ''"},
		{"images", "deleted_at", "DATETIME NULL"},
		{"images", "delete_after", "DATETIME NULL"},
	}
	for _, col := range columns {
		if err := ensureColumn(db, col.table, col.name, col.def); err != nil {
			return err
		}
	}
	// Normalize legacy emails only when the unique index does not exist yet.
	var emailIndex int
	if err := db.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='users' AND INDEX_NAME='idx_users_email_unique'`).Scan(&emailIndex); err != nil {
		return err
	}
	if emailIndex == 0 {
		if err := backfillUserEmails(db, cfg); err != nil {
			return err
		}
	}
	if err := dropUsernameUniqueIndexes(db); err != nil {
		return err
	}
	if err := ensureIndex(db, "idx_users_email_unique", `CREATE UNIQUE INDEX idx_users_email_unique ON users (email)`); err != nil {
		return err
	}
	_ = ensureIndex(db, "idx_users_username", `CREATE INDEX idx_users_username ON users (username)`)
	_ = ensureIndex(db, "idx_users_role", `CREATE INDEX idx_users_role ON users (role)`)
	_ = ensureIndex(db, "idx_users_active", `CREATE INDEX idx_users_active ON users (active)`)
	_ = ensureIndex(db, "idx_images_user_created", `CREATE INDEX idx_images_user_created ON images (user_id, created_at)`)
	_ = ensureIndex(db, "idx_images_deleted_at", `CREATE INDEX idx_images_deleted_at ON images (deleted_at, delete_after)`)
	if err := ensureIndex(db, "idx_images_user_deleted_cursor", `CREATE INDEX idx_images_user_deleted_cursor ON images (user_id, deleted_at, created_at, id)`); err != nil {
		return err
	}
	if err := ensureIndex(db, "idx_images_deleted_cursor", `CREATE INDEX idx_images_deleted_cursor ON images (deleted_at, created_at, id)`); err != nil {
		return err
	}
	return runVersionedMigrations(db, cfg)
}

func unifiedSchemaApplied(db *sql.DB) (bool, error) {
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='schema_migrations'`).Scan(&tables); err != nil || tables == 0 {
		return false, err
	}
	var applied int
	err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version >= 10`).Scan(&applied)
	return applied > 0, err
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
		table,
		column,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

func ensureIndex(db *sql.DB, name string, statement string) error {
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN ('users', 'images') AND INDEX_NAME = ?`,
		name,
	).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := db.Exec(statement)
	return err
}

func backfillUserEmails(db *sql.DB, cfg *config.Config) error {
	if _, err := db.Exec(`UPDATE users SET email = CONCAT('user-', id, '@local.zhuapic') WHERE email = '' OR email IS NULL`); err != nil {
		return err
	}
	return normalizeDuplicateEmails(db)
}

func normalizeDuplicateEmails(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, email FROM users ORDER BY LOWER(email), id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var id uint
		var email string
		if err := rows.Scan(&id, &email); err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSpace(email))
		if key == "" {
			key = fmt.Sprintf("user-%d@local.zhuapic", id)
		}
		if seen[key] {
			if _, err := db.Exec(`UPDATE users SET email = CONCAT('user-', id, '@local.zhuapic'), email_verified = 0 WHERE id = ?`, id); err != nil {
				return err
			}
			continue
		}
		seen[key] = true
		if email != key {
			if _, err := db.Exec(`UPDATE users SET email = ? WHERE id = ?`, key, id); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func dropUsernameUniqueIndexes(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT INDEX_NAME
		FROM INFORMATION_SCHEMA.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME = 'users'
			AND NON_UNIQUE = 0
			AND INDEX_NAME <> 'PRIMARY'
		GROUP BY INDEX_NAME
		HAVING COUNT(*) = 1
			AND SUM(CASE WHEN COLUMN_NAME = 'username' THEN 1 ELSE 0 END) = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if _, err := db.Exec("ALTER TABLE users DROP INDEX " + quoteIdentifier(name)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func quoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func systemEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "admin@example.com"
	}
	return email
}

func seedAdmin(db *sql.DB, cfg *config.Config, allowBootstrap bool) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if !allowBootstrap {
		return fmt.Errorf("existing database has no users; refusing automatic administrator creation")
	}
	if err := config.ValidateAdmin(cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		return err
	}
	hash, err := utils.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	result, err := db.Exec(`INSERT INTO users (username, email, email_verified, password, role, active, group_id)
		SELECT ?, ?, 1, ?, 'admin', 1, id FROM user_groups WHERE slug='admin'`, cfg.AdminUsername, systemEmail(cfg.AdminEmail), hash)
	if err != nil {
		return err
	}
	countCreated, err := result.RowsAffected()
	if err == nil && countCreated != 1 {
		return fmt.Errorf("administrator group missing; bootstrap was not completed")
	}
	return err
}

func seedSettings(db *sql.DB, cfg *config.Config) error {
	defaults := map[string]string{
		"site_name":              "Origin",
		"site_tagline":           "",
		"site_logo_url":          "/assets/logo.svg",
		"admin_brand_name":       "",
		"admin_brand_subtitle":   "",
		"admin_badge_text":       "",
		"site_domains":           "",
		"hero_title":             "",
		"hero_subtitle":          "",
		"announcement":           "",
		"footer_text":            "",
		"upload_mode":            "classic",
		"admin_path":             "/admin",
		"user_path":              "/dashboard",
		"public_upload_enabled":  "false",
		"max_upload_mb":          strconv.FormatInt(cfg.MaxUploadMB, 10),
		"cache_ttl_days":         "30",
		"disk_stop_percent":      "85",
		"recycle_bin_days":       "7",
		"replica_worker_enabled": "true",
		"db_backup_enabled":      "false",
		"db_backup_hour":         "4",
		"db_backup_s3_prefix":    "backups/database",
		"mysqldump_path":         "mysqldump",
		"allowed_types":          "jpg,jpeg,png,gif,webp,avif,bmp,tif,tiff,ico,svg,heic,heif,jxl,psd",
		"s3_cache_dir":           cfg.CacheDir,
		"db_backup_storage_id":   "0",
		"thumb_dir":              "data/thumbs",
		"login_captcha_enabled":  "true",
		"upload_rate_per_min":    "60",
		"image_rate_per_min":     "600",
		"login_rate_per_min":     "20",
		"app_version":            config.AppVersion,
	}

	for key, value := range defaults {
		_, err := db.Exec(
			`INSERT IGNORE INTO site_settings (`+"`key`"+`, value) VALUES (?, ?)`,
			key,
			value,
		)
		if err != nil {
			return fmt.Errorf("seed setting %s: %w", key, err)
		}
	}

	return nil
}
