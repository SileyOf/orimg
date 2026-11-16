package utils

import (
	"path/filepath"
	"testing"
	"time"

	"zhuapic/config"
)

func TestIndependentSessionKeyRejectsLegacyTokens(t *testing.T) {
	cfg := config.Config{JWTSecret: "change-me", JWTExpiry: time.Hour}
	old, err := GenerateToken(&cfg, 1, "owner", "owner@example.test", "admin")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := EncryptSecret("saved-smtp-password", cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "installation.json")
	if err = cfg.PrepareSessionKey(path); err != nil {
		t.Fatal(err)
	}
	if _, err = ParseToken(&cfg, old); err == nil {
		t.Fatal("weak-key token still accepted")
	}
	token, err := GenerateToken(&cfg, 1, "owner", "owner@example.test", "admin")
	if err != nil {
		t.Fatal(err)
	}
	restart := config.Config{JWTSecret: cfg.JWTSecret, JWTExpiry: time.Hour}
	if err = restart.PrepareSessionKey(path); err != nil {
		t.Fatal(err)
	}
	if _, err = ParseToken(&restart, token); err != nil {
		t.Fatal("restart invalidated new token", err)
	}
	if plain, err := DecryptSecret(encrypted, restart.JWTSecret); err != nil || plain != "saved-smtp-password" {
		t.Fatal("encrypted credentials lost", err)
	}
}
