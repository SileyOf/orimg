package storage

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"zhuapic/config"
	"zhuapic/internal/testsupport"
	"zhuapic/models"
)

func TestTargetValidation(t *testing.T) {
	for _, tgt := range []Target{{Name: "", Provider: "local"}, {Name: "test", Provider: "ftp"}, {Name: "test", Provider: "local", Config: TargetConfig{Directory: "/"}}, {Name: "test", Provider: "local", Config: TargetConfig{Directory: "relative"}}} {
		if ValidateTarget(&tgt) == nil {
			t.Fatalf("accepted invalid target: %+v", tgt)
		}
	}
	good := Target{Name: "NAS", Provider: "local", Config: TargetConfig{Directory: "/mnt/orimg"}}
	if err := ValidateTarget(&good); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"file:///etc/passwd", "https://user:pass@example.com", "https://example.com/?token=a", "https://example.com/#secret"} {
		target := Target{Name: "bad", Provider: "s3", Config: TargetConfig{Endpoint: endpoint, Region: "auto", Bucket: "images", AccessKey: "a", SecretKey: "b"}}
		if ValidateTarget(&target) == nil {
			t.Fatal("accepted invalid endpoint")
		}
	}
}

func TestStageReplicaExactSize(t *testing.T) {
	for _, size := range []int64{2, 4} {
		if f, _, err := stageReplica(strings.NewReader("abc"), size); err == nil {
			f.Close()
			os.Remove(f.Name())
			t.Fatal("size mismatch accepted")
		}
	}
	f, hash, err := stageReplica(strings.NewReader("abc"), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if hash != ChecksumSHA256([]byte("abc")) {
		t.Fatal("bad checksum")
	}
}

func TestTargetStorageApprovedDatabase(t *testing.T) {
	dsn := os.Getenv("ORIMG_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit test database required")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil || !testsupport.IsIsolatedDatabase(parsed) {
		t.Fatal("requires ORIMG_ALLOW_DB_TESTS=1 and a loopback zhuapictest database on an unprivileged TCP port")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	ctx := context.Background()
	var foreignQueue int
	if err = db.QueryRow("SELECT COUNT(*) FROM storage_target_replicas WHERE status<>'ready'").Scan(&foreignQueue); err != nil {
		t.Fatal("run migration test first:", err)
	}
	if foreignQueue != 0 {
		t.Fatal("test DB has existing named jobs; refusing to run worker")
	}
	var settingsBefore sql.NullString
	_ = db.QueryRow("SELECT value FROM site_settings WHERE `key`='default_storage_target_id'").Scan(&settingsBefore)
	defer func() {
		if settingsBefore.Valid {
			_, _ = db.Exec("UPDATE site_settings SET value=? WHERE `key`='default_storage_target_id'", settingsBefore.String)
		} else {
			_, _ = db.Exec("DELETE FROM site_settings WHERE `key`='default_storage_target_id'")
		}
	}()
	cfg := &config.Config{DBName: "zhuapictest", JWTSecret: "isolated-target-integration-secret", UploadDir: t.TempDir(), CacheDir: t.TempDir()}
	c := NewCoordinator(db, cfg, NewManager(cfg))
	var mu sync.Mutex
	objects := map[string][]byte{}
	failPUT := false
	failGET := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			w.WriteHeader(403)
			return
		}
		switch r.Method {
		case "PUT":
			if failPUT {
				w.WriteHeader(503)
				return
			}
			b, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = b
			w.WriteHeader(200)
		case "GET":
			if failGET {
				w.WriteHeader(503)
				return
			}
			b, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(b)
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	primary := Target{Name: "test-primary-" + time.Now().Format("150405"), Provider: "local", Enabled: true, Config: TargetConfig{Directory: t.TempDir()}}
	cloud := Target{Name: "test-s3", Provider: "s3", Enabled: true, AutoBackup: true, Config: TargetConfig{Endpoint: server.URL, Region: "us-east-1", Bucket: "test-only", Prefix: "images", AccessKey: "test-access", SecretKey: "test-secret", PathStyle: true}}
	backup := Target{Name: "test-local-backup", Provider: "local", Enabled: true, AutoBackup: true, Config: TargetConfig{Directory: t.TempDir()}}
	ids := []uint{}
	imageIDs := []uint{}
	defer func() {
		for _, id := range ids {
			_, _ = db.Exec("DELETE FROM storage_target_replicas WHERE target_id=?", id)
		}
		for _, id := range imageIDs {
			_, _ = db.Exec("DELETE FROM images WHERE id=?", id)
		}
		for _, id := range ids {
			_, _ = db.Exec("DELETE FROM storage_targets WHERE id=?", id)
		}
	}()
	for _, target := range []*Target{&primary, &cloud, &backup} {
		if err = c.SaveTarget(ctx, target); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, target.ID)
	}
	if err = c.ProbeTarget(ctx, cloud.ID); err != nil {
		t.Fatal(err)
	}
	if len(objects) != 0 {
		t.Fatal("probe object was not cleaned")
	}
	if err = c.SetDefaultTarget(ctx, primary.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteTarget(ctx, primary.ID); err == nil {
		t.Fatal("default storage deletion allowed")
	}
	listed, err := c.ListTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range listed {
		if target.Config.SecretKey != "" {
			t.Fatal("secret leaked")
		}
	}
	var encrypted string
	if err = db.QueryRow("SELECT config_json FROM storage_targets WHERE id=?", cloud.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "test-secret") {
		t.Fatal("plaintext credential stored")
	}
	content := bytes.Repeat([]byte("orimg fixture "), 4096)
	image := models.Image{PublicName: "orimg-v04-fixture-" + time.Now().Format("150405") + ".png", MimeType: "image/png", Size: int64(len(content)), StorageMode: "local"}
	opts := c.TargetOptions(&primary)
	if err = c.manager.SaveReader(ctx, opts, "local", &image, bytes.NewReader(content), image.Size, ChecksumSHA256(content)); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec("INSERT INTO images (public_name,original_name,mime_type,ext,size,storage_mode,storage_key) VALUES (?,?,'image/png','.png',?,'local',?)", image.PublicName, "isolated v04 fixture", image.Size, image.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	image.ID = uint(id)
	imageIDs = append(imageIDs, image.ID)
	if err = c.RegisterTargetImage(ctx, &image, &primary, ChecksumSHA256(content)); err != nil {
		t.Fatal(err)
	}
	var replicas int
	if err = db.QueryRow("SELECT COUNT(*) FROM storage_target_replicas WHERE image_id=?", image.ID).Scan(&replicas); err != nil || replicas != 3 {
		t.Fatal("automatic backups not queued", replicas, err)
	}
	if err = c.DeleteTarget(ctx, primary.ID); err == nil {
		t.Fatal("referenced target deletion allowed")
	}
	changed := primary
	changed.Config.Directory = filepath.Join(primary.Config.Directory, "changed")
	if err = c.SaveTarget(ctx, &changed); err == nil {
		t.Fatal("location rewrite allowed")
	}
	// Only the newly created target queue is processed, never the legacy production job queue.
	for i := 0; i < 3; i++ {
		if err = c.processTargetReplica(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var ready int
	db.QueryRow("SELECT COUNT(*) FROM storage_target_replicas WHERE image_id=? AND status='ready'", image.ID).Scan(&ready)
	if ready != 3 {
		t.Fatal("multi-location replicas not ready", ready)
	}
	if err = os.Remove(c.manager.LocalPath(opts, image.StorageKey)); err != nil {
		t.Fatal(err)
	}
	reader, err := c.imageSource(ctx, image.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := io.ReadAll(reader)
	reader.Close()
	if !bytes.Equal(restored, content) {
		t.Fatal("backup fallback content mismatch")
	}
	if _, err = c.QueueTarget(ctx, cloud.ID, "verify"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	failGET = true
	mu.Unlock()
	if err = c.processTargetReplica(ctx); err != nil {
		t.Fatal(err)
	}
	var verificationStatus string
	if err = db.QueryRow("SELECT status FROM storage_target_replicas WHERE image_id=? AND target_id=?", image.ID, cloud.ID).Scan(&verificationStatus); err != nil || verificationStatus != "verify_failed" {
		t.Fatal("verification intent lost", verificationStatus, err)
	}
	mu.Lock()
	failGET = false
	failPUT = true
	mu.Unlock()
	if _, err = c.QueueTarget(ctx, cloud.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	if err = c.processTargetReplica(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT status FROM storage_target_replicas WHERE image_id=? AND target_id=?", image.ID, cloud.ID).Scan(&verificationStatus); err != nil || verificationStatus != "ready" {
		t.Fatal("verification retry unexpectedly wrote data", verificationStatus, err)
	}
	mu.Lock()
	failPUT = false
	mu.Unlock()
	// Interrupted writes recover using a lease and can be retried.
	_, err = db.Exec("UPDATE storage_target_replicas SET status='syncing',lease_expires_at=DATE_SUB(NOW(),INTERVAL 1 MINUTE) WHERE image_id=? AND target_id=?", image.ID, cloud.ID)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	failPUT = true
	mu.Unlock()
	if err = c.processTargetReplica(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	db.QueryRow("SELECT status FROM storage_target_replicas WHERE image_id=? AND target_id=?", image.ID, cloud.ID).Scan(&status)
	if status != "failed" {
		t.Fatal("failed write not surfaced", status)
	}
	mu.Lock()
	failPUT = false
	mu.Unlock()
	if _, err = c.QueueTarget(ctx, cloud.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	if err = c.processTargetReplica(ctx); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT status FROM storage_target_replicas WHERE image_id=? AND target_id=?", image.ID, cloud.ID).Scan(&status)
	if status != "ready" {
		t.Fatal("retry failed", status)
	}
	// Queue historic records, inspect idempotency, then remove only our new task rows before any worker executes.
	n, err := c.QueueTarget(ctx, backup.ID, "backfill")
	if err != nil {
		t.Fatal(err)
	}
	n2, err := c.QueueTarget(ctx, backup.ID, "backfill")
	if err != nil || n2 != 0 {
		t.Fatal("backfill not idempotent")
	}
	_, _ = db.Exec("DELETE FROM storage_target_replicas WHERE target_id=? AND image_id<>?", backup.ID, image.ID)
	t.Logf("historic queue planned %d records; no historic files read or modified", n)
	_, _ = db.Exec("UPDATE images SET deleted_at=NOW(),delete_after=DATE_SUB(NOW(),INTERVAL 1 MINUTE) WHERE id=?", image.ID)
	if err = c.purgeImageByID(ctx, image.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM storage_target_replicas WHERE image_id=?", image.ID).Scan(&count)
	if count != 0 {
		t.Fatal("purge left replica records")
	}
	t.Log("local + S3 + local backups, encrypted secrets, fallback, retry, verify, purge and deletion guards passed; fixtures cleaned")
}
