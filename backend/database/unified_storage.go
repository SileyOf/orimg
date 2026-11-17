package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"zhuapic/config"
	"zhuapic/utils"
)

// Schema 10 replaces the two parallel storage systems (legacy local/s3/mirror
// modes with image_replicas, and named targets with storage_target_replicas)
// by one model: storages + image_copies. Upload policies are folded into user
// groups, and group storage access becomes an explicit allow-list.
//
// MySQL DDL commits implicitly, so every step is idempotent: a migration that
// stops half-way can simply run again.

const unifiedStorageTables = `
CREATE TABLE IF NOT EXISTS storages (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	name VARCHAR(80) NOT NULL,
	provider VARCHAR(24) NOT NULL,
	config_json TEXT NOT NULL,
	status VARCHAR(16) NOT NULL DEFAULT 'active',
	accept_uploads TINYINT(1) NOT NULL DEFAULT 1,
	auto_backup TINYINT(1) NOT NULL DEFAULT 0,
	read_priority INT NOT NULL DEFAULT 100,
	health VARCHAR(16) NOT NULL DEFAULT 'unknown',
	health_error VARCHAR(500) NOT NULL DEFAULT '',
	health_checked_at DATETIME NULL,
	offline_until DATETIME NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS image_copies (
	image_id BIGINT UNSIGNED NOT NULL,
	storage_id BIGINT UNSIGNED NOT NULL,
	object_key VARCHAR(1024) NOT NULL DEFAULT '',
	state VARCHAR(16) NOT NULL DEFAULT 'pending',
	checksum_sha256 CHAR(64) NOT NULL DEFAULT '',
	size BIGINT NOT NULL DEFAULT 0,
	attempts INT NOT NULL DEFAULT 0,
	last_error VARCHAR(1000) NOT NULL DEFAULT '',
	next_attempt_at DATETIME NULL,
	lease_token VARCHAR(64) NOT NULL DEFAULT '',
	lease_until DATETIME NULL,
	verified_at DATETIME NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	PRIMARY KEY (image_id, storage_id),
	INDEX idx_copies_work (storage_id, state, next_attempt_at),
	INDEX idx_copies_state (state, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS storage_gc (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	storage_id BIGINT UNSIGNED NOT NULL,
	object_key VARCHAR(1024) NOT NULL,
	attempts INT NOT NULL DEFAULT 0,
	last_error VARCHAR(500) NOT NULL DEFAULT '',
	next_attempt_at DATETIME NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	INDEX idx_gc_work (storage_id, next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS operations (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	kind VARCHAR(24) NOT NULL,
	storage_id BIGINT UNSIGNED NULL,
	subject VARCHAR(80) NOT NULL DEFAULT '',
	destination_id BIGINT UNSIGNED NULL,
	delete_objects TINYINT(1) NOT NULL DEFAULT 0,
	status VARCHAR(16) NOT NULL DEFAULT 'queued',
	total BIGINT NOT NULL DEFAULT 0,
	done BIGINT NOT NULL DEFAULT 0,
	failed BIGINT NOT NULL DEFAULT 0,
	message VARCHAR(1000) NOT NULL DEFAULT '',
	created_by BIGINT UNSIGNED NULL,
	started_at DATETIME NULL,
	finished_at DATETIME NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	INDEX idx_operations_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS group_storages (
	group_id BIGINT UNSIGNED NOT NULL,
	storage_id BIGINT UNSIGNED NOT NULL,
	PRIMARY KEY (group_id, storage_id),
	INDEX idx_group_storages_storage (storage_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

// Settings that only described the old storage model.
var retiredStorageSettings = []string{
	"storage_mode", "local_upload_dir", "s3_endpoint", "s3_region", "s3_bucket", "s3_access_key",
	"s3_secret_key", "s3_force_path_style", "s3_key_prefix", "default_storage_target_id", "db_backup_target_id",
}

type storageConfigV10 struct {
	Directory string `json:"directory"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style"`
}

func tableExists(tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, name).Scan(&n)
	return n > 0, err
}

func columnExists(tx *sql.Tx, table, column string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, table, column).Scan(&n)
	return n > 0, err
}

