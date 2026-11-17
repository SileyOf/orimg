package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"zhuapic/mailer"
	"zhuapic/utils"
)

// Called only after TestMailOnApprovedTestDB proves every mail table is unused.
func testRegistrationFlow(t *testing.T, a *App, s *mailer.Service) {
	t.Helper()
	db := a.DB
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	email := "register-" + suffix + "@example.test"
	res, err := db.Exec(`INSERT INTO user_groups(name,slug,is_system,allowed_types) VALUES(?,?,0,'jpg,png')`, "fixture-"+suffix, "fixture-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	group, _ := res.LastInsertId()
	defer db.Exec(`DELETE FROM user_groups WHERE id=?`, group)
	defer db.Exec(`DELETE FROM users WHERE email=?`, email)
	defer db.Exec(`DELETE FROM registration_requests WHERE email=?`, email)
	call := func(handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest("POST", "/fixture", strings.NewReader(string(b))))
		return rr
	}
	request := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		a.Captcha(rr, httptest.NewRequest("GET", "/api/auth/captcha", nil))
		var c struct{ Token, Question string }
		if json.Unmarshal(rr.Body.Bytes(), &c) != nil {
			t.Fatal("captcha decode")
		}
		var x, y int
		fmt.Sscanf(c.Question, "%d + %d = ?", &x, &y)
		return call(a.RequestRegistration, map[string]any{"email": email, "captcha_token": c.Token, "captcha_answer": strconv.Itoa(x + y)})
	}
	if rr := request(); rr.Code != 403 {
		t.Fatal("registration must default closed", rr.Code, rr.Body.String())
	}
	if rr := call(a.RegistrationSettings, map[string]any{"enabled": true, "group_id": group}); rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if rr := request(); rr.Code != 202 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE email=?`, email).Scan(&count)
	if count != 0 {
		t.Fatal("unverified account created")
	}
	var encrypted string
	if err := db.QueryRow(`SELECT payload FROM mail_outbox WHERE kind='register' ORDER BY id DESC LIMIT 1`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := utils.DecryptSecret(encrypted, a.Cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	var m mailer.Message
	json.Unmarshal([]byte(plain), &m)
	if len(m.Image) == 0 || !strings.Contains(m.HTML, "cid:origin-logo") {
		t.Fatal("registration missing HTML and image")
	}
	token := strings.TrimPrefix(m.ActionURL, "https://example.test/account-action#register:")
	if len(token) != 64 {
		t.Fatal("registration missing token")
	}
	body := map[string]any{"token": token, "username": "RegistrationFixture", "password": "fixture-password-long", "role": "admin", "group_id": 9999}
	db.Exec(`UPDATE registration_settings SET enabled=0 WHERE id=1`)
	if rr := call(a.ConfirmRegistration, body); rr.Code != 409 {
		t.Fatal("closed registration accepted", rr.Code)
	}
	db.Exec(`UPDATE registration_settings SET enabled=1 WHERE id=1`)
	db.Exec(`UPDATE user_groups SET active=0 WHERE id=?`, group)
	if rr := call(a.ConfirmRegistration, body); rr.Code != 409 {
		t.Fatal("inactive group accepted", rr.Code)
	}
	db.Exec(`UPDATE user_groups SET active=1 WHERE id=?`, group)
	if rr := call(a.ConfirmRegistration, body); rr.Code != 201 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var actualGroup int64
	var role, password string
	var verified bool
	if err = db.QueryRow(`SELECT group_id,role,email_verified,password FROM users WHERE email=?`, email).Scan(&actualGroup, &role, &verified, &password); err != nil || actualGroup != group || role != "user" || !verified || !utils.VerifyPassword(password, "fixture-password-long") {
		t.Fatal("registration authorization mismatch", err, actualGroup, role, verified)
	}
	if rr := call(a.ConfirmRegistration, body); rr.Code != 400 {
		t.Fatal("registration replay accepted", rr.Code)
	}
	// A consumed registration challenge cannot be sent again by the queue worker.
	db.Exec(`UPDATE mail_outbox SET status='cancelled',payload='' WHERE kind<>'register'`)
	if err = s.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	db.QueryRow(`SELECT status FROM mail_outbox WHERE kind='register' ORDER BY id DESC LIMIT 1`).Scan(&status)
	if status != "cancelled" {
		t.Fatal("consumed registration mail not cancelled", status)
	}
	t.Log("PASS default-closed registration, no pre-verification account, enabled group binding, privilege escalation rejection, closed/paused policy guards, replay prevention, inline HTML image")
}
