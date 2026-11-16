package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"zhuapic/config"
	"zhuapic/internal/testsupport"
	"zhuapic/storage"
	"zhuapic/utils"
)

func TestGroupProfilesOnApprovedTestDB(t *testing.T) {
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
	db.SetMaxOpenConns(8)
	cfg := &config.Config{JWTSecret: "profile-fixture-only", JWTExpiry: time.Hour, UploadDir: t.TempDir(), CacheDir: t.TempDir()}
	manager := storage.NewManager(cfg)
	a := &App{DB: db, Cfg: cfg, Store: manager, Coordinator: storage.NewCoordinator(db, cfg, manager)}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var uid, gid, pid uint
	var targetIDs []uint
	defer func() {
		db.Exec(`DELETE FROM user_avatars WHERE user_id=?`, uid)
		db.Exec(`DELETE FROM upload_reservations WHERE user_id=?`, uid)
		db.Exec(`DELETE FROM users WHERE id=?`, uid)
		db.Exec(`DELETE FROM user_groups WHERE id=?`, gid)
		db.Exec(`DELETE FROM upload_policies WHERE id=?`, pid)
		for _, id := range targetIDs {
			db.Exec(`DELETE FROM storage_targets WHERE id=?`, id)
		}
		db.Exec(`DELETE FROM audit_logs WHERE actor_user_id=? OR (target_type='user_group' AND target_id=?)`, uid, fmt.Sprint(gid))
	}()
	res, err := db.Exec(`INSERT INTO users(username,email,password,role,active) VALUES(?,?,?,'admin',1)`, "profile-fixture", suffix+"@example.test", "not-a-password")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	uid = uint(id)
	token, _ := utils.GenerateToken(cfg, uid, "profile-fixture", suffix+"@example.test", "admin")
	call := func(method, path, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", fmt.Sprint(gid))
		rr := httptest.NewRecorder()
		handler(rr, req)
		return rr
	}
	user, err := a.findUserByID(uid)
	if err != nil {
		t.Fatal(err)
	}
	if a.uploadPolicyFor(user, true).Active {
		t.Fatal("ungrouped admin gained upload access")
	}
	rr := call("POST", "/api/admin/groups", `{"name":"fixture `+suffix+`","copy_from_group_id":0}`, a.AdminGroups)
	if rr.Code != 201 {
		t.Fatal("empty-policy creation failed", rr.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &created)
	gid = created.ID
	if err = db.QueryRow(`SELECT policy_id FROM user_groups WHERE id=?`, gid).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET group_id=?,quota_bytes=100 WHERE id=?`, gid, uid); err != nil {
		t.Fatal(err)
	}
	// Existing formerly built-in rows must be deletable without deleting their users.
	if _, err = db.Exec(`UPDATE user_groups SET is_system=1 WHERE id=?`, gid); err != nil {
		t.Fatal(err)
	}
	user, err = a.findUserByID(uid)
	if err != nil {
		t.Fatal(err)
	}
	policy := a.uploadPolicyFor(user, true)
	if !policy.Active || policy.TotalQuotaBytes != 100 || policy.AllowStorageSelection {
		t.Fatalf("incorrect effective policy: %+v", policy)
	}
	for _, access := range []string{"admin", "members"} {
		target := storage.Target{Name: "profile-fixture-" + access, Provider: "local", Enabled: true, UploadAccess: access, Config: storage.TargetConfig{Directory: t.TempDir()}}
		if err := a.Coordinator.SaveTarget(context.Background(), &target); err != nil {
			t.Fatal(err)
		}
		targetIDs = append(targetIDs, target.ID)
	}
	for _, allow := range []bool{false, true} {
		body := fmt.Sprintf(`{"max_file_mb":50,"max_batch_count":20,"storage_mode":"target:%d","allow_storage_selection":%t,"allowed_types":"png,jpg","active":true}`, targetIDs[0], allow)
		req := httptest.NewRequest("PUT", "/api/admin/policies/"+fmt.Sprint(pid), strings.NewReader(body))
		req.SetPathValue("id", fmt.Sprint(pid))
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		a.UpdateUploadPolicy(rr, req)
		if rr.Code != 200 {
			t.Fatal("policy update failed", rr.Body.String())
		}
		policy = a.uploadPolicyFor(user, true)
		if policy.AllowStorageSelection != allow || policy.StorageMode != storage.TargetSelection(targetIDs[0]) {
			t.Fatal("policy storage settings not persisted")
		}
		rr = call("GET", "/api/storage/options", "", a.UploadTargets)
		if rr.Code != 200 {
			t.Fatal(rr.Body.String())
		}
		var options struct {
			Targets []struct {
				ID      uint `json:"id"`
				Default bool `json:"is_default"`
			} `json:"targets"`
		}
		json.Unmarshal(rr.Body.Bytes(), &options)
		if !allow && (len(options.Targets) != 1 || options.Targets[0].ID != targetIDs[0] || !options.Targets[0].Default) {
			t.Fatal("admin bypassed group default", rr.Body.String())
		}
		for _, admin := range []bool{false, true} {
			req = httptest.NewRequest("POST", "/api/upload", strings.NewReader("storage_mode=default"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			chosen, _, err := a.resolveUploadTarget(req, policy, true, admin)
			if err != nil || chosen.ID != targetIDs[0] {
				t.Fatal("group default not used", err)
			}
			req = httptest.NewRequest("POST", "/api/upload", strings.NewReader("storage_mode="+storage.TargetSelection(targetIDs[1])))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			_, _, err = a.resolveUploadTarget(req, policy, true, admin)
			if (err == nil) != allow {
				t.Fatal("group storage gate bypassed", err)
			}
		}
	}
	for _, allow := range []bool{false, true} {
		policy.AllowStorageSelection = allow
		for _, admin := range []bool{false, true} {
			target := &storage.Target{ID: 91, Enabled: true, Configured: true, UploadAccess: "members"}
			if storage.CanSelectTarget(target, 90, "target:90", true, admin, allow) != allow {
				t.Fatal("selection gate bypassed")
			}
		}
	}
	policy.AllowStorageSelection = false
	var wg sync.WaitGroup
	releases := make(chan func(), 4)
	blocked := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := a.reserveUploadQuota(context.Background(), user, policy, 60)
			if err != nil {
				blocked <- err
			} else {
				releases <- release
			}
		}()
	}
	wg.Wait()
	close(releases)
	close(blocked)
	if len(releases) != 1 || len(blocked) != 3 {
		t.Errorf("quota race: allowed=%d blocked=%d", len(releases), len(blocked))
	}
	for release := range releases {
		release()
	}
	release, err := a.reserveUploadQuota(context.Background(), user, policy, 100)
	if err != nil {
		t.Fatal("released quota not available", err)
	}
	release()
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("avatar", "avatar.png")
	part.Write(img.Bytes())
	writer.Close()
	req := httptest.NewRequest("POST", "/api/me/avatar", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	a.UploadAvatar(rr, req)
	if rr.Code != 200 {
		t.Fatal("avatar upload failed", rr.Body.String())
	}
	user, err = a.findUserByID(uid)
	if err != nil || !strings.HasPrefix(user.AvatarURL, "/avatars/") {
		t.Fatal("avatar not attached")
	}
	req = httptest.NewRequest("GET", user.AvatarURL, nil)
	req.SetPathValue("name", strings.TrimPrefix(user.AvatarURL, "/avatars/"))
	rr = httptest.NewRecorder()
	a.ServeAvatar(rr, req)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "image/png" {
		t.Fatal("avatar serving failed")
	}
	rr = call("DELETE", "/api/me/avatar", "", a.DeleteAvatar)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	rr = call("GET", "/api/admin/groups", "", a.AdminGroups)
	if rr.Code != 200 {
		t.Fatal("group SQL failed", rr.Body.String())
	}
	rr = call("GET", "/api/admin/users", "", a.AdminUsers)
	if rr.Code != 200 {
		t.Fatal("user SQL failed", rr.Body.String())
	}
	rr = call("DELETE", "/api/admin/groups/"+fmt.Sprint(gid), "", a.AdminGroup)
	if rr.Code != 200 {
		t.Fatal("built-in/member group deletion failed", rr.Body.String())
	}
	user, err = a.findUserByID(uid)
	if err != nil || user.GroupID != 0 || user.Role != "admin" || !user.Active {
		t.Fatal("group deletion damaged account")
	}
	if a.uploadPolicyFor(user, true).Active {
		t.Fatal("deleted group regained upload access")
	}
	t.Log("group lifecycle, fail-closed upload, per-user quota, concurrent reservations, avatar upload/read/delete and admin SQL verified; only temporary fixtures changed")
}