func indexExists(tx *sql.Tx, table, index string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_NAME=?`, table, index).Scan(&n)
	return n > 0, err
}

func migrateUnifiedStorage(cfg *config.Config) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		for _, statement := range strings.Split(unifiedStorageTables, ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
		settings, err := readSettings(tx)
		if err != nil {
			return err
		}
		setting := func(key, fallback string) string {
			if value := strings.TrimSpace(settings[key]); value != "" {
				return value
			}
			return fallback
		}
		legacy, err := migrateStorageRows(tx, cfg, settings, setting)
		if err != nil {
			return err
		}
		if err = migrateCopyRows(tx, legacy); err != nil {
			return err
		}
		if err = migrateGroupPolicies(tx, settings, legacy); err != nil {
			return err
		}
		if err = migrateStorageSettings(tx, settings, setting, legacy); err != nil {
			return err
		}
		return dropLegacyStorage(tx)
	}
}

func readSettings(tx *sql.Tx) (map[string]string, error) {
	out := map[string]string{}
	rows, err := tx.Query("SELECT `key`, value FROM site_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

type legacyStorageIDs struct{ local, s3 uint }

func (l legacyStorageIDs) forMode(mode string) uint {
	switch mode {
	case "local", "mirror":
		return l.local
	case "s3":
		return l.s3
	}
	return 0
}

func storageCount(tx *sql.Tx, id uint) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM storages WHERE id=?`, id).Scan(&n)
	return n, err
}

func migrateStorageRows(tx *sql.Tx, cfg *config.Config, settings map[string]string, setting func(string, string) string) (legacyStorageIDs, error) {
	var legacy legacyStorageIDs
	hasTargets, err := tableExists(tx, "storage_targets")
	if err != nil {
		return legacy, err
	}
	hasOldReplicas, err := tableExists(tx, "image_replicas")
	if err != nil {
		return legacy, err
	}
	hasTargetReplicas, err := tableExists(tx, "storage_target_replicas")
	if err != nil {
		return legacy, err
	}
	mode := strings.ToLower(setting("storage_mode", cfg.StorageMode))
	defaultTarget, _ := strconv.ParseUint(settings["default_storage_target_id"], 10, 64)
	if hasTargets {
		type row struct {
			id                          uint
			name, provider, legacy, raw string
			enabled, autoBackup         bool
		}
		rows, err := tx.Query(`SELECT id, name, provider, COALESCE(legacy_mode,''), config_json, enabled, auto_backup FROM storage_targets ORDER BY id`)
		if err != nil {
			return legacy, err
		}
		var targets []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.name, &r.provider, &r.legacy, &r.raw, &r.enabled, &r.autoBackup); err != nil {
				rows.Close()
				return legacy, err
			}
			targets = append(targets, r)
		}
		rows.Close()
		for _, t := range targets {
			used := false
			if hasTargetReplicas {
				var n int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM storage_target_replicas WHERE target_id=?`, t.id).Scan(&n); err != nil {
					return legacy, err
				}
				used = n > 0
			}
			raw := t.raw
			switch t.legacy {
			case "local":
				c := storageConfigV10{Directory: setting("local_upload_dir", cfg.UploadDir)}
				b, _ := json.Marshal(c)
				raw = string(b)
				used = true // the original local location always survives
			case "s3":
				secret := setting("s3_secret_key", cfg.S3.SecretKey)
				if secret != "" && !strings.HasPrefix(secret, "enc:v1:") {
					if secret, err = utils.EncryptSecret(secret, cfg.JWTSecret); err != nil {
						return legacy, err
					}
				}
				c := storageConfigV10{Endpoint: strings.TrimRight(setting("s3_endpoint", cfg.S3.Endpoint), "/"), Region: setting("s3_region", firstNonBlank(cfg.S3.Region, "auto")),
					Bucket: setting("s3_bucket", cfg.S3.Bucket), Prefix: setting("s3_key_prefix", "images"), AccessKey: setting("s3_access_key", cfg.S3.AccessKey),
					SecretKey: secret, PathStyle: parseBoolSetting(settings["s3_force_path_style"], cfg.S3.ForcePathStyle)}
				if !used && hasOldReplicas {
					var n int
					if err := tx.QueryRow(`SELECT COUNT(*) FROM image_replicas WHERE storage_mode='s3'`).Scan(&n); err != nil {
						return legacy, err
					}
					used = n > 0
				}
				if !used && (c.Endpoint == "" || c.Bucket == "") && mode != "s3" && mode != "mirror" {
					continue // an unconfigured placeholder is not worth keeping
				}
				b, _ := json.Marshal(c)
				raw = string(b)
			}
			status := "active"
			if !t.enabled {
				status = "paused"
			}
			autoBackup := t.autoBackup || (t.legacy == "s3" && mode == "mirror")
			isDefault := uint(defaultTarget) == t.id || (defaultTarget == 0 && t.legacy != "" && (t.legacy == mode || (mode == "mirror" && t.legacy == "local")))
			accept := !(autoBackup && !isDefault)
			priority := 100
			if t.provider == "local" {
				priority = 10
			}
			if _, err := tx.Exec(`INSERT IGNORE INTO storages (id, name, provider, config_json, status, accept_uploads, auto_backup, read_priority) VALUES (?,?,?,?,?,?,?,?)`,
				t.id, t.name, t.provider, raw, status, accept, autoBackup, priority); err != nil {
				return legacy, err
			}
			switch t.legacy {
			case "local":
				legacy.local = t.id
			case "s3":
				legacy.s3 = t.id
			}
		}
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM storages`).Scan(&count); err != nil {
		return legacy, err
	}
	if count == 0 {
		b, _ := json.Marshal(storageConfigV10{Directory: firstNonBlank(cfg.UploadDir, "data/uploads")})
		res, err := tx.Exec(`INSERT INTO storages (name, provider, config_json, read_priority) VALUES ('本地存储','local',?,10)`, string(b))
		if err != nil {
			return legacy, err
		}
		id, _ := res.LastInsertId()
		legacy.local = uint(id)
	}
	// Default upload storage.
	defaultID := uint(0)
	if defaultTarget > 0 {
		if n, err := storageCount(tx, uint(defaultTarget)); err != nil {
			return legacy, err
		} else if n > 0 {
			defaultID = uint(defaultTarget)
		}
	}
	if defaultID == 0 {
		defaultID = legacy.forMode(mode)
	}
	if defaultID == 0 {
		if err := tx.QueryRow(`SELECT id FROM storages ORDER BY provider<>'local', id LIMIT 1`).Scan(&defaultID); err != nil {
			return legacy, err
		}
	}
	if _, err := tx.Exec(`UPDATE storages SET accept_uploads=1 WHERE id=?`, defaultID); err != nil {
		return legacy, err
	}
	if existing := settings["default_storage_id"]; existing == "" {
		if _, err := tx.Exec("INSERT INTO site_settings (`key`, value) VALUES ('default_storage_id', ?) ON DUPLICATE KEY UPDATE value=VALUES(value)", strconv.FormatUint(uint64(defaultID), 10)); err != nil {
			return legacy, err
		}
	}
	return legacy, nil
}

