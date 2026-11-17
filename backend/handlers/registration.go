package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
	"zhuapic/mailer"
	"zhuapic/utils"
)

type registrationSettings struct {
	Enabled bool `json:"enabled"`
	GroupID uint `json:"group_id"`
}

func (a *App) RegistrationSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var settings registrationSettings
	if r.Method == "GET" {
		if a.DB.QueryRowContext(r.Context(), `SELECT enabled,group_id FROM registration_settings WHERE id=1`).Scan(&settings.Enabled, &settings.GroupID) != nil {
			utils.WriteError(w, 500, "读取注册设置失败")
			return
		}
	} else {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&settings) != nil {
			utils.WriteError(w, 400, "设置无效")
			return
		}
		if settings.Enabled {
			c, _, e := mailer.New(a.DB, a.Cfg.JWTSecret).Config(r.Context())
			if e != nil || !c.Enabled || a.Cfg.DisableBackgroundWork {
				utils.WriteError(w, 409, "请先启用正式发信")
				return
			}
		}
		tx, e := a.DB.BeginTx(r.Context(), nil)
		if e != nil {
			utils.WriteError(w, 500, "保存失败")
			return
		}
		defer tx.Rollback()
		if settings.GroupID > 0 || settings.Enabled {
			var active bool
			if tx.QueryRowContext(r.Context(), `SELECT g.active FROM user_groups g WHERE g.id=? FOR UPDATE`, settings.GroupID).Scan(&active) != nil || settings.Enabled && !active {
				utils.WriteError(w, 400, "请选择启用的用户组")
				return
			}
		}
		_, e = tx.ExecContext(r.Context(), `UPDATE registration_settings SET enabled=?,group_id=? WHERE id=1`, settings.Enabled, settings.GroupID)
		if e != nil || tx.Commit() != nil {
			utils.WriteError(w, 500, "保存失败")
			return
		}
		actor, _ := a.currentUser(r)
		a.writeAudit(r, actor, "mail.registration_updated", "mail", "registration", nil)
	}
	utils.WriteJSON(w, 200, settings)
}

