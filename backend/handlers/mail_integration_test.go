package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"zhuapic/config"
	"zhuapic/internal/testsupport"
	"zhuapic/mailer"
	"zhuapic/utils"
)

func TestMailOnApprovedTestDB(t *testing.T) {
	dsn := os.Getenv("ORIMG_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated database required")
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
	for _, table := range []string{"mail_settings", "mail_outbox", "account_challenges", "mail_limits", "registration_requests", "mail_templates"} {
		var n int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("mail fixtures require unused mail tables", table, n, err)
		}
	}
	var initialRegistration registrationSettings
	if err = db.QueryRow(`SELECT enabled,group_id FROM registration_settings WHERE id=1`).Scan(&initialRegistration.Enabled, &initialRegistration.GroupID); err != nil || initialRegistration.Enabled || initialRegistration.GroupID != 0 {
		t.Fatal("registration fixtures require unused registration settings", err)
	}
	var uid uint
	defer func() {
		for _, table := range []string{"mail_settings", "mail_outbox", "account_challenges", "mail_limits"} {
			db.Exec("DELETE FROM " + table)
		}
		db.Exec(`UPDATE registration_settings SET enabled=0,group_id=0 WHERE id=1`)
		// This action was introduced by these fixtures; httptest uses the reserved TEST-NET address.
		db.Exec(`DELETE FROM audit_logs WHERE action='mail.registration_updated' AND actor_user_id IS NULL AND target_type='mail' AND target_id='registration' AND ip='192.0.2.1' AND user_agent=''`)
		db.Exec("DELETE FROM api_tokens WHERE user_id=?", uid)
		db.Exec("DELETE FROM audit_logs WHERE actor_user_id=?", uid)
		db.Exec("DELETE FROM users WHERE id=?", uid)
	}()
	ctx := context.Background()
	cfg := &config.Config{JWTSecret: "mail-fixture-only", JWTExpiry: time.Hour}
	app := &App{DB: db, Cfg: cfg}
	service := mailer.New(db, cfg.JWTSecret)
	settings := mailer.Config{Host: "smtp.example.test", Port: 465, TLS: "tls", From: "sender@example.test", Name: "Fixture", BaseURL: "https://example.test", Password: "fixture-smtp-secret"}
	settings.Enabled = true
	if service.Save(ctx, settings) == nil {
		t.Fatal("enabled without test")
	}
	settings.Enabled = false
	if err = service.Save(ctx, settings); err != nil {
		t.Fatal(err)
	}
	var raw string
	db.QueryRow("SELECT config_json FROM mail_settings WHERE id=1").Scan(&raw)
	if strings.Contains(raw, settings.Password) {
		t.Fatal("SMTP secret stored plaintext")
	}
	sends := 0
	fail := false
	service.Deliver = func(_ context.Context, _ mailer.Config, _ string, m mailer.Message) error {
		if !strings.HasSuffix(m.To, "@example.test") {
			t.Fatal("unexpected recipient")
		}
		sends++
		if fail {
			return fmt.Errorf("fake temporary failure")
		}
		return nil
	}
	id, err := service.Test(ctx, "test@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var status, payload string
	db.QueryRow("SELECT status,payload FROM mail_outbox WHERE id=?", id).Scan(&status, &payload)
	if status != "accepted" || payload != "" || sends != 1 {
		t.Fatal("mail not accepted and scrubbed", status, sends)
	}
	settings.Enabled = true
	if err = service.Save(ctx, settings); err != nil {
		t.Fatal(err)
	}
	hash, _ := utils.HashPassword("old-password-fixture")
	email := fmt.Sprintf("mail-%d@example.test", time.Now().UnixNano())
	res, err := db.Exec(`INSERT INTO users(username,email,password,role,active) VALUES('mail-fixture',?,?,'user',1)`, email, hash)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := res.LastInsertId()
	uid = uint(userID)
	token, _ := utils.GenerateToken(cfg, uid, "mail-fixture", email, "user")
	call := func(purpose string, body any, confirm bool) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/auth/mail/"+purpose, strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.SetPathValue("purpose", purpose)
		rr := httptest.NewRecorder()
		if confirm {
			app.ConfirmAccountMail(rr, req)
		} else {
			app.RequestAccountMail(rr, req)
		}
		return rr
	}
	clearLimits := func() { db.Exec("DELETE FROM mail_limits") }
	getToken := func(purpose string) string {
		var encrypted string
		if err := db.QueryRow("SELECT payload FROM mail_outbox WHERE kind=? ORDER BY id DESC LIMIT 1", purpose).Scan(&encrypted); err != nil {
			t.Fatal(err)
		}
		plain, err := utils.DecryptSecret(encrypted, cfg.JWTSecret)
		if err != nil {
			t.Fatal(err)
		}
		var m mailer.Message
		json.Unmarshal([]byte(plain), &m)
		parts := strings.Split(m.Body, "#"+purpose+":")
		if len(parts) != 2 {
			t.Fatal("missing fragment token")
		}
		return strings.Split(parts[1], "\n")[0]
	}
	rr := call("verify", map[string]string{}, false)
	if rr.Code != 202 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	verify := getToken("verify")
	rr = call("verify", map[string]string{"purpose": "verify", "token": verify}, true)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if rr = call("verify", map[string]string{"purpose": "verify", "token": verify}, true); rr.Code != 400 {
		t.Fatal("replay accepted")
	}
	clearLimits()
	newEmail := "new-" + email
	rr = call("change", map[string]string{"email": newEmail, "current_password": "old-password-fixture"}, false)
	if rr.Code != 202 {
		t.Fatal(rr.Body.String())
	}
	var storedEmail string
	db.QueryRow("SELECT email FROM users WHERE id=?", uid).Scan(&storedEmail)
	if storedEmail != email {
		t.Fatal("email changed before verification")
	}
	change := getToken("change")
	rr = call("change", map[string]string{"purpose": "change", "token": change}, true)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	request := httptest.NewRequest("GET", "/api/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if _, ok := app.currentUser(request); ok {
		t.Fatal("old session survived email change")
	}
	clearLimits()
	rr = call("reset", map[string]string{"email": newEmail}, false)
	if rr.Code != 202 {
		t.Fatal(rr.Body.String())
	}
	reset := getToken("reset")
	rr = call("reset", map[string]string{"purpose": "reset", "token": reset, "password": "new-password-fixture"}, true)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if rr = call("reset", map[string]string{"purpose": "reset", "token": reset, "password": "other-password-fixture"}, true); rr.Code != 400 {
		t.Fatal("password reset replay accepted")
	}
	user, err := app.findUserByID(uid)
	if err != nil || !utils.VerifyPassword(user.Password, "new-password-fixture") || user.AuthVersion != 2 {
		t.Fatal("reset did not update credential version", err)
	}
	clearLimits()
	unknown := call("reset", map[string]string{"email": "absent@example.test"}, false)
	clearLimits()
	known := call("reset", map[string]string{"email": newEmail}, false)
	if unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Fatal("account existence disclosed")
	}
	testRegistrationFlow(t, app, service)
	// Only fake deliveries run; no real SMTP, storage workers, or production settings are touched.
	db.Exec("UPDATE mail_outbox SET status='cancelled',payload='' WHERE kind<>'test'")
	fail = true
	id, err = service.Test(ctx, "retry@example.test")
	if err != nil {
		t.Fatal(err)
	}
	clearLimits()
	for i := 0; i < 5; i++ {
		db.Exec("UPDATE mail_outbox SET next_attempt_at=NOW() WHERE id=?", id)
		if err = service.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	db.QueryRow("SELECT status FROM mail_outbox WHERE id=?", id).Scan(&status)
	if status != "failed" {
		t.Fatal("retry limit", status)
	}
	if !service.Allow(ctx, "fixture-limit", 1, 60) || service.Allow(ctx, "fixture-limit", 1, 60) {
		t.Fatal("rate limit failed")
	}
	response := httptest.NewRecorder()
	app.MailConfig(response, httptest.NewRequest(http.MethodGet, "/api/admin/mail/config", nil))
	if strings.Contains(response.Body.String(), "fixture-smtp-secret") {
		t.Fatal("credential exposed")
	}
	t.Log("PASS encrypted config/outbox, test-before-enable, verification, email change, reset, replay rejection, session invalidation, enumeration resistance, bounded retry, redaction; fake delivery only")
}
