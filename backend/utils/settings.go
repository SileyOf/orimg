package utils

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Settings are read on nearly every request (CORS, rate limits, storage
// paths), so a short-lived per-database snapshot replaces one query per key.
// Writes through this process invalidate it immediately; changes made by other
// processes or direct SQL become visible within settingsCacheTTL.
const settingsCacheTTL = 2 * time.Second

type settingsSnapshot struct {
	values map[string]string
	loaded time.Time
}

type settingsCache struct {
	mu         sync.Mutex
	snapshot   *settingsSnapshot
	generation uint64
}

var settingsCaches sync.Map // *sql.DB -> *settingsCache

func cacheFor(db *sql.DB) *settingsCache {
	if cache, ok := settingsCaches.Load(db); ok {
		return cache.(*settingsCache)
	}
	cache, _ := settingsCaches.LoadOrStore(db, &settingsCache{})
	return cache.(*settingsCache)
}

// InvalidateSettings drops the cached snapshot after site_settings changes.
func InvalidateSettings(db *sql.DB) {
	cache := cacheFor(db)
	cache.mu.Lock()
	cache.snapshot = nil
	cache.generation++
	cache.mu.Unlock()
}

func cachedSettings(db *sql.DB) (map[string]string, bool) {
	cache := cacheFor(db)
	cache.mu.Lock()
	if snap := cache.snapshot; snap != nil && time.Since(snap.loaded) < settingsCacheTTL {
		cache.mu.Unlock()
		return snap.values, true
	}
	generation := cache.generation
	cache.mu.Unlock()
	values, err := loadSettings(db)
	if err != nil {
		return nil, false
	}
	cache.mu.Lock()
	if cache.generation == generation {
		cache.snapshot = &settingsSnapshot{values: values, loaded: time.Now()}
	}
	cache.mu.Unlock()
	return values, true
}

func loadSettings(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT ` + "`key`" + `, value FROM site_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err == nil {
			out[key] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func GetSetting(db *sql.DB, key string, fallback string) string {
	values, ok := cachedSettings(db)
	if !ok {
		return fallback
	}
	value, found := values[key]
	if !found {
		return fallback
	}
	return value
}

// GetSettings returns a private copy that callers may modify.
func GetSettings(db *sql.DB) map[string]string {
	values, ok := cachedSettings(db)
	out := make(map[string]string, len(values))
	if !ok {
		return out
	}
	for key, value := range values {
		out[key] = value
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
	InvalidateSettings(db)
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
