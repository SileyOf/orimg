package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLegacySessionKeyPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installation.json")
	var wg sync.WaitGroup
	keys := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := Config{JWTSecret: "change-me"}
			if err := cfg.PrepareSessionKey(path); err != nil {
				t.Error(err)
				return
			}
			if cfg.JWTSecret != "change-me" || ValidateSecret(cfg.SigningKey()) != nil {
				t.Error("legacy encryption key changed or signing key weak")
			}
			keys <- cfg.SigningKey()
		}()
	}
	wg.Wait()
	close(keys)
	var first string
	for key := range keys {
		if first != "" && key != first {
			t.Fatal("concurrent startups used different keys")
		}
		first = key
	}
	if first == "" {
		t.Fatal("no key persisted")
	}
	cfg := validInstallConfig()
	cfg.JWTSecret = "change-me"
	if err := WriteInstallation(path, &Installation{Installed: true, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	state, err := ReadInstallation(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Config.PrepareSessionKey(path); err != nil || state.Config.SigningKey() != first {
		t.Fatal("restart lost session key", err)
	}
	if state.Config.Validate() == nil {
		t.Fatal("fresh deployment accepted weak original key")
	}
	if err := os.WriteFile(SessionKeyPath(path), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if state.Config.PrepareSessionKey(path) == nil {
		t.Fatal("corrupt key silently replaced")
	}
}

func TestStrongSessionKeyCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installation.json")
	cfg := validInstallConfig()
	if err := cfg.PrepareSessionKey(path); err != nil || cfg.SigningKey() != cfg.JWTSecret {
		t.Fatal("strong existing session key changed", err)
	}
	if _, err := os.Stat(SessionKeyPath(path)); !os.IsNotExist(err) {
		t.Fatal("unnecessary session key created")
	}
	cfg.JWTSecret = ""
	if cfg.PrepareSessionKey(path) == nil {
		t.Fatal("missing original key guessed")
	}
}

func TestSessionKeyRejectsUnsafeFile(t *testing.T) {
	for _, mode := range []string{"public", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "installation.json")
			cfg := validInstallConfig()
			target := SessionKeyPath(path)
			if mode == "symlink" {
				if err := os.Symlink("missing", target); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(target, []byte(cfg.JWTSecret), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if cfg.PrepareSessionKey(path) == nil {
				t.Fatal("unsafe key file accepted")
			}
		})
	}
}
