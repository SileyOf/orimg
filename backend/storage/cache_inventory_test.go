package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zhuapic/config"
)

func TestCacheInventoryAndCleanup(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write := func(name string, size int, age time.Duration) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return path
	}
	expired := write("000000000000000000000001.jpg", 100, 48*time.Hour)
	fresh := write("000000000000000000000002.png", 200, time.Hour)
	temporary := write("000000000000000000000003.jpg.tmp", 500, 48*time.Hour)
	unknown := write("keep-me.sql.gz", 600, 48*time.Hour)
	backup := filepath.Join(dir, "database-backups")
	if err := os.Mkdir(backup, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "snapshot.sql"), []byte("backup"), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "original.jpg")
	if err := os.WriteFile(external, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "000000000000000000000004.jpg")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	stats := scanCache(context.Background(), dir, 24*time.Hour, now, 100)
	if stats.Status != "ready" || stats.Partial || stats.Files != 2 || stats.Bytes != 300 || stats.ExpiredFiles != 1 || stats.ExpiredBytes != 100 {
		t.Fatalf("incorrect stats: %+v", stats)
	}
	if len(stats.Largest) != 2 || stats.Largest[0].Bytes != 200 {
		t.Fatal("incorrect largest ordering")
	}
	if _, err := os.Stat(expired); err != nil {
		t.Fatal("inventory modified files")
	}
	cleanCache(context.Background(), dir, 24*time.Hour, now)
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatal("expired cache not removed")
	}
	for _, path := range []string{fresh, temporary, unknown, backup, external, link} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("cleanup touched non-cache: %s", path)
		}
	}
}

func TestCacheInventoryBoundsAndMemo(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i := 1; i <= 30; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%024x.jpg", i)), make([]byte, i), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stats := scanCache(context.Background(), dir, time.Hour, now, 100)
	if stats.Files != 30 || len(stats.Largest) != 20 || stats.Largest[0].Bytes != 30 || stats.Largest[19].Bytes != 11 {
		t.Fatalf("top files: %+v", stats)
	}
	if !scanCache(context.Background(), dir, time.Hour, now, 3).Partial {
		t.Fatal("scan limit not reported")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !scanCache(ctx, dir, time.Hour, now, 100).Partial {
		t.Fatal("cancellation not reported")
	}
	if scanCache(context.Background(), filepath.Join(dir, "missing"), time.Hour, now, 100).Status != "empty" {
		t.Fatal("missing cache must be empty")
	}
	if scanCache(context.Background(), filepath.Join(dir, fmt.Sprintf("%024x.jpg", 1)), time.Hour, now, 100).Status != "unavailable" {
		t.Fatal("invalid directory must not report ready")
	}
	m := NewManager(&config.Config{CacheDir: dir})
	a := m.CacheInventory(context.Background(), Options{CacheDir: dir}, time.Hour)
	b := m.CacheInventory(context.Background(), Options{CacheDir: dir}, time.Hour)
	if !a.ScannedAt.Equal(b.ScannedAt) {
		t.Fatal("repeated requests rescanned directory")
	}
	c := m.CacheInventory(context.Background(), Options{CacheDir: dir}, 2*time.Hour)
	if c.TTLSeconds != 7200 {
		t.Fatal("TTL change did not invalidate stats")
	}
}
