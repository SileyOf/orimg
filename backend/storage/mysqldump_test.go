package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMysqldumpFallsBackToPath(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "mysqldump")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := resolveMysqldump("/www/server/mysql/bin/mysqldump")
	if err != nil || got != fake {
		t.Fatalf("expected PATH fallback, got %q %v", got, err)
	}
	if got, err = resolveMysqldump(fake); err != nil || got != fake {
		t.Fatalf("configured path not honoured: %q %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err = resolveMysqldump("/missing/mysqldump"); err == nil {
		t.Fatal("expected failure without any mysqldump")
	}
}
