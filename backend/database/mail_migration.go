package database

import "database/sql"

func migrateMail(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='users' AND COLUMN_NAME='auth_version'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := tx.Exec(`ALTER TABLE users ADD COLUMN auth_version BIGINT UNSIGNED NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS mail_settings(id INT PRIMARY KEY,config_json TEXT NOT NULL,tested_hash CHAR(64) NOT NULL DEFAULT '',updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS mail_outbox(id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,message_key CHAR(32) NOT NULL UNIQUE,kind VARCHAR(24) NOT NULL,recipient_mask VARCHAR(190) NOT NULL,payload MEDIUMTEXT NOT NULL,challenge_hash CHAR(64) NOT NULL DEFAULT '',config_hash CHAR(64) NOT NULL,status VARCHAR(24) NOT NULL DEFAULT 'queued',attempts INT NOT NULL DEFAULT 0,last_error VARCHAR(255) NOT NULL DEFAULT '',next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,lease CHAR(32) NOT NULL DEFAULT '',lease_until DATETIME NULL,expires_at DATETIME NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,finished_at DATETIME NULL,INDEX idx_mail_work(status,next_attempt_at,id),INDEX idx_mail_expiry(expires_at),INDEX idx_mail_created(created_at,id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS account_challenges(token_hash CHAR(64) PRIMARY KEY,user_id BIGINT UNSIGNED NOT NULL,purpose VARCHAR(24) NOT NULL,email VARCHAR(190) NOT NULL,old_email VARCHAR(190) NOT NULL,auth_version BIGINT UNSIGNED NOT NULL,expires_at DATETIME NOT NULL,consumed_at DATETIME NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX idx_challenge_user(user_id,purpose,created_at),INDEX idx_challenge_expiry(expires_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS mail_limits(bucket CHAR(64) PRIMARY KEY,window_start BIGINT NOT NULL,hits INT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