func migrateCopyRows(tx *sql.Tx, legacy legacyStorageIDs) error {
	if ok, err := tableExists(tx, "storage_target_replicas"); err != nil {
		return err
	} else if ok {
		// A primary copy was the authoritative original; keep it readable even
		// if a later verification or sync attempt failed.
		if _, err := tx.Exec(`INSERT IGNORE INTO image_copies (image_id, storage_id, object_key, state, checksum_sha256, size, attempts, last_error, verified_at)
			SELECT r.image_id, r.target_id, r.storage_key,
				CASE WHEN r.status IN ('ready','verifying') OR r.is_primary=1 THEN 'ready'
				     WHEN r.status IN ('pending','syncing') THEN 'pending' ELSE 'failed' END,
				r.checksum_sha256, r.size, IF(r.status IN ('failed','verify_failed') AND r.is_primary=0, r.attempts, 0),
				IF(r.status IN ('failed','verify_failed') AND r.is_primary=0, r.last_error, ''), r.verified_at
			FROM storage_target_replicas r JOIN storages s ON s.id = r.target_id
			WHERE r.storage_key <> '' OR r.status IN ('pending','syncing','failed')`); err != nil {
			return err
		}
	}
	if ok, err := tableExists(tx, "image_replicas"); err != nil {
		return err
	} else if ok {
		for mode, id := range map[string]uint{"local": legacy.local, "s3": legacy.s3} {
			if id == 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT IGNORE INTO image_copies (image_id, storage_id, object_key, state, checksum_sha256, size, attempts, last_error, verified_at)
				SELECT image_id, ?, storage_key,
					CASE status WHEN 'ready' THEN 'ready' WHEN 'corrupt' THEN 'failed' WHEN 'failed' THEN 'failed' ELSE 'pending' END,
					checksum_sha256, size, IF(status='failed', attempts, 0), IF(status IN ('failed','corrupt'), last_error, ''), last_verified_at
				FROM image_replicas WHERE storage_mode=? AND status <> 'deleted'`, id, mode); err != nil {
				return err
			}
		}
	}
	if ok, err := columnExists(tx, "images", "storage_mode"); err != nil {
		return err
	} else if ok {
		// Very old rows may only be described by images.storage_mode/storage_key.
		for mode, id := range map[string]uint{"local": legacy.local, "mirror": legacy.local, "s3": legacy.s3} {
			if id == 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT IGNORE INTO image_copies (image_id, storage_id, object_key, state, checksum_sha256, size)
				SELECT i.id, ?, i.storage_key, 'ready', i.checksum_sha256, i.size FROM images i
				WHERE i.storage_mode=? AND i.storage_key<>'' AND NOT EXISTS (SELECT 1 FROM image_copies c WHERE c.image_id=i.id AND c.state='ready')`, id, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateGroupPolicies(tx *sql.Tx, settings map[string]string, legacy legacyStorageIDs) error {
	columns := []struct{ name, def string }{
		{"max_file_mb", "INT NOT NULL DEFAULT 50"}, {"max_batch_count", "INT NOT NULL DEFAULT 20"},
		{"max_daily_uploads", "INT NOT NULL DEFAULT 0"}, {"max_daily_bytes", "BIGINT NOT NULL DEFAULT 0"},
		{"total_quota_bytes", "BIGINT NOT NULL DEFAULT 0"}, {"retention_days", "INT NOT NULL DEFAULT 0"},
		{"allowed_types", "TEXT NULL"}, {"allow_svg", "TINYINT(1) NOT NULL DEFAULT 0"},
		{"allow_api", "TINYINT(1) NOT NULL DEFAULT 0"}, {"active", "TINYINT(1) NOT NULL DEFAULT 1"},
		{"default_storage_id", "BIGINT UNSIGNED NULL"},
	}
	for _, col := range columns {
		if ok, err := columnExists(tx, "user_groups", col.name); err != nil {
			return err
		} else if !ok {
			if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE user_groups ADD COLUMN %s %s", col.name, col.def)); err != nil {
				return err
			}
		}
	}
	hasPolicies, err := tableExists(tx, "upload_policies")
	if err != nil {
		return err
	}
	hasPolicyID, err := columnExists(tx, "user_groups", "policy_id")
	if err != nil {
		return err
	}
	if hasPolicies && hasPolicyID {
		if _, err := tx.Exec(`UPDATE user_groups g JOIN upload_policies p ON p.id = g.policy_id SET
			g.max_file_mb=p.max_file_mb, g.max_batch_count=p.max_batch_count, g.max_daily_uploads=p.max_daily_uploads,
			g.max_daily_bytes=p.max_daily_bytes, g.total_quota_bytes=p.total_quota_bytes, g.retention_days=p.retention_days,
			g.allowed_types=p.allowed_types, g.allow_svg=p.allow_svg, g.allow_api=p.allow_api, g.active=p.active`); err != nil {
			return err
		}
		guestGroup, _ := strconv.ParseUint(settings["guest_group_id"], 10, 64)
		hasAccess, err := columnExists(tx, "storage_targets", "upload_access")
		if err != nil {
			return err
		}
		hasSelection, err := columnExists(tx, "upload_policies", "allow_storage_selection")
		if err != nil {
			return err
		}
		selection := "0"
		if hasSelection {
			selection = "p.allow_storage_selection"
		}
		type groupRow struct {
			id         uint
			slug, mode string
			canSelect  bool
		}
		rows, err := tx.Query(`SELECT g.id, g.slug, p.storage_mode, ` + selection + ` FROM user_groups g JOIN upload_policies p ON p.id=g.policy_id`)
		if err != nil {
			return err
		}
		var groups []groupRow
		for rows.Next() {
			var g groupRow
			if err := rows.Scan(&g.id, &g.slug, &g.mode, &g.canSelect); err != nil {
				rows.Close()
				return err
			}
			groups = append(groups, g)
		}
		rows.Close()
		for _, g := range groups {
			fixed := uint(0)
			if strings.HasPrefix(g.mode, "target:") {
				id, _ := strconv.ParseUint(strings.TrimPrefix(g.mode, "target:"), 10, 64)
				if n, err := storageCount(tx, uint(id)); err != nil {
					return err
				} else if n > 0 {
					fixed = uint(id)
				}
			} else {
				fixed = legacy.forMode(g.mode)
			}
			if fixed != 0 {
				if _, err := tx.Exec(`UPDATE user_groups SET default_storage_id=? WHERE id=?`, fixed, g.id); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT IGNORE INTO group_storages (group_id, storage_id) VALUES (?,?)`, g.id, fixed); err != nil {
					return err
				}
			}
			if g.canSelect && hasAccess {
				access := "('members','public')"
				if uint(guestGroup) == g.id || g.slug == "guest" {
					access = "('public')"
				}
				if _, err := tx.Exec(`INSERT IGNORE INTO group_storages (group_id, storage_id)
					SELECT ?, s.id FROM storages s JOIN storage_targets t ON t.id=s.id WHERE t.upload_access IN `+access, g.id); err != nil {
					return err
				}
			}
		}
	}
	if _, err := tx.Exec(`UPDATE user_groups SET allowed_types='jpg,jpeg,png,gif,webp,avif' WHERE allowed_types IS NULL OR allowed_types=''`); err != nil {
		return err
	}
	if hasPolicyID {
		if ok, err := indexExists(tx, "user_groups", "idx_user_groups_policy"); err != nil {
			return err
		} else if ok {
			if _, err := tx.Exec(`ALTER TABLE user_groups DROP INDEX idx_user_groups_policy`); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`ALTER TABLE user_groups DROP COLUMN policy_id`); err != nil {
			return err
		}
	}
	return nil
}

func migrateStorageSettings(tx *sql.Tx, settings map[string]string, setting func(string, string) string, legacy legacyStorageIDs) error {
	if _, ok := settings["db_backup_storage_id"]; !ok {
		backup := uint(0)
		if id, _ := strconv.ParseUint(settings["db_backup_target_id"], 10, 64); id > 0 {
			if n, err := storageCount(tx, uint(id)); err != nil {
				return err
			} else if n > 0 {
				backup = uint(id)
			}
		} else if parseBoolSetting(settings["db_backup_enabled"], false) {
			backup = legacy.s3 // "0" used to mean the legacy S3 settings
		}
		if _, err := tx.Exec("INSERT INTO site_settings (`key`, value) VALUES ('db_backup_storage_id', ?) ON DUPLICATE KEY UPDATE value=VALUES(value)", strconv.FormatUint(uint64(backup), 10)); err != nil {
			return err
		}
	}
	for _, key := range retiredStorageSettings {
		if _, err := tx.Exec("DELETE FROM site_settings WHERE `key`=?", key); err != nil {
			return err
		}
	}
	return nil
}

func dropLegacyStorage(tx *sql.Tx) error {
	for _, table := range []string{"storage_target_replicas", "storage_targets", "image_replicas", "storage_job_items", "storage_jobs", "upload_policies"} {
		if _, err := tx.Exec("DROP TABLE IF EXISTS " + quoteIdentifier(table)); err != nil {
			return err
		}
	}
	for _, index := range []string{"idx_images_storage_mode", "idx_images_user_storage_cursor", "idx_images_storage_cursor"} {
		if ok, err := indexExists(tx, "images", index); err != nil {
			return err
		} else if ok {
			if _, err := tx.Exec("ALTER TABLE images DROP INDEX " + quoteIdentifier(index)); err != nil {
				return err
			}
		}
	}
	for _, column := range []string{"storage_mode", "storage_key"} {
		if ok, err := columnExists(tx, "images", column); err != nil {
			return err
		} else if ok {
			if _, err := tx.Exec("ALTER TABLE images DROP COLUMN " + quoteIdentifier(column)); err != nil {
				return err
			}
		}
	}
	return nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseBoolSetting(raw string, fallback bool) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return fallback
	}
	return raw == "true" || raw == "1" || raw == "yes" || raw == "on"
}