func (a *App) RequestRegistration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email         string `json:"email"`
		CaptchaToken  string `json:"captcha_token"`
		CaptchaAnswer string `json:"captcha_answer"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		utils.WriteError(w, 400, "请求无效")
		return
	}
	email, ok := cleanEmail(req.Email)
	if !ok {
		utils.WriteError(w, 400, "邮箱格式无效")
		return
	}
	s := mailer.New(a.DB, a.Cfg.JWTSecret)
	if !s.Allow(r.Context(), "registration-ip:"+a.clientIP(r), 5, 600) || !s.Allow(r.Context(), "request-global", 100, 60) {
		utils.WriteError(w, 429, "请求过于频繁，请稍后再试")
		return
	}
	if a.verifyCaptcha(req.CaptchaToken, req.CaptchaAnswer) != nil {
		utils.WriteError(w, 400, "验证码不正确或已过期")
		return
	}
	if !s.Allow(r.Context(), "registration-captcha:"+req.CaptchaToken, 1, 600) {
		utils.WriteError(w, 400, "请刷新验证码")
		return
	}
	c, _, err := s.Config(r.Context())
	if err != nil || !c.Enabled || a.Cfg.DisableBackgroundWork {
		utils.WriteError(w, 503, "邮件注册暂不可用")
		return
	}
	var group uint
	if a.DB.QueryRowContext(r.Context(), `SELECT s.group_id FROM registration_settings s JOIN user_groups g ON g.id=s.group_id WHERE s.id=1 AND s.enabled=1 AND g.active=1`).Scan(&group) != nil {
		utils.WriteError(w, 403, "站点暂未开放注册")
		return
	}
	// Always return the same response for an existing address and a newly queued request.
	start := time.Now()
	defer func() {
		if wait := 300*time.Millisecond - time.Since(start); wait > 0 {
			time.Sleep(wait)
		}
	}()
	generic := func() {
		utils.WriteJSON(w, 202, map[string]string{"message": "如该邮箱可以注册，验证邮件将发送至您的邮箱。请在邮件中继续创建账号。"})
	}
	if !s.Allow(r.Context(), "recipient:"+email, 1, 60) || !s.Allow(r.Context(), "recipient-hour:"+email, 5, 3600) {
		generic()
		return
	}
	var count int
	if a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE email=?`, email).Scan(&count) != nil {
		utils.WriteError(w, 503, "注册暂不可用")
		return
	}
	if count > 0 {
		generic()
		return
	}
	token, err := utils.RandomHex(32)
	if err != nil {
		utils.WriteError(w, 500, "请求创建失败")
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, 500, "请求创建失败")
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO registration_requests(email,token_hash,group_id,expires_at) VALUES(?,?,?,DATE_ADD(NOW(),INTERVAL 24 HOUR)) ON DUPLICATE KEY UPDATE token_hash=VALUES(token_hash),group_id=VALUES(group_id),expires_at=VALUES(expires_at),consumed_at=NULL,created_at=NOW()`, email, mailer.Hash(token), group)
	if err == nil {
		_, err = s.QueueTx(r.Context(), tx, c, "register", mailer.Message{To: email, Body: "欢迎加入。验证您的邮箱后，即可设置用户名和密码，开启自己的图片空间。", ActionURL: c.BaseURL + "/account-action#register:" + token, ValidFor: "24 小时"}, mailer.Hash(token), time.Now().Add(24*time.Hour))
	}
	if err != nil || tx.Commit() != nil {
		utils.WriteError(w, 503, "邮件暂时无法入队")
		return
	}
	generic()
}

func (a *App) ConfirmRegistration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || len(req.Token) != 64 {
		utils.WriteError(w, 400, "验证链接无效")
		return
	}
	name, ok := cleanUsername(req.Username)
	if !ok || len(req.Password) < 10 || len(req.Password) > 128 {
		utils.WriteError(w, 400, "请填写有效用户名，密码需要 10 至 128 位")
		return
	}
	s := mailer.New(a.DB, a.Cfg.JWTSecret)
	if !s.Allow(r.Context(), "registration-confirm:"+a.clientIP(r), 10, 600) {
		utils.WriteError(w, 429, "请求过于频繁")
		return
	}
	password, err := utils.HashPassword(req.Password)
	if err != nil {
		utils.WriteError(w, 500, "账号创建失败")
		return
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		utils.WriteError(w, 500, "账号创建失败")
		return
	}
	defer tx.Rollback()
	// Lock the selected group before settings, matching policy editing/deletion order.
	var group uint
	if tx.QueryRowContext(ctx, `SELECT group_id FROM registration_requests WHERE token_hash=?`, mailer.Hash(req.Token)).Scan(&group) != nil {
		utils.WriteError(w, 400, "链接已失效或已使用")
		return
	}
	var active bool
	if tx.QueryRowContext(ctx, `SELECT g.active FROM user_groups g WHERE g.id=? FOR UPDATE`, group).Scan(&active) != nil || !active {
		utils.WriteError(w, 409, "注册用户组已不可用，请联系管理员")
		return
	}
	var enabled bool
	var currentGroup uint
	if tx.QueryRowContext(ctx, `SELECT enabled,group_id FROM registration_settings WHERE id=1 FOR UPDATE`).Scan(&enabled, &currentGroup) != nil || !enabled || group != currentGroup {
		utils.WriteError(w, 409, "注册设置已变化，请重新申请")
		return
	}
	var email string
	err = tx.QueryRowContext(ctx, `SELECT email FROM registration_requests WHERE token_hash=? AND consumed_at IS NULL AND expires_at>NOW() FOR UPDATE`, mailer.Hash(req.Token)).Scan(&email)
	if err == sql.ErrNoRows {
		utils.WriteError(w, 400, "链接已失效或已使用")
		return
	}
	if err != nil {
		utils.WriteError(w, 500, "验证失败")
		return
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users(username,email,email_verified,password,role,group_id,active) VALUES(?,?,1,?,'user',?,1)`, name, email, password, group)
	if err != nil {
		utils.WriteError(w, 409, "账号无法创建，该邮箱可能已有账号，请尝试登录或找回密码")
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE registration_requests SET consumed_at=NOW() WHERE token_hash=?`, mailer.Hash(req.Token)); err != nil || tx.Commit() != nil {
		utils.WriteError(w, 500, "账号创建失败")
		return
	}
	id, _ := res.LastInsertId()
	utils.WriteJSON(w, 201, map[string]any{"created": true, "requires_login": true, "id": id})
}
