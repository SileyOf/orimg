package utils

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

func GetSetting(db *sql.DB, key string, fallback string) string {
	var value string
	if err := db.QueryRow(`SELECT value FROM site_settings WHERE `+"`key`"+` = ?`, key).Scan(&value); err != nil {
		return fallback
	}
	return value
}

func GetSettings(db *sql.DB) map[string]string {
	rows, err := db.Query(`SELECT ` + "`key`" + `, value FROM site_settings`)
	if err != nil {
		return map[string]string{}
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err == nil {
			out[key] = value
		}
	}
	return out
}

func UpsertSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		`INSERT INTO site_settings (`+"`key`"+`, value) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = CURRENT_TIMESTAMP`,
		key,
		value,
	)
	return err
}

func SettingBool(db *sql.DB, key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(GetSetting(db, key, "")))
	if raw == "" {
		return fallback
	}
	return raw == "true" || raw == "1" || raw == "yes" || raw == "on"
}

func SettingInt64(db *sql.DB, key string, fallback int64, min int64, max int64) int64 {
	raw := strings.TrimSpace(GetSetting(db, key, ""))
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	if parsed < min {
		return min
	}
	if max > 0 && parsed > max {
		return max
	}
	return parsed
}

func SettingCacheTTL(db *sql.DB, fallback time.Duration) time.Duration {
	days := SettingInt64(db, "cache_ttl_days", int64(fallback.Hours()/24), 1, 365)
	return time.Duration(days) * 24 * time.Hour
}
