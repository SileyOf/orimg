package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseArchiveLocalPrivateAndComplete(t *testing.T) {
	root := t.TempDir()
	opts := Options{UploadDir: root}
	body := []byte(strings.Repeat("private database fixture\n", 100000))
	key := "backups/database/test.sql.gz"
	if err := writeDatabaseArchive(context.Background(), opts, "local", key, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, filepath.FromSlash(key))
	actual, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(body, actual) {
		t.Fatal("backup content mismatch", err)
	}
	stat, err := os.Stat(dest)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("archive must be private", err)
	}
	if err := writeDatabaseArchive(context.Background(), opts, "local", key, bytes.NewReader([]byte("short")), 99); err == nil {
		t.Fatal("partial archive accepted")
	}
	actual, _ = os.ReadFile(dest)
	if !bytes.Equal(body, actual) {
		t.Fatal("failed write replaced valid archive")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".backup-*"))
	if len(files) != 0 {
		t.Fatal("temporary file leaked")
	}
	for _, key := range []string{"../escape", "/absolute"} {
		if writeDatabaseArchive(context.Background(), opts, "local", key, bytes.NewReader(body), int64(len(body))) == nil {
			t.Fatal("unsafe backup path accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if writeDatabaseArchive(ctx, opts, "local", "cancelled", bytes.NewReader(body), int64(len(body))) == nil {
		t.Fatal("cancelled write accepted")
	}
}
