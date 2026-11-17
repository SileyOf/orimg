package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"zhuapic/config"
	"zhuapic/internal/testsupport"
	"zhuapic/models"
)

// TestStorageLifecycleOnApprovedTestDB exercises the unified storage model
// against MySQL: replication, repair, circuit breaking, verification,
// removal (drain and force) and purging through the deletion queue.
func TestStorageLifecycleOnApprovedTestDB(t *testing.T) {
	dsn := os.Getenv("ORIMG_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated test database required")
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
	ctx := context.Background()
	cfg := &config.Config{JWTSecret: "storage-fixture-secret-0123456789abcdef", UploadDir: t.TempDir(), CacheDir: t.TempDir(), CacheTTL: time.Hour}
	s := NewService(db, cfg, NewManager(cfg))
	suffix := fmt.Sprint(time.Now().UnixNano())
	var storageIDs, imageIDs []uint
	previousDefault := s.DefaultID()
	_, _ = db.Exec("INSERT INTO site_settings (`key`,value) VALUES ('disk_stop_percent','99') ON DUPLICATE KEY UPDATE value='99'")
	defer func() {
		for _, id := range imageIDs {
			db.Exec(`DELETE FROM image_copies WHERE image_id=?`, id)
			db.Exec(`DELETE FROM images WHERE id=?`, id)
		}
		for _, id := range storageIDs {
			db.Exec(`DELETE FROM image_copies WHERE storage_id=?`, id)
			db.Exec(`DELETE FROM storage_gc WHERE storage_id=?`, id)
			db.Exec(`DELETE FROM operations WHERE storage_id=?`, id)
			db.Exec(`DELETE FROM storages WHERE id=?`, id)
		}
		db.Exec("UPDATE site_settings SET value=? WHERE `key`='default_storage_id'", fmt.Sprint(previousDefault))
	}()
	// A fake S3 endpoint that can be switched off.
	online := true
	objects := map[string][]byte{}
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !online {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body := new(bytes.Buffer)
			body.ReadFrom(r.Body)
			objects[r.URL.Path] = body.Bytes()
		case http.MethodGet:
			body, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(body)
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer s3.Close()
	create := func(st Storage) *Storage {
		t.Helper()
		st.Name += "-" + suffix
		if st.Status == "" {
			st.Status = StatusActive
		}
		if err := s.Save(ctx, &st); err != nil {
			t.Fatal(err)
		}
		storageIDs = append(storageIDs, st.ID)
		loaded, err := s.Get(ctx, st.ID)
		if err != nil {
			t.Fatal(err)
		}
		return loaded
	}
	primary := create(Storage{Name: "primary", Provider: "local", AcceptUploads: true, Config: StorageConfig{Directory: t.TempDir()}})
	backup := create(Storage{Name: "backup", Provider: "local", AutoBackup: true, Config: StorageConfig{Directory: t.TempDir()}})
	cloud := create(Storage{Name: "cloud", Provider: "s3", AutoBackup: true, Config: StorageConfig{Endpoint: s3.URL, Region: "auto", Bucket: "fixture", AccessKey: "a", SecretKey: "b", PathStyle: true}})
	if dup := (Storage{Name: "dup", Provider: "local", Config: StorageConfig{Directory: backup.Config.Directory}, Status: StatusActive}); s.Save(ctx, &dup) == nil {
		storageIDs = append(storageIDs, dup.ID)
		t.Fatal("two storages with the same location were allowed")
	}
	if err := s.SetDefault(ctx, primary.ID); err != nil {
		t.Fatal(err)
	}
	upload := func(name string, st *Storage) *models.Image {
		t.Helper()
		body := []byte(strings.Repeat(name, 300))
		image := &models.Image{PublicName: name + "-" + suffix + ".png", OriginalName: name + ".png", MimeType: "image/png", Ext: ".png", Size: int64(len(body)), CreatedAt: time.Now()}
		if err := s.manager.SaveReader(ctx, s.Options(st), st.Mode(), image, bytes.NewReader(body), image.Size, ""); err != nil {
			t.Fatal(err)
		}
		res, err := db.Exec(`INSERT INTO images (public_name, original_name, mime_type, ext, size, upload_ip) VALUES (?,?,?,?,?, '127.0.0.1')`, image.PublicName, image.OriginalName, image.MimeType, image.Ext, image.Size)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		image.ID = uint(id)
		imageIDs = append(imageIDs, image.ID)
		if err := s.RegisterUpload(ctx, image, st, ChecksumSHA256(body)); err != nil {
			t.Fatal(err)
		}
		return image
	}
	copyState := func(imageID, storageID uint) string {
		var state string
		db.QueryRow(`SELECT state FROM image_copies WHERE image_id=? AND storage_id=?`, imageID, storageID).Scan(&state)
		return state
	}
	drain := func(st *Storage) {
		t.Helper()
		for i := 0; i < 10; i++ {
			db.Exec(`UPDATE image_copies SET next_attempt_at=NULL, lease_until=NULL WHERE storage_id=?`, st.ID)
			db.Exec(`UPDATE storage_gc SET next_attempt_at=NULL WHERE storage_id=?`, st.ID)
			fresh, err := s.Get(ctx, st.ID)
			if err != nil {
				return
			}
			worked, err := s.processCopy(ctx, fresh)
			if err != nil {
				t.Fatal(err)
			}
			if !worked {
				if worked, err = s.processGC(ctx, fresh); err != nil {
					t.Fatal(err)
				}
			}
			if !worked {
				return
			}
		}
	}

	// 1. Upload lands on the primary; both backups are queued and replicated.
	img := upload("alpha", primary)
	if copyState(img.ID, primary.ID) != "ready" || copyState(img.ID, backup.ID) != "pending" || copyState(img.ID, cloud.ID) != "pending" {
		t.Fatal("upload did not queue automatic backups")
	}
	drain(backup)
	drain(cloud)
	if copyState(img.ID, backup.ID) != "ready" || copyState(img.ID, cloud.ID) != "ready" || len(objects) != 1 {
		t.Fatalf("replication failed: backup=%s cloud=%s objects=%d", copyState(img.ID, backup.ID), copyState(img.ID, cloud.ID), len(objects))
	}

	// 2. A deleted local original is detected on read and repaired from a copy.
	path, source, err := s.ServePath(ctx, img)
	if err != nil || source != "local" {
		t.Fatal("serve", err, source)
	}
	os.Remove(path)
	if _, source, err = s.ServePath(ctx, img); err != nil || source != "local" {
		t.Fatal("did not fall back to the backup copy", err, source)
	}
	if copyState(img.ID, primary.ID) != "missing" {
		t.Fatal("missing original not recorded")
	}
	drain(primary)
	if copyState(img.ID, primary.ID) != "ready" {
		t.Fatal("missing original not repaired")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("repaired file absent", err)
	}

	// 3. An offline storage trips its breaker without blocking the others.
	online = false
	img2 := upload("beta", primary)
	for i := 0; i < failureThreshold; i++ {
		drain(cloud)
	}
	if s.health.Available(cloud.ID, nil) {
		t.Fatal("breaker did not open for the offline storage")
	}
	var health string
	db.QueryRow(`SELECT health FROM storages WHERE id=?`, cloud.ID).Scan(&health)
	if health != "offline" {
		t.Fatal("offline state not persisted:", health)
	}
	drain(backup)
	if copyState(img2.ID, backup.ID) != "ready" {
		t.Fatal("healthy storage was blocked by the offline one")
	}

	// 4. Verification detects a damaged copy and repairs it.
	if err := os.WriteFile(s.manager.LocalPath(s.Options(backup), img2StorageKey(t, db, img2.ID, backup.ID)), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	op, err := s.StartVerify(ctx, backup.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	drain(backup)
	if err := s.progressOperations(ctx); err != nil {
		t.Fatal(err)
	}
	var status, message string
	db.QueryRow(`SELECT status, message FROM operations WHERE id=?`, op).Scan(&status, &message)
	if status != "done" || copyState(img2.ID, backup.ID) != "ready" {
		t.Fatalf("verification did not repair: %s %s %s", status, message, copyState(img2.ID, backup.ID))
	}

	// 5. Force removal of an unreachable storage that holds the only copy.
	lonely := create(Storage{Name: "lonely", Provider: "s3", Config: StorageConfig{Endpoint: s3.URL, Region: "auto", Bucket: "lonely", AccessKey: "a", SecretKey: "b", PathStyle: true}})
	db.Exec(`UPDATE storages SET accept_uploads=1 WHERE id=?`, lonely.ID)
	lonelyImage := func() *models.Image {
		online = true
		defer func() { online = false }()
		return upload("gamma", lonely)
	}()
	plan, err := s.RemovalPlan(ctx, lonely.ID)
	if err != nil || plan.OnlyHere != 1 || plan.Reachable != true {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err := s.StartDrain(ctx, primary.ID, backup.ID, false, 0); err == nil {
		t.Fatal("default storage drain allowed")
	}
	lost, err := s.ForceRemove(ctx, lonely.ID, 0)
	if err != nil || lost != 1 {
		t.Fatal("force remove", lost, err)
	}
	var deleted sql.NullTime
	db.QueryRow(`SELECT deleted_at FROM images WHERE id=?`, lonelyImage.ID).Scan(&deleted)
	if !deleted.Valid {
		t.Fatal("image whose only copy was removed is not in the recycle bin")
	}
	if _, err := s.Get(ctx, lonely.ID); err == nil {
		t.Fatal("storage row survived force removal")
	}

	// 6. Drain the offline cloud storage: nothing lives only there, so it is
	// released at once; with deleteObjects the queue would wait for it.
	if _, err := s.StartDrain(ctx, cloud.ID, 0, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, cloud.ID); err == nil {
		t.Fatal("drained storage not removed")
	}

	// 7. Drain the backup with file deletion; its objects are queued and removed.
	backupFile := s.manager.LocalPath(s.Options(backup), img2StorageKey(t, db, img.ID, backup.ID))
	opID, err := s.StartDrain(ctx, backup.ID, primary.ID, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	drain(backup)
	if err := s.progressOperations(ctx); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT status FROM operations WHERE id=?`, opID).Scan(&status)
	if _, err := os.Stat(backupFile); !errors.Is(err, os.ErrNotExist) || status != "done" {
		t.Fatal("drain did not delete files or finish:", status, err)
	}

	// 8. Purging never touches storages; the deletion queue does.
	db.Exec(`UPDATE images SET deleted_at=NOW(), delete_after=NOW() WHERE id=?`, img.ID)
	primaryFile := s.manager.LocalPath(s.Options(primary), img2StorageKey(t, db, img.ID, primary.ID))
	if err := s.PurgeImage(ctx, img.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	db.QueryRow(`SELECT COUNT(*) FROM images WHERE id=?`, img.ID).Scan(&rows)
	if rows != 0 {
		t.Fatal("image row survived purge")
	}
	drain(primary)
	if _, err := os.Stat(primaryFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deletion queue did not remove the file")
	}
	t.Log("replication, read repair, breaker isolation, verification repair, force removal, drain with deletion and queued purge passed")
}

func img2StorageKey(t *testing.T, db *sql.DB, imageID, storageID uint) string {
	t.Helper()
	var key string
	if err := db.QueryRow(`SELECT object_key FROM image_copies WHERE image_id=? AND storage_id=?`, imageID, storageID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}
