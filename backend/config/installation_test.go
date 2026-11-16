package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validInstallConfig() Config {
	return Config{DBHost: "127.0.0.1", DBPort: "3306", DBUser: "fixture", DBName: "fixture", DBTLS: "disabled", DBMaxOpen: 2,
		DBConnLifetime: time.Minute, JWTSecret: "0123456789abcdef0123456789abcdef", JWTExpiry: time.Hour, Port: "8080", BaseURL: "https://example.com",
		CacheTTL: time.Hour, CleanupInterval: time.Hour, UploadDir: "data/uploads", CacheDir: "data/cache", AssetDir: "data/assets"}
}

func TestInstallationCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "installation.json")
	state, err := ReadInstallation(path)
	if err != nil || state != nil {
		t.Fatal("missing file should mean uninstalled")
	}
	cfg := validInstallConfig()
	cfg.AdminPassword = "must-never-be-persisted"
	if err := WriteInstallation(path, &Installation{Config: cfg, Fresh: true}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), cfg.AdminPassword) || strings.Contains(string(body), "AdminPassword") {
		t.Fatal("administrator password persisted")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %o", info.Mode().Perm())
	}
	state, err = ReadInstallation(path)
	if err != nil || state.Installed || !state.Fresh || state.Config.JWTSecret != cfg.JWTSecret {
		t.Fatalf("checkpoint: %+v %v", state, err)
	}
	state.Installed = true
	if err := WriteInstallation(path, state); err != nil {
		t.Fatal(err)
	}
	state, err = ReadInstallation(path)
	if err != nil || !state.Installed {
		t.Fatal("commit failed", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInstallation(path); err == nil {
		t.Fatal("corruption must fail closed")
	}
}

func TestConfigurationValidation(t *testing.T) {
	for _, secret := range []string{"", "change-me-0123456789abcdef0123456789", strings.Repeat("a", 64), "0123456789abcdef0123456789abcdef\n"} {
		if ValidateSecret(secret) == nil {
			t.Fatal("weak secret accepted")
		}
	}
	cfg := validInstallConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.DBTLS = "skip-verify" }, func(c *Config) { c.DBMaxOpen = 1 }, func(c *Config) { c.DBName = "bad;sql" },
		func(c *Config) { c.BaseURL = "https://name:pass@example.com" }, func(c *Config) { c.TrustedProxies = "not-an-ip" },
	} {
		bad := cfg
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if ValidateAdmin("owner", "owner@example.test", "long-fixture-password") != nil {
		t.Fatal("valid admin rejected")
	}
	if ValidateAdmin("owner", "owner@example.test", "admin") == nil {
		t.Fatal("short password accepted")
	}
}
