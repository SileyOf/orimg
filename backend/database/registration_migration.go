package database

import "database/sql"

func migrateRegistration(tx *sql.Tx) error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS mail_templates(kind VARCHAR(24) PRIMARY KEY,content_json TEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS registration_settings(id INT PRIMARY KEY,enabled BOOLEAN NOT NULL DEFAULT 0,group_id BIGINT UNSIGNED NOT NULL DEFAULT 0) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`INSERT IGNORE INTO registration_settings(id) VALUES(1)`,
		`CREATE TABLE IF NOT EXISTS registration_requests(email VARCHAR(190) PRIMARY KEY,token_hash CHAR(64) NOT NULL UNIQUE,group_id BIGINT UNSIGNED NOT NULL,expires_at DATETIME NOT NULL,consumed_at DATETIME NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX idx_registration_expiry(expires_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	} {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}
	return nil
}
