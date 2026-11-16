package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Installation struct {
	Installed bool   `json:"installed"`
	Fresh     bool   `json:"fresh_database"`
	Config    Config `json:"config"`
}

func InstallationPath() string { return getEnv("INSTALL_CONFIG", "data/installation.json") }

func ReadInstallation(path string) (*Installation, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state Installation
	if err = json.Unmarshal(body, &state); err != nil {
		return nil, errors.New("invalid installation configuration; restore the configuration backup")
	}
	if err = state.Config.ValidateConnection(); err != nil {
		return nil, err
	}
	if state.Config.JWTSecret == "" {
		return nil, errors.New("original JWT_SECRET is missing; restore the configuration backup")
	}
	if state.Fresh {
		if err = ValidateSecret(state.Config.JWTSecret); err != nil {
			return nil, err
		}
	}
	return &state, nil
}

// Commit a durable checkpoint before database changes; interrupted setup can resume.
func WriteInstallation(path string, state *Installation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".install-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func HasLegacyConfiguration() bool {
	for _, key := range []string{"DB_HOST", "DB_USER", "DB_NAME", "JWT_SECRET"} {
		if _, ok := os.LookupEnv(key); ok {
			return true
		}
	}
	return false
}

func ValidateSecret(secret string) error {
	if len(secret) < 32 || strings.TrimSpace(secret) != secret || strings.ContainsAny(secret, "\r\n") ||
		strings.Contains(strings.ToLower(secret), "change-me") || strings.Contains(strings.ToLower(secret), "replace-") {
		return errors.New("JWT_SECRET must be at least 32 characters and must not be a placeholder; preserve the original key for existing databases")
	}
	unique := map[rune]bool{}
	for _, r := range secret {
		unique[r] = true
	}
	if len(unique) < 8 {
		return errors.New("JWT_SECRET has insufficient diversity")
	}
	return nil
}

func ValidateAdmin(username, email, password string) error {
	if len(username) < 3 || len(username) > 80 {
		return errors.New("administrator username must have 3-80 characters")
	}
	for _, r := range username {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.-", r)) {
			return errors.New("invalid administrator username")
		}
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 190 {
		return errors.New("invalid administrator email")
	}
	if len(password) < 12 || len(password) > 128 {
		return errors.New("administrator password must have 12-128 bytes")
	}
	return nil
}

func (c *Config) Validate() error {
	if err := ValidateSecret(c.JWTSecret); err != nil {
		return err
	}
	return c.ValidateConnection()
}

// Legacy encryption keys are checked against the database before session setup.
func (c *Config) ValidateConnection() error {
	if c.DBHost == "" || strings.ContainsAny(c.DBHost, "/\\ \r\n") || c.DBUser == "" || c.DBName == "" {
		return errors.New("database host, user and database name are required")
	}
	for _, r := range c.DBName {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return errors.New("database name must contain only letters, digits and underscores")
		}
	}
	for _, port := range []string{c.Port, c.DBPort} {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("invalid port")
		}
	}
	if c.DBTLS != "disabled" && c.DBTLS != "verify" {
		return errors.New("DB_TLS must be disabled or verify")
	}
	if c.DBMaxOpen < 2 || c.DBMaxIdle < 0 || c.DBConnLifetime <= 0 || c.JWTExpiry <= 0 || c.CacheTTL <= 0 || c.CleanupInterval <= 0 {
		return errors.New("invalid pool or timeout configuration (DB_MAX_OPEN_CONNS must be >= 2)")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("BASE_URL must be an HTTP(S) site root")
	}
	for _, entry := range strings.Split(c.TrustedProxies, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return fmt.Errorf("invalid trusted proxy: %s", entry)
			}
		}
	}
	if c.UploadDir == "" || c.CacheDir == "" || c.AssetDir == "" {
		return errors.New("data directories are required")
	}
	return nil
}
