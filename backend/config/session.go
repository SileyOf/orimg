package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (c *Config) SigningKey() string {
	if c.SessionSecret != "" {
		return c.SessionSecret
	}
	return c.JWTSecret
}

func SessionKeyPath(installationPath string) string { return installationPath + ".session-key" }

// Call only after verifying the original database key. Never replace that key:
// storage credentials and queued mail must remain decryptable.
func (c *Config) PrepareSessionKey(installationPath string) error {
	path := SessionKeyPath(installationPath)
	read := func() error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
			return errors.New("session key must be a private regular file (0600)")
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if ValidateSecret(string(key)) != nil {
			return errors.New("invalid session key file; restore its backup, do not replace JWT_SECRET")
		}
		c.SessionSecret = string(key)
		return nil
	}
	if err := read(); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if ValidateSecret(c.JWTSecret) == nil {
		c.SessionSecret = ""
		return nil
	}
	if c.JWTSecret == "" {
		return errors.New("original JWT_SECRET is missing; restore the original key")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("cannot create session key directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".session-key-*")
	if err != nil {
		return fmt.Errorf("cannot persist session key; check data directory permissions: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(hex.EncodeToString(key)); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Publish a complete key without overwriting a concurrent startup's winner.
	if err = os.Link(f.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("cannot persist session key: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return err
	}
	return read()
}
