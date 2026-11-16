package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"zhuapic/config"
	"zhuapic/internal/testsupport"
	"zhuapic/models"
	"zhuapic/storage"
	"zhuapic/utils"
)

func TestNamedStorageUploadAndQueries(t *testing.T) {
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
	cfg := &config.Config{DBName: "zhuapictest", JWTSecret: "isolated-http-fixture", JWTExpiry: time.Hour, UploadDir: t.TempDir(), CacheDir: t.TempDir(), StorageMode: "local", BaseURL: "http://localhost"}
	manager := storage.NewManager(cfg)
	coordinator := storage.NewCoordinator(db, cfg, manager)
	a := &App{DB: db, Cfg: cfg, Store: manager, Coordinator: coordinator}
	target := storage.Target{Name: "http-fixture", Provider: "local", Enabled: true, Config: storage.TargetConfig{Directory: t.TempDir()}}
	if err = coordinator.SaveTarget(ctx, &target); err != nil {
		t.Fatal(err)
	}
	var originalDefault sql.NullString
	_ = db.QueryRow("SELECT value FROM site_settings WHERE `key`='default_storage_target_id'").Scan(&originalDefault)
	var userID uint
	var selectableID uint
	var policyID int64
	defer func() {
		if userID != 0 {
			_, _ = db.Exec("DELETE r FROM storage_target_replicas r JOIN images i ON i.id=r.image_id WHERE i.user_id=?", userID)
			_, _ = db.Exec("DELETE FROM images WHERE user_id=?", userID)
			_, _ = db.Exec("DELETE FROM upload_usage_daily WHERE subject_type='user' AND subject_key=?", strconv.Itoa(int(userID)))
			_, _ = db.Exec("DELETE FROM users WHERE id=?", userID)
		}
		if policyID != 0 {
			_, _ = db.Exec("DELETE FROM upload_policies WHERE id=?", policyID)
		}
		_, _ = db.Exec("DELETE FROM storage_targets WHERE id=?", target.ID)
		if selectableID != 0 {
			_, _ = db.Exec("DELETE FROM storage_targets WHERE id=?", selectableID)
		}
		if originalDefault.Valid {
			_, _ = db.Exec("UPDATE site_settings SET value=? WHERE `key`='default_storage_target_id'", originalDefault.String)
		} else {
			_, _ = db.Exec("DELETE FROM site_settings WHERE `key`='default_storage_target_id'")
		}
	}()
	res, err := db.Exec("INSERT INTO users (username,email,password,role,active) VALUES ('v04-fixture',?,'not-a-login-password','admin',1)", "fixture-"+strconv.FormatInt(time.Now().UnixNano(), 10)+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	userID = uint(id)
	user := &models.User{ID: userID, Username: "v04-fixture", Role: "admin", Active: true}
	token, err := utils.GenerateToken(cfg, userID, user.Username, "fixture@example.test", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.SetDefaultTarget(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	selectable := storage.Target{Name: "selectable-fixture", Provider: "local", Enabled: true, UploadAccess: "members", Config: storage.TargetConfig{Directory: t.TempDir()}}
	if err := coordinator.SaveTarget(ctx, &selectable); err != nil {
		t.Fatal(err)
	}
	selectableID = selectable.ID
	policyResult, err := db.Exec("INSERT INTO upload_policies (name,slug,storage_mode,allowed_types) VALUES ('isolated-storage-policy',?,?,'svg')", "fixture-"+strconv.FormatInt(time.Now().UnixNano(), 10), storage.TargetSelection(selectable.ID))
	if err != nil {
		t.Fatal(err)
	}
	policyID, _ = policyResult.LastInsertId()
	if err := coordinator.DeleteTarget(ctx, selectable.ID); err == nil {
		t.Fatal("deleted group-referenced storage")
	}
	paused := selectable
	paused.Enabled = false
	if err := coordinator.SaveTarget(ctx, &paused); err == nil {
		t.Fatal("paused group-referenced storage")
	}
	for _, tc := range []struct {
		selection, policy        string
		loggedIn, admin, allowed bool
	}{
		{storage.TargetSelection(selectable.ID), "default", true, false, true},
		{storage.TargetSelection(selectable.ID), "default", false, false, false},
		{storage.TargetSelection(selectable.ID), "local", true, false, false},
		{storage.TargetSelection(selectable.ID), storage.TargetSelection(target.ID), true, false, false},
		{"default", storage.TargetSelection(selectable.ID), true, false, true},
		{"target:0", "default", true, true, false},
	} {
		req := httptest.NewRequest("POST", "http://localhost/api/upload", strings.NewReader("storage_mode="+tc.selection))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		chosen, _, err := a.resolveUploadTarget(req, models.UploadPolicy{Active: true, StorageMode: tc.policy, AllowStorageSelection: tc.policy == "default"}, tc.loggedIn, tc.admin)
		if (err == nil) != tc.allowed {
			t.Fatalf("%+v: %v", tc, err)
		}
		if tc.allowed && chosen.ID != selectable.ID {
			t.Fatal("wrong named target selected")
		}
	}
	options, err := coordinator.UploadTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range options {
		if option.Config.SecretKey != "" || option.Config.Endpoint != "" {
			t.Fatal("upload catalog leaked storage configuration")
		}
	}
	for _, mode := range []string{"", "default", storage.TargetSelection(selectable.ID)} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", "fixture.svg")
		_, _ = part.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20" fill="red"/></svg>`))
		if mode != "" {
			_ = writer.WriteField("storage_mode", mode)
		}
		writer.Close()
		req := httptest.NewRequest(http.MethodPost, "http://localhost/api/upload", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)
		if err = req.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, header, err := req.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
		recorder := httptest.NewRecorder()
		a.createImageFromUpload(recorder, req, header, user, mode != storage.TargetSelection(selectable.ID), 1<<20, models.UploadPolicy{Active: true, AllowStorageSelection: true, StorageMode: "default", MaxFileMB: 1, AllowedTypes: "svg", AllowSVG: true})
		req.MultipartForm.RemoveAll()
		if recorder.Code != 200 {
			t.Fatalf("upload failed: %d %s", recorder.Code, recorder.Body.String())
		}
		var result struct {
			Image struct {
				ID         uint   `json:"id"`
				PublicName string `json:"public_name"`
			} `json:"image"`
		}
		if err = json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var primaryID uint
		expectedID := target.ID
		if mode == storage.TargetSelection(selectable.ID) {
			expectedID = selectable.ID
		}
		if err = db.QueryRow("SELECT target_id FROM storage_target_replicas WHERE image_id=? AND is_primary=1", result.Image.ID).Scan(&primaryID); err != nil || primaryID != expectedID {
			t.Fatal("default target not used", err)
		}
		image, err := a.findImageByPublicName(result.Image.PublicName)
		if err != nil || image.StorageMode != "local" {
			t.Fatal("effective storage query failed", err)
		}
		serve := httptest.NewRecorder()
		a.ServeImage(serve, httptest.NewRequest("GET", "http://localhost/i/"+result.Image.PublicName, nil))
		if serve.Code != 200 || serve.Header().Get("X-Orimg-Storage") != "named" {
			t.Fatal("named image HTTP read failed", serve.Code)
		}
	}
	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/user/dashboard", a.RequireLogin(a.UserDashboard)},
		{"/api/admin/dashboard", a.RequireAdmin(a.AdminDashboard)},
		{"/api/admin/images?storage=local", a.RequireAdmin(a.ListImages)},
	} {
		req := httptest.NewRequest("GET", "http://localhost"+endpoint.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		endpoint.handler(response, req)
		if response.Code != 200 {
			t.Fatalf("%s returned %d", endpoint.path, response.Code)
		}
	}
	t.Log("default target resolved with and without form override; HTTP image reads, effective storage filters and dashboard SQL passed; fixtures removed")
}
