package config

import (
	"bufio"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type S3Config struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
}

const AppVersion = "v0.7.0"

type Config struct {
	DBTLS                 string
	DBTLSCA               string
	AssetDir              string
	TrustedProxies        string
	DisableBackgroundWork bool
	DBHost                string
	DBPort                string
	DBUser                string
	DBPassword            string
	DBName                string
	DBMaxOpen             int
	DBMaxIdle             int
	DBConnLifetime        time.Duration
	JWTSecret             string
	SessionSecret         string `json:"-"`
	JWTExpiry             time.Duration
	Port                  string
	BaseURL               string
	UploadDir             string
	CacheDir              string
	CacheTTL              time.Duration
	CleanupInterval       time.Duration
	MaxUploadMB           int64
	StorageMode           string
	AdminUsername         string
	AdminEmail            string
	AdminPassword         string `json:"-"`
	S3                    S3Config
}

func Load() *Config {
	if err := loadDotEnv(".env"); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	jwtExpiry := parseDuration("JWT_EXPIRY", 24*time.Hour)
	cacheTTL := parseDuration("CACHE_TTL", 30*24*time.Hour)
	cleanupInterval := parseDuration("CACHE_CLEANUP_INTERVAL", 12*time.Hour)
	maxUploadMB := parseInt64("MAX_UPLOAD_MB", 20)
	storageMode := strings.ToLower(getEnv("STORAGE_MODE", "local"))
	if storageMode != "local" && storageMode != "s3" && storageMode != "mirror" {
		storageMode = "local"
	}

	return &Config{
		DBTLS:                 getEnv("DB_TLS", "disabled"),
		DBTLSCA:               getEnv("DB_TLS_CA", ""),
		AssetDir:              getEnv("ASSET_DIR", "data/site-assets"),
		TrustedProxies:        getEnv("TRUSTED_PROXIES", ""),
		DisableBackgroundWork: parseBool("DISABLE_BACKGROUND_WORK", false),
		DBHost:                getEnv("DB_HOST", "127.0.0.1"),
		DBPort:                getEnv("DB_PORT", "3306"),
		DBUser:                getEnv("DB_USER", "root"),
		DBPassword:            getEnv("DB_PASSWORD", ""),
		DBName:                getEnv("DB_NAME", "zhuapic"),
		DBMaxOpen:             int(parseInt64("DB_MAX_OPEN_CONNS", 30)),
		DBMaxIdle:             int(parseInt64("DB_MAX_IDLE_CONNS", 10)),
		DBConnLifetime:        parseDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		JWTSecret:             getEnv("JWT_SECRET", ""),
		JWTExpiry:             jwtExpiry,
		Port:                  getEnv("PORT", "8080"),
		BaseURL:               strings.TrimRight(getEnv("BASE_URL", "http://localhost:8080"), "/"),
		UploadDir:             getEnv("UPLOAD_DIR", "data/uploads"),
		CacheDir:              getEnv("CACHE_DIR", "data/cache"),
		CacheTTL:              cacheTTL,
		CleanupInterval:       cleanupInterval,
		MaxUploadMB:           maxUploadMB,
		StorageMode:           storageMode,
		AdminUsername:         getEnv("ADMIN_USERNAME", "admin"),
		AdminEmail:            strings.ToLower(strings.TrimSpace(getEnv("ADMIN_EMAIL", ""))),
		AdminPassword:         getEnv("ADMIN_PASSWORD", ""),
		S3: S3Config{
			Endpoint:       strings.TrimRight(getEnv("S3_ENDPOINT", ""), "/"),
			Region:         getEnv("S3_REGION", "auto"),
			Bucket:         getEnv("S3_BUCKET", ""),
			AccessKey:      getEnv("S3_ACCESS_KEY", ""),
			SecretKey:      getEnv("S3_SECRET_KEY", ""),
			ForcePathStyle: parseBool("S3_FORCE_PATH_STYLE", true),
		},
	}
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

func (c *Config) MaxUploadBytes() int64 {
	return c.MaxUploadMB * 1024 * 1024
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func parseDuration(key string, fallback time.Duration) time.Duration {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseInt64(key string, fallback int64) int64 {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(getEnv(key, "")))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}
