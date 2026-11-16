package database

import "database/sql"

func migrateStorageTargets(tx *sql.Tx) error {
	// Additive migration: legacy paths, secrets, replica records and settings stay untouched.
	statements := []string{
		`CREATE TABLE IF NOT EXISTS storage_targets (
		 id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		 name VARCHAR(80) NOT NULL,
		 provider VARCHAR(24) NOT NULL,
		 legacy_mode VARCHAR(20) NULL,
		 config_json TEXT NOT NULL,
		 enabled TINYINT(1) NOT NULL DEFAULT 1,
		 auto_backup TINYINT(1) NOT NULL DEFAULT 0,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_storage_targets_legacy (legacy_mode)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`INSERT IGNORE INTO storage_targets (name, provider, legacy_mode, config_json) VALUES
		 ('Local', 'local', 'local', '{}'), ('S3', 's3', 's3', '{}')`,
		`CREATE TABLE IF NOT EXISTS storage_target_replicas (
		 image_id BIGINT UNSIGNED NOT NULL,
		 target_id BIGINT UNSIGNED NOT NULL,
		 storage_key VARCHAR(1024) NOT NULL DEFAULT '',
		 is_primary TINYINT(1) NOT NULL DEFAULT 0,
		 status VARCHAR(20) NOT NULL DEFAULT 'pending',
		 checksum_sha256 CHAR(64) NOT NULL DEFAULT '',
		 size BIGINT NOT NULL DEFAULT 0,
		 attempts INT NOT NULL DEFAULT 0,
		 last_error VARCHAR(1000) NOT NULL DEFAULT '',
		 lease_token VARCHAR(64) NOT NULL DEFAULT '',
		 lease_expires_at DATETIME NULL,
		 next_retry_at DATETIME NULL,
		 verified_at DATETIME NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		 PRIMARY KEY (image_id, target_id),
		 INDEX idx_target_replica_queue (status, next_retry_at, image_id),
		 INDEX idx_target_replica_target (target_id, status, image_id),
		 CONSTRAINT fk_replica_target FOREIGN KEY (target_id) REFERENCES storage_targets(id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
